package dataflowfacts

import "testing"

// TestLedgerDecisionsAStrictRowDecidesSixWays ports gates_places.test.ts's
// "ledger decisions: a strict row decides six ways" — the direct-read
// half; the kernel-composed fallback (constraintsImply, mixedConstraintsImply)
// is blocked (see this package's linear_constraints.go banner).
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

	assertBool(t, DecideComparison(rows, *low, *high, ComparisonLt), true)
	assertBool(t, DecideComparison(rows, *low, *high, ComparisonLe), true)
	assertBool(t, DecideComparison(rows, *low, *high, ComparisonGt), false)
	assertBool(t, DecideComparison(rows, *low, *high, ComparisonGe), false)
	assertBool(t, DecideComparison(rows, *low, *high, ComparisonEq), false)
	assertBool(t, DecideComparison(rows, *low, *high, ComparisonNe), true)
	// the mirror orientation flips every verdict
	assertBool(t, DecideComparison(rows, *high, *low, ComparisonGt), true)
	assertBool(t, DecideComparison(rows, *high, *low, ComparisonLt), false)
	// a RETIRED row decides nothing
	rows[0].Dead = true
	if got := DecideComparison(rows, *low, *high, ComparisonLt); got != nil {
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
