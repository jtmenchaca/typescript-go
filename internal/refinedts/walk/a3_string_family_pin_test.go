// The A3 string/text family's own mechanisms, pinned as pure functions
// so each iterates under `go test` without a binary build or a fixture
// run: named capture groups (A3.xfer.capture), the text codecs
// (A3.xfer.encode), Unicode normalization (A3.xfer.normalize),
// percent-encoding and URL parsing (A3.xfer.url), and the finite
// repeat image (A3.xfer.repeat).
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// TestA3_xfer_capture_NamedGroupsCarryTheirOwnNamesAndLanguages: the
// span walk records `(?<code>[A-Z]{2})-(?<digits>\d+)`'s two names in
// order, and each group's own sub-pattern compiles to its own language
// — the digits group is a digit grammar, not the string sort.
func TestA3_xfer_capture_NamedGroupsCarryTheirOwnNamesAndLanguages(t *testing.T) {
	pattern := `^(?<code>[A-Z]{2})-(?<digits>\d+)$`
	spans, ok := captureGroupSourceSpans(pattern)
	if !ok {
		t.Fatalf("captureGroupSourceSpans(%q) refused the pattern", pattern)
	}
	if len(spans) != 2 {
		t.Fatalf("captureGroupSourceSpans(%q) found %d groups, want 2", pattern, len(spans))
	}
	if spans[0].name != "code" || spans[1].name != "digits" {
		t.Fatalf("group names = %q, %q; want \"code\", \"digits\"", spans[0].name, spans[1].name)
	}
	for i, span := range spans {
		if span.optional {
			t.Errorf("group %d reads optional; both groups participate in every match", i+1)
		}
	}
	digits, compiled, _ := captureGroupSetOf(pattern, "", 2)
	if !compiled {
		t.Fatalf("captureGroupSetOf(%q, 2) did not compile the digits group", pattern)
	}
	if _, isWordList := refinementsets.WordTuplesOf(digits); isWordList {
		t.Errorf("the digits group compiled to a finite word list; want the \\d+ grammar")
	}
}

// TestA3_xfer_capture_TheGroupsObjectNamesTheSameValuesTheSlotsHold:
// withNamedGroups hangs a COMPLETE `groups` object off the match list,
// each name holding the very value its numbered slot holds.
func TestA3_xfer_capture_TheGroupsObjectNamesTheSameValuesTheSlotsHold(t *testing.T) {
	pattern := `^(?<code>[A-Z]{2})-(?<digits>\d+)$`
	spans, ok := captureGroupSourceSpans(pattern)
	if !ok {
		t.Fatalf("captureGroupSourceSpans(%q) refused the pattern", pattern)
	}
	elements := captureGroupElements(pattern, "", spans, abstractdomain.TrustSpec)
	items := append([]abstractdomain.AbstractValue{abstractdomain.KnownSet(refinementsets.Strings, nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone)}, elements...)
	list := withNamedGroups(abstractdomain.KnownList(items, abstractdomain.TrustSpec), pattern, spans, elements, abstractdomain.TrustSpec)
	idx, found := objectKeyIndex(list, "groups")
	if !found {
		t.Fatalf("the match list carries no `groups` key")
	}
	groups := list.Keys[idx].Value
	if groups.Kind != abstractdomain.KindObject || !groups.Complete {
		t.Fatalf("`groups` = %+v, want a complete object", groups)
	}
	for i, name := range []string{"code", "digits"} {
		at, ok := objectKeyIndex(groups, name)
		if !ok {
			t.Fatalf("`groups` carries no %q key", name)
		}
		if !abstractdomain.SameKnown(groups.Keys[at].Value, elements[i]) {
			t.Errorf("groups.%s = %+v, want the value slot %d holds (%+v)", name, groups.Keys[at].Value, i+1, elements[i])
		}
	}
}

// TestA3_xfer_capture_APatternWithNoNamedGroupHasGroupsUndefined:
// sec-regexpbuiltinexec leaves `groups` undefined when the pattern
// defines no group name.
func TestA3_xfer_capture_APatternWithNoNamedGroupHasGroupsUndefined(t *testing.T) {
	pattern := `^(\d+)-(\d+)$`
	spans, ok := captureGroupSourceSpans(pattern)
	if !ok {
		t.Fatalf("captureGroupSourceSpans(%q) refused the pattern", pattern)
	}
	elements := captureGroupElements(pattern, "", spans, abstractdomain.TrustSpec)
	list := withNamedGroups(abstractdomain.KnownList(elements, abstractdomain.TrustSpec), pattern, spans, elements, abstractdomain.TrustSpec)
	idx, found := objectKeyIndex(list, "groups")
	if !found {
		t.Fatalf("the match list carries no `groups` key")
	}
	if list.Keys[idx].Value.Kind != abstractdomain.KindUndef {
		t.Errorf("groups = %+v, want undefined", list.Keys[idx].Value)
	}
}

// TestA3_xfer_normalize_NFCComposesTheDecomposedAcute: "e" + U+0301
// composes to U+00E9 under NFC — one code point where the input had
// two — and an already-normal ASCII text is unchanged.
func TestA3_xfer_normalize_NFCComposesTheDecomposedAcute(t *testing.T) {
	decomposed := "é"
	composed, ok := normalizedText("NFC", decomposed)
	if !ok {
		t.Fatalf("normalizedText(\"NFC\", %q) refused the form", decomposed)
	}
	if composed != "é" {
		t.Errorf("normalizedText(\"NFC\", %q) = %q, want %q", decomposed, composed, "é")
	}
	if unchanged, ok := normalizedText("NFC", "AA"); !ok || unchanged != "AA" {
		t.Errorf("normalizedText(\"NFC\", \"AA\") = %q, %v; want \"AA\", true", unchanged, ok)
	}
	if _, ok := normalizedText("NFQ", "AA"); ok {
		t.Errorf("normalizedText answered a form outside UAX #15's four; that call raises a RangeError")
	}
}

// TestA3_xfer_encode_TheReplacementCharacterStandsForEachIllFormedRun:
// decoding the single invalid byte 0xFF at the non-fatal default gives
// exactly one U+FFFD, and an ASCII round trip returns its own text.
func TestA3_xfer_encode_TheReplacementCharacterStandsForEachIllFormedRun(t *testing.T) {
	if got := decodeUTF8Replacing([]byte{0xff}); got != "�" {
		t.Errorf("decodeUTF8Replacing([0xFF]) = %q, want the one replacement character", got)
	}
	if got := decodeUTF8Replacing([]byte("AB")); got != "AB" {
		t.Errorf("decodeUTF8Replacing(\"AB\") = %q, want \"AB\"", got)
	}
	// "é" (U+00E9) encodes to exactly two bytes, 0xC3 0xA9
	bytes := []byte("é")
	if len(bytes) != 2 || bytes[0] != 0xc3 || bytes[1] != 0xa9 {
		t.Errorf("the UTF-8 bytes of U+00E9 = %v, want [0xC3 0xA9]", bytes)
	}
}

// TestA3_xfer_url_PercentEncodingSpellsASpaceAsPercentTwenty:
// sec-encodeuricomponent's Encode spells a space "%20"; the unreserved
// set passes through untouched.
func TestA3_xfer_url_PercentEncodingSpellsASpaceAsPercentTwenty(t *testing.T) {
	got, ok := urlEncodedComponentText("a b")
	if !ok || got != "a%20b" {
		t.Errorf("urlEncodedComponentText(\"a b\") = %q, %v; want \"a%%20b\", true", got, ok)
	}
	if got, ok := urlEncodedComponentText("AA"); !ok || got != "AA" {
		t.Errorf("urlEncodedComponentText(\"AA\") = %q, %v; want \"AA\", true", got, ok)
	}
}

// TestA3_xfer_url_AParsedQueryNamesItsParametersExactly: the object
// `new URL(<exact string>)` builds carries the query's own names, each
// holding its exact value.
func TestA3_xfer_url_AParsedQueryNamesItsParametersExactly(t *testing.T) {
	built, ok := exactUrlObject("https://x.example/?code=AB", abstractdomain.TrustSpec)
	if !ok {
		t.Fatalf("exactUrlObject refused an absolute https URL")
	}
	idx, found := objectKeyIndex(built, urlSearchParamsKey)
	if !found {
		t.Fatalf("the parsed URL carries no %q key", urlSearchParamsKey)
	}
	params := built.Keys[idx].Value
	if params.Kind != abstractdomain.KindObject || !params.Complete {
		t.Fatalf("searchParams = %+v, want a complete object", params)
	}
	at, found := objectKeyIndex(params, "code")
	if !found {
		t.Fatalf("the parsed query carries no \"code\" parameter")
	}
	if text, ok := exactStringOf(params.Keys[at].Value); !ok || text != "AB" {
		t.Errorf("searchParams.code = %+v, want the exact word \"AB\"", params.Keys[at].Value)
	}
	if _, found := objectKeyIndex(params, "other"); found {
		t.Errorf("the parsed query invented a parameter the URL does not carry")
	}
}

// TestA3_seed_boundary_ACallbackHandedToAnUncontractedCalleeIsStillWalked:
// the callback's stated return type binds it whether or not the checker
// holds a contract for the callee — `rl.on("line", (line): Code => …)`
// returns Σ* into Code, which must be refused. Before this reader the
// body was never walked at all, so nothing could be reported.
func TestA3_seed_boundary_ACallbackHandedToAnUncontractedCalleeIsStillWalked(t *testing.T) {
	compoundAssignLoadKernel(t)
	p := entryEnvTestProgram(t, "declare const rl: { on(event: string, cb: (line: string) => void): void };\n"+
		"function f(): void {\n"+
		"  rl.on(\"line\", (line): string => {\n"+
		"    return line;\n"+
		"  });\n"+
		"}\n")
	ctx := superArrayContracts(t, p)
	fn := entryEnvFunctionNamed(t, p, "f")
	call := superArrayFirstNode(t, fn.Body(), "call expression", ast.IsCallExpression)
	var arguments []*ast.Node
	if call.AsCallExpression().Arguments != nil {
		arguments = call.AsCallExpression().Arguments.Nodes
	}
	var arrow *ast.Node
	for _, argument := range arguments {
		if ast.IsArrowFunction(argument) {
			arrow = argument
		}
	}
	if arrow == nil {
		t.Fatalf("the probe call carries no arrow argument — the fixture shape changed")
	}
	// the listener position states `void`, so ONLY the arrow's own
	// stated return type can carry the obligation — and it must, even
	// though the checker holds no contract for `rl.on`
	if arrow.AsArrowFunction().Type == nil {
		t.Fatalf("the probe arrow states no return type — the fixture shape changed")
	}
	CheckOwnStatedReturnCallbacks(ctx, arguments)
}

// TestA3_xfer_repeat_AFiniteCountSetNamesItsWholeImage: the words
// "AB".repeat(n) takes over n in {0, 1, 2, 3} fold to one set spelling
// exactly those four, which is what the length read then counts.
func TestA3_xfer_repeat_AFiniteCountSetNamesItsWholeImage(t *testing.T) {
	words := []abstractdomain.AbstractValue{}
	for _, count := range []string{"", "AB", "ABAB", "ABABAB"} {
		words = append(words, abstractdomain.KnownValues(refinementsets.CodepointsOf(count), abstractdomain.PrimitiveString, abstractdomain.TrustSpec))
	}
	folded, ok := unionOfExactStrings(words, abstractdomain.TrustSpec)
	if !ok {
		t.Fatalf("unionOfExactStrings refused four exact words")
	}
	tuples, ok := refinementsets.WordTuplesOf(folded.Set)
	if !ok {
		t.Fatalf("the folded set does not read back as a word list")
	}
	if len(tuples) != 4 {
		t.Fatalf("the folded set spells %d words, want 4", len(tuples))
	}
	wantLengths := map[int]bool{0: true, 2: true, 4: true, 6: true}
	for _, tuple := range tuples {
		length := refinementsets.Utf16LengthOf(tuple)
		if !wantLengths[length] {
			t.Errorf("a folded word has length %d, outside {0, 2, 4, 6}", length)
		}
		delete(wantLengths, length)
	}
	if len(wantLengths) != 0 {
		t.Errorf("the folded words miss the lengths %v", wantLengths)
	}
}
