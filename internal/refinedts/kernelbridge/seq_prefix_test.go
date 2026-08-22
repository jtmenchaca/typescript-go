// kernel.SeqPrefix — the ask kernel_asks.go wires to kernel_seq_prefix
// (mirrors kernel_seq_subset's two-string shape). seqWindowOf
// (prefix_read.lean) reads scalars, the empty tuple, Star, Repeat
// with any bound, and a Concatenation whose BOTH operands are
// themselves recognized — in either orientation — answering the
// proved window Repeat(foldedAlphabet, min(lo, n), n). The tests pin
// both the concatenation-of-windows answer and the fixed-scalar-head
// answer.
package kernelbridge

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// The old premise, retired with seqWindowOf (prefix_read.lean): seqOf
// recognized a concatenation only when its LEFT operand was a single
// scalar set, so an open-left operand declined in both orientations.
// seqWindowOf reads a Concatenation of recognized operands in either
// order, so both orientations now ANSWER the proved prefix window.
func TestSeqPrefix_AConcatenationOfWindowsAnswersInBothOrientations(t *testing.T) {
	if !KernelArtifactsPresent(DylibPath) {
		t.Skip("native kernel dylib absent")
	}
	kernel, err := LoadKernel(DylibPath)
	if err != nil {
		t.Fatalf("LoadKernel: %v", err)
	}
	openLeft := refinementsets.MakeRefinedSet(refinementsets.Concatenation(refinementsets.Strings, refinementsets.StringTuple("xxxxxxxx")))
	multiCharHead := refinementsets.MakeRefinedSet(refinementsets.Concatenation(refinementsets.StringTuple("xxxxxxxx"), refinementsets.Strings))
	for name, set := range map[string]refinementsets.RefinedSet{
		"Strings . StringTuple(xxxxxxxx)": openLeft,
		"StringTuple(xxxxxxxx) . Strings": multiCharHead,
	} {
		got, ok := kernel.SeqPrefix(set, 3)
		if !ok {
			t.Errorf("SeqPrefix(%s, 3) ok = false, want true — seqWindowOf reads a concatenation of recognized operands in either orientation", name)
			continue
		}
		// total length floor is 8 (the literal), so take(3) is exactly
		// 3 characters over the folded alphabet
		if !kernel.Member(got, refinementsets.CodepointsOf("xxx")) {
			t.Errorf("%s: the answered set excludes \"xxx\" — a 3-character take must be a member", name)
		}
		if kernel.Member(got, refinementsets.CodepointsOf("xxxx")) {
			t.Errorf("%s: the answered set admits \"xxxx\" — take(3) never exceeds n", name)
		}
		if kernel.Member(got, refinementsets.CodepointsOf("")) {
			t.Errorf("%s: the answered set admits the empty word — the length floor is min(8, 3) = 3", name)
		}
	}
}

func TestSeqPrefix_AFixedScalarHeadAnswersTheProvedWindow(t *testing.T) {
	if !KernelArtifactsPresent(DylibPath) {
		t.Skip("native kernel dylib absent")
	}
	kernel, err := LoadKernel(DylibPath)
	if err != nil {
		t.Fatalf("LoadKernel: %v", err)
	}
	head := refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{'x'}))
	set := refinementsets.MakeRefinedSet(refinementsets.Concatenation(head, refinementsets.Strings))
	got, ok := kernel.SeqPrefix(set, 3)
	if !ok {
		t.Fatalf("SeqPrefix('x' . Strings, 3) ok = false, want true — a fixed-scalar-head/open-tail concatenation is seqOf-recognized")
	}
	if !kernel.Member(got, refinementsets.CodepointsOf("xxx")) {
		t.Errorf(`the answered set excludes "xxx" — a 3-character take must be a member`)
	}
	if kernel.Member(got, refinementsets.CodepointsOf("xxxx")) {
		t.Errorf(`the answered set admits "xxxx" — take(3) never exceeds n`)
	}
	if kernel.Member(got, refinementsets.CodepointsOf("")) {
		t.Errorf(`the answered set admits the empty word — the floor is 1 (the fixed head is always present)`)
	}
}
