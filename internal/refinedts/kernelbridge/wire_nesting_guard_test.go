package kernelbridge

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// deepConcatenationChain mirrors abstractdomain's own test helper of
// the same name: a right-nested Concatenation chain of the given
// depth over single-codepoint OneOf leaves.
func deepConcatenationChain(depth int) refinementsets.RefinedSet {
	set := refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{97}))
	for i := 0; i < depth; i++ {
		set = refinementsets.MakeRefinedSet(refinementsets.Concatenation(
			refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{97})), set))
	}
	return set
}

// deepConcatenationChainOverStrings is deepConcatenationChain's twin
// with a Star at the leaf instead of a bare OneOf singleton — the
// SAME shape the ReduceCSSCalc.ts reproducer's seqSubset ask carried
// (a Concatenation/Star nesting, never a plain literal word). Unlike
// deepConcatenationChain, this shape is NOT WordOf-recognized (WordOf
// only reads EmptyTuple/OneOf-singleton/Concatenation, and refuses the
// moment it reaches a Star), so kernel.SeqSubset's own identity/word
// fast paths (kernel_asks.go) never intercept it before reaching the
// ask2 seam this guard sits on.
func deepConcatenationChainOverStrings(depth int) refinementsets.RefinedSet {
	set := refinementsets.Strings
	for i := 0; i < depth; i++ {
		set = refinementsets.MakeRefinedSet(refinementsets.Concatenation(
			refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{97})), set))
	}
	return set
}

// TestWireNestingCountReadsAConcatenationChainsDepth pins the seam
// guard's cheap proxy directly against the wire EncodeSet produces: a
// chain of N Concatenation layers mentions the "concatenation" form
// tag N times.
func TestWireNestingCountReadsAConcatenationChainsDepth(t *testing.T) {
	for _, depth := range []int{0, 1, 17, 64, 65, 100} {
		wire := EncodeSet(deepConcatenationChain(depth))
		if got := wireNestingCount(wire); got != depth {
			t.Errorf("wireNestingCount(chain of depth %d) = %d, want %d", depth, got, depth)
		}
	}
}

// TestWireExceedsNestingCapAtTheNamedBoundary pins the cap itself: a
// wire exactly at wireNestingCap does not exceed it; one layer past
// does.
func TestWireExceedsNestingCapAtTheNamedBoundary(t *testing.T) {
	atCap := EncodeSet(deepConcatenationChain(wireNestingCap))
	if wireExceedsNestingCap(atCap) {
		t.Errorf("wireExceedsNestingCap(chain at exactly the cap) = true, want false")
	}
	pastCap := EncodeSet(deepConcatenationChain(wireNestingCap + 1))
	if !wireExceedsNestingCap(pastCap) {
		t.Errorf("wireExceedsNestingCap(chain one past the cap) = false, want true")
	}
}

// TestSeqSubsetDeclinesATooDeepOperandRatherThanAsking is the seam
// guard's end-to-end pin: kernel.SeqSubset over an operand whose wire
// nesting exceeds the cap declines (recovers from the panic every
// kernel-ask field wraps its error in, kernel_asks.go) INSTEAD of
// reaching the native call — the defect this guards against is the
// FFI call itself running unboundedly, so the guard must fire before
// Call2 is ever made. A shallow operand of the same shape still
// answers normally, pinning that the guard costs nothing for the
// ordinary case.
func TestSeqSubsetDeclinesATooDeepOperandRatherThanAsking(t *testing.T) {
	if !KernelArtifactsPresent(DylibPath) {
		t.Skip("native kernel dylib absent — build it first")
	}
	kernel, err := LoadKernel(DylibPath)
	if err != nil {
		t.Fatalf("LoadKernel: %v", err)
	}

	tooDeep := deepConcatenationChainOverStrings(wireNestingCap + 1)
	declined := func() (ok bool) {
		defer func() {
			if recover() != nil {
				ok = false
			} else {
				ok = true
			}
		}()
		kernel.SeqSubset(tooDeep, refinementsets.Strings)
		return true
	}
	if declined() {
		t.Errorf("SeqSubset(tooDeep, Strings) did not panic/decline — want the seam guard to refuse before the FFI call")
	}

	shallow := deepConcatenationChainOverStrings(3)
	answered := func() (ok bool) {
		defer func() {
			if recover() != nil {
				ok = false
			}
		}()
		kernel.SeqSubset(shallow, refinementsets.Strings)
		return true
	}
	if !answered() {
		t.Errorf("SeqSubset(shallow, Strings) declined — want the guard to leave an ordinary shallow ask untouched")
	}
}
