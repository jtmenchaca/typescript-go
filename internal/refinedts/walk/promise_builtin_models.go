// from evaluation/promise_builtin_models.ts
//
// Promise.resolve(x): PromiseResolve fulfills with a non-thenable
// x unchanged (sec-promise.resolve), and a promise argument is
// adopted, never double-wrapped. Only kinds that can never be
// thenables wrap — a values word, a set-known real, absence, NaN.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
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
