// Sum rows a condition vouches: computed doubles of two-place sums
// against an anchor place, plus the loop-exit side channel for those
// rows.

package dataflowfacts

import (
	"strconv"
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/conditiontree"
)

// SumPlaces is a two-place sum with an optional trailing ± literal
// (`offset + length`, `offset + length - 1`), read as places.
type SumPlaces struct {
	Terms  [2]PlaceKey
	Offset float64
}

// SumPlacesOf reads a two-place sum with an optional trailing ± literal
// (`offset + length`, `offset + length - 1`), as places. Nil when the
// expression is not one.
func SumPlacesOf(c *checker.Checker, e *ast.Node) *SumPlaces {
	cursor := e
	for ast.IsParenthesizedExpression(cursor) {
		cursor = cursor.AsParenthesizedExpression().Expression
	}
	offset := 0.0
	if ast.IsBinaryExpression(cursor) {
		bin := cursor.AsBinaryExpression()
		if (bin.OperatorToken.Kind == ast.KindPlusToken ||
			bin.OperatorToken.Kind == ast.KindMinusToken) &&
			ast.IsNumericLiteral(bin.Right) {
			k, err := strconv.ParseFloat(bin.Right.Text(), 64)
			if err != nil || k != float64(int64(k)) {
				return nil
			}
			if bin.OperatorToken.Kind == ast.KindMinusToken {
				offset = -k
			} else {
				offset = k
			}
			cursor = bin.Left
			for ast.IsParenthesizedExpression(cursor) {
				cursor = cursor.AsParenthesizedExpression().Expression
			}
		}
	}
	if !ast.IsBinaryExpression(cursor) || cursor.AsBinaryExpression().OperatorToken.Kind != ast.KindPlusToken {
		return nil
	}
	sumBin := cursor.AsBinaryExpression()
	a := PlaceKeyOf(c, sumBin.Left)
	if a == nil {
		return nil
	}
	b := PlaceKeyOf(c, sumBin.Right)
	if b == nil {
		return nil
	}
	return &SumPlaces{Terms: [2]PlaceKey{*a, *b}, Offset: offset}
}

// SumConstraintsOf is the sum rows a condition vouches — the TRUE
// side, or with `negatedAtRoot` the FALSE side (gated on real
// operands, as the order rows are). Places must stay stable through
// `scope`.
func SumConstraintsOf(
	c *checker.Checker,
	condition *ast.Node,
	scope *ast.Node,
	negatedAtRoot bool,
	real func(e *ast.Node) bool,
) []SumConstraint {
	var rows []SumConstraint
	fn := EnclosingFunctionOf(condition)

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
		effective := held
		if negated {
			effective = shape{LowOnLeft: !held.LowOnLeft, Strict: !held.Strict}
		}
		if negated && (real == nil || !real(bin.Left) || !real(bin.Right)) {
			return
		}
		record := func(sumExpression *ast.Node, anchorExpression *ast.Node, sumIsLow bool) {
			sum := SumPlacesOf(c, sumExpression)
			if sum == nil {
				return
			}
			anchor := PlaceKeyOf(c, anchorExpression)
			if anchor == nil {
				return
			}
			if !StableIn(sum.Terms[0], []*ast.Node{scope}, fn) ||
				!StableIn(sum.Terms[1], []*ast.Node{scope}, fn) ||
				!StableIn(*anchor, []*ast.Node{scope}, fn) {
				return
			}
			rows = append(rows, SumConstraint{
				Terms:     sum.Terms,
				Offset:    sum.Offset,
				Anchor:    *anchor,
				SumAtMost: sumIsLow,
				Strict:    effective.Strict,
			})
		}
		record(bin.Left, bin.Right, effective.LowOnLeft)
		record(bin.Right, bin.Left, !effective.LowOnLeft)
	}

	for _, leaf := range conditiontree.ConjunctiveLeaves(conditiontree.ConditionTreeOf(condition, negatedAtRoot)) {
		readLeaf(leaf.Test, leaf.Negated)
	}
	return rows
}

// sumExitConstraintsMu guards sumExitConstraintsCache.
//
// The TS source keys this store with a WeakMap<ts.Node, …>; Go has no
// weak maps, so this substitutes a regular map guarded by a mutex.
// Functionally identical per program: entries live exactly as long as
// the program that produced their nodes is in use by this port.
var (
	sumExitConstraintsMu    sync.Mutex
	sumExitConstraintsCache = map[*ast.Node][]SumConstraint{}
)

// NoteSumExitConstraints records the sum rows a statement's exit hands
// to its continuation.
func NoteSumExitConstraints(statement *ast.Node, rows []SumConstraint) {
	sumExitConstraintsMu.Lock()
	sumExitConstraintsCache[statement] = rows
	sumExitConstraintsMu.Unlock()
}

// SumExitConstraintsOf reads the sum rows recorded for a statement, if
// any.
func SumExitConstraintsOf(statement *ast.Node) ([]SumConstraint, bool) {
	sumExitConstraintsMu.Lock()
	rows, ok := sumExitConstraintsCache[statement]
	sumExitConstraintsMu.Unlock()
	return rows, ok
}
