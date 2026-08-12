// from interprocedural/callback_models.ts
//
// The synchronous callbacks: map, filter, reduce, forEach, flatMap,
// find with an inline arrow. These run to completion inside the call
// expression — nothing can write between capture and invocation — so
// the environment AT the call is exactly what the callback sees, and
// no capture rule is needed here (stored closures are inlined at
// their own call sites; see flow.ts).
//
// Over an exact tuple the checker simply RUNS the fold, element by
// element, on the machine's own arithmetic — block bodies included,
// their returned values collected through the return sink. Over a
// star set the element wears the star's item: map transfers the body,
// filter narrows by the predicate's lift (an unliftable predicate
// falls back to the unnarrowed element — filter never adds members,
// so that is sound), and reduce rides the accumulation engine —
// iterate, widen, the kernel certifies. A reduce with no initial
// value initialStates from the element. Whatever the body writes to outer
// names is forgotten for EVERY method — a callback is still a write
// site. Judgments inside the body report exactly once, against the
// representative element.
//
// This file's TS twin (interprocedural/callback_models.ts) originally
// had a Go hub file at internal/refinedts/interprocedural/
// callback_models.go, holding only the Callback type alias; this
// port completed it here in package walk, per PORT.md's walk-package
// rule. The hub package's Callback alias moved to walk/flow_context.go
// and the interprocedural package was deleted at integration
// (walk-integration-punchlist.md items 3 and 9).

package walk

import (
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// CallbackOf is the callback a call hands to a recognized method, or
// nil. A `f.bind(...)` — inline or const-bound — answers the TARGET
// function; the prebound offset is re-derived where the parameters
// bind (CallbackOutcome), from the same recognizer.
func CallbackOf(ctx *FlowContext, method string, call *ast.Node) Callback {
	callExpr := call.AsCallExpression()
	if _, ok := ArrayCallbackMethods[method]; !ok || len(callExpr.Arguments.Nodes) < 1 {
		return nil
	}
	argument := callExpr.Arguments.Nodes[0]
	if ast.IsArrowFunction(argument) || ast.IsFunctionExpression(argument) {
		return argument
	}
	bound := StoredBoundFunctionOf(ctx, argument)
	if bound != nil {
		return bound.Target
	}
	return FunctionInReach(ctx, argument)
}

// InlineCallback runs ONE callback with one argument's knowledge
// bound — the seam a schema's `.transform` evaluation calls
// (libraryAdapters parse-eval). The callback executes exactly once
// at the parse site, so its body's judgments report normally;
// whatever it writes to outer names is forgotten (a callback is
// still a write site).
func InlineCallback(
	ctx *FlowContext,
	env Env,
	callback Callback,
	argument abstractdomain.AbstractValue,
	analyzers LoopAnalyzers,
	indexArgument *abstractdomain.AbstractValue,
) abstractdomain.AbstractValue {
	parameters := callback.Parameters()
	bindings := map[string]abstractdomain.AbstractValue{}
	if len(parameters) > 0 {
		BindParameter(ctx.P.Checker, parameters[0], argument, bindings)
	}
	if indexArgument != nil {
		var parameter1 *ast.Node
		if len(parameters) > 1 {
			parameter1 = parameters[1]
		}
		BindParameter(ctx.P.Checker, parameter1, *indexArgument, bindings)
	}
	callEnv := Env{}
	for k, v := range env {
		callEnv[k] = v
	}
	for name, known := range bindings {
		callEnv[name] = known
	}
	body := callback.Body()
	result := silence.Residue()
	if body != nil {
		if ast.IsBlock(body) {
			var sink []abstractdomain.AbstractValue
			sinkCtx := *ctx
			sinkCtx.ReturnSink = &sink
			analyzers.AnalyzeStatement(&sinkCtx, callEnv, body, nil)
			if len(sink) > 0 {
				joined := sink[0]
				for _, v := range sink[1:] {
					joined = abstractdomain.JoinKnown(joined, v)
				}
				result = joined
			}
		} else {
			result = analyzers.EvaluateExpression(ctx, callEnv, body)
		}
	}
	written := map[string]struct{}{}
	if body != nil {
		AssignedNames(ctx.P.Checker, body, written)
	}
	for name := range written {
		if _, ok := env[name]; ok {
			ctx.Aliases.Havoc(env, name)
		}
	}
	return result
}

// walkingCallbacks is the callback bodies currently being walked.
// Running a callback walks its body HERE, and a body may reach the
// very same callback again — `xs.filter(p)` inside the function `p`
// belongs to, a method whose implementation maps over the same
// collection. Nothing guarded that: the walk re-entered without
// limit and exhausted the stack, and a 40,000-frame stack exhausted
// just as surely, because the recursion had no bottom rather than a
// deep one.
//
// Re-entry answers unknown, which is what the three inlining seams
// already do for a recursive call (`ctx.Inlining`). This is keyed by
// the callback NODE, so the same callback still runs once per
// element — those calls are sequential, not nested.
//
// The TS source keys this with a `Set<ts.Node>`; the Go twin keys by
// (check, node) under a mutex — the parallel sweep runs one walk per
// entry goroutine, and a shared node reached by TWO checks at once is
// concurrency, not recursion, so each check tracks only its own
// nesting (ctx.P is the per-check handle, the parallel-sweep audit's
// registry key).
type walkingCallbackKey struct {
	p     *program.CheckerProgram
	arrow *ast.Node
}

var (
	walkingCallbacksMu sync.Mutex
	walkingCallbacks   = map[walkingCallbackKey]struct{}{}
)

// CallbackResult is callbackResult in the TS source.
func CallbackResult(
	ctx *FlowContext,
	env Env,
	rawReceiver abstractdomain.AbstractValue,
	method string,
	call *ast.Node,
	arrow Callback,
	trackedName string,
	hasTrackedName bool,
	analyzers LoopAnalyzers,
) abstractdomain.AbstractValue {
	key := walkingCallbackKey{p: ctx.P, arrow: arrow}
	walkingCallbacksMu.Lock()
	_, walking := walkingCallbacks[key]
	if !walking {
		walkingCallbacks[key] = struct{}{}
	}
	walkingCallbacksMu.Unlock()
	if walking {
		return silence.CutUnknown() // recursion: silence
	}
	defer func() {
		walkingCallbacksMu.Lock()
		delete(walkingCallbacks, key)
		walkingCallbacksMu.Unlock()
	}()
	return CallbackOutcome(ctx, env, rawReceiver, method, call, arrow, trackedName, hasTrackedName, analyzers)
}
