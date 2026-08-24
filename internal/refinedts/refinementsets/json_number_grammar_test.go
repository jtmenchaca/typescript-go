package refinementsets

import "testing"

/* ── PlainScalarWindow / TightenedJSONNumberGrammar: the CLASSIFICATION
 * pins — which window shape reaches which case, or none. These need no
 * membership question (no kernel), so they stay in this package.
 * Membership pins (does the resulting grammar actually ADMIT/EXCLUDE a
 * given string) live in walk/foreign_edge_test.go instead, against a
 * REAL loaded kernel's Member ask — the proved decider
 * (kernelbridge.RefinedTSKernel.Member, memberB_iff) is what this
 * package cannot reach (kernelbridge imports refinementsets, not the
 * other way — a package-layer cycle either way), and a hand-rolled
 * regex matcher here would be exactly the twin decider the thin-walk
 * doctrine deletes.
 */

// TestTightenedJSONNumberGrammar_ZeroToOneWindowFires pins case 1's own
// classification: a window ⊆ [0, 1] reaches the [0, 1] case.
func TestTightenedJSONNumberGrammar_ZeroToOneWindowFires(t *testing.T) {
	window := MakeRefinedSet(AtLeast(0), AtMost(1))
	if _, ok := TightenedJSONNumberGrammar(window); !ok {
		t.Fatalf("TightenedJSONNumberGrammar([0,1]) = ok=false, want a tightened grammar")
	}
}

// TestTightenedJSONNumberGrammar_AStraddlingWindowDeclines pins the
// fallback classification: a window straddling 0 ([-2, 2]) is neither
// non-negative nor non-positive, so no case fires and the caller keeps
// the windowless grammar.
func TestTightenedJSONNumberGrammar_AStraddlingWindowDeclines(t *testing.T) {
	window := MakeRefinedSet(AtLeast(-2), AtMost(2))
	if _, ok := TightenedJSONNumberGrammar(window); ok {
		t.Fatalf("TightenedJSONNumberGrammar([-2,2]) = ok=true, want the straddling window to decline")
	}
}

// TestTightenedJSONNumberGrammar_ANonNegativeWindowFires pins case 3's
// own classification, on a window wide enough that the tighter [0, 1]
// and integer-digit-count cases do not also fire.
func TestTightenedJSONNumberGrammar_ANonNegativeWindowFires(t *testing.T) {
	window := MakeRefinedSet(AtLeast(0), AtMost(1000.5))
	if _, ok := TightenedJSONNumberGrammar(window); !ok {
		t.Fatalf("TightenedJSONNumberGrammar([0,1000.5]) = ok=false, want a tightened grammar")
	}
}

// TestTightenedJSONNumberGrammar_ANonPositiveWindowFires pins case 4's
// own classification.
func TestTightenedJSONNumberGrammar_ANonPositiveWindowFires(t *testing.T) {
	window := MakeRefinedSet(AtLeast(-1000), AtMost(0))
	if _, ok := TightenedJSONNumberGrammar(window); !ok {
		t.Fatalf("TightenedJSONNumberGrammar([-1000,0]) = ok=false, want a tightened grammar")
	}
}

// TestTightenedJSONNumberGrammar_ANonNegativeIntegerWindowUsesDigitCounts
// pins case 2's own classification AND its shape directly (no
// membership question needed): the digit-count window numericSetText
// (walk/text_of_value.go) already derives for [0, 99] is Repetition
// (Digits, 1, 2), concatenated with the trailing newline.
func TestTightenedJSONNumberGrammar_ANonNegativeIntegerWindowUsesDigitCounts(t *testing.T) {
	window := MakeRefinedSet(Integer, AtLeast(0), AtMost(99))
	got, ok := TightenedJSONNumberGrammar(window)
	if !ok {
		t.Fatalf("TightenedJSONNumberGrammar(integer [0,99]) = ok=false, want a tightened grammar")
	}
	want := MakeRefinedSet(Concatenation(
		Repetition(Digits, 1, intPtr(2)),
		MakeRefinedSet(OneOf([]float64{'\n'})),
	))
	if !sameSetJSON(got, want) {
		t.Errorf("TightenedJSONNumberGrammar(integer [0,99]) = %+v, want the digit-count run %+v", got, want)
	}
}

// TestTightenedJSONNumberGrammar_AnUnclassifiableWindowFallsBackOkFalse
// pins the "anything else: unchanged" rule directly: an unbounded
// window (no AtMost/Below at all) is not one of the four ray/window
// shapes this reader classifies, and answers ok=false so the caller
// keeps the windowless grammar.
func TestTightenedJSONNumberGrammar_AnUnclassifiableWindowFallsBackOkFalse(t *testing.T) {
	unbounded := MakeRefinedSet(AtLeast(0))
	if _, ok := TightenedJSONNumberGrammar(unbounded); ok {
		t.Errorf("TightenedJSONNumberGrammar(atLeast(0)) = ok=true, want ok=false — no AtMost side to classify")
	}
}
