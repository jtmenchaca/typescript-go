// Methods on an inner chain: default, temporal bounds, string
// transforms, refine/rest, and the library-adapter vocabulary.
// Numeric bounds live in chain_numeric_method.go. Sequence helpers
// (lists, nest, pair) are asked by root constructors too.
//
// Ported 1:1 from annotations/chain_method.ts. Annotation.Set is
// *refinementsets.RefinedSet (the hub type's own convention, landed
// ahead of this file in declared_refinement.go/schema_chain_compiler.go);
// setPtr/derefSet bridge to the value-typed refinementsets API.

package annotations

import (
	"reflect"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations/libraryadapters"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations/libraryadapters/zod"
	"github.com/microsoft/typescript-go/internal/refinedts/diagnose"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/narrowing"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

func setPtr(s refinementsets.RefinedSet) *refinementsets.RefinedSet { return &s }

func derefSet(s *refinementsets.RefinedSet) refinementsets.RefinedSet {
	if s == nil {
		return refinementsets.RefinedSet{}
	}
	return *s
}

// numberList is numberList in the TS source.
func numberList(p *program.CheckerProgram, e *ast.Node) ([]float64, bool) {
	if e == nil {
		return nil, false
	}
	bare := e
	if ast.IsAsExpression(bare) {
		bare = bare.AsAsExpression().Expression
	}
	if !ast.IsArrayLiteralExpression(bare) {
		return nil, false
	}
	var out []float64
	for _, element := range bare.AsArrayLiteralExpression().Elements.Nodes {
		v, ok := NumberArg(p, element)
		if !ok {
			return nil, false
		}
		out = append(out, v)
	}
	return out, true
}

// annotationList is annotationList in the TS source: returns
// (sets, unread, unsupported).
func annotationList(p *program.CheckerProgram, e *ast.Node, registry AnnotationRegistry) ([]refinementsets.RefinedSet, bool, *Compiled) {
	if !ast.IsArrayLiteralExpression(e) {
		return nil, false, unsupportedf(e, "expected a literal array of schemas")
	}
	var out []refinementsets.RefinedSet
	unread := false
	for _, element := range e.AsArrayLiteralExpression().Elements.Nodes {
		compiled := CompileAnnotation(p, element, registry)
		if IsUnsupported(compiled) {
			return nil, false, &compiled
		}
		if compiled.Annotation.Unread {
			unread = true
		}
		out = append(out, derefSet(compiled.Annotation.Set))
	}
	return out, unread, nil
}

// nestOnto is nestOnto in the TS source: right-nest sequence members
// onto an ending set.
func nestOnto(items []refinementsets.RefinedSet, end refinementsets.RefinedSet) refinementsets.RefinedSet {
	set := end
	for i := len(items) - 1; i >= 0; i-- {
		set = refinementsets.MakeRefinedSet(refinementsets.Concatenation(items[i], set))
	}
	return set
}

// restOnto is restOnto in the TS source: `.rest(T)`: replace the
// nest's trailing 1-tuple end with end . T*.
func restOnto(set refinementsets.RefinedSet, tail refinementsets.RefinedSet) refinementsets.RefinedSet {
	if len(set.Forms) == 1 {
		only := set.Forms[0]
		if only.Form == refinementsets.FormEmptyTuple {
			return tail
		}
		if only.Form == refinementsets.FormConcatenation {
			return refinementsets.MakeRefinedSet(refinementsets.Concatenation(*only.A_, restOnto(*only.B, tail)))
		}
	}
	return refinementsets.MakeRefinedSet(refinementsets.Concatenation(set, tail))
}

// compilePair is compilePair in the TS source: returns (a, b,
// unsupported).
func compilePair(p *program.CheckerProgram, at *ast.Node, args []*ast.Node, registry AnnotationRegistry) (Annotation, Annotation, *Compiled) {
	if len(args) != 2 {
		return Annotation{}, Annotation{}, unsupportedf(at, "expected two schemas")
	}
	a := CompileAnnotation(p, args[0], registry)
	if IsUnsupported(a) {
		return Annotation{}, Annotation{}, &a
	}
	b := CompileAnnotation(p, args[1], registry)
	if IsUnsupported(b) {
		return Annotation{}, Annotation{}, &b
	}
	return *a.Annotation, *b.Annotation, nil
}

// ChainMethodParams is the destructured named-parameter struct for
// ChainMethod.
type ChainMethodParams struct {
	P        *program.CheckerProgram
	At       *ast.Node
	Inner    Annotation
	Method   string
	Args     []*ast.Node
	Registry AnnotationRegistry
}

// sameSetStructurally is the port's substitute for the TS source's
// JSON.stringify identity check on a RefinedSet -- see
// libraryadapters/zod/zod_def_chain.go's bareStringBase for the same
// substitution.
func sameSetStructurally(a, b refinementsets.RefinedSet) bool {
	return reflect.DeepEqual(normalizeSetForCompare(a), normalizeSetForCompare(b))
}

func normalizeSetForCompare(s refinementsets.RefinedSet) refinementsets.RefinedSet {
	forms := make([]refinementsets.Refinement, len(s.Forms))
	for i, f := range s.Forms {
		out := f
		if f.A_ != nil {
			normalized := normalizeSetForCompare(*f.A_)
			out.A_ = &normalized
		}
		if f.B != nil {
			normalized := normalizeSetForCompare(*f.B)
			out.B = &normalized
		}
		if f.W != nil {
			out.W = append([]float64{}, f.W...)
		}
		forms[i] = out
	}
	return refinementsets.RefinedSet{Forms: forms}
}

// ChainMethod is chainMethod in the TS source.
func ChainMethod(params ChainMethodParams) (result *Compiled) {
	p, at, inner, method, args, registry := params.P, params.At, params.Inner, params.Method, params.Args, params.Registry
	base := derefSet(inner.Set)

	// the set spelling before and after this one method -- what shows a
	// `.int()` or `.min(0)` silently dropping out of the chain
	if diagnose.EventOn("annotations.chainMethod") {
		before := kernelbridge.EncodeSet(base)
		defer func() {
			after := before
			if result != nil && result.Annotation != nil {
				after = kernelbridge.EncodeSet(derefSet(result.Annotation.Set))
			}
			diagnose.Log("annotations.chainMethod",
				"method", method, "before", before, "after", after,
				"unsupported", result != nil && IsUnsupported(*result))
		}()
	}
	// an opaque unread chain (a record, a map, a custom schema -- the
	// unknown claim with the unread mark): any later method checks
	// the parse further, which the claim already covers -- the
	// statement stays the honest unread unknown
	if inner.Unread && len(base.Forms) == 0 {
		result := inner
		return &Compiled{Annotation: &result}
	}
	// `.default(v)`: an undefined input becomes the default WITHOUT
	// validation (vendored core/schemas.ts:3647-3652), so the output
	// is the inner set union {default} -- the default is not checked
	// into the set -- and absence never comes out
	if method == "default" && len(args) == 1 {
		// a default already an ARM of the inner union adds nothing --
		// the union with it is the inner set itself, so no arm is
		// built
		alreadyArm := func(piece refinementsets.RefinedSet) bool {
			var armSpellings func(set refinementsets.RefinedSet) []refinementsets.RefinedSet
			armSpellings = func(set refinementsets.RefinedSet) []refinementsets.RefinedSet {
				if len(set.Forms) == 1 && set.Forms[0].Form == refinementsets.FormUnion {
					only := set.Forms[0]
					return append(armSpellings(*only.A_), armSpellings(*only.B)...)
				}
				return []refinementsets.RefinedSet{set}
			}
			for _, arm := range armSpellings(base) {
				if sameSetStructurally(arm, piece) {
					return true
				}
			}
			return false
		}
		if v, ok := NumberArg(p, args[0]); ok {
			piece := refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{v}))
			if alreadyArm(piece) {
				return &Compiled{Annotation: &Annotation{Set: setPtr(base)}}
			}
			return &Compiled{Annotation: &Annotation{Set: setPtr(refinementsets.MakeRefinedSet(refinementsets.Union(base, piece)))}}
		}
		if s, ok := StringArg(args[0]); ok {
			piece := refinementsets.StringTuple(s)
			if alreadyArm(piece) {
				return &Compiled{Annotation: &Annotation{Set: setPtr(base)}}
			}
			return &Compiled{Annotation: &Annotation{Set: setPtr(refinementsets.MakeRefinedSet(refinementsets.Union(base, piece)))}}
		}
		return unsupportedf(at, ".default takes one literal value here")
	}
	// a TEMPORAL chain: .min/.max state chart bounds as ISO spellings
	// of the same type, resolved through the kernel's calendar lens
	// at judgment; a month-day has no order, and a zone or calendar
	// is a vocabulary, so none of them takes a bound
	if inner.Temporal != nil {
		if method == "min" || method == "max" {
			chart := inner.Temporal.Chart
			if chart == refinementsets.ChartPlainMonthDay {
				return unsupportedf(at, "a month-day has no order (no year, so no compare) — .%s is not defined for it", method)
			}
			if chart == refinementsets.ChartTimeZone || chart == refinementsets.ChartCalendar {
				return unsupportedf(at, ".%s is not defined for z.%s", method, chart)
			}
			s, ok := "", false
			if len(args) == 1 {
				s, ok = StringArg(args[0])
			}
			if !ok {
				return unsupportedf(at, ".%s on a temporal chain takes one string literal (an ISO spelling of the same type)", method)
			}
			temporal := *inner.Temporal
			if method == "min" {
				temporal.Min = s
				temporal.HasMin = true
			} else {
				temporal.Max = s
				temporal.HasMax = true
			}
			return &Compiled{Annotation: &Annotation{Set: setPtr(base), Temporal: &temporal}}
		}
		return unsupportedf(at, ".%s is not a readable temporal chain method", method)
	}
	var adapter *libraryadapters.LibraryAdapter
	if inner.LibraryAdapter != "" {
		adapter = libraryadapters.LibraryAdapterNamed(inner.LibraryAdapter)
	}
	// the libraryAdapter's DEF chain vocabulary -- the complete check
	// table; a nil answer falls through to the shared language below
	if adapter != nil && adapter.DefChain != nil {
		if defChain, ok := adapter.DefChain[method]; ok {
			read := defChain(zod.DefChainReaderContext{
				DefReadContext: zod.DefReadContext{
					P:        p,
					At:       at,
					Args:     args,
					Registry: toLibraryAdapterRegistry(registry),
					Compile:  libraryAdapterCompile(func(e *ast.Node) Compiled { return CompileAnnotation(p, e, registry) }),
				},
				Inner: *toLibraryAdapterAnnotation(&inner),
			})
			if read != nil {
				return fromLibraryAdapterCompiled(*read)
			}
		}
	}
	if numeric := NumericChainMethod(NumericChainMethodParams{P: p, At: at, Inner: inner, Method: method, Args: args}); numeric != nil {
		return numeric
	}
	withString := func(build func(s string) refinementsets.RefinedSet) *Compiled {
		s, ok := "", false
		if len(args) == 1 {
			s, ok = StringArg(args[0])
		}
		if !ok {
			return unsupportedf(at, ".%s takes one string literal", method)
		}
		built := build(s)
		// the C* ground drops beside the pattern: the stack blinds
		// the kernel's one-shape pattern prover, and the conjunct
		// adds nothing (the pattern is a language over C already)
		set := refinementsets.MakeRefinedSet(
			refinementsets.WithoutStringGround(append(append([]refinementsets.Refinement{}, base.Forms...), built.Forms...))...,
		)
		result := inner
		result.Set = setPtr(set)
		return &Compiled{Annotation: &result}
	}
	switch method {
	case "brand", "describe", "meta":
		// metadata only -- brand is type-level, describe and meta
		// write the registry (vendored classic/schemas.ts); the value
		// set is untouched, so the chain reads on through them
		result := inner
		return &Compiled{Annotation: &result}
	case "optional", "nullable", "nullish":
		// absence rides beside the set -- optional admits undefined,
		// nullable null, nullish both; the model conflates the two on
		// the absent marker (verified: nullish = optional(nullable),
		// vendored schemas.ts:300)
		if len(args) == 0 {
			return &Compiled{Annotation: &Annotation{Set: setPtr(base), Absent: true}}
		}
		return unsupportedf(at, ".%s takes no arguments", method)
	case "not":
		// the strings NOT in the excluded schema's set -- the chain
		// spelling of z.exclude, RefinedTS's own (zod has none). The
		// conjoined difference keeps the guard-narrowed shape
		// (strings minus pattern), so a startsWith-refuted
		// fall-through and the stated set ask the same question.
		if len(args) != 1 {
			return unsupportedf(at, ".%s takes one schema", method)
		}
		excluded := CompileAnnotation(p, args[0], registry)
		if IsUnsupported(excluded) {
			return &excluded
		}
		set := refinementsets.MakeRefinedSet(refinementsets.WithoutStringGround(append(
			append([]refinementsets.Refinement{}, base.Forms...),
			refinementsets.Difference(refinementsets.Strings, derefSet(excluded.Annotation.Set)),
		))...)
		if excluded.Annotation.Unread {
			result := inner
			result.Set = setPtr(set)
			result.Unread = true
			return &Compiled{Annotation: &result}
		}
		result := inner
		result.Set = setPtr(set)
		return &Compiled{Annotation: &result}
	case "toUpperCase", "toLowerCase":
		// an OVERWRITE transform (the vendored library maps the value
		// and hands the mapped text on): each accumulated form
		// survives the case mapping or falls by its own rule -- a
		// case-invariant word survives (mapping is per code point and
		// never merges across it), a minimum length survives (no
		// code point maps to nothing), a maximum falls (full mappings
		// expand: ß -> SS), and anything undecidable falls with it --
		// sound weakening, never a wrong claim
		if len(args) != 0 {
			return unsupportedf(at, ".%s takes no arguments", method)
		}
		return &Compiled{Annotation: &Annotation{Set: setPtr(refinementsets.MakeRefinedSet(CaseStableForms(base.Forms)...))}}
	case "startsWith":
		return withString(refinementsets.StartsWithSet)
	case "endsWith":
		return withString(refinementsets.EndsWithSet)
	case "includes":
		return withString(refinementsets.IncludesSet)
	case "regex":
		if len(args) != 1 || !ast.IsRegularExpressionLiteral(args[0]) {
			return unsupportedf(at, ".regex takes one regular-expression literal")
		}
		// the literal's text is /pattern/flags -- strip the
		// delimiters
		text := args[0].AsRegularExpressionLiteral().Text
		lastSlash := lastSlashIndex(text)
		pattern := text[1:lastSlash]
		flags := text[lastSlash+1:]
		compiled := refinementsets.FormatGrammar(pattern, flags)
		if !compiled.Ok {
			return unsupportedf(at, ".regex: %s", compiled.Unsupported)
		}
		// the C* ground drops beside the pattern -- withString's own
		// rule (above), applied here too: a bare `z.string()` base
		// compiles to Star(Codepoints) (Strings), and stacking it
		// alongside the grammar's own concatenation/repeat forms blinds
		// the kernel's one-shape sequence-subset prover (measured:
		// alignedSegSubsetB proves the grammar-alone pair but declines
		// once the redundant ground rides beside it) for a conjunct
		// that adds nothing -- the pattern is already a language over C.
		set := refinementsets.MakeRefinedSet(
			refinementsets.WithoutStringGround(append(append([]refinementsets.Refinement{}, base.Forms...), compiled.Set.Forms...))...,
		)
		return &Compiled{Annotation: &Annotation{Set: setPtr(set)}}
	case "rest":
		if len(args) != 1 {
			return unsupportedf(at, ".rest takes one schema")
		}
		item := CompileAnnotation(p, args[0], registry)
		if IsUnsupported(item) {
			return &item
		}
		return &Compiled{Annotation: &Annotation{Set: setPtr(restOnto(base, refinementsets.MakeRefinedSet(refinementsets.Star(derefSet(item.Annotation.Set)))))}}
	case "refine":
		// .refine(predicate, message?) accepts a value exactly when
		// the predicate returns truthy (vendored zod core/
		// schemas.ts: $ZodCustom's check runs def.fn and
		// handleRefineResult files an issue on a falsy result). A
		// single-parameter, expression-bodied predicate whose body
		// the narrowing reader fully reads compiles to the forms the
		// held predicate proves; anything richer stays unread.
		if len(args) < 1 || len(args) > 2 {
			return unsupportedf(at, ".refine takes a predicate and an optional message")
		}
		forms, exact, ok := narrowing.ReadableRefine(p.Checker, args[0])
		// the predicate itself rides every UNREAD outcome, so a
		// judged position holding an EXACT value can still run it
		var predicate *ast.Node
		if ast.IsArrowFunction(args[0]) || ast.IsFunctionExpression(args[0]) {
			predicate = args[0]
		}
		if !ok {
			// a body outside the readable guard language stays what
			// it is in zod -- a runtime check at parse -- and the
			// annotation is marked UNREAD: positions checked against
			// it alert instead of accepting, since the parse checks
			// more than the set says
			result := inner
			result.Set = setPtr(base)
			result.Unread = true
			result.Refine = predicate
			return &Compiled{Annotation: &result}
		}
		// a LOOSENED fold (a length body counted in UTF-16 units) is
		// wider than the predicate: the set folds for refutations,
		// and the annotation stays unread so acceptances still alert
		set := refinementsets.MakeRefinedSet(append(append([]refinementsets.Refinement{}, base.Forms...), forms...)...)
		if exact {
			result := inner
			result.Set = setPtr(set)
			return &Compiled{Annotation: &result}
		}
		result := inner
		result.Set = setPtr(set)
		result.Unread = true
		result.Refine = predicate
		return &Compiled{Annotation: &result}
	default:
		// the libraryAdapter's own vocabulary
		if adapter != nil && len(args) == 0 {
			if custom, ok := adapter.Chain[method]; ok {
				built, err := guardedChainBuild(custom, base)
				if err != "" {
					return unsupportedf(at, "%s", err)
				}
				return &Compiled{Annotation: &Annotation{Set: setPtr(built)}}
			}
		}
		return unsupportedf(at, ".%s is not a readable chain method", method)
	}
}

func lastSlashIndex(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '/' {
			return i
		}
	}
	return -1
}

// guardedChainBuild wraps a libraryadapter chain vocabulary call in a
// recover, mirroring the TS source's try/catch around `custom(base)`.
func guardedChainBuild(custom func(base refinementsets.RefinedSet) refinementsets.RefinedSet, base refinementsets.RefinedSet) (result refinementsets.RefinedSet, errText string) {
	defer func() {
		if r := recover(); r != nil {
			errText = panicText(r)
		}
	}()
	return custom(base), ""
}
