// The typeof-level sort a walked value speaks for — the one word
// `typeof` answers, whether more than one word is admitted, and the
// sort-discrimination bucket a kind-union arm claims.

package abstractdomain

import (
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// ClaimSort is the "string" | "number" | "boolean" | "object" | "symbol"
// return of KindOfClaim, plus "" for the TS source's null (no sort).
type ClaimSort string

const (
	ClaimSortNone    ClaimSort = ""
	ClaimSortString  ClaimSort = "string"
	ClaimSortNumber  ClaimSort = "number"
	ClaimSortBoolean ClaimSort = "boolean"
	ClaimSortObject  ClaimSort = "object"
	ClaimSortSymbol  ClaimSort = "symbol"
)

// KindOfClaim is kindOfClaim in the TS source: the typeof-level sort an
// arm's claim speaks for, best effort: a sortless SET is a string when
// its forms are sequence-shaped and numeric otherwise — which conflates
// number with boolean (their words share the ground), so discrimination
// buckets those together. ClaimSortNone (the TS source's null) where the
// claim names no sort cleanly.
func KindOfClaim(k AbstractValue) ClaimSort {
	switch k.Kind {
	case KindValues:
		if k.KindTag == PrimitiveArray {
			return ClaimSortObject
		}
		return ClaimSort(k.KindTag)
	case KindNaN:
		return ClaimSortNumber
	case KindSymbol:
		return ClaimSortSymbol
	case KindPossiblyNaN, KindPossiblyUndefined:
		return KindOfClaim(*k.Inner)
	case KindSet:
		if k.SetKindTag == SetKindTagSymbol {
			return ClaimSortSymbol
		}
		if k.SetKindTag != SetKindTagNone {
			return ClaimSortNone
		}
		return setSortOfForms(k.Set.Forms)
	case KindObject, KindList, KindCollection, KindPromise, KindDate, KindRegex:
		return ClaimSortObject
	default:
		return ClaimSortNone
	}
}

// setSortOfForms is setSortOfForms in the TS source: the sort a set's
// FORMS speak, looked at through unions and differences: sequence forms
// say "string" (strings and arrays share the tuple layer), scalar leaves
// say "number", a union whose arms disagree says nothing. A union of
// string tuples (an enum of words) reads "string" — the direct-forms
// reading called it a number, and a typeof guard then failed to shed it.
func setSortOfForms(forms []refinementsets.Refinement) ClaimSort {
	seen := ClaimSortNone
	meet := func(sort ClaimSort) bool {
		if sort == ClaimSortNone {
			return false
		}
		if seen == ClaimSortNone {
			seen = sort
			return true
		}
		return seen == sort
	}
	for _, f := range forms {
		switch f.Form {
		case refinementsets.FormConcatenation, refinementsets.FormStar,
			refinementsets.FormRepeat, refinementsets.FormRepeatWord,
			refinementsets.FormEmptyTuple:
			if !meet(ClaimSortString) {
				return ClaimSortNone
			}
		case refinementsets.FormUnion:
			a := setSortOfForms(f.A_.Forms)
			b := setSortOfForms(f.B.Forms)
			if a != b || !meet(a) {
				return ClaimSortNone
			}
		case refinementsets.FormDifference:
			if !meet(setSortOfForms(f.A_.Forms)) {
				return ClaimSortNone
			}
		default:
			if !meet(ClaimSortNumber) {
				return ClaimSortNone
			}
		}
	}
	if seen == ClaimSortNone {
		return ClaimSortNumber
	}
	return seen
}

// TypeofWordOfKnown is typeofWordOfKnown in the TS source: the one word
// `typeof` answers for a walked value, or "" (the TS source's null) when
// the claim does not pin it. Scalar sets answer nothing: the same {0,1}
// set states a boolean claim at one position and a number claim at
// another, and the two words differ. String-shaped sets are unambiguous
// — only strings carry sequence forms.
func TypeofWordOfKnown(k AbstractValue) string {
	switch k.Kind {
	case KindValues:
		if k.KindTag == PrimitiveArray {
			return "object"
		}
		return string(k.KindTag)
	case KindNaN:
		return "number"
	case KindBigints:
		return "bigint"
	case KindSymbol:
		return "symbol"
	case KindHostFunction:
		return "function"
	case KindObject, KindList, KindCollection, KindPromise, KindDate, KindRegex:
		return "object"
	case KindPossiblyNaN:
		// NaN is a number, so the ride changes nothing when the inner
		// claim already answers "number"
		inner := TypeofWordOfKnown(*k.Inner)
		if inner == "number" {
			return "number"
		}
		return ""
	case KindKindUnion:
		// a kindUnion with zero arms does not occur in practice
		// (KindUnionOf collapses that case to Unknown before
		// construction); this guard keeps the function total.
		if len(k.Arms) == 0 {
			return ""
		}
		first := TypeofWordOfKnown(k.Arms[0])
		for _, arm := range k.Arms {
			if TypeofWordOfKnown(arm) != first {
				return ""
			}
		}
		return first
	case KindSet:
		if k.SetKindTag == SetKindTagBigint {
			return "bigint"
		}
		if k.SetKindTag == SetKindTagSymbol {
			return "symbol"
		}
		for _, f := range k.Set.Forms {
			switch f.Form {
			case refinementsets.FormConcatenation, refinementsets.FormStar,
				refinementsets.FormRepeat, refinementsets.FormRepeatWord,
				refinementsets.FormEmptyTuple:
				return "string"
			}
		}
		return ""
	default:
		return ""
	}
}

// TypeofPlural is typeofPlural in the TS source: whether the walked
// value ADMITS more than one typeof word — a kind union across sorts, or
// possible absence beside a present claim. A plural value contradicts
// any single static word (tsc's enum reverse-read types this hole), so
// the typeof transfer must answer no word at all rather than trust the
// shape layer.
func TypeofPlural(k AbstractValue) bool {
	switch k.Kind {
	case KindPossiblyUndefined:
		return true
	case KindUndef:
		// the absent marker conflates undefined ("undefined") and null
		// ("object") — two words, so no static claim may stand over it
		// (tsc typed prisma's maybe-unwritten `let wire: string` as
		// string and a live guard folded dead)
		return true
	case KindKindUnion:
		// mirrors the TS source's `words[0]` on a possibly-empty array
		// (undefined, so `first !== null` holds) and `.every` vacuously
		// true on empty: an empty arms list reads as NOT plural. A
		// kindUnion with zero arms does not occur in practice —
		// KindUnionOf collapses that case to Unknown before construction
		// — so this arm is unreached in the same way the TS source's is.
		if len(k.Arms) == 0 {
			return false
		}
		first := TypeofWordOfKnown(k.Arms[0])
		if first == "" {
			return true
		}
		for _, arm := range k.Arms {
			if TypeofWordOfKnown(arm) != first {
				return true
			}
		}
		return false
	case KindPossiblyNaN:
		return TypeofPlural(*k.Inner)
	default:
		return false
	}
}
