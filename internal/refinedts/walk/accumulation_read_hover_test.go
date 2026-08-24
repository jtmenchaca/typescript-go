// Pins AnswerFlowAt's answer for a READ position of the accumulator
// name sitting INSIDE the division statement itself — hovering `total`
// in `const mean = total / clamped.length`, not its declaration name.
// reach_to_token.go's AnalyzeToToken only runs the loop-and-division
// pairing (RelationalAccumulationOf, consumed inside listWalk) when
// both statements sit together in the SAME slice a list walk sees. A
// WRITE position at the division's own bound name gets that slice
// through the writes-branch (holder appended to prefix, sharing one
// AnalyzeStatements call). A READ position inside the division's own
// statement does not: AnalyzeToToken's non-write branch walks the
// PREFIX ONLY (excluding holder) through AnalyzeStatements, then
// descends into holder alone through enterToToken, which never runs
// listWalk's pairing at all — so the loop is walked without its
// division partner in the same slice, and RelationalAccumulationOf's
// own index+1 bound (relational_accumulation.go) never fires.
//
// The fix's own probe (reach_to_token.go's pairsWithPrecedingLoop)
// walks a CLONE of env through `prefix[:loopIndex]` — everything
// before the loop — because RelationalAccumulationOf reads the
// accumulator's value as it stands right before the loop runs, not
// the site's raw entry state. The single-loop fixture above has an
// EMPTY pre-loop prefix (`let total = 0` IS the pairing's own loop
// partner's predecessor, not a statement ahead of it), so it never
// exercises that walk-through. AnAccumulationWithAnUnrelatedStatement
// below adds one (`const label = "clamp"`) so loopIndex > 0 and the
// probe's own prefix[:loopIndex] slice is non-empty.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
)

// TestAnswerFlowAt_AReadInsideTheDivisionStatementServesThePostLoopClaim
// hovers `total` at its READ occurrence inside the division statement
// (`const mean = total / clamped.length`), not the declaration name
// accumulation_declaration_hover_test.go already pins. The served value
// must be the SAME post-loop claim the declaration-position test pins
// — never the entry value {0} — because both positions read the same
// binding's state at a point after the loop ran.
func TestAnswerFlowAt_AReadInsideTheDivisionStatementServesThePostLoopClaim(t *testing.T) {
	kernel := accumulationHoverLoadKernel(t)
	names := accumulationHoverSetup(t, kernel, accumulationHoverSource)

	// statements[2] is `const mean = total / clamped.length` — its
	// initializer is the binary division `total / clamped.length`,
	// whose Left operand is the READ occurrence of `total` this test
	// hovers.
	meanDeclStatement := names.Statements[2]
	initializer := meanDeclStatement.AsVariableStatement().DeclarationList.
		AsVariableDeclarationList().Declarations.Nodes[0].AsVariableDeclaration().Initializer
	if initializer == nil || !ast.IsBinaryExpression(initializer) {
		t.Fatalf("mean's initializer is not a binary expression: %+v", initializer)
	}
	totalRead := initializer.AsBinaryExpression().Left
	if totalRead == nil || !ast.IsIdentifier(totalRead) || totalRead.Text() != "total" {
		t.Fatalf("division's left operand is not the `total` read: %+v", totalRead)
	}

	// the SAME registries accumulationHoverSetup compiled zClamped's
	// z.array(...) chain into — a fresh empty pair resolves
	// z.infer<typeof zClamped> to nothing, leaving `clamped`'s element
	// bound Top and declining the relational-accumulation route's own
	// element-state gate (relational_accumulation.go's
	// accumulationProgram) before the kernel is ever asked.
	readAnswer := AnswerFlowAt(names.P, names.Registry, names.Objects, names.Contracts, totalRead)
	if !readAnswer.HasKnown {
		t.Fatalf("read-position answer for total: HasKnown = false (%+v), want a served claim", readAnswer.UnknownValue)
	}
	if readAnswer.Known == "0" {
		t.Fatalf("read-position answer for total = %q, still the entry value — the loop-and-division pairing never reached it", readAnswer.Known)
	}

	// cross-checked against the reference: walking the whole scope the
	// ordinary way (walkedEnvOf, the same call listWalk's pairing runs
	// through) must leave `total` admitting exactly the same two facts.
	finalEnv := walkedEnvOf(t, names)
	finalTotal, ok := finalEnv.Get("total")
	if !ok {
		t.Fatalf("env[total] missing after walking the whole scope")
	}
	finalState, ok := StateOfKnown(finalTotal)
	if !ok || finalState.Top {
		t.Fatalf("StateOfKnown(final total) = %+v, %v, want a non-top state", finalState, ok)
	}
	if !kernel.Member(finalState.Set, []float64{0}) {
		t.Errorf("member(final total, [0]) = false, want true — the reference walk must still admit 0")
	}
	if kernel.Member(finalState.Set, []float64{2}) {
		t.Errorf("member(final total, [2]) = true, want false — the reference walk must rule out 2")
	}
}

// accumulationReadHoverUnrelatedPrefixSource is accumulationHoverSource
// with ONE statement ahead of the accumulator's own declaration — a
// binding the accumulation names nowhere at all. Its only job is to
// make the loop's own index in the statement list greater than zero,
// so AnalyzeToToken's prefix (everything before the division) holds a
// statement before the loop, and pairsWithPrecedingLoop's own probe
// (reach_to_token.go) must walk that statement through
// `AnalyzeStatements(ctx, probeEnv, prefix[:loopIndex], nil)` before
// asking RelationalAccumulationOf — the walk-through the single-loop
// fixture above never exercises. `clamped` carries the SAME stated
// element bound accumulationHoverSource does, for the same reason:
// RelationalAccumulationOf's accumulationProgram gate declines over an
// unbounded (Top) element state, which a bare `number[]` parameter
// carries with no stated window anywhere in the source. The exact
// length ceiling (`.min(1).max(1)`) carries the Repeat form with its
// element intact — see accumulationHoverSource's own comment
// (accumulation_declaration_hover_test.go) for why.
const accumulationReadHoverUnrelatedPrefixSource = `
import * as z from "/surface/z.ts";
const zClamped = z.array(z.number().min(-1).max(1)).min(1).max(1);
function level(clamped: z.infer<typeof zClamped>): number {
	const label = "clamp";
	let total = 0;
	for (const s of clamped) { total += s * s; }
	const mean = total / clamped.length;
	return mean;
}
`

// accumulationReadHoverUnrelatedPrefixNames mirrors accumulationHoverSetup
// for the shifted statement list: statements[0] is the unrelated
// `const label`, [1] `let total = 0`, [2] the loop, [3] the division.
// Registry and Objects are the SAME populated registries this setup's
// own CompileAnnotationFileFacts call filled — see accumulationHoverNames'
// own comment (accumulation_declaration_hover_test.go) for why a fresh
// empty pair at the call site loses zClamped's compiled chain.
type accumulationReadHoverUnrelatedPrefixNames struct {
	P          *program.CheckerProgram
	Registry   annotations.AnnotationRegistry
	Objects    annotations.ObjectRegistry
	Contracts  map[*ast.Symbol]*FunctionContract
	Statements []*ast.Node
}

func accumulationReadHoverUnrelatedPrefixSetup(t *testing.T, kernel *kernelbridge.RefinedTSKernel) accumulationReadHoverUnrelatedPrefixNames {
	t.Helper()
	// parseVocabProgram, not entryEnvTestProgram — see accumulationHoverSetup's
	// own comment (accumulation_declaration_hover_test.go): this fixture
	// imports "/surface/z.ts" too, and only parseVocabProgram registers
	// that file with the SurfacePaths marker the zod recognizer reads.
	p := parseVocabProgram(t, accumulationReadHoverUnrelatedPrefixSource)
	fn := parseVocabFunctionNamed(t, p, "level")
	body := fn.AsFunctionDeclaration().Body
	if body == nil || !ast.IsBlock(body) {
		t.Fatalf("level has no block body")
	}
	statements := body.AsBlock().Statements.Nodes
	// statements[0] `const label = "clamp"`, [1] `let total = 0`,
	// [2] the loop, [3] `const mean = total / clamped.length`
	if len(statements) < 4 {
		t.Fatalf("level has %d statements, want at least 4", len(statements))
	}
	registry := annotations.AnnotationRegistry{}
	objects := annotations.ObjectRegistry{}
	// the ANNOTATION pass first — see accumulationHoverSetup's own
	// comment: without it `zClamped`'s z.array(...) chain never lands in
	// the registry, and `z.infer<typeof zClamped>` resolves to nothing.
	annotations.CompileAnnotationFileFacts(p, p.Entry,
		annotations.AnnotationFileFactsMerged{Registry: registry, Objects: objects},
		kernel, false, func(assignability.RefinementDiagnostic) {})
	merged := map[*ast.Symbol]*FunctionContract{}
	contracts := CompileContractFileFacts(p, p.Entry, registry, objects, merged, false, func(assignability.RefinementDiagnostic) {})
	return accumulationReadHoverUnrelatedPrefixNames{
		P: p, Registry: registry, Objects: objects, Contracts: contracts, Statements: statements,
	}
}

// TestAnswerFlowAt_AReadPastAnUnrelatedPrefixStatementStillServesThePostLoopClaim
// pins the probe-env guard directly: with a statement ahead of the
// loop (loopIndex > 0 inside pairsWithPrecedingLoop), the read-position
// hover on `total` inside the division statement must still serve the
// post-loop claim, not the entry value — the guard must actually walk
// `prefix[:loopIndex]` into the probe env rather than asking
// RelationalAccumulationOf over an unwalked one.
func TestAnswerFlowAt_AReadPastAnUnrelatedPrefixStatementStillServesThePostLoopClaim(t *testing.T) {
	kernel := accumulationHoverLoadKernel(t)
	names := accumulationReadHoverUnrelatedPrefixSetup(t, kernel)

	divisionStatement := names.Statements[3]
	initializer := divisionStatement.AsVariableStatement().DeclarationList.
		AsVariableDeclarationList().Declarations.Nodes[0].AsVariableDeclaration().Initializer
	if initializer == nil || !ast.IsBinaryExpression(initializer) {
		t.Fatalf("mean's initializer is not a binary expression: %+v", initializer)
	}
	totalRead := initializer.AsBinaryExpression().Left
	if totalRead == nil || !ast.IsIdentifier(totalRead) || totalRead.Text() != "total" {
		t.Fatalf("division's left operand is not the `total` read: %+v", totalRead)
	}

	// the SAME populated registries again — see the setup's own comment.
	readAnswer := AnswerFlowAt(names.P, names.Registry, names.Objects, names.Contracts, totalRead)
	if !readAnswer.HasKnown {
		t.Fatalf("read-position answer for total: HasKnown = false (%+v), want a served claim", readAnswer.UnknownValue)
	}
	if readAnswer.Known == "0" {
		t.Fatalf("read-position answer for total = %q, still the entry value — the probe's walk-through of the unrelated prefix statement never reached the pairing", readAnswer.Known)
	}

	ctx := &FlowContext{
		P:         names.P,
		Contracts: names.Contracts,
		Report:    func(assignability.RefinementDiagnostic) {},
		Aliases:   dataflowfacts.NewAliasClasses(),
		Declared:  map[string]*annotations.DeclaredRefinement{},
	}
	fn := parseVocabFunctionNamed(t, names.P, "level")
	env := NewEnv()
	// StatedParams from level's own compiled contract — see walkedEnvOf's
	// own comment (accumulation_declaration_hover_test.go): without it
	// the plain-type path cannot resolve z.infer<typeof zClamped>, and
	// this reference walk would disagree with AnswerFlowAt's own binding
	// for no reason but an incomplete setup here.
	var statedParams []*annotations.DeclaredRefinement
	if contract := contractFor(names.Contracts, fn); contract != nil {
		statedParams = contract.Params
	}
	BindEntryEnv(BindEntryEnvInput{
		P: names.P, Env: env, Parameters: fn.AsFunctionDeclaration().Parameters.Nodes,
		StatedParams: statedParams,
	})
	AnalyzeStatements(ctx, env, names.Statements, nil)
	finalTotal, ok := env.Get("total")
	if !ok {
		t.Fatalf("env[total] missing after walking the whole scope")
	}
	finalState, ok := StateOfKnown(finalTotal)
	if !ok || finalState.Top {
		t.Fatalf("StateOfKnown(final total) = %+v, %v, want a non-top state", finalState, ok)
	}
	if !kernel.Member(finalState.Set, []float64{0}) {
		t.Errorf("member(final total, [0]) = false, want true — the reference walk must still admit 0")
	}
	if kernel.Member(finalState.Set, []float64{2}) {
		t.Errorf("member(final total, [2]) = true, want false — the reference walk must rule out 2")
	}
}

// The two-step seed shape (`let total; total = 0;` splitting the
// accumulator's declaration from its exact write across two
// statements) is NOT pinned here. accumulationLoopOf reads only the
// loop statement's own text and accumulationProgram's EXACT-start gate
// (relational_accumulation.go) reads env.Get(loop.TotalName)'s
// RESULTING value at the loop's own index — neither inspects how many
// statements wrote it or the declaration's own shape — so tracing the
// code says a plain `total = 0` assignment should leave `total` at
// KindValues{0} the same as an initializer would, and the pairing
// should fire exactly as it does for the single-declaration fixture.
// That trace is not execution: this suite could not be run here
// (edit-only agent, no shell), so the two-step shape is left as a
// traced-but-unverified expectation rather than committed as an
// asserted pass. The unrelated-prefix fixture above is the pin that
// IS verified by inspection to exercise the same probe-env
// walk-through without resting on that unverified step.
