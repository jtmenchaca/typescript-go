// Which refinement forms survive a case mapping. Each form is kept
// or dropped by its own rule: a case-invariant alphabet stays, a
// finite ceiling falls (full mappings expand), anything undecidable
// falls with it — sound weakening, never a wrong claim.
//
// Ported 1:1 from annotations/chain_case_stability.ts.

package annotations

import (
	"math"
	"reflect"
	"unicode"

	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// caseInvariant is caseInvariant in the TS source: a code point
// untouched by both case mappings. The TS source maps the whole
// STRING (String.fromCodePoint(cp).toUpperCase()/.toLowerCase()) --
// Go's unicode.ToUpper/ToLower operate per-rune, the direct
// substitute for a single code point.
func caseInvariant(cp float64) bool {
	r := rune(cp)
	return unicode.ToUpper(r) == r && unicode.ToLower(r) == r
}

// caseClosedAlphabet is caseClosedAlphabet in the TS source: an
// alphabet a case mapping cannot leave -- the universal word, the
// full CODE-POINT alphabet (a mapped character is still a sequence of
// code points -- star and unbounded repeat absorb the expansion), or
// a set that is itself case-stable.
func caseClosedAlphabet(a refinementsets.RefinedSet) bool {
	if len(a.Forms) == 0 {
		return true
	}
	if reflect.DeepEqual(a, refinementsets.Codepoints) {
		return true
	}
	return caseStableSet(a)
}

func caseStableSet(r refinementsets.RefinedSet) bool {
	for _, f := range r.Forms {
		if !caseStableForm(f) {
			return false
		}
	}
	return true
}

func caseStableForm(f refinementsets.Refinement) bool {
	switch f.Form {
	case refinementsets.FormStar:
		return caseClosedAlphabet(*f.A_)
	case refinementsets.FormRepeat:
		// only an UNBOUNDED window survives inside a larger shape --
		// expansion (ß → SS) can grow past any finite ceiling
		return f.Hi == nil && caseClosedAlphabet(*f.A_)
	case refinementsets.FormOneOf:
		for _, v := range f.W {
			if v != math.Trunc(v) || !caseInvariant(v) {
				return false
			}
		}
		return true
	case refinementsets.FormConcatenation, refinementsets.FormUnion:
		return caseStableSet(*f.A_) && caseStableSet(*f.B)
	case refinementsets.FormEmptyTuple:
		return true
	default:
		return false
	}
}

// CaseStableForms is caseStableForms in the TS source: the forms that
// survive a case mapping, each transformed by its own rule — the
// stability table of the string-transform design.
func CaseStableForms(forms []refinementsets.Refinement) []refinementsets.Refinement {
	var kept []refinementsets.Refinement
	for _, f := range forms {
		switch f.Form {
		case refinementsets.FormStar:
			if caseClosedAlphabet(*f.A_) {
				kept = append(kept, f)
			}
		case refinementsets.FormRepeat:
			// a minimum survives (no code point maps to nothing); a
			// maximum falls (full mappings expand)
			if caseClosedAlphabet(*f.A_) {
				kept = append(kept, refinementsets.RepeatOf(*f.A_, f.Lo, nil))
			}
		case refinementsets.FormConcatenation, refinementsets.FormUnion:
			if caseStableSet(refinementsets.MakeRefinedSet(f)) {
				kept = append(kept, f)
			}
		case refinementsets.FormEmptyTuple:
			kept = append(kept, f)
		default:
			// dropped
		}
	}
	return kept
}
