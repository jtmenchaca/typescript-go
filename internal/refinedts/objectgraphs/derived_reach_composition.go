// Composition along paths: the specification already fixes, for each
// related pair, ONE subset of the Cartesian product — so cardinality
// facts COMPOSE. If every A reaches c₁ B's and every B reaches c₂
// C's, then every A reaches, through B, a number of (B, C) hops
// whose total lies between lo(c₁)·lo(c₂) and hi(c₁)·hi(c₂). The
// derived count is a sound ENCLOSURE of reachable C-slots (distinct
// C elements may repeat across branches, so this bounds mentions,
// which bounds distinct elements from above; the lower bound holds
// when every hop exists, i.e. lo(c₂) counts each B's own reach).
//
// This is derived knowledge, not a verdict: it is computed FROM the
// stated counts and offered back as a fact the developer paid for.
package objectgraphs

import (
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// DerivedReach is the TS DerivedReach interface.
type DerivedReach struct {
	// Tail is the chain's start data node.
	Tail int
	// Head is the chain's end data node.
	Head int
	// Through is the composed path positions, in walking order.
	Through []int
	// Count is the derived count: how many end-slots one tail element reaches.
	Count refinementsets.RefinedSet
}

// composeCounts is composeCounts in the TS source: the natural-bounded
// product of two count sets — the KERNEL's exact ℕ product (counts
// are cardinalities by the specification's own domain, so the
// countProduct question's premise is met; the corners are exact
// dyadic products, where a host multiply could round past 2^53). The
// bool result is false where a count has no readable range (the TS
// null).
func composeCounts(
	kernel *kernelbridge.RefinedTSKernel,
	a, b refinementsets.RefinedSet,
) (refinementsets.RefinedSet, bool) {
	answer := kernel.Transfer(kernelbridge.TransferQuestion{
		Op: kernelbridge.TransferOpCountProduct,
		A:  a,
		B:  b,
	})
	if answer.Kind == kernelbridge.TransferAnswerSet {
		return answer.Set, true
	}
	return refinementsets.RefinedSet{}, false
}

// DerivedReachesBounds is the destructured named-parameter struct for
// DerivedReaches (the TS bounds object, `{ maxHops?: number }`).
type DerivedReachesBounds struct {
	// MaxHops is the chain length cap. Zero means unset — the TS
	// `?? 3` default applies (see DerivedReaches).
	MaxHops int
}

// DerivedReaches is derivedReaches in the TS source: every derived
// reach a specification's paths determine, for chains of 2 up to
// `maxHops` steps: each chain A → … → Z composes its counts hop by
// hop. A path never repeats within one chain, so the walk
// terminates; cycles surface as chains that revisit NODES (each
// Person reaches n manager's-managers), which is meaningful, not an
// error.
func DerivedReaches(
	spec Specification,
	kernel *kernelbridge.RefinedTSKernel,
	bounds DerivedReachesBounds,
) []DerivedReach {
	maxHops := bounds.MaxHops
	if maxHops == 0 {
		maxHops = 3
	}
	var out []DerivedReach
	var walk func(tail, at int, through []int, count refinementsets.RefinedSet)
	walk = func(tail, at int, through []int, count refinementsets.RefinedSet) {
		if len(through) >= 2 {
			out = append(out, DerivedReach{Tail: tail, Head: at, Through: through, Count: count})
		}
		if len(through) >= maxHops {
			return
		}
		for j, next := range spec.Paths {
			if next.Tail != at || containsInt(through, j) {
				continue
			}
			composed, ok := composeCounts(kernel, count, next.Count)
			if !ok {
				continue
			}
			nextThrough := make([]int, len(through)+1)
			copy(nextThrough, through)
			nextThrough[len(through)] = j
			walk(tail, next.Head, nextThrough, composed)
		}
	}
	for i, first := range spec.Paths {
		walk(first.Tail, first.Head, []int{i}, first.Count)
	}
	return out
}

// containsInt is Array.prototype.includes over the through slice — no
// TS twin (JS arrays carry .includes natively).
func containsInt(xs []int, x int) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
