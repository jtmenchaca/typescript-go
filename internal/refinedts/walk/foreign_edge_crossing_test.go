// The outbound leg's own fit chain — the shared foreignEdgeFixture type,
// the stdin-scalar and array-literal fixture builders, and the stdin
// fit-chain tests (channel match, NaN-freedom, the element/length/
// scalar fit questions, and the const-array-literal payload shapes).
// The argv-value leg lives in foreign_edge_argv_test.go; the mixed
// (stdin + argv) shape in foreign_edge_mixed_test.go; the file-carried
// shape and the KindList sequence-crossing conversions in
// foreign_edge_sequence_test.go — all four files share this one's
// fixture builders and foreignEdgeFixture type.

package walk

import (
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

/* ── the outbound leg, against a real kernel ─────────────────────── */

// foreignCrossingEnv seeds the value that crosses out under one name,
// as a repetition of a bounded element — the shape `boosted` wears in
// the tutorial fixture after the map derivation.
func foreignCrossingEnv(name string, lo float64, hi float64, atLeast int) Env {
	env := NewEnv()
	element := refinementsets.MakeRefinedSet(
		refinementsets.AtLeast(lo), refinementsets.AtMost(hi))
	env.Set(name, abstractdomain.KnownSet(
		refinementsets.Repetition(element, atLeast, nil), nil,
		abstractdomain.TrustProved, abstractdomain.SetKindTagNone))
	return env
}

// foreignEdgeFixture is the whole outbound-leg setup: an artifact read
// off disk, a context carrying a real kernel and a collecting sink, an
// env holding the crossing value, and the node it is judged at.
type foreignEdgeFixture struct {
	artifact *ForeignArtifact
	ctx      *FlowContext
	env      Env
	edge     *ForeignEdge
	reported *[]assignability.RefinementDiagnostic
}

func foreignOutboundFixture(t *testing.T, lo float64, hi float64, atLeast int) foreignEdgeFixture {
	t.Helper()
	targetPath, contentHash := writeForeignTarget(t)
	writeForeignArtifact(t, targetPath, foreignArtifactJSON(contentHash, targetPath, true))
	artifact, sentence := ReadForeignArtifact(targetPath)
	if sentence != "" {
		t.Fatalf("the fixture artifact declined: %s", sentence)
	}
	p := entryEnvTestProgram(t, "function f(boosted: number[]) { boosted; }\n")
	fn := entryEnvFunctionNamed(t, p, "f")
	payload := fn.AsFunctionDeclaration().Body.AsBlock().Statements.Nodes[0].
		AsExpressionStatement().Expression
	var reported []assignability.RefinementDiagnostic
	ctx := &FlowContext{
		P:         p,
		Kernel:    nanWrapperLoadKernel(t),
		Contracts: map[*ast.Symbol]*FunctionContract{},
		Aliases:   dataflowfacts.NewAliasClasses(),
		Declared:  map[string]*annotations.DeclaredRefinement{},
		Report:    func(d assignability.RefinementDiagnostic) { reported = append(reported, d) },
	}
	return foreignEdgeFixture{
		artifact: artifact,
		ctx:      ctx,
		env:      foreignCrossingEnv("boosted", lo, hi, atLeast),
		edge:     &ForeignEdge{Call: payload, TargetPath: targetPath, Payload: payload, StdoutName: "stdout"},
		reported: &reported,
	}
}

// foreignLiteralArrayOutboundFixture is the array-LITERAL counterpart of
// foreignOutboundFixture: the payload identifier reads `samples`'s bare
// array literal — declared either at MODULE scope (declareSamplesInModule
// true, d-data-legs.ts's own shape), where the identifier resolves through
// evaluateExpression's env.Get miss into UntrackedIdentifier's module-const
// follow, or inside the function body one statement above the call, where
// the const's own preceding statement is walked first (AnalyzeVariableStatement)
// so env carries exactly the binding an ordinary function-local const
// leaves behind — the ordinary env.Get hit, never the module-const follow.
// audio_level.py's own entry (−2 … 2, lengthAtLeast 1) is the target
// throughout — the payload literal's own values decide whether the
// crossing fits, not the fixture itself.
func foreignLiteralArrayOutboundFixture(
	t *testing.T, samplesLiteral string, declareSamplesInModule bool,
) foreignEdgeFixture {
	t.Helper()
	targetPath, contentHash := writeForeignTarget(t)
	writeForeignArtifact(t, targetPath, foreignArtifactJSON(contentHash, targetPath, true))
	artifact, sentence := ReadForeignArtifact(targetPath)
	if sentence != "" {
		t.Fatalf("the fixture artifact declined: %s", sentence)
	}
	var source string
	var callStatementIndex int
	if declareSamplesInModule {
		source = "const samples = " + samplesLiteral + ";\n" +
			"function f() {\n" +
			"	samples;\n" +
			"}\n"
		callStatementIndex = 0
	} else {
		source = "function f() {\n" +
			"	const samples = " + samplesLiteral + ";\n" +
			"	samples;\n" +
			"}\n"
		callStatementIndex = 1
	}
	p := entryEnvTestProgram(t, source)
	statements := relationalAccumulationBodyOf(t, p, "f")
	payload := statements[callStatementIndex].AsExpressionStatement().Expression
	var reported []assignability.RefinementDiagnostic
	ctx := &FlowContext{
		P:         p,
		Kernel:    nanWrapperLoadKernel(t),
		Contracts: map[*ast.Symbol]*FunctionContract{},
		Aliases:   dataflowfacts.NewAliasClasses(),
		Declared:  map[string]*annotations.DeclaredRefinement{},
		Report:    func(d assignability.RefinementDiagnostic) { reported = append(reported, d) },
	}
	env := NewEnv()
	if !declareSamplesInModule {
		// the ordinary local-binding path: `samples`'s own preceding const
		// statement is walked for real, so env holds exactly what an
		// ordinary function-local const leaves behind — no synthetic seed
		AnalyzeVariableStatement(ctx, env, statements[0])
	}
	return foreignEdgeFixture{
		artifact: artifact,
		ctx:      ctx,
		env:      env,
		edge:     &ForeignEdge{Call: payload, TargetPath: targetPath, Payload: payload, StdoutName: "stdout"},
		reported: &reported,
	}
}

func TestCheckOutboundLeg_AValueInsideTheStatedEntryPassesEveryPremise(t *testing.T) {
	// -2 … 2, at least one element: exactly what the artifact admits
	fixture := foreignOutboundFixture(t, -2, 2, 1)
	if outcome := checkOutboundLeg(fixture.ctx, fixture.env, fixture.edge, fixture.artifact); outcome != nil {
		t.Fatalf("a fitting crossing did not pass: %+v", outcome)
	}
	if len(*fixture.reported) != 0 {
		t.Errorf("a fitting crossing reported %d diagnostics: %+v", len(*fixture.reported), *fixture.reported)
	}
}

func TestCheckOutboundLeg_ElementsOutsideTheStatedEntryFireAtTheCall(t *testing.T) {
	// -4 … 4 is not inside the target's -2 … 2: the value can escape what
	// the target states it accepts, which is a defect in THIS program
	fixture := foreignOutboundFixture(t, -4, 4, 1)
	outcome := checkOutboundLeg(fixture.ctx, fixture.env, fixture.edge, fixture.artifact)
	if outcome == nil {
		t.Fatalf("a crossing outside the stated entry passed")
	}
	if outcome.Decline != "" {
		t.Fatalf("a crossing outside the stated entry DECLINED (%q); a fit failure is a refutation", outcome.Decline)
	}
	if len(*fixture.reported) != 1 {
		t.Fatalf("reported %d diagnostics, want exactly 1: %+v", len(*fixture.reported), *fixture.reported)
	}
	fired := (*fixture.reported)[0]
	if fired.Code != 7001 {
		t.Errorf("Code = %d, want 7001 — the target admits -2 … 2 and the value reaches ±4", fired.Code)
	}
	if !strings.Contains(fired.MessageText, "audio_level") {
		t.Errorf("the message %q does not name the target function", fired.MessageText)
	}
	// the provenance no longer rides the message text — it is the second
	// step of the two-language explanation, a related step in the Python
	// file itself
	if strings.Contains(fired.MessageText, "said:") {
		t.Errorf("the message %q still concatenates the flat provenance sentence", fired.MessageText)
	}
	if len(fired.Steps) != 1 {
		t.Fatalf("want exactly 1 related step (the target's provenance line), got %d: %+v", len(fired.Steps), fired.Steps)
	}
	step := fired.Steps[0]
	if step.ForeignFile != fixture.artifact.TargetFile {
		t.Errorf("the step's ForeignFile = %q, want the target path %q", step.ForeignFile, fixture.artifact.TargetFile)
	}
	if step.Sentence != "math.sqrt of a mean of squares of -1 … 1 is 0 … 1" {
		t.Errorf("the step's Sentence = %q, want the artifact's own provenance.said", step.Sentence)
	}
	// line 5 of foreignTargetSource is "    return math.sqrt(...)" — the
	// step's span must start there, not merely name the file
	wantStart := strings.Index(foreignTargetSource, "    return math.sqrt")
	if step.Start != wantStart {
		t.Errorf("the step's Start = %d, want %d (the start of line 5)", step.Start, wantStart)
	}
	wantLength := len("    return math.sqrt(sum(s * s for s in samples) / len(samples))")
	if step.Length != wantLength {
		t.Errorf("the step's Length = %d, want %d (line 5's own length, no trailing newline)", step.Length, wantLength)
	}
}

func TestCheckOutboundLeg_AShorterSequenceThanTheTargetReliesOnFires(t *testing.T) {
	// elements fit, but the target's body divides by len and the artifact
	// states lengthAtLeast 1 — a possibly-empty sequence is a different
	// program on the other side
	fixture := foreignOutboundFixture(t, -2, 2, 0)
	outcome := checkOutboundLeg(fixture.ctx, fixture.env, fixture.edge, fixture.artifact)
	if outcome == nil || outcome.Decline != "" {
		t.Fatalf("a too-short crossing did not fire: %+v", outcome)
	}
	if len(*fixture.reported) != 1 || (*fixture.reported)[0].Code != 7001 {
		t.Fatalf("want exactly one 7001 for the length floor: %+v", *fixture.reported)
	}
	if !strings.Contains((*fixture.reported)[0].MessageText, "at least 1") {
		t.Errorf("the message %q does not carry the target's stated length floor", (*fixture.reported)[0].MessageText)
	}
}

func TestCheckOutboundLeg_APossiblyNaNCrossingFiresNamingTheStringifyBehaviour(t *testing.T) {
	fixture := foreignOutboundFixture(t, -2, 2, 1)
	// NaN stringifies to null (§4), so the target would receive a value
	// this program never computed
	fixture.env.Set("boosted", abstractdomain.PossiblyNaN(abstractdomain.KnownSet(
		refinementsets.MakeRefinedSet(refinementsets.AtLeast(-2), refinementsets.AtMost(2)),
		nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone)))

	outcome := checkOutboundLeg(fixture.ctx, fixture.env, fixture.edge, fixture.artifact)
	if outcome == nil || outcome.Decline != "" {
		t.Fatalf("a possibly-NaN crossing did not fire: %+v", outcome)
	}
	if len(*fixture.reported) != 1 || (*fixture.reported)[0].Code != 7001 {
		t.Fatalf("want exactly one 7001 for NaN-freedom: %+v", *fixture.reported)
	}
	if !strings.Contains((*fixture.reported)[0].MessageText, "null") {
		t.Errorf("the message %q does not say what stringify does to NaN", (*fixture.reported)[0].MessageText)
	}
}

/* ── a module-level (and function-local) const array LITERAL payload ── */

// TestCheckOutboundLeg_AModuleLevelConstArrayLiteralPayloadPassesEveryPremise
// pins d-data-legs.ts's own jsonStdinCapturedStdoutRecognized shape: `const
// samples = [0.5, -0.3, 0.2];` at MODULE scope, read as the payload inside
// a function that never itself declares `samples` — the identifier
// resolves through UntrackedIdentifier's module-const follow to
// EvaluateArrayLiteral's flat KindValues{PrimitiveArray} reading, and
// checkSequenceCrossing's own sequenceCrossingOfExactTuple converts that
// tuple to the same Repetition-shaped window a declared `number[]`
// parameter wears. Every element (0.5, −0.3, 0.2) sits inside audio_level's
// stated −2 … 2, and the length (3) clears its lengthAtLeast (1): the
// crossing passes with no diagnostic.
func TestCheckOutboundLeg_AModuleLevelConstArrayLiteralPayloadPassesEveryPremise(t *testing.T) {
	fixture := foreignLiteralArrayOutboundFixture(t, "[0.5, -0.3, 0.2]", true)
	if outcome := checkOutboundLeg(fixture.ctx, fixture.env, fixture.edge, fixture.artifact); outcome != nil {
		t.Fatalf("a module-level const array literal inside the stated entry did not pass: %+v", outcome)
	}
	if len(*fixture.reported) != 0 {
		t.Errorf("a fitting module-level literal crossing reported %d diagnostics: %+v", len(*fixture.reported), *fixture.reported)
	}
}

// TestCheckOutboundLeg_AModuleConstArrayMutatedByPushFiresNaNFreedomLikeLet
// mirrors the pass test above with ONE addition: a `samples.push(...)`
// statement anywhere in the module (here, inside a second, unrelated
// function — the brief's "top level or inside another function" case).
// RULING (JT 2026-08-21): a mutation site anywhere in the module blocks
// the const-follow's array serve — UntrackedIdentifier falls to its
// last-reader path instead of handing out the stale literal, exactly the
// path TestCheckOutboundLeg_ALetBoundModuleArrayReadsByTypeAndFiresNaNFreedom
// already pins for a `let`-bound array: the identifier reads through its
// DECLARED type (a sequence of unbounded numbers, which admit NaN), and
// the NaN-freedom premise fires rather than the crossing converting the
// stale tuple.
func TestCheckOutboundLeg_AModuleConstArrayMutatedByPushFiresNaNFreedomLikeLet(t *testing.T) {
	targetPath, contentHash := writeForeignTarget(t)
	writeForeignArtifact(t, targetPath, foreignArtifactJSON(contentHash, targetPath, true))
	artifact, sentence := ReadForeignArtifact(targetPath)
	if sentence != "" {
		t.Fatalf("the fixture artifact declined: %s", sentence)
	}
	p := entryEnvTestProgram(t, "const samples = [0.5, -0.3, 0.2];\n"+
		"function f() {\n"+
		"	samples;\n"+
		"}\n"+
		"function mutateElsewhere() {\n"+
		"	samples.push(0.1);\n"+
		"}\n")
	statements := relationalAccumulationBodyOf(t, p, "f")
	payload := statements[0].AsExpressionStatement().Expression
	var reported []assignability.RefinementDiagnostic
	ctx := &FlowContext{
		P:         p,
		Kernel:    nanWrapperLoadKernel(t),
		Contracts: map[*ast.Symbol]*FunctionContract{},
		Aliases:   dataflowfacts.NewAliasClasses(),
		Declared:  map[string]*annotations.DeclaredRefinement{},
		Report:    func(d assignability.RefinementDiagnostic) { reported = append(reported, d) },
	}
	edge := &ForeignEdge{Call: payload, TargetPath: targetPath, Payload: payload, StdoutName: "stdout"}
	outcome := checkOutboundLeg(ctx, NewEnv(), edge, artifact)
	if outcome == nil {
		t.Fatalf("a mutated module const array passed with no outcome at all — the literal was served despite the push")
	}
	if outcome.Decline != "" || outcome.Override != nil {
		t.Fatalf("expected the NaN-freedom fire's empty outcome, got %+v", outcome)
	}
	if len(reported) != 1 {
		t.Fatalf("expected exactly one NaN-freedom refutation (the literal must not have been served), got %d: %+v", len(reported), reported)
	}
	if !strings.Contains(reported[0].MessageText, "JSON.stringify writes NaN as null") {
		t.Errorf("the refutation does not name the stringify behaviour: %q", reported[0].MessageText)
	}
}

// TestCheckOutboundLeg_AFunctionLocalConstArrayLiteralPayloadPassesEveryPremise
// is the function-local mirror: the SAME literal, one statement above the
// call inside the function body rather than at module scope. The payload
// identifier now resolves through env.Get directly (the ordinary tracked
// local-binding path, never UntrackedIdentifier's module-const follow) —
// evaluateExpression still reads it through EvaluateArrayLiteral's flat
// reading either way, so both routes converge on the same
// KindValues{PrimitiveArray} tuple and the same converted crossing.
func TestCheckOutboundLeg_AFunctionLocalConstArrayLiteralPayloadPassesEveryPremise(t *testing.T) {
	fixture := foreignLiteralArrayOutboundFixture(t, "[0.5, -0.3, 0.2]", false)
	if outcome := checkOutboundLeg(fixture.ctx, fixture.env, fixture.edge, fixture.artifact); outcome != nil {
		t.Fatalf("a function-local const array literal inside the stated entry did not pass: %+v", outcome)
	}
	if len(*fixture.reported) != 0 {
		t.Errorf("a fitting function-local literal crossing reported %d diagnostics: %+v", len(*fixture.reported), *fixture.reported)
	}
}

// TestCheckOutboundLeg_AModuleLevelConstArrayLiteralOutsideTheEntryFires
// pins the fit-failure side of the same conversion: a module-level const
// array literal whose own elements sit outside audio_level's stated
// −2 … 2 still fires 7001 at the call — the conversion feeds the same
// element-fit and length-floor premises checkSequenceCrossing always
// asked, it does not skip them.
func TestCheckOutboundLeg_AModuleLevelConstArrayLiteralOutsideTheEntryFires(t *testing.T) {
	fixture := foreignLiteralArrayOutboundFixture(t, "[4, -0.3, 0.2]", true)
	outcome := checkOutboundLeg(fixture.ctx, fixture.env, fixture.edge, fixture.artifact)
	if outcome == nil {
		t.Fatalf("a module-level literal crossing outside the stated entry passed")
	}
	if outcome.Decline != "" {
		t.Fatalf("a crossing outside the stated entry DECLINED (%q); a fit failure is a refutation", outcome.Decline)
	}
	if len(*fixture.reported) != 1 || (*fixture.reported)[0].Code != 7001 {
		t.Fatalf("want exactly one 7001 for the element fit: %+v", *fixture.reported)
	}
}

// TestCheckOutboundLeg_ALetBoundModuleArrayLiteralPayloadStaysUndetermined
// pins the const-only gate UntrackedIdentifier's own module-const follow
// already carries (untracked_identifier.go: `(d.Parent.Flags&ast.NodeFlagsConst)
// != 0`): a MODULE-LEVEL `let samples = [...]` the rest of the module could
// rewrite never reaches the follow, so the identifier reads through its
// DECLARED type instead — a sequence whose elements are unbounded numbers,
// which admit NaN. That reading derives a real stringify hazard, so the
// NaN-freedom premise fires the refutation and the outcome carries no
// override and no decline sentence of its own.
func TestCheckOutboundLeg_ALetBoundModuleArrayReadsByTypeAndFiresNaNFreedom(t *testing.T) {
	targetPath, contentHash := writeForeignTarget(t)
	writeForeignArtifact(t, targetPath, foreignArtifactJSON(contentHash, targetPath, true))
	artifact, sentence := ReadForeignArtifact(targetPath)
	if sentence != "" {
		t.Fatalf("the fixture artifact declined: %s", sentence)
	}
	p := entryEnvTestProgram(t, "let samples = [0.5, -0.3, 0.2];\n"+
		"function f() {\n"+
		"	samples;\n"+
		"}\n")
	statements := relationalAccumulationBodyOf(t, p, "f")
	payload := statements[0].AsExpressionStatement().Expression
	var reported []assignability.RefinementDiagnostic
	ctx := &FlowContext{
		P:         p,
		Kernel:    nanWrapperLoadKernel(t),
		Contracts: map[*ast.Symbol]*FunctionContract{},
		Aliases:   dataflowfacts.NewAliasClasses(),
		Declared:  map[string]*annotations.DeclaredRefinement{},
		Report: func(d assignability.RefinementDiagnostic) {
			reported = append(reported, d)
		},
	}
	edge := &ForeignEdge{Call: payload, TargetPath: targetPath, Payload: payload, StdoutName: "stdout"}
	outcome := checkOutboundLeg(ctx, NewEnv(), edge, artifact)
	if outcome == nil {
		t.Fatalf("a let-bound module array literal passed with no outcome at all")
	}
	if outcome.Decline != "" || outcome.Override != nil {
		t.Fatalf("expected the NaN-freedom fire's empty outcome, got %+v", outcome)
	}
	if len(reported) != 1 {
		t.Fatalf("expected exactly one NaN-freedom refutation, got %d", len(reported))
	}
	if !strings.Contains(reported[0].MessageText, "JSON.stringify writes NaN as null") {
		t.Errorf("the refutation does not name the stringify behaviour: %q", reported[0].MessageText)
	}
}

// TestCheckOutboundLeg_AModuleConstArrayWithANonLiteralElementFiresNaNFreedom
// pins the other edge of the exact shape: a module-level const array
// literal with ONE non-literal element (`gain`, a declared number) never
// reaches EvaluateArrayLiteral's flat path — flat requires every item to
// be a single-number KindValues (array_literal.go) — so
// sequenceCrossingOfExactTuple's gate never fires and the reading falls
// to a sequence whose elements admit NaN (the declared `number` carries
// the possibly-NaN wrapper). That derives the same stringify hazard, so
// the NaN-freedom premise fires rather than serving or declining.
func TestCheckOutboundLeg_AModuleConstArrayWithANonLiteralElementFiresNaNFreedom(t *testing.T) {
	targetPath, contentHash := writeForeignTarget(t)
	writeForeignArtifact(t, targetPath, foreignArtifactJSON(contentHash, targetPath, true))
	artifact, sentence := ReadForeignArtifact(targetPath)
	if sentence != "" {
		t.Fatalf("the fixture artifact declined: %s", sentence)
	}
	p := entryEnvTestProgram(t, "declare const gain: number;\n"+
		"const samples = [gain, -0.3, 0.2];\n"+
		"function f() {\n"+
		"	samples;\n"+
		"}\n")
	statements := relationalAccumulationBodyOf(t, p, "f")
	payload := statements[0].AsExpressionStatement().Expression
	var reported []assignability.RefinementDiagnostic
	ctx := &FlowContext{
		P:         p,
		Kernel:    nanWrapperLoadKernel(t),
		Contracts: map[*ast.Symbol]*FunctionContract{},
		Aliases:   dataflowfacts.NewAliasClasses(),
		Declared:  map[string]*annotations.DeclaredRefinement{},
		Report: func(d assignability.RefinementDiagnostic) {
			reported = append(reported, d)
		},
	}
	edge := &ForeignEdge{Call: payload, TargetPath: targetPath, Payload: payload, StdoutName: "stdout"}
	outcome := checkOutboundLeg(ctx, NewEnv(), edge, artifact)
	if outcome == nil {
		t.Fatalf("a module const array with a non-literal element passed with no outcome at all")
	}
	if outcome.Decline != "" || outcome.Override != nil {
		t.Fatalf("expected the NaN-freedom fire's empty outcome, got %+v", outcome)
	}
	if len(reported) != 1 {
		t.Fatalf("expected exactly one NaN-freedom refutation, got %d", len(reported))
	}
	if !strings.Contains(reported[0].MessageText, "JSON.stringify writes NaN as null") {
		t.Errorf("the refutation does not name the stringify behaviour: %q", reported[0].MessageText)
	}
}
