package refinementsets

import (
	"strings"
	"testing"
)

/* ── JSONValueGrammar / JSONValueHover: the CLASSIFICATION pins — which
 * arm a case kind reaches, and the composition's own per-arm-degrades-
 * alone behavior. These need no kernel (no membership question), so
 * they stay in this package, mirroring json_number_grammar_test.go's
 * own split: MEMBERSHIP pins (does the composed grammar actually
 * ADMIT/EXCLUDE a given string) live in walk/foreign_edge_test.go
 * against a real loaded kernel's Member ask instead.
 */

// TestJSONValueGrammar_ANumberCaseTightensThroughTheLandedArm pins that
// the number-case arm reuses TightenedJSONNumberGrammar's own window
// dispatch (this file's own bareTightenedJSONNumberGrammar, the SAME
// case-by-case bounds reading with the trailing newline trimmed off
// each compiled pattern): a [0,1] window composes to the bare [0,1]
// production plus the ONE top-level trailing newline JSONValueGrammar
// appends after composing every case -- a DIFFERENT tree shape from
// TightenedJSONNumberGrammar(window) itself (that function compiles
// its own newline INSIDE the SAME regex as the digits; this composes
// the newline as a separate outer Concatenation), since every arm here
// composes bare so a number arm nested inside an object member never
// carries a premature newline before the closing brace.
// TightenedJSONNumberGrammar's own exact regex-compiled shape is
// pinned separately, unchanged, by json_number_grammar_test.go.
func TestJSONValueGrammar_ANumberCaseTightensThroughTheLandedArm(t *testing.T) {
	window := MakeRefinedSet(AtLeast(0), AtMost(1))
	bareTightened, tightenedOk := bareTightenedJSONNumberGrammar(window)
	if !tightenedOk {
		t.Fatalf("bareTightenedJSONNumberGrammar([0,1]) = ok=false, want a tightened grammar")
	}
	got, ok := JSONValueGrammar([]JSONCase{{Kind: JSONCaseNumber, Set: window}})
	if !ok {
		t.Fatalf("JSONValueGrammar(number [0,1]) = ok=false, want true")
	}
	newline := MakeRefinedSet(OneOf([]float64{'\n'}))
	want := MakeRefinedSet(Concatenation(bareTightened, newline))
	if !sameSetJSON(got, want) {
		t.Errorf("JSONValueGrammar(number [0,1]) did not compose the bare [0,1] production plus one trailing newline")
	}
}

// TestJSONValueGrammar_AnUnboundedNumberCaseFallsToTheWindowlessGrammar
// pins the windowless fallback arm: a number case with no derivable
// window still composes (never ok=false) to the BARE windowless
// production (bareWindowlessJSONNumberGrammar -- the same pattern
// pieces WindowlessJSONNumberGrammar compiles, minus its own baked-in
// newline) plus the ONE top-level newline JSONValueGrammar appends
// after composing every case. This is a DIFFERENT tree shape from
// WindowlessJSONNumberGrammar() itself (that function compiles its
// newline INSIDE one regex, this composes it as a separate outer
// Concatenation) -- both admit the identical language, but only the
// bare+wrap shape is what JSONValueGrammar actually builds now that
// every arm composes bare and the newline lands once, outermost, so a
// number arm nested inside an object member never carries a premature
// newline before the closing brace. WindowlessJSONNumberGrammar's own
// exact regex-compiled shape stays pinned separately, unchanged, by
// walk/foreign_edge_test.go's TestWindowlessJSONNumberGrammar_
// MatchesThisFilesOwnWindowlessProduction (which does not go through
// JSONValueGrammar at all).
func TestJSONValueGrammar_AnUnboundedNumberCaseFallsToTheWindowlessGrammar(t *testing.T) {
	unbounded := MakeRefinedSet(AtLeast(0)) // no AtMost side: PlainScalarWindow declines
	got, ok := JSONValueGrammar([]JSONCase{{Kind: JSONCaseNumber, Set: unbounded}})
	if !ok {
		t.Fatalf("JSONValueGrammar(number, unbounded) = ok=false, want true")
	}
	newline := MakeRefinedSet(OneOf([]float64{'\n'}))
	want := MakeRefinedSet(Concatenation(bareWindowlessJSONNumberGrammar(), newline))
	if !sameSetJSON(got, want) {
		t.Errorf("JSONValueGrammar(number, unbounded) did not fall back to the bare windowless production plus one trailing newline")
	}
}

// TestJSONValueGrammar_ANullCaseIsTheBareLiteral pins SerializeJSONProperty
// step 5 directly: a null case composes to exactly the four-codepoint
// word "null" followed by the harness's one trailing newline.
func TestJSONValueGrammar_ANullCaseIsTheBareLiteral(t *testing.T) {
	got, ok := JSONValueGrammar([]JSONCase{{Kind: JSONCaseNull}})
	if !ok {
		t.Fatalf("JSONValueGrammar(null) = ok=false, want true")
	}
	want := MakeRefinedSet(Concatenation(StringTuple("null"), MakeRefinedSet(OneOf([]float64{'\n'}))))
	if !sameSetJSON(got, want) {
		t.Errorf("JSONValueGrammar(null) != the literal \"null\" word plus the trailing newline")
	}
}

// TestJSONValueGrammar_ABooleanCaseIsTrueOrFalse pins
// SerializeJSONProperty steps 6-7: a boolean case composes to the
// two-word alternation "true" | "false", exact, with the harness's one
// trailing newline appended after the whole union (never inside each
// arm -- see JSONValueGrammar's own doc).
func TestJSONValueGrammar_ABooleanCaseIsTrueOrFalse(t *testing.T) {
	got, ok := JSONValueGrammar([]JSONCase{{Kind: JSONCaseBoolean}})
	if !ok {
		t.Fatalf("JSONValueGrammar(boolean) = ok=false, want true")
	}
	newline := MakeRefinedSet(OneOf([]float64{'\n'}))
	want := MakeRefinedSet(Concatenation(
		MakeRefinedSet(Union(StringTuple("true"), StringTuple("false"))),
		newline,
	))
	if !sameSetJSON(got, want) {
		t.Errorf("JSONValueGrammar(boolean) != (\"true\" | \"false\") plus the trailing newline")
	}
}

// TestJSONValueGrammar_AnExactStringSetAlternatesTheQuotedSpellings
// pins the string arm's exact-word case: a finite word set (a Python
// Literal["a","b"] shape) composes to the alternation of each word's
// own JSON-quoted spelling, never the wide quote-Codepoints-quote
// fallback, with the harness's one trailing newline appended after the
// whole alternation.
func TestJSONValueGrammar_AnExactStringSetAlternatesTheQuotedSpellings(t *testing.T) {
	words := MakeRefinedSet(Union(StringTuple("a"), StringTuple("b")))
	got, ok := JSONValueGrammar([]JSONCase{{Kind: JSONCaseString, Set: words}})
	if !ok {
		t.Fatalf("JSONValueGrammar(string, exact {a,b}) = ok=false, want true")
	}
	newline := MakeRefinedSet(OneOf([]float64{'\n'}))
	want := MakeRefinedSet(Concatenation(
		MakeRefinedSet(Union(StringTuple(`"a"`), StringTuple(`"b"`))),
		newline,
	))
	if !sameSetJSON(got, want) {
		t.Errorf("JSONValueGrammar(string, exact {a,b}) did not alternate the quoted spellings exactly")
	}
}

// TestJSONValueGrammar_APatternedStringSetFallsToTheWideQuoteGrammar
// pins the string arm's wide fallback: an ordinary z.string() shape
// (Strings itself, no finite word list) composes to the sound wide
// arm rather than ok=false, with the harness's one trailing newline
// appended after.
func TestJSONValueGrammar_APatternedStringSetFallsToTheWideQuoteGrammar(t *testing.T) {
	got, ok := JSONValueGrammar([]JSONCase{{Kind: JSONCaseString, Set: Strings}})
	if !ok {
		t.Fatalf("JSONValueGrammar(string, Strings) = ok=false, want true")
	}
	quote := MakeRefinedSet(OneOf([]float64{'"'}))
	newline := MakeRefinedSet(OneOf([]float64{'\n'}))
	want := MakeRefinedSet(Concatenation(
		MakeRefinedSet(Concatenation(quote, MakeRefinedSet(Concatenation(Strings, quote)))),
		newline,
	))
	if !sameSetJSON(got, want) {
		t.Errorf("JSONValueGrammar(string, Strings) did not fall back to the wide quote-Codepoints-quote grammar")
	}
}

// TestJSONValueGrammar_AClosedEmptyObjectCaseIsTheBareBraces pins
// SerializeJSONObject step 12: a CLOSED object with zero members
// composes to exactly "{}" plus the harness's one trailing newline —
// a closed empty object genuinely holds no members, ever.
func TestJSONValueGrammar_AClosedEmptyObjectCaseIsTheBareBraces(t *testing.T) {
	got, ok := JSONValueGrammar([]JSONCase{
		{Kind: JSONCaseObject, Members: map[string][]JSONCase{}, Closed: true},
	})
	if !ok {
		t.Fatalf("JSONValueGrammar(closed object, no members) = ok=false, want true")
	}
	want := MakeRefinedSet(Concatenation(StringTuple("{}"), MakeRefinedSet(OneOf([]float64{'\n'}))))
	if !sameSetJSON(got, want) {
		t.Errorf("JSONValueGrammar(closed object, no members) != the literal \"{}\" word plus the trailing newline")
	}
}

// TestJSONValueGrammar_AnOpenObjectCaseFallsToTheWideBraceGrammar pins
// the SOUNDNESS gate directly: an object case the producer states as
// OPEN (Closed: false — may carry members beyond the stated ones)
// composes to the wide "{" Codepoints "}" arm regardless of member
// count, EVEN WITH ZERO stated members — an exact "{}" or
// "{key:value}" spelling would exclude a real serialized text
// carrying an unstated extra member, which the wide arm alone does
// not. The harness's one trailing newline still rides after.
func TestJSONValueGrammar_AnOpenObjectCaseFallsToTheWideBraceGrammar(t *testing.T) {
	open := MakeRefinedSet(OneOf([]float64{'{'}))
	close_ := MakeRefinedSet(OneOf([]float64{'}'}))
	newline := MakeRefinedSet(OneOf([]float64{'\n'}))
	want := MakeRefinedSet(Concatenation(
		MakeRefinedSet(Concatenation(open, MakeRefinedSet(Concatenation(Strings, close_)))),
		newline,
	))

	gotEmpty, ok := JSONValueGrammar([]JSONCase{
		{Kind: JSONCaseObject, Members: map[string][]JSONCase{}, Closed: false},
	})
	if !ok {
		t.Fatalf("JSONValueGrammar(open object, no members) = ok=false, want true")
	}
	if !sameSetJSON(gotEmpty, want) {
		t.Errorf("JSONValueGrammar(open object, no members) did not fall back to the wide brace grammar")
	}

	gotWithMembers, ok := JSONValueGrammar([]JSONCase{
		{Kind: JSONCaseObject, Members: map[string][]JSONCase{"ok": {{Kind: JSONCaseBoolean}}}, Closed: false},
	})
	if !ok {
		t.Fatalf("JSONValueGrammar(open object, 1 member) = ok=false, want true")
	}
	if !sameSetJSON(gotWithMembers, want) {
		t.Errorf("JSONValueGrammar(open object, 1 member) did not fall back to the wide brace grammar")
	}
}

// TestJSONValueGrammar_AFourMemberClosedObjectCaseFallsToTheWideBraceGrammar
// pins jsonObjectMemberLimit's own boundary: a CLOSED object with a
// member count ABOVE the limit composes to the wide "{" Codepoints "}"
// arm, never an unbounded permutation blowup. The harness's one
// trailing newline still rides after.
func TestJSONValueGrammar_AFourMemberClosedObjectCaseFallsToTheWideBraceGrammar(t *testing.T) {
	members := map[string][]JSONCase{
		"a": {{Kind: JSONCaseBoolean}},
		"b": {{Kind: JSONCaseBoolean}},
		"c": {{Kind: JSONCaseBoolean}},
		"d": {{Kind: JSONCaseBoolean}},
	}
	got, ok := JSONValueGrammar([]JSONCase{{Kind: JSONCaseObject, Members: members, Closed: true}})
	if !ok {
		t.Fatalf("JSONValueGrammar(closed object, 4 members) = ok=false, want true")
	}
	open := MakeRefinedSet(OneOf([]float64{'{'}))
	close_ := MakeRefinedSet(OneOf([]float64{'}'}))
	newline := MakeRefinedSet(OneOf([]float64{'\n'}))
	want := MakeRefinedSet(Concatenation(
		MakeRefinedSet(Concatenation(open, MakeRefinedSet(Concatenation(Strings, close_)))),
		newline,
	))
	if !sameSetJSON(got, want) {
		t.Errorf("JSONValueGrammar(closed object, 4 members) did not fall back to the wide brace grammar")
	}
}

// TestJSONValueGrammar_ATwoMemberClosedObjectAlternatesBothKeyOrderings
// pins jsonObjectPermutations' own small-N exact arm: a CLOSED object
// with two members composes to the union of BOTH "key":value orderings
// (member map iteration carries no order, so a real serialized text may
// print either), brace-wrapped, comma-joined -- never a wide-arm
// fallback, and never only one ordering (permuteNames' own recursion
// must actually visit both leaves of a 2-element permutation, not
// silently collapse to a single arm through slice aliasing between
// sibling loop iterations).
func TestJSONValueGrammar_ATwoMemberClosedObjectAlternatesBothKeyOrderings(t *testing.T) {
	members := map[string][]JSONCase{
		"a": {{Kind: JSONCaseBoolean}},
		"b": {{Kind: JSONCaseBoolean}},
	}
	got, ok := JSONValueGrammar([]JSONCase{{Kind: JSONCaseObject, Members: members, Closed: true}})
	if !ok {
		t.Fatalf("JSONValueGrammar(closed object, 2 members) = ok=false, want true")
	}
	boolArm := MakeRefinedSet(Union(StringTuple("true"), StringTuple("false")))
	pairA := jsonMemberPair(`"a"`, boolArm)
	pairB := jsonMemberPair(`"b"`, boolArm)
	open := MakeRefinedSet(OneOf([]float64{'{'}))
	close_ := MakeRefinedSet(OneOf([]float64{'}'}))
	aFirst := MakeRefinedSet(Concatenation(open, MakeRefinedSet(Concatenation(jsonCommaJoin([]RefinedSet{pairA, pairB}), close_))))
	bFirst := MakeRefinedSet(Concatenation(open, MakeRefinedSet(Concatenation(jsonCommaJoin([]RefinedSet{pairB, pairA}), close_))))
	newline := MakeRefinedSet(OneOf([]float64{'\n'}))
	want := MakeRefinedSet(Concatenation(MakeRefinedSet(Union(aFirst, bFirst)), newline))
	if !sameSetJSON(got, want) {
		t.Errorf("JSONValueGrammar(closed object, 2 members) did not alternate both key orderings — want the union of {a,b} and {b,a} orderings")
	}
}

// TestPermuteNames_ThreeNamesVisitsAllSixOrderingsExactlyOnce pins
// permuteNames' own recursion directly, at N=3 -- the deepest jsonObjectMemberLimit
// permits and the shape most likely to expose slice aliasing between
// sibling loop iterations at the same recursion depth (each iteration
// calls append(chosen, remaining[i]) on the SAME chosen slice value; a
// bug reusing one shared backing array across siblings would make later
// iterations silently overwrite an earlier permutation's own captured
// tail, collapsing distinct orderings into duplicates rather than
// failing outright). Asserts all 3! = 6 distinct permutations are
// visited, each exactly once.
func TestPermuteNames_ThreeNamesVisitsAllSixOrderingsExactlyOnce(t *testing.T) {
	seen := map[string]int{}
	permuteNames([]string{"a", "b", "c"}, func(ordering []string) {
		seen[strings.Join(ordering, "")]++
	})
	want := []string{"abc", "acb", "bac", "bca", "cab", "cba"}
	if len(seen) != len(want) {
		t.Fatalf("permuteNames(a,b,c) visited %d distinct orderings, want %d — seen: %v", len(seen), len(want), seen)
	}
	for _, ordering := range want {
		if seen[ordering] != 1 {
			t.Errorf("permuteNames(a,b,c) visited %q %d times, want exactly 1", ordering, seen[ordering])
		}
	}
}

// TestJSONValueGrammar_AUnionOfCasesComposesEachArmIndependently pins
// the per-arm union composition: a null case beside a number case
// (a possibly-null numeric return) composes to the UNION of both
// arms' own BARE grammars, with the harness's one trailing newline
// appended exactly once after the whole union — never ok=false just
// because one arm is present alongside another sort, and never a
// newline embedded inside either arm (bareTightenedJSONNumberGrammar's
// own contract).
func TestJSONValueGrammar_AUnionOfCasesComposesEachArmIndependently(t *testing.T) {
	window := MakeRefinedSet(AtLeast(0), AtMost(1))
	got, ok := JSONValueGrammar([]JSONCase{
		{Kind: JSONCaseNumber, Set: window},
		{Kind: JSONCaseNull},
	})
	if !ok {
		t.Fatalf("JSONValueGrammar(number [0,1] | null) = ok=false, want true")
	}
	bareNumberArm, numberOk := bareTightenedJSONNumberGrammar(window)
	if !numberOk {
		t.Fatalf("bareTightenedJSONNumberGrammar([0,1]) = ok=false, want a tightened grammar")
	}
	newline := MakeRefinedSet(OneOf([]float64{'\n'}))
	want := MakeRefinedSet(Concatenation(
		MakeRefinedSet(Union(bareNumberArm, StringTuple("null"))),
		newline,
	))
	if !sameSetJSON(got, want) {
		t.Errorf("JSONValueGrammar(number [0,1] | null) did not union both arms' own bare grammars with one trailing newline after")
	}
}

// TestJSONValueGrammar_AnEmptyCasesListAnswersOkFalse pins the total
// failure boundary: nothing to compose at all answers ok=false, the
// caller's own signal to leave the stdout binding unbound.
func TestJSONValueGrammar_AnEmptyCasesListAnswersOkFalse(t *testing.T) {
	if _, ok := JSONValueGrammar(nil); ok {
		t.Errorf("JSONValueGrammar(nil) = ok=true, want ok=false — nothing to compose")
	}
}

/* ── JSONValueHover: the semantic spelling pins ─────────────────── */

// TestJSONValueHover_AnObjectCaseSpellsItsOwnMembersByName pins the
// hover vocabulary directly: an object case with a boolean 'ok' member
// and a number 'value' member spells as "the JSON of {ok: boolean,
// value: number {...}}" — the same brace-and-colon shape
// formatObjectCase (walk/fact_export.go) already reads for the
// identical schema, rebuilt from this package's own vocabulary.
func TestJSONValueHover_AnObjectCaseSpellsItsOwnMembersByName(t *testing.T) {
	window := MakeRefinedSet(AtLeast(0), AtMost(1))
	members := map[string][]JSONCase{
		"ok":    {{Kind: JSONCaseBoolean}},
		"value": {{Kind: JSONCaseNumber, Set: window}},
	}
	got, ok := JSONValueHover([]JSONCase{{Kind: JSONCaseObject, Members: members, Closed: true}})
	if !ok {
		t.Fatalf("JSONValueHover(object {ok, value}) = ok=false, want true")
	}
	numberFacts, factsOk := FormatForHover(window)
	if !factsOk {
		t.Fatalf("FormatForHover([0,1]) = ok=false, want facts")
	}
	want := "the JSON of {ok: boolean, value: number " + numberFacts + "}"
	if got != want {
		t.Errorf("JSONValueHover(object {ok, value}) = %q, want %q", got, want)
	}
}

// TestJSONValueHover_AnOpenObjectCaseDeclinesRatherThanOverclaim pins
// the hover side of the SAME soundness gate the grammar arm pins: an
// OPEN object case (Closed: false) declines the exact "{key: word}"
// spelling outright, agreeing with jsonObjectCaseGrammar's own
// identical refusal — the hover never claims a completeness the
// producer itself did not state.
func TestJSONValueHover_AnOpenObjectCaseDeclinesRatherThanOverclaim(t *testing.T) {
	members := map[string][]JSONCase{"ok": {{Kind: JSONCaseBoolean}}}
	if _, ok := JSONValueHover([]JSONCase{{Kind: JSONCaseObject, Members: members, Closed: false}}); ok {
		t.Errorf("JSONValueHover(open object) = ok=true, want ok=false — an open object states no exact member spelling")
	}
}

// TestJSONValueHover_EveryArmSharesJSONValueGrammarsOwnOkFalseBoundary
// pins that the hover walk and the grammar walk agree on totality: an
// empty cases list answers ok=false for both.
func TestJSONValueHover_EveryArmSharesJSONValueGrammarsOwnOkFalseBoundary(t *testing.T) {
	if _, ok := JSONValueHover(nil); ok {
		t.Errorf("JSONValueHover(nil) = ok=true, want ok=false")
	}
}
