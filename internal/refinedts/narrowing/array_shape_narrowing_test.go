// Ports the array_shape_narrowing.test.ts coverage for literal-array
// `.includes` — the whenTrue pin (already covered by
// condition_analysis_test.go's kernel-backed suite style) plus the new
// whenFalse fold this file adds: the kernel's Or-of-Eq/EqSeq complement,
// weak, and its declines.
package narrowing

import "testing"

func TestIncludesOnANumericListNarrowsBothBranches(t *testing.T) {
	loadNarrowKernel(t)
	held := ofCondition(t, "[1, 2, 3].includes(x)")
	if len(held.WhenTrue) != 1 || held.WhenTrue[0].Binding != "x" {
		t.Fatalf("held.WhenTrue = %+v", held.WhenTrue)
	}
	if len(held.WhenTrue[0].Forms) != 1 || held.WhenTrue[0].Forms[0].Form != "oneOf" {
		t.Errorf("held.WhenTrue[0].Forms = %+v", held.WhenTrue[0].Forms)
	}
	// the whenFalse complement is new: the kernel's Or-of-Eq fold
	// answers the real-line difference, weak (Refuting true) — the
	// same shape a `x === 1 || x === 2 || x === 3` refutation takes
	if len(held.WhenFalse) != 1 || held.WhenFalse[0].Binding != "x" {
		t.Fatalf("held.WhenFalse = %+v", held.WhenFalse)
	}
	if !held.WhenFalse[0].Refuting {
		t.Errorf("held.WhenFalse[0].Refuting = false, want true")
	}
	if len(held.WhenFalse[0].Forms) == 0 || held.WhenFalse[0].Forms[0].Form != "difference" {
		t.Errorf("held.WhenFalse[0].Forms = %+v", held.WhenFalse[0].Forms)
	}
}

func TestIncludesOnAStringListNarrowsBothBranches(t *testing.T) {
	loadNarrowKernel(t)
	c, file := checkerFor(t, `function f(x: string, y: string) { if (["a", "b"].includes(x)) { return x; } return y; }`)
	cond := firstIfCondition(file)
	held := Narrowings(c, cond, func(string) bool { return true }, nil, GuardReadNowhere)
	if len(held.WhenTrue) != 1 || held.WhenTrue[0].Binding != "x" {
		t.Fatalf("held.WhenTrue = %+v", held.WhenTrue)
	}
	if len(held.WhenTrue[0].Forms) == 0 || held.WhenTrue[0].Forms[0].Form != "union" {
		t.Errorf("held.WhenTrue[0].Forms = %+v", held.WhenTrue[0].Forms)
	}
	// the whenFalse complement: the kernel's Or-of-EqSeq fold answers
	// the tuple difference, weak
	if len(held.WhenFalse) != 1 || held.WhenFalse[0].Binding != "x" {
		t.Fatalf("held.WhenFalse = %+v", held.WhenFalse)
	}
	if !held.WhenFalse[0].Refuting {
		t.Errorf("held.WhenFalse[0].Refuting = false, want true")
	}
	if len(held.WhenFalse[0].Forms) == 0 {
		t.Errorf("held.WhenFalse[0].Forms = %+v, want a nonempty complement", held.WhenFalse[0].Forms)
	}
}

func TestIncludesOnAMixedSortListDeclinesBothBranches(t *testing.T) {
	loadNarrowKernel(t)
	c, file := checkerFor(t, `function f(x: number | string, y: number | string) { if ([1, "a"].includes(x)) { return x; } return y; }`)
	cond := firstIfCondition(file)
	declined := Narrowings(c, cond, func(string) bool { return true }, nil, GuardReadNowhere)
	if len(declined.WhenTrue) != 0 {
		t.Errorf("declined.WhenTrue = %+v, want empty (mixed sort declines the pin)", declined.WhenTrue)
	}
	if len(declined.WhenFalse) != 0 {
		t.Errorf("declined.WhenFalse = %+v, want empty (mixed sort declines the fold too)", declined.WhenFalse)
	}
}
