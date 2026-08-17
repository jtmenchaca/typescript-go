// from conformance/narrowing_conformance.test.ts
//
// The kernel's structural narrowings, pinned through the wire: each
// op filters a knowledge state into its two sides exactly as the
// proved theorems say (narrow*_sound, set_functions/known_state.lean).
// Membership questions read the answered sets back; the flags carry
// the absent/NaN discipline the adapter mirrors.

package conformance

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

type narrowingFlags struct {
	absent bool
	nan    bool
}

// flagged is the TS test's `flagged`: panics (mirroring the TS throw)
// on a top state, since every call site here expects a concrete one.
func flagged(t *testing.T, s kernelbridge.KnownStateWire) narrowingFlags {
	t.Helper()
	if s.Top {
		t.Fatalf("expected a concrete state")
	}
	return narrowingFlags{absent: s.Undef || s.Null, nan: s.Nan}
}

// setOf is the TS test's `setOf`.
func setOf(t *testing.T, s kernelbridge.KnownStateWire) refinementsets.RefinedSet {
	t.Helper()
	if s.Top {
		t.Fatalf("expected a concrete state")
	}
	return s.Set
}

func TestTheStructuralNarrowingsFilterTheWayTheProofsSay(t *testing.T) {
	if !kernelbridge.KernelArtifactsPresent(kernelbridge.DylibPath) {
		t.Skip("native kernel dylib absent — build it first")
	}
	kernel, err := kernelbridge.LoadKernel(kernelbridge.DylibPath)
	if err != nil {
		t.Fatalf("LoadKernel: %v", err)
	}

	wide := kernelbridge.KnownStateWire{
		Set:   refinementsets.MakeRefinedSet(refinementsets.AtLeast(-1)),
		Undef: true,
		Null:  true,
		Nan:   true,
	}

	// definedness: truth strips the absent flag and nothing else;
	// falsity leaves only the absent value
	definedTrue, definedFalse := kernel.NarrowState(wide, "defined", 0, false)
	if got, want := flagged(t, definedTrue), (narrowingFlags{absent: false, nan: true}); got != want {
		t.Errorf("defined.whenTrue flags = %+v, want %+v", got, want)
	}
	if got := kernel.Member(setOf(t, definedTrue), []float64{3}); !got {
		t.Errorf("member(defined.whenTrue, [3]) = %v, want true", got)
	}
	if got, want := flagged(t, definedFalse), (narrowingFlags{absent: true, nan: false}); got != want {
		t.Errorf("defined.whenFalse flags = %+v, want %+v", got, want)
	}
	if got := kernel.Member(setOf(t, definedFalse), []float64{3}); got {
		t.Errorf("member(defined.whenFalse, [3]) = %v, want false", got)
	}

	// numeric truthiness: truth removes 0, absence, and NaN;
	// falsity keeps exactly 0 and the two flags
	truthyTrue, truthyFalse := kernel.NarrowState(wide, "truthyNum", 0, false)
	if got, want := flagged(t, truthyTrue), (narrowingFlags{absent: false, nan: false}); got != want {
		t.Errorf("truthyNum.whenTrue flags = %+v, want %+v", got, want)
	}
	if got := kernel.Member(setOf(t, truthyTrue), []float64{0}); got {
		t.Errorf("member(truthyNum.whenTrue, [0]) = %v, want false", got)
	}
	if got := kernel.Member(setOf(t, truthyTrue), []float64{1}); !got {
		t.Errorf("member(truthyNum.whenTrue, [1]) = %v, want true", got)
	}
	if got := kernel.Member(setOf(t, truthyTrue), []float64{-0.5}); !got {
		t.Errorf("member(truthyNum.whenTrue, [-0.5]) = %v, want true", got)
	}
	if got, want := flagged(t, truthyFalse), (narrowingFlags{absent: true, nan: true}); got != want {
		t.Errorf("truthyNum.whenFalse flags = %+v, want %+v", got, want)
	}
	if got := kernel.Member(setOf(t, truthyFalse), []float64{0}); !got {
		t.Errorf("member(truthyNum.whenFalse, [0]) = %v, want true", got)
	}
	if got := kernel.Member(setOf(t, truthyFalse), []float64{1}); got {
		t.Errorf("member(truthyNum.whenFalse, [1]) = %v, want false", got)
	}

	// string truthiness from no knowledge at all: truth is every
	// nonempty tuple, falsity exactly the empty one
	strTrue, strFalse := kernel.NarrowState(kernelbridge.KnownStateWire{Top: true}, "truthyStr", 0, false)
	if got, want := flagged(t, strTrue), (narrowingFlags{absent: false, nan: false}); got != want {
		t.Errorf("truthyStr.whenTrue flags = %+v, want %+v", got, want)
	}
	if got := kernel.Member(setOf(t, strTrue), []float64{}); got {
		t.Errorf("member(truthyStr.whenTrue, []) = %v, want false", got)
	}
	if got := kernel.Member(setOf(t, strTrue), []float64{97}); !got {
		t.Errorf("member(truthyStr.whenTrue, [97]) = %v, want true", got)
	}
	if got := kernel.Member(setOf(t, strFalse), []float64{}); !got {
		t.Errorf("member(truthyStr.whenFalse, []) = %v, want true", got)
	}
	if got := kernel.Member(setOf(t, strFalse), []float64{97}); got {
		t.Errorf("member(truthyStr.whenFalse, [97]) = %v, want false", got)
	}

	// equality against 5: truth pins the word outright; falsity
	// removes it and keeps the flags
	eqTrue, eqFalse := kernel.NarrowState(wide, "eq", 5, true)
	if got, want := flagged(t, eqTrue), (narrowingFlags{absent: false, nan: false}); got != want {
		t.Errorf("eq.whenTrue flags = %+v, want %+v", got, want)
	}
	if got := kernel.Member(setOf(t, eqTrue), []float64{5}); !got {
		t.Errorf("member(eq.whenTrue, [5]) = %v, want true", got)
	}
	if got := kernel.Member(setOf(t, eqTrue), []float64{4}); got {
		t.Errorf("member(eq.whenTrue, [4]) = %v, want false", got)
	}
	if got, want := flagged(t, eqFalse), (narrowingFlags{absent: true, nan: true}); got != want {
		t.Errorf("eq.whenFalse flags = %+v, want %+v", got, want)
	}
	if got := kernel.Member(setOf(t, eqFalse), []float64{5}); got {
		t.Errorf("member(eq.whenFalse, [5]) = %v, want false", got)
	}
	if got := kernel.Member(setOf(t, eqFalse), []float64{4}); !got {
		t.Errorf("member(eq.whenFalse, [4]) = %v, want true", got)
	}

	// the adapter's own empty spelling round-trips as the absent-only
	// state through a join and a definedness split
	absentOnly := kernel.JoinState(
		kernelbridge.KnownStateWire{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf(nil)), Undef: true, Null: true, Nan: false},
		kernelbridge.KnownStateWire{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{7})), Undef: false, Null: false, Nan: false},
	)
	splitTrue, splitFalse := kernel.NarrowState(absentOnly, "defined", 0, false)
	if got := kernel.Member(setOf(t, splitTrue), []float64{7}); !got {
		t.Errorf("member(split.whenTrue, [7]) = %v, want true", got)
	}
	if got, want := flagged(t, splitTrue), (narrowingFlags{absent: false, nan: false}); got != want {
		t.Errorf("split.whenTrue flags = %+v, want %+v", got, want)
	}
	if got, want := flagged(t, splitFalse), (narrowingFlags{absent: true, nan: false}); got != want {
		t.Errorf("split.whenFalse flags = %+v, want %+v", got, want)
	}
}
