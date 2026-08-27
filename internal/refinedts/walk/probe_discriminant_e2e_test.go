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

func probeFlowContext(t *testing.T, p *program.CheckerProgram) *FlowContext {
	t.Helper()
	registry := annotations.AnnotationRegistry{}
	objects := annotations.ObjectRegistry{}
	merged := map[*ast.Symbol]*FunctionContract{}
	contracts := CompileContractFileFacts(p, p.Entry, registry, objects, merged, false, func(assignability.RefinementDiagnostic) {})
	return &FlowContext{
		P:         p,
		Registry:  registry,
		Objects:   objects,
		Contracts: contracts,
		Report:    func(assignability.RefinementDiagnostic) {},
		Aliases:   dataflowfacts.NewAliasClasses(),
		Declared:  map[string]*annotations.DeclaredRefinement{},
	}
}

func probeLoadKernel(t *testing.T) {
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
}

func TestProbeDiscriminantEndToEndInsideGuard(t *testing.T) {
	probeLoadKernel(t)
	p := entryEnvTestProgram(t, `type Shape = { kind: "circle"; r: number } | { kind: "square"; side: number };
function f(s: Shape): number {
  if (s.kind === "circle") {
    return s.r;
  }
  return 0;
}
void f;
`)
	fn := entryEnvFunctionNamed(t, p, "f")
	env := NewEnv()
	BindEntryEnv(BindEntryEnvInput{
		P:                     p,
		Env:                   env,
		Parameters:            fn.AsFunctionDeclaration().Parameters.Nodes,
		StatedParams:          nil,
		CallSiteInitialStates: nil,
	})
	ctx := probeFlowContext(t, p)

	body := fn.AsFunctionDeclaration().Body
	ifStatement := body.AsBlock().Statements.Nodes[0]
	ifNode := ifStatement.AsIfStatement()

	transfers := ConditionEnvTransfersOf(ctx, env, ifNode.Expression, ConditionEnvTransfersSite{At: ifNode.Expression})
	innerEnv := env.Clone()
	transfers.ApplyWhenTrue(innerEnv)

	sVal, _ := innerEnv.Get("s")
	t.Logf("inside-guard s.Kind = %v", sVal.Kind)
	if sVal.Kind == abstractdomain.KindObject {
		for _, k := range sVal.Keys {
			spelling, ok := abstractdomain.FormatAbstractValueInline(k.Value)
			t.Logf("  key=%s kind=%v ok=%v spelling=%q", k.Name, k.Value.Kind, ok, spelling)
		}
	}
}
