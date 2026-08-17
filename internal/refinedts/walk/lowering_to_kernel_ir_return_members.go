// split from lowering_to_kernel_ir.go — the returned value's member slots

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

/* ── the returned value's members ────────────────────────────────── */

// returnMemberStatements lowers a return whose value is the LITERAL the
// layout allocated member slots for: each member's own effect written
// into its own slot, then the scalar #ret written unknown and the flag
// raised.
//
// #ret stays UNKNOWN, and that is not a loss here. The object itself has
// no scalar spelling — it never had one — and the members now carry what
// the caller actually reads. A caller taking the direct apply route
// rebuilds the object from the member exits (applySummary); one taking
// the statement route keeps reading #ret and gets the same unknown it
// always got, so nothing that worked before reads differently.
//
// A MEMBER the effect grammar cannot spell takes unknown in ITS OWN slot
// rather than refusing the whole return — a partial object beats a whole
// unknown, and unknown in one member claims nothing about that member
// while the readable ones keep their values.
//
// What this does NOT do is run code. A member whose value expression
// would MOVE something — a call, a `new`, a write — is not lowered as an
// effect at all: the effect grammar has no statement position inside it,
// so those members take unknown and the statement's own mention havoc is
// what covers what they moved. A literal whose evaluation is not
// write-and-call free therefore declines back to the caller's routes,
// where the opaque return's havoc floor serves it exactly as before.
func returnMemberStatements(
	context *LoweringContext,
	returned *ast.Node,
	raise kernelbridge.IrStatement,
) ([]kernelbridge.IrStatement, bool) {
	if context == nil || context.Result == nil || returned == nil {
		return nil, false
	}
	if context.RetShape == RetShapeNone || len(context.RetMembers) == 0 {
		return nil, false
	}
	head := Unwrapped(returned)
	if head == nil {
		return nil, false
	}
	// the members' values are read as EFFECTS, which have no room for a
	// statement — so a literal that runs code keeps the floor that covers
	// what it ran
	if !writeAndCallFree(head) {
		return nil, false
	}
	switch context.RetShape {
	case RetShapeObject:
		if !ast.IsObjectLiteralExpression(head) {
			return nil, false
		}
		return objectReturnMemberStatements(context, head, raise), true
	case RetShapeArray:
		if !ast.IsArrayLiteralExpression(head) {
			return nil, false
		}
		return arrayReturnMemberStatements(context, head, raise), true
	}
	return nil, false
}

// objectReturnMemberStatements writes each key of a returned object
// literal into the slot the layout gave it.
//
// A key this literal does NOT spell is left alone: its slot keeps
// whatever the path it is on left there, which for a body's single return
// is the absent entry state — "this returned object has no such key" —
// and for one arm of a several-arm body is the join the exits carry.
// Writing absent here would say the same thing on the arms that omit the
// key; leaving it says it without an extra statement.
func objectReturnMemberStatements(
	context *LoweringContext,
	literal *ast.Node,
	raise kernelbridge.IrStatement,
) []kernelbridge.IrStatement {
	var out []kernelbridge.IrStatement
	written := map[int]struct{}{}
	for _, property := range literal.AsObjectLiteralExpression().Properties.Nodes {
		name, named := retMemberNameOf(property)
		if !named {
			continue
		}
		slot, held := RetMemberSlotOf(context, name)
		if !held {
			continue
		}
		value := retMemberValueOf(property)
		effect := unknownEffect
		if value != nil {
			// the member's own slot sort decides the reading, the way the
			// scalar ret's sort decides the whole-value one
			if read, ok := RhsEffect(context, context.Sorts[slot], value); ok {
				effect = asVarStateEffect(read)
			}
		}
		out = append(out, kernelbridge.IrStatement{
			Kind: kernelbridge.IrStatementAssign, Target: slot, Effect: effect,
		})
		written[slot] = struct{}{}
	}
	// a member slot ANOTHER arm spells and this one does not must not keep
	// a value this path never wrote — the slot is one binding across the
	// whole body, so a write on an earlier statement would otherwise be
	// read as this return's member. Absent is what this path says about a
	// key its literal has no property for.
	for _, entry := range context.RetMembers {
		if _, already := written[entry.Index]; already {
			continue
		}
		out = append(out, kernelbridge.IrStatement{
			Kind: kernelbridge.IrStatementAssign, Target: entry.Index, Effect: kernelbridge.AbsentConst(),
		})
	}
	// the object value itself has no scalar spelling; the members carry it
	out = append(out, kernelbridge.IrStatement{
		Kind: kernelbridge.IrStatementAssign, Target: context.Result.Ret, Effect: unknownEffect,
	})
	return append(out, raise)
}

// retMemberValueOf is the expression one literal property holds: the
// property assignment's initializer, or — for a shorthand — the name
// itself, which reads as the local of that name.
func retMemberValueOf(property *ast.Node) *ast.Node {
	if ast.IsPropertyAssignment(property) {
		return property.AsPropertyAssignment().Initializer
	}
	if ast.IsShorthandPropertyAssignment(property) {
		return property.AsShorthandPropertyAssignment().Name()
	}
	return nil
}

// arrayReturnMemberStatements writes the returned array literal's LENGTH
// — exact, the element count, since the shape reader refused every
// spread — and the JOIN of its elements into the ".elem" slot.
//
// The element slot takes the same WEAK UPDATE a flattened local array's
// element slot takes (ir_array_slots.go's convention): one slot stands
// for every position, so it must hold something true of them all. The
// join is built as a branch over the elements — each arm writing one
// element's effect — which is exactly how the exits join, so `[a, b]`
// leaves ".elem" holding a value true of both. An element the effect
// grammar cannot spell makes the whole join unknown: an arm claiming
// nothing joins to nothing.
func arrayReturnMemberStatements(
	context *LoweringContext,
	literal *ast.Node,
	raise kernelbridge.IrStatement,
) []kernelbridge.IrStatement {
	elements := literal.AsArrayLiteralExpression().Elements.Nodes
	var out []kernelbridge.IrStatement
	if lenSlot, held := RetMemberSlotOf(context, "len"); held {
		out = append(out, kernelbridge.IrStatement{
			Kind:   kernelbridge.IrStatementAssign,
			Target: lenSlot,
			Effect: kernelbridge.LoopEffect{
				Kind: kernelbridge.LoopEffectConst,
				Set:  refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{float64(len(elements))})),
			},
		})
	}
	if elemSlot, held := RetMemberSlotOf(context, "elem"); held {
		out = append(out, elementJoinAssignments(context, elemSlot, elements)...)
	}
	out = append(out, kernelbridge.IrStatement{
		Kind: kernelbridge.IrStatementAssign, Target: context.Result.Ret, Effect: unknownEffect,
	})
	return append(out, raise)
}

// elementJoinAssignments writes the JOIN of a literal's elements into one
// slot, as nested branches whose arms each write one element. The kernel
// joins branch arms by the proved exact join, so the slot ends holding a
// value true of every element — the weak update an array's one element
// slot needs.
//
// The branch is the BRANCH-BOTH shape (IrStatementBranchBoth): it tests
// nothing, walks both arms and joins them. An EMPTY literal writes
// absent — `[]` has no element, and absent is what a read of one would
// find.
func elementJoinAssignments(
	context *LoweringContext,
	elemSlot int,
	elements []*ast.Node,
) []kernelbridge.IrStatement {
	assign := func(effect kernelbridge.LoopEffect) kernelbridge.IrStatement {
		return kernelbridge.IrStatement{Kind: kernelbridge.IrStatementAssign, Target: elemSlot, Effect: effect}
	}
	if len(elements) == 0 {
		return []kernelbridge.IrStatement{assign(kernelbridge.AbsentConst())}
	}
	effectOf := func(element *ast.Node) kernelbridge.LoopEffect {
		if ast.IsOmittedExpression(element) {
			return kernelbridge.AbsentConst()
		}
		if read, ok := RhsEffect(context, context.Sorts[elemSlot], element); ok {
			return asVarStateEffect(read)
		}
		return unknownEffect
	}
	// one element: no join to build, the slot simply holds it
	out := []kernelbridge.IrStatement{assign(effectOf(elements[0]))}
	for _, element := range elements[1:] {
		// each further element joins in as the other arm of a condition
		// nothing reads — the kernel walks both and joins them, which is
		// the weak update this slot needs
		out = []kernelbridge.IrStatement{{
			Kind: kernelbridge.IrStatementBranchBoth,
			Then: out,
			Else: []kernelbridge.IrStatement{assign(effectOf(element))},
		}}
	}
	return out
}
