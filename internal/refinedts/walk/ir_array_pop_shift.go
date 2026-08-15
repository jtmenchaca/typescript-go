// split from ir_array_slots.go — `a.pop()` / `a.shift()`: the
// shrinking pair's slot writes and the value they answer.
//
// Both remove one element and answer it, or answer undefined over an
// empty array (sec-array.prototype.pop steps 3-4, 6-9;
// sec-array.prototype.shift steps 3-4, and the element-return steps
// that follow it in the same clause) — the same VALUE shape
// ArrayIndexReadEffect already wears for an unguarded `a[i]`: the elem
// slot's var, or-absent. The elem slot itself is UNCHANGED — it is a
// weak join over everything the array ever held, and removing one
// occurrence never shrinks what a later read may still answer, so
// leaving it alone (rather than trying to narrow it) is the sound
// reading, exactly as ArrayIndexWriteOf leaves the len slot alone for
// the opposite reason.
//
// The len slot DOES change: `max(0, len - 1)`, spelled with the
// kernel's own binary transfers over the len slot's var and the
// constants 0 and 1 — the same `LoopOp2.apply` family push's
// increment rides, read the other way. A len slot only ever holds
// AtLeast-0 integers, so this effect's own enclosure is the honest
// one: max clamps the -1 case (an empty array) to 0, and otherwise
// answers len-1 exactly.
package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// shrinkLenEffect is `max(0, len - 1)`: the len slot's honest new
// value after a pop or a shift, clamped at zero the way the spec's own
// `length = 0` branch already answers zero rather than a negative
// count (sec-array.prototype.pop step 3.a; sec-array.prototype.shift
// carries the same zero-length branch under LengthOfArrayLike).
func shrinkLenEffect(lenSlot int) kernelbridge.LoopEffect {
	decremented := arrayLenMinusOne(lenSlot)
	zero := constNumber(0)
	return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectBinary, Op: kernelbridge.LoopOpMax, A: &zero, B: &decremented}
}

// shrinkSlotEffectsOf is the two slot writes `a.pop()` / `a.shift()`
// make: the len slot steps down (clamped at zero), the elem slot is
// untouched. Answers the two assignments and the elem slot, which the
// value-form readers below read back out as the removed element.
func shrinkSlotEffectsOf(context *LoweringContext, name string) (assignments []AssignmentTarget, elemSlot int, ok bool) {
	lenSlot, elemSlot, slotsOk := arraySlotsOf(context, name)
	if !slotsOk {
		return nil, 0, false
	}
	return []AssignmentTarget{
		{Target: lenSlot, Effect: shrinkLenEffect(lenSlot)},
	}, elemSlot, true
}

// shrinkValueAssignmentsOf is a pop/shift whose RESULT is written into
// the spelled target name: the shrink's own len write, then the target
// taking the elem slot's or-absent reading — the removed element, or
// undefined over an empty array, which is what ArrayIndexReadEffect's
// unguarded branch already answers for "some element or nothing proved
// about position".
//
// Declines where the target has no slot of its own, or where its sort
// disagrees with the element sort — the same rule pushValueAssignmentsOf
// applies for the length, applied here for the element.
func shrinkValueAssignmentsOf(context *LoweringContext, call *ast.Node, target string) ([]AssignmentTarget, bool) {
	name, method, isShrink := shrinkReceiverOf(call)
	if !isShrink {
		return nil, false
	}
	_ = method
	assignments, elemSlot, ok := shrinkSlotEffectsOf(context, name)
	if !ok {
		return nil, false
	}
	targetSlot, targetOk := slotIndexOfName(context, target)
	if !targetOk {
		return nil, false
	}
	if context.Sorts[targetSlot] != BindingKindUnknown && context.Sorts[targetSlot] != context.Sorts[elemSlot] {
		return nil, false
	}
	elem := varEffect(elemSlot)
	orAbsent := kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectOrAbsent, A: &elem}
	return append(assignments, AssignmentTarget{Target: targetSlot, Effect: orAbsent}), true
}

// shrinkReceiverOf is the array name and method a call expression
// shrinks — `("a", "pop")` for `a.pop()` — through arrayShrinkCallOf,
// which already gates the zero-argument, non-optional shape.
func shrinkReceiverOf(call *ast.Node) (name string, method string, ok bool) {
	head := Unwrapped(call)
	if !ast.IsCallExpression(head) {
		return "", "", false
	}
	access := head.AsCallExpression().Expression
	if !ast.IsPropertyAccessExpression(access) {
		return "", "", false
	}
	receiver := access.AsPropertyAccessExpression().Expression
	if !ast.IsIdentifier(receiver) {
		return "", "", false
	}
	method, isShrink := arrayShrinkCallOf(head, receiver.Text())
	if !isShrink {
		return "", "", false
	}
	return receiver.Text(), method, true
}

// ArrayShrinkAssignmentsOf is `a.pop()` / `a.shift()` in a statement
// position: as a bare expression statement (the removed value
// discarded — the len write still has to happen), and as the right
// side of a declaration (`const v = a.pop()`) or an assignment
// (`v = a.pop()`).
//
// Mirrors ArrayPushAssignmentsOf's three-shape dispatch exactly, one
// direction reversed: push answers the length AFTER its step; pop and
// shift answer the ELEMENT, which the elem slot already held before
// the len step and holds unchanged after it, so no ordering between
// the two slot writes matters here the way it matters for push's
// length read.
func ArrayShrinkAssignmentsOf(context *LoweringContext, statement *ast.Node) ([]AssignmentTarget, bool) {
	if ast.IsExpressionStatement(statement) {
		e := Unwrapped(statement.AsExpressionStatement().Expression)
		if name, _, isShrink := shrinkReceiverOf(e); isShrink {
			assignments, _, ok := shrinkSlotEffectsOf(context, name)
			if ok {
				return assignments, true
			}
		}
		if ast.IsBinaryExpression(e) {
			bin := e.AsBinaryExpression()
			if bin.OperatorToken.Kind == ast.KindEqualsToken {
				target, spelled := SpelledNameOf(Unwrapped(bin.Left))
				if spelled {
					if assignments, ok := shrinkValueAssignmentsOf(context, bin.Right, target); ok {
						return assignments, true
					}
				}
			}
		}
		return nil, false
	}
	if !ast.IsVariableStatement(statement) {
		return nil, false
	}
	declarations := statement.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes
	if len(declarations) != 1 {
		return nil, false
	}
	declaration := declarations[0].AsVariableDeclaration()
	if declaration.Initializer == nil || !ast.IsIdentifier(declaration.Name()) {
		return nil, false
	}
	return shrinkValueAssignmentsOf(context, declaration.Initializer, declaration.Name().Text())
}
