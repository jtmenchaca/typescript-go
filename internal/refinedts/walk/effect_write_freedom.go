// split from effect_expression.go — the write/call freedom predicates

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
)

// writeAndCallFree answers whether evaluating the subtree can move any
// state the lowering tracks: no write form (an assignment, ++/--,
// delete) and no code the lowering does not run (a call, a `new`, an
// await, a yield, a tagged template). A getter behind a plain property
// read still runs code this test does not see — the same standing gap
// the ternary's write-free condition and the opaque branch's test
// accept.
//
// The walk descends THROUGH a function literal, so a subtree building a
// closure whose body writes or calls answers false. That reading is
// wrong about evaluation — creating a closure runs none of its body —
// and right about every caller that HANDS THE VALUE OVER, where the
// closure's later run is exactly what may not be lost. The callers that
// only need the evaluation question ask inertValue below.
func writeAndCallFree(node *ast.Node) bool {
	if node == nil {
		return true
	}
	if ast.IsBinaryExpression(node) {
		operator := node.AsBinaryExpression().OperatorToken.Kind
		if operator >= ast.KindFirstAssignment && operator <= ast.KindLastAssignment {
			return false
		}
	}
	if ast.IsPrefixUnaryExpression(node) {
		operator := node.AsPrefixUnaryExpression().Operator
		if operator == ast.KindPlusPlusToken || operator == ast.KindMinusMinusToken {
			return false
		}
	}
	if ast.IsPostfixUnaryExpression(node) {
		operator := node.AsPostfixUnaryExpression().Operator
		if operator == ast.KindPlusPlusToken || operator == ast.KindMinusMinusToken {
			return false
		}
	}
	switch node.Kind {
	case ast.KindDeleteExpression, ast.KindCallExpression, ast.KindNewExpression,
		ast.KindAwaitExpression, ast.KindYieldExpression, ast.KindTaggedTemplateExpression:
		return false
	}
	free := true
	node.ForEachChild(func(child *ast.Node) bool {
		if !writeAndCallFree(child) {
			free = false
			return true
		}
		return false
	})
	return free
}

// inertValue answers writeAndCallFree's question with the function
// boundary drawn where evaluation really draws it: EVALUATING this
// expression moves nothing.
//
// The difference from writeAndCallFree is one rule. The walk STOPS at a
// function or class literal, because creating a closure runs none of its
// body: `{ [APP_GUARD]: guard => this.config.addGlobalGuard(guard) }`
// builds an object holding a function value and calls nothing, and the
// same holds for an arrow in an array literal or a ternary arm. Only
// CALLING the closure runs it, and a call is its own node this predicate
// already catches wherever it is written. `havocEnumerable`
// (ir_opaque_havoc.go:277) draws the boundary at the same place for the
// same question — a nested function's transfers leave IT, not this
// statement — and this predicate now agrees with it.
//
// What the boundary does NOT settle is WHEN the closure runs. Whoever
// receives the value may call it later, and the closure's writes land
// then. So every caller of this predicate must ALSO discharge that
// obligation, which is what closureWritesTracked below is for. Asking
// this one alone would trade a call the lowering does not run for a
// write the lowering does not see, which is the worse of the two.
func inertValue(node *ast.Node) bool {
	if node == nil {
		return true
	}
	if ast.IsFunctionLike(node) || ast.IsClassLike(node) {
		return true
	}
	if ast.IsBinaryExpression(node) {
		operator := node.AsBinaryExpression().OperatorToken.Kind
		if operator >= ast.KindFirstAssignment && operator <= ast.KindLastAssignment {
			return false
		}
	}
	if ast.IsPrefixUnaryExpression(node) {
		operator := node.AsPrefixUnaryExpression().Operator
		if operator == ast.KindPlusPlusToken || operator == ast.KindMinusMinusToken {
			return false
		}
	}
	if ast.IsPostfixUnaryExpression(node) {
		operator := node.AsPostfixUnaryExpression().Operator
		if operator == ast.KindPlusPlusToken || operator == ast.KindMinusMinusToken {
			return false
		}
	}
	switch node.Kind {
	case ast.KindDeleteExpression, ast.KindCallExpression, ast.KindNewExpression,
		ast.KindAwaitExpression, ast.KindYieldExpression, ast.KindTaggedTemplateExpression:
		return false
	}
	free := true
	node.ForEachChild(func(child *ast.Node) bool {
		if !inertValue(child) {
			free = false
			return true
		}
		return false
	})
	return free
}

// ContainsWrite is containsWrite in the TS source: does the subtree
// perform any write? A shape mapped to an opaque effect must be
// write-free, or the lowering's state would miss the write. Shared
// by both lowerings.
func ContainsWrite(node *ast.Node) bool {
	if ast.IsBinaryExpression(node) {
		bin := node.AsBinaryExpression()
		if bin.OperatorToken.Kind >= ast.KindFirstAssignment && bin.OperatorToken.Kind <= ast.KindLastAssignment {
			return true
		}
	}
	if ast.IsPrefixUnaryExpression(node) {
		unary := node.AsPrefixUnaryExpression()
		if unary.Operator == ast.KindPlusPlusToken || unary.Operator == ast.KindMinusMinusToken {
			return true
		}
	}
	if ast.IsPostfixUnaryExpression(node) {
		unary := node.AsPostfixUnaryExpression()
		if unary.Operator == ast.KindPlusPlusToken || unary.Operator == ast.KindMinusMinusToken {
			return true
		}
	}
	found := false
	node.ForEachChild(func(child *ast.Node) bool {
		if !found {
			found = ContainsWrite(child)
		}
		return false
	})
	return found
}
