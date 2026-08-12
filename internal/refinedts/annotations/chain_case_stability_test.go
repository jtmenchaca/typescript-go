// Interface test for CaseStableForms: a minimum-length repeat
// survives a case mapping (no code point maps to nothing), a
// maximum-length one does not (full mappings can expand), and the
// codepoint-alphabet star survives always.

package annotations

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

func TestCaseStableForms_StarOfTheFullAlphabetSurvives(t *testing.T) {
	forms := []refinementsets.Refinement{refinementsets.Star(refinementsets.Codepoints)}
	kept := CaseStableForms(forms)
	if len(kept) != 1 || kept[0].Form != refinementsets.FormStar {
		t.Errorf("CaseStableForms(star of codepoints) = %+v, want the star kept", kept)
	}
}

func TestCaseStableForms_ARepeatMinimumSurvivesAMaximumFalls(t *testing.T) {
	hi := 5
	forms := []refinementsets.Refinement{
		refinementsets.RepeatOf(refinementsets.Codepoints, 2, &hi),
	}
	kept := CaseStableForms(forms)
	if len(kept) != 1 {
		t.Fatalf("CaseStableForms(repeat[2,5]) = %+v, want one kept form", kept)
	}
	if kept[0].Form != refinementsets.FormRepeat {
		t.Fatalf("kept[0].Form = %v, want repeat", kept[0].Form)
	}
	if kept[0].Lo != 2 {
		t.Errorf("kept[0].Lo = %d, want 2 (the minimum survives)", kept[0].Lo)
	}
	if kept[0].Hi != nil {
		t.Errorf("kept[0].Hi = %v, want nil (the maximum falls — full mappings can expand)", kept[0].Hi)
	}
}

func TestCaseStableForms_AnUndecidableFormIsDropped(t *testing.T) {
	forms := []refinementsets.Refinement{refinementsets.Integer}
	kept := CaseStableForms(forms)
	if len(kept) != 0 {
		t.Errorf("CaseStableForms(integer) = %+v, want dropped (undecidable under a case mapping)", kept)
	}
}
