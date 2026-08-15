// split from ir_array_slots.go — `a.indexOf(v)` / `a.lastIndexOf(v)` /
// `a.includes(v)` / `a.at(i)`: the four value-producing read methods
// over a flattened array's two slots.
//
// None of these read the ARGUMENT's value — every claim below holds
// for every searched-for value or every index, because what the two
// slots carry is a WEAK summary (the len slot's count, the elem
// slot's join), never a per-position fact a search could sharpen.
// That is also why a bounds-checked `fromIndex` on indexOf/lastIndexOf
// is not read here: it only narrows which positions are searched, and
// the answer's window does not shrink from narrowing positions out of
// a search that already covers the whole array in the worst case
// (sec-array.prototype.indexof steps 4-11 — the window the kernel
// proves is `{-1} u [0, length)` regardless of where the scan started).
package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// arrayLenMinusOne is `len - 1` as an effect: the kernel's own binary
// transfer over the len slot's var and the constant 1, evaluated as
// interval subtraction at walk time.
func arrayLenMinusOne(lenSlot int) kernelbridge.LoopEffect {
	length := varEffect(lenSlot)
	one := constNumber(1)
	return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectBinary, Op: kernelbridge.LoopOpSub, A: &length, B: &one}
}

// arraySearchIndexEffect is `a.indexOf(v)` / `a.lastIndexOf(v)`: an
// integer in `{-1} u [0, length)`
// (sec-array.prototype.indexof steps 3-11, sec-array.prototype.lastindexof
// steps 3, 9-11 — both are -1 or a valid index into the receiver,
// never anything else and never a thrown completion for an ordinary
// array receiver). Read as `join(-1, len - 1)` — the kernel's own
// lattice OR over the constant -1 and the len slot's own upper reach.
// The join is load-bearing, not cosmetic: -1 is answered whether or
// not the needle occurs, so a NON-empty, EXACTLY-known array (whose
// len slot is a singleton, e.g. a literal's constNumber(3)) still has
// to answer -1 — `len - 1` alone would compute the single value 2 and
// wrongly exclude the not-found sentinel. The join's own enclosure is
// `[min(-1, lo-1), max(-1, hi-1)]`, which for a len slot's AtLeast-0
// floor collapses to `[-1, hi-1]` — the row's stated shape — and for a
// len slot with no ceiling similarly gives `[-1, +inf)`, both integer
// (an integer join of two integer operands). The needle is not
// consulted — see the file comment — so indexOf and lastIndexOf answer
// identically here.
func arraySearchIndexEffect(context *LoweringContext, node *ast.Node, wantMethod string) (kernelbridge.LoopEffect, bool) {
	receiver, receiverOk := arrayReadReceiverOf(node)
	if !receiverOk {
		return kernelbridge.LoopEffect{}, false
	}
	method, argument, isRead := arrayReadMethodCallOf(node, receiver)
	if !isRead || method != wantMethod {
		return kernelbridge.LoopEffect{}, false
	}
	lenSlot, _, ok := arraySlotsOf(context, receiver)
	if !ok {
		return kernelbridge.LoopEffect{}, false
	}
	// the needle still has to be a value the effect grammar can read,
	// even though its OWN value never enters the answer — a needle that
	// runs code would need a statement, which an effect position is not
	if !writeAndCallFree(argument) {
		return kernelbridge.LoopEffect{}, false
	}
	return joinEffect(constNumber(-1), arrayLenMinusOne(lenSlot)), true
}

// arrayIncludesEffect is `a.includes(v)`: the two-value boolean set
// (sec-array.prototype.includes steps 3, 9-11 — always *true* or
// *false*, never a thrown completion for an ordinary array receiver).
// Write-free on purpose: SameValueZero reads the needle without
// running it, and the elem slot's own knowledge is not consulted
// either way, since the two-value answer holds whatever the slot
// states.
func arrayIncludesEffect(context *LoweringContext, node *ast.Node) (kernelbridge.LoopEffect, bool) {
	receiver, receiverOk := arrayReadReceiverOf(node)
	if !receiverOk {
		return kernelbridge.LoopEffect{}, false
	}
	method, argument, isRead := arrayReadMethodCallOf(node, receiver)
	if !isRead || method != "includes" {
		return kernelbridge.LoopEffect{}, false
	}
	if _, _, ok := arraySlotsOf(context, receiver); !ok {
		return kernelbridge.LoopEffect{}, false
	}
	if !writeAndCallFree(argument) {
		return kernelbridge.LoopEffect{}, false
	}
	return booleanPairEffect(), true
}

// arrayAtEffect is `a.at(i)`: the same reading `a[i]` already answers
// under the SAME guard-scope contract ArrayIndexReadEffect carries — a
// dominating `i < a.length` over a spelled, non-negative-provable
// index bounds the read to the plain elem var; every other shape,
// INCLUDING a negative index (sec-array.prototype.at steps 3-6: a
// negative relativeIndex reads length + relativeIndex, a position the
// guard scope never proved bounded), wears the or-absent wrapping —
// which is exactly what `at` answers past either end
// (sec-array.prototype.at step 6: `k < 0 or k >= length` returns
// *undefined*).
func arrayAtEffect(context *LoweringContext, node *ast.Node) (kernelbridge.LoopEffect, bool) {
	receiver, receiverOk := arrayReadReceiverOf(node)
	if !receiverOk {
		return kernelbridge.LoopEffect{}, false
	}
	method, argument, isRead := arrayReadMethodCallOf(node, receiver)
	if !isRead || method != "at" {
		return kernelbridge.LoopEffect{}, false
	}
	_, elemSlot, ok := arraySlotsOf(context, receiver)
	if !ok {
		return kernelbridge.LoopEffect{}, false
	}
	elem := varEffect(elemSlot)
	if indexName, spelled := SpelledNameOf(Unwrapped(argument)); spelled &&
		IndexIsBounded(context, indexName, receiver) {
		return elem, true
	}
	return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectOrAbsent, A: &elem}, true
}

// arrayReadReceiverOf is the flattened array name a read-method call's
// receiver names — `a` in `a.indexOf(v)` — or ("", false) for a
// receiver that is not a plain identifier.
func arrayReadReceiverOf(node *ast.Node) (string, bool) {
	if !ast.IsCallExpression(node) {
		return "", false
	}
	access := node.AsCallExpression().Expression
	if !ast.IsPropertyAccessExpression(access) {
		return "", false
	}
	receiver := access.AsPropertyAccessExpression().Expression
	if !ast.IsIdentifier(receiver) {
		return "", false
	}
	return receiver.Text(), true
}

// ArrayNumericReadEffect is the numeric-result hub `EffectOf`'s Opaque
// callback reaches for: `indexOf`/`lastIndexOf` (a number) and
// `includes` (the boolean pair, itself number-sorted) and `at` (the
// element, under the number-sort gate the caller already applies to
// every other element read). Declines wherever none of the four forms
// match, exactly as an unmodeled method does.
func ArrayNumericReadEffect(context *LoweringContext, node *ast.Node) (kernelbridge.LoopEffect, bool) {
	if held, ok := arraySearchIndexEffect(context, node, "indexOf"); ok {
		return held, true
	}
	if held, ok := arraySearchIndexEffect(context, node, "lastIndexOf"); ok {
		return held, true
	}
	if held, ok := arrayIncludesEffect(context, node); ok {
		return held, true
	}
	if held, ok := arrayAtEffect(context, node); ok {
		return held, true
	}
	return kernelbridge.LoopEffect{}, false
}
