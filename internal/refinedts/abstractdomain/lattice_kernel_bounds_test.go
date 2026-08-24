package abstractdomain

import (
	"reflect"
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// boundsKernel loads the native kernel the same way every other
// kernel-gated test in this package does (skip when the dylib is
// absent, fatal on a load error).
func boundsKernel(t *testing.T) *kernelbridge.RefinedTSKernel {
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

// TestKernelBounds_RefusesWithNoKernelSeated pins the degraded-never-
// wrong contract every kernel ask in this file keeps: with
// SetLatticeKernel never called (or explicitly cleared), kernelBounds
// answers ok=false rather than panicking on a nil kernel.
func TestKernelBounds_RefusesWithNoKernelSeated(t *testing.T) {
	SetLatticeKernel(nil)
	_, ok := kernelBounds(refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.AtMost(3), refinementsets.Integer))
	if ok {
		t.Errorf("kernelBounds with no kernel seated answered ok=true, want a refusal")
	}
}

// TestKernelBounds_EmptyConjunctionAnswersEmpty pins the shape item 1's
// fix depends on: a scalar conjunction whose lower ray sits strictly
// above its upper bound (Above(5) ∧ AtMost(3) ∧ Integer — the exact
// spelling a place-value narrowing met against a tighter natural bound
// produces) answers Empty: true, the kernel's own scalarEmptyB verdict
// — not a panic, not a wrong nonempty hull.
func TestKernelBounds_EmptyConjunctionAnswersEmpty(t *testing.T) {
	kernel := boundsKernel(t)
	SetLatticeKernel(kernel)
	t.Cleanup(func() { SetLatticeKernel(nil) })
	contradictory := refinementsets.MakeRefinedSet(
		refinementsets.Above(5), refinementsets.AtMost(3), refinementsets.Integer,
	)
	result, ok := kernelBounds(contradictory)
	if !ok {
		t.Fatalf("kernelBounds(Above(5) ∧ AtMost(3) ∧ Integer) refused, want an answer")
	}
	if !result.Empty {
		t.Errorf("kernelBounds(Above(5) ∧ AtMost(3) ∧ Integer).Empty = false, want true — no integer satisfies both bounds")
	}
}

// TestKernelBounds_NonemptyIntegralWindowAnswersItsOwnEdges pins the
// positive case collapseTouchingHulls reads back: a bounded, nonempty
// integral window answers its own least and greatest members through
// the hull field, unchanged from what the window already states (both
// edges are attained members here, so no bisection runs).
func TestKernelBounds_NonemptyIntegralWindowAnswersItsOwnEdges(t *testing.T) {
	kernel := boundsKernel(t)
	SetLatticeKernel(kernel)
	t.Cleanup(func() { SetLatticeKernel(nil) })
	window := refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.AtMost(3), refinementsets.Integer)
	result, ok := kernelBounds(window)
	if !ok {
		t.Fatalf("kernelBounds([0,3]∩ℤ) refused, want an answer")
	}
	if result.Empty {
		t.Fatalf("kernelBounds([0,3]∩ℤ).Empty = true, want false — [0,3]∩ℤ holds 0..3")
	}
	lo, hi, edgesOK := integralClosedWindow(result.Hull)
	if !edgesOK || lo != 0 || hi != 3 {
		t.Errorf("kernelBounds([0,3]∩ℤ).Hull = %+v, want the closed window [0,3]", result.Hull)
	}
}

// TestCollapseTouchingHulls_AdjacentIntegerWindowsCollapseToTheirHull
// pins the Bounds-driven collapse's positive case: two windows that
// touch (one's floor sits at or one past the other's ceiling) fold to
// the single hull spanning both — the same shape the syntactic
// integerRunOf fast path already produces when it recognizes the
// operands directly, reached here through the Bounds-derived reading
// instead.
func TestCollapseTouchingHulls_AdjacentIntegerWindowsCollapseToTheirHull(t *testing.T) {
	a := refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.AtMost(3), refinementsets.Integer)
	b := refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.AtMost(0), refinementsets.Integer) // {0}
	collapsed, ok := collapseTouchingHulls(a, b)
	if !ok {
		t.Fatalf("collapseTouchingHulls([0,3], [0,0]) declined, want a collapse — the windows overlap")
	}
	want := refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.AtMost(3), refinementsets.Integer)
	if !reflect.DeepEqual(collapsed, want) {
		t.Errorf("collapseTouchingHulls([0,3], [0,0]) = %+v, want %+v", collapsed, want)
	}
}

// TestCollapseTouchingHulls_DisjointIntegerWindowsDecline pins the
// negative case: two windows with a genuine gap between them (no
// touching or overlap) must NOT collapse — collapsing would claim
// membership for values neither side ever admits.
func TestCollapseTouchingHulls_DisjointIntegerWindowsDecline(t *testing.T) {
	a := refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.AtMost(3), refinementsets.Integer)
	b := refinementsets.MakeRefinedSet(refinementsets.AtLeast(100), refinementsets.AtMost(103), refinementsets.Integer)
	if _, ok := collapseTouchingHulls(a, b); ok {
		t.Errorf("collapseTouchingHulls([0,3], [100,103]) collapsed, want a decline — the windows are disjoint with a real gap")
	}
}

// TestCollapseTouchingHulls_UnboundedOrNonIntegralHullsDecline pins
// integralClosedWindow's own refusal: a hull missing a finite edge, or
// missing the Integer mark, is not the closed-window shape this
// collapse reads, and must decline rather than misread a wider claim
// as a narrow one.
func TestCollapseTouchingHulls_UnboundedOrNonIntegralHullsDecline(t *testing.T) {
	bounded := refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.AtMost(3), refinementsets.Integer)
	unbounded := refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.Integer)
	if _, ok := collapseTouchingHulls(bounded, unbounded); ok {
		t.Errorf("collapseTouchingHulls([0,3], [0,+inf)) collapsed, want a decline — the second hull has no finite ceiling")
	}
	nonIntegral := refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.AtMost(3))
	if _, ok := collapseTouchingHulls(bounded, nonIntegral); ok {
		t.Errorf("collapseTouchingHulls([0,3]∩ℤ, [0,3]) collapsed, want a decline — the second hull carries no Integer mark")
	}
}

// TestJoinKnown_BoundsCollapseRepairsAnEmptyConjunctionArm is the
// join-level twin of item 1's meet-level fix: a scalar side that is
// EMPTY (a contradictory conjunction — the shape a bad meet can still
// produce even after the meetHeldPlaceEntry fix, from any other
// producer) joins as the pure identity, ∅ ∪ B = B, rather than
// stacking a raw Union term the syntactic integerRunOf path cannot
// read (it has no case for the Above form this shape carries).
func TestJoinKnown_BoundsCollapseRepairsAnEmptyConjunctionArm(t *testing.T) {
	kernel := boundsKernel(t)
	SetLatticeKernel(kernel)
	t.Cleanup(func() { SetLatticeKernel(nil) })
	empty := KnownSet(
		refinementsets.MakeRefinedSet(refinementsets.Above(5), refinementsets.AtMost(3), refinementsets.Integer),
		nil, TrustProved, SetKindTagNone,
	)
	zero := KnownValues([]float64{0}, PrimitiveNumber, TrustProved)
	joined := JoinKnown(empty, zero)
	if joined.Kind != KindValues && joined.Kind != KindSet {
		t.Fatalf("JoinKnown(∅-conjunction, {0}) = %+v, want the {0} side alone (the join identity)", joined)
	}
	set, ok := SetOfKnown(joined)
	if !ok {
		t.Fatalf("JoinKnown(∅-conjunction, {0}) denoted no set at all")
	}
	if !reflect.DeepEqual(set, refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{0}))) {
		t.Errorf("JoinKnown(∅-conjunction, {0}) = %+v, want exactly {0} — the empty side contributes nothing", set)
	}
}

// TestJoinKnown_PointsOnlyJoinStaysExactNeverHulls is the gate's own
// negative pin: TestAnalyzeTryStatement_TheCatchEnvJoinsEveryTryPrefixSnapshot's
// own regression, reproduced directly at the JoinKnown layer — three
// plain numeric writes ({0}, {1}, {2}) joined pairwise, exactly the
// shape a try body's snapshot join produces. Every side at every round
// is exact points only (a bare oneOf, or a union tree of bare oneOfs —
// exactPointsOnly's own reading), so the Bounds-driven hull collapse
// must never fire even with a kernel seated — the exact member
// spelling has to survive, since hover text, diagnostic sentences, and
// this family of test all read it back by MEMBERSHIP, not by bound.
func TestJoinKnown_PointsOnlyJoinStaysExactNeverHulls(t *testing.T) {
	kernel := boundsKernel(t)
	SetLatticeKernel(kernel)
	t.Cleanup(func() { SetLatticeKernel(nil) })
	zero := KnownValues([]float64{0}, PrimitiveNumber, TrustProved)
	one := KnownValues([]float64{1}, PrimitiveNumber, TrustProved)
	two := KnownValues([]float64{2}, PrimitiveNumber, TrustProved)

	firstRound := JoinKnown(zero, one)
	firstSet, ok := SetOfKnown(firstRound)
	if !ok {
		t.Fatalf("JoinKnown({0}, {1}) denoted no set at all")
	}
	if !exactPointsOnly(firstSet) {
		t.Fatalf("JoinKnown({0}, {1}) = %+v, want a points-only spelling (a bare oneOf or a union of them) — a hull here is what broke the try-catch pin", firstSet)
	}

	secondRound := JoinKnown(firstRound, two)
	finalSet, ok := SetOfKnown(secondRound)
	if !ok {
		t.Fatalf("JoinKnown({0,1}, {2}) denoted no set at all")
	}
	if !exactPointsOnly(finalSet) {
		t.Fatalf("JoinKnown({0,1}, {2}) = %+v, want a points-only spelling, not a hull — three exact-point sides never earn the Bounds collapse", finalSet)
	}
	// membership, not bound-reading, is the contract a points-only
	// spelling exists to keep: every one of the three original values
	// must still be a member — walked directly off the oneOf tree
	// (pointSetContains), the same shape a hover/diagnostic member list
	// would read
	for _, want := range []float64{0, 1, 2} {
		if !pointSetContains(finalSet, want) {
			t.Errorf("JoinKnown({0,1}, {2}) = %+v, want %v among its exact members", finalSet, want)
		}
	}
}

// pointSetContains walks a points-only tree (exactPointsOnly's own
// shape) and reports whether v is one of its oneOf leaves.
func pointSetContains(set refinementsets.RefinedSet, v float64) bool {
	if len(set.Forms) != 1 {
		return false
	}
	form := set.Forms[0]
	switch form.Form {
	case refinementsets.FormOneOf:
		for _, w := range form.W {
			if w == v {
				return true
			}
		}
		return false
	case refinementsets.FormUnion:
		return form.A_ != nil && form.B != nil && (pointSetContains(*form.A_, v) || pointSetContains(*form.B, v))
	default:
		return false
	}
}

// TestJoinKnown_MixedPointAndWindowJoinStillCollapses is the gate's own
// positive pin, beside the negative one above: item 1's repetition
// case, where ONE side is a genuine window ([0,3] the syntactic
// integerRunOf fast path already reads) and the other is a bare point
// ({0}) — not points-only on both sides, so the gate must still let
// the existing integerRunOf collapse (or, were that path to decline,
// the Bounds-driven one) produce the single hull [0,3], never a raw
// Union term.
func TestJoinKnown_MixedPointAndWindowJoinStillCollapses(t *testing.T) {
	window := KnownSet(
		refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.AtMost(3), refinementsets.Integer),
		nil, TrustProved, SetKindTagNone,
	)
	zero := KnownValues([]float64{0}, PrimitiveNumber, TrustProved)
	joined := JoinKnown(window, zero)
	set, ok := SetOfKnown(joined)
	if !ok {
		t.Fatalf("JoinKnown([0,3]∩ℤ, {0}) denoted no set at all")
	}
	want := refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.AtMost(3), refinementsets.Integer)
	if !reflect.DeepEqual(set, want) {
		t.Errorf("JoinKnown([0,3]∩ℤ, {0}) = %+v, want %+v — one side is a genuine window, so the collapse must still fire", set, want)
	}
}
