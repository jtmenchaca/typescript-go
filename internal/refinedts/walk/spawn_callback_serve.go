// Serving the spawn (async) return leg foreign_edge.go's SpawnReturnLeg
// recognizes: the accumulate-then-parse `.on()` pair spawnReturnLegOf
// reads, walked so the parse node inside the close handler answers the
// target's stated fact.
//
// WHY THIS IS ITS OWN FILE, SEPARATE FROM foreign_edge.go. spawnReturnLegOf
// (foreign_edge.go) already recognizes the whole accumulate-then-parse
// shape; nothing here re-derives that reading. What foreign_edge.go's OWN
// route (ForeignEdgeAt → spawnAsyncEdgeOf) cannot do is serve it: the
// override foreignOverrideAt installs pins a whole STATEMENT the walk is
// about to reach ordinarily, and here the statement that must carry the
// fact — the `.on("close", cb)` call — never walks INTO the callback body
// on its own (an ordinary expression-statement evaluation calls
// evaluateExpression on the `.on(...)` call, which evaluates its two
// arguments and returns; nothing inlines the handler). This file supplies
// that inlining, reusing InlineCallback (callback_models.go) unchanged.
//
// THE ROUTE, in order:
//
//  1. Recognize the spawn call at statements[index] exactly as
//     spawnAsyncEdgeOf does (the runner word, the argv-named script, the
//     resolved path) — this file does not re-derive that reading, it CALLS
//     the same unexported helpers foreign_edge.go already exports at
//     package grain (resolvesToChildProcessMember, constBoundCallOf,
//     runnerAndScriptArgvOf, resolveForeignScriptPath).
//  2. Recognize the return leg itself: spawnReturnLegOf, called exactly as
//     spawnAsyncEdgeOf already calls it.
//  3. Discharge the artifact-side premises ReadForeignArtifact owns
//     (target integrity, runtime identity, harness shape) and channel
//     purity (§5) — the SAME two gates checkOutboundLeg's caller
//     (ForeignEdgeAt) already runs for the synchronous edges, reused
//     verbatim rather than re-derived.
//  4. WALK the data handler's body through InlineCallback — havoc
//     semantics unchanged, so the accumulator's env value after the data
//     handler is NOT trusted (the file banner's own rule: the fact
//     attaches to the parse, never to the accumulator string).
//  5. Install the parse node's override — the target's stated return set,
//     at the grade the crossing's weakest cited boundary admits, exactly
//     foreignReturnValue's reading — on a PINNED CONTEXT COPY (the same
//     idiom listWalk's own foreignOverrideAt consumer and
//     RelationalQuotientOverride's caller both use: copy *ctx, set
//     NodeOverrides, walk, let the copy fall out of scope), and walk the
//     close handler's body through InlineCallback under that pinned copy
//     — the override rides the ctx COPY into the inlined body, so
//     JSON.parse(out) answers the fact regardless of what the env thinks
//     `out` holds.
//
// The premise the override at step 5 rests on (the close handler running
// after every data event) is stated once, in a comment at the install
// site inside serveRecognizedSpawnLeg below — not repeated here.
//
// TRUST GRADE. Serving the parse node reuses foreignReturnValue's own
// reading unchanged: the fact is TrustSpec end to end, whether it attaches
// through the synchronous override or here — the transport identity §4
// states as a cited premise does not get any stronger for running through
// two callbacks instead of one statement.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// SpawnCallbackServeOutcome is what serving a recognized spawn return leg
// decided at one statement: a green crossing names the two handler bodies
// this route walked so a caller can skip walking them again ordinarily
// (they are handled here, under the pinned override); anything else
// declines by name, exactly as every other premise in this package does.
type SpawnCallbackServeOutcome struct {
	// Served: true when every premise came back green and both handler
	// bodies were walked here.
	Served bool
	// DataHandler, CloseHandler: the two callback nodes this route walked
	// — nil unless Served.
	DataHandler  *ast.Node
	CloseHandler *ast.Node
	// CloseHandlerResult: the close handler's own block-return value,
	// collected through InlineCallback's ReturnSink exactly as any other
	// inlined callback's result is — the served fact rides in here when
	// the handler's last statement returns the parsed value (the fixture
	// shape), so a caller can read the fact off the walk's own result
	// without re-evaluating the parse node under a since-restored
	// override. Zero value unless Served.
	CloseHandlerResult abstractdomain.AbstractValue
	// Decline: the sentence naming the first premise that stopped serving,
	// "" wherever the statement is not a recognized spawn call at all (no
	// sentence owed — an ordinary call).
	Decline string
	// DeclineNode: where the decline sentence points, nil when Decline is
	// "".
	DeclineNode *ast.Node
	// TargetPath: the resolved .py path this route consumed, set whenever
	// the spawn call itself resolved one — mirrors
	// ForeignEdgeOutcome.TargetPath's "consumed means looked at, not
	// approved" reading.
	TargetPath string
}

// ServeSpawnReturnLeg recognizes and serves the spawn (async)
// accumulate-then-parse pair at statements[index], or answers
// (outcome, false) for every statement that is not this shape at all —
// the caller's ordinary walk is untouched.
//
// A recognized pair whose artifact premises are not green still answers
// (outcome, true) carrying a Decline: an edge the checker sees and cannot
// serve is a work-queue item, never a silence — the same discipline
// ForeignEdgeAt already applies to every other premise in this file.
func ServeSpawnReturnLeg(
	ctx *FlowContext, env Env, statements []*ast.Node, index int,
) (*SpawnCallbackServeOutcome, bool) {
	statement := statements[index]
	name, call, ok := constBoundCallOf(statement)
	if !ok {
		return nil, false
	}
	callee := calleeOf(call)
	if !resolvesToChildProcessMember(ctx, callee, "spawn") {
		return nil, false
	}
	args, argsOk := callArguments(call)
	if !argsOk || len(args) < 2 {
		return nil, false
	}
	runnerWord, script, _, scriptOk, sentence, sentenceNode := runnerAndScriptArgvOf(ctx, args[0], args[1])
	if sentence != "" {
		return &SpawnCallbackServeOutcome{Decline: sentence, DeclineNode: sentenceNode}, true
	}
	if !scriptOk {
		return nil, false
	}
	resolvedPath, pathSentence := resolveForeignScriptPath(call, runnerWord, script)
	if pathSentence != "" {
		return &SpawnCallbackServeOutcome{Decline: pathSentence, DeclineNode: call, TargetPath: resolvedPath}, true
	}
	if resolvedPath == "" {
		return nil, false
	}
	leg, legSentence := spawnReturnLegOf(statements, index, name)
	if legSentence != "" {
		// the pair itself is not recognized — foreign_edge.go's own
		// spawnAsyncEdgeOf already owns naming this sentence; this route
		// declines the identical way rather than serving nothing
		return &SpawnCallbackServeOutcome{Decline: legSentence, DeclineNode: call, TargetPath: resolvedPath}, true
	}
	artifact, artifactSentence := ReadForeignArtifact(resolvedPath)
	if artifactSentence != "" {
		return &SpawnCallbackServeOutcome{Decline: artifactSentence, DeclineNode: call, TargetPath: resolvedPath}, true
	}
	outcome := serveRecognizedSpawnLeg(ctx, env, leg, artifact, call)
	outcome.TargetPath = resolvedPath
	return outcome, true
}

// serveRecognizedSpawnLeg is the part of the route that starts AFTER
// recognition: leg and artifact are already in hand (a recognized
// SpawnReturnLeg, and a green-envelope ForeignArtifact), and this
// discharges channel purity, then walks both handler bodies. Split out
// from ServeSpawnReturnLeg so it can be exercised directly against a
// hand-built leg/artifact pair, the same way checkOutboundLeg's own tests
// build an edge/artifact by hand and skip ForeignEdgeAt's callee
// resolution gate (foreign_edge_test.go's own banner: resolving `spawn`
// to a real child_process.d.ts declaration needs a program host this
// package's tests do not stand up).
func serveRecognizedSpawnLeg(
	ctx *FlowContext, env Env, leg SpawnReturnLeg, artifact *ForeignArtifact, declineNode *ast.Node,
) *SpawnCallbackServeOutcome {
	if !artifact.Called.Return.StdoutPure {
		return &SpawnCallbackServeOutcome{
			Decline: "the target " + artifact.Called.Name + " does not state that it writes " +
				"nothing else to stdout, and this edge reads its result off stdout — " +
				"the channel-purity premise is undischarged",
			DeclineNode: declineNode,
		}
	}
	// (a) the data handler's body walks through InlineCallback — havoc
	// semantics unchanged: whatever it writes to the accumulator (and any
	// other outer name) is forgotten, exactly as InlineCallback already
	// forgets every callback body's writes. The accumulator's env value
	// after this walk is NOT trusted; only ParseNode's fact is claimed.
	InlineCallback(ctx, env, leg.DataHandler, silence.Residue(), StandardAnalyzers(), nil)
	// (b) the close handler's body walks through InlineCallback UNDER A
	// PINNED CONTEXT COPY carrying the parse node's override — the same
	// pinned-ctx idiom listWalk's foreignOverrideAt consumer and
	// RelationalQuotientOverride's caller both use: copy *ctx, set
	// NodeOverrides on the copy alone, walk, let the copy fall out of
	// scope when this call returns. The override rides the ctx COPY into
	// InlineCallback's own sub-walk for free (flow_context.go's own
	// scoping obligation: FlowContext is value-copied into every sub-walk,
	// so a *FlowContext built here and handed to InlineCallback carries the
	// override exactly as far as that one inlined body and no further).
	//
	// PREMISE: the close handler runs after the data events — Node's
	// child_process contract states a 'close' listener fires only once
	// every stdio stream has ended, so every 'data' event this walk can
	// see has already run by the time this override installs. TrustSpec,
	// the same cited-runtime-commitment shape the runtime band already
	// stamps: a spec clause read correctly, not a kernel decision.
	pinning := *ctx
	pinning.NodeOverrides = map[*ast.Node]abstractdomain.AbstractValue{
		leg.ParseNode: foreignReturnValue(artifact),
	}
	closeResult := InlineCallback(&pinning, env, leg.CloseHandler, silence.Residue(), StandardAnalyzers(), nil)
	return &SpawnCallbackServeOutcome{
		Served:             true,
		DataHandler:        leg.DataHandler,
		CloseHandler:       leg.CloseHandler,
		CloseHandlerResult: closeResult,
	}
}
