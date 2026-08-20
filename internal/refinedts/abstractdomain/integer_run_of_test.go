// Companion test for the isInfinities precondition tightened in
// lattice_operations.go: the mark integerRunOf's Union branch
// recognizes (Integer | oneOf{...}) must read as "both infinities are
// members" for the negInf/posInf derivation below it to hold. Every
// producer emits the two-sided oneOf{+Inf, -Inf} (the kernel's
// intOrInfForm, encode_sets.lean; the Go mirror in
// walk/coercion_models.go), so this pins the recognizer's own
// declared precondition directly rather than waiting on a producer
// that cannot currently build the one-sided shape.
package abstractdomain

import (
	"math"
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// TestIntegerRunOf_TwoSidedInfinityMarkAdmitsBothFlags pins the
// existing positive case the fix must leave alone: Integer | oneOf{+Inf,
// -Inf} with no window bound reads as an unbounded run with both
// negInf and posInf set.
func TestIntegerRunOf_TwoSidedInfinityMarkAdmitsBothFlags(t *testing.T) {
	set := refinementsets.MakeRefinedSet(refinementsets.Union(
		refinementsets.MakeRefinedSet(refinementsets.Integer),
		refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{math.Inf(1), math.Inf(-1)})),
	))
	run, ok := integerRunOf(set)
	if !ok {
		t.Fatalf("integerRunOf(Integer | {+Inf,-Inf}) = (_, false), want true")
	}
	if !run.negInf || !run.posInf {
		t.Errorf("integerRunOf(Integer | {+Inf,-Inf}) = negInf=%v posInf=%v, want both true", run.negInf, run.posInf)
	}
}

// TestIntegerRunOf_OneSidedInfinityMarkDeclines is the precondition
// violation this fix closes: a oneOf holding ONLY +Inf (or only -Inf)
// is not the two-sided shape any producer emits, so the Union branch
// must decline (return integerRun{}, false) rather than let the
// negInf/posInf derivation assert an infinity the set does not hold.
func TestIntegerRunOf_OneSidedInfinityMarkDeclines(t *testing.T) {
	positiveOnly := refinementsets.MakeRefinedSet(refinementsets.Union(
		refinementsets.MakeRefinedSet(refinementsets.Integer),
		refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{math.Inf(1)})),
	))
	if _, ok := integerRunOf(positiveOnly); ok {
		t.Errorf("integerRunOf(Integer | {+Inf}) = (_, true), want false (one-sided mark must decline)")
	}

	negativeOnly := refinementsets.MakeRefinedSet(refinementsets.Union(
		refinementsets.MakeRefinedSet(refinementsets.Integer),
		refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{math.Inf(-1)})),
	))
	if _, ok := integerRunOf(negativeOnly); ok {
		t.Errorf("integerRunOf(Integer | {-Inf}) = (_, true), want false (one-sided mark must decline)")
	}
}

// TestIntegerRunOf_EmptyInfinityMarkDeclines pins the degenerate
// all-infinities-list shape the recognizer must also refuse: an empty
// oneOf trivially satisfies "every member is an infinity" but holds
// neither infinity.
func TestIntegerRunOf_EmptyInfinityMarkDeclines(t *testing.T) {
	empty := refinementsets.MakeRefinedSet(refinementsets.Union(
		refinementsets.MakeRefinedSet(refinementsets.Integer),
		refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{})),
	))
	if _, ok := integerRunOf(empty); ok {
		t.Errorf("integerRunOf(Integer | {}) = (_, true), want false (empty mark must decline)")
	}
}
