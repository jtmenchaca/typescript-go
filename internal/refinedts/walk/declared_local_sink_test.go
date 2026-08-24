// Pins the e2e sink.assign rows (A1/A2/A3/A5.sink.assign): the
// designation sits directly above `const a: Age = x;` — the assignment
// the row's own claim is about — and the reported 7001 lands at that
// declaration. AnalyzeVariableStatement (variable_statement.go) routes
// the declared local's initializer through WriteBinding, which calls
// CheckAssignability against the declared type (assignments.go), and
// the diagnostic's span is the declaration's own. After the refused
// write the binding keeps the DECLARED set (the refused-write law), so
// the following `return a;` is silent rather than a second fire.
package walk

import (
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

const declaredLocalSinkHeader = `import * as z from "/surface/z.ts";
const zAge = z.number().int().min(0).max(150);
type Age = z.infer<typeof zAge>;
const zWide = z.number().int().min(0).max(200);
type Wide = z.infer<typeof zWide>;
`

// declaredLocalSinkRun mirrors parseVocabRun (parse_and_chain_vocabulary_
// test.go) with the ANNOTATION pass run FIRST, production's own order
// (CompileFileFacts) — eRowRun's own recipe
// (e_class_and_function_accessor_rows_test.go). A `const a: Age = x;`
// declaration's `: Age` annotation resolves ONLY through the populated
// registry (Age = z.infer<typeof zAge> resolves through zAge's compiled
// annotation, variable_statement.go's AnnotationOfType call) — the bare
// .parse-vocabulary runner never needs this because EvaluateParseOutcome
// (parse_evaluator.go) walks a `.parse` call's own chain syntactically
// off the AST node, never through the registry. Without this pass,
// AnnotationOfType(decl.Type) reports 7004 ("'zAge'/'zWide' is not a
// stated annotation the checker read") instead of resolving Age/Wide's
// window, and WriteBinding's refused-write law never runs at all. Keeps
// parseVocabKernel's full three global seats (unlike eRowRun's isolated
// parseVocabAssignabilityKernel) — these tests exercise the ordinary
// WriteBinding/CheckAssignability refusal path, not a walk-route-vs-
// summary-serving isolation concern.
func declaredLocalSinkRun(t *testing.T, kernel *kernelbridge.RefinedTSKernel, source string, name string) []assignability.RefinementDiagnostic {
	t.Helper()
	p := parseVocabProgram(t, source)
	registry := annotations.AnnotationRegistry{}
	objects := annotations.ObjectRegistry{}
	annotations.CompileAnnotationFileFacts(p, p.Entry,
		annotations.AnnotationFileFactsMerged{Registry: registry, Objects: objects},
		kernel, false, func(assignability.RefinementDiagnostic) {})
	merged := map[*ast.Symbol]*FunctionContract{}
	contracts := CompileContractFileFacts(p, p.Entry, registry, objects, merged, false, func(assignability.RefinementDiagnostic) {})
	fn := parseVocabFunctionNamed(t, p, name)
	symbol := p.Checker.GetSymbolAtLocation(fn.Name())
	if symbol == nil {
		t.Fatalf("the checker resolved no symbol for %s", name)
	}
	contract, ok := contracts[symbol]
	if !ok {
		t.Fatalf("CompileContractFileFacts registered no contract for %s", name)
	}
	var diagnostics []assignability.RefinementDiagnostic
	ctx := &FlowContext{
		P:         p,
		Registry:  registry,
		Objects:   objects,
		Contracts: contracts,
		Report: func(d assignability.RefinementDiagnostic) {
			diagnostics = append(diagnostics, d)
		},
		Aliases:  dataflowfacts.NewAliasClasses(),
		Declared: map[string]*annotations.DeclaredRefinement{},
		Kernel:   kernel,
	}
	AnalyzeFunction(ctx, contract, nil)
	return diagnostics
}

// TestDeclaredLocalSink_FiresAtTheDeclaration pins the mechanism:
// assigning an out-of-range Wide value to a binding declared Age
// reports 7001 with the diagnostic's Start inside
// `const a: Age = x;`, and no second 7001 lands on `return a;`.
func TestDeclaredLocalSink_FiresAtTheDeclaration(t *testing.T) {
	kernel := parseVocabKernel(t)
	source := declaredLocalSinkHeader + `
function assignOutside(x: Wide): Age {
  const a: Age = x;
  return a;
}
`
	diagnostics := declaredLocalSinkRun(t, kernel, source, "assignOutside")
	declStart := strings.Index(source, "const a: Age = x;")
	returnStart := strings.Index(source, "return a;")
	if declStart < 0 || returnStart < 0 {
		t.Fatalf("fixture source changed shape — const/return offsets not found")
	}
	atDeclaration := 0
	atReturn := 0
	for _, d := range diagnostics {
		if d.Code != 7001 {
			continue
		}
		if d.Start >= declStart && d.Start < declStart+len("const a: Age = x;") {
			atDeclaration++
		}
		if d.Start >= returnStart && d.Start < returnStart+len("return a;") {
			atReturn++
		}
	}
	if atDeclaration != 1 {
		t.Errorf("want exactly one 7001 inside `const a: Age = x;`, got %d (all: %+v)", atDeclaration, diagnostics)
	}
	if atReturn != 0 {
		t.Errorf("want no 7001 on `return a;` (the refused write keeps the declared set), got %d (all: %+v)", atReturn, diagnostics)
	}
}

// TestDeclaredLocalSink_PossiblyNaNFiresAtTheDeclarationOnly is the
// bare-`number` twin of the test above: a bare `number` parameter
// seeds abstractdomain.KindPossiblyNaN (declared_value.go's
// AbstractValueOfDeclared, InitialStateOfPlainParameter — the same
// PossiblyNaN(numberGround) wrapper the Wide→Age case above never
// wears, since Wide states a genuine zod-derived bound, not the bare
// keyword). WriteBinding's refused-write law used to gate only on
// value.Kind == KindSet, so a possibly-NaN write skipped the meet
// entirely and kept the ORIGINAL possibly-NaN claim bound — a second
// read (`return u;`) then re-carried the same out-of-range claim
// against the same declared target and re-fired 7001, a false
// positive at the marked line (A2.sink.assign's own falsePositives:
// 1 row). Fixed by extending the same law to the PossiblyNaN shape,
// gated on the target genuinely excluding NaN (not AddsNothingSet).
func TestDeclaredLocalSink_PossiblyNaNFiresAtTheDeclarationOnly(t *testing.T) {
	kernel := parseVocabKernel(t)
	// A genuine zod-derived Unit target (not the placeholder "adds
	// nothing" number ground) so the declared side excludes NaN —
	// matching A2.sink.assign.ts's own fixture shape.
	source := `import * as z from "/surface/z.ts";
const zUnit = z.number().min(0).max(1);
type Unit = z.infer<typeof zUnit>;
function assignOutside(x: number): Unit {
  const u: Unit = x;
  return u;
}
`
	diagnostics := declaredLocalSinkRun(t, kernel, source, "assignOutside")
	declStart := strings.Index(source, "const u: Unit = x;")
	returnStart := strings.Index(source, "return u;")
	if declStart < 0 || returnStart < 0 {
		t.Fatalf("fixture source changed shape — const/return offsets not found")
	}
	atDeclaration := 0
	atReturn := 0
	for _, d := range diagnostics {
		if d.Code != 7001 {
			continue
		}
		if d.Start >= declStart && d.Start < declStart+len("const u: Unit = x;") {
			atDeclaration++
		}
		if d.Start >= returnStart && d.Start < returnStart+len("return u;") {
			atReturn++
		}
	}
	if atDeclaration != 1 {
		t.Errorf("want exactly one 7001 inside `const u: Unit = x;`, got %d (all: %+v)", atDeclaration, diagnostics)
	}
	if atReturn != 0 {
		t.Errorf("want no 7001 on `return u;` (the refused possibly-NaN write keeps the declared set, NaN excluded), got %d (all: %+v)", atReturn, diagnostics)
	}
}
