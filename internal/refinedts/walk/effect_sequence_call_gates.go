// split from effect_expression.go — syntax gates on calls inside a sequence

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
)

// isAwaitedCallShape is `await f(…)` — the one wrapper the hoist route
// peels, spelled here so the sequence reader can ask before handing the
// node over.
func isAwaitedCallShape(e *ast.Node) bool {
	operand, isAwait := AwaitedOperandOf(e)
	return isAwait && ast.IsCallExpression(operand)
}

// sliceArgumentsArePlain is whether a slice call's cut positions are
// expressions this reader can leave alone: at most two of them, none
// spelling a call, an await, or a spread.
//
// The positions' VALUES are never read, and they never need to be. The
// kernel's gated slice row holds for every pair of endpoints — under
// the BMP gate each cut lands on a scalar boundary whatever the indices
// were, so the piece is a contiguous subsequence and the drawn-from
// closure applies. What the gate here rules out is a position that
// COMPUTES: a call or an await inside an argument would have to hoist
// to a temp slot ahead of this statement, and the hoist route owns that
// reordering decision (ir_call_hoist.go). Rather than reorder behind
// its back, this reader declines and the ordinary decline path runs.
//
// A spread declines for a different reason: `s.slice(...xs)` supplies
// an unknown NUMBER of arguments, so the call may not be the two-index
// form the row is written for.
func sliceArgumentsArePlain(call *ast.CallExpression) bool {
	if call.Arguments == nil {
		return true
	}
	if len(call.Arguments.Nodes) > 2 {
		return false
	}
	for _, argument := range call.Arguments.Nodes {
		if ast.IsSpreadElement(argument) {
			return false
		}
		computes := false
		var visit func(node *ast.Node) bool
		visit = func(node *ast.Node) bool {
			if node == nil || computes {
				return true
			}
			if ast.IsCallExpression(node) || ast.IsNewExpression(node) ||
				ast.IsAwaitExpression(node) || ast.IsTaggedTemplateExpression(node) {
				computes = true
				return true
			}
			node.ForEachChild(visit)
			return false
		}
		visit(argument)
		if computes {
			return false
		}
	}
	return true
}
