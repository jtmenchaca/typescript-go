// THE DIFFERENTIAL HARNESS — the standing gate the thin-walk
// migration needs.
//
// lattice_conformance_test.go holds ONE adapter computation (JoinKnown)
// to the kernel's proved twin. This file generalizes that discipline to
// every adapter computation that HAS a kernel twin: for each one, run
// representative values through BOTH routes and compare. The audit's B
// rows (THIN-WALK-AUDIT.md slices 9 + 10, the W1 flip queue) name which
// computations those are; the four files beside this one cover them:
//
//   truthiness_conformance_test.go   — abstractdomain.Truthiness vs
//                                      kernel.NarrowState js.truthyNum /
//                                      js.truthyStr
//   narrow_known_conformance_test.go — abstractdomain.NarrowKnown over
//                                      kernel.Narrow's own forms, vs
//                                      kernel.NarrowState
//   arithmetic_conformance_test.go   — walk.TransferBinary vs a direct
//                                      kernel.Transfer of the same op
//   min_max_conformance_test.go      — walk.TransferMathCall's all-exact
//                                      min/max fast path vs the
//                                      binary64.min/max pairwise fold
//
// THE THREE-VERDICT FRAME
// =======================
//
// A differential row lands in exactly one of three classes, and the
// class decides what the harness DOES about it:
//
//   AGREEMENT   — both routes answer, and they must answer the SAME
//                 thing. Drift is a TEST FAILURE, unconditionally. This
//                 is the whole point of the gate: two implementations of
//                 one judgment, one of which is proved, and the unproved
//                 one is not allowed to differ.
//
//   DETERMINATION GAP — the adapter DECLINES (answers "I know nothing")
//                 and the kernel ANSWERS. This is NOT a failure: the
//                 adapter is weaker than the proof, which is sound but
//                 leaves a body undetermined. Each one is recorded as a
//                 ledger entry in the covering test file's comments, so
//                 the migration has a named queue instead of a silence.
//                 The assertion for these rows checks the DECLINE ITSELF
//                 (the adapter really does decline — if it starts
//                 answering, the row moves to AGREEMENT and the ledger
//                 entry is stale), plus the kernel's answer is recorded
//                 so the gap's SIZE is visible.
//
//   SCRUTINY    — the adapter ANSWERS and the kernel declines, or answers
//                 in a shape the two cannot be compared in. This is the
//                 adapter claiming what the proof does not, which is the
//                 only class that can hide an unsoundness. Where the two
//                 answers ARE comparable, agreement is asserted like any
//                 AGREEMENT row. Where they are NOT, the row is flagged
//                 LOUDLY in the covering file's comments with the exact
//                 reason the comparison could not be made — never
//                 silently skipped.
//
// Every test here gates on the dylib the way lattice_conformance_test.go
// does: absent kernel skips, never a faked pass.

package conformance

import (
	"math"
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// differentialKernel is replayKernel's twin for the differential files:
// the same dylib convention (skip when absent, fatal on a load error),
// named so each operation file reads its own gate at its own call site.
func differentialKernel(t *testing.T) *kernelbridge.RefinedTSKernel {
	t.Helper()
	if !kernelbridge.KernelArtifactsPresent(kernelbridge.DylibPath) {
		t.Skip("native kernel dylib absent — build it first")
	}
	kernel, err := kernelbridge.LoadKernel(kernelbridge.DylibPath)
	if err != nil {
		t.Fatalf("LoadKernel: %v", err)
	}
	return kernel
}

// sameSet is sameState's set half, lifted for the rows that compare
// bare sets rather than whole states: mutual containment, since the two
// routes may spell one set two ways (the same argument sameState makes).
func sameSet(kernel *kernelbridge.RefinedTSKernel, a, b refinementsets.RefinedSet) bool {
	return kernel.ScalarSubset(a, b) && kernel.ScalarSubset(b, a)
}

// exactSet spells one exact value as the singleton set a transfer
// question carries — the same spelling walk's SetOfKnownForTransfer
// builds for a one-value KindValues.
func exactSet(x float64) refinementsets.RefinedSet {
	return refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{x}))
}

// exactValue is exactSet's AbstractValue twin: the adapter-side operand
// a walk holds for a numeric literal.
func exactValue(x float64) abstractdomain.AbstractValue {
	return abstractdomain.KnownValues([]float64{x}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
}

// answeredExactly reads an adapter answer as the ONE float it pins, and
// whether it pins one at all. A KindValues of length one is the pin; a
// one-member OneOf set is the same pin spelled as a set (KnownOfAnswer
// builds either, depending on how many values the kernel answered), so
// both read here. Anything else — a window, a wrapper, unknown — pins
// nothing, and the caller decides whether that is a determination gap or
// a comparison it cannot make.
func answeredExactly(k abstractdomain.AbstractValue) (float64, bool) {
	if k.Kind == abstractdomain.KindValues && k.KindTag == abstractdomain.PrimitiveNumber &&
		len(k.Values) == 1 {
		return k.Values[0], true
	}
	if k.Kind == abstractdomain.KindSet && k.SetKindTag == abstractdomain.SetKindTagNone &&
		len(k.Set.Forms) == 1 && k.Set.Forms[0].Form == refinementsets.FormOneOf &&
		len(k.Set.Forms[0].W) == 1 {
		return k.Set.Forms[0].W[0], true
	}
	return 0, false
}

// kernelExactly reads a kernel transfer answer the same way: the ONE
// float it pins, and whether it pins one.
func kernelExactly(answer kernelbridge.TransferAnswer) (float64, bool) {
	if answer.Kind == kernelbridge.TransferAnswerValues && len(answer.Values) == 1 {
		return answer.Values[0], true
	}
	if answer.Kind == kernelbridge.TransferAnswerSet &&
		len(answer.Set.Forms) == 1 && answer.Set.Forms[0].Form == refinementsets.FormOneOf &&
		len(answer.Set.Forms[0].W) == 1 {
		return answer.Set.Forms[0].W[0], true
	}
	return 0, false
}

// sameFloatBits compares two answered floats the way the ECMA rows
// distinguish them: −0 and +0 are DIFFERENT answers here (Math.max's
// −0 ordering is exactly a place a local fast path could drift and a
// plain `==` would hide it), and NaN equals NaN (both routes spell the
// spec's one NaN, and `!=` would call two agreements a drift).
func sameFloatBits(a, b float64) bool {
	if math.IsNaN(a) || math.IsNaN(b) {
		return math.IsNaN(a) && math.IsNaN(b)
	}
	if a == 0 && b == 0 {
		return math.Signbit(a) == math.Signbit(b)
	}
	return a == b
}

// isNaNAnswer says whether an adapter answer is the pinned NaN — the
// one arm that carries no set at all, so answeredExactly cannot read it.
func isNaNAnswer(k abstractdomain.AbstractValue) bool {
	return k.Kind == abstractdomain.KindNaN
}

// declined says whether an adapter answer determines nothing — the
// DETERMINATION GAP test. Unknown (plain or opaque) is the whole of it:
// every other kind states something.
func declined(k abstractdomain.AbstractValue) bool {
	return k.Kind == abstractdomain.KindUnknown
}

// negZero, posInf, negInf name the three floats every row table below
// spells more than once — written as functions so no package-level var
// can be reassigned out from under a row.
func negZero() float64 { return math.Copysign(0, -1) }
func posInf() float64  { return math.Inf(1) }
func negInf() float64  { return math.Inf(-1) }

// edgeValues are the floats the kernel's own rows pin, shared by the
// arithmetic and min/max files so both walk the SAME corners: the two
// zeros, the two infinities, the 2^53 boundary on both sides (where
// integer exactness ends), a plain pair, and a fraction. NaN is NOT
// here — it enters as abstractdomain.NaNValue, not as a set member (the
// kernel refuses NaN at set construction: refinement_forms.go's
// element).
var edgeValues = []float64{
	math.Copysign(0, -1), // −0
	0,                    // +0
	1,
	-1,
	0.5,
	-0.5,
	9007199254740992,  // 2^53 — the first integer whose successor is not exact
	-9007199254740992, // −2^53
	9007199254740991,  // 2^53 − 1, Number.MAX_SAFE_INTEGER
	math.Inf(1),
	math.Inf(-1),
}
