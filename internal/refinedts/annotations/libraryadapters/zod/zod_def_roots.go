// The COMPLETE zod v4 schema-def vocabulary (z.<name>(…)). Check-def
// rows live in zod_def_chain.go; the disposition index at the bottom
// of zod_def_chain.ts covers both tables. The contract per row is
// two-tier:
//
//	exact — the set language states what the parse enforces, and the
//	  row builds those forms (patterns verbatim from the vendored
//	  core/regexes.ts, windows from core/util.ts);
//	unread — the parse checks MORE than the sets can say (a
//	  functional validator, a transform, a container the model does
//	  not speak), so the row compiles the widest sound claim and
//	  marks the annotation unread: checked positions alert instead of
//	  accepting, and nothing is ever silently dropped.
//
// A reader returns nil to mean "no opinion here" — the shared
// zod-shaped chain language in chain_root_constructor.go then reads
// the spelling as before.
//
// Ported 1:1 from annotations/library_adapters/zod/zod_def_roots.ts.
package zod

import (
	"strconv"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/jsnum"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations/libraryadapters/compiledshape"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// thunkBody is thunkBody in the TS source: an arrow's immediate
// expression body, unwrapped -- `() => X`.
func thunkBody(e *ast.Node) *ast.Node {
	if !ast.IsArrowFunction(e) {
		return nil
	}
	if len(e.Parameters()) != 0 {
		return nil
	}
	body := e.Body()
	if body == nil || ast.IsBlock(body) {
		return nil
	}
	return body
}

func oneSchema(args []*ast.Node, compile func(*ast.Node) compiledshape.Compiled) *compiledshape.AnnotationValue {
	if len(args) != 1 {
		return nil
	}
	inner := compile(args[0])
	if compiledshape.IsUnsupported(inner) {
		return nil
	}
	return inner.Annotation
}

func wrapAbsent(args []*ast.Node, compile func(*ast.Node) compiledshape.Compiled) *compiledshape.Compiled {
	inner := oneSchema(args, compile)
	if inner == nil {
		result := UnreadUnknown
		return &result
	}
	wrapped := *inner
	wrapped.Absent = true
	return &compiledshape.Compiled{Annotation: &wrapped}
}

// ZodDefRoots is ZOD_DEF_ROOTS in the TS source.
var ZodDefRoots = buildZodDefRoots()

func buildZodDefRoots() map[string]DefRootReader {
	out := map[string]DefRootReader{}
	for name, reader := range ZodFormatRoots {
		out[name] = reader
	}

	// -- $ZodAnyDef / $ZodUnknownDef: every value, no claim --
	out["any"] = func(DefReadContext) *compiledshape.Compiled {
		return &compiledshape.Compiled{Annotation: &compiledshape.AnnotationValue{Set: refinementsets.MakeRefinedSet()}}
	}

	// -- $ZodNeverDef: the empty set -- nothing parses; the hover says
	//    the word, the ray past +infinity stays the working set --
	out["never"] = func(DefReadContext) *compiledshape.Compiled {
		return &compiledshape.Compiled{Annotation: &compiledshape.AnnotationValue{
			Set:  Bottom,
			Word: &compiledshape.WordSpelling{Text: "never", Covers: len(Bottom.Forms)},
		}}
	}

	// -- $ZodUndefinedDef / $ZodVoidDef / $ZodNullDef: absence only --
	//    no present value ever comes out ("never, or absent"), and the
	//    absent flag carries what does --
	absenceRow := func(DefReadContext) *compiledshape.Compiled {
		return &compiledshape.Compiled{Annotation: &compiledshape.AnnotationValue{
			Set:    Bottom,
			Absent: true,
			Word:   &compiledshape.WordSpelling{Text: "never", Covers: len(Bottom.Forms)},
		}}
	}
	out["undefined"] = absenceRow
	out["void"] = absenceRow
	out["null"] = absenceRow

	// -- $ZodNaNDef: NaN is not an element of R-bar, so no set holds
	//    the output -- the widest claim, unread --
	out["nan"] = func(DefReadContext) *compiledshape.Compiled {
		result := UnreadWord("NaN")
		return &result
	}

	// -- $ZodNumberFormatDef (util.ts:588 windows; the check is
	//    Number.isInteger + the range for int formats, the range alone
	//    for floats -- checks.ts:282-370, no representability test) --
	out["uint32"] = func(DefReadContext) *compiledshape.Compiled {
		return &compiledshape.Compiled{Annotation: &compiledshape.AnnotationValue{
			Set: refinementsets.MakeRefinedSet(refinementsets.Integer, refinementsets.AtLeast(0), refinementsets.AtMost(4294967295)),
		}}
	}
	out["float32"] = func(DefReadContext) *compiledshape.Compiled {
		return &compiledshape.Compiled{Annotation: &compiledshape.AnnotationValue{
			Set: refinementsets.MakeRefinedSet(
				refinementsets.AtLeast(-3.4028234663852886e38),
				refinementsets.AtMost(3.4028234663852886e38),
			),
		}}
	}

	// -- $ZodBigIntFormatDef (util.ts:596): exact integers in the
	//    64-bit windows, worn on the bigint sort; the strict upper
	//    spelling keeps every endpoint an exact double --
	out["int64"] = func(DefReadContext) *compiledshape.Compiled {
		return &compiledshape.Compiled{Annotation: &compiledshape.AnnotationValue{
			Set:     refinementsets.MakeRefinedSet(refinementsets.Integer, refinementsets.AtLeast(-(1 << 63)), refinementsets.Below(1<<63)),
			KindTag: "bigint",
		}}
	}
	out["uint64"] = func(DefReadContext) *compiledshape.Compiled {
		return &compiledshape.Compiled{Annotation: &compiledshape.AnnotationValue{
			Set:     refinementsets.MakeRefinedSet(refinementsets.Integer, refinementsets.AtLeast(0), refinementsets.Below(1<<64)),
			KindTag: "bigint",
		}}
	}

	// -- $ZodLiteralDef beyond one string/number: literal arrays,
	//    booleans, null/undefined, safe-window bigints (the shared
	//    switch keeps the single string/number fast path) --
	out["literal"] = func(ctx DefReadContext) *compiledshape.Compiled {
		if len(ctx.Args) != 1 {
			return nil
		}
		argument := ctx.Args[0]
		bare := argument
		if ast.IsAsExpression(bare) {
			bare = bare.AsAsExpression().Expression
		}
		if ast.IsArrayLiteralExpression(bare) {
			var pieces []LiteralPieceResult
			var labels []string
			for _, element := range bare.AsArrayLiteralExpression().Elements.Nodes {
				piece := LiteralPiece(ctx.P, element)
				if !piece.Ok {
					result := UnreadUnknown
					return &result
				}
				pieces = append(pieces, piece)
				if label, ok := LiteralLabel(ctx.P, element); ok {
					labels = append(labels, label)
				}
			}
			if len(pieces) == 0 {
				return nil
			}
			hasWord := len(labels) == len(pieces)
			wordText := ""
			if hasWord {
				wordText = joinPipe(labels)
			}
			result := UnionOfPieces(pieces, wordText, hasWord)
			return &result
		}
		piece := LiteralPiece(ctx.P, bare)
		if !piece.Ok {
			return nil // the shared switch's refusal stands
		}
		if !piece.IsSet {
			return &compiledshape.Compiled{Annotation: &compiledshape.AnnotationValue{Set: Bottom, Absent: true}}
		}
		// a lone string/number already reads in the shared switch;
		// the rows here add booleans and bigints
		if _, ok := stringArg(bare); ok {
			return nil
		}
		if _, ok := numberArg(ctx.P, bare); ok {
			return nil
		}
		annotation := &compiledshape.AnnotationValue{Set: piece.Set}
		if ast.IsBigIntLiteral(bare) {
			annotation.KindTag = "bigint"
		}
		return &compiledshape.Compiled{Annotation: annotation}
	}

	// -- $ZodEnumDef over a TS enum object (z.enum(Fruits) and the
	//    deprecated z.nativeEnum): the declaration's members are the
	//    values, auto-increment included --
	enumRow := func(unreadOnMiss bool) DefRootReader {
		return func(ctx DefReadContext) *compiledshape.Compiled {
			if len(ctx.Args) != 1 {
				if unreadOnMiss {
					result := UnreadWord("enum")
					return &result
				}
				return nil
			}
			members, ok := NativeEnumMembers(ctx.P, ctx.Args[0])
			if !ok {
				if unreadOnMiss {
					result := UnreadWord("enum")
					return &result
				}
				return nil // literal arrays read in the switch
			}
			pieces := make([]LiteralPieceResult, len(members))
			labels := make([]string, len(members))
			for i, m := range members {
				if m.IsString {
					pieces[i] = LiteralPieceResult{Set: refinementsets.StringTuple(m.Str), IsSet: true, Ok: true}
					labels[i] = strconv.Quote(m.Str)
				} else {
					pieces[i] = LiteralPieceResult{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{m.Num})), IsSet: true, Ok: true}
					labels[i] = jsnum.Number(m.Num).String()
				}
			}
			result := UnionOfPieces(pieces, joinPipe(labels), true)
			return &result
		}
	}
	out["enum"] = enumRow(false)
	out["nativeEnum"] = enumRow(true)

	// -- $ZodStringBoolDef composition (core/api.ts:1738): whatever
	//    the word lists say, the OUTPUT is exactly a boolean -- the
	//    argful spelling reads the same as the argless libraryAdapter
	//    root --
	out["stringbool"] = func(DefReadContext) *compiledshape.Compiled {
		return &compiledshape.Compiled{Annotation: &compiledshape.AnnotationValue{
			Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{0, 1})),
		}}
	}

	out["templateLiteral"] = TemplateLiteralRoot

	// -- the wrapper defs as ROOT spellings, each delegating to the
	//    inner schema the way the chain methods do --
	// $ZodOptionalDef / $ZodExactOptionalDef / $ZodNullableDef
	out["optional"] = func(ctx DefReadContext) *compiledshape.Compiled { return wrapAbsent(ctx.Args, ctx.Compile) }
	out["exactOptional"] = func(ctx DefReadContext) *compiledshape.Compiled { return wrapAbsent(ctx.Args, ctx.Compile) }
	out["nullable"] = func(ctx DefReadContext) *compiledshape.Compiled { return wrapAbsent(ctx.Args, ctx.Compile) }
	out["nullish"] = func(ctx DefReadContext) *compiledshape.Compiled { return wrapAbsent(ctx.Args, ctx.Compile) }
	// $ZodNonOptionalDef: absence stripped -- parse refuses undefined
	out["nonoptional"] = func(ctx DefReadContext) *compiledshape.Compiled {
		inner := oneSchema(ctx.Args, ctx.Compile)
		if inner == nil {
			result := UnreadUnknown
			return &result
		}
		stripped := *inner
		stripped.Absent = false
		return &compiledshape.Compiled{Annotation: &stripped}
	}
	// $ZodReadonlyDef: values unchanged
	out["readonly"] = func(ctx DefReadContext) *compiledshape.Compiled {
		inner := oneSchema(ctx.Args, ctx.Compile)
		if inner == nil {
			return nil
		}
		return &compiledshape.Compiled{Annotation: inner}
	}
	// $ZodSuccessDef: the outcome flag itself -- exactly a boolean
	out["success"] = func(DefReadContext) *compiledshape.Compiled {
		return &compiledshape.Compiled{Annotation: &compiledshape.AnnotationValue{
			Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{0, 1})),
		}}
	}
	// $ZodPipeDef / $ZodCodecDef: the output validates against the
	//   TARGET schema (decode direction), whatever precedes
	out["pipe"] = func(ctx DefReadContext) *compiledshape.Compiled {
		if len(ctx.Args) != 2 {
			result := UnreadUnknown
			return &result
		}
		target := ctx.Compile(ctx.Args[1])
		if compiledshape.IsUnsupported(target) {
			result := UnreadUnknown
			return &result
		}
		return &target
	}
	out["codec"] = func(ctx DefReadContext) *compiledshape.Compiled {
		if len(ctx.Args) < 2 {
			result := UnreadUnknown
			return &result
		}
		target := ctx.Compile(ctx.Args[1])
		if compiledshape.IsUnsupported(target) {
			result := UnreadUnknown
			return &result
		}
		return &target
	}
	// $ZodPreprocessDef: the conversion runs on the INPUT; the output
	//   validates the named schema
	out["preprocess"] = func(ctx DefReadContext) *compiledshape.Compiled {
		if len(ctx.Args) != 2 {
			result := UnreadUnknown
			return &result
		}
		target := ctx.Compile(ctx.Args[1])
		if compiledshape.IsUnsupported(target) {
			result := UnreadUnknown
			return &result
		}
		return &target
	}
	// $ZodTransformDef: the output is whatever the map returns -- the
	//   widest claim, and nothing more is checked on it, so no unread
	out["transform"] = func(DefReadContext) *compiledshape.Compiled {
		return &compiledshape.Compiled{Annotation: &compiledshape.AnnotationValue{Set: refinementsets.MakeRefinedSet()}}
	}
	// $ZodLazyDef: the thunk's immediate body reads as the schema; a
	//   recursive or block-bodied getter is unread
	out["lazy"] = func(ctx DefReadContext) *compiledshape.Compiled {
		if len(ctx.Args) != 1 {
			result := UnreadUnknown
			return &result
		}
		body := thunkBody(ctx.Args[0])
		if body == nil {
			result := UnreadUnknown
			return &result
		}
		inner := ctx.Compile(body)
		if compiledshape.IsUnsupported(inner) {
			result := UnreadUnknown
			return &result
		}
		return &inner
	}

	// -- the containers and code-bearing defs the model does not
	//    speak: the widest sound claim, unread, never silent --
	// $ZodObjectDef in VALUE position (top-level object statements
	//   compile through object_schema_compiler.go with full key sets
	//   -- annotation_file_facts.go routes them there first; these
	//   rows catch nesting)
	unreadOf := func(word string) DefRootReader {
		return func(DefReadContext) *compiledshape.Compiled {
			result := UnreadWord(word)
			return &result
		}
	}
	out["object"] = unreadOf("object")
	out["strictObject"] = unreadOf("object")
	out["looseObject"] = unreadOf("object")
	// $ZodDiscriminatedUnionDef: a union of object schemas
	out["discriminatedUnion"] = unreadOf("discriminated union")
	// $ZodUnionDef with the exclusivity gate: members' union is the
	//   sound base; "exactly one" is checked beyond it
	out["xor"] = func(ctx DefReadContext) *compiledshape.Compiled {
		if len(ctx.Args) < 1 || !ast.IsArrayLiteralExpression(ctx.Args[0]) {
			result := UnreadUnknown
			return &result
		}
		var set *refinementsets.RefinedSet
		for _, element := range ctx.Args[0].AsArrayLiteralExpression().Elements.Nodes {
			member := ctx.Compile(element)
			if compiledshape.IsUnsupported(member) {
				result := UnreadUnknown
				return &result
			}
			if set == nil {
				s := member.Annotation.Set
				set = &s
			} else {
				joined := refinementsets.MakeRefinedSet(refinementsets.Union(*set, member.Annotation.Set))
				set = &joined
			}
		}
		if set == nil {
			result := UnreadUnknown
			return &result
		}
		return &compiledshape.Compiled{Annotation: &compiledshape.AnnotationValue{Set: *set, Unread: true}}
	}
	// $ZodRecordDef and its partial/loose spellings: keys the model
	//   cannot enumerate
	out["record"] = unreadOf("record")
	out["partialRecord"] = unreadOf("record")
	out["looseRecord"] = unreadOf("record")
	// $ZodMapDef / $ZodSetDef / $ZodFileDef: container sorts the set
	//   language does not hold
	out["map"] = unreadOf("map")
	out["set"] = unreadOf("set")
	out["file"] = unreadOf("file")
	// $ZodFunctionDef / $ZodCustomDef / z.instanceof: code-bearing
	out["function"] = unreadOf("function")
	out["custom"] = unreadOf("custom check")
	out["instanceof"] = unreadOf("instanceof")

	return out
}

func joinPipe(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += " | "
		}
		out += p
	}
	return out
}
