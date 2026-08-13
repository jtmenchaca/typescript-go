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
	numberSlot := func(i int) (kernelbridge.LoopEffect, bool) {
		// arithmetic admits only the number sort
		if context.Sorts[i] == BindingKindNumber {
			return varEffect(i), true
		}
		return kernelbridge.LoopEffect{}, false
	}
	return LowerEffectExpression(e, EffectReader{
		ReadPlace: func(spelled string) (kernelbridge.LoopEffect, bool) {
			i, found := slotIndexOfName(context, spelled)
			if !found {
				return kernelbridge.LoopEffect{}, false
			}
			return numberSlot(i)
		},
		// a DEEP record path (`p.a.b`), an array's `a.length`, and an
		// index read `a[i]` are all ordinary slot reads once the
		// flattenings gave them slots
		ReadNode: func(node *ast.Node) (kernelbridge.LoopEffect, bool) {
			if i, ok := PathSlotIndexOf(context, node); ok {
				return numberSlot(i)
			}
			if i, ok := ArrayLengthSlotOf(context, node); ok {
				return numberSlot(i)
			}
			// a deep access whose last step is a GETTER
			// (`this.holder.value`) never reaches Opaque —
			// LowerEffectExpression's deep-path arm answers ReadNode
			// alone — so the getter route is tried here too
			if held, ok := GetterReadEffect(context, node); ok {
				return held, true
			}
			return kernelbridge.LoopEffect{}, false
		},
		Opaque: func(node *ast.Node) (kernelbridge.LoopEffect, bool) {
			// `a[i]`: the element slot, or-absent where nothing bounds i.
			// Arithmetic admits only a number-sorted element slot, the
			// same gate every other read here wears.
			if slot, ok := ArrayElementSlotOf(context, node); ok && context.Sorts[slot] == BindingKindNumber {
				return ArrayIndexReadEffect(context, node)
			}
			// `m.get(k)`: the collection's values slot, always or-absent
			// (no per-key knowledge can rule the miss out), under the
			// same number-sort gate
			if slot, ok := MapValueSlotOf(context, node); ok && context.Sorts[slot] == BindingKindNumber {
				return MapGetReadEffect(context, node)
			}
			// `this.value` where value is a GETTER: the read runs a body,
			// so it hoists as a zero-argument call exactly as an explicit
			// call does. Ahead of HoistCallEffect, which reads only a
			// CallExpression and has no reading for a property access.
			if held, ok := GetterReadEffect(context, node); ok {
				return held, true
			}
			// a CALL inside the expression — `count + this.bump()`, an
			// argument, a ternary arm: it HOISTS to a temp-slot call
			// statement emitted before this statement, and the expression
			// reads the temp. Gated on a statement stream existing and on
			// the reordering being observable by nothing
			// (ir_call_hoist.go); a refusal reads exactly as it did before
			// the route existed.
			return HoistCallEffect(context, node)
		},
	})
}

// IsAbsentKeyword is whether an expression spells the ABSENT value:
// `null` or `undefined`. JavaScript's two absent spellings are one
// outcome kernel-side (KnownState's absent flag conflates them), so
// both lower to the same state constant.
//
// `undefined` is an ordinary identifier in the grammar, not a keyword
// token — a local named `undefined` would shadow it, so the tracked
// slots are consulted first by the caller (a tracked name COPIES) and
// only a free spelling reaches here.
func IsAbsentKeyword(e *ast.Node) bool {
	head := Unwrapped(e)
	if head.Kind == ast.KindNullKeyword {
		return true
	}
	return ast.IsIdentifier(head) && head.Text() == "undefined"
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
