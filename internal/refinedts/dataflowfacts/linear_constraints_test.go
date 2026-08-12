package dataflowfacts

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// TestLedgerDecisionsAStrictRowDecidesSixWays ports gates_places.test.ts's
// "ledger decisions: a strict row decides six ways" — the direct-read
// half (kernel nil).
func TestLedgerDecisionsAStrictRowDecidesSixWays(t *testing.T) {
	c, file := checkerFor(t, `
const i = 0;
const n = 9;
if (i < n) {}
`)
	compare := ifConditions(file)[0].AsBinaryExpression()
	low := PlaceKeyOf(c, compare.Left)
	high := PlaceKeyOf(c, compare.Right)
	if low == nil || high == nil {
		t.Fatalf("expected places for both comparison sides")
	}
	rows := []DifferenceConstraint{
		{Minuend: *high, Subtrahend: *low, Bound: 0, Strict: true},
	}

	assertBool := func(t *testing.T, got *bool, want bool) {
		t.Helper()
		if got == nil {
			t.Fatalf("expected a decision, got nil")
		}
		if *got != want {
			t.Errorf("got %v, want %v", *got, want)
		}
	}

	assertBool(t, DecideComparison(rows, *low, *high, ComparisonLt, nil), true)
	assertBool(t, DecideComparison(rows, *low, *high, ComparisonLe, nil), true)
	assertBool(t, DecideComparison(rows, *low, *high, ComparisonGt, nil), false)
	assertBool(t, DecideComparison(rows, *low, *high, ComparisonGe, nil), false)
	assertBool(t, DecideComparison(rows, *low, *high, ComparisonEq, nil), false)
	assertBool(t, DecideComparison(rows, *low, *high, ComparisonNe, nil), true)
	// the mirror orientation flips every verdict
	assertBool(t, DecideComparison(rows, *high, *low, ComparisonGt, nil), true)
	assertBool(t, DecideComparison(rows, *high, *low, ComparisonLt, nil), false)
	// a RETIRED row decides nothing
	rows[0].Dead = true
	if got := DecideComparison(rows, *low, *high, ComparisonLt, nil); got != nil {
		t.Errorf("expected a dead row to decide nothing, got %v", *got)
	}
}

func TestDifferenceConstraintFor(t *testing.T) {
	c, file := checkerFor(t, `
const a = 1;
const b = 2;
a;
b;
`)
	aExpr := file.Statements.Nodes[2].AsExpressionStatement().Expression
	bExpr := file.Statements.Nodes[3].AsExpressionStatement().Expression
	a := PlaceKeyOf(c, aExpr)
	b := PlaceKeyOf(c, bExpr)
	if a == nil || b == nil {
		t.Fatalf("expected places")
	}

	rows := []DifferenceConstraint{
		{Minuend: *a, Subtrahend: *b, Bound: 3, Strict: false},
		{Minuend: *a, Subtrahend: *b, Bound: 5, Strict: false},
	}
	got := DifferenceConstraintFor(rows, *a, *b)
	if got == nil {
		t.Fatalf("expected a row")
	}
	if got.Bound != 5 {
		t.Errorf("expected the strongest bound (5), got %v", got.Bound)
	}

	if got := DifferenceConstraintFor(rows, *b, *a); got != nil {
		t.Errorf("expected no row for the mirror orientation, got %+v", got)
	}
}

func TestAnyRowMentions(t *testing.T) {
	c, file := checkerFor(t, `
const a = 1;
const b = 2;
const z = 3;
a;
b;
z;
`)
	aExpr := file.Statements.Nodes[3].AsExpressionStatement().Expression
	bExpr := file.Statements.Nodes[4].AsExpressionStatement().Expression
	zExpr := file.Statements.Nodes[5].AsExpressionStatement().Expression
	a := PlaceKeyOf(c, aExpr)
	b := PlaceKeyOf(c, bExpr)
	z := PlaceKeyOf(c, zExpr)
	if a == nil || b == nil || z == nil {
		t.Fatalf("expected places")
	}

	rows := []DifferenceConstraint{{Minuend: *a, Subtrahend: *b, Bound: 0, Strict: false}}
	if !AnyRowMentions(rows, *a) {
		t.Errorf("expected a to be mentioned")
	}
	if !AnyRowMentions(rows, *b) {
		t.Errorf("expected b to be mentioned")
	}
	if AnyRowMentions(rows, *z) {
		t.Errorf("expected z not to be mentioned")
	}

	rows[0].Dead = true
	if AnyRowMentions(rows, *a) {
		t.Errorf("expected a dead row to mention nothing")
	}
}

func TestComputedWindowFor(t *testing.T) {
	c, file := checkerFor(t, `
const a = 1;
const b = 2;
a;
b;
`)
	aExpr := file.Statements.Nodes[2].AsExpressionStatement().Expression
	bExpr := file.Statements.Nodes[3].AsExpressionStatement().Expression
	a := PlaceKeyOf(c, aExpr)
	b := PlaceKeyOf(c, bExpr)
	if a == nil || b == nil {
		t.Fatalf("expected places")
	}

	t.Run("no computed rows", func(t *testing.T) {
		if got := ComputedWindowFor(nil, *a, *b); got != nil {
			t.Errorf("expected nil, got %+v", got)
		}
	})

	t.Run("a low edge from the direct spelling", func(t *testing.T) {
		rows := []DifferenceConstraint{
			{Minuend: *a, Subtrahend: *b, Bound: 3, Strict: false, Computed: true},
		}
		got := ComputedWindowFor(rows, *a, *b)
		if got == nil || got.Lo == nil {
			t.Fatalf("expected a low edge")
		}
		if got.Lo.Value != 3 {
			t.Errorf("lo = %v, want 3", got.Lo.Value)
		}
		if got.Hi != nil {
			t.Errorf("expected no high edge, got %+v", got.Hi)
		}
	})

	t.Run("a high edge from the mirrored spelling", func(t *testing.T) {
		rows := []DifferenceConstraint{
			{Minuend: *b, Subtrahend: *a, Bound: -3, Strict: false, Computed: true},
		}
		got := ComputedWindowFor(rows, *a, *b)
		if got == nil || got.Hi == nil {
			t.Fatalf("expected a high edge")
		}
		if got.Hi.Value != 3 {
			t.Errorf("hi = %v, want 3", got.Hi.Value)
		}
		if got.Lo != nil {
			t.Errorf("expected no low edge, got %+v", got.Lo)
		}
	})

	t.Run("a non-computed row is ignored", func(t *testing.T) {
		rows := []DifferenceConstraint{
			{Minuend: *a, Subtrahend: *b, Bound: 3, Strict: false},
		}
		if got := ComputedWindowFor(rows, *a, *b); got != nil {
			t.Errorf("expected nil, got %+v", got)
		}
	})
}

// loadDataflowKernel is the local twin of kernelbridge's
// loadRoundTripKernel: skip (never fake a pass) when the native dylib
// artifact is absent.
func loadDataflowKernel(t *testing.T) *kernelbridge.RefinedTSKernel {
	t.Helper()
	if !kernelbridge.KernelArtifactsPresent(kernelbridge.DylibPath) {
		t.Skip("native kernel dylib absent — build it first")
	}
	kernel, err := kernelbridge.LoadKernel(kernelbridge.DylibPath)
	if err != nil {
		t.Fatalf("LoadKernel: %v", err)
	}
	return kernel
}

// TestDecideComparisonKernelFallbackDecidesATransitiveChain ports
// linear_constraints.test.ts's "relations: a transitive chain decides a
// comparison no single row holds" down to the ledger question it exercises:
// a < b and b < c decide a < c only by COMPOSING the rows — no single row
// orders (a, c) directly.
func TestDecideComparisonKernelFallbackDecidesATransitiveChain(t *testing.T) {
	kernel := loadDataflowKernel(t)
	c, file := checkerFor(t, `
const a = 1;
const b = 2;
const cc = 3;
a;
b;
cc;
`)
	aExpr := file.Statements.Nodes[3].AsExpressionStatement().Expression
	bExpr := file.Statements.Nodes[4].AsExpressionStatement().Expression
	cExpr := file.Statements.Nodes[5].AsExpressionStatement().Expression
	a := PlaceKeyOf(c, aExpr)
	b := PlaceKeyOf(c, bExpr)
	cc := PlaceKeyOf(c, cExpr)
	if a == nil || b == nil || cc == nil {
		t.Fatalf("expected places for a, b, c")
	}
	// a < b: b − a ≥ 0, strict
	// b < c: c − b ≥ 0, strict
	rows := []DifferenceConstraint{
		{Minuend: *b, Subtrahend: *a, Bound: 0, Strict: true},
		{Minuend: *cc, Subtrahend: *b, Bound: 0, Strict: true},
	}
	// no single row orders (a, c) directly
	if got := DecideComparison(rows, *a, *cc, ComparisonLt, nil); got != nil {
		t.Fatalf("expected the direct read to decide nothing, got %v", *got)
	}
	got := DecideComparison(rows, *a, *cc, ComparisonLt, kernel)
	if got == nil {
		t.Fatalf("expected the kernel fallback to decide a < c")
	}
	if !*got {
		t.Errorf("expected a < c, got false")
	}
}

// TestMixedConstraintsImplyComposesASumRowAndADifferenceRow ports
// linear_constraints.test.ts's "relations: a sum row and a difference row
// prove an index together" down to the ledger question: offset + len ≤
// anchor and k < len reach offset + k < anchor only by COMPOSING the sum
// row (whose terms are offset, len) with the difference row (k, len) — the
// two share no place pair a single-row read could match.
func TestMixedConstraintsImplyComposesASumRowAndADifferenceRow(t *testing.T) {
	kernel := loadDataflowKernel(t)
	c, file := checkerFor(t, `
const offset = 0;
const k = 1;
const len = 2;
const anchor = 3;
offset;
k;
len;
anchor;
`)
	offsetExpr := file.Statements.Nodes[4].AsExpressionStatement().Expression
	kExpr := file.Statements.Nodes[5].AsExpressionStatement().Expression
	lenExpr := file.Statements.Nodes[6].AsExpressionStatement().Expression
	anchorExpr := file.Statements.Nodes[7].AsExpressionStatement().Expression
	offset := PlaceKeyOf(c, offsetExpr)
	k := PlaceKeyOf(c, kExpr)
	length := PlaceKeyOf(c, lenExpr)
	anchor := PlaceKeyOf(c, anchorExpr)
	if offset == nil || k == nil || length == nil || anchor == nil {
		t.Fatalf("expected places for offset, k, len, anchor")
	}
	// offset + len ≤ anchor (sumAtMost: anchor − (offset + len) ≥ 0)
	sums := []SumConstraint{
		{Terms: [2]PlaceKey{*offset, *length}, Offset: 0, Anchor: *anchor, SumAtMost: true, Strict: false},
	}
	// k < len: len − k ≥ 0, strict
	orderRows := []DifferenceConstraint{
		{Minuend: *length, Subtrahend: *k, Bound: 0, Strict: true},
	}
	// target: anchor − (offset + k) ≥ 0, i.e. offset + k < anchor is
	// posed as anchor − offset − k > 0 (strict)
	implied := MixedConstraintsImply(kernel, orderRows, sums, MixedConstraintsTarget{
		Plus:   []PlaceKey{*anchor},
		Minus:  []PlaceKey{*offset, *k},
		Bound:  0,
		Strict: true,
	})
	if !implied {
		t.Errorf("expected the sum row and the difference row to compose")
	}
}

// TestConstraintsImply ports constraintsImply's single-pair convenience
// wrapper over MixedConstraintsImply.
func TestConstraintsImply(t *testing.T) {
	kernel := loadDataflowKernel(t)
	c, file := checkerFor(t, `
const a = 1;
const b = 2;
a;
b;
`)
	aExpr := file.Statements.Nodes[2].AsExpressionStatement().Expression
	bExpr := file.Statements.Nodes[3].AsExpressionStatement().Expression
	a := PlaceKeyOf(c, aExpr)
	b := PlaceKeyOf(c, bExpr)
	if a == nil || b == nil {
		t.Fatalf("expected places for a, b")
	}
	rows := []DifferenceConstraint{
		{Minuend: *b, Subtrahend: *a, Bound: 5, Strict: false},
	}
	if !ConstraintsImply(kernel, rows, *b, *a, 3, false) {
		t.Errorf("expected the row to imply a looser bound")
	}
	if ConstraintsImply(kernel, rows, *b, *a, 10, false) {
		t.Errorf("expected the row not to imply a tighter bound than it states")
	}
}
