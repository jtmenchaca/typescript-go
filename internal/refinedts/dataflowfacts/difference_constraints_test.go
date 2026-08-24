package dataflowfacts

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
)

// realAlways is the `real` callback the negated-equality rows gate on —
// every fixture here compares plain number places, always real (NaN
// can never reach a place read straight off a number-typed parameter
// in these sources).
func realAlways(e *ast.Node) bool { return true }

// rowHolds reports whether rows contains a row matching minuend,
// subtrahend, bound and strictness exactly — the shape equalityRow
// produces (Bound 0, Strict false) for both directions of a pin.
func rowHolds(rows []DifferenceConstraint, minuend, subtrahend string, bound float64, strict bool) bool {
	for _, row := range rows {
		if row.Minuend.BaseName == minuend && row.Subtrahend.BaseName == subtrahend &&
			row.Bound == bound && row.Strict == strict {
			return true
		}
	}
	return false
}

func TestDifferenceConstraintsOf_HeldEquality(t *testing.T) {
	c, file := checkerFor(t, `
function f(a: number, b: number) {
  if (a === b) {
    return a;
  }
  return 0;
}
`)
	fnDecl := file.Statements.Nodes[0]
	ifStmt := fnDecl.AsFunctionDeclaration().Body.AsBlock().Statements.Nodes[0].AsIfStatement()
	condition := ifStmt.Expression
	scope := ifStmt.ThenStatement

	rows := DifferenceConstraintsOf(c, condition, scope, nil, nil, nil)
	if !rowHolds(rows, "a", "b", 0, false) {
		t.Errorf("a === b did not pin a − b >= 0, got %+v", rows)
	}
	if !rowHolds(rows, "b", "a", 0, false) {
		t.Errorf("a === b did not pin b − a >= 0, got %+v", rows)
	}
}

func TestDifferenceConstraintsOf_HeldInequalityProvesNoOrder(t *testing.T) {
	c, file := checkerFor(t, `
function f(a: number, b: number) {
  if (a !== b) {
    return a;
  }
  return 0;
}
`)
	fnDecl := file.Statements.Nodes[0]
	ifStmt := fnDecl.AsFunctionDeclaration().Body.AsBlock().Statements.Nodes[0].AsIfStatement()
	condition := ifStmt.Expression
	scope := ifStmt.ThenStatement

	rows := DifferenceConstraintsOf(c, condition, scope, nil, nil, nil)
	if len(rows) != 0 {
		t.Errorf("a !== b held alone should prove no order, got %+v", rows)
	}
}

func TestNegatedDifferenceConstraintsOf_RefutedInequalityPinsEquality(t *testing.T) {
	c, file := checkerFor(t, `
function f(a: number, b: number) {
  if (a !== b) {
    return 0;
  }
  return a;
}
`)
	fnDecl := file.Statements.Nodes[0]
	body := fnDecl.AsFunctionDeclaration().Body.AsBlock()
	ifStmt := body.Statements.Nodes[0].AsIfStatement()
	condition := ifStmt.Expression
	// the continuation after the exiting then-branch: the second
	// statement (`return a`), where ¬(a !== b) — i.e. a === b — holds
	scope := body.Statements.Nodes[1]

	rows := NegatedDifferenceConstraintsOf(c, condition, scope, nil, nil, realAlways, nil)
	if !rowHolds(rows, "a", "b", 0, false) {
		t.Errorf("refuted a !== b did not pin a − b >= 0, got %+v", rows)
	}
	if !rowHolds(rows, "b", "a", 0, false) {
		t.Errorf("refuted a !== b did not pin b − a >= 0, got %+v", rows)
	}
}

func TestNegatedDifferenceConstraintsOf_RefutedEqualityProvesNoOrder(t *testing.T) {
	c, file := checkerFor(t, `
function f(a: number, b: number) {
  if (a === b) {
    return 0;
  }
  return a;
}
`)
	fnDecl := file.Statements.Nodes[0]
	body := fnDecl.AsFunctionDeclaration().Body.AsBlock()
	ifStmt := body.Statements.Nodes[0].AsIfStatement()
	condition := ifStmt.Expression
	scope := body.Statements.Nodes[1]

	rows := NegatedDifferenceConstraintsOf(c, condition, scope, nil, nil, realAlways, nil)
	if len(rows) != 0 {
		t.Errorf("refuted a === b should prove no order, got %+v", rows)
	}
}
