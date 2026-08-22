// kernel.SeqPrefix — the ask kernel_asks.go wires to kernel_seq_prefix
// (mirrors kernel_seq_subset's two-string shape). seqOf
// (subset_seq_shape.lean) recognizes a concatenation only when its
// LEFT operand is a single scalar set — the fixed-head, open-tail
// shape z.tuple([...], rest)/z.array() emits — so both tests below
// pin that boundary: a fixed-head concatenation answers the proved
// prefix window; an open-head one (either operand order) declines.
package kernelbridge

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

func TestSeqPrefix_AnOpenLeftOperandDeclinesInBothOrientations(t *testing.T) {
	if !KernelArtifactsPresent(DylibPath) {
		t.Skip("native kernel dylib absent")
	}
	kernel, err := LoadKernel(DylibPath)
	if err != nil {
		t.Fatalf("LoadKernel: %v", err)
	}
	// Strings (a Star) as the LEFT operand: not scalar, so seqOf's
	// .Concatenation arm never matches
	openLeft := refinementsets.MakeRefinedSet(refinementsets.Concatenation(refinementsets.Strings, refinementsets.StringTuple("xxxxxxxx")))
	if _, ok := kernel.SeqPrefix(openLeft, 3); ok {
		t.Errorf("SeqPrefix(Strings . StringTuple(xxxxxxxx), 3) ok = true, want false — the left operand is not scalar")
	}
	// a MULTI-character word as the left operand is itself a
	// Concatenation, not one scalar set — same decline
	multiCharHead := refinementsets.MakeRefinedSet(refinementsets.Concatenation(refinementsets.StringTuple("xxxxxxxx"), refinementsets.Strings))
	if _, ok := kernel.SeqPrefix(multiCharHead, 3); ok {
		t.Errorf("SeqPrefix(StringTuple(xxxxxxxx) . Strings, 3) ok = true, want false — a multi-char literal is a Concatenation, not one scalar set")
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
