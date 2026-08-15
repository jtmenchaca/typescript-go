// split from ir_callback_summary.go — the SCALAR-RESULT statements: the
// whole-name target resolution, find / reduce / flatMap over a flattened
// array, and the bare-position traversal whose result is discarded
package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// scalarTargetSlotOf resolves the slot a NON-ARRAY result lands in: the
// one `find`, `reduce`, and `flatMap` write. Where the enclosing layout
// already laid the name out, that slot is it; otherwise one is
// allocated under the name itself.
//
// The allocated sort is UNKNOWN, which is the same honesty
// targetArraySlotsOf's element allocation keeps: what the callback
// answers is the kernel's, not something this site reads off syntax. An
// unknown-sorted slot admits the definedness test alone.
//
// A name the layout already gave a FLATTENED family to — a record's
// leaves, an array's ".len"/".elem" pair, a collection's size/vals/keys,
// a promise's ".inner" — declines rather than allocating a whole-name
// slot beside it. This is the mirror of targetArraySlotsOf's rule and it
// exists for the same reason: the reads of that name resolve to the
// family, so a fresh whole-name slot would hold the result where nothing
// consults it while those reads went on answering the family's own
// values. Declining leaves the statement to its havoc floor, which is
// weak rather than wrong.
func scalarTargetSlotOf(context *LoweringContext, name string) (int, bool) {
	if slot, found := slotIndexOfName(context, name); found {
		return slot, true
	}
	if len(flattenedSlotsUnder(context, name)) > 0 {
		return 0, false
	}
	if context.Allocate == nil {
		return 0, false
	}
	return context.Allocate(name, BindingKindUnknown, TypeofTagNone)
}

// findStatements is `ys = xs.find(cb)` over a flattened array: the
// callback runs on the element join for its EFFECTS — its truthiness
// answer is not what find hands back — and the result is an element the
// array held OR undefined, which is exactly the or-absent effect over
// the source's element slot.
//
// The precision this keeps is real and the precision it drops is named.
// Kept: every value `ys` can hold is one `xs.elem` can hold, or absent,
// so a later `if (ys !== undefined)` narrows to the element set. Dropped:
// nothing says WHICH element, and nothing says the predicate held of it
// — a `find(x => x > 10)` result is not narrowed to "> 10" here, because
// the two-slot flattening carries the elements' join and not a
// per-element relation to a predicate's answer.
//
// cb must still CONVERT, for the same reason filter's must: a callback
// that writes a capture or leaves the lowered subset declines the whole
// statement rather than passing as a pure predicate.
//
// The two slots must AGREE ON SORT. What lands in the target is the
// source's element values, so a target the layout sorted differently
// would be read under a sort the values it now holds do not wear —
// `const first = words.find(cb)` over a string-sorted array writes word
// tuples, and the layout sorts a call-initialized local as a number
// (LocalSort reads the initializer's syntax, and a call is not one of
// the shapes it rules out), which would then admit `first` into
// arithmetic. That is a wrong answer rather than a weak one, so the
// mismatch declines. An UNKNOWN-sorted target takes the write either
// way: unknown promises no reading, so nothing can be read out of it
// under the wrong one.
func findStatements(
	context *LoweringContext,
	source collectionCall,
	target string,
) ([]kernelbridge.IrStatement, bool) {
	_, sourceElem, sourceOk := arraySlotsOf(context, source.Receiver)
	if !sourceOk {
		return nil, false
	}
	converted, convertedOk := convertArrayArrow(context, source.Callback, sourceElem)
	if !convertedOk {
		return nil, false
	}
	targetSlot, targetOk := scalarTargetSlotOf(context, target)
	if !targetOk {
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

// seedEffectOf reads a reduce SEED and reports the sort the reading
// committed to. The seed is the one value the site names outright, so
// both worlds of the effect grammar are tried rather than one: the
// SEQUENCE reading first, which answers a string literal's exact tuple
// and a string-sorted name's read, then the numeric reading, which
// answers a number, a boolean, and everything else RhsEffect reads.
//
// Reading the seed at the unknown sort alone is what starved
// `xs.reduce((acc, w) => acc + w, "")`: RhsEffect's string-literal and
// sequence arms both stand behind a string-sorted target, so a string
// seed fell to the numeric reader, which has no spelling for a word,
// and the whole statement declined. A number seed lowered all along.
//
// The sort answers unknown wherever the numeric reader took a value
// that is not a spelled number — the reading is honest either way, and
// the caller only ever narrows the accumulator entry with a sort it can
// also see on the target slot.
func seedEffectOf(context *LoweringContext, seed *ast.Node) (kernelbridge.LoopEffect, BindingKind, bool) {
	if sequence, ok := SequenceEffectOf(context, seed); ok {
		return sequence, BindingKindString, true
	}
	effect, ok := RhsEffect(context, BindingKindUnknown, seed)
	if !ok {
		return kernelbridge.LoopEffect{}, BindingKindUnknown, false
	}
	// a spelled number or boolean is a number-sorted seed; a tracked name
	// wears whatever its own slot wears, and absent wears nothing
	if head := Unwrapped(seed); head != nil {
		if slot, tracked := IndexOf(context, head); tracked {
			return effect, context.Sorts[slot], true
		}
		if _, isNumber := NumberOf(head); ast.IsNumericLiteral(head) || isNumber ||
			head.Kind == ast.KindTrueKeyword || head.Kind == ast.KindFalseKeyword {
			return effect, BindingKindNumber, true
		}
	}
	return effect, BindingKindUnknown, true
}

// reduceStatements is `ys = xs.reduce(cb, seed)` over a flattened
// array: ONE call statement whose accumulator entry covers the
// accumulator at EVERY pass, with cb's ret landing in the target.
//
// The fold is not unrolled, and the argument for why one application
// suffices is the join-of-elements argument one level up. The
// accumulator entry is filled with the JOIN of the seed's own effect
// and the target slot's var — and the target slot is where cb's ret
// lands, so after the statement the slot holds cb's image of that join.
// Every intermediate accumulator the real fold produces is either the
// seed (pass 0) or a value cb returned from an earlier pass, and both
// are admitted by that join; cb's summary quantifies over all entries,
// so applying it at the join covers cb's image of each intermediate.
//
// The target slot is ASSIGNED THE SEED first, so the var read in the
// join is not the slot's stale entry state. Reading a slot that held
// something unrelated would make the join a claim about a value the
// reduce never had.
//
// The accumulator's SORT is the seed's, and only where the TARGET SLOT
// wears that sort too. The entry is a join of the seed and the target
// slot's var, so the sort has to be one both halves can be read under;
// where the slot disagrees the entry takes unknown, which costs
// arithmetic inside cb and claims nothing about what the slot holds.
func reduceStatements(
	context *LoweringContext,
	source collectionCall,
	seed *ast.Node,
	target string,
) ([]kernelbridge.IrStatement, bool) {
	_, sourceElem, sourceOk := arraySlotsOf(context, source.Receiver)
	if !sourceOk {
		return nil, false
	}
	targetSlot, targetOk := scalarTargetSlotOf(context, target)
	if !targetOk {
		return nil, false
	}
	seedEffect, seedSort, seedOk := seedEffectOf(context, seed)
	if !seedOk {
		return nil, false
	}
	// the seed is written into the target slot below, so a seed whose sort
	// the slot does not wear declines: a word tuple in a number-sorted slot
	// would be admitted into arithmetic by every reader that consults the
	// sort. An unknown-sorted slot takes any seed — unknown promises no
	// reading, so nothing is read out of it under the wrong one.
	if seedSort != BindingKindUnknown && context.Sorts[targetSlot] != BindingKindUnknown &&
		context.Sorts[targetSlot] != seedSort {
		return nil, false
	}
	// the accumulator entry: the seed, joined with whatever cb's ret put
	// in the target on an earlier pass
	accumulator := joinEffect(seedEffect, varEffect(targetSlot))
	// the accumulator's sort is the SEED's, and only where the target slot
	// wears it too: the join above reads that slot, so a sort the slot
	// does not carry would promise cb a reading of a value the slot cannot
	// hold. The gate above already refused a disagreement, so what is left
	// to rule out is an unknown-sorted slot, whose var half of the join
	// carries no reading for the seed's sort to stand on.
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

// flatMapStatements is `ys = xs.flatMap(cb)` over a flattened array:
// the callback converts and RUNS, and the result answers unknown.
//
// The result shape has no spelling here. flatMap concatenates cb's
// per-element arrays one level down, so `ys.elem` would have to be the
// element of cb's RETURNED array — and cb's ret is one slot holding
// that array as a whole, which the two-slot flattening never opened.
// Claiming `ys.elem := cb's ret` would say the elements of ys are the
// ARRAYS cb returned, which is wrong rather than weak.
//
// So the target takes unknown and the conversion is kept for its own
// sake: cb's body lowers, its calls compose, and a cb that writes a
// capture or leaves the subset declines the statement instead of
// slipping past unread. That is the whole reason this method is
// recognized at all — the effects are what is worth having, and the
// result honestly says nothing.
func flatMapStatements(
	context *LoweringContext,
	source collectionCall,
	target string,
) ([]kernelbridge.IrStatement, bool) {
	_, sourceElem, sourceOk := arraySlotsOf(context, source.Receiver)
	if !sourceOk {
		return nil, false
	}
	converted, convertedOk := convertArrayArrow(context, source.Callback, sourceElem)
	if !convertedOk {
		return nil, false
	}
	targetSlot, targetOk := scalarTargetSlotOf(context, target)
	if !targetOk {
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

// convertDiscardedArrow converts a bare-position traversal's callback
// under the layout its own METHOD spells: reduce's shifted entries with
// an absent accumulator, and the ordinary element-then-index layout for
// every other method. Reading the method here is what keeps the two
// layouts from being chosen by anything but the method's own signature.
func convertDiscardedArrow(context *LoweringContext, source collectionCall, elementSlot int) (convertedArrow, bool) {
	if source.Method != "reduce" {
		return convertArrayArrow(context, source.Callback, elementSlot)
	}
	return convertReduceArrow(
		context, source.Callback, elementSlot,
		kernelbridge.AbsentConst(), BindingKindUnknown, TypeofTagNone,
	)
}

// discardedResultStatement is a traversal in BARE EXPRESSION position —
// `xs.find(cb);`, `xs.map(cb);`, `xs.reduce(cb, seed);` — where the
// method's own answer is thrown away. The traversal still runs, so the
// callback still runs, and the one call statement at the element entry
// is exactly what forEach's route emits: no ret, nothing written.
//
// Running it is what keeps an unconvertible callback honest. Without
// this arm the statement declined at the receiver and fell to the havoc
// floor, which havocs the mentioned slots and never asks whether the
// callback converts — so a cb that writes a capture or leaves the
// lowered subset passed unread, indistinguishable from one that
// converts cleanly. Here it declines the statement instead, and a cb
// that does convert contributes its calls to the body.
//
// REDUCE keeps its own shifted layout here, with the accumulator entry
// ABSENT. With no target slot there is nothing for cb's ret to land in,
// so the join that fills that entry in the assigning route has no slot
// to read — and converting reduce's cb under the ordinary array layout
// instead would put the ELEMENT where the accumulator belongs, which
// describes entry 0 as a value it never holds. An absent entry promises
// nothing, so cb's reads of the accumulator answer unknown: weaker than
// the assigning route, and true.
func discardedResultStatement(context *LoweringContext, source collectionCall) ([]kernelbridge.IrStatement, bool) {
	_, sourceElem, sourceOk := arraySlotsOf(context, source.Receiver)
	if !sourceOk {
		return nil, false
	}
	converted, convertedOk := convertDiscardedArrow(context, source, sourceElem)
	if !convertedOk {
		return nil, false
	}
	call, callOk := arrowCallStatement(context, converted, -1)
	if !callOk {
		return nil, false
	}
	return []kernelbridge.IrStatement{call}, true
}
