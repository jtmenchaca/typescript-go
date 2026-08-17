package abstractdomain

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// SetOfKnown folds a union of SAME-SORT value tuples into the union of
// the arms' sets — the c-1291 shape: `("axis" | "item")[]` needs its
// element to denote ONE set for StarOfElement to star.
func TestSetOfKnown_ASameSortWordUnionDenotesTheUnionOfItsArms(t *testing.T) {
	axis := KnownValues(refinementsets.CodepointsOf("axis"), PrimitiveString, TrustProved)
	item := KnownValues(refinementsets.CodepointsOf("item"), PrimitiveString, TrustProved)
	union := KindUnionOf([]AbstractValue{axis, item})
	if union.Kind != KindKindUnion {
		t.Fatalf("KindUnionOf = %+v, want a sort union", union)
	}
	set, ok := SetOfKnown(union)
	if !ok {
		t.Fatalf("a two-word union denoted no set")
	}
	if len(set.Forms) != 1 || set.Forms[0].Form != refinementsets.FormUnion {
		t.Errorf("Forms = %+v, want one union form", set.Forms)
	}
}

func TestSetOfKnown_ASameSortNumberUnionDenotesTheUnionOfItsArms(t *testing.T) {
	one := KnownValues([]float64{1}, PrimitiveNumber, TrustProved)
	two := KnownValues([]float64{2}, PrimitiveNumber, TrustProved)
	if _, ok := SetOfKnown(KindUnionOf([]AbstractValue{one, two})); !ok {
		t.Errorf("a two-number union denoted no set")
	}
}

// a one-letter word and a number spell the same double — an untagged
// set folding both would let each arm read the other's members
func TestSetOfKnown_AMixedSortUnionStaysRefused(t *testing.T) {
	word := KnownValues(refinementsets.CodepointsOf("a"), PrimitiveString, TrustProved)
	number := KnownValues([]float64{97}, PrimitiveNumber, TrustProved)
	if _, ok := SetOfKnown(KindUnionOf([]AbstractValue{word, number})); ok {
		t.Errorf("a mixed-sort union denoted a set — the codepoint/number ambiguity is live again")
	}
}

func TestSetOfKnown_AUnionWithANonValueArmStaysRefused(t *testing.T) {
	word := KnownValues(refinementsets.CodepointsOf("axis"), PrimitiveString, TrustProved)
	ground := KnownSet(refinementsets.Strings, nil, TrustProved, SetKindTagNone)
	if _, ok := SetOfKnown(KindUnionOf([]AbstractValue{word, ground})); ok {
		t.Errorf("a union with a whole-sort arm denoted a set — only value-tuple arms fold")
	}
}
