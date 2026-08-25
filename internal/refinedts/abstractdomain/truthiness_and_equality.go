// Truthiness (the three-valued ToBoolean read) and structural sameness
// over abstract values — SameKnown compares by structure, not JSON
// (an object's stated annotation can carry compiler symbols, so it
// compares by identity).

package abstractdomain

import (
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// Truthiness is truthiness in the TS source.
//
// The three-valued result (true / false / unknown) is (bool, bool):
// (value, known).
func Truthiness(k AbstractValue) (bool, bool) {
	switch k.Kind {
	case KindValues:
		if k.KindTag == PrimitiveString {
			return len(k.Values) > 0, true
		}
		if k.KindTag == PrimitiveArray {
			return true, true // arrays are objects
		}
		if len(k.Values) == 1 {
			return k.Values[0] != 0, true
		}
		return false, false
	case KindObject, KindList, KindCollection, KindPromise, KindDate, KindRegex:
		return true, true // an object — always truthy
	case KindObjectStar, KindArrayHoles:
		// an Array is an Object, and ToBoolean maps every Object to true
		// (sec-toboolean: only undefined, null, false, ±0, NaN, "" and 0n
		// are false). An EMPTY array is still an object, so the unstated
		// (or stated-zero) length changes nothing here.
		return true, true
	case KindSymbol:
		return true, true // every symbol is truthy
	case KindHostFunction:
		return true, true // every function object is truthy
	case KindBigints:
		// 0n is the one falsy bigint
		if len(k.BigintValues) == 1 {
			return k.BigintValues[0].Sign() != 0, true
		}
		return false, false
	case KindNaN:
		return false, true
	case KindUndef:
		return false, true
	case KindNull:
		// sec-toboolean: null is one of the seven falsy values
		return false, true
	case KindKindUnion:
		// decided only when EVERY arm agrees. A kindUnion with zero arms
		// does not occur in practice (KindUnionOf collapses that case to
		// Unknown before construction); this guard keeps the function
		// total (the TS source's `verdicts[0]` on an empty array reads
		// as `undefined`, which the surrounding ternary then treats as
		// non-null and returns as-is — behavior this port does not
		// reproduce since it is unreached).
		if len(k.Arms) == 0 {
			return false, false
		}
		first, firstKnown := Truthiness(k.Arms[0])
		if !firstKnown {
			return false, false
		}
		for _, arm := range k.Arms {
			v, known := Truthiness(arm)
			if !known || v != first {
				return false, false
			}
		}
		return first, true
	case KindSet, KindVariable, KindPossiblyUndefined, KindPossiblyNaN, KindUnknown:
		return false, false
	default:
		return false, false
	}
}

// SameKnown is sameKnown in the TS source: structural sameness. (Not
// JSON: an object's stated annotation can carry compiler symbols; it
// compares by identity.)
func SameKnown(a, b AbstractValue) bool {
	if a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case KindValues:
		return a.KindTag == b.KindTag && sameFloats(a.Values, b.Values)
	case KindSet:
		// SeqDenseKnown/SeqDense is a real observable difference the same
		// way KindArrayHoles's own Dense/DenseKnown pair is (this switch's
		// KindArrayHoles case, below): two otherwise-identical repetition
		// claims where one proved every counted index present and the
		// other did not are NOT the same claim — a caller reading SameKnown
		// as license to keep EITHER side's fields (JoinKnown's own
		// AtTrustLevel(a, grade) fast path, taken exactly when SameKnown
		// holds) must not silently keep a's density mark while discarding
		// b's absence of one, or the reverse.
		return sameRefinedSet(a.Set, b.Set) &&
			sameTemporal(a.Temporal, b.Temporal) &&
			a.SetKindTag == b.SetKindTag &&
			a.SeqDenseKnown == b.SeqDenseKnown &&
			(!a.SeqDenseKnown || a.SeqDense == b.SeqDense)
	case KindObject:
		if a.Stated != b.Stated {
			return false
		}
		if a.Complete != b.Complete {
			return false
		}
		if a.BareProto != b.BareProto {
			return false
		}
		if len(a.Keys) != len(b.Keys) {
			return false
		}
		for _, key := range a.Keys {
			other, ok := lookupKey(b.Keys, key.Name)
			if !ok || !SameKnown(key.Value, other) {
				return false
			}
		}
		return true
	case KindVariable:
		// the variable, by identity — its compiler symbol
		return a.Symbol == b.Symbol && a.StarDepth == b.StarDepth
	case KindList:
		if len(a.Items) != len(b.Items) {
			return false
		}
		for i, item := range a.Items {
			if !SameKnown(item, b.Items[i]) {
				return false
			}
		}
		return true
	case KindObjectStar:
		// the element is the whole claim — neither side states a length,
		// so two stars over the same element say the same thing
		if a.Inner == nil || b.Inner == nil {
			return a.Inner == b.Inner
		}
		return SameKnown(*a.Inner, *b.Inner)
	case KindArrayHoles:
		// the length (wrapped in Inner, {n}) and the always-∅ element
		// set say the same thing for a dense and a sparse array-holes
		// alike, but Dense/DenseKnown is a real observable difference
		// (Object.keys answers [] for one and n index strings for the
		// other, and answers nothing at all where density was never
		// established) — two array-holes compare equal only when the
		// length AND the density claim (proved-or-not, and if proved,
		// which way) agree.
		if a.DenseKnown != b.DenseKnown || (a.DenseKnown && a.Dense != b.Dense) {
			return false
		}
		if a.Inner == nil || b.Inner == nil {
			return a.Inner == b.Inner
		}
		return SameKnown(*a.Inner, *b.Inner)
	case KindCollection:
		if a.CollectionFlavor != b.CollectionFlavor || a.Complete != b.Complete {
			return false
		}
		if len(a.Entries) != len(b.Entries) {
			return false
		}
		for i, e := range a.Entries {
			if !SameKnown(e.Key, b.Entries[i].Key) || !SameKnown(e.Value, b.Entries[i].Value) {
				return false
			}
		}
		return true
	case KindUndef:
		return true
	case KindNull:
		return true
	case KindNaN:
		return true
	case KindPossiblyUndefined:
		return a.AbsentSide == b.AbsentSide && SameKnown(*a.Inner, *b.Inner)
	case KindPromise:
		return SameKnown(*a.Inner, *b.Inner)
	case KindPossiblyNaN:
		return SameKnown(*a.Inner, *b.Inner)
	case KindDate:
		return SameKnown(*a.Millis, *b.Millis)
	case KindSymbol:
		return a.HasSymbolKey == b.HasSymbolKey && a.SymbolKey == b.SymbolKey &&
			a.HasDescription == b.HasDescription && a.Description == b.Description
	case KindHostFunction:
		return true // the sort is the whole claim
	case KindBigints:
		if len(a.BigintValues) != len(b.BigintValues) {
			return false
		}
		for i, v := range a.BigintValues {
			if v.Cmp(b.BigintValues[i]) != 0 {
				return false
			}
		}
		return true
	case KindRegex:
		return a.Source == b.Source && a.Flags == b.Flags
	case KindKindUnion:
		if len(a.Arms) != len(b.Arms) {
			return false
		}
		for i, arm := range a.Arms {
			if !SameKnown(arm, b.Arms[i]) {
				return false
			}
		}
		return true
	case KindUnknown:
		// the opaque marker is provenance, not the same fact — joining
		// an opaque path with a plain-unknown one stays plain.
		// ResidueReason is deliberately NOT compared here: it is a
		// diagnostic-only sentence (abstract_value.go's doc), and two
		// unknowns naming different readers are still the same lattice
		// value.
		return a.Opaque == b.Opaque
	default:
		return false
	}
}

func lookupKey(keys []ObjectKey, name string) (AbstractValue, bool) {
	for _, key := range keys {
		if key.Name == name {
			return key.Value, true
		}
	}
	return AbstractValue{}, false
}

func sameFloats(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i, v := range a {
		if v != b[i] {
			return false
		}
	}
	return true
}

// sameTemporal compares the TS source's `JSON.stringify(a.temporal ??
// null) === JSON.stringify(b.temporal ?? null)` — a deep-equality
// fallback the TS source reaches for because TemporalAnnotation has no
// hand-written equality. refinementsets.TemporalAnnotation is a plain
// value struct (refinement_sets/calendar_interpreter.go), so a Go value
// comparison over the dereferenced structs is the same deep-equality
// check without a JSON round trip.
func sameTemporal(a, b *refinementsets.TemporalAnnotation) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// sameRefinedSet compares the TS source's `JSON.stringify(a.set) ===
// JSON.stringify(b.set)` structurally, since refinementsets.RefinedSet
// holds pointer fields (A_, B) that a Go `==` cannot compare.
func sameRefinedSet(a, b refinementsets.RefinedSet) bool {
	if len(a.Forms) != len(b.Forms) {
		return false
	}
	for i, f := range a.Forms {
		if !sameRefinement(f, b.Forms[i]) {
			return false
		}
	}
	return true
}

func sameRefinement(a, b refinementsets.Refinement) bool {
	if a.Form != b.Form {
		return false
	}
	if a.A != b.A {
		return false
	}
	if !sameFloats(a.W, b.W) {
		return false
	}
	if (a.A_ == nil) != (b.A_ == nil) {
		return false
	}
	if a.A_ != nil && !sameRefinedSet(*a.A_, *b.A_) {
		return false
	}
	if (a.B == nil) != (b.B == nil) {
		return false
	}
	if a.B != nil && !sameRefinedSet(*a.B, *b.B) {
		return false
	}
	if a.Lo != b.Lo {
		return false
	}
	if (a.Hi == nil) != (b.Hi == nil) {
		return false
	}
	if a.Hi != nil && *a.Hi != *b.Hi {
		return false
	}
	return true
}
