// Ported 1:1 from annotations/library_adapters/zod/zod_def_chain.ts.
package zod

import (
	"reflect"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations/libraryadapters/compiledshape"
	"github.com/microsoft/typescript-go/internal/refinedts/narrowing"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

const maxSafeInteger = 9007199254740991

// unreadOn is unreadOn in the TS source: the chain's honest fallback
// -- keep every sound claim the chain already made, and mark that the
// parse now checks more.
func unreadOn(inner compiledshape.AnnotationValue) *compiledshape.Compiled {
	out := inner
	out.Unread = true
	return &compiledshape.Compiled{Annotation: &out}
}

// bareStringBase is bareStringBase in the TS source: a chain whose
// set is exactly the bare string statement (z.string() with nothing
// stacked): a format grammar REPLACES it -- an equal set, since the
// grammar admits only strings -- and the word rides. The TS source
// compares by JSON.stringify identity; reflect.DeepEqual over the
// dereferenced tree is the substitute refinementsets itself uses
// (repetition_window_forms.go's sameSetJSON, unexported there).
func bareStringBase(inner compiledshape.AnnotationValue) bool {
	if inner.Word != nil || inner.Unread {
		return false
	}
	return reflect.DeepEqual(normalizeForCompare(inner.Set), normalizeForCompare(refinementsets.Strings))
}

// normalizeForCompare is a pointer-free copy of a RefinedSet's tree,
// so reflect.DeepEqual compares structure rather than pointer
// identity of the nested *RefinedSet fields (mirrors
// refinementsets.normalizeSet, unexported there).
func normalizeForCompare(s refinementsets.RefinedSet) refinementsets.RefinedSet {
	forms := make([]refinementsets.Refinement, len(s.Forms))
	for i, f := range s.Forms {
		out := f
		if f.A_ != nil {
			normalized := normalizeForCompare(*f.A_)
			out.A_ = &normalized
		}
		if f.B != nil {
			normalized := normalizeForCompare(*f.B)
			out.B = &normalized
		}
		if f.W != nil {
			out.W = append([]float64{}, f.W...)
		}
		forms[i] = out
	}
	return refinementsets.RefinedSet{Forms: forms}
}

func intersectPattern(inner compiledshape.AnnotationValue, pattern string) *compiledshape.Compiled {
	compiled := refinementsets.FormatGrammar(pattern, "")
	if !compiled.Ok {
		return unreadOn(inner)
	}
	out := inner
	out.Set = refinementsets.MakeRefinedSet(append(append([]refinementsets.Refinement{}, inner.Set.Forms...), compiled.Set.Forms...)...)
	return &compiledshape.Compiled{Annotation: &out}
}

// enumSelection is enumSelection in the TS source: the named members
// of an enum surgery call: one literal array of strings or numbers,
// as a union set.
func enumSelection(ctx DefChainReaderContext) (refinementsets.RefinedSet, bool) {
	if len(ctx.Args) < 1 {
		return refinementsets.RefinedSet{}, false
	}
	if words, ok := stringList(ctx.Args[0]); ok {
		set := refinementsets.StringTuple(words[0])
		for _, w := range words[1:] {
			set = refinementsets.MakeRefinedSet(refinementsets.Union(set, refinementsets.StringTuple(w)))
		}
		return set, true
	}
	bare := ctx.Args[0]
	if ast.IsAsExpression(bare) {
		bare = bare.AsAsExpression().Expression
	}
	if !ast.IsArrayLiteralExpression(bare) {
		return refinementsets.RefinedSet{}, false
	}
	var values []float64
	for _, element := range bare.AsArrayLiteralExpression().Elements.Nodes {
		v, ok := numberArg(ctx.P, element)
		if !ok {
			return refinementsets.RefinedSet{}, false
		}
		values = append(values, v)
	}
	if len(values) == 0 {
		return refinementsets.RefinedSet{}, false
	}
	return refinementsets.MakeRefinedSet(refinementsets.OneOf(values)), true
}

// ZodDefChain is ZOD_DEF_CHAIN in the TS source.
var ZodDefChain = buildZodDefChain()

func buildZodDefChain() map[string]DefChainReader {
	out := map[string]DefChainReader{}

	// -- $ZodCheckNumberFormatDef via `.int()` (classic ZodNumber):
	//    v4's method is the safeint format -- Number.isInteger AND the
	//    safe window (checks.ts:285-345), tighter than the bare
	//    integer form the shared switch states --
	out["int"] = func(ctx DefChainReaderContext) *compiledshape.Compiled {
		if len(ctx.Args) != 0 {
			return nil
		}
		result := ctx.Inner
		result.Set = refinementsets.MakeRefinedSet(append(append([]refinementsets.Refinement{}, ctx.Inner.Set.Forms...),
			refinementsets.Integer, refinementsets.AtLeast(-maxSafeInteger), refinementsets.AtMost(maxSafeInteger))...)
		return &compiledshape.Compiled{Annotation: &result}
	}

	// -- $ZodCheckMultipleOfDef via the `.step` alias
	//    (classic/schemas.ts:1047) --
	out["step"] = func(ctx DefChainReaderContext) *compiledshape.Compiled {
		var k float64
		ok := false
		if len(ctx.Args) == 1 {
			k, ok = numberArg(ctx.P, ctx.Args[0])
		}
		if !ok {
			return unreadOn(ctx.Inner)
		}
		result, ok := multipleOfSafe(ctx.Inner, k)
		if !ok {
			return unreadOn(ctx.Inner)
		}
		return result
	}

	// -- $ZodCheckLowerCaseDef / $ZodCheckUpperCaseDef: VALIDATION
	//    checks (regexes.ts:149-151), not transforms --
	out["lowercase"] = func(ctx DefChainReaderContext) *compiledshape.Compiled {
		if len(ctx.Args) != 0 {
			return unreadOn(ctx.Inner)
		}
		return intersectPattern(ctx.Inner, "^[^A-Z]*$")
	}
	out["uppercase"] = func(ctx DefChainReaderContext) *compiledshape.Compiled {
		if len(ctx.Args) != 0 {
			return unreadOn(ctx.Inner)
		}
		return intersectPattern(ctx.Inner, "^[^a-z]*$")
	}

	// -- $ZodCheckStringFormatDef as chain methods -- the spelling the
	//    wild actually writes (`z.string().email()`): every
	//    regex-backed format reads EXACTLY here, the same patterns as
	//    the roots. On a bare string chain the grammar replaces the
	//    base outright (an equal set, since the grammar admits only
	//    strings), and the word rides; on a chain with facts already
	//    stacked it intersects. Functional validators ride unread. --
	formatMethods := map[string]string{}
	for name, pattern := range V4FormatPatterns {
		formatMethods[name] = pattern
	}
	for name, pattern := range FormatMethodPatterns {
		formatMethods[name] = pattern
	}
	formatMethods["uuidv4"] = UUIDVersioned(4)
	formatMethods["uuidv6"] = UUIDVersioned(6)
	formatMethods["uuidv7"] = UUIDVersioned(7)
	formatMethods["hex"] = "^[0-9a-fA-F]*$"
	for name, pattern := range formatMethods {
		name, pattern := name, pattern
		out[name] = func(ctx DefChainReaderContext) *compiledshape.Compiled {
			if len(ctx.Args) != 0 {
				return unreadOn(ctx.Inner)
			}
			if bareStringBase(ctx.Inner) {
				read := PatternOrUnread(pattern, name)
				merged := ctx.Inner
				if read.Annotation != nil {
					merged.Set = read.Annotation.Set
					merged.Word = read.Annotation.Word
					merged.Unread = merged.Unread || read.Annotation.Unread
				} else if read.Unsupported != nil {
					return &read
				}
				return &compiledshape.Compiled{Annotation: &merged}
			}
			return intersectPattern(ctx.Inner, pattern)
		}
	}
	for _, name := range []string{
		"url", "httpUrl", "ipv6", "mac", "cidrv6", "jwt",
		"hostname", "datetime", "date", "time",
	} {
		out[name] = func(ctx DefChainReaderContext) *compiledshape.Compiled { return unreadOn(ctx.Inner) }
	}

	// -- $ZodCheckOverwriteDef and the normalize map: the value is
	//    replaced by the map's output -- every accumulated fact drops,
	//    the way trim already reads --
	out["overwrite"] = func(DefChainReaderContext) *compiledshape.Compiled {
		return &compiledshape.Compiled{Annotation: &compiledshape.AnnotationValue{Set: refinementsets.MakeRefinedSet()}}
	}
	// normalization re-spells the string (NFC/NFD/…), which can
	// change lengths and codepoints -- the output is a string again
	out["normalize"] = func(DefChainReaderContext) *compiledshape.Compiled {
		return &compiledshape.Compiled{Annotation: &compiledshape.AnnotationValue{Set: refinementsets.Strings}}
	}

	// -- $ZodCheckDef via `.check(…)` (classic/schemas.ts:98): each
	//    argument is a check -- a `z.refine(fn)` whose body the
	//    narrowing reader speaks folds in; anything else is unread --
	out["check"] = func(ctx DefChainReaderContext) *compiledshape.Compiled {
		result := ctx.Inner
		for _, argument := range ctx.Args {
			forms, exact, ok := checkArgumentForms(ctx.P, argument)
			if !ok {
				result.Unread = true
				continue
			}
			result.Set = refinementsets.MakeRefinedSet(append(append([]refinementsets.Refinement{}, result.Set.Forms...), forms...)...)
			if !exact {
				result.Unread = true
			}
		}
		return &compiledshape.Compiled{Annotation: &result}
	}

	// -- superRefine: the issue ladder is code -- unread --
	out["superRefine"] = func(ctx DefChainReaderContext) *compiledshape.Compiled { return unreadOn(ctx.Inner) }

	// -- $ZodCatchDef via `.catch(v)` (classic:176): failures become
	//    the catch value UNVALIDATED -- a literal joins the set, a
	//    computed one widens to the unknown claim --
	out["catch"] = func(ctx DefChainReaderContext) *compiledshape.Compiled {
		if len(ctx.Args) == 1 {
			piece := LiteralPiece(ctx.P, ctx.Args[0])
			if piece.Ok && piece.IsSet {
				result := ctx.Inner
				result.Set = refinementsets.MakeRefinedSet(refinementsets.Union(ctx.Inner.Set, piece.Set))
				return &compiledshape.Compiled{Annotation: &result}
			}
			if piece.Ok {
				result := ctx.Inner
				result.Absent = true
				return &compiledshape.Compiled{Annotation: &result}
			}
		}
		return &compiledshape.Compiled{Annotation: &compiledshape.AnnotationValue{Set: refinementsets.MakeRefinedSet()}}
	}

	// -- $ZodPrefaultDef via `.prefault(v)` (classic:168): an
	//    undefined input becomes the value and PARSES like any other
	//    (schemas.ts:3707-3717) -- the output set is the inner one,
	//    exactly, unlike .default's unvalidated union --
	out["prefault"] = func(ctx DefChainReaderContext) *compiledshape.Compiled {
		result := ctx.Inner
		result.Absent = false
		return &compiledshape.Compiled{Annotation: &result}
	}

	// -- $ZodNonOptionalDef via `.nonoptional()` (classic:163) --
	out["nonoptional"] = func(ctx DefChainReaderContext) *compiledshape.Compiled {
		if len(ctx.Args) != 0 {
			return unreadOn(ctx.Inner)
		}
		result := ctx.Inner
		result.Absent = false
		return &compiledshape.Compiled{Annotation: &result}
	}

	// -- $ZodEnumDef surgery: `.extract` keeps the named members,
	//    `.exclude` differences them away -- exact on string and
	//    number enums (zod itself throws at build on a non-member) --
	out["extract"] = func(ctx DefChainReaderContext) *compiledshape.Compiled {
		kept, ok := enumSelection(ctx)
		if !ok {
			return unreadOn(ctx.Inner)
		}
		result := ctx.Inner
		result.Set = kept
		return &compiledshape.Compiled{Annotation: &result}
	}
	out["exclude"] = func(ctx DefChainReaderContext) *compiledshape.Compiled {
		dropped, ok := enumSelection(ctx)
		if !ok {
			return unreadOn(ctx.Inner)
		}
		result := ctx.Inner
		result.Set = refinementsets.MakeRefinedSet(refinementsets.Difference(ctx.Inner.Set, dropped))
		return &compiledshape.Compiled{Annotation: &result}
	}

	// -- `.array()`: the same statement as z.array(X) --
	out["array"] = func(ctx DefChainReaderContext) *compiledshape.Compiled {
		if len(ctx.Args) != 0 {
			return unreadOn(ctx.Inner)
		}
		set := refinementsets.MakeRefinedSet(refinementsets.Star(ctx.Inner.Set))
		if ctx.Inner.Unread {
			return &compiledshape.Compiled{Annotation: &compiledshape.AnnotationValue{Set: set, Unread: true}}
		}
		return &compiledshape.Compiled{Annotation: &compiledshape.AnnotationValue{Set: set}}
	}

	// -- `.or(X)` / `.and(X)`: the v3-style union and intersection
	//    spellings, still live on every chain --
	out["or"] = func(ctx DefChainReaderContext) *compiledshape.Compiled {
		if len(ctx.Args) != 1 {
			return unreadOn(ctx.Inner)
		}
		other := ctx.Compile(ctx.Args[0])
		if compiledshape.IsUnsupported(other) {
			return unreadOn(ctx.Inner)
		}
		set := refinementsets.MakeRefinedSet(refinementsets.Union(ctx.Inner.Set, other.Annotation.Set))
		return &compiledshape.Compiled{Annotation: &compiledshape.AnnotationValue{
			Set:    set,
			Absent: ctx.Inner.Absent || other.Annotation.Absent,
			Unread: ctx.Inner.Unread || other.Annotation.Unread,
		}}
	}
	out["and"] = func(ctx DefChainReaderContext) *compiledshape.Compiled {
		if len(ctx.Args) != 1 {
			return unreadOn(ctx.Inner)
		}
		other := ctx.Compile(ctx.Args[0])
		if compiledshape.IsUnsupported(other) {
			return unreadOn(ctx.Inner)
		}
		set := refinementsets.MakeRefinedSet(append(append([]refinementsets.Refinement{}, ctx.Inner.Set.Forms...), other.Annotation.Set.Forms...)...)
		unread := ctx.Inner.Unread || other.Annotation.Unread
		return &compiledshape.Compiled{Annotation: &compiledshape.AnnotationValue{Set: set, Unread: unread}}
	}

	// -- `.unwrap()`: the wrapper's inner schema -- absence dropped --
	out["unwrap"] = func(ctx DefChainReaderContext) *compiledshape.Compiled {
		if len(ctx.Args) != 0 {
			return unreadOn(ctx.Inner)
		}
		result := ctx.Inner
		result.Absent = false
		return &compiledshape.Compiled{Annotation: &result}
	}

	return out
}

// multipleOfSafe applies multipleOf without letting a d=0 panic
// escape -- the TS source's try/catch around `refinedSet(...,
// multipleOf(k))`.
func multipleOfSafe(inner compiledshape.AnnotationValue, k float64) (result *compiledshape.Compiled, ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	out := inner
	out.Set = refinementsets.MakeRefinedSet(append(append([]refinementsets.Refinement{}, inner.Set.Forms...), refinementsets.MultipleOf(k))...)
	return &compiledshape.Compiled{Annotation: &out}, true
}

// checkArgumentForms reads one `.check(...)` argument -- a
// `z.refine(fn)` call whose body the narrowing reader speaks -- into
// the forms it proves, mirroring readableRefine's (forms, exact, ok)
// triple.
func checkArgumentForms(p *program.CheckerProgram, argument *ast.Node) ([]refinementsets.Refinement, bool, bool) {
	if !ast.IsCallExpression(argument) {
		return nil, false, false
	}
	call := argument.AsCallExpression()
	if !ast.IsPropertyAccessExpression(call.Expression) {
		return nil, false, false
	}
	access := call.Expression.AsPropertyAccessExpression()
	if access.Name().Text() != "refine" || len(call.Arguments.Nodes) < 1 {
		return nil, false, false
	}
	return narrowing.ReadableRefine(p.Checker, call.Arguments.Nodes[0])
}
