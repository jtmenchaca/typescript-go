package abstractdomain

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// stringGroundAbsorptionKernel loads the native kernel the same way
// every other kernel-gated test in this tree does (skip when the
// dylib is absent, fatal on a load error) — JoinKnown's string-ground
// absorption arm (lattice_operations.go) asks kernelSeqSubset, which
// answers ok=false with no kernel seated at all.
func stringGroundAbsorptionKernel(t *testing.T) *kernelbridge.RefinedTSKernel {
	t.Helper()
	if !kernelbridge.KernelArtifactsPresent(kernelbridge.DylibPath) {
		t.Skip("native kernel dylib absent — build it first")
	}
	kernel, err := kernelbridge.LoadKernel(kernelbridge.DylibPath)
	if err != nil {
		t.Fatalf("LoadKernel: %v", err)
	}
	return kernel
}

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
// Unknown. Neither side contains the other (kernelSeqSubset answers
// false both ways), so the string-ground ABSORPTION arm (below) does
// not touch this boundary either — the deliberate refusal stays exactly
// as strict with a kernel seated as it was declared to be unseated.
func TestJoinKnownOfOneCodepointStringsStillFallsToTheNumericPath(t *testing.T) {
	kernel := stringGroundAbsorptionKernel(t)
	SetLatticeKernel(kernel)
	t.Cleanup(func() { SetLatticeKernel(nil) })
	a := KnownValues([]float64{97}, PrimitiveString, TrustProved) // "a"
	b := KnownValues([]float64{98}, PrimitiveString, TrustProved) // "b"
	joined := JoinKnown(a, b)
	if joined.Kind != KindUnknown {
		t.Errorf("JoinKnown(\"a\", \"b\").Kind = %v, want KindUnknown (unchanged boundary — neither word contains the other)", joined.Kind)
	}
}

// TestJoinKnownStringGroundAbsorbsAContainedLiteral pins §G's
// text_label.ts shape directly: `padded.length >= 3 ? padded : "xxx"`
// joins a string-sorted, untagged set that is the WHOLE string ground
// (Strings — Star(Codepoints), which genuinely admits 1-character
// words and so can never pass stringWordSet's own reread-safety gate)
// against the exact literal "xxx". Before this fix: stringWordSet
// refused the ground side, the join fell to the numeric fallback,
// IsNumericKind refused the string-sorted side, and JoinKnown answered
// Unknown — even though "xxx" is trivially already a member of
// Strings. After this fix: the kernel's own seqSubset ask certifies
// "xxx" ⊆ Strings, and the join answers Strings itself — DETERMINED,
// never a widened claim (Strings already admitted "xxx" before this
// join ran).
func TestJoinKnownStringGroundAbsorbsAContainedLiteral(t *testing.T) {
	kernel := stringGroundAbsorptionKernel(t)
	SetLatticeKernel(kernel)
	t.Cleanup(func() { SetLatticeKernel(nil) })
	stringGround := KnownSet(refinementsets.Strings, nil, TrustProved, SetKindTagNone)
	literal := KnownValues(refinementsets.CodepointsOf("xxx"), PrimitiveString, TrustProved)
	joined := JoinKnown(stringGround, literal)
	if joined.Kind == KindUnknown {
		t.Fatalf("JoinKnown(Strings, \"xxx\") = Unknown, want the absorbed string ground")
	}
	if joined.Kind != KindSet || !refinementsets.IsStringGround(joined.Set) {
		spelled, _ := FormatAbstractValue(joined)
		t.Errorf("JoinKnown(Strings, \"xxx\") = %+v (%q), want the whole string ground (Strings) absorbed unchanged", joined, spelled)
	}
	// the SAME join, arguments swapped — absorption must not depend on
	// which side the ground rides on
	joinedReversed := JoinKnown(literal, stringGround)
	if joinedReversed.Kind != KindSet || !refinementsets.IsStringGround(joinedReversed.Set) {
		spelled, _ := FormatAbstractValue(joinedReversed)
		t.Errorf("JoinKnown(\"xxx\", Strings) = %+v (%q), want the whole string ground absorbed the same way, argument order reversed", joinedReversed, spelled)
	}
}
