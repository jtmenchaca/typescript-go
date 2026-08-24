// split from ir_callback_summary.go — the SYNTAX READERS: the `xs.m(cb)`
// shapes the lowering recognizes, reduce's two-argument spelling, the
// Promise.all(map) wrapper, and the assignment target's name
package walk

import (
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
)

// collectionReceiverPathOf reads a collection call's own RECEIVER
// position — the `xs` of `xs.m(cb)` — admitting either a bare identifier
// (`xs`) or a plain property-access chain rooted at an identifier or
// `this` (`request.samples`, `this.items`), spelled as one dotted string
// exactly as propertyPathOf/PathSlotIndexOf already spell every other
// interior-path slot lookup in this package ("request.samples" is the
// same key arraySlotsOf resolves an array local's "…len"/"…elem" pair
// under). A computed or optional step declines, same as propertyPathOf's
// own rule — neither names a fixed leaf this reader can spell.
//
// Widening the receiver here is a SYNTAX admission only: whether
// "request.samples" actually resolves to an allocated array pair is
// arraySlotsOf's own question at consumption time, decided by whatever
// the array-parameter/local flattening laid out. A receiver this reader
// now admits but that flattening never expanded still declines exactly
// as it does today — this only stops the syntax gate itself from being
// the reason an interior-path receiver's array pair never gets asked
// for.
func collectionReceiverPathOf(node *ast.Node) (string, bool) {
	if ast.IsIdentifier(node) {
		return node.Text(), true
	}
	root, path, ok := propertyPathOf(node)
	if !ok {
		return "", false
	}
	return root + "." + strings.Join(path, "."), true
}

// collectionCall is one recognized `xs.m(cb)` shape: the receiver's
// spelled name, the method, and the single callback argument.
type collectionCall struct {
	Receiver string
	Method   string
	Callback *ast.Node
}

// collectionCallOf reads `xs.m(cb)` with exactly one argument and a
// plain (non-optional) receiver — a bare identifier OR an interior
// property-access chain rooted at one (collectionReceiverPathOf) — the
// receiver shapes the two-slot flattening's own lookup (arraySlotsOf,
// spelled by dotted name) can resolve.
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
	receiver, receiverOk := collectionReceiverPathOf(property.Expression)
	if !receiverOk || !ast.IsIdentifier(property.Name()) {
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
		Receiver: receiver,
		Method:   property.Name().Text(),
		Callback: argument,
	}, true
}

// reduceCallExpressionOf matches the `xs.reduce(...)` receiver shape
// every reduce reader needs — a plain non-optional receiver
// (collectionReceiverPathOf: a bare identifier or an interior path
// rooted at one), a non-optional `.reduce` step — and hands back the
// parsed call plus the receiver's own spelling, leaving the ARGUMENT
// COUNT to each caller. Both reduceCallOf (two arguments, a seed) and
// oneArgumentReduceCallOf (one argument, no seed) start here, since the
// receiver rule the two share is the whole of collectionCallOf's own
// rule minus the argument count.
func reduceCallExpressionOf(node *ast.Node) (call *ast.CallExpression, receiver string, ok bool) {
	head := Unwrapped(node)
	if !ast.IsCallExpression(head) {
		return nil, "", false
	}
	callExpr := head.AsCallExpression()
	if callExpr.QuestionDotToken != nil {
		return nil, "", false
	}
	access := Unwrapped(callExpr.Expression)
	if !ast.IsPropertyAccessExpression(access) {
		return nil, "", false
	}
	property := access.AsPropertyAccessExpression()
	if property.QuestionDotToken != nil {
		return nil, "", false
	}
	receiverPath, receiverOk := collectionReceiverPathOf(property.Expression)
	if !receiverOk || !ast.IsIdentifier(property.Name()) {
		return nil, "", false
	}
	if property.Name().Text() != "reduce" {
		return nil, "", false
	}
	return callExpr, receiverPath, true
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
// The one-argument form `xs.reduce(cb)` — no seed — is read separately,
// by oneArgumentReduceCallOf below: its accumulator starts as an ELEMENT
// rather than a value this reader can name, which is a different shape of
// answer, not a decline.
func reduceCallOf(node *ast.Node) (source collectionCall, seed *ast.Node, ok bool) {
	call, receiver, matched := reduceCallExpressionOf(node)
	if !matched {
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
		Receiver: receiver,
		Method:   "reduce",
		Callback: callback,
	}, seedNode, true
}

// oneArgumentReduceCallOf reads `xs.reduce(cb)` — no seed, the array's
// own first element standing in for the accumulator's starting value
// (sec-array.prototype.reduce, specifications/javascript/spec.html: with no initial
// value, the accumulator is set to the array's element at index 0 and
// the callback runs from index 1).
//
// AN EMPTY ARRAY THROWS on this form (same clause: "If len is 0 and
// initialValue is not present, throw a TypeError exception") — a control-
// flow outcome this lowering does not model as a statement. That is not
// a soundness gap: on an empty array the call throws and no later
// statement in the body runs, so a summary that quantifies only over
// completing runs claims nothing false about the empty-array case. This
// is the same stance the lowering already takes toward every callee that
// may throw (a served call's summary is a claim about the runs that
// return, never about the runs that don't).
//
// The accumulator's START is therefore the source's own ELEMENT slot,
// not a caller-named seed node — there is no seed expression to read.
func oneArgumentReduceCallOf(node *ast.Node) (source collectionCall, ok bool) {
	call, receiver, matched := reduceCallExpressionOf(node)
	if !matched {
		return collectionCall{}, false
	}
	if call.Arguments == nil || len(call.Arguments.Nodes) != 1 {
		return collectionCall{}, false
	}
	callback := call.Arguments.Nodes[0]
	if ast.IsSpreadElement(callback) {
		return collectionCall{}, false
	}
	return collectionCall{
		Receiver: receiver,
		Method:   "reduce",
		Callback: callback,
	}, true
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
