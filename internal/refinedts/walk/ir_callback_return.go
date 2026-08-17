// split from ir_callback_summary.go — the RETURN-position entry and the
// slot-target statements it emits: reduce, find and flatMap writing the
// body's own result slot rather than a named local
package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
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
// The ARRAY-result methods serve too, onto the "#ret.len"/"#ret.elem"
// pair the layout allocates for a body whose returns agree on the
// array shape (returnedLiteralShape's collection-call arm): `map`
// copies the source's length and lands the callback's joined image in
// the elem slot; `filter` copies the source's elements and states the
// non-negative integer count. Where the pair was never laid out —
// mixed return shapes — both decline and the opaque return stands.
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
	// `return xs.reduce(cb)` — the SEEDLESS one-argument shape: the
	// accumulator starts as the source's own ELEMENT, not a seed node.
	// Tried after the two-argument reader (which already refuses this
	// call — a different argument count) and ahead of the plain switch,
	// which has no `"reduce"` case of its own.
	if reduceSource, ok := oneArgumentReduceCallOf(returned); ok {
		return oneArgumentReduceSlotStatements(context, reduceSource, target)
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
	case "map":
		return mapPairSlotStatements(context, source)
	case "filter":
		return filterPairSlotStatements(context, source)
	}
	return nil, false
}

// retPairSlotsOf resolves the "#ret.len"/"#ret.elem" pair the layout
// allocated for an array-shaped return (returnedLiteralShape's
// collection-call arm), or declines where the body's returns did not
// agree on the array shape and the pair was never laid out.
func retPairSlotsOf(context *LoweringContext) (lenSlot int, elemSlot int, ok bool) {
	lenSlot, lenOk := slotIndexOfName(context, retLenSlotName())
	elemSlot, elemOk := slotIndexOfName(context, retElemSlotName())
	if !lenOk || !elemOk {
		return 0, 0, false
	}
	return lenSlot, elemSlot, true
}

// mapPairSlotStatements lowers `return xs.map(cb)` onto the array
// pair: the result's LENGTH is exactly the source's (sec-array.prototype.map
// builds one element per element), so "#ret.len" copies "xs.len"
// verbatim; the callback converts and its ret — the join over every
// per-element image — lands in "#ret.elem". The scalar #ret stays at
// its unknown, which is what the member-carrying shape expects.
func mapPairSlotStatements(
	context *LoweringContext,
	source collectionCall,
) ([]kernelbridge.IrStatement, bool) {
	sourceLen, sourceElem, sourceOk := arraySlotsOf(context, source.Receiver)
	if !sourceOk {
		return nil, false
	}
	lenSlot, elemSlot, pairOk := retPairSlotsOf(context)
	if !pairOk {
		return nil, false
	}
	converted, convertedOk := convertArrayArrow(context, source.Callback, sourceElem)
	if !convertedOk {
		return nil, false
	}
	call, callOk := arrowCallStatement(context, converted, elemSlot)
	if !callOk {
		return nil, false
	}
	return []kernelbridge.IrStatement{
		{Kind: kernelbridge.IrStatementAssign, Target: lenSlot, Effect: varStateEffect(sourceLen)},
		call,
	}, true
}

// filterPairSlotStatements lowers `return xs.filter(cb)` onto the
// pair: every kept element IS a source element (sec-array.prototype.filter
// selects, never transforms), so "#ret.elem" copies "xs.elem"; the
// kept COUNT is some non-negative integer the predicate decides, so
// "#ret.len" takes exactly that claim and no more. The predicate still
// converts and runs — its own answer routes nowhere, its porosity and
// effects propagate as every converted callback's do.
func filterPairSlotStatements(
	context *LoweringContext,
	source collectionCall,
) ([]kernelbridge.IrStatement, bool) {
	_, sourceElem, sourceOk := arraySlotsOf(context, source.Receiver)
	if !sourceOk {
		return nil, false
	}
	lenSlot, elemSlot, pairOk := retPairSlotsOf(context)
	if !pairOk {
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
		{Kind: kernelbridge.IrStatementAssign, Target: elemSlot, Effect: varStateEffect(sourceElem)},
		{Kind: kernelbridge.IrStatementAssign, Target: lenSlot, Effect: kernelbridge.LoopEffect{
			Kind: kernelbridge.LoopEffectConst,
			Set:  refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.Integer),
		}},
	}, true
}

// reduceSlotStatements is reduceStatements with the target given as a
// SLOT rather than a name. The fold's own argument is unchanged — the
// accumulator entry is the join of the seed's effect and the target
// slot's var, and cb's ret lands in that slot — so the two routes emit
// the same statements and differ only in how the slot was reached.
//
// An ARRAY-TYPED accumulator (`(acc: T[], x) => …`) tries FIRST, ahead
// of the scalar path below: a scalar accumulator entry over an array-
// typed parameter would enter unknown-sorted (no scalar sort spells an
// array) and nothing downstream would ever recover the shape, so the
// pair path has to be offered the callback before the scalar one
// commits to a weaker entry.
func reduceSlotStatements(
	context *LoweringContext,
	source collectionCall,
	seed *ast.Node,
	targetSlot int,
) ([]kernelbridge.IrStatement, bool) {
	if statements, ok := reduceArrayAccumulatorSlotStatements(context, source, seed, targetSlot); ok {
		return statements, true
	}
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
	accumulatorSort := accumulatorSortOfResultSlot(context, targetSlot, seedSort)
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
		{Kind: kernelbridge.IrStatementAssign, Target: targetSlot, Effect: asVarStateEffect(seedEffect)},
		call,
	}, true
}

// reduceArrayAccumulatorSlotStatements lowers `return xs.reduce((acc:
// T[], x) => …, [])` where the ACCUMULATOR is itself array-typed —
// Text.tsx's `words.reduce((result: WordsWithWidth[], w) =>
// result.concat(w), [])` shape, reduced to a bare array.
//
// Three conditions all have to hold before this offers anything over
// the scalar path, and each declines cleanly where they don't:
//
//   - the callback's own first parameter reads as an array type by
//     syntax (arrayTypeNodeElementSort) — a scalar or unannotated
//     accumulator takes the ordinary path below;
//   - the OUTER function's own return slot is ALREADY laid out as a
//     "#ret.len"/"#ret.elem" pair (retPairSlotsOf) — which only happens
//     where returnedLiteralShape's isArrayProducingCollectionCall
//     recognizes THIS call as array-producing. Today that reader knows
//     "map"/"filter" only, not "reduce" — so on the live syntax this
//     gate is always closed, and the array-typed-accumulator route
//     declines to the scalar path exactly as before. Widening that
//     recognizer to admit reduce (ir_summary_returned_shape.go, not
//     this file) is what opens it;
//   - the seed reads as the EMPTY array literal `[]` — arrayLiteralPairSeedOf,
//     the one seed shape this route spells (len {0}, elem absent); any
//     other seed expression declines, the scalar path's own seed
//     reading not being applicable to a pair target.
//
// Where all three hold: convertReduceArrowArray compiles the callback
// under the len/elem pair at entry 0, and arrowCallStatementArrayResult
// maps the callback's OWN array-shaped return (its "#ret.len"/
// "#ret.elem" — real only where the callback body's own return is
// itself recognized as array-producing, `.concat` included) onto the
// outer pair. A callback whose body does not clear THAT gate still
// converts and runs (its effects and porosity propagate as any
// converted callback's do) but arrowCallStatementArrayResult itself
// declines, and the whole statement falls back to the scalar route.
func reduceArrayAccumulatorSlotStatements(
	context *LoweringContext,
	source collectionCall,
	seed *ast.Node,
	targetSlot int,
) ([]kernelbridge.IrStatement, bool) {
	arrow := callbackFunctionOf(context, source.Callback)
	if arrow == nil || len(arrow.Parameters()) == 0 {
		return nil, false
	}
	accumulatorParam := arrow.Parameters()[0].AsParameterDeclaration()
	if accumulatorParam.Type == nil {
		return nil, false
	}
	elementSort, isArray := arrayTypeNodeElementSort(accumulatorParam.Type)
	if !isArray {
		return nil, false
	}
	// the pair belongs to THIS return's own laid-out shape, never
	// targetSlot: targetSlot is context.Result.Ret, the SCALAR #ret the
	// scalar route writes, which retPairSlotsOf's "#ret.len"/"#ret.elem"
	// rows sit BESIDE rather than at — a body whose return shape was
	// never recognized as array-producing (returnedLiteralShape) has no
	// such pair at all, which is the gate this declines on.
	lenSlot, elemSlot, pairOk := retPairSlotsOf(context)
	if !pairOk {
		return nil, false
	}
	_, sourceElem, sourceOk := arraySlotsOf(context, source.Receiver)
	if !sourceOk {
		return nil, false
	}
	seedLen, seedElemAbsent, seedOk := arrayLiteralPairSeedOf(seed)
	if !seedOk {
		return nil, false
	}
	elementTypeof := TypeofTagNone
	switch elementSort {
	case BindingKindNumber:
		elementTypeof = TypeofTagNumber
	case BindingKindString:
		elementTypeof = TypeofTagString
	}
	accumulatorLen := joinEffect(seedLen, varEffect(lenSlot))
	accumulatorElem := joinEffect(seedElemAbsent, varEffect(elemSlot))
	converted, convertedOk := convertReduceArrowArray(
		context, source.Callback, sourceElem,
		accumulatorLen, accumulatorElem,
		elementSort, elementTypeof,
	)
	if !convertedOk {
		return nil, false
	}
	call, callOk := arrowCallStatementArrayResult(context, converted, lenSlot, elemSlot)
	if !callOk {
		return nil, false
	}
	return []kernelbridge.IrStatement{
		// the pair starts at the seed, exactly as the scalar route's
		// target starts at the seed's own effect
		{Kind: kernelbridge.IrStatementAssign, Target: lenSlot, Effect: asVarStateEffect(seedLen)},
		{Kind: kernelbridge.IrStatementAssign, Target: elemSlot, Effect: asVarStateEffect(seedElemAbsent)},
		call,
	}, true
}

// arrayLiteralPairSeedOf reads a reduce seed as the len/elem pair an
// EMPTY array literal `[]` claims: length exactly 0, element absent (no
// element has ever been written). Any other seed — a non-empty literal,
// a name, a call — declines: the pair path spells no other seed shape.
func arrayLiteralPairSeedOf(seed *ast.Node) (lenEffect kernelbridge.LoopEffect, elemEffect kernelbridge.LoopEffect, ok bool) {
	head := Unwrapped(seed)
	if head == nil || !ast.IsArrayLiteralExpression(head) {
		return kernelbridge.LoopEffect{}, kernelbridge.LoopEffect{}, false
	}
	if len(head.AsArrayLiteralExpression().Elements.Nodes) != 0 {
		return kernelbridge.LoopEffect{}, kernelbridge.LoopEffect{}, false
	}
	return constNumber(0), kernelbridge.AbsentConst(), true
}

// oneArgumentReduceSlotStatements lowers `return xs.reduce(cb)` — the
// SEEDLESS form — onto the result slot. The accumulator's starting value
// is the source's own ELEMENT (sourceElem), not a seed effect: with no
// initial value, sec-array.prototype.reduce sets the accumulator to
// `xs[0]` and runs the callback from index 1, and the two-slot flattening
// already carries "every element xs can hold" in sourceElem — the exact
// join a starting value drawn from index 0 is admitted by.
//
// AN EMPTY xs THROWS on this form and is not modeled as a statement here,
// same as oneArgumentReduceCallOf's own doc: the throw ends the run before
// any later statement executes, so the summary this produces claims
// nothing about the empty-array case — the same stance held toward any
// other callee that may throw.
//
// The accumulator's SORT is the element slot's OWN sort, read directly —
// unlike the seeded route, there is no seed reading to compare it
// against, and the element slot's sort is already an established fact
// about every value the array can hold, not a placeholder.
func oneArgumentReduceSlotStatements(
	context *LoweringContext,
	source collectionCall,
	targetSlot int,
) ([]kernelbridge.IrStatement, bool) {
	_, sourceElem, sourceOk := arraySlotsOf(context, source.Receiver)
	if !sourceOk {
		return nil, false
	}
	// the same sort-agreement gate the seeded route applies: a target slot
	// already sorted differently from the element would be read under a
	// sort the accumulator's actual values do not wear
	elementSort := context.Sorts[sourceElem]
	if elementSort != BindingKindUnknown && context.Sorts[targetSlot] != BindingKindUnknown &&
		context.Sorts[targetSlot] != elementSort {
		return nil, false
	}
	accumulator := joinEffect(varEffect(sourceElem), varEffect(targetSlot))
	converted, convertedOk := convertReduceArrow(
		context, source.Callback, sourceElem,
		accumulator, elementSort, TypeofTagNone,
	)
	if !convertedOk {
		return nil, false
	}
	call, callOk := arrowCallStatement(context, converted, targetSlot)
	if !callOk {
		return nil, false
	}
	return []kernelbridge.IrStatement{
		// the slot starts at the source's own element, so the join above
		// reads a value the reduce actually had rather than the slot's
		// entry state
		{Kind: kernelbridge.IrStatementAssign, Target: targetSlot, Effect: varStateEffect(sourceElem)},
		call,
	}, true
}

// accumulatorSortOfResultSlot is the sort the reduce's accumulator entry
// wears when the target is the body's own RESULT SLOT.
//
// The named route (reduceStatements, ir_callback_scalar_statements.go)
// takes the seed's sort only where the target slot ALREADY wears it, and
// unknown otherwise. That caution is right there and wrong here, and the
// difference is what the two routes know about their slot.
//
// A NAMED target is a binding the body declared. Its sort came from its
// own declaration, other statements read and write it under that sort,
// and the join's `var` half reads whatever those statements left — so a
// sort the slot does not carry would promise the callback a reading of
// values the slot can hold and the sort does not cover.
//
// The RESULT SLOT is none of that. "#ret" is laid out with
// BindingKindUnknown unconditionally (ir_summary_body_lowering_slots.go
// appends it as `BindingKindUnknown` beside "#done"), because the layout
// runs before any statement lowers and cannot know what the body will
// return. Reading the gate literally against that slot therefore makes
// the accumulator sort unknown for EVERY return-position reduce, which
// is not a judgement about the seed — it is the layout's placeholder
// answering a question it was never asked.
//
// What this route knows instead: it WRITES the seed into "#ret" as its
// own first statement, and the only other writer of that slot is the
// callback's own ret through this same call. A return ends the statement
// list, so no earlier return wrote it either. At the join the slot holds
// the seed, or a value the callback returned from an earlier pass — and
// the callback's ret is what the seed's sort is being promised FOR. So
// the seed's sort is true of both halves of the join, which is exactly
// the condition the named route's slot check stands in for.
//
// The gate above still runs first and is unchanged: a seed whose sort a
// SORTED target does not wear declines outright. This only decides what
// an UNSORTED target — the result slot's permanent state — promises,
// and it promises the seed's own sort rather than nothing.
func accumulatorSortOfResultSlot(
	context *LoweringContext,
	targetSlot int,
	seedSort BindingKind,
) BindingKind {
	if seedSort == BindingKindUnknown {
		return BindingKindUnknown
	}
	if context.Sorts[targetSlot] == BindingKindUnknown ||
		context.Sorts[targetSlot] == seedSort {
		return seedSort
	}
	return BindingKindUnknown
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
