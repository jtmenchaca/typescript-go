// from evaluation/promise_builtin_models.ts
//
// The Promise statics. Promise.resolve(x): PromiseResolve fulfills
// with a non-thenable x unchanged (sec-promise.resolve), and a promise
// argument is adopted, never double-wrapped. Only kinds that can never
// be thenables wrap — a values word, a set-known real, absence, NaN.
// The combinators read one law each off the vendored spec: all is the
// fulfillment tuple in element order (sec-promise.all), race and any
// settle with some one element's own value (sec-performpromiserace,
// sec-promise.any), allSettled is the state-snapshot array
// (sec-performpromiseallsettled). Total-or-decline throughout: a field
// whose elements are not all in view answers the walk's residue.
//
// The instance methods (then/catch/finally) live in
// promise_instance_models.go; the constructor's executor in
// promise_constructor_models.go.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// readPromiseResolve is readPromiseResolve in the TS source:
// Promise.resolve when Promise resolves to the default library. Nil
// when the call is not that form.
func readPromiseResolve(ctx *FlowContext, env Env, e *ast.Node) *abstractdomain.AbstractValue {
	call := e.AsCallExpression()
	if !ast.IsPropertyAccessExpression(call.Expression) {
		return nil
	}
	pa := call.Expression.AsPropertyAccessExpression()
	var argCount int
	if call.Arguments != nil {
		argCount = len(call.Arguments.Nodes)
	}
	if !(ast.IsIdentifier(pa.Expression) && pa.Expression.Text() == "Promise" &&
		pa.Name().Text() == "resolve" && resolvesToDefaultLib(ctx, pa.Expression) && argCount == 1) {
		return nil
	}
	inner := evaluateExpression(ctx, env, call.Arguments.Nodes[0])
	if inner.Kind == abstractdomain.KindPromise {
		return &inner
	}
	if inner.Kind == abstractdomain.KindValues || inner.Kind == abstractdomain.KindSet ||
		inner.Kind == abstractdomain.KindUndef || inner.Kind == abstractdomain.KindNaN {
		out := abstractdomain.AbstractValue{Kind: abstractdomain.KindPromise, Inner: &inner}
		return &out
	}
	out := silence.Residue()
	return &out
}

// promiseSettledValueOf is the value a position settles to when its
// element fulfills: a walk-built promise answers its inner value —
// the wrapper's trust rides in, the same composition EvaluateAwait's
// unwrap takes — and a value whose kind rules out a callable `then`
// answers itself (sec-promise-resolve: a non-thenable fulfills
// unchanged). Anything else — an opaque value, an object that could
// hide a `then` — answers false, and the caller declines whole.
func promiseSettledValueOf(value abstractdomain.AbstractValue) (abstractdomain.AbstractValue, bool) {
	if value.Kind == abstractdomain.KindPromise && value.Inner != nil {
		return abstractdomain.AtTrustLevel(
			*value.Inner,
			abstractdomain.MinTrustLevel(abstractdomain.TrustLevelOf(value), abstractdomain.TrustLevelOf(*value.Inner)),
		), true
	}
	if CannotBeThenable(value) {
		return value, true
	}
	return abstractdomain.AbstractValue{}, false
}

// promiseWrapping puts the Promise wrapper on a settled value — the
// walk's own promise build, the same shape readPromiseResolve writes.
// A value that is already a promise stays one promise deep
// (sec-performpromisethen: a handler's thenable result is adopted,
// never nested).
func promiseWrapping(settled abstractdomain.AbstractValue) abstractdomain.AbstractValue {
	if settled.Kind == abstractdomain.KindPromise {
		return settled
	}
	inner := settled
	return abstractdomain.AbstractValue{Kind: abstractdomain.KindPromise, Inner: &inner}
}

// promiseStaticCombinatorOf is the `Promise.<combinator>(field)` call
// shape — all, race, any, or allSettled on the default-library
// Promise, with exactly one argument. The same property-access shape
// readPromiseResolve reads, for the four combinator names.
func promiseStaticCombinatorOf(ctx *FlowContext, e *ast.Node) (method string, argument *ast.Node, ok bool) {
	call := e.AsCallExpression()
	if !ast.IsPropertyAccessExpression(call.Expression) {
		return "", nil, false
	}
	pa := call.Expression.AsPropertyAccessExpression()
	name := pa.Name().Text()
	if name != "all" && name != "race" && name != "any" && name != "allSettled" {
		return "", nil, false
	}
	if !ast.IsIdentifier(pa.Expression) || pa.Expression.Text() != "Promise" ||
		!resolvesToDefaultLib(ctx, pa.Expression) {
		return "", nil, false
	}
	if call.Arguments == nil || len(call.Arguments.Nodes) != 1 {
		return "", nil, false
	}
	return name, call.Arguments.Nodes[0], true
}

// promiseCombinatorItemsOf is the element list a combinator reads its
// field from. A plain array-literal argument answers its element
// nodes with each one's evaluated value — the per-element nodes are
// what the allSettled gate reads — and any other argument shape
// answers the items of its evaluated value when that value is an
// exact sequence. The combinators drain the whole iterable up front
// (sec-promise.all step 5 GetIterator, and kin), so every element
// evaluates exactly once here whatever the caller then answers.
// False when the field's items are not all in view.
func promiseCombinatorItemsOf(ctx *FlowContext, env Env, argumentNode *ast.Node) (items []abstractdomain.AbstractValue, elements []*ast.Node, ok bool) {
	head := Unwrapped(argumentNode)
	if ast.IsArrayLiteralExpression(head) {
		nodes := head.AsArrayLiteralExpression().Elements.Nodes
		plain := true
		for _, element := range nodes {
			if ast.IsOmittedExpression(element) || ast.IsSpreadElement(element) {
				plain = false
				break
			}
		}
		if plain {
			items = make([]abstractdomain.AbstractValue, len(nodes))
			for i, element := range nodes {
				items[i] = evaluateExpression(ctx, env, element)
			}
			return items, nodes, true
		}
	}
	value := evaluateExpression(ctx, env, argumentNode)
	if value.Kind == abstractdomain.KindList {
		return value.Items, nil, true
	}
	if value.Kind == abstractdomain.KindValues && value.KindTag == abstractdomain.PrimitiveArray {
		items = make([]abstractdomain.AbstractValue, len(value.Values))
		for i, v := range value.Values {
			items[i] = abstractdomain.KnownValues([]float64{v}, abstractdomain.PrimitiveNumber, abstractdomain.TrustLevelOf(value))
		}
		return items, nil, true
	}
	return nil, nil, false
}

// readPromiseStatics reads the four combinators. Nil when the call is
// not a combinator on the default-library Promise; the walk's residue
// when it is one but the field's elements do not all settle in view.
func readPromiseStatics(ctx *FlowContext, env Env, e *ast.Node) *abstractdomain.AbstractValue {
	method, argumentNode, ok := promiseStaticCombinatorOf(ctx, e)
	if !ok {
		return nil
	}
	items, elements, itemsOk := promiseCombinatorItemsOf(ctx, env, argumentNode)
	if !itemsOk {
		out := silence.Residue()
		return &out
	}
	settled := make([]abstractdomain.AbstractValue, len(items))
	for i, item := range items {
		s, sOk := promiseSettledValueOf(item)
		if !sOk {
			out := silence.Residue()
			return &out
		}
		settled[i] = s
	}
	switch method {
	case "all":
		// fulfilled with the array of fulfillment values in element
		// order (sec-promise.all). A member that rejects rejects the
		// whole, which the await reads as the continuation never
		// running — the same over-approximation a throw takes.
		inner := abstractdomain.KnownList(settled, abstractdomain.TrustProved)
		out := abstractdomain.AbstractValue{Kind: abstractdomain.KindPromise, Inner: &inner}
		return &out
	case "race", "any":
		// the settled value is some one element's own —
		// sec-performpromiserace wires each element straight to the
		// result capability, and sec-promise.any fulfills with the
		// first fulfilled member — so the join of every element's
		// settled reading admits whichever wins. An empty field never
		// settles, and there is nothing to claim.
		if len(settled) == 0 {
			out := silence.Residue()
			return &out
		}
		joined := settled[0]
		for _, s := range settled[1:] {
			joined = abstractdomain.JoinKnown(joined, s)
		}
		out := promiseWrapping(joined)
		return &out
	case "allSettled":
		return promiseAllSettledSnapshots(elements, items, settled)
	}
	return nil
}

// promiseAllSettledSnapshots is Promise.allSettled's fulfillment
// array: one state snapshot per element —
// { status: "fulfilled", value } for a fulfilled member,
// { status: "rejected", reason } for a rejected one
// (sec-performpromiseallsettled's fulfilledSteps and rejectedSteps).
// The value key is written only where the element PROVABLY fulfills —
// a non-thenable element, or a literal `Promise.resolve(…)` whose
// wrap the walk itself read — because a member that could reject
// snapshots a reason instead, and unlike the other combinators that
// path still runs the continuation, so the throw over-approximation
// does not cover it. The status key wears the string sort, the
// weaker true claim under both spellings.
func promiseAllSettledSnapshots(elements []*ast.Node, items, settled []abstractdomain.AbstractValue) *abstractdomain.AbstractValue {
	if elements == nil {
		out := silence.Residue()
		return &out
	}
	snapshots := make([]abstractdomain.AbstractValue, len(settled))
	for i := range settled {
		if items[i].Kind == abstractdomain.KindPromise {
			if _, isResolve := promiseResolveArgumentOf(Unwrapped(elements[i])); !isResolve {
				out := silence.Residue()
				return &out
			}
		}
		status := abstractdomain.KnownSet(refinementsets.Strings, nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone)
		snapshots[i] = abstractdomain.KnownObject([]abstractdomain.ObjectKey{
			{Name: "status", Value: status},
			{Name: "value", Value: settled[i]},
		}, nil, true, abstractdomain.TrustSpec, false)
	}
	inner := abstractdomain.KnownList(snapshots, abstractdomain.TrustProved)
	out := abstractdomain.AbstractValue{Kind: abstractdomain.KindPromise, Inner: &inner}
	return &out
}
