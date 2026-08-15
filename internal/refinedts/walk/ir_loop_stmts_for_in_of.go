// split from ir_loop_stmts.go — the for-of/for-in head: its iterable
// read for inertness, its binding written unknown at the top of every
// trip

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// lowerForInOrOfStatements is `for (x of xs)` and `for (k in o)`: the
// binding written unknown at the top of every trip, then the body.
//
// `for await (const x of xs)` is admitted too. The awaited value has no
// spelling the slots carry, and unknown is exactly what this form
// writes for the bound name either way — the await changes what the
// value IS, not what is known about it.
func lowerForInOrOfStatements(
	context *LoweringContext, statement *ast.Node,
) ([]kernelbridge.IrStatement, kernelbridge.IrStatement, bool) {
	forInOrOf := statement.AsForInOrOfStatement()
	// the ITERABLE is evaluated once, before the first trip, and this
	// form reads nothing of it — so it must move nothing
	if !writeAndCallFree(forInOrOf.Expression) {
		return nil, kernelbridge.IrStatement{}, false
	}
	binding, bindingOk := forOfBindingAssignments(context, forInOrOf.Initializer)
	if !bindingOk {
		return nil, kernelbridge.IrStatement{}, false
	}
	body, bodyOk := LowerStatements(context, StatementsOf(forInOrOf.Statement))
	if !bodyOk {
		return nil, kernelbridge.IrStatement{}, false
	}
	// the bound names take their per-trip value FIRST: the body reads
	// them, so the write has to stand ahead of it
	return nil, loopStmtsStatement(append(binding, body...)), true
}

// forOfBindingAssignments is what one trip writes for a for-of/for-in
// head, ahead of the body.
//
// A DECLARATION head (`for (const x of xs)`, `for (const [k, v] of m)`)
// writes UNKNOWN into every bound name that has a slot: the iterated
// value has no spelling here, and unknown claims nothing about it. A
// bound name with NO slot needs no write — nothing lowered can read it
// later either.
//
// An EXPRESSION head (`for (this.x of xs)`) writes through the ordinary
// assignment target resolution, and declines where the target does not
// resolve: the real run writes that place on every trip, and a loop
// that skipped the write would leave the walk claiming an old value.
func forOfBindingAssignments(
	context *LoweringContext, initializer *ast.Node,
) ([]kernelbridge.IrStatement, bool) {
	if initializer == nil {
		return nil, false
	}
	if ast.IsVariableDeclarationList(initializer) {
		declarations := initializer.AsVariableDeclarationList().Declarations.Nodes
		if len(declarations) != 1 {
			return nil, false
		}
		name := declarations[0].AsVariableDeclaration().Name()
		if name == nil {
			return nil, false
		}
		var spelled []string
		switch {
		case ast.IsIdentifier(name):
			spelled = []string{name.Text()}
		case ast.IsObjectBindingPattern(name), ast.IsArrayBindingPattern(name):
			spelled = boundPatternNames(name)
		default:
			return nil, false
		}
		slots := map[int]struct{}{}
		for _, text := range spelled {
			if slot, found := slotIndexOfName(context, text); found {
				slots[slot] = struct{}{}
			}
			// a flattened local bound by the head — its leaves move too
			for _, leaf := range flattenedSlotsUnder(context, text) {
				if leaf >= 0 {
					slots[leaf] = struct{}{}
				}
			}
		}
		return havocAssignments(slots), true
	}
	// the expression head: `for (this.x of xs)`, `for (a[i] of xs)`
	slot, tracked := IndexOf(context, Unwrapped(initializer))
	if !tracked {
		return nil, false
	}
	return []kernelbridge.IrStatement{{
		Kind:   kernelbridge.IrStatementAssign,
		Target: slot,
		Effect: unknownEffect,
	}}, true
}
