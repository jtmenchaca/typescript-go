// The JSON-number grammar a bounded scalar window forces, tightened
// from the windowless production RFC 8259 §6 states. The windowless
// grammar (walk/foreign_edge.go's jsonNumberGrammarPattern) is a sound
// claim for every value JSON.stringify can spell; this file narrows it
// to a WEAKER text set on the derivable windows, never a wider one --
// an excluding claim on a value the window still admits is the defect
// this file exists to avoid, so every case here traces to a clause of
// ECMA-262's Number::toString (sec-numeric-types-number-tostring,
// specifications/javascript/spec.html:2414) rather than to memory.
//
// Number::toString's own shape, read once for every case below: step 2
// answers "0" for both +0 and -0 before the sign check ever runs; step
// 3 answers "-" concatenated with toString(-x) for every x < -0 (so a
// negative value's tail is exactly the POSITIVE-side spelling of its
// magnitude); steps 6-9 hold radix 10 to plain decimal while the
// leading-digit exponent n sits in [-5, 21] and switch to scientific
// e-notation outside that band. A JSON number is that text with no
// leading zero on the integer part (json.dumps and JSON.stringify both
// follow this production), which is exactly what jsonNumberGrammarSet
// already encodes windowless.
package refinementsets

// jsonIntegerPartPattern is the ordinary decimal integer part with no
// leading zero -- the SAME production jsonNumberGrammarPattern already
// carries (walk/foreign_edge.go), repeated here so a tightened grammar
// stays built from the identical alphabet rather than a second
// hand-spelled copy.
const jsonIntegerPartPattern = `(0|[1-9][0-9]*)`

// jsonPlainOrScientificTailPattern is the fractional-and-exponent tail
// common to every case below: an optional decimal fraction, an
// optional exponent. Number::toString's plain-decimal branch (steps
// 6a-6c) never emits an exponent; its scientific branch (steps 7-9)
// always does and the mantissa may or may not carry a fraction
// depending on k -- one alternation covers both without picking a
// branch, which stays a sound over-approximation of either.
const jsonPlainOrScientificTailPattern = `(\.[0-9]+)?([eE][+-]?[0-9]+)?`

// jsonNonNegativePattern is jsonNumberGrammarPattern with the leading
// `-?` dropped: sound on any window ⊆ [0, +∞) because
// Number::toString never reaches its step-3 minus branch for such a
// value (step 3 fires only for x < -0), and -0 itself reads through
// step 2 as the bare digit "0", never "-0".
const jsonNonNegativePattern = jsonIntegerPartPattern + jsonPlainOrScientificTailPattern + `\n`

// jsonNonPositivePattern is sound on any window ⊆ (-∞, 0]: the only
// non-negative member such a window can hold is 0 itself (step 2's
// "0", the SAME literal spelling regardless of which zero the window
// carries), and every other member is strictly negative, spelling as
// step 3's "-" prefix over jsonNonNegativePattern's own tail applied
// to its (positive) magnitude -- reusing that pattern rather than a
// second hand-built copy, which also admits the harmless extra case
// of a "-0" tail the window's strictly-negative members never
// actually produce (a weaker true claim, not an excluding one).
const jsonNonPositivePattern = `(0|-` + jsonIntegerPartPattern + jsonPlainOrScientificTailPattern + `)\n`

// jsonZeroToOnePattern is sound on any window ⊆ [0, 1], read off the
// same clauses:
//
//   - x = 0 spells "0" (step 2, exact -- the only way a bare "0"
//     with no fraction and no exponent appears in this window).
//   - x = 1 spells "1" (step 5: n=1, k=1, s=1; step 6a fires since
//     n >= k, appending n-k = 0 zeroes -- the single digit alone,
//     never a decimal point, and no other member of [0, 1] reaches
//     step 6a since that branch needs n >= k >= 1 together with
//     x <= 1, which pins x = 1 exactly).
//   - every x in (0, 1) has leading-digit exponent n <= 0 (step 5's
//     s * 10^(n-k) = x with x < 1 forces n <= 0). Where n sits in
//     [-5, 0] (radix 10's plain-decimal band, step 6), step 6c fires:
//     "0." followed by -n zeroes then the digits of s -- always the
//     shape "0." + digits, i.e. `0\.[0-9]+`. Where n < -5, step 7's
//     scientific branch fires instead: sign is always "-" (step 7,
//     n < 0) and the exponent magnitude is abs(n-1); the mantissa is
//     one digit alone when k = 1 (step 8) or digit "." digits when
//     k > 1 (step 9) -- covered by the one alternation
//     `[1-9](\.[0-9]+)?e-[0-9]+` (spec's LATIN SMALL LETTER E, always
//     lowercase, never `E`).
const jsonZeroToOnePattern = `(0|1|0\.[0-9]+|[1-9](\.[0-9]+)?e-[0-9]+)\n`

// PlainScalarWindow reads a set as a plain ray conjunction -- the
// exact shape NonNegativeIntegerBounds already reads for the
// non-negative-integer case, generalized to ANY sign and to an
// OPTIONAL integer mark: {AtLeast(lo) or Above(lo)} and {AtMost(hi) or
// Below(hi)} folded through FoldRayForms, with nothing else riding
// alongside beside an optional Integer conjunct. ok=false on anything
// wider (an unbounded side, a OneOf, a MultipleOf, a Union, a
// Difference) -- the syntactic shape a declared z.number().min(lo)
// .max(hi) window leaves, no kernel round trip, so it only answers
// what is built exactly this way.
func PlainScalarWindow(set RefinedSet) (lo, hi float64, loStrict, hiStrict, isInteger, ok bool) {
	folded := FoldRayForms(set.Forms)
	var loForm, hiForm *Refinement
	sawInteger := false
	for i, f := range folded {
		switch f.Form {
		case FormAtLeast, FormAbove:
			if loForm != nil {
				return 0, 0, false, false, false, false
			}
			loForm = &folded[i]
		case FormAtMost, FormBelow:
			if hiForm != nil {
				return 0, 0, false, false, false, false
			}
			hiForm = &folded[i]
		case FormInteger:
			sawInteger = true
		default:
			return 0, 0, false, false, false, false
		}
	}
	if loForm == nil || hiForm == nil {
		return 0, 0, false, false, false, false
	}
	return loForm.A, hiForm.A, loForm.Form == FormAbove, hiForm.Form == FormBelow, sawInteger, true
}

// TightenedJSONNumberGrammar answers a JSON-number-grammar set NARROWER
// than the windowless production, derived from a scalar window's own
// [lo, hi] bounds where one of the sound cases above applies -- or
// ok=false, where the caller keeps the windowless grammar unchanged.
// Every returned set is compiled through FormatGrammar, the SAME door
// jsonNumberGrammarSet itself compiles through (walk/foreign_edge.go),
// so the tightened claim is built from the identical regex vocabulary
// rather than hand-assembled forms.
//
// windowSet is READ, never asked of a kernel -- a syntactic hull over
// the crossed cases' own declared bounds, exactly as PlainScalarWindow
// reads it. A window this reader cannot classify (unbounded on a side,
// a non-ray form riding alongside, or bounds that fit none of the four
// cases below) answers ok=false rather than guess.
func TightenedJSONNumberGrammar(windowSet RefinedSet) (RefinedSet, bool) {
	lo, hi, loStrict, hiStrict, isInteger, ok := PlainScalarWindow(windowSet)
	if !ok {
		return RefinedSet{}, false
	}
	nonNegative := lo >= 0 && !loStrict
	nonPositive := hi <= 0 && !hiStrict
	switch {
	// checked before the general non-negative case below: [0, 1] is
	// the tighter claim, and a strict subset must win the switch or
	// its own tighter grammar is never reached
	case nonNegative && hi <= 1 && !hiStrict:
		return compileAnchoredGrammar(jsonZeroToOnePattern)
	case isInteger && nonNegative && !hiStrict:
		return integerDigitCountGrammar(lo, hi)
	case nonNegative:
		return compileAnchoredGrammar(jsonNonNegativePattern)
	case nonPositive:
		return compileAnchoredGrammar(jsonNonPositivePattern)
	}
	return RefinedSet{}, false
}

// compileAnchoredGrammar mirrors jsonNumberGrammarSet's own compile: a
// fixed, already-exercised pattern, so a compile failure is an
// impossible state and panics rather than silently widening to
// Strings.
func compileAnchoredGrammar(pattern string) (RefinedSet, bool) {
	compiled := FormatGrammar("^"+pattern+"$", "")
	if !compiled.Ok {
		panic("a tightened JSON number pattern does not compile: " + compiled.Unsupported)
	}
	return compiled.Set, true
}

// integerDigitCountGrammar is the non-negative-integer window case:
// the JSON text is exactly the digit run numericSetText
// (walk/text_of_value.go) already derives for the SAME window shape
// (Number::toString never pads a leading zero, sec-numeric-types-
// number-tostring's own description line), with the one trailing
// newline the harness's captured stdout always carries appended after
// it. Spelled through Repetition(Digits, ...) -- the identical
// mechanism numericSetText uses -- rather than a regex source, since
// the digit-count window is already a RefinedSet shape and needs no
// grammar round trip.
func integerDigitCountGrammar(lo, hi float64) (RefinedSet, bool) {
	loCount := decimalDigitCount(lo)
	hiCount := decimalDigitCount(hi)
	digits := Repetition(Digits, loCount, &hiCount)
	newline := MakeRefinedSet(OneOf([]float64{'\n'}))
	return MakeRefinedSet(Concatenation(digits, newline)), true
}

// decimalDigitCount mirrors walk/text_of_value.go's digitCountOf: the
// decimal digit count of a non-negative integer under Number::
// toString's no-leading-zero rule (0 spells as one digit "0"; every
// v >= 1 spells as floor(log10(v)) + 1 digits), walked by repeated
// division so it stays exact at the double integers this window ever
// carries.
func decimalDigitCount(v float64) int {
	n := int64(v)
	if n == 0 {
		return 1
	}
	count := 0
	for n > 0 {
		count++
		n /= 10
	}
	return count
}
