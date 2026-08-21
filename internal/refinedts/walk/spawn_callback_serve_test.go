// Tests for serveRecognizedSpawnLeg / ServeSpawnReturnLeg
// (spawn_callback_serve.go).
//
// WHAT IS PINNED AT WHICH GRAIN, and why. ServeSpawnReturnLeg's own
// recognition half (constBoundCallOf, resolvesToChildProcessMember,
// runnerAndScriptArgvOf, resolveForeignScriptPath, spawnReturnLegOf) is
// already exercised — spawnReturnLegOf's own cases live in
// foreign_edge_test.go, and resolvesToChildProcessMember needs a real
// child_process.d.ts declaration that file's own banner says this
// package's tests do not stand up (entryEnvTestProgram carries no
// @types/node). So the served case here calls serveRecognizedSpawnLeg
// directly, past the callee-resolution gate — a hand-built SpawnReturnLeg
// (read off a real parsed program the same way TestSpawnReturnLegOf_*
// already does) and a hand-built ForeignArtifact (the same
// foreignArtifactJSON-shaped literal foreign_edge_test.go's own outbound
// fixtures build), never a fake ServeSpawnReturnLeg call.

package walk

import (
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// spawnServeFunctionSource is the accumulate-then-parse pair every case
// below parses against — spawnPairSource (foreign_edge_test.go) plus a
// function naming the shape once, so each test only supplies its own
// body when it differs.
const spawnServeFunctionSource = spawnPairSource + `
function f() {
	const child = spawn("python3", ["./targets/level_ok.py"]);
	let out = "";
	child.stdout.on("data", (d) => {
		out += d;
	});
	child.on("close", () => {
		const level = JSON.parse(out);
		return level;
	});
}
`

// spawnServeArtifact is a green ForeignArtifact naming a scalar 0…1
// return, stdout-pure — the shape a level_ok.py-style target states.
func spawnServeArtifact() *ForeignArtifact {
	return &ForeignArtifact{
		TargetFile:  "targets/level_ok.py",
		RuntimeBand: "cpython-3.11+",
		Surface:     ForeignSurfaceStdinJSON,
		Called: ForeignFunctionFact{
			Name: "level_ok",
			Return: ForeignReturn{
				Set: refinementsets.MakeRefinedSet(
					refinementsets.AtLeast(0), refinementsets.AtMost(1)),
				StdoutPure: true,
			},
		},
	}
}

// spawnServeContext is the walk context every case shares: a real checker
// program, an alias store, and a collecting diagnostic sink — no kernel,
// since serving the return leg asks nothing of it (foreignReturnValue
// reads the artifact's own stated set; no ScalarSubset question is asked
// here the way the outbound leg's fit check asks one).
func spawnServeContext(p *program.CheckerProgram) (*FlowContext, *[]assignability.RefinementDiagnostic) {
	var reported []assignability.RefinementDiagnostic
	return &FlowContext{
		P:         p,
		Contracts: map[*ast.Symbol]*FunctionContract{},
		Aliases:   dataflowfacts.NewAliasClasses(),
		Declared:  map[string]*annotations.DeclaredRefinement{},
		Report:    func(d assignability.RefinementDiagnostic) { reported = append(reported, d) },
	}, &reported
}

// TestServeRecognizedSpawnLeg_TheServedCaseAnswersTheTargetsFactAtTheParseNode
// pins the served path end to end: the recognized SpawnReturnLeg walks
// through serveRecognizedSpawnLeg, and the close handler's own return
// value (collected through InlineCallback's ReturnSink) is the target's
// stated 0…1 fact — not Unknown, and not whatever the accumulator's own
// honest string value would have read as.
func TestServeRecognizedSpawnLeg_TheServedCaseAnswersTheTargetsFactAtTheParseNode(t *testing.T) {
	p := entryEnvTestProgram(t, spawnServeFunctionSource)
	statements := relationalAccumulationBodyOf(t, p, "f")
	leg, sentence := spawnReturnLegOf(statements, 0, "child")
	if sentence != "" {
		t.Fatalf("the accumulate-then-parse pair declined: %s", sentence)
	}
	ctx, reported := spawnServeContext(p)
	artifact := spawnServeArtifact()
	outcome := serveRecognizedSpawnLeg(ctx, NewEnv(), leg, artifact, statements[0])
	if !outcome.Served {
		t.Fatalf("a green crossing did not serve: %+v", outcome)
	}
	if len(*reported) != 0 {
		t.Errorf("a served crossing reported %d diagnostics: %+v", len(*reported), *reported)
	}
	crossingSet, ok := abstractdomain.SetOfKnown(outcome.CloseHandlerResult)
	if !ok {
		t.Fatalf("the close handler's result is not read as a set: %+v", outcome.CloseHandlerResult)
	}
	words := foreignSetWords(crossingSet)
	if !strings.Contains(words, "0") || !strings.Contains(words, "1") {
		t.Errorf("the close handler's result set reads %q, want the target's stated 0…1", words)
	}
	if abstractdomain.TrustLevelOf(outcome.CloseHandlerResult) != abstractdomain.TrustSpec {
		t.Errorf("the served fact's grade = %v, want TrustSpec", abstractdomain.TrustLevelOf(outcome.CloseHandlerResult))
	}
}

// TestServeRecognizedSpawnLeg_ChannelPurityUndischargedDeclines pins the
// one artifact-side premise this file discharges itself (rather than
// re-deriving from checkOutboundLeg): an artifact that does NOT state
// stdout purity declines rather than serving.
func TestServeRecognizedSpawnLeg_ChannelPurityUndischargedDeclines(t *testing.T) {
	p := entryEnvTestProgram(t, spawnServeFunctionSource)
	statements := relationalAccumulationBodyOf(t, p, "f")
	leg, sentence := spawnReturnLegOf(statements, 0, "child")
	if sentence != "" {
		t.Fatalf("the accumulate-then-parse pair declined: %s", sentence)
	}
	ctx, reported := spawnServeContext(p)
	artifact := spawnServeArtifact()
	artifact.Called.Return.StdoutPure = false
	outcome := serveRecognizedSpawnLeg(ctx, NewEnv(), leg, artifact, statements[0])
	if outcome.Served {
		t.Fatalf("an artifact with no stdout-purity premise was served: %+v", outcome)
	}
	if outcome.Decline == "" {
		t.Fatalf("channel-purity's own decline sentence is empty")
	}
	if outcome.DeclineNode == nil {
		t.Errorf("the decline carries no node to point at")
	}
	// declining owes no report of its own — ServeSpawnReturnLeg's caller
	// (analyze_statement.go) is the one that turns Decline into a 7002
	if len(*reported) != 0 {
		t.Errorf("serveRecognizedSpawnLeg reported %d diagnostics itself: %+v", len(*reported), *reported)
	}
}

// TestServeRecognizedSpawnLeg_ACloseHandlerThatAlsoWritesAnOuterNameStillHavocsIt
// pins the accumulator hazard's OTHER edge: InlineCallback's own exit
// havoc (callback_models.go:111-119) still runs for the close handler
// exactly as it would for any other callback — a second outer name the
// close handler writes is forgotten, not preserved through the pinned
// walk.
func TestServeRecognizedSpawnLeg_ACloseHandlerThatAlsoWritesAnOuterNameStillHavocsIt(t *testing.T) {
	p := entryEnvTestProgram(t, spawnPairSource+`
function f() {
	const child = spawn("python3", ["./targets/level_ok.py"]);
	let out = "";
	let done = false;
	child.stdout.on("data", (d) => {
		out += d;
	});
	child.on("close", () => {
		done = true;
		const level = JSON.parse(out);
		return level;
	});
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	leg, sentence := spawnReturnLegOf(statements, 0, "child")
	if sentence != "" {
		t.Fatalf("the accumulate-then-parse pair declined: %s", sentence)
	}
	ctx, _ := spawnServeContext(p)
	artifact := spawnServeArtifact()
	env := NewEnv()
	env.Set("done", abstractdomain.KnownValues([]float64{0}, abstractdomain.PrimitiveBoolean, abstractdomain.TrustProved))
	outcome := serveRecognizedSpawnLeg(ctx, env, leg, artifact, statements[0])
	if !outcome.Served {
		t.Fatalf("a green crossing did not serve: %+v", outcome)
	}
	held, ok := env.Get("done")
	if !ok {
		t.Fatalf("done was removed from the environment rather than havoced")
	}
	// the exit havoc must not leave "done" pinned at its pre-handler value
	// of exactly false — the close handler's own write to it is real, and
	// InlineCallback's forget (callback_models.go:111-119) is what must
	// clear the stale singleton
	if held.Kind == abstractdomain.KindValues && held.KindTag == abstractdomain.PrimitiveBoolean &&
		len(held.Values) == 1 && held.Values[0] == 0 {
		t.Errorf("done still reads as exactly its pre-handler value false; the close handler's write to it was not havoced: %+v", held)
	}
}

// TestSpawnCallbackServe_TheCalleeGateDeclinesWithNoChildProcessDeclarationInReach
// pins ServeSpawnReturnLeg's OWN recognition gate: with no @types/node in
// the test program (this package's own limit — foreign_edge_test.go's
// banner names the identical limit for ForeignEdgeAt),
// resolvesToChildProcessMember cannot resolve "spawn" to a real
// child_process.d.ts declaration, so ServeSpawnReturnLeg answers
// isSpawn=false — an ordinary call, no sentence owed — rather than
// reaching spawnReturnLegOf at all. spawnReturnLegOf's OWN declines (a
// missing 'close' handler, an intervening write, a mismatched parameter)
// are unchanged and stay pinned at their existing grain
// (TestSpawnReturnLegOf_* in foreign_edge_test.go) — this file does not
// re-derive them.
func TestSpawnCallbackServe_TheCalleeGateDeclinesWithNoChildProcessDeclarationInReach(t *testing.T) {
	p := entryEnvTestProgram(t, spawnPairSource+`
function f() {
	const child = spawn("python3", ["./targets/level_ok.py"]);
	let out = "";
	child.stdout.on("data", (d) => {
		out += d;
	});
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	ctx, _ := spawnServeContext(p)
	outcome, isSpawn := ServeSpawnReturnLeg(ctx, NewEnv(), statements, 0)
	if isSpawn {
		t.Fatalf("ServeSpawnReturnLeg recognized a spawn call with no child_process.d.ts in reach: %+v", outcome)
	}
}
