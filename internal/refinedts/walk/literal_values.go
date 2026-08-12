// from evaluation/literal_values.ts
//
// What an exact literal (or exact template) holds: numeric, boolean,
// string, bigint, and regex literals, and templates whose every span
// is readable as a string or a string set.

package walk

import (
	"strconv"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/jsnum"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// EvaluateLiteral is evaluateLiteral in the TS source: an exact
// literal, or a template whose spans read as strings. (AbstractValue{},
// false) when the expression is neither.
func EvaluateLiteral(ctx *FlowContext, env Env, e *ast.Node) (abstractdomain.AbstractValue, bool) {
	if ast.IsNumericLiteral(e) {
		return abstractdomain.KnownValues([]float64{float64(jsnum.FromString(e.Text()))}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved), true
	}
	// a boolean literal is the exact word 1 or 0 under the boolean
	// sort (true ↦ 1 is the spec's own ToNumber)
	if e.Kind == ast.KindTrueKeyword {
		return abstractdomain.KnownValues([]float64{1}, abstractdomain.PrimitiveBoolean, abstractdomain.TrustProved), true
	}
	if e.Kind == ast.KindFalseKeyword {
		return abstractdomain.KnownValues([]float64{0}, abstractdomain.PrimitiveBoolean, abstractdomain.TrustProved), true
	}
	// a string IS its codepoint tuple — one root, no separate ground;
	// the SORT rides along so the word is never reread as numbers
	if ast.IsStringLiteral(e) || ast.IsNoSubstitutionTemplateLiteral(e) {
		return abstractdomain.KnownValues(refinementsets.CodepointsOf(e.Text()), abstractdomain.PrimitiveString, abstractdomain.TrustProved), true
	}
	// a bigint literal is its exact integer at any width — the value
	// rides its own kind, outside the double sort
	if ast.IsBigIntLiteral(e) {
		text := e.Text()
		trimmed := text
		if len(trimmed) > 0 && trimmed[len(trimmed)-1] == 'n' {
			trimmed = trimmed[:len(trimmed)-1]
		}
		v, err := strconv.ParseInt(trimmed, 10, 64)
		if err != nil {
			panic(err)
		}
		return abstractdomain.AbstractValue{Kind: abstractdomain.KindBigints, BigintValues: []int64{v}}, true
	}
	// a regex literal is its source and flags exactly (the host
	// object's mutable lastIndex stays untracked)
	if ast.IsRegularExpressionLiteral(e) {
		text := e.Text()
		close := lastIndexByte(text, '/')
		return abstractdomain.AbstractValue{
			Kind:   abstractdomain.KindRegex,
			Source: text[1:close],
			Flags:  text[close+1:],
		}, true
	}
	if ast.IsTemplateExpression(e) {
		return evaluateTemplate(ctx, env, e), true
	}
	return abstractdomain.AbstractValue{}, false
}

func lastIndexByte(s string, b byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == b {
			return i
		}
	}
	return -1
}

// evaluateTemplate is the TS source's evaluateTemplate: a template
// with substitutions is exact when every span is: a string span
// appends its tuple, a number span appends the exact decimal spelling
// (ToString of a Number is spec-exact). A span holding a string SET
// keeps the template a PATTERN instead of nothing: `/${rest}` is the
// concatenation of the literal tuple with whatever strings the span
// admits — the kernel's own form.
func evaluateTemplate(ctx *FlowContext, env Env, e *ast.Node) abstractdomain.AbstractValue {
	template := e.AsTemplateExpression()
	exact := refinementsets.CodepointsOf(template.Head.Text())
	hasExact := true
	var parts []refinementsets.RefinedSet
	if len(template.Head.Text()) > 0 {
		parts = append(parts, refinementsets.StringTuple(template.Head.Text()))
	}
	readable := true
	grade := abstractdomain.TrustProved
	for _, spanNode := range template.TemplateSpans.Nodes {
		span := spanNode.AsTemplateSpan()
		known := evaluateExpression(ctx, env, span.Expression)
		grade = abstractdomain.MinTrustLevel(grade, abstractdomain.TrustLevelOf(known))
		// a span typed `string | undefined` is still a STRING span for
		// reading purposes — the nullish arms spell their own words, so
		// look through them when judging string-ness
		spanType := ctx.P.Checker.GetTypeAtLocation(span.Expression)
		var constituents []*checker.Type
		if spanType.IsUnion() {
			constituents = spanType.Types()
		} else {
			constituents = []*checker.Type{spanType}
		}
		var nonNullish []*checker.Type
		for _, t := range constituents {
			if (t.Flags() & (checker.TypeFlagsUndefined | checker.TypeFlagsNull)) == 0 {
				nonNullish = append(nonNullish, t)
			}
		}
		stringy := len(nonNullish) > 0
		for _, t := range nonNullish {
			if (t.Flags() & checker.TypeFlagsStringLike) == 0 {
				stringy = false
				break
			}
		}
		switch {
		case known.Kind == abstractdomain.KindValues && (stringy || known.KindTag == abstractdomain.PrimitiveString):
			if hasExact {
				exact = append(exact, known.Values...)
			}
			parts = append(parts, refinementsets.StringTuple(stringOf(known.Values)))
		case known.Kind == abstractdomain.KindValues && known.KindTag == abstractdomain.PrimitiveNumber && len(known.Values) == 1:
			// the kernel's ToString where it answers (integral, below
			// 10^21 — the plain-decimal form); the host's where it
			// declines, at spec grade
			lifted, ok := ctx.Kernel.Decimal(known.Values[0])
			var text string
			if ok {
				text = lifted
			} else {
				text = strconv.FormatFloat(known.Values[0], 'g', -1, 64)
				grade = abstractdomain.MinTrustLevel(grade, abstractdomain.TrustSpec)
			}
			if hasExact {
				exact = append(exact, refinementsets.CodepointsOf(text)...)
			}
			parts = append(parts, refinementsets.StringTuple(text))
		case known.Kind == abstractdomain.KindSet && known.SetKindTag == abstractdomain.SetKindTagNone && stringy:
			hasExact = false
			parts = append(parts, known.Set)
		case known.Kind == abstractdomain.KindPossiblyUndefined && known.Inner != nil &&
			known.Inner.Kind == abstractdomain.KindSet && known.Inner.SetKindTag == abstractdomain.SetKindTagNone && stringy:
			// a maybe-absent string span: the strings it admits, or the
			// WORD the absent value spells (the marker conflates
			// undefined with null — two words)
			hasExact = false
			parts = append(parts, unionOf(
				unionOf(known.Inner.Set, refinementsets.StringTuple("undefined")),
				refinementsets.StringTuple("null"),
			))
		default:
			// every other value converts through the ONE text model —
			// undefined and null become their words, booleans theirs, a
			// plain object "[object Object]", an array its comma join, a
			// maybe wrapper the union with "undefined"
			text, ok := TextOfKnown(ctx.Kernel.Decimal, known)
			if ok {
				grade = abstractdomain.MinTrustLevel(grade, text.Grade)
				if text.HasExact && hasExact {
					exact = append(exact, text.Exact...)
				} else {
					hasExact = false
				}
				parts = append(parts, text.Set)
			} else {
				hasExact = false
				readable = false
			}
		}
		if len(span.Literal.Text()) > 0 {
			if hasExact {
				exact = append(exact, refinementsets.CodepointsOf(span.Literal.Text())...)
			}
			parts = append(parts, refinementsets.StringTuple(span.Literal.Text()))
		}
	}
	if hasExact {
		return abstractdomain.KnownValues(exact, abstractdomain.PrimitiveString, grade)
	}
	if readable && len(parts) > 0 {
		set := parts[len(parts)-1]
		for i := len(parts) - 2; i >= 0; i-- {
			set = refinementsets.MakeRefinedSet(refinementsets.Concatenation(parts[i], set))
		}
		return abstractdomain.KnownSet(set, nil, abstractdomain.MinTrustLevel(grade, abstractdomain.TrustSpec), abstractdomain.SetKindTagNone)
	}
	return silence.Residue()
}
