// Pins the nested-function-declaration gap the summarizedCall /
// nestedFunctionDeclaration fixture rows (a-statements.ts) name: a
// function declaration nested inside another function's body must
// register a contract exactly as a top-level one does, and a call to
// it must resolve through the same ContractOf / inline route — the
// walk's own CompileContractFileFacts.go collect() already recurses
// into every child unconditionally, so a nested declaration's symbol
// lands in the same file-wide contracts map a top-level one does; the
// call route (ContractOf, EvaluateCallExpression's inline/RecoverPure
// path) reads that map by symbol, with no top-level-only gate. This
// file pins both halves so a future regression on either shows here
// instead of only in the coverage fixture.

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
)

// contractLookupNestedFunction finds a FunctionDeclaration named text
// ANYWHERE in the tree — top-level or nested inside another
// function's body — unlike entryEnvFunctionNamed / callSiteFunctionNamed,
// which only look at the entry's top-level statements.
func contractLookupNestedFunction(t *testing.T, root *ast.Node, text string) *ast.Node {
	t.Helper()
	var found *ast.Node
	var visit func(node *ast.Node)
	visit = func(node *ast.Node) {
		if found != nil {
			return
		}
		if ast.IsFunctionDeclaration(node) {
			name := node.Name()
			if name != nil && ast.IsIdentifier(name) && name.Text() == text {
				found = node
				return
			}
		}
		node.ForEachChild(func(child *ast.Node) bool {
			visit(child)
			return found != nil
		})
	}
	visit(root)
	if found == nil {
		t.Fatalf("no function declaration named %s anywhere in the tree", text)
	}
	return found
}

// contractLookupCallArg finds the CallExpression of calleeName whose
// first argument is a NumericLiteral spelling `wantArg` — enough to
// tell `nextYear(120)` from `nextYear(40)` in one body.
func contractLookupCallArg(t *testing.T, root *ast.Node, calleeName string, wantArg string) *ast.Node {
	t.Helper()
	var found *ast.Node
	var visit func(node *ast.Node)
	visit = func(node *ast.Node) {
		if found != nil {
			return
		}
		if ast.IsCallExpression(node) {
			call := node.AsCallExpression()
			if ast.IsIdentifier(call.Expression) && call.Expression.Text() == calleeName &&
				call.Arguments != nil && len(call.Arguments.Nodes) == 1 &&
				ast.IsNumericLiteral(call.Arguments.Nodes[0]) &&
				call.Arguments.Nodes[0].Text() == wantArg {
				found = node
				return
			}
		}
		node.ForEachChild(func(child *ast.Node) bool {
			visit(child)
			return found != nil
		})
	}
	visit(root)
	if found == nil {
		t.Fatalf("no call %s(%s) found in the tree", calleeName, wantArg)
	}
	return found
}

// TestCompileContractFileFacts_ANestedFunctionDeclarationRegistersAContractLikeATopLevelOne
// pins registration: collect()'s unconditional recursion into every
// child (contract_file_facts.go) reaches a FunctionDeclaration nested
// inside another function's body exactly as it reaches a top-level
// one, so the nested declaration's symbol keys a FunctionContract in
// the SAME file-wide map — the map the call route reads by symbol.
func TestCompileContractFileFacts_ANestedFunctionDeclarationRegistersAContractLikeATopLevelOne(t *testing.T) {
	p := entryEnvTestProgram(t,
		"function summarizedCall(): number {\n"+
			"  function nextYear(age: number): number { return age + 1; }\n"+
			"  return nextYear(40);\n"+
			"}\n"+
			"function topLevelNextYear(age: number): number { return age + 1; }\n")
	registry := annotations.AnnotationRegistry{}
	objects := annotations.ObjectRegistry{}
	merged := map[*ast.Symbol]*FunctionContract{}
	contracts := CompileContractFileFacts(p, p.Entry, registry, objects, merged, false, func(assignability.RefinementDiagnostic) {})

	nested := contractLookupNestedFunction(t, p.Entry.AsNode(), "nextYear")
	nestedSymbol := p.Checker.GetSymbolAtLocation(nested.Name())
	if nestedSymbol == nil {
		t.Fatalf("the checker resolved no symbol for the nested nextYear's own name")
	}
	nestedContract, ok := contracts[nestedSymbol]
	if !ok {
		t.Fatalf("CompileContractFileFacts did not register a contract for the nested nextYear — nested function declarations do not yet register")
	}
	if nestedContract.Declaration != nested {
		t.Errorf("the registered contract's Declaration is not the nested nextYear's own node")
	}

	topLevel := entryEnvFunctionNamed(t, p, "topLevelNextYear")
	topLevelSymbol := p.Checker.GetSymbolAtLocation(topLevel.Name())
	if _, ok := contracts[topLevelSymbol]; !ok {
		t.Fatalf("CompileContractFileFacts did not register the top-level control case — the probe itself is broken")
	}
}

// TestEvaluateCallExpression_ANestedFunctionsCallSiteInlinesLikeATopLevelOnes
// pins the call route: nextYear(120) and nextYear(40) — nextYear
// declared NESTED inside summarizedCall — must answer exactly 121 and
// 41 through the same ContractOf / RecoverPure route a top-level
// declaration's call site takes. Skipped (never a faked pass) when
// the native kernel dylib is absent, the same gate every other
// kernel-backed walk test in this package uses.
func TestEvaluateCallExpression_ANestedFunctionsCallSiteInlinesLikeATopLevelOnes(t *testing.T) {
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

	p := entryEnvTestProgram(t,
		"function summarizedCall(): number {\n"+
			"  function nextYear(age: number): number { return age + 1; }\n"+
			"  const over = nextYear(120);\n"+
			"  const ok = nextYear(40);\n"+
			"  return over + ok;\n"+
			"}\n")
	registry := annotations.AnnotationRegistry{}
	objects := annotations.ObjectRegistry{}
	merged := map[*ast.Symbol]*FunctionContract{}
	contracts := CompileContractFileFacts(p, p.Entry, registry, objects, merged, false, func(assignability.RefinementDiagnostic) {})

	ctx := &FlowContext{
		P:         p,
		Kernel:    kernel,
		Registry:  registry,
		Objects:   objects,
		Contracts: contracts,
		Report:    func(assignability.RefinementDiagnostic) {},
		Aliases:   dataflowfacts.NewAliasClasses(),
		Declared:  map[string]*annotations.DeclaredRefinement{},
	}
	env := NewEnv()

	// degenerate windows dedup to "= value" (the ruled grammar's OPEN-1
	// fix); old wording (pre dedup): "{integer, multipleOf 1, 121 ≤ 𝑥 ≤ 121}"
	overCall := contractLookupCallArg(t, p.Entry.AsNode(), "nextYear", "120")
	overValue := evaluateExpression(ctx, env, overCall)
	overSpelled, overOk := abstractdomain.FormatAbstractValue(overValue)
	if !overOk || overSpelled != "{integer, multipleOf 1, = 121}" {
		t.Errorf("nextYear(120) through the nested declaration answered %q (ok %v), want the exact set {121}", overSpelled, overOk)
	}

	// old wording (pre dedup): "{integer, multipleOf 1, 41 ≤ 𝑥 ≤ 41}"
	okCall := contractLookupCallArg(t, p.Entry.AsNode(), "nextYear", "40")
	okValue := evaluateExpression(ctx, env, okCall)
	okSpelled, okOk := abstractdomain.FormatAbstractValue(okValue)
	if !okOk || okSpelled != "{integer, multipleOf 1, = 41}" {
		t.Errorf("nextYear(40) through the nested declaration answered %q (ok %v), want the exact set {41}", okSpelled, okOk)
	}
}
