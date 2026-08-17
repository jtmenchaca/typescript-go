package narrowing

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// TestARemovedClosedEndpointBumpsAnIntegerBound ports apply_narrowing.test.ts's
// "a removed closed endpoint bumps an integer bound".
func TestARemovedClosedEndpointBumpsAnIntegerBound(t *testing.T) {
	// the sentinel shed: `i !== -1` over {integer, ≥ −1, ≤ 2} IS
	// {integer, ≥ 0, ≤ 2} — the difference form must not survive as a
	// stacked spelling, because the enclosure downstream keeps only
	// closed bounds and would keep the sentinel alive
	held := abstractdomain.KnownSet(
		refinementsets.MakeRefinedSet(refinementsets.Integer, refinementsets.AtLeast(-1), refinementsets.AtMost(2)),
		nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone,
	)
	removedSet := refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{-1}))
	narrowed := ApplyNarrowed(held, Narrowed{
		Binding:  "i",
		Forms:    []refinementsets.Refinement{refinementsets.Difference(refinementsets.MakeRefinedSet(), removedSet)},
		Refuting: true,
	})
	if narrowed.Kind != abstractdomain.KindSet {
		t.Fatalf("expected a set, got %v", narrowed.Kind)
	}
	want := []refinementsets.Refinement{
		{Form: refinementsets.FormAtLeast, A: 0},
		{Form: refinementsets.FormAtMost, A: 2},
		{Form: refinementsets.FormInteger},
	}
	if len(narrowed.Set.Forms) != len(want) {
		t.Fatalf("forms = %+v, want %+v", narrowed.Set.Forms, want)
	}
	for i, f := range narrowed.Set.Forms {
		if f.Form != want[i].Form || f.A != want[i].A {
			t.Errorf("forms[%d] = %+v, want %+v", i, f, want[i])
		}
	}
}

// TestARefutedWordEqualitySheds ports apply_narrowing.test.ts's "a
// refuted word equality sheds the word from a literal union".
func TestARefutedWordEqualitySheds(t *testing.T) {
	// `axisDomainType !== 'auto'` over {"number","category","auto"} IS
	// {"number","category"} — the difference form must not survive as a
	// stacked spelling: the pattern prover downstream reads one shape,
	// and the placement ask cannot help (C* minus a word sits inside no
	// finite union)
	inner := refinementsets.MakeRefinedSet(refinementsets.Union(
		refinementsets.StringTuple("number"), refinementsets.StringTuple("category"),
	))
	held := abstractdomain.KnownSet(
		refinementsets.MakeRefinedSet(refinementsets.Union(inner, refinementsets.StringTuple("auto"))),
		nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone,
	)
	narrowed := ApplyNarrowed(held, Narrowed{
		Binding:  "axisDomainType",
		Forms:    []refinementsets.Refinement{refinementsets.Difference(refinementsets.Strings, refinementsets.StringTuple("auto"))},
		Refuting: true,
	})
	if narrowed.Kind != abstractdomain.KindSet {
		t.Fatalf("expected a set, got %v", narrowed.Kind)
	}
	words, ok := refinementsets.WordTuplesOf(narrowed.Set)
	if !ok {
		t.Fatalf("expected a word list")
	}
	wantWords := [][]float64{refinementsets.CodepointsOf("number"), refinementsets.CodepointsOf("category")}
	if len(words) != len(wantWords) {
		t.Fatalf("words = %v, want %v", words, wantWords)
	}
	for i := range words {
		if !sameWord(words[i], wantWords[i]) {
			t.Errorf("words[%d] = %v, want %v", i, words[i], wantWords[i])
		}
	}
}

// TestAnInteriorRemovedPointKeepsTheStackedDifference ports
// apply_narrowing.test.ts's "an interior removed point keeps the
// stacked difference".
func TestAnInteriorRemovedPointKeepsTheStackedDifference(t *testing.T) {
	held := abstractdomain.KnownSet(
		refinementsets.MakeRefinedSet(refinementsets.Integer, refinementsets.AtLeast(-1), refinementsets.AtMost(2)),
		nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone,
	)
	removedSet := refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{1}))
	narrowed := ApplyNarrowed(held, Narrowed{
		Binding:  "i",
		Forms:    []refinementsets.Refinement{refinementsets.Difference(refinementsets.MakeRefinedSet(), removedSet)},
		Refuting: true,
	})
	if narrowed.Kind != abstractdomain.KindSet {
		t.Fatalf("expected a set, got %v", narrowed.Kind)
	}
	// 1 is interior — no bound moves, the difference claim stays
	hasDifference := false
	hasLoBound := false
	for _, f := range narrowed.Set.Forms {
		if f.Form == refinementsets.FormDifference {
			hasDifference = true
		}
		if f.Form == refinementsets.FormAtLeast && f.A == -1 {
			hasLoBound = true
		}
	}
	if !hasDifference {
		t.Errorf("expected the difference form to survive")
	}
	if !hasLoBound {
		t.Errorf("expected the lower bound to stay at -1")
	}
}

// TestExcludeKindPreservesTheAbsentFlavor pins the AbsentFlavor fix: a
// KindPossiblyUndefined wrapper's own AbsentSide (NullOnly/UndefOnly)
// must survive a refuted-typeof narrowing on its inner union — the
// pre-fix rebuild via PossiblyUndefined silently widened every
// flavored wrapper back to conflated.
func TestExcludeKindPreservesTheAbsentFlavor(t *testing.T) {
	number := abstractdomain.KnownValues([]float64{1}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
	str := abstractdomain.KnownValues(refinementsets.CodepointsOf("a"), abstractdomain.PrimitiveString, abstractdomain.TrustProved)
	union := abstractdomain.KindUnionOf([]abstractdomain.AbstractValue{number, str})
	wrapped := abstractdomain.PossiblyAbsent(union, abstractdomain.AbsentFlavorNullOnly, "", false, false)
	narrowed := ApplyNarrowed(wrapped, Narrowed{
		Binding:      "v",
		ExcludesKind: "string",
	})
	if narrowed.Kind != abstractdomain.KindPossiblyUndefined {
		t.Fatalf("ApplyNarrowed(ExcludesKind) = %+v, want KindPossiblyUndefined", narrowed)
	}
	if narrowed.AbsentSide != abstractdomain.AbsentFlavorNullOnly {
		t.Errorf("ApplyNarrowed(ExcludesKind).AbsentSide = %v, want AbsentFlavorNullOnly (excludeKind must not widen it back to conflated)", narrowed.AbsentSide)
	}
}

// TestDropWordSetPreservesTheAbsentFlavor is
// TestExcludeKindPreservesTheAbsentFlavor's dropWordSet twin.
func TestDropWordSetPreservesTheAbsentFlavor(t *testing.T) {
	inner := abstractdomain.KnownSet(
		refinementsets.MakeRefinedSet(refinementsets.Union(refinementsets.StringTuple("a"), refinementsets.StringTuple("b"))),
		nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone,
	)
	wrapped := abstractdomain.PossiblyAbsent(inner, abstractdomain.AbsentFlavorUndefOnly, "", false, false)
	narrowed := ApplyNarrowed(wrapped, Narrowed{
		Binding:            "v",
		WordSetExcluded:    [][]float64{refinementsets.CodepointsOf("a")},
		HasWordSetExcluded: true,
	})
	if narrowed.Kind != abstractdomain.KindPossiblyUndefined {
		t.Fatalf("ApplyNarrowed(WordSetExcluded) = %+v, want KindPossiblyUndefined", narrowed)
	}
	if narrowed.AbsentSide != abstractdomain.AbsentFlavorUndefOnly {
		t.Errorf("ApplyNarrowed(WordSetExcluded).AbsentSide = %v, want AbsentFlavorUndefOnly (dropWordSet must not widen it back to conflated)", narrowed.AbsentSide)
	}
}
