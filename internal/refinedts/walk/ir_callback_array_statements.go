// split from ir_callback_summary.go — the ARRAY-RESULT statements: the
// ".len"/".elem" target resolution, and map / filter / forEach over a
// flattened array receiver
package walk

import (
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// targetArraySlotsOf resolves the slots a mapped/filtered RESULT lands
// in: its "ys.len" and "ys.elem" pair, allocated where the enclosing
// layout did not lay them out. A result that is neither laid out nor
// allocatable declines the whole statement — there is nowhere to write.
//
// A target the layout gave a WHOLE-NAME scalar slot to, with no
// ".len"/".elem" pair beside it, declines rather than allocating one.
// The array recognizer refused that name — it is read somewhere as a
// whole array — so its scalar slot is what those reads resolve to, and
// this route never writes it. Allocating a pair anyway would leave the
// bare-name reads answering that slot's stale entry state while the
// array's real values sat in slots nothing consults: a wrong answer
// rather than a weak one.
func targetArraySlotsOf(context *LoweringContext, name string) (lenSlot int, elemSlot int, ok bool) {
	if lenSlot, elemSlot, found := arraySlotsOf(context, name); found {
		return lenSlot, elemSlot, true
	}
	if _, whole := slotIndexOfName(context, name); whole {
		return 0, 0, false
	}
	if context.Allocate == nil {
		return 0, 0, false
	}
	lenSlot, lenOk := slotIndexOfName(context, name+arrayLenSuffix)
	if !lenOk {
		lenSlot, lenOk = context.Allocate(name+arrayLenSuffix, BindingKindNumber, TypeofTagNumber)
		if !lenOk {
			return 0, 0, false
		}
	}
	elemSlot, elemOk := slotIndexOfName(context, name+arrayElemSuffix)
	if !elemOk {
		// the element sort is UNKNOWN: what the callback returns is the
		// kernel's answer, not something the site reads off syntax. An
		// unknown-sorted slot admits the definedness test alone, which is
		// the honest standing for a value nothing here promised a sort for.
		elemSlot, elemOk = context.Allocate(name+arrayElemSuffix, BindingKindUnknown, TypeofTagNone)
		if !elemOk {
			return 0, 0, false
		}
	}
	return lenSlot, elemSlot, true
}

// nonNegativeIntegerSet is "an integer at least 0" — what a FILTER's
// result length is known to be and no more. The two-slot flattening has
// no spelling for "at most the source's length": that upper bound is a
// relation between two slots, and a stored set relates a slot to
// constants alone. The precision is honestly lost here rather than
// claimed.
func nonNegativeIntegerSet() refinementsets.RefinedSet {
	return refinementsets.MakeRefinedSet(refinementsets.Integer, refinementsets.AtLeast(0))
}

// mapStatements is `ys = xs.map(cb)` over a flattened source: the
// result's length is the source's exactly (map preserves length), and
// the result's element is cb's return.
//
// The one call statement covers the WHOLE traversal. The source's
// element slot holds the join of every element the array can hold, and
// cb's summary quantifies over all entries — so applying cb at that
// join answers a set covering cb's image of each individual element.
// Every per-pass image is admitted by the one answer.
func mapStatements(
	context *LoweringContext,
	source collectionCall,
	target string,
) ([]kernelbridge.IrStatement, bool) {
	sourceLen, sourceElem, sourceOk := arraySlotsOf(context, source.Receiver)
	if !sourceOk {
		return nil, false
	}
	converted, convertedOk := convertArrayArrow(context, source.Callback, sourceElem)
	if !convertedOk {
		return nil, false
	}
	targetLen, targetElem, targetOk := targetArraySlotsOf(context, target)
	if !targetOk {
		return nil, false
	}
	call, callOk := arrowCallStatement(context, converted, targetElem)
	if !callOk {
		return nil, false
	}
	return []kernelbridge.IrStatement{
		{Kind: kernelbridge.IrStatementAssign, Target: targetLen, Effect: varStateEffect(sourceLen)},
		call,
	}, true
}

// filterStatements is `ys = xs.filter(cb)`: every surviving element is
// one the source held, so the result's element slot copies the source's
// — and the result's length is an integer ≥ 0, the honest loss above.
//
// cb still has to CONVERT even though its truthiness answer is not
// modeled. The conversion is not there to read the predicate; it is
// there so an effectful callback cannot hide: a cb that writes a
// capture, reads an unslotted name, or leaves the lowered subset
// declines the whole statement rather than passing as a pure predicate.
func filterStatements(
	context *LoweringContext,
	source collectionCall,
	target string,
) ([]kernelbridge.IrStatement, bool) {
	_, sourceElem, sourceOk := arraySlotsOf(context, source.Receiver)
	if !sourceOk {
		return nil, false
	}
	if _, convertedOk := convertArrayArrow(context, source.Callback, sourceElem); !convertedOk {
		return nil, false
	}
	targetLen, targetElem, targetOk := targetArraySlotsOf(context, target)
	if !targetOk {
		return nil, false
	}
	return []kernelbridge.IrStatement{
		{Kind: kernelbridge.IrStatementAssign, Target: targetElem, Effect: varStateEffect(sourceElem)},
		{
			Kind:   kernelbridge.IrStatementAssign,
			Target: targetLen,
			Effect: kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectConst, Set: nonNegativeIntegerSet()},
		},
	}, true
}

// forEachStatement is `xs.forEach(cb)`: the single call statement at
// the element entry with NO ret — forEach's own value is undefined and
// nothing reads it.
//
// Nothing rides back into the caller's slots, and that is by rule
// rather than by omission: captures are READ-ONLY, so a cb that would
// write one declined at conversion. What the cb's body does beyond its
// own calls touches only the cb's own slots, which end with the
// application.
func forEachStatement(context *LoweringContext, source collectionCall) ([]kernelbridge.IrStatement, bool) {
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
	return []kernelbridge.IrStatement{call}, true
}
