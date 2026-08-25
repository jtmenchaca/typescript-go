// The argv-value leg, against a real kernel: a scalar crossing on
// argv[1] alone (level_scalar_argv.py's own anatomy), and the
// channel-mismatch declines at both ends (an argv call at a stdin-json
// target, and the mirror — a stdin call at an argv-scalar target). Split
// out of foreign_edge_crossing_test.go, which still holds the shared
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
)

/* ── the argv-value leg, against a real kernel ───────────────────── */

// foreignArgvOutboundFixture is the argv-crossing counterpart of
// foreignOutboundFixture: an argv-scalar artifact read off disk, a
// context carrying a real kernel and a collecting sink, and an edge
// whose ArgvValue is the parsed source's own `const gain = <literalText>;`
// initializer node — the same shape d-data-legs.ts's row spells.
func foreignArgvOutboundFixture(t *testing.T, literalText string) foreignEdgeFixture {
	t.Helper()
	targetPath, contentHash := writeForeignScalarArgvTarget(t)
	writeForeignArtifact(t, targetPath, foreignArgvScalarArtifactJSON(contentHash, targetPath))
	artifact, sentence := ReadForeignArtifact(targetPath)
	if sentence != "" {
		t.Fatalf("the fixture artifact declined: %s", sentence)
	}
	p := entryEnvTestProgram(t, `function f() { const gain = `+strconv.Quote(literalText)+`; gain; }`+"\n")
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
	return foreignEdgeFixture{
		artifact: artifact,
		ctx:      ctx,
		env:      NewEnv(),
		edge:     &ForeignEdge{Call: argvValue, TargetPath: targetPath, ArgvValue: argvValue, StdoutName: "stdout"},
		reported: &reported,
	}
}

func TestCheckArgvCrossing_ALiteralInsideTheStatedEntryIsSilent(t *testing.T) {
	// 0.5 is inside the target's stated 0 … 4 gain window
	fixture := foreignArgvOutboundFixture(t, "0.5")
	if outcome := checkOutboundLeg(fixture.ctx, fixture.env, fixture.edge, fixture.artifact); outcome != nil {
		t.Fatalf("a fitting argv crossing did not pass: %+v", outcome)
	}
	if len(*fixture.reported) != 0 {
		t.Errorf("a fitting argv crossing reported %d diagnostics: %+v", len(*fixture.reported), *fixture.reported)
	}
}

func TestCheckArgvCrossing_ALiteralOutsideTheStatedEntryFiresWithThePinnedSentence(t *testing.T) {
	// 9 is outside the target's stated 0 … 4 gain window
	fixture := foreignArgvOutboundFixture(t, "9")
	outcome := checkOutboundLeg(fixture.ctx, fixture.env, fixture.edge, fixture.artifact)
	if outcome == nil {
		t.Fatalf("an out-of-set argv literal passed")
	}
	if outcome.Decline != "" {
		t.Fatalf("an out-of-set argv literal DECLINED (%q); a fit failure is a refutation", outcome.Decline)
	}
	if len(*fixture.reported) != 1 {
		t.Fatalf("reported %d diagnostics, want exactly 1: %+v", len(*fixture.reported), *fixture.reported)
	}
	fired := (*fixture.reported)[0]
	if fired.Code != 7001 {
		t.Errorf("Code = %d, want 7001", fired.Code)
	}
	// old wording (pre house-fire reword): "the argv value crossing to
	// level_from_gain is 9, and the target admits 0 … 4 — the value can
	// escape what the target states it accepts"
	wantSentence := "the argv value sent to level_from_gain is of type '9', which is not " +
		"assignable to the target's stated entry '0 … 4'"
	if !strings.Contains(fired.MessageText, "which is not assignable to the target's stated entry") {
		t.Errorf("the message %q does not carry the fit-failure sentence, want it to contain %q", fired.MessageText, wantSentence)
	}
	if !strings.Contains(fired.MessageText, "level_from_gain") {
		t.Errorf("the message %q does not name the target function", fired.MessageText)
	}
}

func TestCheckArgvCrossing_ANonLiteralArgvValueIsRecognizedUndetermined(t *testing.T) {
	targetPath, contentHash := writeForeignScalarArgvTarget(t)
	writeForeignArtifact(t, targetPath, foreignArgvScalarArtifactJSON(contentHash, targetPath))
	artifact, sentence := ReadForeignArtifact(targetPath)
	if sentence != "" {
		t.Fatalf("the fixture artifact declined: %s", sentence)
	}
	p := entryEnvTestProgram(t, "function f(gain: string) { gain; }\n")
	statements := relationalAccumulationBodyOf(t, p, "f")
	argvValue := statements[0].AsExpressionStatement().Expression
	ctx := &FlowContext{
		P:         p,
		Kernel:    nanWrapperLoadKernel(t),
		Contracts: map[*ast.Symbol]*FunctionContract{},
		Aliases:   dataflowfacts.NewAliasClasses(),
		Declared:  map[string]*annotations.DeclaredRefinement{},
		Report:    func(assignability.RefinementDiagnostic) {},
	}
	edge := &ForeignEdge{Call: argvValue, TargetPath: targetPath, ArgvValue: argvValue, StdoutName: "stdout"}
	outcome := checkOutboundLeg(ctx, NewEnv(), edge, artifact)
	if outcome == nil {
		t.Fatalf("a non-literal argv value passed with no outcome at all")
	}
	if outcome.Decline != "the argv value is not a written string literal; its parsed value cannot be pinned" {
		t.Errorf("Decline = %q, want the recognized-undetermined sentence naming the construct", outcome.Decline)
	}
}

func TestCheckArgvCrossing_ANaNLiteralIsRefused(t *testing.T) {
	fixture := foreignArgvOutboundFixture(t, "nan")
	outcome := checkOutboundLeg(fixture.ctx, fixture.env, fixture.edge, fixture.artifact)
	if outcome == nil || outcome.Decline != "" {
		t.Fatalf("a NaN argv literal did not fire: %+v", outcome)
	}
	if len(*fixture.reported) != 1 || (*fixture.reported)[0].Code != 7001 {
		t.Fatalf("want exactly one 7001 for NaN-freedom: %+v", *fixture.reported)
	}
	if !strings.Contains((*fixture.reported)[0].MessageText, "NaN") {
		t.Errorf("the message %q does not name NaN", (*fixture.reported)[0].MessageText)
	}
}

func TestCheckArgvCrossing_AParseFailureFiresNamingTheParseFailure(t *testing.T) {
	fixture := foreignArgvOutboundFixture(t, "--fast")
	outcome := checkOutboundLeg(fixture.ctx, fixture.env, fixture.edge, fixture.artifact)
	if outcome == nil || outcome.Decline != "" {
		t.Fatalf("an unparseable argv literal did not fire: %+v", outcome)
	}
	if len(*fixture.reported) != 1 || (*fixture.reported)[0].Code != 7001 {
		t.Fatalf("want exactly one 7001 for the parse failure: %+v", *fixture.reported)
	}
	if !strings.Contains((*fixture.reported)[0].MessageText, "does not parse as a Python float()") {
		t.Errorf("the message %q does not name the parse failure", (*fixture.reported)[0].MessageText)
	}
}

func TestCheckArgvCrossing_AnArgvCallAtAStdinJsonTargetDeclinesWithTheChannelMismatchSentence(t *testing.T) {
	targetPath, contentHash := writeForeignTarget(t)
	writeForeignArtifact(t, targetPath, foreignArtifactJSON(contentHash, targetPath, true))
	artifact, sentence := ReadForeignArtifact(targetPath)
	if sentence != "" {
		t.Fatalf("the fixture artifact declined: %s", sentence)
	}
	p := entryEnvTestProgram(t, `function f() { const gain = "0.5"; gain; }`+"\n")
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
	edge := &ForeignEdge{Call: argvValue, TargetPath: targetPath, ArgvValue: argvValue, StdoutName: "stdout"}
	outcome := checkOutboundLeg(ctx, NewEnv(), edge, artifact)
	if outcome == nil {
		t.Fatalf("an argv call at a stdin-json target passed with no outcome at all")
	}
	want := "the call passes the value as argv[1], but the target's fact serves JSON on stdin — " +
		"the channels do not meet"
	if outcome.Decline != want {
		t.Errorf("Decline = %q, want %q", outcome.Decline, want)
	}
}

func TestCheckOutboundLeg_AStdinCallAtAnArgvScalarTargetDeclinesWithTheMirroredChannelMismatchSentence(t *testing.T) {
	targetPath, contentHash := writeForeignScalarArgvTarget(t)
	writeForeignArtifact(t, targetPath, foreignArgvScalarArtifactJSON(contentHash, targetPath))
	artifact, sentence := ReadForeignArtifact(targetPath)
	if sentence != "" {
		t.Fatalf("the fixture artifact declined: %s", sentence)
	}
	p := entryEnvTestProgram(t, "function f(gain: number) { gain; }\n")
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
	env.Set("gain", abstractdomain.KnownValues([]float64{1}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved))
	// Payload set, ArgvValue nil: the ordinary stdin shape, at a target
	// whose surface is argv-scalar
	edge := &ForeignEdge{Call: payload, TargetPath: targetPath, Payload: payload, StdoutName: "stdout"}
	outcome := checkOutboundLeg(ctx, env, edge, artifact)
	if outcome == nil {
		t.Fatalf("a stdin call at an argv-scalar target passed with no outcome at all")
	}
	want := "the call passes the value as JSON on stdin, but the target's fact serves an argv[1] " +
		"scalar — the channels do not meet"
	if outcome.Decline != want {
		t.Errorf("Decline = %q, want %q", outcome.Decline, want)
	}
}
