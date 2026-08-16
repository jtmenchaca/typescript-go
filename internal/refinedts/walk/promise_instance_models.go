// The Promise instance methods — then, catch, finally — on a receiver
// the walk holds as a promise value. Each row reads the vendored
// spec's own settlement law:
//
//   - then(onFulfilled): the derived promise settles with the
//     handler's result on fulfillment (sec-performpromisethen); on
//     rejection it rejects, which the await reads as the continuation
//     never running — the throw over-approximation.
//   - then(onFulfilled, onRejected) and catch(onRejected): whichever
//     channel fires, one handler's result is the settlement — catch is
//     then(undefined, onRejected) exactly (sec-promise.prototype.catch
//     step 2) — so the sound reading is the join. For catch the
//     fulfillment channel forwards the value untouched (the identity
//     substitute of sec-performpromisethen step 3), so the receiver's
//     own inner joins the handler's result.
//   - finally(onFinally): the value thunk forwards the original
//     settlement (sec-promise.prototype.finally steps 6.a.i-v), and
//     the handler's own return is dropped — the receiver rides
//     through, and the handler runs only for its effects.
//
// A rejection HANDLER's argument is the reason, a value this walk
// never names — it binds as opaque. Handlers run under the same
// re-entry guard CallbackResult holds, so a handler reaching its own
// call again answers silence instead of recursing without bottom.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// promiseHandlerOf is the handler a promise method call hands over,
// when the walk can follow its body: an inline arrow or function
// expression, or a name resolving to a function in reach. A bound
// function is NOT followed here — Function.prototype.bind prepends
// arguments, so parameter 0 would no longer wear the settlement.
func promiseHandlerOf(ctx *FlowContext, argument *ast.Node) Callback {
	head := Unwrapped(argument)
	if ast.IsArrowFunction(head) || ast.IsFunctionExpression(head) {
		return head
	}
	return FunctionInReach(ctx, argument)
}

// StandardAnalyzers is the analyzer triple every InlineCallback site
// in the evaluation passes — exported for callers outside this
// package (the hover rendering in service inlines transform callbacks
// under the same walk).
func StandardAnalyzers() LoopAnalyzers {
	return LoopAnalyzers{
		AnalyzeStatement:   AnalyzeStatement,
		EvaluateExpression: evaluateExpression,
		IterationElement:   IterationElementOf,
	}
}

// promiseCallbackAnalyzers is the same triple under the settlement
// handlers' own name.
func promiseCallbackAnalyzers() LoopAnalyzers {
	return StandardAnalyzers()
}

// promiseRunHandler runs one settlement handler with the argument
// bound, under the same re-entry guard CallbackResult holds. False
// when the handler is already on the walk's own stack — the recursive
// re-entry answers nothing rather than diverging.
func promiseRunHandler(ctx *FlowContext, env Env, handler Callback, argument abstractdomain.AbstractValue) (abstractdomain.AbstractValue, bool) {
	key := walkingCallbackKey{p: ctx.P, arrow: handler}
	walkingCallbacksMu.Lock()
	_, walking := walkingCallbacks[key]
	if !walking {
		walkingCallbacks[key] = struct{}{}
	}
	walkingCallbacksMu.Unlock()
	if walking {
		return abstractdomain.AbstractValue{}, false
	}
	defer func() {
		walkingCallbacksMu.Lock()
		delete(walkingCallbacks, key)
		walkingCallbacksMu.Unlock()
	}()
	return InlineCallback(ctx, env, handler, argument, promiseCallbackAnalyzers(), nil), true
}

// promiseArgumentsResidue evaluates the handler expressions a promise
// row did not follow — their own effects still happen at the call —
// and answers the walk's residue for the call itself.
func promiseArgumentsResidue(ctx *FlowContext, env Env, arguments []*ast.Node) *abstractdomain.AbstractValue {
	for _, argument := range arguments {
		evaluateExpression(ctx, env, argument)
	}
	out := silence.Residue()
	return &out
}

// readPromiseInstanceMethod reads then/catch/finally where the
// receiver's evaluated value is a walk-built promise. Nil when the
// receiver is not one or the method is another name — the chain moves
// on; the walk's residue when the method is one of the three but a
// handler cannot be followed whole.
func readPromiseInstanceMethod(site MethodCallSite) *abstractdomain.AbstractValue {
	ctx, env, e, receiver, method := site.Ctx, site.Env, site.E, site.Receiver, site.Method
	if receiver.Kind != abstractdomain.KindPromise || receiver.Inner == nil {
		return nil
	}
	if method != "then" && method != "catch" && method != "finally" {
		return nil
	}
	call := e.AsCallExpression()
	var arguments []*ast.Node
	if call.Arguments != nil {
		arguments = call.Arguments.Nodes
	}
	inner := abstractdomain.AtTrustLevel(
		*receiver.Inner,
		abstractdomain.MinTrustLevel(abstractdomain.TrustLevelOf(receiver), abstractdomain.TrustLevelOf(*receiver.Inner)),
	)
	// no handler at all forwards the settlement unchanged — then() and
	// catch() substitute identity/thrower (sec-performpromisethen
	// steps 3-4), and finally() with a non-callable forwards the same
	// way (sec-promise.prototype.finally step 5)
	if len(arguments) == 0 {
		out := receiver
		return &out
	}
	switch method {
	case "finally":
		handler := promiseHandlerOf(ctx, arguments[0])
		if handler == nil {
			return promiseArgumentsResidue(ctx, env, arguments)
		}
		// onFinally is called with no arguments (steps 6.a.i, 6.b.i:
		// Call(onFinally, undefined) with an empty list) — a declared
		// parameter wears absence; the run is for the body's effects,
		// its return is dropped by the value thunk
		if _, ran := promiseRunHandler(ctx, env, handler, abstractdomain.AtTrustLevel(abstractdomain.Undef, abstractdomain.TrustSpec)); !ran {
			return promiseArgumentsResidue(ctx, env, arguments[1:])
		}
		out := receiver
		return &out
	case "catch":
		handler := promiseHandlerOf(ctx, arguments[0])
		if handler == nil {
			return promiseArgumentsResidue(ctx, env, arguments)
		}
		handled, ran := promiseRunHandler(ctx, env, handler, abstractdomain.Opaque)
		if !ran {
			return promiseArgumentsResidue(ctx, env, arguments[1:])
		}
		settledHandled, ok := promiseSettledValueOf(handled)
		if !ok {
			return promiseArgumentsResidue(ctx, env, arguments[1:])
		}
		out := promiseWrapping(abstractdomain.JoinKnown(inner, settledHandled))
		return &out
	case "then":
		onFulfilled := promiseHandlerOf(ctx, arguments[0])
		if onFulfilled == nil {
			return promiseArgumentsResidue(ctx, env, arguments)
		}
		result, ran := promiseRunHandler(ctx, env, onFulfilled, inner)
		if !ran {
			return promiseArgumentsResidue(ctx, env, arguments[1:])
		}
		settledResult, ok := promiseSettledValueOf(result)
		if !ok {
			return promiseArgumentsResidue(ctx, env, arguments[1:])
		}
		joined := settledResult
		if len(arguments) >= 2 {
			onRejected := promiseHandlerOf(ctx, arguments[1])
			if onRejected == nil {
				return promiseArgumentsResidue(ctx, env, arguments[1:])
			}
			rejected, rejectedRan := promiseRunHandler(ctx, env, onRejected, abstractdomain.Opaque)
			if !rejectedRan {
				return promiseArgumentsResidue(ctx, env, arguments[2:])
			}
			settledRejected, rejectedOk := promiseSettledValueOf(rejected)
			if !rejectedOk {
				return promiseArgumentsResidue(ctx, env, arguments[2:])
			}
			joined = abstractdomain.JoinKnown(joined, settledRejected)
		}
		out := promiseWrapping(joined)
		return &out
	}
	return nil
}
