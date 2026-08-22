// Compiling z.* chains to the refined sets they denote. Recognition
// (chain_roots.go) is symbol resolution, never name matching; literal
// arguments (chain_args.go) and the iso datetime window
// (iso_datetime_window.go, refinementsets) are asked from here. Root
// constructors, chain methods, and case stability live beside this
// file. A chain that roots in the surface but falls outside the
// readable forms is unsupported loudly — the statement cannot be
// honored, so it is never silently dropped.
//
// Ported 1:1 from annotations/schema_chain_compiler.ts. Types landed
// ahead of the directory (see the file's original header, preserved
// in git history); this pass completes it with compileAnnotation and
// libraryAdapterTotal.

package annotations

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations/libraryadapters"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations/libraryadapters/zod"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

type Unsupported struct {
	Unsupported string
	At          *ast.Node
}

// Annotation is what one z.* chain states.
type Annotation struct {
	Set *refinementsets.RefinedSet
	// Refine is the `.refine` predicate riding an UNREAD statement —
	// carried so a judged position holding an EXACT value can run the
	// predicate itself where the compiled set could not speak for it.
	Refine   *ast.Node
	Temporal *refinementsets.TemporalAnnotation
	// LibraryAdapter is the adapter id when the chain roots in a
	// third-party schema package rather than the checker's own
	// surface — carried so the chain reader can apply the adapter's
	// vocabulary and quirks, and so a statement the checker cannot
	// honor stays silent rather than refusing.
	LibraryAdapter string
	// Absent is true when the statement ALSO admits the absent value
	// — a union with a null or undefined member. Absence is not a set
	// member (it leaves ℝ̄), so it rides beside the set the way the
	// maybe wrapper rides beside an AbstractValue.
	Absent bool
	// Promise is true for `z.promise(inner)`: the set is what the
	// RESOLVED value wears, and the parse result itself is a promise
	// OBJECT, which never wears the set. Only an await reads through.
	Promise bool
	// Date is true for `z.date()` chains: the set describes the TIME
	// VALUE (epoch milliseconds) of the Date the parse hands back,
	// not the Date object itself.
	Date bool
	// KindTag is the sort the statement's values wear when it is not
	// the double: "bigint" | "symbol" | "boolean" | "".
	KindTag string
	// Passthrough is true for validation-only schemas whose output
	// VALUES equal the input's exactly (`z.json()`). An exact
	// argument keeps its exact value knowledge; reference identity is
	// never claimed.
	Passthrough bool
	// Measures are sequence MEASURES the statement carries beside the
	// element sets: facts BETWEEN elements, which no per-position
	// form can state.
	Measures *Measures
	// Unread is true when a `.refine` body the reader could not parse
	// rides the chain: the parse checks MORE than the set says, so a
	// position checked against it alerts instead of accepting.
	Unread bool
	// Word is the surface's OWN WORD for the statement — "uuid",
	// "e164" — carried so hovers speak it instead of the compiled
	// grammar's algebra.
	Word *WordSpelling
}

// Compiled is an Annotation or an Unsupported; exactly one side is
// non-nil (the TS union, spread as a pair).
type Compiled struct {
	Annotation  *Annotation
	Unsupported *Unsupported
}

func IsUnsupported(c Compiled) bool {
	return c.Unsupported != nil
}

type AnnotationRegistry = map[*ast.Symbol]*Annotation

// libraryAdapterTotal is libraryAdapterTotal in the TS source: TOTAL
// coverage at a libraryAdapter boundary: a schema-producing spelling
// the rows refuse compiles as the widest sound claim marked unread --
// checked positions alert, nothing goes silent. Only the
// libraryAdapter's RUNTIME vocabulary (parse, encode, the registries)
// stays a plain value.
func libraryAdapterTotal(compiled Compiled, adapter *libraryadapters.LibraryAdapter, kind string, name string) Compiled {
	if adapter == nil || !IsUnsupported(compiled) {
		return compiled
	}
	var runtime map[string]bool
	if kind == "root" {
		runtime = adapter.RuntimeRoots
	} else {
		runtime = adapter.RuntimeMethods
	}
	if runtime != nil && runtime[name] {
		return compiled
	}
	return Compiled{Annotation: &Annotation{Set: setPtr(refinementsets.MakeRefinedSet()), Unread: true, LibraryAdapter: adapter.Name}}
}

// CompileAnnotation is compileAnnotation in the TS source: compile a
// z.* chain (or a reference to an annotation constant) to the
// refined set it denotes.
func CompileAnnotation(p *program.CheckerProgram, expr *ast.Node, registry AnnotationRegistry) Compiled {
	e := expr
	if ast.IsParenthesizedExpression(e) {
		e = e.AsParenthesizedExpression().Expression
	}

	if ast.IsIdentifier(e) {
		symbol := symbolAt(p.Checker, e)
		var known *Annotation
		if symbol != nil {
			known = registry[symbol]
		}
		if known != nil {
			return Compiled{Annotation: known}
		}
		return Compiled{Unsupported: &Unsupported{
			Unsupported: "'" + e.AsIdentifier().Text + "' is not a stated annotation",
			At:          e,
		}}
	}

	if !ast.IsCallExpression(e) || !ast.IsPropertyAccessExpression(e.AsCallExpression().Expression) {
		return Compiled{Unsupported: &Unsupported{Unsupported: "not a recognized z.* chain", At: e}}
	}
	access := e.AsCallExpression().Expression.AsPropertyAccessExpression()
	receiver := access.Expression
	method := access.Name().Text()
	args := e.AsCallExpression().Arguments.Nodes

	// `.pipe(target)` VALIDATES its output against the target
	// schema, whatever precedes -- the output set is the target's
	// outright, so an unreadable prefix (a transform) does not
	// matter
	if method == "pipe" && len(args) == 1 {
		root := chainRoot(e)
		var adapter *libraryadapters.LibraryAdapter
		if root != nil {
			adapter = libraryAdapterOfNode(p, root)
		}
		if adapter != nil {
			target := CompileAnnotation(p, args[0], registry)
			if !IsUnsupported(target) {
				withAdapter := *target.Annotation
				withAdapter.LibraryAdapter = adapter.Name
				return Compiled{Annotation: &withAdapter}
			}
		}
	}

	// `.keyof()` on an object schema: zod's own keyof() is
	// _enum(Object.keys(shape)) (vendored schemas.ts:1530), so the
	// set is exactly the declared key strings -- read syntactically
	// off the shape literal, the same names zod enumerates. The
	// libraryAdapter rides from the SHAPE's own root, because the
	// chain here roots in a schema constant.
	if method == "keyof" && len(args) == 0 {
		shape := shapeOf(p, receiver)
		if shape.Ok && len(shape.Names) > 0 {
			set := refinementsets.StringTuple(shape.Names[0])
			for _, name := range shape.Names[1:] {
				set = refinementsets.MakeRefinedSet(refinementsets.Union(set, refinementsets.StringTuple(name)))
			}
			adapter := libraryAdapterOfNode(p, shape.Root)
			if adapter == nil {
				return Compiled{Annotation: &Annotation{Set: setPtr(set)}}
			}
			return Compiled{Annotation: &Annotation{Set: setPtr(set), LibraryAdapter: adapter.Name}}
		}
		return Compiled{Unsupported: &Unsupported{
			Unsupported: ".keyof() needs an object schema whose keys are in view",
			At:          e,
		}}
	}

	// `z.iso.datetime()` / `z.iso.date()` / `z.iso.time()` -- string
	// format schemas whose grammars are zod's own regexes (vendored
	// core/regexes.ts:96-132), transcribed at their DEFAULT
	// arguments (no precision, no offset, no local => the Z suffix),
	// compiled through the same format-grammar reader `.regex` uses
	if ast.IsPropertyAccessExpression(receiver) &&
		ast.IsIdentifier(receiver.AsPropertyAccessExpression().Expression) &&
		receiver.AsPropertyAccessExpression().Name().Text() == "iso" &&
		(method == "datetime" || method == "date" || method == "time" || method == "duration") {
		adapter := libraryAdapterOfNode(p, receiver.AsPropertyAccessExpression().Expression)
		if adapter != nil {
			// duration ($ZodISODurationDef) reads through the def
			// vocabulary's own row; an ARGFUL spelling (precision,
			// offset, local) re-shapes the grammar, so the value is
			// a string and the rest is unread -- never silent
			if method == "duration" || len(args) != 0 {
				if method == "duration" && len(args) == 0 && adapter.DefRoots != nil {
					if defRoot, ok := adapter.DefRoots["duration"]; ok {
						read := defRoot(zod.DefReadContext{
							P:        p,
							At:       e,
							Args:     args,
							Registry: toLibraryAdapterRegistry(registry),
							Compile:  libraryAdapterCompile(func(inner *ast.Node) Compiled { return CompileAnnotation(p, inner, registry) }),
						})
						if read != nil {
							converted := fromLibraryAdapterCompiled(*read)
							if !IsUnsupported(*converted) {
								withMeta := *converted.Annotation
								withMeta.LibraryAdapter = adapter.Name
								withMeta.Word = &WordSpelling{Text: "iso duration", Covers: len(withMeta.Set.Forms)}
								return Compiled{Annotation: &withMeta}
							}
						}
					}
				}
				return Compiled{Annotation: &Annotation{
					Set:            setPtr(refinementsets.Strings),
					Unread:         true,
					LibraryAdapter: adapter.Name,
					Word:           &WordSpelling{Text: "iso " + method, Covers: len(refinementsets.Strings.Forms)},
				}}
			}
			pattern := refinementsets.IsoPatterns[method]
			compiled := refinementsets.FormatGrammar(pattern, "")
			if !compiled.Ok {
				return Compiled{Unsupported: &Unsupported{Unsupported: "z.iso." + method + ": " + compiled.Unsupported, At: e}}
			}
			return Compiled{Annotation: &Annotation{
				Set:            setPtr(compiled.Set),
				LibraryAdapter: adapter.Name,
				Word:           &WordSpelling{Text: "iso " + method, Covers: len(compiled.Set.Forms)},
			}}
		}
	}

	// `z.coerce.number()`: the coercion root -- Number(input) runs
	// first, and the OUTPUT then passes the same finite gate as
	// z.number() (vendored schemas.ts:1124-1130: coerce, then
	// `Number.isFinite`), so what comes out is a finite double
	if ast.IsPropertyAccessExpression(receiver) &&
		ast.IsIdentifier(receiver.AsPropertyAccessExpression().Expression) &&
		receiver.AsPropertyAccessExpression().Name().Text() == "coerce" &&
		method == "number" && len(args) == 0 {
		adapter := libraryAdapterOfNode(p, receiver.AsPropertyAccessExpression().Expression)
		if adapter != nil {
			var set refinementsets.RefinedSet
			if root, ok := adapter.Roots["number"]; ok {
				set = root()
			} else {
				set = refinementsets.Numbers
			}
			return Compiled{Annotation: &Annotation{Set: setPtr(set), LibraryAdapter: adapter.Name}}
		}
	}

	// `z.coerce.<name>()`: coercion runs the conversion on the INPUT
	// before the same validation (vendored core/api.ts:593 --
	// `coerce: true` on the same-typed schema), so the OUTPUT set is
	// the same-named root's, unchanged
	if ast.IsPropertyAccessExpression(receiver) &&
		receiver.AsPropertyAccessExpression().Name().Text() == "coerce" &&
		ast.IsIdentifier(receiver.AsPropertyAccessExpression().Expression) &&
		resolvesToAnnotationRoot(p, receiver.AsPropertyAccessExpression().Expression) {
		adapter := libraryAdapterOfNode(p, receiver.AsPropertyAccessExpression().Expression)
		compiled := libraryAdapterTotal(
			*RootConstructor(RootConstructorParams{P: p, At: e, Name: method, Args: args, Registry: registry, LibraryAdapter: adapter}),
			adapter, "root", method,
		)
		if IsUnsupported(compiled) || adapter == nil {
			return compiled
		}
		withAdapter := *compiled.Annotation
		withAdapter.LibraryAdapter = adapter.Name
		return Compiled{Annotation: &withAdapter}
	}

	// the root constructors: z.<name>(...)
	if ast.IsIdentifier(receiver) && resolvesToAnnotationRoot(p, receiver) {
		adapter := libraryAdapterOfNode(p, receiver)
		compiled := libraryAdapterTotal(
			*RootConstructor(RootConstructorParams{P: p, At: e, Name: method, Args: args, Registry: registry, LibraryAdapter: adapter}),
			adapter, "root", method,
		)
		if IsUnsupported(compiled) || adapter == nil {
			return compiled
		}
		withAdapter := *compiled.Annotation
		withAdapter.LibraryAdapter = adapter.Name
		return Compiled{Annotation: &withAdapter}
	}

	// a method on an inner chain -- the libraryAdapter id rides along
	inner := CompileAnnotation(p, receiver, registry)
	if IsUnsupported(inner) {
		return inner
	}
	var innerAdapter *libraryadapters.LibraryAdapter
	if inner.Annotation.LibraryAdapter != "" {
		innerAdapter = libraryadapters.LibraryAdapterNamed(inner.Annotation.LibraryAdapter)
	}
	chained := libraryAdapterTotal(
		*ChainMethod(ChainMethodParams{P: p, At: e, Inner: *inner.Annotation, Method: method, Args: args, Registry: registry}),
		innerAdapter, "method", method,
	)
	if IsUnsupported(chained) || inner.Annotation.LibraryAdapter == "" {
		return chained
	}
	withAdapter := *chained.Annotation
	withAdapter.LibraryAdapter = inner.Annotation.LibraryAdapter
	return Compiled{Annotation: &withAdapter}
}
