// split from ir_map_slots.go — the mutation lowerings
//
// What `m.set(k, v)` / `s.add(v)` and `m.delete(k)` / `s.delete(v)`
// write into the slots: the joined size readings and the weak updates
// of the value and key summaries for a set, and the non-negative
// integer ray for a delete.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// MapSetAssignmentsOf is `m.set(k, v)` / `s.add(v)` as a statement.
//
// The size: a set on a key the collection ALREADY holds overwrites and
// leaves the count where it was; a set on a fresh key steps it by one.
// Nothing in the two-slot world tells the two apart, so BOTH readings
// ride — the join of the old size with the stepped one. `s.add(v)` has
// the same overwrite behaviour (adding a member twice keeps one) and
// takes the same join.
//
// The values and keys: the weak update, exactly as an array's push —
// after the set the slot holds everything the collection could hold,
// which is what a get or an iteration may answer.
func MapSetAssignmentsOf(context *LoweringContext, statement *ast.Node) ([]AssignmentTarget, bool) {
	if !ast.IsExpressionStatement(statement) {
		return nil, false
	}
	call := Unwrapped(statement.AsExpressionStatement().Expression)
	if !ast.IsCallExpression(call) {
		return nil, false
	}
	access := call.AsCallExpression().Expression
	if !ast.IsPropertyAccessExpression(access) {
		return nil, false
	}
	receiver := access.AsPropertyAccessExpression().Expression
	if !ast.IsIdentifier(receiver) {
		return nil, false
	}
	method, arguments, isCall := collectionMethodCallOf(call, receiver.Text())
	if !isCall {
		return nil, false
	}
	sizeSlot, valsSlot, keysSlot, keysOk, ok := mapSlotsOf(context, receiver.Text())
	if !ok {
		return nil, false
	}
	var keyArgument, valueArgument *ast.Node
	switch method {
	case "set":
		// a Map operation: the keys slot must exist
		if !keysOk || len(arguments) != 2 {
			return nil, false
		}
		keyArgument, valueArgument = arguments[0], arguments[1]
	case "add":
		// a Set operation: there is no keys slot
		if keysOk || len(arguments) != 1 {
			return nil, false
		}
		valueArgument = arguments[0]
	default:
		return nil, false
	}
	written, writtenOk := RhsEffect(context, context.Sorts[valsSlot], valueArgument)
	if !writtenOk {
		return nil, false
	}
	one := constNumber(1)
	sizeVar := varEffect(sizeSlot)
	stepped := kernelbridge.LoopEffect{
		Kind: kernelbridge.LoopEffectBinary, Op: kernelbridge.LoopOpAdd, A: &sizeVar, B: &one,
	}
	out := []AssignmentTarget{
		{Target: sizeSlot, Effect: joinEffect(varEffect(sizeSlot), stepped)},
		{Target: valsSlot, Effect: joinEffect(varEffect(valsSlot), written)},
	}
	if keyArgument != nil {
		key, keyOk := RhsEffect(context, context.Sorts[keysSlot], keyArgument)
		if !keyOk {
			return nil, false
		}
		out = append(out, AssignmentTarget{Target: keysSlot, Effect: joinEffect(varEffect(keysSlot), key)})
	}
	return out, true
}

// nonNegativeIntegers is the honest floor a delete leaves behind: the
// integers from zero up. A delete that MISSES leaves the count where it
// was and a delete that hits drops it by one, and the two-slot world has
// no spelling for "the old reading, or one less, but never below zero" —
// the decrement is not claimable. Joining this ray with the old reading
// keeps the miss admitted and never claims a value the collection cannot
// have.
func nonNegativeIntegers() kernelbridge.LoopEffect {
	return kernelbridge.LoopEffect{
		Kind: kernelbridge.LoopEffectConst,
		Set:  refinementsets.MakeRefinedSet(refinementsets.Integer, refinementsets.AtLeast(0)),
	}
}

// MapDeleteAssignmentsOf is `m.delete(k)` / `s.delete(v)` as a
// statement: the size slot takes the non-negative integer ray joined
// with its old reading, and the value and key slots are untouched —
// removal never adds a value, so the joined summaries stay sound where
// they stand.
//
// The precise decrement is deliberately NOT claimed: a delete on a key
// the collection does not hold answers false and moves nothing, and
// nothing here tells that case from the hit.
func MapDeleteAssignmentsOf(context *LoweringContext, statement *ast.Node) ([]AssignmentTarget, bool) {
	if !ast.IsExpressionStatement(statement) {
		return nil, false
	}
	call := Unwrapped(statement.AsExpressionStatement().Expression)
	if !ast.IsCallExpression(call) {
		return nil, false
	}
	access := call.AsCallExpression().Expression
	if !ast.IsPropertyAccessExpression(access) {
		return nil, false
	}
	receiver := access.AsPropertyAccessExpression().Expression
	if !ast.IsIdentifier(receiver) {
		return nil, false
	}
	method, arguments, isCall := collectionMethodCallOf(call, receiver.Text())
	if !isCall || method != "delete" || len(arguments) != 1 {
		return nil, false
	}
	sizeSlot, _, _, _, ok := mapSlotsOf(context, receiver.Text())
	if !ok {
		return nil, false
	}
	return []AssignmentTarget{
		{Target: sizeSlot, Effect: joinEffect(nonNegativeIntegers(), varEffect(sizeSlot))},
	}, true
}
