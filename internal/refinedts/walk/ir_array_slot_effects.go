// split from ir_array_slots.go — resolving a spelled array to its two
// slots, and the three effect one-liners every lowering reaches for

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// arraySlotsOf resolves a spelled array name to its two slots, or
// declines: a name with no "a.len"/"a.elem" pair is not a flattened
// array here.
func arraySlotsOf(context *LoweringContext, name string) (lenSlot int, elemSlot int, ok bool) {
	lenSlot, lenOk := slotIndexOfName(context, name+arrayLenSuffix)
	elemSlot, elemOk := slotIndexOfName(context, name+arrayElemSuffix)
	if !lenOk || !elemOk {
		return 0, 0, false
	}
	return lenSlot, elemSlot, true
}

// ArrayLengthSlotOf resolves `a.length` to the len slot — the one
// property read a flattened array answers. IndexOf routes through here
// so an `a.length` read and an `i < a.length` head both land on the
// ordinary number slot the guards and the loop head already speak.
func ArrayLengthSlotOf(context *LoweringContext, node *ast.Node) (int, bool) {
	head := Unwrapped(node)
	if !ast.IsPropertyAccessExpression(head) {
		return 0, false
	}
	access := head.AsPropertyAccessExpression()
	if access.QuestionDotToken != nil {
		return 0, false
	}
	if !ast.IsIdentifier(access.Expression) || !ast.IsIdentifier(access.Name()) {
		return 0, false
	}
	if access.Name().Text() != "length" {
		return 0, false
	}
	lenSlot, _, ok := arraySlotsOf(context, access.Expression.Text())
	if !ok {
		return 0, false
	}
	return lenSlot, true
}

// ArrayElementSlotOf resolves an index access `a[i]` to the element
// slot its read answers — the sort gate a caller consults before
// admitting the read into arithmetic or a sequence.
func ArrayElementSlotOf(context *LoweringContext, node *ast.Node) (int, bool) {
	head := Unwrapped(node)
	if !ast.IsElementAccessExpression(head) {
		return 0, false
	}
	receiver := head.AsElementAccessExpression().Expression
	if !ast.IsIdentifier(receiver) {
		return 0, false
	}
	if _, isIndex := indexAccessOf(head, receiver.Text()); !isIndex {
		return 0, false
	}
	_, elemSlot, ok := arraySlotsOf(context, receiver.Text())
	if !ok {
		return 0, false
	}
	return elemSlot, true
}

// varEffect is a slot read as an effect — the one-liner every array
// and record lowering below reaches for. This is the NUMERIC/coercing
// read (walk.lean's flagged-read rule): an absent or NaN source reads
// flagged here. Use it for anything that becomes an OPERAND of another
// effect (A/B of a binary, join, concat, or-absent, …) — never wrap
// this one in asVarStateEffect.
func varEffect(index int) kernelbridge.LoopEffect {
	return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectVar, Index: index}
}

// varStateEffect is a slot read as its WHOLE state, verbatim — set,
// absent admissions, and the NaN flag alike, with no numeric coercion.
// This is what a plain copy (`lo = p.lo`, a destructuring leaf read, a
// whole-record reassignment) means at the RUNTIME level: the copy
// carries whatever the source held, including an admission a numeric
// read would launder into the NaN flag.
//
// ONLY safe where the result becomes the WHOLE Effect of an
// IrStatementAssign standing alone — never nested inside another
// effect's operand (A/B of un/bin/concat/join/orAbsent), and never fed
// to SubstituteVars/FoldBody's loop-effect folding, which re-nests a
// binding's current effect into LATER statements' operands. The kernel
// proof is explicit about this (set_functions/walk.lean's
// effectReadsFlagged/seqSetOf/readEnclosure `.varState _ =>
// none`/unreachable arms): a copy in operand position answers nothing,
// by design — the adapter must never emit one there.
func varStateEffect(index int) kernelbridge.LoopEffect {
	return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectVarState, Index: index}
}

// asVarStateEffect upgrades a bare identity-copy read (LoopEffectVar)
// to the verbatim whole-state copy (LoopEffectVarState) — same Index,
// nothing else changes. Effects of any other kind pass through
// unchanged (a const, join, arithmetic build, etc. already carries
// whatever ride-alongs it needs, or is itself something a copy
// upgrade does not apply to).
//
// Call this ONLY at a site where the result becomes the WHOLE Effect
// of an IrStatementAssign — see varStateEffect's doc for the operand
// restriction this must never violate.
func asVarStateEffect(e kernelbridge.LoopEffect) kernelbridge.LoopEffect {
	if e.Kind == kernelbridge.LoopEffectVar {
		return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectVarState, Index: e.Index}
	}
	return e
}

// joinEffect pairs two effects into the effect grammar's join — what a
// weak update writes.
func joinEffect(a, b kernelbridge.LoopEffect) kernelbridge.LoopEffect {
	return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectJoin, A: &a, B: &b}
}

// constNumber is the exact-value set constant.
func constNumber(w float64) kernelbridge.LoopEffect {
	return kernelbridge.LoopEffect{
		Kind: kernelbridge.LoopEffectConst,
		Set:  refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{w})),
	}
}
