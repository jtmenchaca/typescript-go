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

// EffectOf is effectOf in the TS source: an expression as a body
// effect, or (zero, false) where the reading ends.
//
// The read resolves through slotIndexOfName, which honours the CLOSED
// name map an inlined body carries — a free name inside an inlined
// callee must decline, never bind to the caller's slot of the same
// spelling. (The TS source reads context.bindings directly here; a
// name the callee did not declare could resolve to the caller's
// binding of that spelling, which is the capture the `names` map
// exists to forbid. Resolving through the one path IndexOf uses closes
// that.)
func EffectOf(context *LoweringContext, e *ast.Node) (kernelbridge.LoopEffect, bool) {
	return LowerEffectExpression(e, EffectReader{
		ReadPlace: func(spelled string) (kernelbridge.LoopEffect, bool) {
			i, found := slotIndexOfName(context, spelled)
			if !found {
				return kernelbridge.LoopEffect{}, false
			}
			// arithmetic admits only the number sort
			if context.Sorts[i] == BindingKindNumber {
				return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectVar, Index: i}, true
			}
			return kernelbridge.LoopEffect{}, false
		},
		Opaque: func(e *ast.Node) (kernelbridge.LoopEffect, bool) {
			return kernelbridge.LoopEffect{}, false
		},
	})
}

// RhsEffect is rhsEffect in the TS source: an assigned RIGHT side as
// an effect — a tracked name COPIES under any sort, a string
// literal writes its exact tuple into a string-sorted slot, and
// everything else reads numerically.
func RhsEffect(context *LoweringContext, targetSort BindingKind, e *ast.Node) (kernelbridge.LoopEffect, bool) {
	if copy, ok := IndexOf(context, e); ok {
		return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectVar, Index: copy}, true
	}
	if ast.IsStringLiteral(e) && targetSort == BindingKindString {
		return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectConst, Set: refinementsets.StringTuple(e.AsStringLiteral().Text)}, true
	}
	return EffectOf(context, e)
}

var compoundOps = map[ast.Kind]kernelbridge.LoopEffectOp{
	ast.KindPlusEqualsToken:     kernelbridge.LoopOpAdd,
	ast.KindMinusEqualsToken:    kernelbridge.LoopOpSub,
	ast.KindAsteriskEqualsToken: kernelbridge.LoopOpMul,
	ast.KindSlashEqualsToken:    kernelbridge.LoopOpDiv,
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

// DeclarationAssignment is declarationAssignment in the TS source: a
// single-name declaration's target and effect.
func DeclarationAssignment(context *LoweringContext, declarations []*ast.Node) (AssignmentTarget, bool) {
	if len(declarations) != 1 {
		return AssignmentTarget{}, false
	}
	d := declarations[0].AsVariableDeclaration()
	if !ast.IsIdentifier(d.Name()) || d.Initializer == nil {
		return AssignmentTarget{}, false
	}
	target, ok := IndexOf(context, d.Name())
	if !ok {
		return AssignmentTarget{}, false
	}
	effect, ok := RhsEffect(context, context.Sorts[target], d.Initializer)
	if !ok {
		return AssignmentTarget{}, false
	}
	return AssignmentTarget{Target: target, Effect: effect}, true
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
