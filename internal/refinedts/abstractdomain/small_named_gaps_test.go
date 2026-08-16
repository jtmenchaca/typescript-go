// Companion test for gap 2 of three small pre-specified gaps, each
// named by a prior unit:
//
//  2. statesOnlyLongSequences (lattice_operations.go) walked a KindSet's
//     forms admitting FormConcatenation and FormUnion shapes, but not a
//     directly-held FormEmptyTuple — so a set-shaped value holding the
//     empty word alone could not join through stringWordSet. Admitting
//     FormEmptyTuple mirrors the len(k.Values) == 0 widening
//     empty_word_join_test.go already pins on the KindValues side: the
//     empty tuple is the concatenation identity, with no scalar-reread
//     ambiguity to guard against.
//
// (Gaps 1 and 3 live in package walk, where their own
// small_named_gaps_test.go sits beside the files they fix.)
package abstractdomain

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// TestStatesOnlyLongSequences_AdmitsADirectlyHeldEmptyTuple is the
// direct check on the widened gate: a set whose ONLY form is the empty
// tuple itself (not wrapped in a concatenation or a union) must pass,
// the same as a concatenation or union of long sequences already does.
func TestStatesOnlyLongSequences_AdmitsADirectlyHeldEmptyTuple(t *testing.T) {
	set := refinementsets.MakeRefinedSet(refinementsets.EmptyTuple)
	if !statesOnlyLongSequences(set) {
		t.Errorf("statesOnlyLongSequences(EmptyTuple) = false, want true — the empty tuple is the concatenation identity, no scalar to misread")
	}
}

// TestStatesOnlyLongSequences_AnEmptyTupleInsideAUnionStillAdmits pins
// the union recursion picking up the widened leaf: a union with the
// empty tuple on one side and a long word on the other must still pass.
func TestStatesOnlyLongSequences_AnEmptyTupleInsideAUnionStillAdmits(t *testing.T) {
	set := refinementsets.MakeRefinedSet(refinementsets.Union(
		refinementsets.MakeRefinedSet(refinementsets.EmptyTuple),
		refinementsets.StringTuple("ab"),
	))
	if !statesOnlyLongSequences(set) {
		t.Errorf("statesOnlyLongSequences(EmptyTuple | \"ab\") = false, want true")
	}
}

// TestStatesOnlyLongSequences_AOneCodepointFormStillRefuses pins the
// boundary the fix must not touch: a bare one-codepoint tuple is
// exactly the shape a scalar position could reread, and stays refused.
func TestStatesOnlyLongSequences_AOneCodepointFormStillRefuses(t *testing.T) {
	set := refinementsets.StringTuple("a")
	if statesOnlyLongSequences(set) {
		t.Errorf("statesOnlyLongSequences(\"a\") = true, want false (the 1-tuple reread risk, unchanged)")
	}
}

// TestStringWordSet_ADirectlyHeldEmptySetShapedTupleJoinsThroughTheGate
// is the consequence gap 2 exists for: stringWordSet's KindSet branch
// (the untagged-set half of the same function empty_word_join_test.go
// exercises on the KindValues half) now accepts a set whose only form
// is the empty tuple directly, not only one buried in a concatenation.
func TestStringWordSet_ADirectlyHeldEmptySetShapedTupleJoinsThroughTheGate(t *testing.T) {
	emptySetShaped := KnownSet(
		refinementsets.MakeRefinedSet(refinementsets.EmptyTuple),
		nil, TrustProved, SetKindTagNone,
	)
	set, ok := stringWordSet(emptySetShaped)
	if !ok {
		t.Fatalf("stringWordSet(KindSet{EmptyTuple}) = (_, false), want true")
	}
	word, wordOK := refinementsets.WordOf(set)
	if !wordOK || len(word) != 0 {
		t.Errorf("stringWordSet(KindSet{EmptyTuple})'s set = %v, %v, want the empty word", word, wordOK)
	}
}
