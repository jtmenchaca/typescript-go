// split from ir_opaque_havoc.go — what a statement's subtree does

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
)

// StatementRunsCode answers whether a statement's subtree can execute
// a callee — a call, a construction, an await, a yield, a tagged
// template. The capture-havoc bracketing reads it: any such execution
// may run a stored closure. (A getter behind a plain property read
// still runs code this test does not see — the standing gap every
// syntactic write/call test in this package accepts.)
func StatementRunsCode(node *ast.Node) bool {
	if node == nil {
		return false
	}
	switch node.Kind {
	case ast.KindCallExpression, ast.KindNewExpression, ast.KindAwaitExpression,
		ast.KindYieldExpression, ast.KindTaggedTemplateExpression:
		return true
	}
	runs := false
	node.ForEachChild(func(child *ast.Node) bool {
		if runs {
			return true
		}
		if StatementRunsCode(child) {
			runs = true
			return true
		}
		return false
	})
	return runs
}

// StatementStoresElement answers whether a statement's subtree stores
// through an element access — `o[k] = v`, `o[k]++`, `delete o[k]`. In
// capture-havoc mode a computed store on the receiver moves a field
// nothing names, so such statements bracket the havoc set exactly as
// code-running ones do. The receiver is not distinguished here — an
// element store on any object triggers the bracket, which only costs a
// few redundant unknown-assigns.
func StatementStoresElement(node *ast.Node) bool {
	if node == nil {
		return false
	}
	if ast.IsBinaryExpression(node) {
		bin := node.AsBinaryExpression()
		if bin.OperatorToken.Kind >= ast.KindFirstAssignment &&
			bin.OperatorToken.Kind <= ast.KindLastAssignment &&
			ast.IsElementAccessExpression(Unwrapped(bin.Left)) {
			return true
		}
	}
	if ast.IsPrefixUnaryExpression(node) {
		unary := node.AsPrefixUnaryExpression()
		if (unary.Operator == ast.KindPlusPlusToken || unary.Operator == ast.KindMinusMinusToken) &&
			ast.IsElementAccessExpression(Unwrapped(unary.Operand)) {
			return true
		}
	}
	if ast.IsPostfixUnaryExpression(node) {
		unary := node.AsPostfixUnaryExpression()
		if (unary.Operator == ast.KindPlusPlusToken || unary.Operator == ast.KindMinusMinusToken) &&
			ast.IsElementAccessExpression(Unwrapped(unary.Operand)) {
			return true
		}
	}
	if ast.IsDeleteExpression(node) &&
		ast.IsElementAccessExpression(Unwrapped(node.AsDeleteExpression().Expression)) {
		return true
	}
	stores := false
	node.ForEachChild(func(child *ast.Node) bool {
		if stores {
			return true
		}
		if StatementStoresElement(child) {
			stores = true
			return true
		}
		return false
	})
	return stores
}
