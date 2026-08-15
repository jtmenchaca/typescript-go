// split from ir_loop_stmts.go — the transfer containment gate: whether
// every break and continue in the loop's subtree stays inside it

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
)

// transfersStayInside is whether every `break` and `continue` in a
// loop's subtree targets something INSIDE that subtree — the one thing
// this form must be right about, since the ordinary statement walk has
// no reading for either and lets both fall through.
//
// The containment test is havoc_floor's containedTransfer, unchanged and
// against this loop STATEMENT as the root, so the two routes can never
// disagree about which transfers leave: a bare `break` finds this loop
// (or a switch or loop nested in its body) as its target and is
// contained; a `break outer` finds its labelled statement only if that
// label sits inside the subtree.
//
// A nested FUNCTION's transfers are its own business — a `break` inside
// a callback leaves the callback's loop, not this one — so the scan
// stops at a function or class boundary, exactly as the floor's does.
func transfersStayInside(statement *ast.Node) bool {
	inside := true
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if !inside {
			return true
		}
		if ast.IsBreakStatement(node) || ast.IsContinueStatement(node) {
			if !containedTransfer(node, statement) {
				inside = false
			}
			return true
		}
		if ast.IsFunctionLike(node) || ast.IsClassLike(node) {
			return false
		}
		node.ForEachChild(visit)
		return false
	}
	visit(statement)
	return inside
}
