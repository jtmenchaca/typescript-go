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
// A pushed value with NO scalar effect spelling — a call
// (`result.push(getThing())`, tmp/recharts-src/src/util shapes over a
// loop-collected array) — still joins, with UNKNOWN standing in for the
// value RhsEffect cannot spell: the call is not an effect-grammar leaf
// at all (RhsEffect's own dispatch has no call arm), but the length
// arithmetic and the OTHER pushed values' exactness do not depend on
// this one value's spelling, so declining the whole push over one
// unspellable argument would lose knowledge the other slots never owed
// to it. Sound only where the unspellable argument is itself
// write-and-call-free over what it hands the call — a nested write
// inside it (`getThing((total = total + 1))`) still declines the whole
// push, since an unknown VALUE is not the same claim as "moved nothing
// else," and this route has no reading for the second.
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
			// RhsEffect declined — the value has no effect-grammar spelling.
			// Sound to join UNKNOWN instead, but only where the argument
			// itself moves nothing else: writeAndCallFree already refuses a
			// WRITE inside it, and a nested CALL is exactly what RhsEffect
			// just declined on, so the one remaining question is whether
			// evaluating it could write a tracked slot — importedHookArgumentsFree
			// answers that for a call argument the same way it does for an
			// imported-hook call's own arguments (every one of ITS arguments
			// write-and-call-free, or a write-free function literal).
			if !pushedValueMovesNothingElse(context, argument) {
				return nil, 0, false
			}
			pushed = unknownEffect
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
// two slot writes followed by `n := a verbatim copy of a.len`, reading
// the length the two writes just established. The assignments emit in
// order, so the read lands after the step.
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
// taking a verbatim copy of the len slot — the new length, which is
// what push answers.
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
	return append(assignments, AssignmentTarget{Target: targetSlot, Effect: varStateEffect(lenSlot)}), true
}

// pushedValueMovesNothingElse answers whether a pushed argument RhsEffect
// declined on (no scalar spelling) is still safe to join as unknown: the
// argument's own evaluation must move no tracked slot. A CALL argument
// (the common shape) is safe exactly when EVERY one of ITS arguments is
// write-and-call-free or a write-free function literal —
// importedHookArgumentsFree's own test, reused rather than
// re-implemented so the two routes' idea of "free" cannot drift apart.
// Any other shape RhsEffect declines on (a `new`, an await) falls back
// to the plain writeAndCallFree reading, which still refuses a call
// nested inside it — sound but narrower, since this route does not need
// to serve every unspellable shape, only the one the census names.
func pushedValueMovesNothingElse(context *LoweringContext, argument *ast.Node) bool {
	unwrapped := Unwrapped(argument)
	if unwrapped != nil && ast.IsCallExpression(unwrapped) {
		var callArguments []*ast.Node
		if a := unwrapped.AsCallExpression().Arguments; a != nil {
			callArguments = a.Nodes
		}
		return importedHookArgumentsFree(context, callArguments)
	}
	return writeAndCallFree(argument)
}
