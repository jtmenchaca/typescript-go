package refinementsets

import (
	"math"
	"testing"
)

func TestTheSpellingFoldsTheGuardSpellsTheSameFoldAsCode(t *testing.T) {
	pct := MakeRefinedSet(AtLeast(math.Inf(-1)), AtLeast(0), AtMost(100))
	if got := FormatForDiagnostics(pct); got != ">= 0 && <= 100" {
		t.Errorf("FormatForDiagnostics(pct) = %q", got)
	}
	if got, ok := FormatAsGuardCode("r", pct); !ok || got != "r >= 0 && r <= 100" {
		t.Errorf("FormatAsGuardCode(r, pct) = %q, %v", got, ok)
	}
	count := MakeRefinedSet(AtLeast(1), Integer)
	if got, ok := FormatAsGuardCode("n", count); !ok || got != "n >= 1 && Number.isInteger(n)" {
		t.Errorf("FormatAsGuardCode(n, count) = %q, %v", got, ok)
	}
	if got, ok := FormatAsGuardCode("x", MakeRefinedSet(OneOf([]float64{5}))); !ok || got != "x === 5" {
		t.Errorf("FormatAsGuardCode(x, oneOf(5)) = %q, %v", got, ok)
	}
	if got, ok := FormatAsGuardCode("x", MakeRefinedSet(OneOf([]float64{1, 2}))); !ok || got != "(x === 1 || x === 2)" {
		t.Errorf("FormatAsGuardCode(x, oneOf(1,2)) = %q, %v", got, ok)
	}
	// no liftable guard exists: no example, never an unprovable one
	if _, ok := FormatAsGuardCode("x", MakeRefinedSet(MultipleOf(5))); ok {
		t.Errorf("FormatAsGuardCode(x, multipleOf(5)) should fail")
	}
	if _, ok := FormatAsGuardCode("x", MakeRefinedSet(Star(Numbers))); ok {
		t.Errorf("FormatAsGuardCode(x, star(numbers)) should fail")
	}
	if _, ok := FormatAsGuardCode("x", MakeRefinedSet()); ok {
		t.Errorf("FormatAsGuardCode(x, empty) should fail")
	}
	// everything folded away: nothing to prove, no guard
	if _, ok := FormatAsGuardCode("x", MakeRefinedSet(AtLeast(math.Inf(-1)))); ok {
		t.Errorf("FormatAsGuardCode(x, atLeast(-Inf)) should fail")
	}
}
