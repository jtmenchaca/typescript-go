package refinementsets

import (
	"math"
	"testing"
)

// The observed hang shape (createCategoricalInverse.ts's bisect
// candidate): an intersection of two unions whose arms repeat and
// carry vacuous +inf bounds. Canonicalized, the duplicate conjuncts
// and arms collapse and the vacuous bounds drop — the spelling the
// run collapse can read.
func TestCanonicalScalarForms_TheHangShapeCollapses(t *testing.T) {
	intOrInf := Union(
		MakeRefinedSet(Integer),
		MakeRefinedSet(OneOf([]float64{math.Inf(-1), math.Inf(1)})),
	)
	armA := MakeRefinedSet(AtMost(math.Inf(1)), AtLeast(0))
	armB := MakeRefinedSet(AtMost(math.Inf(1)), AtLeast(0), intOrInf)
	met := RefinedSet{Forms: []Refinement{
		Union(armA, armB),
		Union(armA, armB),
	}}
	canon := CanonicalScalarForms(met)
	if len(canon.Forms) != 1 {
		t.Fatalf("Forms = %+v, want the duplicated union conjunct said once", canon.Forms)
	}
	if canon.Forms[0].Form != FormUnion {
		t.Fatalf("Forms[0] = %+v, want the union", canon.Forms[0])
	}
	leftArm := *canon.Forms[0].A_
	rightArm := *canon.Forms[0].B
	for _, arm := range []RefinedSet{leftArm, rightArm} {
		for _, form := range arm.Forms {
			if form.Form == FormAtMost && math.IsInf(form.A, 1) {
				t.Errorf("arm %+v kept a vacuous atMost +inf", arm)
			}
		}
	}
}

// a union arm said twice stays once, and a one-arm union IS its arm
func TestCanonicalScalarForms_ADuplicatedArmCollapsesToTheArm(t *testing.T) {
	arm := MakeRefinedSet(AtLeast(0), Integer)
	twice := MakeRefinedSet(Union(arm, arm))
	canon := CanonicalScalarForms(twice)
	if !sameSetJSON(canon, arm) {
		t.Errorf("CanonicalScalarForms(X ∪ X) = %+v, want X = %+v", canon, arm)
	}
}

// an already-plain set is untouched
func TestCanonicalScalarForms_APlainSetIsUntouched(t *testing.T) {
	plain := MakeRefinedSet(AtLeast(0), AtMost(10), Integer)
	if !sameSetJSON(CanonicalScalarForms(plain), plain) {
		t.Errorf("a plain set changed spelling")
	}
}

// a set of ONLY vacuous bounds keeps one form — the kernel's questions
// need at least one refinement
func TestCanonicalScalarForms_AnAllVacuousSetKeepsOneForm(t *testing.T) {
	vacuous := MakeRefinedSet(AtLeast(math.Inf(-1)), AtMost(math.Inf(1)))
	canon := CanonicalScalarForms(vacuous)
	if len(canon.Forms) != 1 {
		t.Errorf("Forms = %+v, want exactly one surviving spelling of ℝ̄", canon.Forms)
	}
}