// The JSON serialization grammar over a crossed cases schema —
// number-only tightening and the compositional per-kind arms — pinned
// against a real loaded kernel's Member ask.

package walk

import (
	"reflect"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// TestForeignStdoutSerializedValue_AZeroToOneReturnServesTheTightenedGrammar
// is the walk-level pin for the tightened stdout grammar: audio_level.
// py's own 0 … 1 return window (foreignArtifactJSON's exact shape) now
// serves refinementsets.TightenedJSONNumberGrammar's [0, 1] grammar
// rather than the windowless jsonNumberGrammarSet() — narrower, per
// this file's own trust discipline, and still a real claim over the
// SAME cases list the pre-existing windowless behavior read.
func TestForeignStdoutSerializedValue_AZeroToOneReturnServesTheTightenedGrammar(t *testing.T) {
	zeroToOne := refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.AtMost(1))
	got, ok := foreignStdoutSerializedValue([]Case{{Sort: CaseSortNumber, Set: zeroToOne}})
	if !ok {
		t.Fatalf("foreignStdoutSerializedValue(0…1) = ok=false, want true")
	}
	gotSet := mustSetOfKnown(t, got)
	wantSet, tightenedOk := refinementsets.TightenedJSONNumberGrammar(zeroToOne)
	if !tightenedOk {
		t.Fatalf("refinementsets.TightenedJSONNumberGrammar(0…1) = ok=false, want the [0,1] case to fire")
	}
	if !reflect.DeepEqual(gotSet, wantSet) {
		t.Errorf("foreignStdoutSerializedValue(0…1) served the windowless grammar, want the tightened [0,1] grammar")
	}
	if abstractdomain.TrustLevelOf(got) != abstractdomain.TrustSpec {
		t.Errorf("foreignStdoutSerializedValue(0…1) grade = %v, want TrustSpec", abstractdomain.TrustLevelOf(got))
	}
}

// TestForeignStdoutSerializedValue_AStraddlingWindowFallsBackWindowless
// pins the fallback side: a return window straddling 0 ([-2, 2], the
// argv-array outbound fixtures' own entry shape) is not one of
// TightenedJSONNumberGrammar's four derivable cases, so the served set
// stays the windowless jsonNumberGrammarSet() unchanged — the exact
// behavior every call site had before this file's tightening existed.
func TestForeignStdoutSerializedValue_AStraddlingWindowFallsBackWindowless(t *testing.T) {
	straddling := refinementsets.MakeRefinedSet(refinementsets.AtLeast(-2), refinementsets.AtMost(2))
	got, ok := foreignStdoutSerializedValue([]Case{{Sort: CaseSortNumber, Set: straddling}})
	if !ok {
		t.Fatalf("foreignStdoutSerializedValue(-2…2) = ok=false, want true")
	}
	gotSet := mustSetOfKnown(t, got)
	if !reflect.DeepEqual(gotSet, jsonNumberGrammarSet()) {
		t.Errorf("foreignStdoutSerializedValue(-2…2) did not fall back to the windowless grammar")
	}
}

/* ── the tightened grammars' own MEMBERSHIP pins, against a real kernel ──
 *
 * refinementsets cannot load a kernel (kernelbridge imports
 * refinementsets, not the other way), so the admit/exclude questions
 * about a compiled grammar set run here instead, through the SAME
 * proved decider (kernelbridge.RefinedTSKernel.Member, memberB_iff)
 * kernel_bridge_test.go's own string-equality rows already ask — never
 * a hand-rolled regex matcher duplicating what the kernel already
 * proves.
 */

func jsonGrammarAdmits(t *testing.T, kernel *kernelbridge.RefinedTSKernel, set refinementsets.RefinedSet, text string) bool {
	t.Helper()
	return kernel.Member(set, refinementsets.CodepointsOf(text))
}

// TestTightenedJSONNumberGrammar_ZeroToOneWindowAdmitsAndExcludes pins
// the [0, 1] case (refinementsets/json_number_grammar.go's
// jsonZeroToOnePattern, traced to Number::toString's own clauses):
// "0.5\n", "1\n", "9.5e-7\n" admitted; "-0.5\n" (outside the window's
// sign), "2\n" (outside the window's magnitude), "1.5\n" (no member of
// [0, 1] spells this text) excluded.
func TestTightenedJSONNumberGrammar_ZeroToOneWindowAdmitsAndExcludes(t *testing.T) {
	kernel := foreignReturnCornerKernel(t)
	window := refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.AtMost(1))
	set, ok := refinementsets.TightenedJSONNumberGrammar(window)
	if !ok {
		t.Fatalf("TightenedJSONNumberGrammar([0,1]) = ok=false, want a tightened grammar")
	}
	for _, admitted := range []string{"0.5\n", "1\n", "9.5e-7\n"} {
		if !jsonGrammarAdmits(t, kernel, set, admitted) {
			t.Errorf("the [0,1] grammar excludes %q, want it admitted", admitted)
		}
	}
	for _, excluded := range []string{"-0.5\n", "2\n", "1.5\n"} {
		if jsonGrammarAdmits(t, kernel, set, excluded) {
			t.Errorf("the [0,1] grammar admits %q, want it excluded", excluded)
		}
	}
}

// TestJSONNumberGrammarSet_TheWindowlessFallbackKeepsTheMinus pins the
// windowless PRODUCTION itself (jsonNumberGrammarSet, unchanged by
// this file's tightening): it still requires the minus on a negative
// value and still admits a positive one — the straddling-window
// fallback's own real behavior.
func TestJSONNumberGrammarSet_TheWindowlessFallbackKeepsTheMinus(t *testing.T) {
	kernel := foreignReturnCornerKernel(t)
	fallback := jsonNumberGrammarSet()
	if !jsonGrammarAdmits(t, kernel, fallback, "-1.5\n") {
		t.Errorf("the windowless grammar excludes %q, want it admitted (the minus is kept)", "-1.5\n")
	}
	if !jsonGrammarAdmits(t, kernel, fallback, "1.5\n") {
		t.Errorf("the windowless grammar excludes %q, want it admitted", "1.5\n")
	}
}

// TestTightenedJSONNumberGrammar_ANonNegativeWindowDropsTheMinus pins
// case 3 (jsonNonNegativePattern) on a bounded but wide non-negative
// window, [0, 1000.5] — wide enough that the tighter [0, 1] and
// integer-digit-count cases do not also fire.
func TestTightenedJSONNumberGrammar_ANonNegativeWindowDropsTheMinus(t *testing.T) {
	kernel := foreignReturnCornerKernel(t)
	window := refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.AtMost(1000.5))
	set, ok := refinementsets.TightenedJSONNumberGrammar(window)
	if !ok {
		t.Fatalf("TightenedJSONNumberGrammar([0,1000.5]) = ok=false, want a tightened grammar")
	}
	if !jsonGrammarAdmits(t, kernel, set, "500.25\n") {
		t.Errorf("the [0,1000.5] grammar excludes %q, want it admitted", "500.25\n")
	}
	if jsonGrammarAdmits(t, kernel, set, "-500\n") {
		t.Errorf("the [0,1000.5] grammar admits %q, want the minus excluded", "-500\n")
	}
}

// TestTightenedJSONNumberGrammar_ANonPositiveWindowRequiresTheMinus
// pins case 4 (jsonNonPositivePattern) directly: a window entirely at
// or below 0 requires the minus on every member except zero itself,
// and excludes an unsigned positive text.
func TestTightenedJSONNumberGrammar_ANonPositiveWindowRequiresTheMinus(t *testing.T) {
	kernel := foreignReturnCornerKernel(t)
	window := refinementsets.MakeRefinedSet(refinementsets.AtLeast(-1000), refinementsets.AtMost(0))
	set, ok := refinementsets.TightenedJSONNumberGrammar(window)
	if !ok {
		t.Fatalf("TightenedJSONNumberGrammar([-1000,0]) = ok=false, want a tightened grammar")
	}
	if !jsonGrammarAdmits(t, kernel, set, "0\n") {
		t.Errorf("the [-1000,0] grammar excludes %q, want it admitted", "0\n")
	}
	if !jsonGrammarAdmits(t, kernel, set, "-500\n") {
		t.Errorf("the [-1000,0] grammar excludes %q, want it admitted", "-500\n")
	}
	if jsonGrammarAdmits(t, kernel, set, "500\n") {
		t.Errorf("the [-1000,0] grammar admits %q, want a positive text excluded", "500\n")
	}
}

// TestTightenedJSONNumberGrammar_ANonNegativeIntegerWindowUsesDigitCounts
// pins case 2 (the digit-count run): a non-negative INTEGER window
// admits one- and two-digit texts and excludes a three-digit or
// fractional one.
func TestTightenedJSONNumberGrammar_ANonNegativeIntegerWindowUsesDigitCounts(t *testing.T) {
	kernel := foreignReturnCornerKernel(t)
	window := refinementsets.MakeRefinedSet(refinementsets.Integer, refinementsets.AtLeast(0), refinementsets.AtMost(99))
	set, ok := refinementsets.TightenedJSONNumberGrammar(window)
	if !ok {
		t.Fatalf("TightenedJSONNumberGrammar(integer [0,99]) = ok=false, want a tightened grammar")
	}
	if !jsonGrammarAdmits(t, kernel, set, "7\n") {
		t.Errorf("the integer [0,99] grammar excludes %q, want it admitted", "7\n")
	}
	if !jsonGrammarAdmits(t, kernel, set, "42\n") {
		t.Errorf("the integer [0,99] grammar excludes %q, want it admitted", "42\n")
	}
	if jsonGrammarAdmits(t, kernel, set, "100\n") {
		t.Errorf("the integer [0,99] grammar admits %q, want a three-digit text excluded", "100\n")
	}
	if jsonGrammarAdmits(t, kernel, set, "7.5\n") {
		t.Errorf("the integer [0,99] grammar admits %q, want a fractional text excluded", "7.5\n")
	}
}

// TestWindowlessJSONNumberGrammar_MatchesThisFilesOwnWindowlessProduction
// pins that refinementsets.WindowlessJSONNumberGrammar (the composer's
// own number-arm fallback) is structurally the SAME set this file's
// jsonNumberGrammarSet already compiles — one production, built from
// the identical pattern pieces and asked from two doors, never two
// hand-copied strings that could drift apart.
func TestWindowlessJSONNumberGrammar_MatchesThisFilesOwnWindowlessProduction(t *testing.T) {
	if !reflect.DeepEqual(refinementsets.WindowlessJSONNumberGrammar(), jsonNumberGrammarSet()) {
		t.Errorf("refinementsets.WindowlessJSONNumberGrammar() != this file's own jsonNumberGrammarSet() — the two windowless productions have drifted apart")
	}
}

// TestForeignStdoutSerializedValue_AnObjectReturnAdmitsTheExactSerializedForm
// pins the object arm end to end through foreignStdoutSerializedValue:
// a target stating a return of {"ok": boolean, "value": number} composes
// to a grammar that admits the EXACT serialized form (in either key
// order the small-N permutation union carries) and excludes a wrong-key
// spelling.
func TestForeignStdoutSerializedValue_AnObjectReturnAdmitsTheExactSerializedForm(t *testing.T) {
	kernel := foreignReturnCornerKernel(t)
	window := refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.AtMost(1))
	cases := []Case{{
		Sort:   CaseSortObject,
		Closed: true,
		Members: map[string][]Case{
			"ok":    {{Sort: CaseSortBoolean}},
			"value": {{Sort: CaseSortNumber, Set: window}},
		},
	}}
	got, ok := foreignStdoutSerializedValue(cases)
	if !ok {
		t.Fatalf("foreignStdoutSerializedValue(object {ok, value}) = ok=false, want true")
	}
	gotSet := mustSetOfKnown(t, got)
	admitted := `{"ok":true,"value":0.5}` + "\n"
	otherOrder := `{"value":0.5,"ok":true}` + "\n"
	if !jsonGrammarAdmits(t, kernel, gotSet, admitted) && !jsonGrammarAdmits(t, kernel, gotSet, otherOrder) {
		t.Errorf("the object grammar excludes both key orderings of %q, want at least one admitted", admitted)
	}
	wrongKey := `{"ko":true,"value":0.5}` + "\n"
	if jsonGrammarAdmits(t, kernel, gotSet, wrongKey) {
		t.Errorf("the object grammar admits the wrong-key spelling %q, want it excluded", wrongKey)
	}
	if abstractdomain.TrustLevelOf(got) != abstractdomain.TrustSpec {
		t.Errorf("foreignStdoutSerializedValue(object {ok, value}) grade = %v, want TrustSpec", abstractdomain.TrustLevelOf(got))
	}
}

// TestForeignStdoutSerializedValue_ANullAdmittingUnionAdmitsBothArmsSpellings
// pins the union-of-cases composition end to end: a number case beside
// a null case (a target stating Optional[float]) composes to a
// grammar that admits BOTH arms' own spellings — the tightened number
// window's text, and the bare "null\n" token json.dumps(None) writes.
func TestForeignStdoutSerializedValue_ANullAdmittingUnionAdmitsBothArmsSpellings(t *testing.T) {
	kernel := foreignReturnCornerKernel(t)
	window := refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.AtMost(1))
	cases := []Case{
		{Sort: CaseSortNumber, Set: window},
		{Sort: CaseSortNull},
	}
	got, ok := foreignStdoutSerializedValue(cases)
	if !ok {
		t.Fatalf("foreignStdoutSerializedValue(number [0,1] | null) = ok=false, want true")
	}
	gotSet := mustSetOfKnown(t, got)
	if !jsonGrammarAdmits(t, kernel, gotSet, "0.5\n") {
		t.Errorf("the number|null grammar excludes %q, want the number arm admitted", "0.5\n")
	}
	if !jsonGrammarAdmits(t, kernel, gotSet, "null\n") {
		t.Errorf("the number|null grammar excludes %q, want the null arm admitted", "null\n")
	}
	if jsonGrammarAdmits(t, kernel, gotSet, "true\n") {
		t.Errorf("the number|null grammar admits %q, want a boolean spelling excluded — neither arm states it", "true\n")
	}
}

// TestForeignStdoutSerializedValue_AnObjectReturnRendersItsSemanticHoverSpelling
// pins the RENDERING half: an object-case return's AbstractValue reads
// through abstractdomain.FormatAbstractValue as "the JSON of {...}" —
// the semantic per-case spelling json_case_grammar.go's JSONValueHover
// composes, reused here rather than the unreadable raw pattern text
// the compiled brace/colon/comma grammar would otherwise show.
func TestForeignStdoutSerializedValue_AnObjectReturnRendersItsSemanticHoverSpelling(t *testing.T) {
	window := refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.AtMost(1))
	cases := []Case{{
		Sort:   CaseSortObject,
		Closed: true,
		Members: map[string][]Case{
			"ok":    {{Sort: CaseSortBoolean}},
			"value": {{Sort: CaseSortNumber, Set: window}},
		},
	}}
	got, ok := foreignStdoutSerializedValue(cases)
	if !ok {
		t.Fatalf("foreignStdoutSerializedValue(object {ok, value}) = ok=false, want true")
	}
	rendered, renderedOk := abstractdomain.FormatAbstractValue(got)
	if !renderedOk {
		t.Fatalf("FormatAbstractValue(object return) = ok=false, want the semantic hover spelling")
	}
	if !strings.HasPrefix(rendered, "the JSON of {") {
		t.Errorf("FormatAbstractValue(object return) = %q, want it to open with \"the JSON of {\"", rendered)
	}
	if !strings.Contains(rendered, "ok: boolean") {
		t.Errorf("FormatAbstractValue(object return) = %q, want it to name the 'ok' member's own boolean word", rendered)
	}
	if !strings.Contains(rendered, "value: number") {
		t.Errorf("FormatAbstractValue(object return) = %q, want it to name the 'value' member's own number word", rendered)
	}
}
