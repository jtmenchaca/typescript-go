// TextOfKnown's own word-set behavior for the absent kinds, pinned
// against sec-tostring (specifications/javascript/spec.html): ToString(undefined) is
// exactly "undefined" (step 3), ToString(null) is exactly "null" (step
// 4) — two DIFFERENT exact words, not one two-word set, now that
// KindUndef and KindNull are split. A KindPossiblyUndefined wrapper's
// own absent side contributes ITS flavor's word(s) on top of Inner's
// text: NullOnly exactly "null", UndefOnly exactly "undefined",
// conflated (the zero value, an honest may-claim over both) both words.

package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// noDecimal always declines — none of these cases route through a
// number's decimal spelling.
func noDecimal(v float64) (string, bool) { return "", false }

// wordsOf reads a text reading's Set back as a plain Go string set,
// via WordTuplesOf (the same finite-word reader the checker's own
// literal-equality narrowing uses).
func wordsOf(t *testing.T, set refinementsets.RefinedSet) map[string]bool {
	t.Helper()
	tuples, ok := refinementsets.WordTuplesOf(set)
	if !ok {
		t.Fatalf("WordTuplesOf(%+v) refused, want a finite word list", set)
	}
	words := make(map[string]bool, len(tuples))
	for _, tuple := range tuples {
		words[stringOf(tuple)] = true
	}
	return words
}

func TestTextOfKnown_UndefIsExactlyTheWordUndefined(t *testing.T) {
	got, ok := TextOfKnown(noDecimal, abstractdomain.Undef)
	if !ok {
		t.Fatalf("TextOfKnown(Undef) refused, want a reading")
	}
	if !got.HasExact || stringOf(got.Exact) != "undefined" {
		t.Errorf("TextOfKnown(Undef) = %+v, want exact \"undefined\"", got)
	}
}

func TestTextOfKnown_NullIsExactlyTheWordNull(t *testing.T) {
	got, ok := TextOfKnown(noDecimal, abstractdomain.Null)
	if !ok {
		t.Fatalf("TextOfKnown(Null) refused, want a reading")
	}
	if !got.HasExact || stringOf(got.Exact) != "null" {
		t.Errorf("TextOfKnown(Null) = %+v, want exact \"null\"", got)
	}
}

func TestTextOfKnown_PossiblyUndefinedNullOnlyUnionsOnlyNull(t *testing.T) {
	five := abstractdomain.KnownValues([]float64{5}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
	wrapped := abstractdomain.PossiblyAbsent(five, abstractdomain.AbsentFlavorNullOnly, "", false, false)
	got, ok := TextOfKnown(noDecimal, wrapped)
	if !ok {
		t.Fatalf("TextOfKnown(NullOnly wrapper) refused, want a reading")
	}
	words := wordsOf(t, got.Set)
	if words["undefined"] {
		t.Errorf("TextOfKnown(NullOnly wrapper).Set = %v, admits \"undefined\", want only \"null\" on the absent side", words)
	}
	if !words["null"] {
		t.Errorf("TextOfKnown(NullOnly wrapper).Set = %v, does not admit \"null\", want it to", words)
	}
}

func TestTextOfKnown_PossiblyUndefinedUndefOnlyUnionsOnlyUndefined(t *testing.T) {
	five := abstractdomain.KnownValues([]float64{5}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
	wrapped := abstractdomain.PossiblyAbsent(five, abstractdomain.AbsentFlavorUndefOnly, "", false, false)
	got, ok := TextOfKnown(noDecimal, wrapped)
	if !ok {
		t.Fatalf("TextOfKnown(UndefOnly wrapper) refused, want a reading")
	}
	words := wordsOf(t, got.Set)
	if words["null"] {
		t.Errorf("TextOfKnown(UndefOnly wrapper).Set = %v, admits \"null\", want only \"undefined\" on the absent side", words)
	}
	if !words["undefined"] {
		t.Errorf("TextOfKnown(UndefOnly wrapper).Set = %v, does not admit \"undefined\", want it to", words)
	}
}

func TestTextOfKnown_PossiblyUndefinedConflatedUnionsBothWords(t *testing.T) {
	five := abstractdomain.KnownValues([]float64{5}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
	wrapped := abstractdomain.PossiblyUndefined(five, "", false, false)
	got, ok := TextOfKnown(noDecimal, wrapped)
	if !ok {
		t.Fatalf("TextOfKnown(conflated wrapper) refused, want a reading")
	}
	words := wordsOf(t, got.Set)
	if !words["undefined"] {
		t.Errorf("TextOfKnown(conflated wrapper).Set = %v, does not admit \"undefined\", want it to (a may-claim over both runtime words)", words)
	}
	if !words["null"] {
		t.Errorf("TextOfKnown(conflated wrapper).Set = %v, does not admit \"null\", want it to (a may-claim over both runtime words)", words)
	}
}
