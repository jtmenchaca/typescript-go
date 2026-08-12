package refinementsets

import (
	"math"
	"testing"
)

func hoverOf(t *testing.T, r RefinedSet) string {
	t.Helper()
	s, _ := FormatForHover(r)
	return s
}

func TestTheHoverVocabularyTheSurfacesOwnWordsChainedBounds(t *testing.T) {
	// integer frames the number before its bounds; a two-sided window
	// chains as an interval over the mathematical italic x
	if got := hoverOf(t, MakeRefinedSet(AtLeast(1), AtMost(65535), Integer)); got != "{integer, 1 ≤ 𝑥 ≤ 65535}" {
		t.Errorf("hover = %q", got)
	}
	if got := hoverOf(t, MakeRefinedSet(AtLeast(0), AtMost(100))); got != "{0 ≤ 𝑥 ≤ 100}" {
		t.Errorf("hover = %q", got)
	}
	// the words are the ones the surface makes you write: z.int() reads
	// back "integer", .multipleOf(5) reads back "multipleOf 5"
	if got := hoverOf(t, MakeRefinedSet(AtLeast(0), AtMost(100), MultipleOf(5))); got != "{multipleOf 5, 0 ≤ 𝑥 ≤ 100}" {
		t.Errorf("hover = %q", got)
	}
	// a lone bound puts the placeholder first, so it reads in the same
	// direction as the chain; strict sides read <
	if got := hoverOf(t, MakeRefinedSet(AtLeast(0))); got != "{𝑥 ≥ 0}" {
		t.Errorf("hover = %q", got)
	}
	if got := hoverOf(t, MakeRefinedSet(AtLeast(0), Below(1))); got != "{0 ≤ 𝑥 < 1}" {
		t.Errorf("hover = %q", got)
	}
	// enumerations read as a type-flavored union
	if got := hoverOf(t, MakeRefinedSet(OneOf([]float64{301, 302, 307}))); got != "{301 | 302 | 307}" {
		t.Errorf("hover = %q", got)
	}
	// ONE value is something the type language can say, so it says it
	// rather than annotating the type with it
	if got := hoverOf(t, MakeRefinedSet(OneOf([]float64{1}))); got != "1" {
		t.Errorf("hover = %q", got)
	}
	// a sequence: what each element is
	if got := hoverOf(t, MakeRefinedSet(Star(MakeRefinedSet(AtLeast(1), AtMost(5), Integer)))); got != "{each integer, 1 ≤ 𝑥 ≤ 5}" {
		t.Errorf("hover = %q", got)
	}
	// length is hoverLength, of a string and of an array alike
	if got := hoverOf(t, Repetition(Codepoints, 2, intPtr(2))); got != "{𝑙𝑒𝑛 = 2}" {
		t.Errorf("hover = %q", got)
	}
	if got := hoverOf(t, Repetition(Codepoints, 1, nil)); got != "{𝑙𝑒𝑛 ≥ 1}" {
		t.Errorf("hover = %q", got)
	}
	if got := hoverOf(t, Repetition(Codepoints, 3, intPtr(5))); got != "{3 ≤ 𝑙𝑒𝑛 ≤ 5}" {
		t.Errorf("hover = %q", got)
	}
	if _, ok := FormatForHover(MakeRefinedSet(Star(Codepoints))); ok {
		t.Errorf("hover(star codepoints) should be nil")
	}
	if got := hoverOf(t, Codepoints); got != "{𝑙𝑒𝑛 = 1}" {
		t.Errorf("hover(Codepoints) = %q", got)
	}
	// the pattern methods keep the names that built them
	if got := hoverOf(t, MakeRefinedSet(Concatenation(StringTuple("AB"), Strings))); got != `{startsWith "AB"}` {
		t.Errorf("hover = %q", got)
	}
	if got := hoverOf(t, MakeRefinedSet(Concatenation(Strings, MakeRefinedSet(Concatenation(StringTuple(","), Strings))))); got != `{includes ","}` {
		t.Errorf("hover = %q", got)
	}
	// a length bound stacked with a pattern: both facts, no repeated
	// "string" that the type line already said
	lenRep := Repetition(Codepoints, 1, nil)
	stacked := MakeRefinedSet(
		lenRep.Forms[0],
		Concatenation(Strings, MakeRefinedSet(Concatenation(StringTuple(","), Strings))),
	)
	if got := hoverOf(t, stacked); got != `{𝑙𝑒𝑛 ≥ 1, includes ","}` {
		t.Errorf("hover = %q", got)
	}
	// nothing beyond the host type: no braces at all
	if _, ok := FormatForHover(MakeRefinedSet()); ok {
		t.Errorf("hover(empty) should be nil")
	}
	if _, ok := FormatForHover(MakeRefinedSet(AtLeast(math.Inf(-1)))); ok {
		t.Errorf("hover(atLeast(-Inf)) should be nil")
	}
}

func TestTheHoverNeverFallsBackIntoTheSetAlgebra(t *testing.T) {
	// a union is the union BAR, each side in the hover's own words
	union := MakeRefinedSet(Union(
		MakeRefinedSet(AtLeast(0), AtMost(100)),
		MakeRefinedSet(AtLeast(200), AtMost(300)),
	))
	if got := hoverOf(t, union); got != "{0 ≤ 𝑥 ≤ 100 | 200 ≤ 𝑥 ≤ 300}" {
		t.Errorf("hover = %q", got)
	}
	// a difference is what it EXCLUDES, last
	without := MakeRefinedSet(Difference(MakeRefinedSet(AtLeast(0), AtMost(100)), MakeRefinedSet(OneOf([]float64{50}))))
	if got := hoverOf(t, without); got != "{0 ≤ 𝑥 ≤ 100, ≠ 50}" {
		t.Errorf("hover = %q", got)
	}
	// neither ∪ nor ∖ nor && reaches a hover
	for _, r := range []RefinedSet{union, without} {
		shown := hoverOf(t, r)
		if containsSubstr(shown, "∪") || containsSubstr(shown, "∖") || containsSubstr(shown, "&&") {
			t.Errorf("hover %q leaked the algebra register", shown)
		}
	}
}

func TestAnOrSideOfSeveralFactsIsBracketed(t *testing.T) {
	// the commas mean "and" and the bar means "or", so a side carrying
	// more than one fact says where it ends.
	mixed := MakeRefinedSet(Below(20), Union(
		MakeRefinedSet(OneOf([]float64{0})),
		MakeRefinedSet(AtLeast(2), AtMost(21), Integer),
	))
	if got := hoverOf(t, mixed); got != "{𝑥 < 20, 0 | (integer, 2 ≤ 𝑥 ≤ 21)}" {
		t.Errorf("hover = %q", got)
	}
	// a single-fact side needs none
	simple := MakeRefinedSet(Union(MakeRefinedSet(OneOf([]float64{0})), MakeRefinedSet(OneOf([]float64{100}))))
	if got := hoverOf(t, simple); got != "{0 | 100}" {
		t.Errorf("hover = %q", got)
	}
}

func containsSubstr(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
