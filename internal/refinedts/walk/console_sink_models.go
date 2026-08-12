// from evaluation/console_sink_models.ts
//
// Known sinks that read their arguments and keep nothing:
// console.* (and the JSON.stringify arm kept here only as a belt —
// readJsonMethods owns both its exact and its spec-image answers).
// Printing a tracked array is not a reason to forget it.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// readConsoleSink is readConsoleSink in the TS source: console.* (and
// a JSON.stringify fallback that should not reach here). Nil when the
// receiver is not a known sink.
func readConsoleSink(site MethodCallSite) *abstractdomain.AbstractValue {
	ctx, env, e, receiverExpression, method := site.Ctx, site.Env, site.E, site.ReceiverExpression, site.Method
	if !(ast.IsIdentifier(receiverExpression) && resolvesToDefaultLib(ctx, receiverExpression) &&
		(receiverExpression.Text() == "console" || (receiverExpression.Text() == "JSON" && method == "stringify"))) {
		return nil
	}
	call := e.AsCallExpression()
	if call.Arguments != nil {
		for _, argument := range call.Arguments.Nodes {
			evaluateExpression(ctx, env, argument)
		}
	}
	out := silence.Residue()
	return &out
}
