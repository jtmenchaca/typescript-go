// The residue-reason sweep's comparison_decision.go and math_transfer.go
// / math_unary_transfer.go family: CompareKnown's own declined-row
// sentences (ordering against absence, unknown-against-null,
// two-unknowns, array-by-reference), transferSign's disjointness
// refusal, Math.hypot/atan2's kernel-refusal fallback, the min/max
// kernel-first flip, and the log1p/log/sqrt domain-split fallback.
// Pinned by DIRECT function call, comparison_decision_test.go's own
// precedent (TestCompareKnownStrictUndefUndef and kin) — see
// residue_reason_test.go's header for why the source-level RTS7002
// pattern does not reach these sites (a boolean/number initializer
// re-seeds the value before assignability ever asks about it).
package walk

import (
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// TestCompareKnown_OrderingAgainstAbsenceNamesItsOwnReader pins
// comparison_decision.go's line-74 family: `x < undefined` with x an
// AbstractValue.Unknown (unread) left operand. bExactAbsent is true and
// op is CompareLt, so CompareKnown returns ResidueOf("an ordering
// comparison against null or undefined has no row…") before any
// value-kind check runs.
func TestCompareKnown_OrderingAgainstAbsenceNamesItsOwnReader(t *testing.T) {
	ctx := &FlowContext{}
	got := CompareKnown(ctx, CompareLt, false, abstractdomain.Unknown, abstractdomain.Undef)
	if got.Kind != abstractdomain.KindUnknown {
		t.Fatalf("CompareKnown(unknown < undefined) = %+v, want KindUnknown", got)
	}
	if !strings.Contains(got.ResidueReason, "an ordering comparison against null or undefined has no row") {
		t.Errorf("ResidueReason = %q, want it to name the ordering-against-absence row", got.ResidueReason)
	}
}

// TestCompareKnown_UnknownAgainstNullNamesItsOwnReader pins the
// line-110 family: `x === null` with x an AbstractValue.Unknown left
// operand. One side is exact absence (null); the other reads
// KindUnknown, which is none of KindValues/KindObject/KindList/
// KindArrayHoles, so CompareKnown falls through to the "non-absent side
// is not a plain value" sentence instead of deciding the equality.
func TestCompareKnown_UnknownAgainstNullNamesItsOwnReader(t *testing.T) {
	ctx := &FlowContext{}
	got := CompareKnown(ctx, CompareEq, true, abstractdomain.Unknown, abstractdomain.Null)
	if got.Kind != abstractdomain.KindUnknown {
		t.Fatalf("CompareKnown(unknown === null) = %+v, want KindUnknown", got)
	}
	if !strings.Contains(got.ResidueReason, "the non-absent side is not a plain value") {
		t.Errorf("ResidueReason = %q, want it to name the non-absent-side row", got.ResidueReason)
	}
}

// TestCompareKnown_TwoUnknownsCompareNamesItsOwnReader pins the
// line-113 family: `x < y` with both operands AbstractValue.Unknown.
// Neither side is NaN or exact absence, and neither reads KindValues, so
// CompareKnown declines with "a side is not a plain known value" rather
// than reaching the string/array/number rows below it.
func TestCompareKnown_TwoUnknownsCompareNamesItsOwnReader(t *testing.T) {
	ctx := &FlowContext{}
	got := CompareKnown(ctx, CompareLt, false, abstractdomain.Unknown, abstractdomain.Unknown)
	if got.Kind != abstractdomain.KindUnknown {
		t.Fatalf("CompareKnown(unknown < unknown) = %+v, want KindUnknown", got)
	}
	if !strings.Contains(got.ResidueReason, "a side is not a plain known value") {
		t.Errorf("ResidueReason = %q, want it to name the not-a-plain-known-value row", got.ResidueReason)
	}
}

// TestCompareKnown_ArrayComparisonNamesItsOwnReader pins the line-155
// family: `[1] < [2]`, two array literals of one exact number each.
// array_literal.go's uniform-numeric case reads them as
// KindValues/PrimitiveArray, which both clears the earlier KindValues
// gate and matches KindTag across sides — so CompareKnown reaches the
// array-by-reference sentence instead of the number rows.
func TestCompareKnown_ArrayComparisonNamesItsOwnReader(t *testing.T) {
	ctx := &FlowContext{}
	arrayOf := func(v float64) abstractdomain.AbstractValue {
		return abstractdomain.KnownValues([]float64{v}, abstractdomain.PrimitiveArray, abstractdomain.TrustProved)
	}
	got := CompareKnown(ctx, CompareLt, false, arrayOf(1), arrayOf(2))
	if got.Kind != abstractdomain.KindUnknown {
		t.Fatalf("CompareKnown([1] < [2]) = %+v, want KindUnknown", got)
	}
	if !strings.Contains(got.ResidueReason, "arrays compare by reference") {
		t.Errorf("ResidueReason = %q, want it to name the array-by-reference row", got.ResidueReason)
	}
}

// residueReasonPanickingScalarDisjoint is a fake kernel whose
// ScalarDisjoint always panics — the same shape a genuine kernel
// refusal takes (kernel_bridge.go's questions panic on a refused
// question, per PORT.md), forcing transferSign's callScalarDisjoint
// recover() to report !ok deterministically rather than depending on
// a real kernel actually refusing.
func residueReasonPanickingScalarDisjoint() *kernelbridge.RefinedTSKernel {
	return &kernelbridge.RefinedTSKernel{
		ScalarDisjoint: func(a, b refinementsets.RefinedSet) bool {
			panic("residue_reason_test: forced ScalarDisjoint refusal")
		},
	}
}

// TestTransferSign_ADeclinedDisjointnessQuestionNamesTheThreeRaysNotABareResidue
// pins math_transfer.go's transferSign: with a kernel present and a
// readable operand set, a ScalarDisjoint refusal must name Math.sign's
// own three-ray disjointness mechanism rather than leaving the
// KindUnknown's ResidueReason blank.
func TestTransferSign_ADeclinedDisjointnessQuestionNamesTheThreeRaysNotABareResidue(t *testing.T) {
	SetTransferKernel(residueReasonPanickingScalarDisjoint())
	operand := abstractdomain.KnownValues([]float64{5}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
	got := transferSign(operand, true)
	if got.Kind != abstractdomain.KindUnknown {
		t.Fatalf("transferSign with a panicking ScalarDisjoint = %+v, want KindUnknown", got)
	}
	if got.ResidueReason == "" {
		t.Fatalf("transferSign's declined-disjointness unknown carries no ResidueReason")
	}
	if !strings.Contains(got.ResidueReason, "disjoint") {
		t.Errorf("ResidueReason = %q, want it to name the disjointness questions", got.ResidueReason)
	}
}

// residueReasonUnknownTransfer is a fake kernel whose Transfer always
// answers TransferAnswerUnknown — the wire shape a genuine kernel
// refusal takes (transfer_questions.go's DecodeTransferAnswer decodes
// exactly this kind from a real refusal), forcing the fold/split
// branches below to decline deterministically.
func residueReasonUnknownTransfer() *kernelbridge.RefinedTSKernel {
	return &kernelbridge.RefinedTSKernel{
		Transfer: func(question kernelbridge.TransferQuestion) kernelbridge.TransferAnswer {
			return kernelbridge.TransferAnswer{Kind: kernelbridge.TransferAnswerUnknown}
		},
	}
}

// TestMathImage_ADeclinedHypotFoldNamesThePairwiseFoldNotABareResidue
// pins math_transfer.go's hypot arm: two readable operands and a live
// kernel whose Transfer refuses every question must name Math.hypot's
// own pairwise-fold mechanism.
func TestMathImage_ADeclinedHypotFoldNamesThePairwiseFoldNotABareResidue(t *testing.T) {
	SetTransferKernel(residueReasonUnknownTransfer())
	a := abstractdomain.KnownValues([]float64{3}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
	b := abstractdomain.KnownValues([]float64{4}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
	got, ok := TransferMathCall("hypot", []abstractdomain.AbstractValue{a, b})
	if !ok {
		t.Fatalf("Math.hypot was not transferred")
	}
	if got.Kind != abstractdomain.KindUnknown {
		t.Fatalf("Math.hypot with a refusing kernel = %+v, want KindUnknown", got)
	}
	if got.ResidueReason == "" {
		t.Fatalf("Math.hypot's declined-fold unknown carries no ResidueReason")
	}
	if !strings.Contains(got.ResidueReason, "hypot") {
		t.Errorf("ResidueReason = %q, want it to name Math.hypot", got.ResidueReason)
	}
}

// TestMathImage_ADeclinedAtan2WindowNamesTheKernelWindowNotABareResidue
// pins math_transfer.go's atan2 arm: a live kernel but an operand that
// SetOfKnownForTransfer cannot read as a set (a multi-value word, none
// of the singleton/set/starDepth-0-variable shapes it reads) must name
// Math.atan2's own kernel-window mechanism rather than falling through
// to KnownOfAnswer's bare unknown.
func TestMathImage_ADeclinedAtan2WindowNamesTheKernelWindowNotABareResidue(t *testing.T) {
	SetTransferKernel(residueReasonUnknownTransfer())
	y := abstractdomain.KnownValues([]float64{1, 2}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
	x := abstractdomain.KnownValues([]float64{1}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
	got, ok := TransferMathCall("atan2", []abstractdomain.AbstractValue{y, x})
	if !ok {
		t.Fatalf("Math.atan2 was not transferred")
	}
	if got.Kind != abstractdomain.KindUnknown {
		t.Fatalf("Math.atan2 with an unreadable operand = %+v, want KindUnknown", got)
	}
	if got.ResidueReason == "" {
		t.Fatalf("Math.atan2's declined-window unknown carries no ResidueReason")
	}
	if !strings.Contains(got.ResidueReason, "atan2") {
		t.Errorf("ResidueReason = %q, want it to name Math.atan2", got.ResidueReason)
	}
}

// residueReasonStraddlingSet is an operand set that straddles a
// domain edge (both a value past it and a value before it) — the
// shape math_unary_transfer.go's log1p/log/sqrt arms read as "not
// provably inside the domain," routing into domainSplitImage.
func residueReasonStraddlingSet(low, high float64) refinementsets.RefinedSet {
	return refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{low, high}))
}

// TestUnaryMathImage_ALog1pSplitThatCannotBuildNamesThePastMinusOneSplitNotABareResidue
// pins math_unary_transfer.go's log1p arm: an operand straddling −1
// with a kernel whose Transfer refuses the clipped question must name
// log1p's own past-−1 split, not a bare unknown.
func TestUnaryMathImage_ALog1pSplitThatCannotBuildNamesThePastMinusOneSplitNotABareResidue(t *testing.T) {
	SetTransferKernel(residueReasonUnknownTransfer())
	operand := abstractdomain.KnownSet(residueReasonStraddlingSet(-2, 5), nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone)
	got, ok := UnaryMathImage("log1p", []abstractdomain.AbstractValue{operand}, abstractdomain.TrustProved)
	if !ok {
		t.Fatalf("Math.log1p was not transferred")
	}
	if got.Kind != abstractdomain.KindUnknown {
		t.Fatalf("Math.log1p with a straddling operand and a refusing kernel = %+v, want KindUnknown", got)
	}
	if got.ResidueReason == "" {
		t.Fatalf("Math.log1p's declined-split unknown carries no ResidueReason")
	}
	if !strings.Contains(got.ResidueReason, "log1p") {
		t.Errorf("ResidueReason = %q, want it to name Math.log1p", got.ResidueReason)
	}
}

// TestUnaryMathImage_ALogSplitThatCannotBuildNamesThePositiveSplitNotABareResidue
// pins math_unary_transfer.go's log/log2/log10 arm: an operand
// straddling zero with a kernel whose Transfer refuses the clipped
// question must name that arm's own positive-operand split.
func TestUnaryMathImage_ALogSplitThatCannotBuildNamesThePositiveSplitNotABareResidue(t *testing.T) {
	SetTransferKernel(residueReasonUnknownTransfer())
	operand := abstractdomain.KnownSet(residueReasonStraddlingSet(-3, 3), nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone)
	got, ok := UnaryMathImage("log", []abstractdomain.AbstractValue{operand}, abstractdomain.TrustProved)
	if !ok {
		t.Fatalf("Math.log was not transferred")
	}
	if got.Kind != abstractdomain.KindUnknown {
		t.Fatalf("Math.log with a straddling operand and a refusing kernel = %+v, want KindUnknown", got)
	}
	if got.ResidueReason == "" {
		t.Fatalf("Math.log's declined-split unknown carries no ResidueReason")
	}
	if !strings.Contains(got.ResidueReason, "positive") {
		t.Errorf("ResidueReason = %q, want it to name the positive-operand split", got.ResidueReason)
	}
}

// TestUnaryMathImage_ASqrtSplitThatCannotBuildNamesTheZeroStraddleNotABareResidue
// pins math_unary_transfer.go's sqrt arm: an operand straddling zero
// with a kernel whose Transfer refuses the clipped question must name
// sqrt's own zero-straddle split.
func TestUnaryMathImage_ASqrtSplitThatCannotBuildNamesTheZeroStraddleNotABareResidue(t *testing.T) {
	SetTransferKernel(residueReasonUnknownTransfer())
	operand := abstractdomain.KnownSet(residueReasonStraddlingSet(-4, 9), nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone)
	got, ok := UnaryMathImage("sqrt", []abstractdomain.AbstractValue{operand}, abstractdomain.TrustProved)
	if !ok {
		t.Fatalf("Math.sqrt was not transferred")
	}
	if got.Kind != abstractdomain.KindUnknown {
		t.Fatalf("Math.sqrt with a straddling operand and a refusing kernel = %+v, want KindUnknown", got)
	}
	if got.ResidueReason == "" {
		t.Fatalf("Math.sqrt's declined-split unknown carries no ResidueReason")
	}
	if !strings.Contains(got.ResidueReason, "sqrt") {
		t.Errorf("ResidueReason = %q, want it to name Math.sqrt", got.ResidueReason)
	}
}

// residueReasonMinMaxKernel loads the real native kernel and wraps its
// Transfer to RECORD every TransferOpMin/TransferOpMax question asked
// — so a test can assert the KERNEL fold actually ran (not just that
// some answer came back) — pinning the W1 kernel-first flip against a
// silent same-shaped-answer false pass. Skipped when the dylib is
// absent, the same gate trig_reduction_test.go uses.
func residueReasonMinMaxKernel(t *testing.T) (*kernelbridge.RefinedTSKernel, *int) {
	t.Helper()
	if !kernelbridge.KernelArtifactsPresent(kernelbridge.DylibPath) {
		t.Skip("native kernel dylib absent — build it first")
	}
	real, err := kernelbridge.LoadKernel(kernelbridge.DylibPath)
	if err != nil {
		t.Fatalf("LoadKernel: %v", err)
	}
	asked := new(int)
	wrapped := *real
	realTransfer := real.Transfer
	wrapped.Transfer = func(question kernelbridge.TransferQuestion) kernelbridge.TransferAnswer {
		if question.Op == kernelbridge.TransferOpMin || question.Op == kernelbridge.TransferOpMax {
			*asked++
		}
		return realTransfer(question)
	}
	return &wrapped, asked
}

// TestMathImage_MinMaxAsksTheKernelFirstOnAllExactOperands pins the
// W1 flip: Math.min/Math.max with all-exact operands must POSE the
// kernel's TransferOpMin/TransferOpMax fold (asked > 0), not skip
// straight to the local all-exact host computation — the kernel-first
// half of the rule (narrow_questions.go's Returned()/MayThrow shape).
// The exact answer must still come out right, whichever route served
// it, since the kernel fold and the host row state the same fact.
func TestMathImage_MinMaxAsksTheKernelFirstOnAllExactOperands(t *testing.T) {
	kernel, asked := residueReasonMinMaxKernel(t)
	SetTransferKernel(kernel)
	a := abstractdomain.KnownValues([]float64{3}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
	b := abstractdomain.KnownValues([]float64{7}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
	got, ok := TransferMathCall("max", []abstractdomain.AbstractValue{a, b})
	if !ok {
		t.Fatalf("Math.max was not transferred")
	}
	if *asked == 0 {
		t.Errorf("Math.max(3, 7) with all-exact operands never posed TransferOpMax — the kernel-first route was skipped")
	}
	if got.Kind != abstractdomain.KindValues || len(got.Values) != 1 || got.Values[0] != 7 {
		t.Errorf("Math.max(3, 7) = %+v, want the exact value 7", got)
	}
}

// TestMathImage_MinMaxFallsBackToTheHostRowWhenTheKernelCannotBePosed
// pins the fallback half: an operand SetOfKnownForTransfer cannot read
// (a multi-value word) alongside an all-exact partner must still
// answer via the local host computation — the REFUSAL half of the
// rule — rather than declining outright, and must NOT pose the kernel
// question for that pair (there is no readable set to pose it with).
func TestMathImage_MinMaxFallsBackToTheHostRowWhenTheKernelCannotBePosed(t *testing.T) {
	kernel, asked := residueReasonMinMaxKernel(t)
	SetTransferKernel(kernel)
	unreadable := abstractdomain.KnownValues([]float64{1, 2}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
	got, ok := TransferMathCall("max", []abstractdomain.AbstractValue{unreadable})
	if !ok {
		t.Fatalf("Math.max was not transferred")
	}
	if got.Kind != abstractdomain.KindUnknown {
		t.Fatalf("Math.max of one unreadable multi-value operand = %+v, want KindUnknown (not all-exact, not kernel-readable)", got)
	}
	if *asked != 0 {
		t.Errorf("Math.max posed TransferOpMax on an operand SetOfKnownForTransfer cannot read (%d questions asked), want 0", *asked)
	}
}
