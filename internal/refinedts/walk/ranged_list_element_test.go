// Pins RangedListElementOf (ranged_list_element.go): element access
// into a KindList at a set-shaped (ranged) numeric index — the §G
// text_status.ts shape (`words[code]` where `code: number` is a bare,
// unguarded parameter — now grounded to R-bar by JT's ruling on
// annotations/type_node_sets.go's primitive-keyword arm).
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

func stringItem(s string) abstractdomain.AbstractValue {
	return abstractdomain.KnownValues(refinementsets.CodepointsOf(s), abstractdomain.PrimitiveString, abstractdomain.TrustProved)
}

// wordStringsOf reads a joined string-word set's own admitted words —
// WordTuplesOfConjunction's tuples, decoded back to text — for a
// plain structural check that every in-bounds element survived the
// join, with no kernel dependency.
func wordStringsOf(t *testing.T, set refinementsets.RefinedSet) []string {
	t.Helper()
	words, ok := refinementsets.WordTuplesOfConjunction(set)
	if !ok {
		t.Fatalf("the joined set %+v is not a plain word union", set)
	}
	out := make([]string, len(words))
	for i, word := range words {
		runes := make([]rune, len(word))
		for j, point := range word {
			runes[j] = rune(point)
		}
		out[i] = string(runes)
	}
	return out
}

// TestRangedListElementOf_FullyUnboundedIndexJoinsEveryElementPlusUndefined
// pins the text_status.ts shape directly: `code: number` (now grounded
// to R-bar, admitting negatives, fractions, and values past the
// array's own length) reads `words[code]` over a three-element exact
// list. Every element joins (all three are in-bounds candidates), and
// the possibly-undefined arm rides beside it — the index's own range
// reaches well outside [0, len).
func TestRangedListElementOf_FullyUnboundedIndexJoinsEveryElementPlusUndefined(t *testing.T) {
	words := abstractdomain.KnownList([]abstractdomain.AbstractValue{
		stringItem("ok"), stringItem("warn"), stringItem("error"),
	}, abstractdomain.TrustProved)
	code := abstractdomain.KnownSet(refinementsets.Numbers, nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone)

	got := RangedListElementOf(words, code)
	if got == nil {
		t.Fatalf("RangedListElementOf(words, R-bar) = nil, want a determined possibly-absent join")
	}
	if got.Kind != abstractdomain.KindPossiblyUndefined {
		t.Fatalf("Kind = %v, want KindPossiblyUndefined (the index reaches outside [0, len))", got.Kind)
	}
	if got.AbsentSide != abstractdomain.AbsentFlavorUndefOnly {
		t.Errorf("AbsentSide = %v, want AbsentFlavorUndefOnly (an array miss is exactly undefined, never null)", got.AbsentSide)
	}
	inner := *got.Inner
	if inner.Kind != abstractdomain.KindSet {
		t.Fatalf("the present half's Kind = %v, want KindSet (the joined string-word set)", inner.Kind)
	}
	// every one of the three words must be admitted — the join must not
	// have dropped any in-bounds candidate
	admitted := map[string]bool{}
	for _, word := range wordStringsOf(t, inner.Set) {
		admitted[word] = true
	}
	for _, word := range []string{"ok", "warn", "error"} {
		if !admitted[word] {
			t.Errorf("the joined set %v excludes %q — every in-bounds element must be admitted", admitted, word)
		}
	}
}

// TestRangedListElementOf_APlainlyBoundedWindowNeverWearsUndefined pins
// the "never a decline for a plainly bounded window" rule: an index
// proven to be an integer in [0, 2] (a three-element array's own
// index range, guarded) joins the three elements with NO
// possibly-undefined arm — the window never reaches outside [0, len).
func TestRangedListElementOf_APlainlyBoundedWindowNeverWearsUndefined(t *testing.T) {
	words := abstractdomain.KnownList([]abstractdomain.AbstractValue{
		stringItem("ok"), stringItem("warn"), stringItem("error"),
	}, abstractdomain.TrustProved)
	boundedCode := abstractdomain.KnownSet(
		refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.AtMost(2), refinementsets.Integer),
		nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone,
	)

	got := RangedListElementOf(words, boundedCode)
	if got == nil {
		t.Fatalf("RangedListElementOf(words, [0,2] integer) = nil, want a determined join")
	}
	if got.Kind == abstractdomain.KindPossiblyUndefined {
		t.Fatalf("Kind = %v, want a bare join (no possibly-undefined arm) — the window is plainly bounded inside [0, len)", got.Kind)
	}
}

// TestRangedListElementOf_AnEntirelyOutOfBoundsWindowAnswersExactlyUndefined
// pins the other edge: an index proven to sit entirely at or past the
// array's own length answers exactly undefined, no present half at
// all — there is no in-bounds candidate to join.
func TestRangedListElementOf_AnEntirelyOutOfBoundsWindowAnswersExactlyUndefined(t *testing.T) {
	words := abstractdomain.KnownList([]abstractdomain.AbstractValue{
		stringItem("ok"), stringItem("warn"), stringItem("error"),
	}, abstractdomain.TrustProved)
	pastEnd := abstractdomain.KnownSet(
		refinementsets.MakeRefinedSet(refinementsets.AtLeast(5), refinementsets.Integer),
		nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone,
	)

	got := RangedListElementOf(words, pastEnd)
	if got == nil {
		t.Fatalf("RangedListElementOf(words, >=5 integer) = nil, want exactly undefined")
	}
	if got.Kind != abstractdomain.KindUndef {
		t.Errorf("Kind = %v, want KindUndef — no position in [5, inf) names an array slot in a 3-element array", got.Kind)
	}
}
