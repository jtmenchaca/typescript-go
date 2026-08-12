// from evaluation/callback_call_models.ts
//
// Method calls whose body is a callback the walk can follow —
// callbackOf / callbackResult, plus Array.prototype.find's element-
// or-undefined answer when the search itself stays undecided.

package walk

import (
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
)

// readCallbackMethod is readCallbackMethod in the TS source: a
// modeled callback method, or nil when the call has no callback the
// walk can follow.
func readCallbackMethod(site MethodCallSite) *abstractdomain.AbstractValue {
	ctx, env, e, receiver, method := site.Ctx, site.Env, site.E, site.Receiver, site.Method
	callback := CallbackOf(ctx, method, e)
	if callback == nil {
		return nil
	}
	answered := CallbackResult(ctx, env, receiver, method, e, callback, site.TrackedName, site.HasTrackedName, LoopAnalyzers{
		AnalyzeStatement:   AnalyzeStatement,
		EvaluateExpression: evaluateExpression,
		IterationElement:   IterationElementOf,
	})
	// find answers a matching ELEMENT, or undefined when nothing
	// matches (sec-array.prototype.find) — so even an undecided search
	// holds the element set, possibly absent
	if method == "find" && answered.Kind == abstractdomain.KindUnknown && !answered.Opaque {
		element := ElementOf(receiver)
		if element.Kind != abstractdomain.KindUnknown {
			out := abstractdomain.PossiblyUndefined(element, "", false, false)
			return &out
		}
	}
	return &answered
}
