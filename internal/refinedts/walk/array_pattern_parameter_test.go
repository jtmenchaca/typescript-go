// Pins the e-class-and-function.ts §E row arrayPatternParameter:
// `function firstOf([a]: number[]): number { return a; }` called as
// `firstOf([40, 41])` must bind `a` to the exact element 40, not the
// plain `number[]` element type ("number, or NaN").
//
// Two routes fed a destructured parameter's leaves BEFORE this pass,
// and both dropped the call-site argument on the floor:
//
//   - BindEntryEnv (entry_env.go) built the pattern's SOURCE value from
//     the stated annotation or the plain type only — it never consulted
//     CallSiteInitialStates, the map declaredJoin/CallSiteBindings
//     already flatten a call site's argument into by leaf name.
//   - The two inline routes (InlineContractBody's parameter loop,
//     recoverPureBody's parameter loop) both gated `ast.IsIdentifier(name)`
//     and did a bare `continue` for a pattern parameter — leaving its
//     leaves wholly UNBOUND, not merely widened.
//
// firstOf's own body (`return a`) is effect-free and self-contained, so
// the live call route is recoverPureBody (Summarize's EffectFree &&
// SelfContained gate) — this test exercises the real pipeline end to
// end so a regression on EITHER route shows here.

package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
)

// TestArrayPatternParameter_ACallSiteArgumentBindsThePatternsLeafExactly
// pins the walk half: firstOf([40, 41]) must determine exactly 40
// through `a`, the pattern's own leaf name.
func TestArrayPatternParameter_ACallSiteArgumentBindsThePatternsLeafExactly(t *testing.T) {
	kernel := yieldContractKernel(t)
	source := "function firstOf([a]: number[]): number {\n" +
		"  return a;\n" +
		"}\n" +
		"function caller(): number {\n" +
		"  return firstOf([40, 41]);\n" +
		"}\n"
	contract, ctx, diagnostics := yieldContractOf(t, source, "caller")
	ctx.Kernel = kernel
	SetEngineKernel(kernel)

	var sink []abstractdomain.AbstractValue
	ctx.ReturnSink = &sink
	AnalyzeFunction(ctx, contract, nil)
	if len(sink) == 0 {
		t.Fatalf("caller's body recorded no return value")
	}
	returned := JoinSinkSummarized(sink)
	if returned.Kind != abstractdomain.KindValues {
		t.Fatalf("firstOf([40, 41]) determined %+v, want an exact KindValues(40) — the array pattern's own leaf `a`", returned)
	}
	if len(returned.Values) != 1 || returned.Values[0] != 40 {
		t.Errorf("firstOf([40, 41]) determined %v, want exactly [40]", returned.Values)
	}
	if len(*diagnostics) != 0 {
		t.Errorf("an in-set array-pattern call reported %d diagnostics, want 0: %+v", len(*diagnostics), *diagnostics)
	}
}

// TestBindEntryEnv_ADestructuredParameterMeetsTheCallSiteJoinPerLeaf
// pins BindEntryEnv directly: an array-pattern parameter `[a]` with a
// call-site join naming `a` must bind `a` to the JOIN's exact value,
// not the plain `number[]` element reading — the identifier branch
// already does this (TestBindEntryEnv_ACallSiteJoinOutranksThePlainType);
// this is its destructured-parameter twin.
func TestBindEntryEnv_ADestructuredParameterMeetsTheCallSiteJoinPerLeaf(t *testing.T) {
	p := entryEnvTestProgram(t, "function firstOf([a]: number[]) { a; }\n")
	fn := entryEnvFunctionNamed(t, p, "firstOf")
	env := NewEnv()
	BindEntryEnv(BindEntryEnvInput{
		P:            p,
		Env:          env,
		Parameters:   fn.AsFunctionDeclaration().Parameters.Nodes,
		StatedParams: nil,
		CallSiteInitialStates: map[string]abstractdomain.AbstractValue{
			"a": abstractdomain.KnownValues([]float64{40}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved),
		},
	})
	held, ok := env.Get("a")
	if !ok {
		t.Fatalf("env[a] was never bound — the destructured parameter's call-site join was dropped")
	}
	formatted, formatOk := abstractdomain.FormatAbstractValue(held)
	if !formatOk || formatted != "40" {
		t.Errorf("env[a] = %q, %v, want %q, true — the call-site join's exact value, not the plain number[] element", formatted, formatOk, "40")
	}
}

// TestBindEntryEnv_ADestructuredParameterWithNoJoinFallsBackToThePlainRead
// pins the OTHER half: with no call-site join, a pattern parameter's
// leaf still reads the plain type through the pattern the way it did
// before this pass — this fix only ADDS the join lookup, it does not
// remove the existing plain-type fallback.
func TestBindEntryEnv_ADestructuredParameterWithNoJoinFallsBackToThePlainRead(t *testing.T) {
	p := entryEnvTestProgram(t, "function firstOf([a]: number[]) { a; }\n")
	fn := entryEnvFunctionNamed(t, p, "firstOf")
	env := NewEnv()
	BindEntryEnv(BindEntryEnvInput{
		P:                     p,
		Env:                   env,
		Parameters:            fn.AsFunctionDeclaration().Parameters.Nodes,
		StatedParams:          nil,
		CallSiteInitialStates: nil,
	})
	held, ok := env.Get("a")
	if !ok {
		t.Fatalf("env[a] was never bound with no call-site join at all")
	}
	if held.Kind == abstractdomain.KindUnknown && held.Opaque {
		t.Errorf("env[a] read Opaque with no join — want the plain number[] element reading, not opacity")
	}
}

// TestRecoverPureBody_ADestructuredParameterBindsThroughThePureRecoveryRoute
// pins the LIVE route firstOf(40, 41) actually takes: Summarize
// classifies `firstOf` as EffectFree and SelfContained (no writes, no
// calls, reads only its own parameter), which routes the call through
// recoverPureBody rather than InlineContractBody — the loop this test
// targets used to `continue` past a destructured parameter and leave
// `a` in callEnv entirely unbound (worse than widened: reading `a`
// inside the body answered residue, not even the plain element type).
func TestRecoverPureBody_ADestructuredParameterBindsThroughThePureRecoveryRoute(t *testing.T) {
	kernel := yieldContractKernel(t)
	source := "function firstOf([a]: number[]): number {\n" +
		"  return a;\n" +
		"}\n"
	p := entryEnvTestProgram(t, source)
	registry := annotations.AnnotationRegistry{}
	objects := annotations.ObjectRegistry{}
	merged := map[*ast.Symbol]*FunctionContract{}
	contracts := CompileContractFileFacts(p, p.Entry, registry, objects, merged, false, func(assignability.RefinementDiagnostic) {})
	fn := entryEnvFunctionNamed(t, p, "firstOf")
	symbol := p.Checker.GetSymbolAtLocation(fn.Name())
	contract := contracts[symbol]
	if contract == nil {
		t.Fatalf("firstOf registered no contract")
	}
	summary := Summarize(&FlowContext{P: p, Contracts: contracts}, *contract)
	if !summary.EffectFree || !summary.SelfContained {
		t.Fatalf("firstOf summarized as EffectFree=%v SelfContained=%v, want both true — recoverPureBody is only reached through this gate; the probe itself is broken if it does not", summary.EffectFree, summary.SelfContained)
	}

	SetEngineKernel(kernel)
	env := NewEnv()
	// build the call node `firstOf([40, 41])` and read it the way
	// EvaluateCallExpression's own pure-recovery arm does, through the
	// public entry point rather than reaching into recoverPureBody
	// directly (it is unexported)
	callSource := "function firstOf([a]: number[]): number {\n" +
		"  return a;\n" +
		"}\n" +
		"const good = firstOf([40, 41]);\n"
	p2 := entryEnvTestProgram(t, callSource)
	merged2 := map[*ast.Symbol]*FunctionContract{}
	contracts2 := CompileContractFileFacts(p2, p2.Entry, registry, objects, merged2, false, func(assignability.RefinementDiagnostic) {})
	ctx2 := &FlowContext{
		P: p2, Kernel: kernel, Registry: registry, Objects: objects, Contracts: contracts2,
		Report:   func(assignability.RefinementDiagnostic) {},
		Aliases:  dataflowfacts.NewAliasClasses(),
		Declared: map[string]*annotations.DeclaredRefinement{},
	}
	var callExpr *ast.Node
	for _, statement := range p2.Entry.Statements.Nodes {
		if !ast.IsVariableStatement(statement) {
			continue
		}
		for _, decl := range statement.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes {
			vd := decl.AsVariableDeclaration()
			if vd.Initializer != nil && ast.IsCallExpression(vd.Initializer) {
				callExpr = vd.Initializer
			}
		}
	}
	if callExpr == nil {
		t.Fatalf("no call expression found for firstOf([40, 41])")
	}
	result := evaluateExpression(ctx2, env, callExpr)
	if result.Kind != abstractdomain.KindValues {
		t.Fatalf("firstOf([40, 41]) through the pure-recovery route determined %+v, want an exact KindValues(40)", result)
	}
	if len(result.Values) != 1 || result.Values[0] != 40 {
		t.Errorf("firstOf([40, 41]) through the pure-recovery route determined %v, want exactly [40]", result.Values)
	}
}
