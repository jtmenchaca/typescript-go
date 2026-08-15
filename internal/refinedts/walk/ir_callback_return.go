// split from ir_callback_summary.go — the RETURN-position entry and the
// slot-target statements it emits: reduce, find and flatMap writing the
// body's own result slot rather than a named local
package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// SummaryCallbackReturnOf is the lowering-side entry for the RETURN
// position: `return xs.reduce(cb, seed)`, `return xs.find(cb)`,
// `return xs.flatMap(cb)` over a flattened array receiver. It answers
// the statements that leave the method's result in the slot the caller
// names, which is the body's own result slot rather than a local.
//
// Why this is a second entry rather than a case in
// SummaryCallbackStatementOf. That entry resolves its target through a
// NAME — callbackAssignmentNameOf hands back the identifier a
// declaration or an assignment writes, and scalarTargetSlotOf /
// targetArraySlotsOf turn that name into slots, allocating where the
// layout laid none out. A return writes no name. Its target is a slot
// the body already owns, so the name-resolving half of the route has
// nothing to resolve and the slot-writing half is all that applies.
// Routing a return through the name entry would mean inventing a name
// for the result slot, which the layout would then answer for
// inconsistently; taking the slot directly is the same lowering with
// the resolution step removed.
//
// Only the SCALAR-result methods serve here. A returned `xs.map(cb)` or
// `xs.filter(cb)` is an ARRAY, and the two-slot flattening writes an
// array into a ".len"/".elem" pair — which the result slot has not got.
// Writing cb's element image into the bare ret slot would say the
// returned value IS one element rather than the array of them, which is
// wrong rather than weak, so those two are left to the opaque return's
// unknown. The row they leave in the report names them.
//
// Total-or-decline, exactly as the statement entry is: an unflattened
// receiver, an unconvertible callback, or a seed the result slot's sort
// cannot carry answers false, and the return then takes its own routes
// and, failing those, the opaque return.
func SummaryCallbackReturnOf(context *LoweringContext, returned *ast.Node) ([]kernelbridge.IrStatement, bool) {
	if context == nil || returned == nil || context.Result == nil {
		return nil, false
	}
	target := context.Result.Ret
	if target < 0 {
		return nil, false
	}
	// `return xs.reduce(cb, seed)` — the two-argument shape, read by its
	// own reader ahead of the one-argument switch, exactly as the
	// statement entry orders them
	if reduceSource, seed, ok := reduceCallOf(returned); ok {
		return reduceSlotStatements(context, reduceSource, seed, target)
	}
	source, sourceOk := collectionCallOf(returned)
	if !sourceOk {
		return nil, false
	}
	switch source.Method {
	case "find":
		return findSlotStatements(context, source, target)
	case "flatMap":
		return flatMapSlotStatements(context, source, target)
	}
	return nil, false
}

// reduceSlotStatements is reduceStatements with the target given as a
// SLOT rather than a name. The fold's own argument is unchanged — the
// accumulator entry is the join of the seed's effect and the target
// slot's var, and cb's ret lands in that slot — so the two routes emit
// the same statements and differ only in how the slot was reached.
func reduceSlotStatements(
	context *LoweringContext,
	source collectionCall,
	seed *ast.Node,
	targetSlot int,
) ([]kernelbridge.IrStatement, bool) {
	_, sourceElem, sourceOk := arraySlotsOf(context, source.Receiver)
	if !sourceOk {
		return nil, false
	}
	seedEffect, seedSort, seedOk := seedEffectOf(context, seed)
	if !seedOk {
		return nil, false
	}
	// the seed is written into the target slot below, so a seed whose
	// sort the slot does not wear declines — the same gate the named
	// route applies, for the same reason: a word tuple left in a
	// number-sorted slot would be admitted into arithmetic by every
	// reader that consults the sort
	if seedSort != BindingKindUnknown && context.Sorts[targetSlot] != BindingKindUnknown &&
		context.Sorts[targetSlot] != seedSort {
		return nil, false
	}
	accumulator := joinEffect(seedEffect, varEffect(targetSlot))
	accumulatorSort := BindingKindUnknown
	if seedSort != BindingKindUnknown && context.Sorts[targetSlot] == seedSort {
		accumulatorSort = seedSort
	}
	converted, convertedOk := convertReduceArrow(
		context, source.Callback, sourceElem,
		accumulator, accumulatorSort, TypeofTagNone,
	)
	if !convertedOk {
		return nil, false
	}
	call, callOk := arrowCallStatement(context, converted, targetSlot)
	if !callOk {
		return nil, false
	}
	return []kernelbridge.IrStatement{
		// the slot starts at the seed, so the join above reads a value the
		// reduce actually had rather than the slot's entry state
		{Kind: kernelbridge.IrStatementAssign, Target: targetSlot, Effect: seedEffect},
		call,
	}, true
}

// findSlotStatements is findStatements with the target given as a SLOT.
// The result is an element the array held OR undefined — the or-absent
// effect over the source's element slot — and the sort gate is the named
// route's: a target sorted differently from the element would be read
// under a sort the values it now holds do not wear.
func findSlotStatements(
	context *LoweringContext,
	source collectionCall,
	targetSlot int,
) ([]kernelbridge.IrStatement, bool) {
	_, sourceElem, sourceOk := arraySlotsOf(context, source.Receiver)
	if !sourceOk {
		return nil, false
	}
	converted, convertedOk := convertArrayArrow(context, source.Callback, sourceElem)
	if !convertedOk {
		return nil, false
	}
	if context.Sorts[targetSlot] != BindingKindUnknown &&
		context.Sorts[targetSlot] != context.Sorts[sourceElem] {
		return nil, false
	}
	// the predicate runs, and its own answer goes nowhere
	call, callOk := arrowCallStatement(context, converted, -1)
	if !callOk {
		return nil, false
	}
	element := varEffect(sourceElem)
	return []kernelbridge.IrStatement{
		call,
		{
			Kind:   kernelbridge.IrStatementAssign,
			Target: targetSlot,
			Effect: kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectOrAbsent, A: &element},
		},
	}, true
}

// flatMapSlotStatements is flatMapStatements with the target given as a
// SLOT. The callback converts and RUNS — which is the whole value of
// recognizing the method — and the result answers unknown, since the
// concatenated array cb's per-element arrays make has no spelling the
// two-slot flattening opened.
func flatMapSlotStatements(
	context *LoweringContext,
	source collectionCall,
	targetSlot int,
) ([]kernelbridge.IrStatement, bool) {
	_, sourceElem, sourceOk := arraySlotsOf(context, source.Receiver)
	if !sourceOk {
		return nil, false
	}
	converted, convertedOk := convertArrayArrow(context, source.Callback, sourceElem)
	if !convertedOk {
		return nil, false
	}
	call, callOk := arrowCallStatement(context, converted, -1)
	if !callOk {
		return nil, false
	}
	return []kernelbridge.IrStatement{
		call,
		{Kind: kernelbridge.IrStatementAssign, Target: targetSlot, Effect: unknownEffect},
	}, true
}
