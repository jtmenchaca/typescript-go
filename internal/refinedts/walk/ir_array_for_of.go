// split from ir_array_slots.go — `for (const x of a)` over a flattened
// array, lowered as the ordinary loop

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// forOfElementSlotOf is the slot a for-of hands its per-pass value to,
// read from the loop's initializer position: a FRESH single binding
// (`for (const x of a)`), or a name declared OUTSIDE the loop that the
// pass assigns into (`for (x of a)`). Both name one tracked slot, and
// the per-pass effect written into it is the same either way.
//
// Declines on a destructuring pattern in either spelling — the elem slot
// holds one scalar, and a pattern reads into a shape it does not have —
// and on a multi-declarator list, which the for-of grammar does not
// produce anyway.
func forOfElementSlotOf(context *LoweringContext, initializer *ast.Node) (int, bool) {
	if initializer == nil {
		return 0, false
	}
	if ast.IsVariableDeclarationList(initializer) {
		declarations := initializer.AsVariableDeclarationList().Declarations.Nodes
		if len(declarations) != 1 {
			return 0, false
		}
		elementName := declarations[0].AsVariableDeclaration().Name()
		if !ast.IsIdentifier(elementName) {
			return 0, false
		}
		return slotIndexOfName(context, elementName.Text())
	}
	// `for (x of a)` — the loop writes an already-declared name, which is
	// a slot exactly as a fresh binding is
	target, spelled := SpelledNameOf(Unwrapped(initializer))
	if !spelled {
		return 0, false
	}
	return slotIndexOfName(context, target)
}

// ArrayForOfLowering is a `for (const x of a)` over a flattened array,
// lowered as the ordinary loop: the element binding's per-pass effect
// is the elem slot's var (every pass hands it one element, and the slot
// holds the join of all of them), and the rest of the body folds
// exactly as any loop body does. NO numeric head bounds the loop — the
// trip count is the array's length, which the two-slot flattening does
// not relate to any binding — so every cond and after entry stays nil
// and the solver certifies whatever the body's effects support.
//
// The element binding may be FRESH (`for (const x of a)`) or a name
// declared outside the loop (`for (x of a)`) — both name one slot the
// pass writes, and forOfElementSlotOf reads either spelling.
//
// Declines where the element binding is not a plain single name, where
// the array is not flattened, or where the body leaves the fold's
// grammar.
func ArrayForOfLowering(context *LoweringContext, statement *ast.Node) (kernelbridge.IrStatement, bool) {
	if !ast.IsForOfStatement(statement) {
		return kernelbridge.IrStatement{}, false
	}
	forOf := statement.AsForInOrOfStatement()
	// `for await (… of …)` awaits each element — the value the binding
	// takes is the awaited one, which the element slot does not hold
	if forOf.AwaitModifier != nil {
		return kernelbridge.IrStatement{}, false
	}
	iterated := Unwrapped(forOf.Expression)
	if !ast.IsIdentifier(iterated) {
		return kernelbridge.IrStatement{}, false
	}
	_, elemSlot, ok := arraySlotsOf(context, iterated.Text())
	if !ok {
		return kernelbridge.IrStatement{}, false
	}
	elementSlot, elementOk := forOfElementSlotOf(context, forOf.Initializer)
	if !elementOk {
		return kernelbridge.IrStatement{}, false
	}
	// the fold starts every binding at its own var, then the element
	// binding takes the elem slot's — the per-pass value — and the body's
	// statements fold over that
	current := make([]kernelbridge.LoopEffect, len(context.Bindings))
	for index := range current {
		current[index] = varEffect(index)
	}
	if elementSlot >= len(current) || elemSlot >= len(current) {
		return kernelbridge.IrStatement{}, false
	}
	current[elementSlot] = varEffect(elemSlot)
	if !FoldBody(context, StatementsOf(forOf.Statement), current) {
		return kernelbridge.IrStatement{}, false
	}
	written := make([]bool, len(current))
	for index, effect := range current {
		written[index] = !(effect.Kind == kernelbridge.LoopEffectVar && effect.Index == index)
	}
	return kernelbridge.IrStatement{
		Kind:    kernelbridge.IrStatementLoop,
		Written: written,
		Cond:    make([]*refinementsets.RefinedSet, len(current)),
		After:   make([]*refinementsets.RefinedSet, len(current)),
		Body:    current,
	}, true
}
