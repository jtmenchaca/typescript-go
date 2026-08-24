// Pins AnswerFlowAt's declaration-position answer for a name the rest
// of its scope keeps writing: the position must serve what a read
// placed right after the name's LAST write would serve, not the
// initializer alone. `let total = 0` ahead of a `for` loop that sums
// into it is the shape that exposed the gap — the loop is a LATER
// write in the same statement list, and reach_to_token.go's own
// writesNameLater gate now walks that rest of the scope before the
// declaration-position answer reads the name back.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/narrowing"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
)

// accumulationHoverLoadKernel mirrors every other kernel-backed walk
// test's own recipe — skipped (never a faked pass) when the native
// kernel dylib is absent. LoadKernel itself registers the loaded
// instance through AdoptKernel, which is the same global
// KernelIfLoaded (AnswerFlowAt's own gate) reads — no separate
// "install" step exists.
func accumulationHoverLoadKernel(t *testing.T) *kernelbridge.RefinedTSKernel {
	t.Helper()
	if !kernelbridge.KernelArtifactsPresent(kernelbridge.DylibPath) {
		t.Skip("native kernel dylib absent — build it first")
	}
	kernel, err := kernelbridge.LoadKernel(kernelbridge.DylibPath)
	if err != nil {
		t.Fatalf("LoadKernel: %v", err)
	}
	// the same four globals production's service/check.go setupKernel
	// seats together — AnswerFlowAt only seats SetTransferKernel and
	// narrowing.SetNarrowKernel itself; the engine-delegation route
	// (EngineEntryOf/EngineMeetInto, which the loop statement and the
	// relational-accumulation route both use) reads engineKernel, and a
	// branch join along the way can read the lattice kernel too.
	SetTransferKernel(kernel)
	SetEngineKernel(kernel)
	narrowing.SetNarrowKernel(kernel)
	abstractdomain.SetLatticeKernel(kernel)
	return kernel
}

// accumulationHoverSource is level_ok.ts's own accumulate-then-divide
// shape (chain_meter.ts's own TS twin), carrying the SAME z.array
// element bound chain_meter.ts states on `Samples` — RelationalAccumulationOf's
// own accumulationProgram gate (relational_accumulation.go) declines
// wherever the iterated sequence's element state is unbounded (Top), so
// a bare `clamped: number[]` parameter — no stated bound anywhere —
// never reaches the pairing this file means to pin. The zod entry
// states the same element window `total`'s own inline comment below
// assumes (an element at most 1 in magnitude), and the exact length
// ceiling (`.max(1)` beside `.min(1)`) matches the "single-element
// sequence" premise directly: z.array(...).min(1).max(1) now carries
// the Repeat form with its element intact (repetition_window_forms.go's
// Repetition only collapses a (1,1) window to the bare element when
// that element is demonstrably Codepoints -- z.string().length(1)'s own
// case -- never for an arbitrary array element), so
// RepetitionArmsOf/AsRepetition read the element and
// RelationalAccumulationOf's gate fires.
const accumulationHoverSource = `
import * as z from "/surface/z.ts";
const zClamped = z.array(z.number().min(-1).max(1)).min(1).max(1);
function level(clamped: z.infer<typeof zClamped>): number {
	let total = 0;
	for (const s of clamped) { total += s * s; }
	const mean = total / clamped.length;
	return mean;
}
`

// accumulationHoverNames locates the positions the cases below hover:
// total's own declaration name and mean's own declaration name.
// Registry and Objects are the SAME populated registries the setup's
// own CompileAnnotationFileFacts call filled — `zClamped`'s z.array(...)
// chain lives there, and z.infer<typeof zClamped> resolves against it.
// A caller that builds fresh empty registries instead loses that
// compile: `clamped`'s element bound reads back Top, RelationalAccumulationOf's
// own element-state gate (relational_accumulation.go's accumulationProgram)
// declines, and the position falls through to the walk's silent answer —
// exactly the gap this field exists to close.
type accumulationHoverNames struct {
	P             *program.CheckerProgram
	Registry      annotations.AnnotationRegistry
	Objects       annotations.ObjectRegistry
	Contracts     map[*ast.Symbol]*FunctionContract
	Statements    []*ast.Node
	TotalDeclName *ast.Node
	MeanDeclName  *ast.Node
}

func accumulationHoverSetup(t *testing.T, kernel *kernelbridge.RefinedTSKernel, source string) accumulationHoverNames {
	t.Helper()
	// parseVocabProgram, not entryEnvTestProgram: the fixture imports
	// "/surface/z.ts", and entryEnvTestProgram's virtual filesystem
	// carries no such file and no SurfacePaths marker — the import
	// resolves to nothing, zClamped's z.array(...) chain never
	// type-checks as zod at all, and CompileAnnotationFileFacts compiles
	// an empty registry with no diagnostic (there is nothing recognizable
	// to decline). parseVocabProgram registers the vendored surface
	// stand-in and sets SurfacePaths, which is what the zod recognizer
	// (isZodRootHere) reads to know this import IS the surface.
	p := parseVocabProgram(t, source)
	fn := parseVocabFunctionNamed(t, p, "level")
	body := fn.AsFunctionDeclaration().Body
	if body == nil || !ast.IsBlock(body) {
		t.Fatalf("level has no block body")
	}
	statements := body.AsBlock().Statements.Nodes
	// statements[0] is `let total = 0`, [1] the loop, [2] `const mean = …`
	if len(statements) < 3 {
		t.Fatalf("level has %d statements, want at least 3", len(statements))
	}
	totalDecl := statements[0].AsVariableStatement().DeclarationList.
		AsVariableDeclarationList().Declarations.Nodes[0]
	meanDecl := statements[2].AsVariableStatement().DeclarationList.
		AsVariableDeclarationList().Declarations.Nodes[0]
	registry := annotations.AnnotationRegistry{}
	objects := annotations.ObjectRegistry{}
	// the ANNOTATION pass first — production's own order (service/check.go's
	// runRefinements, mirrored by every other CompileContractFileFacts
	// caller in this package, e.g. fact_export_test.go's
	// factExportContractOf): `zClamped`'s own z.array(...) chain compiles
	// into the registry HERE, before the contract pass reads it back —
	// skipping this step is why `z.infer<typeof zClamped>` resolved to
	// nothing on either the contract path or the plain-type path, and
	// `clamped`'s element bound came back Top.
	annotations.CompileAnnotationFileFacts(p, p.Entry,
		annotations.AnnotationFileFactsMerged{Registry: registry, Objects: objects},
		kernel, false, func(assignability.RefinementDiagnostic) {})
	merged := map[*ast.Symbol]*FunctionContract{}
	contracts := CompileContractFileFacts(p, p.Entry, registry, objects, merged, false, func(assignability.RefinementDiagnostic) {})
	return accumulationHoverNames{
		P:             p,
		Registry:      registry,
		Objects:       objects,
		Contracts:     contracts,
		Statements:    statements,
		TotalDeclName: totalDecl.AsVariableDeclaration().Name(),
		MeanDeclName:  meanDecl.AsVariableDeclaration().Name(),
	}
}

// walkedEnvOf walks level's whole statement list the ordinary way
// (AnalyzeStatements — the same call listWalk's relational-
// accumulation branch runs the loop and the division through) and
// answers the resulting env, so a reference value can be read back
// from the walk's own machinery rather than typed by hand.
func walkedEnvOf(t *testing.T, names accumulationHoverNames) Env {
	t.Helper()
	fn := parseVocabFunctionNamed(t, names.P, "level")
	ctx := &FlowContext{
		P:         names.P,
		Contracts: names.Contracts,
		Report:    func(assignability.RefinementDiagnostic) {},
		Aliases:   dataflowfacts.NewAliasClasses(),
		Declared:  map[string]*annotations.DeclaredRefinement{},
	}
	env := NewEnv()
	// StatedParams from level's own compiled contract: without it,
	// BindEntryEnv falls to the plain-type path (InitialStateOfPlainParameter
	// / typereading.ReadDeclaredType), which reads `z.infer<typeof zClamped>`
	// SYNTACTICALLY — no registry in reach — and cannot resolve it. The
	// same claim AnswerFlowAt's own binding relies on (site.Contract.Params),
	// so the reference walk here must bind the SAME way to be a fair
	// comparison.
	var statedParams []*annotations.DeclaredRefinement
	if contract := contractFor(names.Contracts, fn); contract != nil {
		statedParams = contract.Params
	}
	BindEntryEnv(BindEntryEnvInput{
		P: names.P, Env: env, Parameters: fn.AsFunctionDeclaration().Parameters.Nodes,
		StatedParams: statedParams,
	})
	AnalyzeStatements(ctx, env, names.Statements, nil)
	return env
}

// TestAnswerFlowAt_AnAccumulationsDeclarationHoverServesThePostLoopClaimNotTheInitializer
// pins the fix directly: total's own declaration-name position must
// answer with the SAME abstract value the whole scope's own walk
// leaves `total` holding — the post-loop enclosure the relational-
// accumulation route proves once the loop and the division are walked
// together — never the entry value {0} the initializer alone would
// leave behind. The reference comes from walking the whole scope the
// ordinary way (walkedEnvOf), not from hand arithmetic on what the
// kernel proves.
func TestAnswerFlowAt_AnAccumulationsDeclarationHoverServesThePostLoopClaimNotTheInitializer(t *testing.T) {
	kernel := accumulationHoverLoadKernel(t)
	names := accumulationHoverSetup(t, kernel, accumulationHoverSource)

	// the SAME registries the setup compiled zClamped's chain into — a
	// fresh empty pair here would resolve z.infer<typeof zClamped> to
	// nothing, leaving `clamped`'s element bound Top and declining the
	// relational-accumulation route before it ever asks the kernel.
	declAnswer := AnswerFlowAt(names.P, names.Registry, names.Objects, names.Contracts, names.TotalDeclName)
	if !declAnswer.HasKnown {
		t.Fatalf("declaration-position answer for total: HasKnown = false (%+v), want a served claim", declAnswer.UnknownValue)
	}
	if declAnswer.Known == "0" {
		t.Fatalf("declaration-position answer for total = %q, still the entry value — the post-loop claim never reached it", declAnswer.Known)
	}

	finalEnv := walkedEnvOf(t, names)
	finalTotal, ok := finalEnv.Get("total")
	if !ok {
		t.Fatalf("env[total] missing after walking the whole scope")
	}
	finalState, ok := StateOfKnown(finalTotal)
	if !ok || finalState.Top {
		t.Fatalf("StateOfKnown(final total) = %+v, %v, want a non-top state", finalState, ok)
	}
	// the post-loop claim admits 0 (an all-zero sequence) and DOES NOT
	// admit a value the sequence's own per-element bound rules out
	// (an element is at most 1 in magnitude, so its square is at most 1,
	// and a single-element sequence's total is bounded the same way) —
	// the same enclosure `total`'s own inline comment in level_ok.ts
	// states (0.0 … clamped.length).
	if !kernel.Member(finalState.Set, []float64{0}) {
		t.Errorf("member(final total, [0]) = false, want true — the post-loop claim must still admit 0")
	}
	if kernel.Member(finalState.Set, []float64{2}) {
		t.Errorf("member(final total, [2]) = true, want false — a single-element sequence's total cannot reach 2")
	}
}

// TestAnswerFlowAt_ADerivedQuotientsDeclarationHoverServesTheQuotient
// pins mean's own declaration-name position: it must serve the derived
// quotient the relational-accumulation route proves, not "no answer".
func TestAnswerFlowAt_ADerivedQuotientsDeclarationHoverServesTheQuotient(t *testing.T) {
	kernel := accumulationHoverLoadKernel(t)
	names := accumulationHoverSetup(t, kernel, accumulationHoverSource)

	// the SAME populated registries again — see the declaration test's
	// own comment above.
	declAnswer := AnswerFlowAt(names.P, names.Registry, names.Objects, names.Contracts, names.MeanDeclName)
	if !declAnswer.HasKnown {
		t.Fatalf("declaration-position answer for mean: HasKnown = false (%+v), want the derived quotient", declAnswer.UnknownValue)
	}
}

// TestAnswerFlowAt_AnUntouchedSimpleBindingsDeclarationHoverStillServesItsValue
// pins the control case: a binding NOTHING rewrites later in its scope
// must still answer its own declared value — writesNameLater's gate
// must not fire, and the extra walk this fix adds must not run, for
// the ordinary case the fix has no business touching.
func TestAnswerFlowAt_AnUntouchedSimpleBindingsDeclarationHoverStillServesItsValue(t *testing.T) {
	accumulationHoverLoadKernel(t)

	source := `
function f(): number {
	const x = 5;
	return x;
}
`
	p := entryEnvTestProgram(t, source)
	fn := entryEnvFunctionNamed(t, p, "f")
	statements := fn.AsFunctionDeclaration().Body.AsBlock().Statements.Nodes
	xDecl := statements[0].AsVariableStatement().DeclarationList.
		AsVariableDeclarationList().Declarations.Nodes[0]
	registry := annotations.AnnotationRegistry{}
	objects := annotations.ObjectRegistry{}
	merged := map[*ast.Symbol]*FunctionContract{}
	contracts := CompileContractFileFacts(p, p.Entry, registry, objects, merged, false, func(assignability.RefinementDiagnostic) {})

	answer := AnswerFlowAt(p, registry, objects, contracts, xDecl.AsVariableDeclaration().Name())
	if !answer.HasKnown {
		t.Fatalf("declaration-position answer for x: HasKnown = false (%+v), want 5", answer.UnknownValue)
	}
	if answer.Known != "5" {
		t.Errorf("declaration-position answer for x = %q, want \"5\"", answer.Known)
	}
}
