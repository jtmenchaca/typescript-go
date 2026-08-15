// split from ir_map_slots.go — the declaration lowering
//
// What `const m = new Map(…)` / `new Set(…)` writes into the slots: the
// seed's row count into the size slot and the join of the seed's
// effects into the value and key slots, or — for a copy — the sibling's
// own three slots read var for var.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// MapDeclarationAssignmentsOf is the lowering-side entry for a
// flattened collection's declaration: the size slot takes the seed's
// row count as an exact constant, and the value (and key) slots take the
// JOIN of the seed's effects. An EMPTY construction writes the absent-
// carrying constant into the value and key slots — there is nothing to
// read, so a get must produce undefined, and the absent flag is where
// that lives. This mirrors the empty-array-literal treatment exactly.
func MapDeclarationAssignmentsOf(context *LoweringContext, statement *ast.Node) ([]AssignmentTarget, bool) {
	if !ast.IsVariableStatement(statement) {
		return nil, false
	}
	declarations := statement.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes
	if len(declarations) != 1 {
		return nil, false
	}
	declaration := declarations[0]
	isMap, seed, isConstruction := collectionConstructionOf(declaration)
	if !isConstruction {
		return nil, false
	}
	if !ast.IsIdentifier(declaration.AsVariableDeclaration().Name()) {
		return nil, false
	}
	name := declaration.AsVariableDeclaration().Name().Text()
	sizeSlot, valsSlot, keysSlot, keysOk, ok := mapSlotsOf(context, name)
	if !ok || keysOk != isMap {
		return nil, false
	}
	// the COPY, tried first: `const n = new Map(m)` writes this
	// collection's slots from the sibling's own — plain slot reads, so
	// what the copy's readers see afterwards is indistinguishable from a
	// seeded construction's lowering.
	if copied, isCopy := copiedDeclarationAssignmentsOf(context, seed, sizeSlot, valsSlot, keysSlot, keysOk); isCopy {
		return copied, true
	}
	keys, vals, seedOk := seedEntriesOf(seed, isMap)
	if !seedOk {
		return nil, false
	}
	out := []AssignmentTarget{{Target: sizeSlot, Effect: constNumber(float64(len(vals)))}}
	valsEffect, valsEffectOk := joinedSeedEffect(context, valsSlot, vals)
	if !valsEffectOk {
		return nil, false
	}
	out = append(out, AssignmentTarget{Target: valsSlot, Effect: valsEffect})
	if isMap {
		keysEffect, keysEffectOk := joinedSeedEffect(context, keysSlot, keys)
		if !keysEffectOk {
			return nil, false
		}
		out = append(out, AssignmentTarget{Target: keysSlot, Effect: keysEffect})
	}
	return out, true
}

// copiedDeclarationAssignmentsOf is the COPY's lowering: `const n = new
// Map(m)` writes `n.size := var m.size`, `n.vals := var m.vals`, and —
// for a Map — `n.keys := var m.keys`. Three ordinary slot reads: the new
// collection holds exactly what the old one held, so every later
// `n.size`, `n.get(k)`, `n.set(k, v)` and iteration over `n` reads the
// same shapes it would over a seeded collection.
//
// The syntax alone decides here, as everywhere in the lowering: the
// recognizer's admission is already recorded in the slot vector, so the
// gate is that both collections' slots resolve and their key halves
// agree — a Map copied from a Set, or the reverse, has no matching slot
// family to read.
func copiedDeclarationAssignmentsOf(context *LoweringContext, seed *ast.Node, sizeSlot int, valsSlot int, keysSlot int, keysOk bool) ([]AssignmentTarget, bool) {
	source, isCopy := copySourceOf(seed)
	if !isCopy {
		return nil, false
	}
	sourceSize, sourceVals, sourceKeys, sourceKeysOk, found := mapSlotsOf(context, source)
	if !found {
		return nil, false
	}
	// a Map's keys slot has no counterpart in a Set's family, and a copy
	// across the two kinds reads values that are not the shape the slots
	// hold
	if sourceKeysOk != keysOk {
		return nil, false
	}
	out := []AssignmentTarget{
		{Target: sizeSlot, Effect: varEffect(sourceSize)},
		{Target: valsSlot, Effect: varEffect(sourceVals)},
	}
	if keysOk {
		out = append(out, AssignmentTarget{Target: keysSlot, Effect: varEffect(sourceKeys)})
	}
	return out, true
}

// joinedSeedEffect is a seed row's expressions joined into one effect
// for their slot, or the absent-carrying constant where the seed is
// empty.
func joinedSeedEffect(context *LoweringContext, slot int, entries []*ast.Node) (kernelbridge.LoopEffect, bool) {
	if len(entries) == 0 {
		return kernelbridge.AbsentConst(), true
	}
	var joined kernelbridge.LoopEffect
	for index, entry := range entries {
		effect, ok := RhsEffect(context, context.Sorts[slot], entry)
		if !ok {
			return kernelbridge.LoopEffect{}, false
		}
		if index == 0 {
			joined = effect
			continue
		}
		joined = joinEffect(joined, effect)
	}
	return joined, true
}
