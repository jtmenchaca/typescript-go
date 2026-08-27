// The string rows added for the syntax-coverage defects: the Annex B
// trim aliases (String.prototype.trimleft/trimright are the SAME
// function objects as trimStart/trimEnd), String.prototype.toString's
// identity, the functional replacer's assembly
// (sec-string.prototype.replace step 13.a; sec-string.prototype.replaceall
// advances by max(1, searchLength)), String.fromCharCode's ToUint16
// unit mapping (sec-string.fromcharcode, sec-touint16), the exact
// numeric Array.prototype.join (sec-array.prototype.join with each
// element spelled by sec-numeric-types-number-tostring), and the
// concatenation transfer `+` and `+=` share
// (sec-applystringornumericbinaryoperator). Most tests here exercise
// the pure computation directly — no kernel, no checker program — with
// one exception: String.prototype.match's no-match branch is read off
// a real CallExpression node (readStringMethods dereferences site.E),
// so it goes through entryEnvTestProgram/superArrayContracts, the
// canonical program-from-source recipe (AGENT-BRIEF.md), still with no
// kernel.
package walk

import (
	"math"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

func TestStringModelRows_TrimLeftAndTrimRightComputeTheirAnnexBTargets(t *testing.T) {
	// "The initial value of the *trimLeft* property is
	// %String.prototype.trimStart%" — same function object, same row
	cases := []struct {
		method string
		text   string
		want   string
	}{
		{"trimLeft", "  ab", "ab"},
		{"trimRight", "abcdefghij  ", "abcdefghij"},
		// NBSP and ZWNBSP are in the spec's white-space set; LS and PS
		// are LineTerminator — spelled as escapes, never literal (the
		// models file's own warning: a literal U+FEFF reads as a
		// byte-order mark)
		{"trimLeft", "\U000000A0\U0000FEFFab", "ab"},
		{"trimRight", "ab\U00002028\U00002029", "ab"},
	}
	for _, c := range cases {
		got, ok := exactZeroArgStringRow(c.method, c.text)
		if !ok {
			t.Errorf("exactZeroArgStringRow(%q, %q) ok = false, want true", c.method, c.text)
			continue
		}
		if got != c.want {
			t.Errorf("exactZeroArgStringRow(%q, %q) = %q, want %q", c.method, c.text, got, c.want)
		}
	}
	// the alias and its target answer identically on the same input
	fromAlias, _ := exactZeroArgStringRow("trimLeft", "  x ")
	fromTarget, _ := exactZeroArgStringRow("trimStart", "  x ")
	if fromAlias != fromTarget {
		t.Errorf("trimLeft = %q, trimStart = %q — the Annex B alias must compute the same row", fromAlias, fromTarget)
	}
}

// TestStringModelRows_TheCaseMappingsFoldTheUnconditionalSpecialCasing
// pins what replaced the earlier ASCII gate. sec-string.prototype.
// tolowercase (which sec-string.prototype.touppercase defers to) says
// the result "must be derived according to the locale-insensitive case
// mappings in the Unicode Character Database … not only the file
// UnicodeData.txt, but also all locale-insensitive mappings in the file
// SpecialCasing.txt". string_method_models_case.go transcribes exactly
// that pair — the simple 1:1 mapping plus the UNCONDITIONAL
// SpecialCasing rows — so a non-ASCII receiver no longer declines
// wholesale. Only the CONTEXT/LANGUAGE-sensitive section (Greek final
// sigma, the Turkic/Lithuanian/Azeri dotted-I) is untranscribed, and
// hasConditionalCasing declines on those code points instead.
func TestStringModelRows_TheCaseMappingsFoldTheUnconditionalSpecialCasing(t *testing.T) {
	// SpecialCasing.txt line 69: 00DF; 00DF; 0053 0073; 0053 0053 —
	// unconditional, so "ß" uppercases to "SS" and the row answers it
	got, ok := exactZeroArgStringRow("toUpperCase", "stra\U000000DFe")
	if !ok || got != "STRASSE" {
		t.Errorf(`exactZeroArgStringRow("toUpperCase", "straße") = %q, %v, want "STRASSE", true`, got, ok)
	}
	// the conditional section still declines: U+03A3 (Greek capital
	// sigma) lowercases to a final or medial sigma by CONTEXT, which
	// this transcription does not carry
	if _, ok := exactZeroArgStringRow("toLowerCase", "\U000003A3"); ok {
		t.Errorf("exactZeroArgStringRow(toLowerCase, U+03A3) ok = true, want false — the conditional-casing decline")
	}
	got, ok = exactZeroArgStringRow("toUpperCase", "ab")
	if !ok || got != "AB" {
		t.Errorf(`exactZeroArgStringRow("toUpperCase", "ab") = %q, %v, want "AB", true`, got, ok)
	}
}

func TestStringModelRows_TheGateListsCarryTheNewRows(t *testing.T) {
	for _, method := range []string{"trimLeft", "trimRight", "toString"} {
		if _, ok := dataflowfacts.StringReadMethods[method]; !ok {
			t.Errorf("dataflowfacts.StringReadMethods[%q] missing — the read-only gate refuses the method before any row can speak", method)
		}
	}
	for _, method := range []string{"trimLeft", "trimRight"} {
		if _, ok := stringOutMethods[method]; !ok {
			t.Errorf("stringOutMethods[%q] missing — an unpinned receiver loses the sort-level string answer", method)
		}
	}
}

func TestStringModelRows_ReplaceWithFunctionResultAssemblesTheSpecPieces(t *testing.T) {
	constant := func(text string) func(matched string, position int) (string, bool) {
		return func(string, int) (string, bool) { return text, true }
	}
	// replace: one match — preceding + replacement + following
	got, ok := replaceWithFunctionResult("abXcd", "X", false, constant("Y"))
	if !ok || got != "abYcd" {
		t.Errorf(`replace "abXcd"/"X" -> %q, %v, want "abYcd", true`, got, ok)
	}
	// the out-of-set twin stays long: the exact answer must still exceed
	// a max-8 refinement
	got, ok = replaceWithFunctionResult("abcdefghijX", "X", false, constant("YY"))
	if !ok || got != "abcdefghijYY" {
		t.Errorf(`replace "abcdefghijX"/"X" -> %q, %v, want "abcdefghijYY", true`, got, ok)
	}
	// replaceAll: every match position
	got, ok = replaceWithFunctionResult("aXbXc", "X", true, constant("Y"))
	if !ok || got != "aYbYc" {
		t.Errorf(`replaceAll "aXbXc"/"X" -> %q, %v, want "aYbYc", true`, got, ok)
	}
	// no match: the receiver rides unchanged and the oracle never runs
	got, ok = replaceWithFunctionResult("abXcd", "Z", false, func(string, int) (string, bool) {
		t.Errorf("the replacement oracle ran with no match")
		return "", false
	})
	if !ok || got != "abXcd" {
		t.Errorf(`replace "abXcd"/"Z" -> %q, %v, want "abXcd", true`, got, ok)
	}
	// the empty pattern matches at every index up to and including the
	// length (sec-stringindexof), advancing by max(1, 0) = 1
	got, ok = replaceWithFunctionResult("ab", "", true, constant("-"))
	if !ok || got != "-a-b-" {
		t.Errorf(`replaceAll "ab"/"" -> %q, %v, want "-a-b-", true`, got, ok)
	}
	// the oracle sees code-unit positions, matched always the pattern
	var positions []int
	_, ok = replaceWithFunctionResult("aXbXc", "X", true, func(matched string, position int) (string, bool) {
		if matched != "X" {
			t.Errorf("matched = %q, want %q — a string pattern's match is the pattern", matched, "X")
		}
		positions = append(positions, position)
		return "Y", true
	})
	if !ok || len(positions) != 2 || positions[0] != 1 || positions[1] != 3 {
		t.Errorf("positions = %v, want [1 3]", positions)
	}
	// an oracle that cannot pin a replacement declines the whole read
	if _, ok := replaceWithFunctionResult("abXcd", "X", false, func(string, int) (string, bool) { return "", false }); ok {
		t.Errorf("a declining oracle must decline the assembled result")
	}
}

func TestStringModelRows_ToUint16FollowsTheFixedSizeIntegerSteps(t *testing.T) {
	cases := []struct {
		in   float64
		want uint16
	}{
		{97, 97},
		{97 + 65536, 97}, // modulo 2^16
		{-1, 65535},      // negative wraps
		{3.7, 3},         // ToIntegerOrInfinity truncates
		{-3.7, 65533},    // truncate toward zero, then wrap
		{math.NaN(), 0},  // NaN reads 0
		{math.Inf(1), 0}, // ±∞ read 0 (sec-tofixedsizeinteger step 1)
		{math.Inf(-1), 0},
	}
	for _, c := range cases {
		if got := jsToUint16(c.in); got != c.want {
			t.Errorf("jsToUint16(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestStringModelRows_FromCharCodeBuildsUnitsAndRefusesLoneSurrogates(t *testing.T) {
	got, ok := jsFromCharCode([]float64{97})
	if !ok || got != "a" {
		t.Errorf(`jsFromCharCode([97]) = %q, %v, want "a", true`, got, ok)
	}
	got, ok = jsFromCharCode([]float64{104, 105})
	if !ok || got != "hi" {
		t.Errorf(`jsFromCharCode([104, 105]) = %q, %v, want "hi", true`, got, ok)
	}
	// a well-formed pair spells the astral scalar
	got, ok = jsFromCharCode([]float64{0xD801, 0xDC37})
	if !ok || got != "\U00010437" {
		t.Errorf(`jsFromCharCode([0xD801, 0xDC37]) = %q, %v, want U+10437, true`, got, ok)
	}
	// a lone surrogate has no code-point spelling — the exact read
	// declines rather than substituting U+FFFD
	if _, ok := jsFromCharCode([]float64{0xD800}); ok {
		t.Errorf("jsFromCharCode([0xD800]) ok = true, want false — a lone surrogate declines")
	}
	if _, ok := jsFromCharCode([]float64{0xDC00, 0xD800}); ok {
		t.Errorf("jsFromCharCode([0xDC00, 0xD800]) ok = true, want false — reversed halves do not pair")
	}
	got, ok = jsFromCharCode(nil)
	if !ok || got != "" {
		t.Errorf(`jsFromCharCode(nil) = %q, %v, want "", true`, got, ok)
	}
}

func TestStringModelRows_NumericJoinSpellsEachElementAndSeparates(t *testing.T) {
	// a decimal speller that declines everything: the strconv fallback
	// spells small integers identically, at the demoted grade
	declining := func(v float64) (string, bool) { return "", false }
	text, grade := jsNumericJoin(declining, []float64{40, 41}, ",")
	if text != "40,41" {
		t.Errorf(`jsNumericJoin([40 41], ",") = %q, want "40,41"`, text)
	}
	if grade != abstractdomain.TrustSpec {
		t.Errorf("grade = %v, want TrustSpec — the fallback spelling demotes", grade)
	}
	text, _ = jsNumericJoin(declining, []float64{100, 101, 102}, ",")
	if text != "100,101,102" {
		t.Errorf(`jsNumericJoin([100 101 102], ",") = %q, want "100,101,102"`, text)
	}
	// a proved speller keeps the proved grade
	proved := func(v float64) (string, bool) {
		if v == 40 {
			return "40", true
		}
		return "41", true
	}
	text, grade = jsNumericJoin(proved, []float64{40, 41}, "-")
	if text != "40-41" {
		t.Errorf(`jsNumericJoin([40 41], "-") = %q, want "40-41"`, text)
	}
	if grade != abstractdomain.TrustProved {
		t.Errorf("grade = %v, want TrustProved — every spelling came from the kernel", grade)
	}
	text, _ = jsNumericJoin(declining, nil, ",")
	if text != "" {
		t.Errorf(`jsNumericJoin(nil, ",") = %q, want "" — an empty array joins to the empty string`, text)
	}
}

func TestStringModelRows_TheConcatenationTransferIsExactOnTwoWords(t *testing.T) {
	// both sides exact string words: the tuple concatenation, with no
	// checker consulted (the sort question never arises), so nil
	// context and nil nodes are safe here
	left := abstractdomain.KnownValues(refinementsets.CodepointsOf("ab"), abstractdomain.PrimitiveString, abstractdomain.TrustProved)
	right := abstractdomain.KnownValues(refinementsets.CodepointsOf("c"), abstractdomain.PrimitiveString, abstractdomain.TrustProved)
	out := readStringConcatenation(nil, nil, nil, left, right)
	if out == nil {
		t.Fatalf("readStringConcatenation(exact, exact) = nil, want the concatenated word")
	}
	if out.Kind != abstractdomain.KindValues || out.KindTag != abstractdomain.PrimitiveString {
		t.Fatalf("out = %+v, want an exact string word", out)
	}
	if got := stringOf(out.Values); got != "abc" {
		t.Errorf(`concatenated = %q, want "abc"`, got)
	}
	// neither side stringy by knowledge or by node: no claim — the
	// numeric transfer keeps the operator (nil nodes read as no
	// string-sorted side)
	number := abstractdomain.KnownValues([]float64{1}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
	if out := readStringConcatenation(nil, nil, nil, number, number); out != nil {
		t.Errorf("readStringConcatenation(number, number) = %+v, want nil", out)
	}
}

// TestStringModelRows_MatchOnAMissReadsExactlyNull pins the producer
// split (KindNull vs KindUndef): a plain regex against a string it does
// not match answers the NULL value, not undefined — RegExp.prototype
// [%Symbol.match%]'s non-global branch delegates to RegExpExec
// (sec-regexp.prototype.exec: "returns an Array... or *null* if string
// did not match").
func TestStringModelRows_MatchOnAMissReadsExactlyNull(t *testing.T) {
	p := entryEnvTestProgram(t, "function f(): RegExpMatchArray | null {\n"+
		"  return \"abc\".match(/xyz/);\n"+
		"}\n")
	ctx := superArrayContracts(t, p)
	fn := entryEnvFunctionNamed(t, p, "f")
	returned := superArrayFirstNode(t, fn.Body(), "return statement", ast.IsReturnStatement)
	value := evaluateExpression(ctx, NewEnv(), returned.AsReturnStatement().Expression)
	if value.Kind != abstractdomain.KindNull {
		spelled, _ := abstractdomain.FormatAbstractValue(value)
		t.Errorf(`"abc".match(/xyz/) Kind = %v (%q), want KindNull`, value.Kind, spelled)
	}
}
