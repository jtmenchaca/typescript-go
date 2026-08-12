// Sum rows a condition vouches: computed doubles of two-place sums
// against an anchor place, plus the loop-exit side channel for those
// rows.
//
// BLOCKED: sumConstraintsOf needs narrowing/condition_tree.ts
// (conditionTreeOf, conjunctiveLeaves) — not yet ported (dataflow_facts
// precedes narrowing in the port order). sumPlacesOf and the exit-row
// side channel, which do not need it, are ported below.
package dataflowfacts

import (
	"strconv"
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
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
