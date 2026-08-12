// The one word `typeof` answers for a walked value — and the scalar
// sets it must refuse: {0,1} states a boolean claim at one position
// and a number claim at another, so no word is honest there.

package abstractdomain

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

func TestExactWordsAnswerTheirSortsWord(t *testing.T) {
	if got := TypeofWordOfKnown(KnownValues([]float64{104, 105}, PrimitiveString, TrustProved)); got != "string" {
		t.Errorf("got %q, want %q", got, "string")
	}
	if got := TypeofWordOfKnown(KnownValues([]float64{3}, PrimitiveNumber, TrustProved)); got != "number" {
		t.Errorf("got %q, want %q", got, "number")
	}
	if got := TypeofWordOfKnown(KnownValues([]float64{1}, PrimitiveBoolean, TrustProved)); got != "boolean" {
		t.Errorf("got %q, want %q", got, "boolean")
	}
	if got := TypeofWordOfKnown(NaNValue); got != "number" {
		t.Errorf("got %q, want %q", got, "number")
	}
	if got := TypeofWordOfKnown(HostFunction); got != "function" {
		t.Errorf("got %q, want %q", got, "function")
	}
}

func TestTheAllStringsSetAnswersString(t *testing.T) {
	got := TypeofWordOfKnown(KnownSet(refinementsets.Strings, nil, TrustSpec, SetKindTagNone))
	if got != "string" {
		t.Errorf("got %q, want %q", got, "string")
	}
}

func TestAScalarSetAnswersNothingTheBooleanClaimRidesInIt(t *testing.T) {
	// `boolean` seeds as this very set; calling it "number" would fold
	// `typeof b === "number"` true on a boolean
	set := refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{0, 1}))
	got := TypeofWordOfKnown(KnownSet(set, nil, TrustProved, SetKindTagNone))
	if got != "" {
		t.Errorf("got %q, want \"\"", got)
	}
}

func TestNaNRidingBesideANumberClaimKeepsTheWord(t *testing.T) {
	got := TypeofWordOfKnown(PossiblyNaN(KnownValues([]float64{3}, PrimitiveNumber, TrustProved)))
	if got != "number" {
		t.Errorf("got %q, want %q", got, "number")
	}
}
