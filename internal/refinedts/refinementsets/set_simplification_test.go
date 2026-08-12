package refinementsets

import (
	"math"
	"testing"
)

// fakeKernel is a test-only SimplificationKernel that answers ScalarSubset,
// Bounds, and Members by brute-force enumeration over a generous
// integer window. The TS source's own test (set_simplification.test.ts)
// runs these same cases against the REAL Lean kernel via loadKernel(),
// which this port cannot reach: kernel_bridge is ported after
// refinement_sets (PORT.md's port order), and no dylib artifact is
// available to this package regardless. Every case this file checks is
// a small, flat integer-interval question, so a brute-force decider is
// semantically faithful to what the real kernel would answer for these
// specific sets -- it is not a general substitute for the kernel's
// proof engine, and is not used outside this test.
type fakeKernel struct{}

const fakeKernelWindow = 1_000_000

func (fakeKernel) member(set RefinedSet, x float64) bool {
	ok, definite := satisfiesScalar(x, set)
	return definite && ok
}

func (k fakeKernel) ScalarSubset(a, b RefinedSet) bool {
	ha, ok := hullOf(a)
	if !ok {
		panic("fakeKernel: a has no scalar hull")
	}
	lo, hi := ha.lo, ha.hi
	if lo < -fakeKernelWindow || hi > fakeKernelWindow {
		panic("fakeKernel: window too wide for brute force")
	}
	if ha.integral {
		for v := math.Ceil(lo); v <= hi; v++ {
			if k.member(a, v) && !k.member(b, v) {
				return false
			}
		}
		return true
	}
	// a continuous window: sample densely enough for these tests'
	// integer-edged sets (every case here is integral or interval-only)
	for v := lo; v <= hi; v++ {
		if k.member(a, v) && !k.member(b, v) {
			return false
		}
	}
	return true
}

func (k fakeKernel) Bounds(set RefinedSet) BoundsResult {
	h, ok := hullOf(set)
	if !ok {
		panic("fakeKernel: set has no scalar hull")
	}
	// tighten to the actual least/greatest member within the syntactic
	// hull, the way the real kernel's bisection would
	lo, hi := h.lo, h.hi
	step := 1.0
	if !h.integral {
		step = 0.0001
	}
	var trueLo, trueHi float64
	foundAny := false
	for v := lo; v <= hi; v += step {
		if k.member(set, v) {
			if !foundAny {
				trueLo = v
				foundAny = true
			}
			trueHi = v
		}
	}
	if !foundAny {
		return BoundsResult{Empty: true}
	}
	return BoundsResult{Empty: false, Hull: asHullSet(hull{lo: trueLo, hi: trueHi, integral: h.integral})}
}

func (k fakeKernel) Members(set RefinedSet, cap int) []float64 {
	h, ok := hullOf(set)
	if !ok || !h.integral {
		panic("fakeKernel: set is not a bounded integral set")
	}
	if h.hi-h.lo+1 > float64(cap) {
		panic("fakeKernel: hull wider than cap")
	}
	var out []float64
	for v := h.lo; v <= h.hi; v++ {
		if k.member(set, v) {
			out = append(out, v)
		}
	}
	return out
}

func TestSimplifyALoopsInvariantSaysWhatItHolds(t *testing.T) {
	kernel := fakeKernel{}
	// the shape a certified invariant arrives in: the fixpoint's union,
	// narrowed by the loop's own condition
	invariant := MakeRefinedSet(
		Below(3),
		Union(
			MakeRefinedSet(OneOf([]float64{0})),
			MakeRefinedSet(AtLeast(1), AtMost(3), Integer),
		),
	)
	if got, _ := FormatForHover(invariant); got != "{𝑥 < 3, 0 | (integer, 1 ≤ 𝑥 ≤ 3)}" {
		t.Fatalf("invariant hover = %q", got)
	}
	plain := SimplifyScalar(kernel, invariant)
	if got, _ := FormatForHover(plain); got != "{integer, 0 ≤ 𝑥 ≤ 2}" {
		t.Errorf("plain hover = %q, want {integer, 0 ≤ 𝑥 ≤ 2}", got)
	}
	// the same values, both directions
	if !kernel.ScalarSubset(invariant, plain) {
		t.Errorf("invariant not subset of plain")
	}
	if !kernel.ScalarSubset(plain, invariant) {
		t.Errorf("plain not subset of invariant")
	}
}

func TestSimplifyALoneValueIsThatValue(t *testing.T) {
	kernel := fakeKernel{}
	// two branches far apart, all but one ruled out -- the hull the
	// forms state is 100 wide and the set holds one value
	one := MakeRefinedSet(
		AtLeast(3),
		Union(MakeRefinedSet(OneOf([]float64{0})), MakeRefinedSet(OneOf([]float64{100}))),
	)
	plain := SimplifyScalar(kernel, one)
	if got, _ := FormatForHover(plain); got != "100" {
		t.Errorf("plain hover = %q, want 100", got)
	}
	if !kernel.ScalarSubset(one, plain) || !kernel.ScalarSubset(plain, one) {
		t.Errorf("subset mismatch")
	}
}

func TestSimplifyAScatteredHandfulIsNamedALongOneIsNot(t *testing.T) {
	kernel := fakeKernel{}
	// three values, not consecutive: naming them is shorter
	few := MakeRefinedSet(
		AtLeast(0), AtMost(10), Integer,
		Union(MakeRefinedSet(OneOf([]float64{1, 5})), MakeRefinedSet(OneOf([]float64{9}))),
	)
	if got, _ := FormatForHover(SimplifyScalar(kernel, few)); got != "{1 | 5 | 9}" {
		t.Errorf("few hover = %q, want {1 | 5 | 9}", got)
	}
	// the even numbers to 20: naming eleven values reads worse than the
	// forms, so the forms stay
	many := MakeRefinedSet(AtLeast(0), AtMost(20), MultipleOf(2), Integer)
	if got := SimplifyScalar(kernel, many); !equalSet(got, many) {
		t.Errorf("many should stay unchanged")
	}
}

func TestSimplifyNearlyARangeIsTheRangeAndWhatItLeavesOut(t *testing.T) {
	kernel := fakeKernel{}
	// the shape a stepped loop's invariant arrives in. It holds every
	// integer from 0 to 19 except 1 -- too many values to name, and not
	// a range either, but the range MINUS one value is two facts
	stepped := MakeRefinedSet(
		Below(20),
		Union(
			MakeRefinedSet(OneOf([]float64{0})),
			MakeRefinedSet(AtLeast(2), AtMost(21), Integer),
		),
	)
	plain := SimplifyScalar(kernel, stepped)
	if got, _ := FormatForHover(plain); got != "{integer, 0 ≤ 𝑥 ≤ 19, ≠ 1}" {
		t.Errorf("plain hover = %q, want {integer, 0 ≤ 𝑥 ≤ 19, ≠ 1}", got)
	}
	if !kernel.ScalarSubset(stepped, plain) || !kernel.ScalarSubset(plain, stepped) {
		t.Errorf("subset mismatch")
	}
}

func TestSimplifyNothingIsReplacedWithoutTheKernelProvingIt(t *testing.T) {
	kernel := fakeKernel{}
	// a set whose hull is honest but whose members are not a range: the
	// hull proposal is refuted, and what comes back still holds exactly
	// the original values
	gapped := MakeRefinedSet(AtLeast(0), AtMost(9), Integer, MultipleOf(3))
	plain := SimplifyScalar(kernel, gapped)
	if !kernel.ScalarSubset(gapped, plain) || !kernel.ScalarSubset(plain, gapped) {
		t.Errorf("subset mismatch")
	}

	// a sequence set is not a scalar question: untouched
	sequence := MakeRefinedSet(Star(MakeRefinedSet(AtLeast(0))), AtLeast(0))
	if got := SimplifyScalar(kernel, sequence); !equalSet(got, sequence) {
		t.Errorf("sequence should stay unchanged")
	}

	// one form is already as plain as it gets
	single := MakeRefinedSet(AtLeast(0))
	if got := SimplifyScalar(kernel, single); !equalSet(got, single) {
		t.Errorf("single-form set should stay unchanged")
	}
}
