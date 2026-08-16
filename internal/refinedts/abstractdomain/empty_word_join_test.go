package abstractdomain

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// TestStringWordSetAdmitsTheEmptyWord is the direct check on
// stringWordSet's gate (lattice_operations.go): the empty string is the
// concatenation identity — the empty tuple — and has no scalar-reread
// ambiguity (there is no value in it to misread as a 1-tuple), so it
// belongs in the same admitted union path as a two-or-more-codepoint
// word. A ONE-codepoint word keeps declining: that tuple IS the one a
// scalar position could reread.
func TestStringWordSetAdmitsTheEmptyWord(t *testing.T) {
	empty := KnownValues(nil, PrimitiveString, TrustProved)
	set, ok := stringWordSet(empty)
	if !ok {
		t.Fatalf("stringWordSet(\"\") = (_, false), want true")
	}
	word, wordOK := refinementsets.WordOf(set)
	if !wordOK || len(word) != 0 {
		t.Errorf("stringWordSet(\"\")'s set = %v, %v, want the empty word", word, wordOK)
	}
}

// TestStringWordSetStillRefusesTheOneCodepointWord pins the length-one
// refusal the fix must not touch: a single-codepoint string's tuple is
// exactly the shape a scalar position could reread as a bare number, so
// stringWordSet keeps declining it.
func TestStringWordSetStillRefusesTheOneCodepointWord(t *testing.T) {
	one := KnownValues([]float64{97}, PrimitiveString, TrustProved) // "a"
	if _, ok := stringWordSet(one); ok {
		t.Errorf("stringWordSet(\"a\") = (_, true), want false (the 1-tuple reread risk)")
	}
}

// TestStringWordSetAdmitsTwoOrMoreCodepoints pins the existing positive
// case the fix must leave alone.
func TestStringWordSetAdmitsTwoOrMoreCodepoints(t *testing.T) {
	ab := KnownValues([]float64{97, 98}, PrimitiveString, TrustProved) // "ab"
	set, ok := stringWordSet(ab)
	if !ok {
		t.Fatalf("stringWordSet(\"ab\") = (_, false), want true")
	}
	word, wordOK := refinementsets.WordOf(set)
	if !wordOK || len(word) != 2 || word[0] != 97 || word[1] != 98 {
		t.Errorf("stringWordSet(\"ab\")'s set = %v, %v, want [97 98]", word, wordOK)
	}
}

// TestJoinKnownOfACaptureLanguageAndTheEmptyStringDetermines is the
// consequence this fix exists for: `exec(...)?.[1] ?? ""` joins a
// string-sorted, untagged set (statesOnlyLongSequences: every member a
// concatenation of two-or-more codepoints — the shape a capture group's
// language takes) against the empty-string literal. Before the fix, the
// empty side fell out of stringWordSet, the join fell to the numeric
// fallback (IsNumericKind refuses a string-sorted set), and JoinKnown
// answered Unknown. After the fix, both sides pass stringWordSet and
// the join is the union of the two word sets — a DETERMINED set, not
// Unknown.
func TestJoinKnownOfACaptureLanguageAndTheEmptyStringDetermines(t *testing.T) {
	captureLanguage := KnownSet(
		refinementsets.MakeRefinedSet(refinementsets.Union(
			refinementsets.StringTuple("ab"),
			refinementsets.StringTuple("cd"),
		)),
		nil, TrustProved, SetKindTagNone,
	)
	emptyString := KnownValues(nil, PrimitiveString, TrustProved)
	joined := JoinKnown(captureLanguage, emptyString)
	if joined.Kind == KindUnknown {
		t.Fatalf("JoinKnown(captureLanguage, \"\") = Unknown, want a determined set")
	}
	if joined.Kind != KindSet {
		t.Errorf("JoinKnown(captureLanguage, \"\").Kind = %v, want KindSet", joined.Kind)
	}
}

// TestJoinKnownOfTwoStringLiteralsStillJoinsAsBefore pins the existing
// two-literal join path (both sides length two or more) so the fix's
// widened admission does not disturb it.
func TestJoinKnownOfTwoStringLiteralsStillJoinsAsBefore(t *testing.T) {
	ab := KnownValues([]float64{97, 98}, PrimitiveString, TrustProved)   // "ab"
	cd := KnownValues([]float64{99, 100}, PrimitiveString, TrustProved) // "cd"
	joined := JoinKnown(ab, cd)
	if joined.Kind != KindSet {
		t.Fatalf("JoinKnown(\"ab\", \"cd\").Kind = %v, want KindSet", joined.Kind)
	}
}

// TestJoinKnownOfOneCodepointStringsStillFallsToTheNumericPath pins the
// other existing boundary: two ONE-codepoint strings still fail
// stringWordSet on both sides (unchanged by this fix), so the join
// falls through to the numeric fallback, which then refuses on
// IsNumericKind (a string-sorted side is never numeric) and answers
// Unknown.
func TestJoinKnownOfOneCodepointStringsStillFallsToTheNumericPath(t *testing.T) {
	a := KnownValues([]float64{97}, PrimitiveString, TrustProved) // "a"
	b := KnownValues([]float64{98}, PrimitiveString, TrustProved) // "b"
	joined := JoinKnown(a, b)
	if joined.Kind != KindUnknown {
		t.Errorf("JoinKnown(\"a\", \"b\").Kind = %v, want KindUnknown (unchanged boundary)", joined.Kind)
	}
}
