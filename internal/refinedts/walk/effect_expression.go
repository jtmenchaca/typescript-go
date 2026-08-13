// from control_flow/effect_expression.ts
//
// The EXPRESSION half of the effect grammar, shared by both
// lowerings — the loop solver (loop_effect.ts) and the flow IR
// (lowering_to_kernel_ir.ts): literals through parens/casts, tracked
// reads, negation and unary plus, the five arithmetic operators, the
// Math reads (five unary, min/max), and the ternary as a join (its
// condition must be write-free — both arms are admitted, sound).
// `ReadPlace` answers a spelled tracked (or known) name; `Opaque`
// says what an unmodeled shape becomes — the solver path answers
// unknown for write-free shapes, the IR path declines. (0-value,
// false) means the reading declines.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/jsnum"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

var binOps = map[ast.Kind]kernelbridge.LoopEffectOp{
	ast.KindPlusToken:     kernelbridge.LoopOpAdd,
	ast.KindMinusToken:    kernelbridge.LoopOpSub,
	ast.KindAsteriskToken: kernelbridge.LoopOpMul,
	ast.KindSlashToken:    kernelbridge.LoopOpDiv,
	ast.KindPercentToken:  kernelbridge.LoopOpRem,
}

var mathOps = map[string]kernelbridge.LoopEffectOp{
	"floor": kernelbridge.LoopOpFloor,
	"ceil":  kernelbridge.LoopOpCeil,
	"round": kernelbridge.LoopOpRound,
	"trunc": kernelbridge.LoopOpTrunc,
	"abs":   kernelbridge.LoopOpAbs,
}

// ContainsWrite is containsWrite in the TS source: does the subtree
// perform any write? A shape mapped to an opaque effect must be
// write-free, or the lowering's state would miss the write. Shared
// by both lowerings.
func ContainsWrite(node *ast.Node) bool {
	if ast.IsBinaryExpression(node) {
		bin := node.AsBinaryExpression()
		if bin.OperatorToken.Kind >= ast.KindFirstAssignment && bin.OperatorToken.Kind <= ast.KindLastAssignment {
			return true
		}
	}
	if ast.IsPrefixUnaryExpression(node) {
		unary := node.AsPrefixUnaryExpression()
		if unary.Operator == ast.KindPlusPlusToken || unary.Operator == ast.KindMinusMinusToken {
			return true
		}
	}
	if ast.IsPostfixUnaryExpression(node) {
		unary := node.AsPostfixUnaryExpression()
		if unary.Operator == ast.KindPlusPlusToken || unary.Operator == ast.KindMinusMinusToken {
			return true
		}
	}
	found := false
	node.ForEachChild(func(child *ast.Node) bool {
		if !found {
			found = ContainsWrite(child)
		}
		return false
	})
	return found
}

// EffectReader mirrors the destructured `reader` parameter of
// lowerEffectExpression in the TS source.
type EffectReader struct {
	ReadPlace func(spelled string) (kernelbridge.LoopEffect, bool)
	Opaque    func(e *ast.Node) (kernelbridge.LoopEffect, bool)
}

// stringSlotEffect is a tracked STRING-sorted read as an effect, or
// (zero, false). Sequence building admits only the string sort: a
// number-sorted operand of `+` is arithmetic, not concatenation, and
// an unknown-sorted one is a reread across sorts, which is never a
// claim.
func stringSlotEffect(context *LoweringContext, e *ast.Node) (kernelbridge.LoopEffect, bool) {
	i, ok := IndexOf(context, e)
	if !ok {
		return kernelbridge.LoopEffect{}, false
	}
	if context.Sorts[i] != BindingKindString {
		return kernelbridge.LoopEffect{}, false
	}
	return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectVar, Index: i}, true
}

// concatOf pairs two operand effects into one concatenation effect.
func concatOf(a, b kernelbridge.LoopEffect) kernelbridge.LoopEffect {
	return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectConcat, A: &a, B: &b}
}

// SequenceEffectOf reads an expression as a SEQUENCE effect — the
// string world's half of the effect grammar, beside the numeric
// LowerEffectExpression:
//
//   - a string literal is its exact tuple;
//   - a tracked string-sorted name is a read;
//   - `a + b` with BOTH sides sequence-readable is their
//     concatenation (a `+` with a number-sorted operand is arithmetic
//     and is not read here — it takes the numeric route as before);
//   - a template literal `a${x}b` is that concatenation spelled out,
//     its literal chunks as exact tuples and its substitutions as
//     sequence reads.
//
// A non-string or untracked part declines the whole reading, exactly
// as it did before this route existed. Parens and casts unwrap first.
func SequenceEffectOf(context *LoweringContext, e *ast.Node) (kernelbridge.LoopEffect, bool) {
	head := Unwrapped(e)
	if ast.IsStringLiteral(head) {
		return kernelbridge.LoopEffect{
			Kind: kernelbridge.LoopEffectConst,
			Set:  refinementsets.StringTuple(head.AsStringLiteral().Text),
		}, true
	}
	if ast.IsNoSubstitutionTemplateLiteral(head) {
		return kernelbridge.LoopEffect{
			Kind: kernelbridge.LoopEffectConst,
			Set:  refinementsets.StringTuple(head.AsNoSubstitutionTemplateLiteral().Text),
		}, true
	}
	if slot, ok := stringSlotEffect(context, head); ok {
		return slot, true
	}
	if ast.IsBinaryExpression(head) {
		bin := head.AsBinaryExpression()
		if bin.OperatorToken.Kind != ast.KindPlusToken {
			return kernelbridge.LoopEffect{}, false
		}
		a, aOk := SequenceEffectOf(context, bin.Left)
		if !aOk {
			return kernelbridge.LoopEffect{}, false
		}
		b, bOk := SequenceEffectOf(context, bin.Right)
		if !bOk {
			return kernelbridge.LoopEffect{}, false
		}
		return concatOf(a, b), true
	}
	if ast.IsTemplateExpression(head) {
		return templateSequenceOf(context, head)
	}
	return kernelbridge.LoopEffect{}, false
}

// SpelledSequenceShape is whether an expression is a sequence by its
// own SYNTAX alone — a string literal, any template literal, or a `+`
// chain of those. It reads no names and consults no slot vector, so it
// answers the same in any context: the sort question a caller must
// settle before the callee's slots exist.
func SpelledSequenceShape(e *ast.Node) bool {
	head := Unwrapped(e)
	if ast.IsStringLiteral(head) || ast.IsNoSubstitutionTemplateLiteral(head) ||
		ast.IsTemplateExpression(head) {
		return true
	}
	if ast.IsBinaryExpression(head) {
		bin := head.AsBinaryExpression()
		if bin.OperatorToken.Kind != ast.KindPlusToken {
			return false
		}
		return SpelledSequenceShape(bin.Left) && SpelledSequenceShape(bin.Right)
	}
	return false
}

// templateSequenceOf is `a${x}b${y}c` as a right-nested chain of the
// concatenation effect: the head's literal text, then per span the
// substituted expression's sequence reading followed by that span's
// literal text. An empty literal chunk contributes the empty tuple,
// which concatenates to nothing — kept rather than special-cased, so
// the chain's shape is one rule.
func templateSequenceOf(context *LoweringContext, head *ast.Node) (kernelbridge.LoopEffect, bool) {
	template := head.AsTemplateExpression()
	if template.TemplateSpans == nil {
		return kernelbridge.LoopEffect{}, false
	}
	parts := []kernelbridge.LoopEffect{{
		Kind: kernelbridge.LoopEffectConst,
		Set:  refinementsets.StringTuple(template.Head.AsTemplateHead().Text),
	}}
	for _, spanNode := range template.TemplateSpans.Nodes {
		span := spanNode.AsTemplateSpan()
		substituted, ok := SequenceEffectOf(context, span.Expression)
		if !ok {
			return kernelbridge.LoopEffect{}, false
		}
		parts = append(parts, substituted)
		var text string
		switch {
		case ast.IsTemplateMiddle(span.Literal):
			text = span.Literal.AsTemplateMiddle().Text
		case ast.IsTemplateTail(span.Literal):
			text = span.Literal.AsTemplateTail().Text
		default:
			return kernelbridge.LoopEffect{}, false
		}
		parts = append(parts, kernelbridge.LoopEffect{
			Kind: kernelbridge.LoopEffectConst,
			Set:  refinementsets.StringTuple(text),
		})
	}
	// fold right so the chain nests the way the kernel's Concatenation
	// form does
	out := parts[len(parts)-1]
	for i := len(parts) - 2; i >= 0; i-- {
		out = concatOf(parts[i], out)
	}
	return out, true
}

// LowerEffectExpression is lowerEffectExpression in the TS source.
func LowerEffectExpression(e *ast.Node, reader EffectReader) (kernelbridge.LoopEffect, bool) {
	if ast.IsParenthesizedExpression(e) || ast.IsAsExpression(e) || ast.IsNonNullExpression(e) {
		var inner *ast.Node
		switch {
		case ast.IsParenthesizedExpression(e):
			inner = e.AsParenthesizedExpression().Expression
		case ast.IsAsExpression(e):
			inner = e.AsAsExpression().Expression
		case ast.IsNonNullExpression(e):
			inner = e.AsNonNullExpression().Expression
		}
		return LowerEffectExpression(inner, reader)
	}
	if ast.IsNumericLiteral(e) {
		return kernelbridge.LoopEffect{
			Kind: kernelbridge.LoopEffectConst,
			Set:  refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{float64(jsnum.FromString(e.AsNumericLiteral().Text))})),
		}, true
	}
	if e.Kind == ast.KindTrueKeyword {
		return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectConst, Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{1}))}, true
	}
	if e.Kind == ast.KindFalseKeyword {
		return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectConst, Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{0}))}, true
	}
	if spelled, ok := SpelledNameOf(e); ok {
		if held, ok := reader.ReadPlace(spelled); ok {
			return held, true
		}
		return reader.Opaque(e)
	}
	if ast.IsPrefixUnaryExpression(e) {
		unary := e.AsPrefixUnaryExpression()
		if unary.Operator == ast.KindMinusToken {
			if ast.IsNumericLiteral(unary.Operand) {
				return kernelbridge.LoopEffect{
					Kind: kernelbridge.LoopEffectConst,
					Set:  refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{-float64(jsnum.FromString(unary.Operand.AsNumericLiteral().Text))})),
				}, true
			}
			a, ok := LowerEffectExpression(unary.Operand, reader)
			if !ok {
				return kernelbridge.LoopEffect{}, false
			}
			return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectUnary, Op: kernelbridge.LoopOpNeg, A: &a}, true
		}
		if unary.Operator == ast.KindPlusToken {
			return LowerEffectExpression(unary.Operand, reader)
		}
		return reader.Opaque(e)
	}
	if ast.IsBinaryExpression(e) {
		bin := e.AsBinaryExpression()
		op, ok := binOps[bin.OperatorToken.Kind]
		if !ok {
			return reader.Opaque(e)
		}
		a, aOk := LowerEffectExpression(bin.Left, reader)
		b, bOk := LowerEffectExpression(bin.Right, reader)
		if !aOk || !bOk {
			return kernelbridge.LoopEffect{}, false
		}
		return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectBinary, Op: op, A: &a, B: &b}, true
	}
	if ast.IsConditionalExpression(e) {
		cond := e.AsConditionalExpression()
		if ContainsWrite(cond.Condition) {
			return kernelbridge.LoopEffect{}, false
		}
		a, aOk := LowerEffectExpression(cond.WhenTrue, reader)
		b, bOk := LowerEffectExpression(cond.WhenFalse, reader)
		if !aOk || !bOk {
			return kernelbridge.LoopEffect{}, false
		}
		return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectJoin, A: &a, B: &b}, true
	}
	if ast.IsCallExpression(e) {
		call := e.AsCallExpression()
		if ast.IsPropertyAccessExpression(call.Expression) {
			access := call.Expression.AsPropertyAccessExpression()
			if ast.IsIdentifier(access.Expression) && access.Expression.Text() == "Math" {
				name := access.Name().Text()
				if un, ok := mathOps[name]; ok && call.Arguments != nil && len(call.Arguments.Nodes) == 1 {
					a, ok := LowerEffectExpression(call.Arguments.Nodes[0], reader)
					if !ok {
						return kernelbridge.LoopEffect{}, false
					}
					return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectUnary, Op: un, A: &a}, true
				}
				if (name == "min" || name == "max") && call.Arguments != nil && len(call.Arguments.Nodes) == 2 {
					a, aOk := LowerEffectExpression(call.Arguments.Nodes[0], reader)
					b, bOk := LowerEffectExpression(call.Arguments.Nodes[1], reader)
					if !aOk || !bOk {
						return kernelbridge.LoopEffect{}, false
					}
					op := kernelbridge.LoopOpMin
					if name == "max" {
						op = kernelbridge.LoopOpMax
					}
					return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectBinary, Op: op, A: &a, B: &b}, true
				}
			}
		}
		return reader.Opaque(e)
	}
	return reader.Opaque(e)
}
