// The mixed shape (stdin + argv, both legs), against a real kernel —
// level_gain_argv.py's own real anatomy (samples on stdin, gain from
// argv[1]), the channel-match premise at a plain stdin-json or
// argv-scalar target, and the two single-leg-at-mixed-surface cases
// (a stdin-only call declines naming the absent argv leg; an argv-only
// call now DETERMINES rather than declines, since the target's own
// stdin read throws before the argv leg is ever consulted). Split out
// of foreign_edge_crossing_test.go, which still holds the shared
// foreignEdgeFixture type and the stdin/array-literal outbound fixture
// builders every foreign_edge_*_test.go file in this package reuses.

package walk

import (
	"strconv"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

/* ── the mixed shape (stdin + argv, both legs), against a real kernel ── */

// foreignMixedFixture builds a mixed-surface artifact (level_gain_argv.py's
// own real anatomy: samples on stdin, gain from argv[1]) and an edge whose
// Payload is a declared `samples: number[]` parameter (the stdin leg) and
// whose ArgvValue is a `const gain = <gainLiteral>;` initializer node (the
// argv leg) in the SAME function — the exact dual-channel shape the brief
// names: both an options-object `input: JSON.stringify(...)` and a
// two-element argv, recognized together rather than declined as a mix.
func foreignMixedFixture(t *testing.T, gainLiteral string) foreignEdgeFixture {
	t.Helper()
	targetPath, contentHash := writeForeignMixedTarget(t)
	writeForeignArtifact(t, targetPath, foreignMixedArtifactJSON(contentHash, targetPath))
	artifact, sentence := ReadForeignArtifact(targetPath)
	if sentence != "" {
		t.Fatalf("the fixture artifact declined: %s", sentence)
	}
	p := entryEnvTestProgram(t, "function f(samples: number[]) { samples; const gain = "+
		strconv.Quote(gainLiteral)+"; gain; }\n")
	statements := relationalAccumulationBodyOf(t, p, "f")
	payload := statements[0].AsExpressionStatement().Expression
	gainDeclaration := statements[1].AsVariableStatement().DeclarationList.
		AsVariableDeclarationList().Declarations.Nodes[0].AsVariableDeclaration()
	argvValue := gainDeclaration.Initializer
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
	env.Set("samples", abstractdomain.KnownSet(
		refinementsets.Repetition(
			refinementsets.MakeRefinedSet(refinementsets.AtLeast(-2), refinementsets.AtMost(2)), 1, nil),
		nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone))
	return foreignEdgeFixture{
		artifact: artifact,
		ctx:      ctx,
		env:      env,
		edge:     &ForeignEdge{Call: payload, TargetPath: targetPath, Payload: payload, ArgvValue: argvValue, StdoutName: "stdout"},
		reported: &reported,
	}
}

// TestCheckMixedCrossing_BothLegsInsideTheStatedEntryAreSilent pins the
// green path: samples (-2 … 2, at least 1 element) fits entry[0], and
// gain 0.5 fits entry[1]'s 0 … 4 — both legs fit, so checkOutboundLeg
// answers clean with nothing reported.
func TestCheckMixedCrossing_BothLegsInsideTheStatedEntryAreSilent(t *testing.T) {
	fixture := foreignMixedFixture(t, "0.5")
	if outcome := checkOutboundLeg(fixture.ctx, fixture.env, fixture.edge, fixture.artifact); outcome != nil {
		t.Fatalf("a fitting mixed crossing did not pass: %+v", outcome)
	}
	if len(*fixture.reported) != 0 {
		t.Errorf("a fitting mixed crossing reported %d diagnostics: %+v", len(*fixture.reported), *fixture.reported)
	}
}

// TestCheckMixedCrossing_AnOutOfSetArgvLiteralFiresOnThatLegOnly pins that
// each leg's refutation is independent: gain "9" sits outside the
// target's 0 … 4 window, and fires 7001 on the ARGV leg specifically,
// while the stdin leg (samples, in-range) is judged and passes cleanly —
// exactly one diagnostic, naming the argv leg's own values.
func TestCheckMixedCrossing_AnOutOfSetArgvLiteralFiresOnThatLegOnly(t *testing.T) {
	fixture := foreignMixedFixture(t, "9")
	outcome := checkOutboundLeg(fixture.ctx, fixture.env, fixture.edge, fixture.artifact)
	if outcome == nil {
		t.Fatalf("an out-of-set argv leg passed")
	}
	if outcome.Decline != "" {
		t.Fatalf("an out-of-set argv leg DECLINED (%q); a fit failure is a refutation", outcome.Decline)
	}
	if len(*fixture.reported) != 1 {
		t.Fatalf("reported %d diagnostics, want exactly 1 (the argv leg's own): %+v", len(*fixture.reported), *fixture.reported)
	}
	fired := (*fixture.reported)[0]
	if fired.Code != 7001 {
		t.Errorf("Code = %d, want 7001", fired.Code)
	}
	// old wording (pre house-fire reword): "the argv value crossing to
	// level_gain_argv is 9"
	if !strings.Contains(fired.MessageText, "the argv value sent to level_gain_argv is of type '9'") {
		t.Errorf("the message %q does not name the argv leg's own out-of-set value", fired.MessageText)
	}
}

// TestCheckMixedCrossing_AMixedCallAtAStdinJsonTargetDeclines pins the
// channel-match premise: a mixed call (both legs recognized) at a target
// whose fact serves plain stdin-json (never both channels) declines
// naming the surface it actually found, never silently picking one leg.
func TestCheckMixedCrossing_AMixedCallAtAStdinJsonTargetDeclines(t *testing.T) {
	targetPath, contentHash := writeForeignTarget(t)
	writeForeignArtifact(t, targetPath, foreignArtifactJSON(contentHash, targetPath, true))
	artifact, sentence := ReadForeignArtifact(targetPath)
	if sentence != "" {
		t.Fatalf("the fixture artifact declined: %s", sentence)
	}
	p := entryEnvTestProgram(t, `function f(samples: number[]) { samples; const gain = "0.5"; gain; }`+"\n")
	statements := relationalAccumulationBodyOf(t, p, "f")
	payload := statements[0].AsExpressionStatement().Expression
	gainDeclaration := statements[1].AsVariableStatement().DeclarationList.
		AsVariableDeclarationList().Declarations.Nodes[0].AsVariableDeclaration()
	argvValue := gainDeclaration.Initializer
	ctx := &FlowContext{
		P:         p,
		Kernel:    nanWrapperLoadKernel(t),
		Contracts: map[*ast.Symbol]*FunctionContract{},
		Aliases:   dataflowfacts.NewAliasClasses(),
		Declared:  map[string]*annotations.DeclaredRefinement{},
		Report:    func(assignability.RefinementDiagnostic) {},
	}
	edge := &ForeignEdge{Call: payload, TargetPath: targetPath, Payload: payload, ArgvValue: argvValue, StdoutName: "stdout"}
	outcome := checkOutboundLeg(ctx, NewEnv(), edge, artifact)
	if outcome == nil {
		t.Fatalf("a mixed call at a stdin-json target passed with no outcome at all")
	}
	if !strings.Contains(outcome.Decline, "the channels do not meet") {
		t.Errorf("Decline = %q, want the channel-mismatch sentence", outcome.Decline)
	}
	if !strings.Contains(outcome.Decline, "stdin-json") {
		t.Errorf("Decline = %q, want it to name the surface actually found (stdin-json)", outcome.Decline)
	}
}

// TestCheckOutboundLeg_AStdinOnlyCallAtAMixedSurfaceTargetDeclinesNamingTheAbsentArgvLeg
// pins the single-channel-at-mixed-surface premise: a call that sends only
// the stdin leg (no ArgvValue at all) at a target whose fact serves the
// mixed surface declines naming the ABSENT leg, never silently judging
// the one leg it has against the mixed surface's own entry[0].
func TestCheckOutboundLeg_AStdinOnlyCallAtAMixedSurfaceTargetDeclinesNamingTheAbsentArgvLeg(t *testing.T) {
	targetPath, contentHash := writeForeignMixedTarget(t)
	writeForeignArtifact(t, targetPath, foreignMixedArtifactJSON(contentHash, targetPath))
	artifact, sentence := ReadForeignArtifact(targetPath)
	if sentence != "" {
		t.Fatalf("the fixture artifact declined: %s", sentence)
	}
	p := entryEnvTestProgram(t, "function f(samples: number[]) { samples; }\n")
	fn := entryEnvFunctionNamed(t, p, "f")
	payload := fn.AsFunctionDeclaration().Body.AsBlock().Statements.Nodes[0].
		AsExpressionStatement().Expression
	ctx := &FlowContext{
		P:         p,
		Kernel:    nanWrapperLoadKernel(t),
		Contracts: map[*ast.Symbol]*FunctionContract{},
		Aliases:   dataflowfacts.NewAliasClasses(),
		Declared:  map[string]*annotations.DeclaredRefinement{},
		Report:    func(assignability.RefinementDiagnostic) {},
	}
	env := NewEnv()
	env.Set("samples", abstractdomain.KnownSet(
		refinementsets.Repetition(
			refinementsets.MakeRefinedSet(refinementsets.AtLeast(-2), refinementsets.AtMost(2)), 1, nil),
		nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone))
	// Payload set, ArgvValue nil: only the stdin leg, at a mixed-surface target
	edge := &ForeignEdge{Call: payload, TargetPath: targetPath, Payload: payload, StdoutName: "stdout"}
	outcome := checkOutboundLeg(ctx, env, edge, artifact)
	if outcome == nil {
		t.Fatalf("a stdin-only call at a mixed-surface target passed with no outcome at all")
	}
	if !strings.Contains(outcome.Decline, "the channels do not meet") {
		t.Errorf("Decline = %q, want the channel-mismatch sentence", outcome.Decline)
	}
	if !strings.Contains(outcome.Decline, "argv leg is absent") {
		t.Errorf("Decline = %q, want it to name the absent argv leg specifically", outcome.Decline)
	}
}

// TestCheckArgvCrossing_AnArgvOnlyCallAtAMixedSurfaceTargetDeterminesRatherThanDeclines
// pins d-data-legs.ts's mixedStdinAndArgvChannelUndetermined row's own
// construct: a call sending ONLY argv[1] (no stdin `input` at all) at a
// target whose fact serves the mixed stdin-json-argv-scalar surface no
// longer declines with "the channels do not meet" — the target's own
// stdin read (json.load on an EOF-empty stream, since this call writes
// no stdin bytes) throws before the argv leg is ever consulted, so
// every concrete run already fails before reaching a state this fact
// could contradict. checkArgvCrossing now judges the argv leg's OWN fit
// against entry[1] instead (argvScalarFitAgainst, the same function the
// pure argv-scalar surface and the true-mixed call both route through):
// a fitting literal answers a clean outcome (nil), exactly as if the
// channel mismatch had never been a decline at all.
func TestCheckArgvCrossing_AnArgvOnlyCallAtAMixedSurfaceTargetDeterminesRatherThanDeclines(t *testing.T) {
	targetPath, contentHash := writeForeignMixedTarget(t)
	writeForeignArtifact(t, targetPath, foreignMixedArtifactJSON(contentHash, targetPath))
	artifact, sentence := ReadForeignArtifact(targetPath)
	if sentence != "" {
		t.Fatalf("the fixture artifact declined: %s", sentence)
	}
	p := entryEnvTestProgram(t, `function f() { const gain = "0.5"; gain; }` + "\n")
	statements := relationalAccumulationBodyOf(t, p, "f")
	gainDeclaration := statements[0].AsVariableStatement().DeclarationList.
		AsVariableDeclarationList().Declarations.Nodes[0].AsVariableDeclaration()
	argvValue := gainDeclaration.Initializer
	ctx := &FlowContext{
		P:         p,
		Kernel:    nanWrapperLoadKernel(t),
		Contracts: map[*ast.Symbol]*FunctionContract{},
		Aliases:   dataflowfacts.NewAliasClasses(),
		Declared:  map[string]*annotations.DeclaredRefinement{},
		Report:    func(assignability.RefinementDiagnostic) {},
	}
	// ArgvValue set, Payload nil: only the argv leg, at a mixed-surface
	// target whose stdin the call never writes — d:115's own shape.
	edge := &ForeignEdge{Call: argvValue, TargetPath: targetPath, ArgvValue: argvValue, StdoutName: "stdout"}
	outcome := checkOutboundLeg(ctx, NewEnv(), edge, artifact)
	if outcome != nil {
		t.Fatalf("an argv-only call at a mixed-surface target with a fitting argv literal reported an outcome, want none (a clean determination): %+v", outcome)
	}
}

// TestCheckArgvCrossing_AnArgvOnlyCallAtAMixedSurfaceTargetStillFiresOnAnUnfittingLiteral
// pins the "judge the rest normally" half: even though the call always
// throws at the target's own stdin read, the argv leg's own fit is
// still a real premise — an argv literal OUTSIDE the target's stated
// entry[1] still fires 7001, exactly as it would at a pure argv-scalar
// surface.
func TestCheckArgvCrossing_AnArgvOnlyCallAtAMixedSurfaceTargetStillFiresOnAnUnfittingLiteral(t *testing.T) {
	targetPath, contentHash := writeForeignMixedTarget(t)
	writeForeignArtifact(t, targetPath, foreignMixedArtifactJSON(contentHash, targetPath))
	artifact, sentence := ReadForeignArtifact(targetPath)
	if sentence != "" {
		t.Fatalf("the fixture artifact declined: %s", sentence)
	}
	// 9 is outside the target's stated gain window (0 … 4)
	p := entryEnvTestProgram(t, `function f() { const gain = "9"; gain; }` + "\n")
	statements := relationalAccumulationBodyOf(t, p, "f")
	gainDeclaration := statements[0].AsVariableStatement().DeclarationList.
		AsVariableDeclarationList().Declarations.Nodes[0].AsVariableDeclaration()
	argvValue := gainDeclaration.Initializer
	var reported []assignability.RefinementDiagnostic
	ctx := &FlowContext{
		P:         p,
		Kernel:    nanWrapperLoadKernel(t),
		Contracts: map[*ast.Symbol]*FunctionContract{},
		Aliases:   dataflowfacts.NewAliasClasses(),
		Declared:  map[string]*annotations.DeclaredRefinement{},
		Report:    func(d assignability.RefinementDiagnostic) { reported = append(reported, d) },
	}
	edge := &ForeignEdge{Call: argvValue, TargetPath: targetPath, ArgvValue: argvValue, StdoutName: "stdout"}
	outcome := checkOutboundLeg(ctx, NewEnv(), edge, artifact)
	if outcome == nil {
		t.Fatalf("an unfitting argv literal at a mixed-surface target passed with no outcome at all")
	}
	if outcome.Decline != "" {
		t.Errorf("Decline = %q, want the fit refutation to fire rather than decline", outcome.Decline)
	}
	if len(reported) != 1 {
		t.Fatalf("expected exactly one fit refutation, got %d", len(reported))
	}
	if !strings.Contains(reported[0].MessageText, "not assignable") {
		t.Errorf("the refutation does not name the fit failure: %q", reported[0].MessageText)
	}
}
