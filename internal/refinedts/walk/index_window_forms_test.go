// from evaluation/index_windows.test.ts

package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

func TestIndexWindowSpansExactNonnegativeIntegers(t *testing.T) {
	got := IndexWindow(abstractdomain.KnownValues([]float64{0, 2, 5}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved))
	if got == nil || got.Lo != 0 || got.Hi != 5 {
		t.Fatalf("indexWindow([0,2,5]) = %+v, want {0 5}", got)
	}
	got = IndexWindow(abstractdomain.KnownValues([]float64{3}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved))
	if got == nil || got.Lo != 3 || got.Hi != 3 {
		t.Fatalf("indexWindow([3]) = %+v, want {3 3}", got)
	}
}

func TestIndexWindowNilForNegativeNonIntegerOrEmpty(t *testing.T) {
	if IndexWindow(abstractdomain.KnownValues([]float64{-1}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)) != nil {
		t.Fatal("indexWindow([-1]) should be nil")
	}
	if IndexWindow(abstractdomain.KnownValues([]float64{1.5}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)) != nil {
		t.Fatal("indexWindow([1.5]) should be nil")
	}
	if IndexWindow(abstractdomain.KnownValues([]float64{}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)) != nil {
		t.Fatal("indexWindow([]) should be nil")
	}
	if IndexWindow(abstractdomain.KnownValues([]float64{1, 2}, abstractdomain.PrimitiveString, abstractdomain.TrustProved)) != nil {
		t.Fatal("indexWindow(string values) should be nil")
	}
	if IndexWindow(silence.Residue()) != nil {
		t.Fatal("indexWindow(residue()) should be nil")
	}
}

func TestElementJoinOfJoinsASequencesElements(t *testing.T) {
	got := ElementJoinOf(abstractdomain.KnownValues([]float64{1, 2}, abstractdomain.PrimitiveArray, abstractdomain.TrustProved))
	want := abstractdomain.KnownSet(refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{1, 2})), nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone)
	if got == nil || !abstractValueEqual(*got, want) {
		t.Fatalf("elementJoinOf(array values) = %+v, want %+v", got, want)
	}

	list := abstractdomain.KnownList([]abstractdomain.AbstractValue{
		abstractdomain.KnownValues([]float64{1}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved),
		abstractdomain.KnownValues([]float64{1}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved),
	}, abstractdomain.TrustProved)
	got = ElementJoinOf(list)
	want = abstractdomain.KnownValues([]float64{1}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
	if got == nil || !abstractValueEqual(*got, want) {
		t.Fatalf("elementJoinOf(list) = %+v, want %+v", got, want)
	}

	if ElementJoinOf(silence.Residue()) != nil {
		t.Fatal("elementJoinOf(residue()) should be nil")
	}
	if ElementJoinOf(abstractdomain.KnownValues([]float64{}, abstractdomain.PrimitiveArray, abstractdomain.TrustProved)) != nil {
		t.Fatal("elementJoinOf(empty array values) should be nil")
	}
}

// abstractValueEqual is a small structural comparer for the fields
// these cases populate — the TS test's toEqual over a plain object.
func abstractValueEqual(a, b abstractdomain.AbstractValue) bool {
	if a.Kind != b.Kind || a.KindTag != b.KindTag || a.Grade != b.Grade {
		return false
	}
	if len(a.Values) != len(b.Values) {
		return false
	}
	for i := range a.Values {
		if a.Values[i] != b.Values[i] {
			return false
		}
	}
	if a.Kind == abstractdomain.KindSet {
		return refinedSetEqual(a.Set, b.Set)
	}
	return true
}

func refinedSetEqual(a, b refinementsets.RefinedSet) bool {
	if len(a.Forms) != len(b.Forms) {
		return false
	}
	for i := range a.Forms {
		if a.Forms[i].Form != b.Forms[i].Form {
			return false
		}
		if len(a.Forms[i].W) != len(b.Forms[i].W) {
			return false
		}
		for j := range a.Forms[i].W {
			if a.Forms[i].W[j] != b.Forms[i].W[j] {
				return false
			}
		}
	}
	return true
}
