// Ported 1:1 from annotations/library_adapters/zod/zod_template_literal_root.ts.
package zod

import (
	"strconv"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/jsnum"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations/libraryadapters/compiledshape"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// sequenceShaped is sequenceShaped in the TS source: a schema part of
// a template literal whose set has SEQUENCE semantics for sure --
// star/repeat/concatenation shapes and unions of them. A
// scalar-anchored part (z.number()) stringifies at runtime, which the
// sets do not model, so it fails this gate.
func sequenceShaped(set refinementsets.RefinedSet) bool {
	if len(set.Forms) == 0 {
		return false
	}
	for _, f := range set.Forms {
		switch f.Form {
		case refinementsets.FormStar, refinementsets.FormRepeat,
			refinementsets.FormConcatenation, refinementsets.FormEmptyTuple, refinementsets.FormWord:
			// ok
		case refinementsets.FormUnion, refinementsets.FormDifference:
			if !sequenceShaped(*f.A_) || !sequenceShaped(*f.B) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

type stringSchemaPart struct {
	set   refinementsets.RefinedSet
	label string
}

// stringSchemaPartOf is stringSchemaPart in the TS source: a template
// part spelled as a string-valued schema -- z.enum of string members
// or z.literal of one string -- read as its word union, with the
// members' own spelling for the hover. (nil, false) anywhere else.
func stringSchemaPartOf(e *ast.Node) (stringSchemaPart, bool) {
	if !ast.IsCallExpression(e) || !ast.IsPropertyAccessExpression(e.AsCallExpression().Expression) {
		return stringSchemaPart{}, false
	}
	access := e.AsCallExpression().Expression.AsPropertyAccessExpression()
	method := access.Name().Text()
	args := e.AsCallExpression().Arguments.Nodes
	if len(args) != 1 {
		return stringSchemaPart{}, false
	}
	argument := args[0]
	if method == "literal" {
		s, ok := stringArg(argument)
		if !ok {
			return stringSchemaPart{}, false
		}
		return stringSchemaPart{set: refinementsets.StringTuple(s), label: strconv.Quote(s)}, true
	}
	if method == "enum" {
		words, ok := stringList(argument)
		if !ok {
			return stringSchemaPart{}, false
		}
		set := refinementsets.StringTuple(words[0])
		for _, w := range words[1:] {
			set = refinementsets.MakeRefinedSet(refinementsets.Union(set, refinementsets.StringTuple(w)))
		}
		labels := make([]string, len(words))
		for i, w := range words {
			labels[i] = strconv.Quote(w)
		}
		return stringSchemaPart{set: set, label: strings.Join(labels, " | ")}, true
	}
	return stringSchemaPart{}, false
}

// TemplateLiteralRoot is templateLiteralRoot in the TS source:
// $ZodTemplateLiteralDef (classic/schemas.ts:2487): literal parts
// concatenate as their codepoint tuples, schema parts as their sets
// when those are sequence-shaped for sure; a stringified scalar part
// is not modeled, so it rides unread.
func TemplateLiteralRoot(ctx DefReadContext) *compiledshape.Compiled {
	if len(ctx.Args) < 1 {
		result := UnreadString
		return &result
	}
	list := ctx.Args[0]
	if !ast.IsArrayLiteralExpression(list) {
		result := UnreadString
		return &result
	}
	var parts []refinementsets.RefinedSet
	spelling := []string{"`"}
	for _, element := range list.AsArrayLiteralExpression().Elements.Nodes {
		if s, ok := stringArg(element); ok {
			parts = append(parts, refinementsets.StringTuple(s))
			spelling = append(spelling, s)
			continue
		}
		if v, ok := numberArg(ctx.P, element); ok {
			text := jsnum.Number(v).String()
			parts = append(parts, refinementsets.StringTuple(text))
			spelling = append(spelling, text)
			continue
		}
		// a STRING-VALUED schema part read by its spelling: an enum of
		// string members or a string literal -- their words
		// concatenate exactly (a one-character word is a bare oneOf,
		// which the shape gate below cannot tell from a scalar, so
		// the spelling decides here)
		if spelled, ok := stringSchemaPartOf(element); ok {
			parts = append(parts, spelled.set)
			spelling = append(spelling, "${"+spelled.label+"}")
			continue
		}
		inner := ctx.Compile(element)
		if compiledshape.IsUnsupported(inner) || inner.Annotation.Unread || !sequenceShaped(inner.Annotation.Set) {
			result := UnreadStringWord("template literal")
			return &result
		}
		parts = append(parts, inner.Annotation.Set)
		wordText := "…"
		if inner.Annotation.Word != nil {
			wordText = inner.Annotation.Word.Text
		}
		spelling = append(spelling, "${"+wordText+"}")
	}
	if len(parts) == 0 {
		result := UnreadStringWord("template literal")
		return &result
	}
	set := parts[len(parts)-1]
	for i := len(parts) - 2; i >= 0; i-- {
		set = refinementsets.MakeRefinedSet(refinementsets.Concatenation(parts[i], set))
	}
	spelling = append(spelling, "`")
	result := compiledshape.Compiled{
		Annotation: &compiledshape.AnnotationValue{
			Set:  set,
			Word: &compiledshape.WordSpelling{Text: strings.Join(spelling, ""), Covers: len(set.Forms)},
		},
	}
	return &result
}
