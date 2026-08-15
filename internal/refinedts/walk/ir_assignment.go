// from control_flow/ir_assignment.ts
//
// Assignments and RHS effects for the flow IR: a declaration or
// expression writes one tracked slot, and the right side lowers
// through the shared effect grammar.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// AssignmentTarget is the (target, effect) pair returned by the
// assignment readers below — the TS source's inline `{ target:
// number; effect: LoopEffect } | null` shape.
type AssignmentTarget struct {
	Target int
	Effect kernelbridge.LoopEffect
}

// RhsEffect is rhsEffect in the TS source: an assigned RIGHT side as
// an effect — a tracked name COPIES under any sort, `null`/`undefined`
// write the absent state constant under ANY sort, a string literal
// writes its exact tuple into a string-sorted slot, a string-sorted
// concatenation or template builds its sequence, and everything else
// reads numerically.
func RhsEffect(context *LoweringContext, targetSort BindingKind, e *ast.Node) (kernelbridge.LoopEffect, bool) {
	if copy, ok := IndexOf(context, e); ok {
		return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectVar, Index: copy}, true
	}
	// `x = null` / `return undefined`: the absent outcome, which no set
	// can hold — it rides in the state constant's flag instead. Under
	// any target sort: absence is neither a number nor a word.
	if IsAbsentKeyword(e) {
		return kernelbridge.AbsentConst(), true
	}
	if ast.IsStringLiteral(e) && targetSort == BindingKindString {
		return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectConst, Set: refinementsets.StringTuple(e.AsStringLiteral().Text)}, true
	}
	// a string-sorted right side reads as a SEQUENCE first: `a + b`
	// between two string-sorted operands concatenates, and a template
	// literal is that concatenation spelled out. Numeric reading
	// follows for everything else.
	if targetSort == BindingKindString {
		if seq, ok := SequenceEffectOf(context, e); ok {
			return seq, true
		}
	}
	return EffectOf(context, e)
}

var compoundOps = map[ast.Kind]kernelbridge.LoopEffectOp{
	ast.KindPlusEqualsToken:     kernelbridge.LoopOpAdd,
	ast.KindMinusEqualsToken:    kernelbridge.LoopOpSub,
	ast.KindAsteriskEqualsToken: kernelbridge.LoopOpMul,
	ast.KindSlashEqualsToken:    kernelbridge.LoopOpDiv,
	// the compound bitwise and shift forms. The number-sort gate below
	// (Sorts[target] != BindingKindNumber) is the only gate they need:
	// ToInt32 is total on doubles, so any number-sorted target and
	// operand is a legal input to transferBitwise.
	ast.KindAmpersandEqualsToken:                         kernelbridge.LoopOpBitAnd,
	ast.KindBarEqualsToken:                               kernelbridge.LoopOpBitOr,
	ast.KindCaretEqualsToken:                             kernelbridge.LoopOpBitXor,
	ast.KindLessThanLessThanEqualsToken:                  kernelbridge.LoopOpShl,
	ast.KindGreaterThanGreaterThanEqualsToken:            kernelbridge.LoopOpSar,
	ast.KindGreaterThanGreaterThanGreaterThanEqualsToken: kernelbridge.LoopOpShr,
	// the compound exponentiation form. It wears the same number-sort
	// gate the rest of this table does: transferPow carries the spec's
	// own NaN rows as cells, so any number-sorted target and operand is
	// a legal input.
	ast.KindAsteriskAsteriskEqualsToken: kernelbridge.LoopOpPow,
}

// AssignmentOfExpression is assignmentOfExpression in the TS
// source: an assigning EXPRESSION's target index and effect —
// `x = e`, `x += e`-family compounds, and `i++`/`--i` steps.
func AssignmentOfExpression(context *LoweringContext, e *ast.Node) (AssignmentTarget, bool) {
	// i++ / --i and friends: the unit step, spelled as arithmetic —
	// the step READS its target numerically
	if ast.IsPostfixUnaryExpression(e) || ast.IsPrefixUnaryExpression(e) {
		var operator ast.Kind
		var operand *ast.Node
		if ast.IsPostfixUnaryExpression(e) {
			unary := e.AsPostfixUnaryExpression()
			operator, operand = unary.Operator, unary.Operand
		} else {
			unary := e.AsPrefixUnaryExpression()
			operator, operand = unary.Operator, unary.Operand
		}
		if operator == ast.KindPlusPlusToken || operator == ast.KindMinusMinusToken {
			target, ok := NumberIndexOf(context, operand)
			if !ok {
				return AssignmentTarget{}, false
			}
			op := kernelbridge.LoopOpAdd
			if operator == ast.KindMinusMinusToken {
				op = kernelbridge.LoopOpSub
			}
			one := kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectConst, Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{1}))}
			targetVar := kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectVar, Index: target}
			return AssignmentTarget{
				Target: target,
				Effect: kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectBinary, Op: op, A: &targetVar, B: &one},
			}, true
		}
	}
	if !ast.IsBinaryExpression(e) {
		return AssignmentTarget{}, false
	}
	bin := e.AsBinaryExpression()
	// the target slot, read through the same IndexOf every route reads a
	// place through. A MEMBER target resolves here exactly as a local
	// does where the member names a tracked slot — a record leaf
	// ("this.staticMethodKey"), an array slot — so `this.x ??= d` reaches
	// the same joins `x ??= d` reaches. A member that resolves to no slot
	// still declines: there is nothing to join into.
	target, ok := IndexOf(context, bin.Left)
	if !ok {
		return AssignmentTarget{}, false
	}
	if bin.OperatorToken.Kind == ast.KindEqualsToken {
		effect, ok := RhsEffect(context, context.Sorts[target], bin.Right)
		if !ok {
			return AssignmentTarget{}, false
		}
		return AssignmentTarget{Target: target, Effect: effect}, true
	}
	// `x ||= e`, `x &&= e`, `x ??= e`: the result is x itself or e —
	// the JOIN of the two admits every run, under any sort, exactly as
	// the short-circuit operators read in expression position
	switch bin.OperatorToken.Kind {
	case ast.KindBarBarEqualsToken, ast.KindAmpersandAmpersandEqualsToken, ast.KindQuestionQuestionEqualsToken:
		right, rightOk := RhsEffect(context, context.Sorts[target], bin.Right)
		if !rightOk {
			return AssignmentTarget{}, false
		}
		targetVar := kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectVar, Index: target}
		return AssignmentTarget{
			Target: target,
			Effect: kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectJoin, A: &targetVar, B: &right},
		}, true
	}
	// `s += suffix` on a STRING-sorted slot is concatenation — exactly
	// `s = s + suffix`, which already lowers; the compound spelling gets
	// the same sequence reading instead of refusing on the number gate
	if bin.OperatorToken.Kind == ast.KindPlusEqualsToken && context.Sorts[target] == BindingKindString {
		right, rightOk := SequenceEffectOf(context, bin.Right)
		if !rightOk {
			return AssignmentTarget{}, false
		}
		targetVar := kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectVar, Index: target}
		return AssignmentTarget{
			Target: target,
			Effect: kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectConcat, A: &targetVar, B: &right},
		}, true
	}
	op, ok := compoundOps[bin.OperatorToken.Kind]
	if !ok {
		return AssignmentTarget{}, false
	}
	// a compound READS its target numerically
	if context.Sorts[target] != BindingKindNumber {
		return AssignmentTarget{}, false
	}
	b, ok := EffectOf(context, bin.Right)
	if !ok {
		return AssignmentTarget{}, false
	}
	targetVar := kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectVar, Index: target}
	return AssignmentTarget{
		Target: target,
		Effect: kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectBinary, Op: op, A: &targetVar, B: &b},
	}, true
}

// AssignmentOf is assignmentOf in the TS source: an assignment's
// target index and effect, from `x = e`, `let x = e`, or `x += e`-
// family compounds.
func AssignmentOf(context *LoweringContext, s *ast.Node) (AssignmentTarget, bool) {
	if ast.IsVariableStatement(s) {
		return DeclarationAssignment(context, s.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes)
	}
	if !ast.IsExpressionStatement(s) {
		return AssignmentTarget{}, false
	}
	return AssignmentOfExpression(context, s.AsExpressionStatement().Expression)
}
