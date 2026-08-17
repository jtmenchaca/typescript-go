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
	return promiseArrayLiteralArgumentOf(node, "all")
}

// promiseRaceArrayOf is the ARRAY LITERAL argument of `Promise.race([…])`
// — its elements, or (nil, false) for any other shape. Same recognition
// as promiseAllArrayOf, one property name apart.
func promiseRaceArrayOf(node *ast.Node) ([]*ast.Node, bool) {
	return promiseArrayLiteralArgumentOf(node, "race")
}

// promiseArrayLiteralArgumentOf is the one array-literal recognition
// promiseAllArrayOf and promiseRaceArrayOf share: `Promise.<property>([…])`
// with a single array-literal argument holding no spread and no elision.
func promiseArrayLiteralArgumentOf(node *ast.Node, property string) ([]*ast.Node, bool) {
	if !ast.IsCallExpression(node) {
		return nil, false
	}
	call := node.AsCallExpression()
	access := Unwrapped(call.Expression)
	if !ast.IsPropertyAccessExpression(access) {
		return nil, false
	}
	pa := access.AsPropertyAccessExpression()
	if pa.QuestionDotToken != nil {
		return nil, false
	}
	if !ast.IsIdentifier(pa.Expression) || pa.Expression.Text() != "Promise" {
		return nil, false
	}
	if !ast.IsIdentifier(pa.Name()) || pa.Name().Text() != property {
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

// promiseRaceJoinEffect is `await Promise.race([a, b, …])` where every
// element's own reading is admitted: race settles to whichever element
// settles first (ECMA-262 sec-performpromiserace — each element is
// wired through `then` straight to the shared capability's resolve/
// reject, so the first to fire wins), so the sound answer is the JOIN
// of every element's reading — the value could be any one of them.
//
// A call element hoists through HoistCallEffect (the same door
// `f(x)` inside any other expression takes) into its own temp-slot
// call statement, appended to context.Hoisted for the statement route
// to flush ahead of the join; a scalar/literal element reads through
// RhsEffect exactly as a Promise.resolve argument does.
//
// An EMPTY array declines: per sec-promise.race's own note, a race over
// no elements never settles at all — there is no completion to give a
// slot, so the floor is the honest answer, not a join of nothing.
func promiseRaceJoinEffect(context *LoweringContext, elements []*ast.Node) (kernelbridge.LoopEffect, bool) {
	if len(elements) == 0 {
		return kernelbridge.LoopEffect{}, false
	}
	var joined kernelbridge.LoopEffect
	for index, element := range elements {
		unwrapped := Unwrapped(element)
		effect, ok := HoistCallEffect(context, unwrapped)
		if !ok {
			effect, ok = RhsEffect(context, BindingKindUnknown, unwrapped)
		}
		if !ok {
			return kernelbridge.LoopEffect{}, false
		}
		if index == 0 {
			joined = effect
			continue
		}
		joined = joinEffect(joined, effect)
	}
	// a ONE-element race has no join to build — the same degenerate case
	// the array-literal fold handles (ir_array_declarations.go): the
	// value IS that element's, exactly, so it rides the verbatim copy.
	// Every caller of awaitIdentityEffect (whose join this feeds)
	// consumes the result as a bare assign, never as another effect's
	// operand.
	if len(elements) == 1 {
		joined = asVarStateEffect(joined)
	}
	return joined, true
}
