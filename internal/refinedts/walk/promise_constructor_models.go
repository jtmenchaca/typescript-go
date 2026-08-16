// `new Promise(executor)` on the default-library constructor. The
// executor runs synchronously at the `new` itself
// (sec-promise-executor step 9-10: Call(executor, undefined,
// « resolve, reject ») before the construction returns), and the
// promise settles with whatever the executor's resolve calls hand
// over — eventually, which is soon enough for the await that reads
// the inner value.
//
// Total-or-decline over the resolve parameter's uses: every
// occurrence must be the callee of a direct `resolve(arg)` call — a
// resolve that escapes into any other position could fulfill with a
// value this read never saw. Reject calls need no reading at all: a
// rejection means the await's continuation never ran, the same
// over-approximation a throw takes. The inner value is the JOIN over
// every resolve call's argument, since [[AlreadyCalled]] lets exactly
// one of them win and the walk does not know which.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// ReadPromiseConstruction reads `new Promise(executor)` with an inline
// executor whose resolve calls are all in view. Nil for every other
// shape — the construction then takes whatever route it took before;
// the walk's residue when the shape is read but a resolve argument's
// value cannot settle in view.
func ReadPromiseConstruction(ctx *FlowContext, env Env, e *ast.Node) *abstractdomain.AbstractValue {
	newExpr := e.AsNewExpression()
	if !ast.IsIdentifier(newExpr.Expression) || newExpr.Expression.Text() != "Promise" ||
		!resolvesToDefaultLib(ctx, newExpr.Expression) {
		return nil
	}
	if newExpr.Arguments == nil || len(newExpr.Arguments.Nodes) != 1 {
		return nil
	}
	executor := Unwrapped(newExpr.Arguments.Nodes[0])
	if !ast.IsArrowFunction(executor) && !ast.IsFunctionExpression(executor) {
		return nil
	}
	body := executor.Body()
	if body == nil {
		return nil
	}
	// `arguments[0]` inside a function-expression executor aliases the
	// resolve parameter past this scan's sight — the form declines
	if ast.IsFunctionExpression(executor) && mentionsArgumentsObject(body) {
		return nil
	}
	parameters := executor.Parameters()
	if len(parameters) == 0 {
		return nil
	}
	resolveName := parameters[0].AsParameterDeclaration().Name()
	if resolveName == nil || !ast.IsIdentifier(resolveName) {
		return nil
	}
	resolveArguments, total := promiseResolveCallsOf(body, resolveName.Text())
	if !total || len(resolveArguments) == 0 {
		// an escaping resolve reads as any value, and a resolve never
		// called never fulfills (the await never resumes) — either way
		// this reader has nothing to claim
		return nil
	}
	var joined *abstractdomain.AbstractValue
	for _, argument := range resolveArguments {
		// `resolve()` with no argument fulfills with undefined — the
		// resolving function's value defaults absent
		// (sec-createresolvingfunctions: resolution is the one argument
		// passed, undefined when none is)
		value := abstractdomain.AtTrustLevel(abstractdomain.Undef, abstractdomain.TrustSpec)
		if argument != nil {
			value = evaluateExpression(ctx, env, argument)
		}
		settled, ok := promiseSettledValueOf(value)
		if !ok {
			out := silence.Residue()
			return &out
		}
		if joined == nil {
			one := settled
			joined = &one
		} else {
			next := abstractdomain.JoinKnown(*joined, settled)
			joined = &next
		}
	}
	// the executor's body already ran by the time the `new` hands the
	// promise back — whatever outer names it writes are written now
	written := map[string]struct{}{}
	AssignedNames(ctx.P.Checker, body, written)
	for name := range written {
		if _, ok := env.Get(name); ok {
			HavocEnv(ctx.Aliases, env, name)
		}
	}
	out := abstractdomain.AbstractValue{Kind: abstractdomain.KindPromise, Inner: joined}
	return &out
}

// promiseResolveCallsOf collects the argument of every direct
// `resolve(arg)` call in the executor's body — nil standing for the
// zero-argument call. False when any occurrence of the name is not
// the callee of such a call (aliased, passed on, called with more
// than one argument): the resolve function escaped the scan's sight.
// The scan descends into nested closures too — a resolve called
// inside a setTimeout callback still settles this promise. A nested
// redeclaration of the same name can only shadow the capability: its
// calls join extra values, and an outer resolve left uncalled never
// fulfills — both on the sound side — so the scan does not chase
// scopes.
func promiseResolveCallsOf(body *ast.Node, name string) (arguments []*ast.Node, total bool) {
	total = true
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if !total {
			return true
		}
		if ast.IsCallExpression(node) {
			call := node.AsCallExpression()
			callee := Unwrapped(call.Expression)
			if ast.IsIdentifier(callee) && callee.Text() == name {
				var callArguments []*ast.Node
				if call.Arguments != nil {
					callArguments = call.Arguments.Nodes
				}
				if len(callArguments) > 1 {
					total = false
					return true
				}
				if len(callArguments) == 0 {
					arguments = append(arguments, nil)
					return false
				}
				arguments = append(arguments, callArguments[0])
				// the argument subtree still scans — `resolve(f(resolve))`
				// hides a second use inside the first call's own argument
				visit(callArguments[0])
				return false
			}
		}
		if ast.IsIdentifier(node) && node.Text() == name {
			total = false
			return true
		}
		node.ForEachChild(visit)
		return false
	}
	visit(body)
	if !total {
		return nil, false
	}
	return arguments, true
}

// mentionsArgumentsObject answers whether a body mentions the
// `arguments` object, which indexes every parameter — the resolve
// alias no identifier scan sees.
func mentionsArgumentsObject(body *ast.Node) bool {
	found := false
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if found {
			return true
		}
		if ast.IsIdentifier(node) && node.Text() == "arguments" {
			found = true
			return true
		}
		node.ForEachChild(visit)
		return false
	}
	visit(body)
	return found
}
