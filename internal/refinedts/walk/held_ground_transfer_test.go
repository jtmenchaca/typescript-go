// Pins the fix to transferBinaryOf's "both operands HELD number-sorted
// knowledge the transfer could not tighten" arm (arithmetic_transfer.go):
// an atLeast(-Infinity) ray, an atLeast(0) ray met with itself, and an
// atLeast(-Infinity) ray times an exact singleton all reach the kernel's
// set-shaped transfer, which correctly declines to tighten a window whose
// true image is the whole real line (or, for a possibly-NaN operand, the
// whole line union NaN). Before the fix the arm's own answer was a
// formless RefinedSet{} at TrustProved — unposeable (OnOneTupleLayer
// requires a non-empty forms list) and ungraded (TrustProved collapses to
// an empty Grade field, the same shape AfterReaders' own never-examined
// seed wears), so nan_wrapper.go's CheckPossiblyNaN treated it as that
// seed and declined to 7002 rather than posing the sink's subset
// question. The fix spells the ground as the explicit
// AtLeast(-Infinity) form, graded TrustSpec (the OpDiv arm's own
// template just above it), so a bounded sink can refute it.
//
// binary64.add ([-inf, ∞) atLeast {1}) is A10.sink.arg.ts:18's shape
// (`x + 1` inside a callback whose parameter reads as an unbounded
// number-sorted set); binary64.sub ([0, ∞) atLeast − [0, ∞) atLeast) is
// B6.est.call.ts:31's shape (`r.hi - r.lo`, r.hi possibly NaN);
// binary64.mul ([-inf, ∞) atLeast × {1000}) is A13.sink.assign.ts:11's
// shape (`seconds * 1000` over an unbounded seconds).
package walk

import (
	"math"
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

func loadHeldGroundTransferKernel(t *testing.T) {
	t.Helper()
	if !kernelbridge.KernelArtifactsPresent(kernelbridge.DylibPath) {
		t.Skip("native kernel dylib absent — build it first")
	}
	kernel, err := kernelbridge.LoadKernel(kernelbridge.DylibPath)
	if err != nil {
		t.Fatalf("LoadKernel: %v", err)
	}
	SetTransferKernel(kernel)
}

// wholeLineRay is the unbounded number-sorted set a bare `number`
// operand's real half carries: atLeast(-Infinity), the same maximal ray
// SetOfKnownForTransfer's KindSet arm and the OpDiv arm above pose.
func wholeLineRay() abstractdomain.AbstractValue {
	return abstractdomain.KnownSet(
		refinementsets.MakeRefinedSet(refinementsets.AtLeast(math.Inf(-1))),
		nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone,
	)
}

// nonNegativeRay is the atLeast(0) set r.hi/r.lo read as in
// B6.est.call.ts.
func nonNegativeRay() abstractdomain.AbstractValue {
	return abstractdomain.KnownSet(
		refinementsets.MakeRefinedSet(refinementsets.AtLeast(0)),
		nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone,
	)
}

// requireHeldGroundAnswer asserts the shared shape every held-arm answer
// must carry: a determined KindSet (never KindUnknown), graded (a
// non-empty Grade — TrustProved's own empty-Grade convention reads as
// the seed AfterReaders hands an unexamined expression, which
// nan_wrapper.go's CheckPossiblyNaN declines rather than checks), and
// poseable (OnOneTupleLayer, so a sink's subset question can actually be
// asked of it).
func requireHeldGroundAnswer(t *testing.T, got abstractdomain.AbstractValue) refinementsets.RefinedSet {
	t.Helper()
	if got.Kind == abstractdomain.KindUnknown {
		t.Fatalf("got KindUnknown (residue), want a determined whole-ground answer")
	}
	inner := got
	if got.Kind == abstractdomain.KindPossiblyNaN {
		if got.Grade == "" {
			t.Errorf("Grade = %q, want a non-empty grade — an ungraded possibly-NaN wrapper reads as AfterReaders' own never-examined seed, which CheckPossiblyNaN declines rather than checks", got.Grade)
		}
		inner = *got.Inner
	}
	if inner.Kind != abstractdomain.KindSet {
		t.Fatalf("the real half's Kind = %v, want KindSet", inner.Kind)
	}
	if !refinementsets.OnOneTupleLayer(inner.Set) {
		t.Fatalf("the real half's set is not OnOneTupleLayer (forms = %+v) — a formless RefinedSet{} cannot be posed as a sink's subset question", inner.Set.Forms)
	}
	return inner.Set
}

// TestTransferBinaryOf_AddOfUnboundedRayAndExactSingletonAnswersHeldGround
// pins A10.sink.arg.ts:18's shape: `x + 1` where x is an unbounded
// number-sorted operand (a callback parameter) and 1 is an exact
// singleton. The arm answers the sort's whole ground wrapped
// possibly-NaN unconditionally (the same conservative shape the OpDiv
// and OpRem arms above it wrap their own ground answers in) — it does
// not attempt to prove NaN unreachable, only that neither operand
// carries less than number-sorted knowledge.
func TestTransferBinaryOf_AddOfUnboundedRayAndExactSingletonAnswersHeldGround(t *testing.T) {
	loadHeldGroundTransferKernel(t)
	one := abstractdomain.KnownValues([]float64{1}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
	got := TransferBinary(OpAdd, wholeLineRay(), one)
	requireHeldGroundAnswer(t, got)
	if got.Kind != abstractdomain.KindPossiblyNaN {
		t.Errorf("Kind = %v, want KindPossiblyNaN — the held arm wraps its ground answer conservatively", got.Kind)
	}
}

// TestTransferBinaryOf_SubOfTwoNonNegativeRaysAnswersHeldGround pins
// B6.est.call.ts:31's shape: `r.hi - r.lo`, where r.hi reads possibly-NaN
// (an unbounded number field the walk never proved finite) and r.lo
// reads a plain atLeast(0) set. binary64.sub's true image over
// [0, ∞) − [0, ∞) is the whole line, or NaN when the possibly-NaN
// operand actually is NaN (sec-numeric-types-number-subtract) — the
// possibly-NaN wrapper must ride through.
func TestTransferBinaryOf_SubOfTwoNonNegativeRaysAnswersHeldGround(t *testing.T) {
	loadHeldGroundTransferKernel(t)
	hiPossiblyNaN := abstractdomain.PossiblyNaN(nonNegativeRay())
	got := TransferBinary(OpSub, hiPossiblyNaN, nonNegativeRay())
	requireHeldGroundAnswer(t, got)
	if got.Kind != abstractdomain.KindPossiblyNaN {
		t.Errorf("Kind = %v, want KindPossiblyNaN — r.hi may be NaN, so the difference may be NaN too", got.Kind)
	}
}

// TestTransferBinaryOf_MulOfUnboundedRayAndExactSingletonAnswersHeldGround
// pins A13.sink.assign.ts:11's shape: `seconds * 1000` over an unbounded
// `seconds`. Same conservative possibly-NaN wrap as the add case above.
func TestTransferBinaryOf_MulOfUnboundedRayAndExactSingletonAnswersHeldGround(t *testing.T) {
	loadHeldGroundTransferKernel(t)
	oneThousand := abstractdomain.KnownValues([]float64{1000}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
	got := TransferBinary(OpMul, wholeLineRay(), oneThousand)
	requireHeldGroundAnswer(t, got)
	if got.Kind != abstractdomain.KindPossiblyNaN {
		t.Errorf("Kind = %v, want KindPossiblyNaN — the held arm wraps its ground answer conservatively", got.Kind)
	}
}
