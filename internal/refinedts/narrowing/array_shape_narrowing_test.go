// Ports the array_shape_narrowing.test.ts coverage for literal-array
// `.includes` — the whenTrue pin (already covered by
// condition_analysis_test.go's kernel-backed suite style) plus the
// whenFalse fold: the kernel's Or-of-Eq/EqSeq complement, weak, and its
// declines.
//
// Both held branches carry TWO whenTrue rows on the tested place: the
// structural pin ArrayShapeLeaf states, and the kernel's own answer to
// the fold IncludesMembershipTree lowers. The kernel row is no longer
// suppressed when a structural pin already covers the place — it is
// strictly tighter under a conjunction, and both narrowings apply
// intersectively.
package narrowing

import "testing"

func TestIncludesOnANumericListNarrowsBothBranches(t *testing.T) {
	loadNarrowKernel(t)
	held := ofCondition(t, "[1, 2, 3].includes(x)")
	// TWO rows on the same place, both strong: the structural pin
	// (array_shape_narrowing.go's `oneOf`) and the kernel's own answer
	// to the Or-of-Eq fold (a `union`, the same shape
	// `x === 1 || x === 2` gets). The kernel row rides BESIDE the
	// structural one rather than being suppressed behind it — for a
	// conjunction it carries every conjunct and is strictly tighter, so
	// dropping it kept a refuted member alive (condition_analysis.go's
	// A2.guard.ne note). Both apply intersectively, so the restatement
	// costs nothing here; settle's stable sort keeps the structural pin
	// first because neither is Refuting.
	if len(held.WhenTrue) != 2 || held.WhenTrue[0].Binding != "x" || held.WhenTrue[1].Binding != "x" {
		t.Fatalf("held.WhenTrue = %+v", held.WhenTrue)
	}
	if len(held.WhenTrue[0].Forms) != 1 || held.WhenTrue[0].Forms[0].Form != "oneOf" {
		t.Errorf("held.WhenTrue[0].Forms = %+v", held.WhenTrue[0].Forms)
	}
	if held.WhenTrue[0].Refuting || held.WhenTrue[1].Refuting {
		t.Errorf("held.WhenTrue refuting = %v, %v, want false, false",
			held.WhenTrue[0].Refuting, held.WhenTrue[1].Refuting)
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
	// two rows again, for the same reason the numeric twin above states:
	// the structural word-union pin, then the kernel's answer to the
	// Or-of-EqSeq fold
	if len(held.WhenTrue) != 2 || held.WhenTrue[0].Binding != "x" || held.WhenTrue[1].Binding != "x" {
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
