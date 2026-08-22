// Pins the syntax-coverage row b-body-expressions.ts:724
// (class NewTargetProbe / function wrapperNewTarget): a constructor's
// own `this.caught = new.target as unknown as Age;` — `new.target`
// evaluates to a host-shaped function value (possibly absent), never a
// number, landing in a field DECLARED `Age`. No pass judged a
// constructor's own `this.key = value` writes before
// checkConstructorFieldWrites (constructor_field_writes.go) — a
// constructor never registers as a FunctionContract, and
// ConstructedInstance's own body walk silences ctx.Report on purpose
// (it exists to compute the VALUE a `new C(...)` holds, not to judge
// the constructor's text).
//
// The real fixture declares the class at MODULE TOP LEVEL (a sibling
// of the function that constructs it), and service/check.go's own
// top-level pass is what reaches it: runRefinements builds a
// topLevelCtx and calls walk.AnalyzeStatements directly over
// p.Entry.Statements.Nodes (skipping only FunctionDeclaration
// statements — check.go's own filter), which is what walks INTO a
// top-level ClassDeclaration statement and fires
// analyzeStatement's checkConstructorFieldWrites call.
// TestConstructorFieldWrites_NewTargetIntoADeclaredField used to nest
// the class INSIDE the analyzed function and drive it through
// parseVocabRun (CompileContractFileFacts + AnalyzeFunction on one
// named function) — that door never reaches a top-level
// ClassDeclaration at all, so the pinned marker silently exercised a
// different construct than the fixture spells: a local class
// declaration is walked by analyzeStatement too (the same
// ClassDeclaration arm), but only because AnalyzeFunction walks the
// function's OWN body statements, which happened to include it.
// Fixed by building the class as a genuine top-level statement and
// calling AnalyzeStatements over the entry's non-function top-level
// statements, mirroring check.go's own topLevelCtx pass exactly
// (including SnapshotOwner) — the real door the judge drives.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
)

const constructorFieldWritesHeader = `import * as z from "/surface/z.ts";
const zAge = z.number().int().min(0).max(120);
type Age = z.infer<typeof zAge>;
`

// constructorFieldWritesTopLevelRun mirrors check.go's runRefinements
// top-level pass: a FlowContext whose SnapshotOwner is the entry file
// itself, walking every top-level statement THAT IS NOT a
// FunctionDeclaration (function bodies get their own contract-body
// walk in the real judge; the class declarations in these fixtures
// never need that second pass, so leaving it out here does not admit
// a different door — it only skips work these tests do not assert on).
//
// kernel seats ctx.Kernel alone (parseVocabAssignabilityKernel) — never
// the three global kernel seats — so checkConstructorFieldWrites'
// CheckAssignability call can ask a real membership question instead of
// panicking on a nil ctx.Kernel and recovering into a decline: a
// refutation this pass owes to report needs the kernel to prove it, the
// same structural requirement set_membership.go's checkExactValues has
// everywhere else in this package.
func constructorFieldWritesTopLevelRun(t *testing.T, source string) []assignability.RefinementDiagnostic {
	t.Helper()
	kernel := parseVocabAssignabilityKernel(t)
	p := parseVocabProgram(t, source)
	registry := annotations.AnnotationRegistry{}
	objects := annotations.ObjectRegistry{}
	// the ANNOTATION pass first — production's own order
	// (CompileFileFacts): the `caught: Age` field refinement resolves
	// only through zAge's compiled annotation in the registry
	annotations.CompileAnnotationFileFacts(p, p.Entry,
		annotations.AnnotationFileFactsMerged{Registry: registry, Objects: objects},
		kernel, false, func(assignability.RefinementDiagnostic) {})
	merged := map[*ast.Symbol]*FunctionContract{}
	CompileContractFileFacts(p, p.Entry, registry, objects, merged, false, func(assignability.RefinementDiagnostic) {})
	var diagnostics []assignability.RefinementDiagnostic
	ctx := &FlowContext{
		P:         p,
		Registry:  registry,
		Objects:   objects,
		Contracts: merged,
		Report: func(d assignability.RefinementDiagnostic) {
			diagnostics = append(diagnostics, d)
		},
		Aliases:       dataflowfacts.NewAliasClasses(),
		Declared:      map[string]*annotations.DeclaredRefinement{},
		SnapshotOwner: p.Entry.AsNode(),
		Kernel:        kernel,
	}
	var statements []*ast.Node
	for _, s := range p.Entry.Statements.Nodes {
		if !ast.IsFunctionDeclaration(s) {
			statements = append(statements, s)
		}
	}
	AnalyzeStatements(ctx, NewEnv(), statements, nil)
	return diagnostics
}

// TestConstructorFieldWrites_NewTargetIntoADeclaredField pins the
// marked leg: `new.target` is host-shaped, never in Age's numeric set,
// so the constructor's own write must report 7001 at the write site.
// The class sits at MODULE TOP LEVEL, matching b-body-expressions.ts's
// own NewTargetProbe/wrapperNewTarget shape exactly.
func TestConstructorFieldWrites_NewTargetIntoADeclaredField(t *testing.T) {
	source := constructorFieldWritesHeader + `
class NewTargetProbe {
  caught: Age;
  constructor() {
    this.caught = new.target as unknown as Age;
  }
}
function wrapperNewTargetOver(): number {
  void new NewTargetProbe();
  return 0;
}
`
	diagnostics := constructorFieldWritesTopLevelRun(t, source)
	found := false
	for _, d := range diagnostics {
		if d.Code == 7001 {
			found = true
		}
	}
	if !found {
		t.Errorf("top-level NewTargetProbe reported %+v, want a 7001 at `this.caught = new.target as unknown as Age`", diagnostics)
	}
}

// TestConstructorFieldWrites_PlainNumberFieldStaysSilent is the
// negative control: a constructor writing an IN-SET literal into an
// Age-declared field reports nothing — the new judge fires only where
// the written value actually falls outside the field's own statement.
//
// OLD PREMISE: this test passed a NIL kernel to parseVocabRun — safe
// only while `wrapperConstructorFieldOk(): number`'s own plain-number
// RETURN position stated nothing (AnnotationOfType answered nil), so
// `return 0;` never asked the kernel a membership question at all.
// Now that a bare `number` keyword grounds (JT's ruling,
// annotations/type_node_sets.go), the return statement's own
// CheckAssignability DOES ask ctx.Kernel.Member(Numbers, [0]) — with a
// nil kernel that call panics on a nil pointer dereference and
// recovers into a 7002 "the kernel declined the question"
// (parseVocabAssignabilityKernel's own doc names this exact failure
// mode). A real kernel answers the trivially-true question instead,
// which is what a genuinely grounded, in-range return position should
// do — so this fix is passing a real kernel, not weakening the check.
func TestConstructorFieldWrites_PlainNumberFieldStaysSilent(t *testing.T) {
	kernel := parseVocabKernel(t)
	source := constructorFieldWritesHeader + `
function wrapperConstructorFieldOk(): number {
  class AgedBox {
    caught: Age;
    constructor() {
      this.caught = 40;
    }
  }
  void new AgedBox();
  return 0;
}
`
	diagnostics := parseVocabRun(t, kernel, source, "wrapperConstructorFieldOk")
	if len(diagnostics) != 0 {
		t.Errorf("wrapperConstructorFieldOk reported %d diagnostic(s), want none (40 is in Age's set, and 0 is in the now-grounded plain number return position): %+v", len(diagnostics), diagnostics)
	}
}

// TestConstructorFieldWrites_UnrefinedFieldTypeStaysSilent pins the
// negative control the brief calls out by name: a field typed plain
// `number` (no compilable refinement annotation — Sealed's own #age in
// the e-class-and-function.ts fixture).
//
// OLD PREMISE: "AnnotationOfType answers Stated: nil for a bare
// `number` node, and checkConstructorFieldWrites skips every field its
// read does not resolve" (asserted zero diagnostics) — now false (JT's
// ruling, annotations/type_node_sets.go's primitive-keyword arm): the
// field's own plain `number` type grounds, so the write DOES check.
//
// The check now genuinely fires ONE 7002 — not a widening bug, but
// nan_wrapper.go's own documented rule (CheckPossiblyNaN's
// AddsNothingSet gate): `age`'s own read is InitialStateOfPlainParameter's
// PossiblyNaN(Numbers) (typereading's ReadDeclaredType already grounded
// a bare `number` PARAMETER this way, independent of this fix), and a
// possibly-NaN value whose real half ADDS NOTHING beyond the plain
// number ground carries no more information than KindUnknown — the
// SAME 7002 KindUnknown itself would take (nan_wrapper.go's own
// comment: "Undetermined stays undetermined: skip straight to the 7002
// alert below"). The write is genuinely unproven NaN-safe: nothing
// here excludes NaN on either the parameter's or the field's side.
func TestConstructorFieldWrites_UnrefinedFieldTypeStaysSilent(t *testing.T) {
	kernel := parseVocabKernel(t)
	source := constructorFieldWritesHeader + `
function wrapperPlainNumberField(): number {
  class PrivateAgeHolder {
    #age: number;
    constructor(age: number) {
      this.#age = age;
    }
  }
  void new PrivateAgeHolder(200);
  return 0;
}
`
	diagnostics := parseVocabRun(t, kernel, source, "wrapperPlainNumberField")
	if len(diagnostics) != 1 {
		t.Fatalf("wrapperPlainNumberField reported %d diagnostic(s), want exactly 1 (the unproven-NaN-safety verdict on `this.#age = age`): %+v", len(diagnostics), diagnostics)
	}
	if diagnostics[0].Code != 7002 {
		t.Errorf("the one diagnostic is code %d, want 7002 (undetermined — a plain number parameter is not proven NaN-free): %+v", diagnostics[0].Code, diagnostics[0])
	}
}
