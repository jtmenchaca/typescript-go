// Ports nothing — pins the Go tree's own yield-position contract
// (yield_contract.go): a generator's declared `Generator<Y, R, N>`
// states Y as what every `yield e` hands the caller and R as what the
// return statements hand the finished generator's last next(). The
// contract compile reads both positions, and the body walk judges
// each yield's operand against Y exactly as it judges each return
// against R — the b-body-expressions / i-more-expressions fixture
// rows (`yield 200` fires, `yield 40` and `return 40` stay silent)
// rest on both halves.

package walk

import (
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/narrowing"
)

// yieldContractOf compiles one file's contracts and hands back the
// named function's — the same CompileContractFileFacts door the real
// pass 2 takes.
func yieldContractOf(t *testing.T, source string, name string) (*FunctionContract, *FlowContext, *[]assignability.RefinementDiagnostic) {
	t.Helper()
	p := entryEnvTestProgram(t, source)
	registry := annotations.AnnotationRegistry{}
	objects := annotations.ObjectRegistry{}
	merged := map[*ast.Symbol]*FunctionContract{}
	contracts := CompileContractFileFacts(p, p.Entry, registry, objects, merged, false, func(assignability.RefinementDiagnostic) {})
	fn := entryEnvFunctionNamed(t, p, name)
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
	}
	return contract, ctx, &diagnostics
}

// TestGeneratorContract_TheDeclaredReturnTypeStatesYieldAndReturnPositions
// pins the contract-compile half: `Generator<10 | 40, 10 | 40,
// unknown>` on a starred declaration compiles Y into contract.Yield
// and R into contract.Result, and either position grounds the
// contract. A plain function's result still reads through the old
// route with no yield position.
//
// OLD PREMISE: "A generator whose type arguments are plain (`number`)
// states nothing at either position" (asserted plainContract.Yield ==
// nil, .Result == nil, .Grounded == false) — true only while a bare
// `number` type-argument read as no DeclaredRefinement. Now that a
// bare `number` keyword grounds (JT's ruling,
// annotations/type_node_sets.go), `Generator<number, number,
// unknown>`'s Y and R positions BOTH compile to a DeclaredSet over
// R-bar, exactly as `Generator<10 | 40, 10 | 40, unknown>`'s literal
// union positions do — so `plain` now grounds too, on the same two
// positions this test already checks `judged` grounds by.
func TestGeneratorContract_TheDeclaredReturnTypeStatesYieldAndReturnPositions(t *testing.T) {
	source := "function* judged(): Generator<10 | 40, 10 | 40, unknown> {\n" +
		"  yield 40;\n" +
		"  return 40;\n" +
		"}\n" +
		"function* plain(): Generator<number, number, unknown> {\n" +
		"  yield 10;\n" +
		"  return 10;\n" +
		"}\n" +
		"function normal(): 10 | 40 { return 40; }\n"

	judged, _, _ := yieldContractOf(t, source, "judged")
	if judged.Yield == nil {
		t.Fatalf("the stated Y of Generator<10 | 40, …> compiled no yield position")
	}
	if judged.Yield.Kind != annotations.DeclaredSet {
		t.Errorf("the yield position compiled as kind %v, want a DeclaredSet", judged.Yield.Kind)
	}
	if judged.Result == nil {
		t.Fatalf("the stated R of Generator<…, 10 | 40, …> compiled no result position")
	}
	if judged.Result.Kind != annotations.DeclaredSet {
		t.Errorf("the result position compiled as kind %v, want a DeclaredSet", judged.Result.Kind)
	}
	if !judged.Grounded {
		t.Errorf("a generator contract with stated yield and return positions did not ground")
	}

	plainContract, _, _ := yieldContractOf(t, source, "plain")
	if plainContract.Yield == nil {
		t.Fatalf("Generator<number, …>'s yield position compiled nil — a bare `number` type argument grounds like any other DeclaredSet now")
	}
	if plainContract.Yield.Kind != annotations.DeclaredSet {
		t.Errorf("the plain yield position compiled as kind %v, want a DeclaredSet", plainContract.Yield.Kind)
	}
	if plainContract.Result == nil {
		t.Fatalf("Generator<…, number, …>'s result position compiled nil — a bare `number` type argument grounds like any other DeclaredSet now")
	}
	if plainContract.Result.Kind != annotations.DeclaredSet {
		t.Errorf("the plain result position compiled as kind %v, want a DeclaredSet", plainContract.Result.Kind)
	}
	if !plainContract.Grounded {
		t.Errorf("a generator contract with plain-but-grounded yield and return positions did not ground")
	}

	normalContract, _, _ := yieldContractOf(t, source, "normal")
	if normalContract.Yield != nil {
		t.Errorf("a non-generator compiled a yield position")
	}
	if normalContract.Result == nil {
		t.Errorf("a non-generator's stated result no longer compiles through the old route")
	}
}

// yieldContractKernel loads the native kernel the way every other
// kernel-backed walk test in this package does, or skips.
func yieldContractKernel(t *testing.T) *kernelbridge.RefinedTSKernel {
	t.Helper()
	if !kernelbridge.KernelArtifactsPresent(kernelbridge.DylibPath) {
		t.Skip("native kernel dylib absent — build it first")
	}
	kernel, err := kernelbridge.LoadKernel(kernelbridge.DylibPath)
	if err != nil {
		t.Fatalf("LoadKernel: %v", err)
	}
	SetEngineKernel(kernel)
	SetTransferKernel(kernel)
	narrowing.SetNarrowKernel(kernel)
	return kernel
}

// TestCheckYieldedValue_AYieldOutsideTheStatedSetFiresAndTheOthersStaySilent
// pins the walk half on the fixture's own shape: under
// `Generator<10 | 40, 10 | 40, unknown>`, `yield 200` refutes (7001,
// at the operand), while `yield 40` and `return 40` are proved
// members and say nothing.
func TestCheckYieldedValue_AYieldOutsideTheStatedSetFiresAndTheOthersStaySilent(t *testing.T) {
	kernel := yieldContractKernel(t)
	source := "function* judged(): Generator<10 | 40, 10 | 40, unknown> {\n" +
		"  yield 40;\n" +
		"  yield 200;\n" +
		"  return 40;\n" +
		"}\n"
	contract, ctx, diagnostics := yieldContractOf(t, source, "judged")
	ctx.Kernel = kernel

	AnalyzeFunction(ctx, contract, nil)

	if len(*diagnostics) != 1 {
		t.Fatalf("the walk reported %d diagnostics, want exactly the one refutation on `yield 200`: %+v", len(*diagnostics), *diagnostics)
	}
	fired := (*diagnostics)[0]
	if fired.Code != 7001 {
		t.Errorf("the yield 200 judgment reported code %d, want the refutation 7001", fired.Code)
	}
	if wantStart := strings.Index(source, "200"); fired.Start != wantStart {
		t.Errorf("the refutation landed at offset %d, want %d (the `200` operand)", fired.Start, wantStart)
	}
}

// TestCheckYieldedValue_ADelegatedYieldJudgesTheElementItHandsOn pins
// the `yield*` route: the caller receives every element OF the
// operand, so the element judges against the stated yield position —
// `yield* [10, 40]` is silent, `yield* [10, 200]` refutes at the
// operand.
func TestCheckYieldedValue_ADelegatedYieldJudgesTheElementItHandsOn(t *testing.T) {
	kernel := yieldContractKernel(t)
	source := "function* delegating(): Generator<10 | 40, 10 | 40, unknown> {\n" +
		"  yield* [10, 40];\n" +
		"  yield* [10, 200];\n" +
		"}\n"
	contract, ctx, diagnostics := yieldContractOf(t, source, "delegating")
	ctx.Kernel = kernel

	AnalyzeFunction(ctx, contract, nil)

	if len(*diagnostics) != 1 {
		t.Fatalf("the walk reported %d diagnostics, want exactly the one refutation on `yield* [10, 200]`: %+v", len(*diagnostics), *diagnostics)
	}
	fired := (*diagnostics)[0]
	if fired.Code != 7001 {
		t.Errorf("the delegated yield judgment reported code %d, want the refutation 7001", fired.Code)
	}
	if wantStart := strings.Index(source, "[10, 200]"); fired.Start != wantStart {
		t.Errorf("the refutation landed at offset %d, want %d (the delegated operand)", fired.Start, wantStart)
	}
}
