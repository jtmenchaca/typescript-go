// split from ir_array_slots.go — `a.push(v, …)`: the two slot writes it
// makes and the new length it answers

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// pushSlotEffectsOf is the two slot writes a `a.push(v, …)` call makes,
// given the call node and the array's spelled receiver name: the len
// slot steps by the ARGUMENT COUNT and the elem slot JOINS every pushed
// value onto its old reading. The join is the weak update — after the
// push the slot holds everything the array could hold, which is exactly
// what an index read may answer.
//
// Answers the two assignments and the len slot, which the expression
// form below reads the new length back out of.
func pushSlotEffectsOf(context *LoweringContext, call *ast.Node, name string) (assignments []AssignmentTarget, lenSlot int, ok bool) {
	arguments, isPush := pushCallOf(call, name)
	if !isPush {
		return nil, 0, false
	}
	lenSlot, elemSlot, slotsOk := arraySlotsOf(context, name)
	if !slotsOk {
		return nil, 0, false
	}
	// every argument joins onto the element slot's old reading, left to
	// right — the same weak update the one-argument push made, applied
	// once per pushed value
	joined := varEffect(elemSlot)
	for _, argument := range arguments {
		pushed, pushedOk := RhsEffect(context, context.Sorts[elemSlot], argument)
		if !pushedOk {
			return nil, 0, false
		}
		joined = joinEffect(joined, pushed)
	}
	// the count steps by exactly as many values as were pushed
	step := constNumber(float64(len(arguments)))
	lenVar := varEffect(lenSlot)
	return []AssignmentTarget{
		{
			Target: lenSlot,
			Effect: kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectBinary, Op: kernelbridge.LoopOpAdd, A: &lenVar, B: &step},
		},
		{Target: elemSlot, Effect: joined},
	}, lenSlot, true
}

// pushReceiverOf is the array name a call expression pushes onto —
// `a` in `a.push(v)` — or ("", false) for anything that is not a
// property-access call on a plain name.
func pushReceiverOf(call *ast.Node) (string, bool) {
	if !ast.IsCallExpression(call) {
		return "", false
	}
	access := call.AsCallExpression().Expression
	if !ast.IsPropertyAccessExpression(access) {
		return "", false
	}
	receiver := access.AsPropertyAccessExpression().Expression
	if !ast.IsIdentifier(receiver) {
		return "", false
	}
	return receiver.Text(), true
}

// ArrayPushAssignmentsOf is `a.push(v, …)` in a statement position: as a
// bare expression statement, and as the right side of a declaration
// (`const n = a.push(v)`) or an assignment (`n = a.push(v)`).
//
// push ANSWERS the array's new length, which is exactly what the len
// slot holds after the step — so the value form lowers as the push's own
// two slot writes followed by `n := var a.len`, reading the length the
// two writes just established. The assignments emit in order, so the
// read lands after the step.
func ArrayPushAssignmentsOf(context *LoweringContext, statement *ast.Node) ([]AssignmentTarget, bool) {
	// `a.push(v);` — the return value discarded
	if ast.IsExpressionStatement(statement) {
		e := Unwrapped(statement.AsExpressionStatement().Expression)
		if name, isReceiver := pushReceiverOf(e); isReceiver {
			assignments, _, ok := pushSlotEffectsOf(context, e, name)
			if ok {
				return assignments, true
			}
		}
		// `n = a.push(v);` — the new length written into a tracked name
		if ast.IsBinaryExpression(e) {
			bin := e.AsBinaryExpression()
			if bin.OperatorToken.Kind == ast.KindEqualsToken {
				target, spelled := SpelledNameOf(Unwrapped(bin.Left))
				if spelled {
					if assignments, ok := pushValueAssignmentsOf(context, bin.Right, target); ok {
						return assignments, true
					}
				}
			}
		}
		return nil, false
	}
	// `const n = a.push(v);` — the same, spelled as a declaration
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
	return pushValueAssignmentsOf(context, declaration.Initializer, declaration.Name().Text())
}

// pushValueAssignmentsOf is a push whose RESULT is written into the
// spelled target name: the push's own slot writes, then the target
// taking the len slot's var — the new length, which is what push
// answers.
//
// Declines where the target has no slot of its own, or where its sort is
// not the number sort the length is read as.
func pushValueAssignmentsOf(context *LoweringContext, value *ast.Node, target string) ([]AssignmentTarget, bool) {
	call := Unwrapped(value)
	name, isReceiver := pushReceiverOf(call)
	if !isReceiver {
		return nil, false
	}
	assignments, lenSlot, ok := pushSlotEffectsOf(context, call, name)
	if !ok {
		return nil, false
	}
	targetSlot, targetOk := slotIndexOfName(context, target)
	if !targetOk {
		return nil, false
	}
	// the length is a count; a target the slot vector reads as a sequence
	// has no reading for it
	if context.Sorts[targetSlot] != BindingKindNumber {
		return nil, false
	}
	return append(assignments, AssignmentTarget{Target: targetSlot, Effect: varEffect(lenSlot)}), true
}
