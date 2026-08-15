// split from ir_map_slots.go — the mutation lowerings
//
// What `m.set(k, v)` / `s.add(v)`, `m.delete(k)` / `s.delete(v)`, and
// `m.clear()` / `s.clear()` write into the slots: the joined size
// readings and the weak updates of the value and key summaries for a
// set, the non-negative integer ray for a delete, and the fresh-empty
// state a clear resets every slot to.

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

// MapClearAssignmentsOf is `m.clear()` / `s.clear()` as a statement: the
// SAME state a fresh `new Map()` / `new Set()` declaration writes —
// size becomes the exact constant zero, and the value (and key) slots
// take the absent-carrying constant, exactly as joinedSeedEffect answers
// for an empty seed. Unlike a delete, clear is unambiguous: the spec
// (Map.prototype.clear / Set.prototype.clear) empties the collection
// outright, no miss case to join against, so the old readings do not
// ride — clear REPLACES rather than joins.
func MapClearAssignmentsOf(context *LoweringContext, statement *ast.Node) ([]AssignmentTarget, bool) {
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
	if !isCall || method != "clear" || len(arguments) != 0 {
		return nil, false
	}
	sizeSlot, valsSlot, keysSlot, keysOk, ok := mapSlotsOf(context, receiver.Text())
	if !ok {
		return nil, false
	}
	out := []AssignmentTarget{
		{Target: sizeSlot, Effect: constNumber(0)},
		{Target: valsSlot, Effect: kernelbridge.AbsentConst()},
	}
	if keysOk {
		out = append(out, AssignmentTarget{Target: keysSlot, Effect: kernelbridge.AbsentConst()})
	}
	return out, true
}

// getOrInsertSlotEffectsOf is the three slot writes `m.getOrInsert(k,
// v)` makes, given the call node and the Map's spelled receiver name —
// exactly MapSetAssignmentsOf's writes: the size steps only where k was
// absent, so both readings ride the same join a plain `set` makes; keys
// and vals take the same weak update `set` makes. getOrInsert differs
// from `set` only in what it hands BACK, which the caller below reads
// out of the vals slot the same way pushSlotEffectsOf hands back the
// len slot for `a.push(v)` to read.
//
// A get-or-default shape ONLY — getOrInsertComputed's second argument
// is a callback, not a plain value, and reading what it returns needs
// the callback-summary machinery ir_callback_summary.go already builds
// for forEach; that machinery sits outside this file and is not
// duplicated here, so getOrInsertComputed is not recognized by this
// reader.
func getOrInsertSlotEffectsOf(context *LoweringContext, call *ast.Node, name string) (assignments []AssignmentTarget, valsSlot int, ok bool) {
	if !ast.IsCallExpression(call) {
		return nil, 0, false
	}
	access := call.AsCallExpression().Expression
	if !ast.IsPropertyAccessExpression(access) {
		return nil, 0, false
	}
	receiver := access.AsPropertyAccessExpression().Expression
	if !ast.IsIdentifier(receiver) || receiver.Text() != name {
		return nil, 0, false
	}
	method, arguments, isCall := collectionMethodCallOf(call, name)
	if !isCall || method != "getOrInsert" || len(arguments) != 2 {
		return nil, 0, false
	}
	sizeSlot, valsSlotIndex, keysSlot, keysOk, slotsOk := mapSlotsOf(context, name)
	// getOrInsert is a Map operation only — a Set has no key half and no
	// getOrInsert of its own
	if !slotsOk || !keysOk {
		return nil, 0, false
	}
	keyArgument, defaultArgument := arguments[0], arguments[1]
	written, writtenOk := RhsEffect(context, context.Sorts[valsSlotIndex], defaultArgument)
	if !writtenOk {
		return nil, 0, false
	}
	key, keyOk := RhsEffect(context, context.Sorts[keysSlot], keyArgument)
	if !keyOk {
		return nil, 0, false
	}
	one := constNumber(1)
	sizeVar := varEffect(sizeSlot)
	stepped := kernelbridge.LoopEffect{
		Kind: kernelbridge.LoopEffectBinary, Op: kernelbridge.LoopOpAdd, A: &sizeVar, B: &one,
	}
	return []AssignmentTarget{
		{Target: sizeSlot, Effect: joinEffect(varEffect(sizeSlot), stepped)},
		{Target: valsSlotIndex, Effect: joinEffect(varEffect(valsSlotIndex), written)},
		{Target: keysSlot, Effect: joinEffect(varEffect(keysSlot), key)},
	}, valsSlotIndex, true
}

// MapGetOrInsertAssignmentsOf is `m.getOrInsert(k, v)` in a statement
// position: as a bare expression statement, and as the right side of a
// declaration (`const r = m.getOrInsert(k, v)`) or an assignment
// (`r = m.getOrInsert(k, v)`).
//
// getOrInsert ANSWERS the key's stored value where k was already
// present, or v itself where it was inserted — the honest claim is the
// JOIN of the vals slot's OLD reading (what k might already have held)
// with v's own reading, which is exactly what the vals slot holds AFTER
// the weak update getOrInsertSlotEffectsOf makes. So the value form
// lowers as the three slot writes followed by `r := var m.vals`,
// reading the updated slot the writes just established — mirroring
// ArrayPushAssignmentsOf's read of the stepped len slot for `a.push(v)`.
func MapGetOrInsertAssignmentsOf(context *LoweringContext, statement *ast.Node) ([]AssignmentTarget, bool) {
	// `m.getOrInsert(k, v);` — the return value discarded
	if ast.IsExpressionStatement(statement) {
		e := Unwrapped(statement.AsExpressionStatement().Expression)
		if name, isReceiver := getOrInsertReceiverOf(e); isReceiver {
			assignments, _, ok := getOrInsertSlotEffectsOf(context, e, name)
			if ok {
				return assignments, true
			}
		}
		// `r = m.getOrInsert(k, v);` — the result written into a tracked name
		if ast.IsBinaryExpression(e) {
			bin := e.AsBinaryExpression()
			if bin.OperatorToken.Kind == ast.KindEqualsToken {
				target, spelled := SpelledNameOf(Unwrapped(bin.Left))
				if spelled {
					if assignments, ok := getOrInsertValueAssignmentsOf(context, bin.Right, target); ok {
						return assignments, true
					}
				}
			}
		}
		return nil, false
	}
	// `const r = m.getOrInsert(k, v);` — the same, spelled as a declaration
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
	return getOrInsertValueAssignmentsOf(context, declaration.Initializer, declaration.Name().Text())
}

// getOrInsertReceiverOf is the Map name a call expression reads
// `getOrInsert` on — `m` in `m.getOrInsert(k, v)` — or ("", false) for
// anything that is not a property-access call on a plain name.
func getOrInsertReceiverOf(call *ast.Node) (string, bool) {
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

// getOrInsertValueAssignmentsOf is a getOrInsert whose RESULT is
// written into the spelled target name: the call's own slot writes,
// then the target taking the vals slot's var — the joined stored-or-
// inserted reading, which is what getOrInsert answers.
//
// Declines where the target has no slot of its own, or where its sort
// does not match the Map's value sort.
func getOrInsertValueAssignmentsOf(context *LoweringContext, value *ast.Node, target string) ([]AssignmentTarget, bool) {
	call := Unwrapped(value)
	name, isReceiver := getOrInsertReceiverOf(call)
	if !isReceiver {
		return nil, false
	}
	assignments, valsSlot, ok := getOrInsertSlotEffectsOf(context, call, name)
	if !ok {
		return nil, false
	}
	targetSlot, targetOk := slotIndexOfName(context, target)
	if !targetOk {
		return nil, false
	}
	if context.Sorts[targetSlot] != context.Sorts[valsSlot] {
		return nil, false
	}
	return append(assignments, AssignmentTarget{Target: targetSlot, Effect: varEffect(valsSlot)}), true
}
