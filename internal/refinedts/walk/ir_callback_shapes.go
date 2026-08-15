// split from ir_callback_summary.go — the SYNTAX READERS: the `xs.m(cb)`
// shapes the lowering recognizes, reduce's two-argument spelling, the
// Promise.all(map) wrapper, and the assignment target's name
package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
)

// collectionCall is one recognized `xs.m(cb)` shape: the receiver's
// spelled name, the method, and the single callback argument.
type collectionCall struct {
	Receiver string
	Method   string
	Callback *ast.Node
}

// collectionCallOf reads `xs.m(cb)` with exactly one argument and a
// plain (non-optional) receiver identifier — the only receiver shape
// the two-slot flattening resolves.
func collectionCallOf(node *ast.Node) (collectionCall, bool) {
	head := Unwrapped(node)
	if !ast.IsCallExpression(head) {
		return collectionCall{}, false
	}
	call := head.AsCallExpression()
	if call.QuestionDotToken != nil {
		return collectionCall{}, false
	}
	access := Unwrapped(call.Expression)
	if !ast.IsPropertyAccessExpression(access) {
		return collectionCall{}, false
	}
	property := access.AsPropertyAccessExpression()
	if property.QuestionDotToken != nil {
		return collectionCall{}, false
	}
	if !ast.IsIdentifier(property.Expression) || !ast.IsIdentifier(property.Name()) {
		return collectionCall{}, false
	}
	if call.Arguments == nil || len(call.Arguments.Nodes) != 1 {
		return collectionCall{}, false
	}
	argument := call.Arguments.Nodes[0]
	if ast.IsSpreadElement(argument) {
		return collectionCall{}, false
	}
	return collectionCall{
		Receiver: property.Expression.Text(),
		Method:   property.Name().Text(),
		Callback: argument,
	}, true
}

// reduceCallOf reads `xs.reduce(cb, seed)` — the TWO-argument shape
// collectionCallOf refuses, since every other recognized method takes
// exactly one. The receiver rules are the same ones collectionCallOf
// applies: a plain non-optional identifier receiver, a non-optional
// method step, and a callback that is not a spread.
//
// The SEED is answered beside the call rather than folded in, because
// what fills the accumulator entry is a caller-side effect the entry
// layout builds; the reader's job is only to hand back the node.
//
// The one-argument form `xs.reduce(cb)` — no seed, the first element
// standing in — is NOT read here. Its accumulator starts as an element
// rather than a value the site can name, and reduce over an empty array
// with no seed THROWS, which is a control-flow outcome this lowering
// does not model. The two-argument form has neither problem.
func reduceCallOf(node *ast.Node) (source collectionCall, seed *ast.Node, ok bool) {
	head := Unwrapped(node)
	if !ast.IsCallExpression(head) {
		return collectionCall{}, nil, false
	}
	call := head.AsCallExpression()
	if call.QuestionDotToken != nil {
		return collectionCall{}, nil, false
	}
	access := Unwrapped(call.Expression)
	if !ast.IsPropertyAccessExpression(access) {
		return collectionCall{}, nil, false
	}
	property := access.AsPropertyAccessExpression()
	if property.QuestionDotToken != nil {
		return collectionCall{}, nil, false
	}
	if !ast.IsIdentifier(property.Expression) || !ast.IsIdentifier(property.Name()) {
		return collectionCall{}, nil, false
	}
	if property.Name().Text() != "reduce" {
		return collectionCall{}, nil, false
	}
	if call.Arguments == nil || len(call.Arguments.Nodes) != 2 {
		return collectionCall{}, nil, false
	}
	callback := call.Arguments.Nodes[0]
	seedNode := call.Arguments.Nodes[1]
	if ast.IsSpreadElement(callback) || ast.IsSpreadElement(seedNode) {
		return collectionCall{}, nil, false
	}
	return collectionCall{
		Receiver: property.Expression.Text(),
		Method:   "reduce",
		Callback: callback,
	}, seedNode, true
}

// promiseAllMapOf reads `Promise.all(xs.map(cb))` — through an await
// where one wraps it — and answers the inner map call.
//
// The await adds NOTHING. A lowered async body's #ret already holds the
// settled inner value (the ret-as-inner convention), so the array
// Promise.all settles to has exactly the elements the map lowering
// already wrote into the result's element slot: cb's ret. Awaiting the
// whole is the identity on that slot.
func promiseAllMapOf(expression *ast.Node) (collectionCall, bool) {
	head := Unwrapped(expression)
	if ast.IsAwaitExpression(head) {
		head = Unwrapped(head.AsAwaitExpression().Expression)
	}
	if !ast.IsCallExpression(head) {
		return collectionCall{}, false
	}
	call := head.AsCallExpression()
	access := Unwrapped(call.Expression)
	if !ast.IsPropertyAccessExpression(access) {
		return collectionCall{}, false
	}
	property := access.AsPropertyAccessExpression()
	if !ast.IsIdentifier(property.Expression) || property.Expression.Text() != "Promise" {
		return collectionCall{}, false
	}
	if !ast.IsIdentifier(property.Name()) || property.Name().Text() != "all" {
		return collectionCall{}, false
	}
	if call.Arguments == nil || len(call.Arguments.Nodes) != 1 {
		return collectionCall{}, false
	}
	inner, innerOk := collectionCallOf(call.Arguments.Nodes[0])
	if !innerOk || inner.Method != "map" {
		return collectionCall{}, false
	}
	return inner, true
}

// callbackAssignmentNameOf is the `ys = e` / `const ys = e` shape read
// for its target NAME rather than its slot.
//
// callAssignmentShapeOf (ir_summary_call.go) reads the same two shapes
// but resolves the target through IndexOf, which answers a SCALAR slot
// — a flattened array target has no such slot, so that reader declines
// exactly the statements this one has to admit. The two readers share
// the shapes and differ only in what they resolve the target to; this
// one is used where the target is an array.
func callbackAssignmentNameOf(context *LoweringContext, statement *ast.Node) (name string, rhs *ast.Node, ok bool) {
	if ast.IsVariableStatement(statement) {
		declarations := statement.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes
		if len(declarations) != 1 {
			return "", nil, false
		}
		d := declarations[0].AsVariableDeclaration()
		if !ast.IsIdentifier(d.Name()) || d.Initializer == nil {
			return "", nil, false
		}
		return d.Name().Text(), d.Initializer, true
	}
	if !ast.IsExpressionStatement(statement) {
		return "", nil, false
	}
	e := Unwrapped(statement.AsExpressionStatement().Expression)
	if !ast.IsBinaryExpression(e) {
		return "", nil, false
	}
	bin := e.AsBinaryExpression()
	if bin.OperatorToken.Kind != ast.KindEqualsToken {
		return "", nil, false
	}
	left := Unwrapped(bin.Left)
	if !ast.IsIdentifier(left) {
		return "", nil, false
	}
	return left.Text(), bin.Right, true
}
