// split from ir_await.go — the Promise.all shapes: the statement
// sequencing and the array-literal recognition behind it.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// promiseAllStatementOf is `await Promise.all([f(a), g(b), …])` as a
// STATEMENT — the value unused — lowered as the calls IN SEQUENCE, each
// a call statement with no ret target.
//
// Sequencing is sound for the concurrent original because a lowered
// effect rides ONLY through a call statement's rets into distinct target
// slots: no lowered call reads or writes a slot another lowered call
// touches, so any interleaving of the concurrent runs computes the same
// exit states as running them one after another. (Only scalar arguments
// lower into calls today — a record or array argument declines the call
// before it reaches here — so there is no shared reference for one call
// to observe another's write through.)
//
// Any OTHER Promise.all shape — a value that is used, a `.map` argument,
// a non-literal array, a non-call element — declines whole.
func promiseAllStatementOf(context *LoweringContext, statement *ast.Node) ([]kernelbridge.IrStatement, bool) {
	if !ast.IsExpressionStatement(statement) {
		return nil, false
	}
	operand, isAwait := AwaitedOperandOf(statement.AsExpressionStatement().Expression)
	if !isAwait {
		return nil, false
	}
	elements, isPromiseAll := promiseAllArrayOf(operand)
	if !isPromiseAll {
		return nil, false
	}
	var out []kernelbridge.IrStatement
	for _, element := range elements {
		call := Unwrapped(element)
		if !ast.IsCallExpression(call) {
			return nil, false
		}
		lowered, ok := SummaryCallOrHavoc(context, call, -1)
		if !ok {
			return nil, false
		}
		out = append(out, lowered...)
	}
	return out, true
}

// promiseAllArrayOf is the ARRAY LITERAL argument of `Promise.all([…])`
// — its elements, or (nil, false) for any other shape. A spread, a named
// array, a `.map` result, or a second argument all decline.
func promiseAllArrayOf(node *ast.Node) ([]*ast.Node, bool) {
	if !ast.IsCallExpression(node) {
		return nil, false
	}
	call := node.AsCallExpression()
	access := Unwrapped(call.Expression)
	if !ast.IsPropertyAccessExpression(access) {
		return nil, false
	}
	property := access.AsPropertyAccessExpression()
	if property.QuestionDotToken != nil {
		return nil, false
	}
	if !ast.IsIdentifier(property.Expression) || property.Expression.Text() != "Promise" {
		return nil, false
	}
	if !ast.IsIdentifier(property.Name()) || property.Name().Text() != "all" {
		return nil, false
	}
	if call.Arguments == nil || len(call.Arguments.Nodes) != 1 {
		return nil, false
	}
	literal := Unwrapped(call.Arguments.Nodes[0])
	if !ast.IsArrayLiteralExpression(literal) {
		return nil, false
	}
	for _, element := range literal.AsArrayLiteralExpression().Elements.Nodes {
		if ast.IsSpreadElement(element) || ast.IsOmittedExpression(element) {
			return nil, false
		}
	}
	return literal.AsArrayLiteralExpression().Elements.Nodes, true
}
