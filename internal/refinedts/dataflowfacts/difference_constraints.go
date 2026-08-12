// from dataflow_facts/difference_constraints.ts
//
// Difference rows a condition vouches: ordered comparisons between
// places, with optional literal offsets and fl-slack widening past
// the safe integer range.

package dataflowfacts

import (
	"math"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/jsnum"
	"github.com/microsoft/typescript-go/internal/refinedts/conditiontree"
)

// numericLiteralValue reads a NumericLiteral node's exact value —
// TS's `Number(node.text)` — through jsnum.FromString (the checker's
// own ECMA StringToNumber), never strconv.ParseFloat, which diverges
// on hex/octal/binary prefixes (PORT.md).
func numericLiteralValue(e *ast.Node) float64 {
	return float64(jsnum.FromString(e.AsNumericLiteral().Text))
}

// ExactWindow is a side's provably exact numeric window — the Go
// twin of the TS inline `{ lo, hi, int }` the exactWindow callback
// returns.
type ExactWindow struct {
	Lo  float64
	Hi  float64
	Int bool
}

// FlSlackOf is an outward bound on |fl(x + offset) − (x + offset)|
// over every integer x the window admits, answered by the kernel's
// proved envelope decider — ok=false where it cannot answer. Without
// it an offset side whose arithmetic leaves the safe range is
// refused.
type FlSlackOf func(window ExactWindow, offset float64) (float64, bool)

// DifferenceConstraintsOf is the difference rows a condition's TRUE
// side vouches: `a < b`, `a <= b` and their mirrors, over places that
// stay stable through `scope` (the branch the rows will be consumed
// in). A side may carry an integer literal OFFSET (`x <= y - 5`) when
// the caller supplies `exactWindow` and the offset arithmetic is
// provably exact: an integer window within the safe range keeps
// fl(y ± k) equal to y ± k, so the guard's float comparison speaks
// about the real difference.
func DifferenceConstraintsOf(
	c *checker.Checker,
	condition *ast.Node,
	scope *ast.Node,
	exactWindow func(e *ast.Node) (ExactWindow, bool),
	exactValue func(e *ast.Node) (float64, bool),
	flSlack FlSlackOf,
) []DifferenceConstraint {
	return constraintsOfCondition(c, condition, scope, false, exactWindow, exactValue, nil, flSlack)
}

// NegatedDifferenceConstraintsOf is the rows a condition's FALSE side
// vouches — what the continuation after an exiting then-branch
// knows. ¬(a < b) proves a ≥ b only of a REAL pair (a comparison with
// NaN in it is false either way), so the caller supplies `real` and
// every negated row is gated on both sides being NaN-free. De Morgan
// reads the connectives: a refuted `||` refutes both sides, a
// refuted `!` holds its operand, and a refuted `&&` proves nothing
// about either side alone.
func NegatedDifferenceConstraintsOf(
	c *checker.Checker,
	condition *ast.Node,
	scope *ast.Node,
	exactWindow func(e *ast.Node) (ExactWindow, bool),
	exactValue func(e *ast.Node) (float64, bool),
	real func(e *ast.Node) bool,
	flSlack FlSlackOf,
) []DifferenceConstraint {
	return constraintsOfCondition(c, condition, scope, true, exactWindow, exactValue, real, flSlack)
}

// side is the sideOf callback's return shape in the TS source.
type side struct {
	Place  PlaceKey
	Offset float64
	Slack  float64
}

// shape is the held/shape object literal in the TS source: which
// operand of a comparison is the LOW side, and whether it is strict.
type shape struct {
	LowOnLeft bool
	Strict    bool
}

func constraintsOfCondition(
	c *checker.Checker,
	condition *ast.Node,
	scope *ast.Node,
	negatedAtRoot bool,
	exactWindow func(e *ast.Node) (ExactWindow, bool),
	exactValue func(e *ast.Node) (float64, bool),
	real func(e *ast.Node) bool,
	flSlack FlSlackOf,
) []DifferenceConstraint {
	var rows []DifferenceConstraint
	fn := EnclosingFunctionOf(condition)

	sideOf := func(e *ast.Node) *side {
		cursor := e
		for ast.IsParenthesizedExpression(cursor) {
			cursor = cursor.AsParenthesizedExpression().Expression
		}
		if ast.IsBinaryExpression(cursor) {
			bin := cursor.AsBinaryExpression()
			if bin.OperatorToken.Kind == ast.KindPlusToken || bin.OperatorToken.Kind == ast.KindMinusToken {
				// the offset operand: a numeric literal, or a place whose
				// knowledge is one exact integer (a const the guard captured)
				var k float64
				var hasK bool
				if ast.IsNumericLiteral(bin.Right) {
					k, hasK = numericLiteralValue(bin.Right), true
				} else if exactValue != nil {
					k, hasK = exactValue(bin.Right)
				}
				if !hasK || !isInteger(k) {
					return nil
				}
				offset := k
				if bin.OperatorToken.Kind == ast.KindMinusToken {
					offset = -k
				}
				place := PlaceKeyOf(c, bin.Left)
				if place == nil || exactWindow == nil {
					return nil
				}
				window, ok := exactWindow(bin.Left)
				if !ok || !window.Int {
					return nil
				}
				if isSafeInteger(window.Lo+offset) && isSafeInteger(window.Hi+offset) {
					return &side{Place: *place, Offset: offset, Slack: 0}
				}
				// past the safe range fl(place ± k) can round: the kernel's
				// envelope decider bounds the rounding outward, and the row's
				// bound gives that much back — the guard's fact about the
				// computed double weakens into a fact about the real pair
				if flSlack == nil {
					return nil
				}
				slack, ok := flSlack(window, offset)
				if !ok {
					return nil
				}
				return &side{Place: *place, Offset: offset, Slack: slack}
			}
		}
		place := PlaceKeyOf(c, cursor)
		if place == nil {
			return nil
		}
		return &side{Place: *place, Offset: 0, Slack: 0}
	}

	readLeaf := func(e *ast.Node, negated bool) {
		if !ast.IsBinaryExpression(e) {
			return
		}
		bin := e.AsBinaryExpression()
		kind := bin.OperatorToken.Kind
		var held shape
		switch kind {
		case ast.KindLessThanToken:
			held = shape{LowOnLeft: true, Strict: true}
		case ast.KindLessThanEqualsToken:
			held = shape{LowOnLeft: true, Strict: false}
		case ast.KindGreaterThanToken:
			held = shape{LowOnLeft: false, Strict: true}
		case ast.KindGreaterThanEqualsToken:
			held = shape{LowOnLeft: false, Strict: false}
		default:
			return
		}
		// the refuted comparison flips: ¬(a < b) is a ≥ b, ¬(a ≤ b) is
		// a > b — sound only of real pairs, so the gate below applies
		effective := held
		if negated {
			effective = shape{LowOnLeft: !held.LowOnLeft, Strict: !held.Strict}
		}
		if negated && (real == nil || !real(bin.Left) || !real(bin.Right)) {
			return
		}
		// a DIFFERENCE of two places tested against an exact number
		// (`if (now - dur < MIN) return`): the guard spoke about the
		// computed double, and the consumption recomputes it — a
		// computed row, whose window intersects the same double
		differenceRow := func(sideExpr *ast.Node, other *ast.Node, lowIsDifference bool) bool {
			cursor := sideExpr
			for ast.IsParenthesizedExpression(cursor) {
				cursor = cursor.AsParenthesizedExpression().Expression
			}
			if !ast.IsBinaryExpression(cursor) || cursor.AsBinaryExpression().OperatorToken.Kind != ast.KindMinusToken {
				return false
			}
			minusBin := cursor.AsBinaryExpression()
			a := PlaceKeyOf(c, minusBin.Left)
			if a == nil {
				return false
			}
			b := PlaceKeyOf(c, minusBin.Right)
			if b == nil {
				return false
			}
			var k float64
			var hasK bool
			if ast.IsNumericLiteral(other) {
				k, hasK = numericLiteralValue(other), true
			} else if exactValue != nil {
				k, hasK = exactValue(other)
			}
			if !hasK || math.IsInf(k, 0) || math.IsNaN(k) {
				return false
			}
			if !StableIn(*a, []*ast.Node{scope}, fn) || !StableIn(*b, []*ast.Node{scope}, fn) {
				return false
			}
			// fl(a−b) REL k: the high side vouches fl(a−b) ≥ k, spelled on
			// (a,b); the low side vouches fl(a−b) ≤ k, which is exactly
			// fl(b−a) ≥ −k (negation of a double is exact), spelled on (b,a)
			if lowIsDifference {
				rows = append(rows, DifferenceConstraint{Minuend: *b, Subtrahend: *a, Bound: -k, Strict: effective.Strict, Computed: true})
			} else {
				rows = append(rows, DifferenceConstraint{Minuend: *a, Subtrahend: *b, Bound: k, Strict: effective.Strict, Computed: true})
			}
			return true
		}
		// `(a - b) REL K`: which side is LOW under the held shape says
		// which direction the vouched window points
		if differenceRow(bin.Left, bin.Right, effective.LowOnLeft) {
			return
		}
		if differenceRow(bin.Right, bin.Left, !effective.LowOnLeft) {
			return
		}
		left := sideOf(bin.Left)
		if left == nil {
			return
		}
		right := sideOf(bin.Right)
		if right == nil {
			return
		}
		if !StableIn(left.Place, []*ast.Node{scope}, fn) || !StableIn(right.Place, []*ast.Node{scope}, fn) {
			return
		}
		// low + a REL high + b vouches high − low ≥ a − b; each side's
		// computed value sits within its slack of the real one, so the
		// vouched bound gives the total slack back
		var bound float64
		if effective.LowOnLeft {
			bound = left.Offset - right.Offset
		} else {
			bound = right.Offset - left.Offset
		}
		bound -= left.Slack + right.Slack
		if !isSafeInteger(bound) {
			return
		}
		if effective.LowOnLeft {
			rows = append(rows, DifferenceConstraint{Minuend: right.Place, Subtrahend: left.Place, Bound: bound, Strict: effective.Strict})
		} else {
			rows = append(rows, DifferenceConstraint{Minuend: left.Place, Subtrahend: right.Place, Bound: bound, Strict: effective.Strict})
		}
	}

	// rows are conjunctive facts: an AND contributes both sides, an
	// OR proves neither side alone — the shared tree resolves the
	// connectives and De Morgan once (conditiontree package)
	for _, leaf := range conditiontree.ConjunctiveLeaves(conditiontree.ConditionTreeOf(condition, negatedAtRoot)) {
		readLeaf(leaf.Test, leaf.Negated)
	}
	return rows
}

// isInteger mirrors Number.isInteger: finite and equal to its own
// truncation.
func isInteger(v float64) bool {
	return !math.IsInf(v, 0) && !math.IsNaN(v) && v == math.Trunc(v)
}

// isSafeInteger mirrors Number.isSafeInteger: an integer within
// ±(2^53 - 1).
func isSafeInteger(v float64) bool {
	const maxSafeInteger = 9007199254740991
	return isInteger(v) && v >= -maxSafeInteger && v <= maxSafeInteger
}
