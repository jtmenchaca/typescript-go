// The widened summary route (KernelSummaryDirect — this port's S5
// completion): a body whose only writes hit locals summarizes
// kernel-side without the effect-free pre-scan, and the declines that
// keep the route honest. Skipped (never a faked pass) when the native
// kernel dylib is absent, the same gate kernel_delegation_test uses.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/parser"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
)

// summaryDeclarationOf parses a throwaway source whose FIRST statement
// is a function declaration and answers that declaration node.
func summaryDeclarationOf(t *testing.T, source string) *ast.Node {
	t.Helper()
	opts := ast.SourceFileParseOptions{FileName: "/s.ts", Path: "/s.ts"}
	file := parser.ParseSourceFile(opts, source, core.ScriptKindTS)
	if len(file.Statements.Nodes) == 0 || !ast.IsFunctionDeclaration(file.Statements.Nodes[0]) {
		t.Fatalf("no function declaration parsed from %q", source)
	}
	return file.Statements.Nodes[0]
}

func exactNumber(t *testing.T, x float64) abstractdomain.AbstractValue {
	t.Helper()
	return abstractdomain.KnownValues([]float64{x}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
}

func TestKernelSummaryDirect_ALocalWritingLoopBodySummarizesWithoutTheEffectFreeGate(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	// scanBody calls this body impure (it assigns s and i), so the
	// effect-free route never reaches it — the direct route must.
	// The while head compares against a LITERAL, so the head's truth
	// and falsity sets narrow the loop (LoopHeadOf); a head comparing
	// two slots lowers too, but claims no narrowing on either side.
	declaration := summaryDeclarationOf(t,
		"function f(n: number) { let s = 0; let i = 0; while (i < 3) { s = s + n; i = i + 1; } return s; }")
	contract := &FunctionContract{Declaration: declaration}
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	answer, ok := KernelSummaryDirect(ctx, []abstractdomain.AbstractValue{exactNumber(t, 2)}, contract)
	if !ok {
		t.Fatalf("KernelSummaryDirect ok = false, want a summarized answer")
	}
	state, stateOk := StateOfKnown(answer)
	if !stateOk || state.Top {
		t.Fatalf("summarized answer did not spell as a scalar state: %+v", answer)
	}
	// f(2) = 2+2+2 = 6; the kernel's loop answer must ADMIT 6 (a
	// widened invariant is sound; an answer excluding 6 is not)
	if !kernel.Member(state.Set, []float64{6}) {
		t.Errorf("summary of f(2) excludes the true value 6: %+v", state.Set)
	}
}

func TestKernelSummaryDirect_APropertyWritingBodyDeclines(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	declaration := summaryDeclarationOf(t,
		"function f(o: { k: number }, n: number) { o.k = n; return n; }")
	contract := &FunctionContract{Declaration: declaration}
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	if _, ok := KernelSummaryDirect(ctx, []abstractdomain.AbstractValue{exactNumber(t, 1), exactNumber(t, 2)}, contract); ok {
		t.Errorf("a property-writing body summarized — its effect on the caller's object would be dropped")
	}
}

func TestKernelSummaryDirect_AnAsyncBodySummarizesAndAnswersAPromiseOfItsRet(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	// the ret-as-inner convention: the lowered body's #ret holds the
	// SETTLED value (n + 1), and the boundary wraps it — so the body
	// summarizes and the caller's view is a Promise of the return set
	declaration := summaryDeclarationOf(t,
		"async function f(n: number) { return n + 1; }")
	contract := &FunctionContract{Declaration: declaration}
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	answer, ok := KernelSummaryDirect(ctx, []abstractdomain.AbstractValue{exactNumber(t, 1)}, contract)
	if !ok {
		t.Fatalf("an async body declined — the ret-as-inner convention makes it lowerable")
	}
	if answer.Kind != abstractdomain.KindPromise {
		t.Fatalf("async summary answered kind %v, want a Promise wrapper", answer.Kind)
	}
	if answer.Inner == nil {
		t.Fatalf("the Promise carries no inner value")
	}
	state, stateOk := StateOfKnown(*answer.Inner)
	if !stateOk || state.Top {
		t.Fatalf("the promise's inner did not spell as a scalar state: %+v", *answer.Inner)
	}
	// f(1) settles at 2; the inner set must ADMIT it
	if !kernel.Member(state.Set, []float64{2}) {
		t.Errorf("the promise's inner excludes the true settled value 2: %+v", state.Set)
	}
}

func TestKernelSummaryDirect_AGeneratorBodyDeclines(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	// the call's value is an ITERATOR, and no slot spells one — there is
	// no inner value for a boundary wrapper to adopt
	declaration := summaryDeclarationOf(t,
		"function* f(n: number) { yield n + 1; }")
	contract := &FunctionContract{Declaration: declaration}
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	if _, ok := KernelSummaryDirect(ctx, []abstractdomain.AbstractValue{exactNumber(t, 1)}, contract); ok {
		t.Errorf("a generator body summarized — the call's value is an iterator, not the yield set")
	}
}
