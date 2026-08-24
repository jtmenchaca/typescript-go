package refinementsets

import (
	"reflect"
	"testing"
)

var repetitionWindowsE = MakeRefinedSet(Integer, OneOf([]float64{1, 2}))

func TestRepetitionBuildsBoundedRepetition(t *testing.T) {
	E := repetitionWindowsE
	if got := Repetition(E, 0, nil).Forms[0].Form; got != FormStar {
		t.Errorf("Repetition(E,0,nil).Forms[0].Form = %v, want star", got)
	}
	// a NON-codepoint element (E here -- integers in {1,2}, the shape
	// z.array(z.number()...) elements take) never collapses at (1,1):
	// a 1-element ARRAY is [E], never bare E, so every window keeps
	// its Repeat wrapper -- only Codepoints collapses (below).
	if got := Repetition(E, 1, intPtr(1)).Forms[0].Form; got != FormRepeat {
		t.Errorf("Repetition(E,1,1).Forms[0].Form = %v, want repeat", got)
	}
	if rep, ok := AsRepetition(Repetition(E, 1, intPtr(1))); !ok || rep.Lo != 1 || rep.Hi == nil || *rep.Hi != 1 || !reflect.DeepEqual(rep.Element, E) {
		t.Errorf("AsRepetition(Repetition(E,1,1)) = %+v (ok=%v), want element %+v, lo=1, hi=1", rep, ok, E)
	}
	// a CODEPOINT element DOES collapse at (1,1): a 1-character
	// string is itself a scalar (z.string().length(1) stays the
	// Codepoints set).
	if got := Repetition(Codepoints, 1, intPtr(1)); !reflect.DeepEqual(got, Codepoints) {
		t.Errorf("Repetition(Codepoints,1,1) = %+v, want %+v", got, Codepoints)
	}
	// everything else is the native window -- O(1) syntax
	if got := Repetition(E, 2, nil).Forms[0].Form; got != FormRepeat {
		t.Errorf("Repetition(E,2,nil).Forms[0].Form = %v, want repeat", got)
	}
	if got := Repetition(E, 0, intPtr(2)).Forms[0].Form; got != FormRepeat {
		t.Errorf("Repetition(E,0,2).Forms[0].Form = %v, want repeat", got)
	}
	if got := Repetition(E, 3, intPtr(3)).Forms[0].Form; got != FormRepeat {
		t.Errorf("Repetition(E,3,3).Forms[0].Form = %v, want repeat", got)
	}
	if got := RepeatExactly(E, 0).Forms[0].Form; got != FormEmptyTuple {
		t.Errorf("RepeatExactly(E,0).Forms[0].Form = %v, want emptyTuple", got)
	}
	if got := RepeatExactly(E, 1); !reflect.DeepEqual(got, E) {
		t.Errorf("RepeatExactly(E,1) = %+v, want %+v", got, E)
	}
}

func TestAsRepetitionReadsBackExactlyWhatRepetitionBuilds(t *testing.T) {
	E := repetitionWindowsE
	cases := []struct {
		lo int
		hi *int
	}{
		{0, nil},
		{2, nil},
		{3, intPtr(3)},
		{0, intPtr(2)},
		{1, intPtr(3)},
	}
	for _, c := range cases {
		read, ok := AsRepetition(Repetition(E, c.lo, c.hi))
		if !ok {
			t.Errorf("AsRepetition(Repetition(E,%d,%v)) failed", c.lo, c.hi)
			continue
		}
		if read.Lo != c.lo {
			t.Errorf("read.Lo = %d, want %d", read.Lo, c.lo)
		}
		if (read.Hi == nil) != (c.hi == nil) || (read.Hi != nil && c.hi != nil && *read.Hi != *c.hi) {
			t.Errorf("read.Hi = %v, want %v", read.Hi, c.hi)
		}
		if !reflect.DeepEqual(read.Element, E) {
			t.Errorf("read.Element = %+v, want %+v", read.Element, E)
		}
	}
}

func TestNonRepetitionShapesReadAsNull(t *testing.T) {
	if _, ok := AsRepetition(repetitionWindowsE); ok {
		t.Errorf("AsRepetition(E) should fail")
	}
	if _, ok := AsRepetition(MakeRefinedSet()); ok {
		t.Errorf("AsRepetition(empty) should fail")
	}
}

func TestBoundsAreNaturalsOrdered(t *testing.T) {
	mustPanicWith(t, "natural", func() { Repetition(repetitionWindowsE, -1, nil) })
	mustPanicWith(t, "natural", func() { Repetition(repetitionWindowsE, 3, intPtr(2)) })
}

// TestTightenRepetitionDeclinesAContradictoryWindow pins the crash-class
// fix: a base already bounded above (hi=3) tightened by a "min" guard
// that asks for a floor past that ceiling (min=6) is the EMPTY
// intersection -- no value satisfies both bounds at once. That is a
// vacuous row, not a malformed set, so TightenRepetition must decline
// it (ok=false) rather than let Repetition's own natural-ordered-bound
// invariant panic out of this constructor.
func TestTightenRepetitionDeclinesAContradictoryWindow(t *testing.T) {
	E := repetitionWindowsE
	base := Repetition(E, 0, intPtr(3))
	if _, ok := TightenRepetition(base, "min", 6, nil); ok {
		t.Errorf("TightenRepetition(base, min, 6) should decline -- [6,3] is the empty window")
	}
	// the same contradiction from the other direction: an already
	// min-floored base tightened by a "max" guard below that floor
	baseFloored := Repetition(E, 5, nil)
	if _, ok := TightenRepetition(baseFloored, "max", 2, nil); ok {
		t.Errorf("TightenRepetition(baseFloored, max, 2) should decline -- [5,2] is the empty window")
	}
	// a non-contradictory tighten still builds, unaffected by the guard
	tightened, ok := TightenRepetition(base, "min", 2, nil)
	if !ok {
		t.Fatalf("TightenRepetition(base, min, 2) should succeed -- [2,3] is a real window")
	}
	rep, repOk := AsRepetition(tightened)
	if !repOk || rep.Lo != 2 || rep.Hi == nil || *rep.Hi != 3 {
		t.Errorf("TightenRepetition(base, min, 2) = %+v (ok=%v), want [2,3]", rep, repOk)
	}
}
