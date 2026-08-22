// numericSetText's digit-count derivation, pinned against Number::
// toString's no-leading-zero decimal spelling
// (sec-numeric-types-number-tostring): every integer in a non-negative
// window [lo, hi] spells EXACTLY digitCount(v) digit codepoints, so
// the derived string shape is a REPETITION window over Digits sized
// by the window's own digit-count span -- exact (repeat(Digits, n,
// n)) where lo and hi share a digit count, a repetition window
// (repeat(Digits, loCount, hiCount)) where they do not.

package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

func numberSet(lo, hi float64) refinementsets.RefinedSet {
	return refinementsets.MakeRefinedSet(
		refinementsets.AtLeast(lo),
		refinementsets.AtMost(hi),
		refinementsets.Integer,
	)
}

// TestNumericSetText_FourDigitWindowDerivesExactlyFourDigits pins the
// CONSTRUCT's concrete case: every integer in [1970, 9999] renders as
// exactly 4 digit codepoints (both edges share digitCount == 4), so
// the derived shape is repeat(Digits, 4, 4), never the unbounded star
// a count-blind derivation would leave.
func TestNumericSetText_FourDigitWindowDerivesExactlyFourDigits(t *testing.T) {
	got := numericSetText(numberSet(1970, 9999))
	rep, ok := refinementsets.AsRepetition(got)
	if !ok {
		t.Fatalf("numericSetText([1970,9999]) = %+v, want a readable repetition shape", got)
	}
	if rep.Lo != 4 {
		t.Errorf("numericSetText([1970,9999]) repetition Lo = %d, want 4", rep.Lo)
	}
	if rep.Hi == nil || *rep.Hi != 4 {
		t.Errorf("numericSetText([1970,9999]) repetition Hi = %v, want exactly 4 (not unbounded)", rep.Hi)
	}
}

// TestNumericSetText_DigitCountCrossingWindowDerivesTheCountSpan pins
// the general rule's other arm: [5, 120] crosses from 1-digit to
// 3-digit numbers (digitCount(5) == 1, digitCount(120) == 3), so the
// derived shape is the repetition window [1, 3] over Digits -- wider
// than the exact case, but still strictly narrower than the unbounded
// star today's derivation leaves.
func TestNumericSetText_DigitCountCrossingWindowDerivesTheCountSpan(t *testing.T) {
	got := numericSetText(numberSet(5, 120))
	rep, ok := refinementsets.AsRepetition(got)
	if !ok {
		t.Fatalf("numericSetText([5,120]) = %+v, want a readable repetition shape", got)
	}
	if rep.Lo != 1 {
		t.Errorf("numericSetText([5,120]) repetition Lo = %d, want 1", rep.Lo)
	}
	if rep.Hi == nil || *rep.Hi != 3 {
		t.Errorf("numericSetText([5,120]) repetition Hi = %v, want exactly 3", rep.Hi)
	}
}

// TestNumericSetText_UnboundedWindowKeepsTodaysStar pins that scope is
// held: a window with no upper edge (or any shape
// NonNegativeIntegerBounds refuses) has no digit-count fact to read
// off syntactically, and keeps today's unbounded star rather than
// guessing a window.
func TestNumericSetText_UnboundedWindowKeepsTodaysStar(t *testing.T) {
	unbounded := refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.Integer)
	got := numericSetText(unbounded)
	if !refinementsets.IsStringGround(got) {
		t.Errorf("numericSetText(unbounded) = %+v, want today's unbounded string ground (Strings)", got)
	}
}

func TestDigitCountOf(t *testing.T) {
	cases := []struct {
		v    float64
		want int
	}{
		{0, 1},
		{5, 1},
		{9, 1},
		{10, 2},
		{99, 2},
		{100, 3},
		{1970, 4},
		{9999, 4},
	}
	for _, c := range cases {
		if got := digitCountOf(c.v); got != c.want {
			t.Errorf("digitCountOf(%v) = %d, want %d", c.v, got, c.want)
		}
	}
}
