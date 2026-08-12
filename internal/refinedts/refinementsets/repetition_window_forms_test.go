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
	if got := Repetition(E, 1, intPtr(1)); !reflect.DeepEqual(got, E) {
		t.Errorf("Repetition(E,1,1) = %+v, want %+v", got, E)
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
