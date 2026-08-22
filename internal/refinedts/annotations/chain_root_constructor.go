// Root constructors: `z.<name>(...)` compiled to the refined set
// they denote. Library-adapter vocabulary wins over the shared
// zod-shaped language; a null def-row falls through here.
//
// Ported 1:1 from annotations/chain_root_constructor.ts.

package annotations

import (
	"regexp"
	"time"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/jsnum"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations/libraryadapters"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations/libraryadapters/zod"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// isoDateShape is ISO_DATE_SHAPE in the TS source: the spec's Date
// Time String Format shape -- the one string form whose Date parse is
// deterministic (sec-date-time-string-format); everything else is
// implementation-defined and stays unread.
var isoDateShape = regexp.MustCompile(
	`^\d{4}-\d{2}-\d{2}(T\d{2}:\d{2}(:\d{2}(\.\d{1,3})?)?(Z|[+-]\d{2}:\d{2})?)?$`,
)

// dateMillisArg is dateMillisArg in the TS source: the exact time
// value of a `new Date(<literal>)` argument -- a spec-format string
// literal parses to the value the host computes (spec-exact); a
// numeric literal is its own time value. (0, false) for any other
// shape.
func dateMillisArg(args []*ast.Node) (float64, bool) {
	if len(args) != 1 {
		return 0, false
	}
	a := args[0]
	if !ast.IsNewExpression(a) {
		return 0, false
	}
	newExpr := a.AsNewExpression()
	if !ast.IsIdentifier(newExpr.Expression) || newExpr.Expression.AsIdentifier().Text != "Date" {
		return 0, false
	}
	if newExpr.Arguments == nil || len(newExpr.Arguments.Nodes) == 0 {
		return 0, false
	}
	argument := newExpr.Arguments.Nodes[0]
	if ast.IsNumericLiteral(argument) {
		return float64(jsnum.FromString(argument.AsNumericLiteral().Text)), true
	}
	if ast.IsStringLiteral(argument) {
		text := argument.AsStringLiteral().Text
		if !isoDateShape.MatchString(text) {
			return 0, false
		}
		t, ok := parseSpecDate(text)
		if !ok {
			return 0, false
		}
		return t, true
	}
	return 0, false
}

// parseSpecDate parses the spec's Date Time String Format
// (sec-date-time-string-format) to its epoch-millisecond time value,
// mirroring what Date.parse computes for a string isoDateShape
// admits: date-only forms are UTC midnight, a time without an offset
// is LOCAL time in the JS spec -- but every string isoDateShape
// admits either carries an explicit offset/Z or is date-only, so
// local-time ambiguity never arises for a string that matches.
func parseSpecDate(text string) (float64, bool) {
	layouts := []string{
		"2006-01-02T15:04:05.000Z07:00",
		"2006-01-02T15:04:05Z07:00",
		"2006-01-02T15:04Z07:00",
		"2006-01-02",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, text); err == nil {
			return float64(t.UnixMilli()), true
		}
	}
	return 0, false
}

// RootConstructorParams is the destructured named-parameter struct
// for RootConstructor.
type RootConstructorParams struct {
	P              *program.CheckerProgram
	At             *ast.Node
	Name           string
	Args           []*ast.Node
	Registry       AnnotationRegistry
	LibraryAdapter *libraryadapters.LibraryAdapter
}

// RootConstructor is rootConstructor in the TS source.
func RootConstructor(params RootConstructorParams) *Compiled {
	p, at, name, args, registry, adapter := params.P, params.At, params.Name, params.Args, params.Registry, params.LibraryAdapter
	guarded := func(build func() (refinementsets.RefinedSet, error)) *Compiled {
		set, err := build()
		if err != nil {
			return unsupportedf(at, "%s", err.Error())
		}
		return &Compiled{Annotation: &Annotation{Set: setPtr(set)}}
	}
	// the libraryAdapter's OWN vocabulary wins over the shared
	// zod-shaped language: where the library's semantics differ from
	// the surface's (zod 4's number() is finite; the surface's is all
	// of R-bar), the libraryAdapter's row is the verified one
	if adapter != nil && len(args) == 0 {
		if adapterRoot, ok := adapter.Roots[name]; ok {
			return guarded(func() (refinementsets.RefinedSet, error) { return adapterRoot(), nil })
		}
	}
	// the libraryAdapter's DEF vocabulary: the complete constructor
	// table, exact rows and unread rows alike; a nil answer falls
	// through to the shared zod-shaped language
	if adapter != nil && adapter.DefRoots != nil {
		if defRoot, ok := adapter.DefRoots[name]; ok {
			read := defRoot(zod.DefReadContext{
				P:        p,
				At:       at,
				Args:     args,
				Registry: toLibraryAdapterRegistry(registry),
				Compile:  libraryAdapterCompile(func(e *ast.Node) Compiled { return CompileAnnotation(p, e, registry) }),
			})
			if read != nil {
				return fromLibraryAdapterCompiled(*read)
			}
		}
	}
	switch name {
	case "promise":
		// the inner statement is about the RESOLVED value; the marker
		// keeps the parse result itself (a promise object) off the set
		if len(args) != 1 {
			return unsupportedf(at, "z.promise takes one schema")
		}
		inner := CompileAnnotation(p, args[0], registry)
		if IsUnsupported(inner) {
			return &inner
		}
		result := *inner.Annotation
		result.Promise = true
		return &Compiled{Annotation: &result}
	case "date":
		// a VALID Date: zod refuses a NaN time value, so what remains
		// is an integral millis inside the spec's TimeClip range
		// (+-8.64e15, sec-timeclip)
		return &Compiled{Annotation: &Annotation{
			Set: setPtr(refinementsets.MakeRefinedSet(
				refinementsets.AtLeast(-8.64e15),
				refinementsets.AtMost(8.64e15),
				refinementsets.Integer,
			)),
			Date: true,
		}}
	case "number":
		return &Compiled{Annotation: &Annotation{Set: setPtr(refinementsets.Numbers)}}
	case "string":
		return &Compiled{Annotation: &Annotation{Set: setPtr(refinementsets.Strings)}}
	case "bigint":
		// exact integers at any width -- the set speaks their windows,
		// the sort keeps them off the double reading
		return &Compiled{Annotation: &Annotation{Set: setPtr(refinementsets.MakeRefinedSet(refinementsets.Integer)), KindTag: "bigint"}}
	case "symbol":
		// the symbol sort, and nothing more -- symbols carry no
		// values the set language speaks
		return &Compiled{Annotation: &Annotation{Set: setPtr(refinementsets.MakeRefinedSet()), KindTag: "symbol"}}
	case "unknown":
		return &Compiled{Annotation: &Annotation{Set: setPtr(refinementsets.MakeRefinedSet())}}
	case "json":
		// any JSON value, validation only -- the parse hands the
		// input back unchanged
		return &Compiled{Annotation: &Annotation{Set: setPtr(refinementsets.MakeRefinedSet()), Passthrough: true}}
	case "boolean":
		// the {0,1} whole-sort floor, tagged -- the same "state the sort
		// before the double collapses it" discipline bigint/symbol already
		// wear (KindTag), so a reader past this point (fact_export.go's
		// entry rows) can still tell "z.boolean()" from "z.literal(0, 1)"
		// once both compile to the identical two-member set
		return &Compiled{Annotation: &Annotation{Set: setPtr(refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{0, 1}))), KindTag: "boolean"}}
	case "guid", "uuid", "cuid", "cuid2", "ulid", "xid", "ksuid", "nanoid", "ipv4", "email":
		// v4 root formats: exactly the vendored library's own regex
		// (tmp/zod-src core/regexes.ts), compiled through the format
		// grammar -- leading negative lookaheads read as differences,
		// so email compiles too; anything the grammar cannot say
		// refuses, and the libraryAdapter boundary keeps that silent
		// for zod
		if len(args) != 0 {
			return unsupportedf(at, "z.%s with arguments is not read", name)
		}
		pattern := zod.V4FormatPatterns[name]
		compiled := refinementsets.FormatGrammar(pattern, "")
		if !compiled.Ok {
			return unsupportedf(at, "z.%s: %s", name, compiled.Unsupported)
		}
		// the hover speaks the format's NAME; the grammar stays the
		// working set judgments act on
		return &Compiled{Annotation: &Annotation{
			Set:  setPtr(compiled.Set),
			Word: &WordSpelling{Text: name, Covers: len(compiled.Set.Forms)},
		}}
	case "literal":
		if s, ok := StringArg(oneArg(args)); ok {
			return guarded(func() (refinementsets.RefinedSet, error) { return refinementsets.StringTuple(s), nil })
		}
		v, ok := oneNumberArg(p, args)
		if !ok {
			return unsupportedf(at, "z.literal takes one literal value")
		}
		return guarded(func() (refinementsets.RefinedSet, error) {
			return refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{v})), nil
		})
	case "enum":
		if members, ok := StringList(oneArg(args)); ok {
			return guarded(func() (refinementsets.RefinedSet, error) {
				set := refinementsets.StringTuple(members[0])
				for _, member := range members[1:] {
					set = refinementsets.MakeRefinedSet(refinementsets.Union(set, refinementsets.StringTuple(member)))
				}
				return set, nil
			})
		}
		w, ok := numberList(p, oneArg(args))
		if !ok {
			return unsupportedf(at, "z.enum takes a literal array of numbers or strings")
		}
		return guarded(func() (refinementsets.RefinedSet, error) {
			return refinementsets.MakeRefinedSet(refinementsets.OneOf(w)), nil
		})
	case "union":
		if len(args) != 1 || !ast.IsArrayLiteralExpression(args[0]) {
			return unsupportedf(at, "z.union takes a literal array of schemas")
		}
		// a null or undefined member admits ABSENCE, which is not a
		// set member -- it rides on the absent flag while the
		// present members fold into the union set
		absent := false
		unread := false
		var sets []refinementsets.RefinedSet
		// allNumericLiterals tracks whether EVERY present member so far
		// is a bare `z.literal(<number>)` -- the one SORT the checker
		// can still tell apart at this point (a compiled oneOf singleton
		// no longer carries whether it came from a number or a
		// single-codepoint string). Only members of this one sort
		// qualify for the flat oneOf merge below; a string literal, a
		// window, or any other member turns this false and the
		// nested-union spelling (matching a mixed z.literal/string
		// union, and matching every string-literal union, which never
		// merges -- surface.rs's own string_literal_set never flattens
		// either) stays exactly as built.
		allNumericLiterals := true
		for _, member := range args[0].AsArrayLiteralExpression().Elements.Nodes {
			if ast.IsCallExpression(member) && ast.IsPropertyAccessExpression(member.AsCallExpression().Expression) {
				access := member.AsCallExpression().Expression.AsPropertyAccessExpression()
				accessName := access.Name().Text()
				if accessName == "null" || accessName == "undefined" || accessName == "void" {
					absent = true
					continue
				}
				if !(accessName == "literal" && isNumericLiteralCall(p, member.AsCallExpression())) {
					allNumericLiterals = false
				}
			} else {
				allNumericLiterals = false
			}
			compiled := CompileAnnotation(p, member, registry)
			if IsUnsupported(compiled) {
				return &compiled
			}
			if compiled.Annotation.Unread {
				unread = true
			}
			sets = append(sets, derefSet(compiled.Annotation.Set))
		}
		if len(sets) == 0 {
			return unsupportedf(at, "z.union takes at least one present member")
		}
		// a union of ONLY numeric z.literal members is one flat oneOf --
		// matching Literal[1, 2, 3]'s own one_of([1,2,3]) reading
		// (surface.rs's literal_alias_set); every other member mix keeps
		// the nested Union tree its own arms already denote correctly.
		if allNumericLiterals && len(sets) > 1 {
			if merged, ok := refinementsets.MergeScalarOneOfArms(sets); ok {
				return &Compiled{Annotation: &Annotation{Set: setPtr(merged), Absent: absent, Unread: unread}}
			}
		}
		set := sets[0]
		for _, member := range sets[1:] {
			set = refinementsets.MakeRefinedSet(refinementsets.Union(set, member))
		}
		return &Compiled{Annotation: &Annotation{Set: setPtr(set), Absent: absent, Unread: unread}}
	case "intersection":
		a, b, unsup := compilePair(p, at, args, registry)
		if unsup != nil {
			return unsup
		}
		set := refinementsets.MakeRefinedSet(append(append([]refinementsets.Refinement{}, derefSet(a.Set).Forms...), derefSet(b.Set).Forms...)...)
		return &Compiled{Annotation: &Annotation{Set: setPtr(set), Unread: a.Unread || b.Unread}}
	case "exclude":
		a, b, unsup := compilePair(p, at, args, registry)
		if unsup != nil {
			return unsup
		}
		set := refinementsets.MakeRefinedSet(refinementsets.Difference(derefSet(a.Set), derefSet(b.Set)))
		return &Compiled{Annotation: &Annotation{Set: setPtr(set), Unread: a.Unread || b.Unread}}
	case "array":
		if len(args) != 1 {
			return unsupportedf(at, "z.array takes one schema")
		}
		item := CompileAnnotation(p, args[0], registry)
		if IsUnsupported(item) {
			return &item
		}
		set := refinementsets.MakeRefinedSet(refinementsets.Star(derefSet(item.Annotation.Set)))
		return &Compiled{Annotation: &Annotation{Set: setPtr(set), Unread: item.Annotation.Unread}}
	case "tuple":
		if len(args) != 1 {
			return unsupportedf(at, "z.tuple takes a literal array of schemas")
		}
		items, unread, unsup := annotationList(p, args[0], registry)
		if unsup != nil {
			return unsup
		}
		if len(items) == 0 {
			return &Compiled{Annotation: &Annotation{Set: setPtr(refinementsets.MakeRefinedSet(refinementsets.EmptyTuple))}}
		}
		set := nestOnto(items[:len(items)-1], items[len(items)-1])
		return &Compiled{Annotation: &Annotation{Set: setPtr(set), Unread: unread}}
	// -- the Temporal types (the vendored spec's accepted languages) --
	case "plainDate":
		return &Compiled{Annotation: &Annotation{
			Set:      setPtr(refinementsets.PlainDateTimeString),
			Temporal: &refinementsets.TemporalAnnotation{Chart: refinementsets.ChartPlainDate},
		}}
	case "plainDateTime":
		return &Compiled{Annotation: &Annotation{
			Set:      setPtr(refinementsets.PlainDateTimeString),
			Temporal: &refinementsets.TemporalAnnotation{Chart: refinementsets.ChartPlainDateTime},
		}}
	case "plainTime":
		return &Compiled{Annotation: &Annotation{
			Set:      setPtr(refinementsets.PlainTimeString),
			Temporal: &refinementsets.TemporalAnnotation{Chart: refinementsets.ChartPlainTime},
		}}
	case "plainYearMonth":
		return &Compiled{Annotation: &Annotation{
			Set:      setPtr(refinementsets.PlainYearMonthString),
			Temporal: &refinementsets.TemporalAnnotation{Chart: refinementsets.ChartPlainYearMonth},
		}}
	case "plainMonthDay":
		return &Compiled{Annotation: &Annotation{
			Set:      setPtr(refinementsets.PlainMonthDayString),
			Temporal: &refinementsets.TemporalAnnotation{Chart: refinementsets.ChartPlainMonthDay},
		}}
	case "instant":
		return &Compiled{Annotation: &Annotation{
			Set:      setPtr(refinementsets.InstantString),
			Temporal: &refinementsets.TemporalAnnotation{Chart: refinementsets.ChartInstant},
		}}
	case "zonedDateTime":
		return &Compiled{Annotation: &Annotation{
			Set:      setPtr(refinementsets.ZonedDateTimeString),
			Temporal: &refinementsets.TemporalAnnotation{Chart: refinementsets.ChartZonedDateTime},
		}}
	case "duration":
		return &Compiled{Annotation: &Annotation{
			Set:      setPtr(refinementsets.DurationString),
			Temporal: &refinementsets.TemporalAnnotation{Chart: refinementsets.ChartDuration},
		}}
	case "timeZone":
		return &Compiled{Annotation: &Annotation{
			Set:      setPtr(refinementsets.TimeZoneIdentifier),
			Temporal: &refinementsets.TemporalAnnotation{Chart: refinementsets.ChartTimeZone},
		}}
	case "calendar":
		return &Compiled{Annotation: &Annotation{
			Set:      setPtr(refinementsets.AnnotationValue),
			Temporal: &refinementsets.TemporalAnnotation{Chart: refinementsets.ChartCalendar},
		}}
	default:
		return unsupportedf(at, "z.%s is not a readable constructor", name)
	}
}

func oneArg(args []*ast.Node) *ast.Node {
	if len(args) != 1 {
		return nil
	}
	return args[0]
}

// isNumericLiteralCall is whether a `z.literal(...)` call's own sole
// argument reads as a NUMBER (oneNumberArg's own reading, not a
// string) -- the union case's own sort test, kept beside the
// "literal" case's identical number/string branch above so both read
// the argument the same way.
func isNumericLiteralCall(p *program.CheckerProgram, call *ast.CallExpression) bool {
	_, ok := oneNumberArg(p, call.Arguments.Nodes)
	return ok
}
