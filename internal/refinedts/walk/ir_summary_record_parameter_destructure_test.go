// pins for destructuring FROM a record parameter — TASK 2 of the
// parameter-entries fix wave. Sankey.tsx's `const { targetNodes } =
// curNode;` shape: a variable declaration whose initializer is the bare
// parameter name, whose bound pattern names only declared depth-1
// members.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
)

// TestKernelSummaryDirect_ADeclarationDestructuringTheWholeParameterPins
// pins the outcome for `const { lo } = p;` where `p` is a record
// parameter — declined "a whole-record parameter use" before the
// recordParameterUseOf classification fix; complete after, since `p`
// expands into its own two member entries ("p.lo"/"p.hi") the same way
// an ordinary whole-record parameter already does, and the destructuring
// declaration itself needed no separate hook to lower.
func TestKernelSummaryDirect_ADeclarationDestructuringTheWholeParameterPins(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	declaration := summaryDeclarationOf(t,
		"function f(p: { lo: number, hi: number }) { const { lo } = p; return lo; }")
	lowered, ok := RelowerSummaryBody(&FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration)
	outcome, construct, _ := SummaryOutcomeOf(nil, declaration)
	if !ok {
		t.Fatalf("declined: outcome = %q, construct = %q", outcome, construct)
	}
	if outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q), want complete", outcome, construct)
	}
	if lowered.ParamCount != 2 {
		t.Errorf("ParamCount = %d, want 2 — p's own member entries (p.lo, p.hi)", lowered.ParamCount)
	}
}

// TestKernelSummaryDirect_ADefaultedDestructureElementLowers pins
// `const { offset = 0, clamp } = options;` — a defaulted element beside a
// plain one, both naming declared members. Before this fix,
// destructuresOnlyDeclaredMembers refused the WHOLE body the moment ANY
// bound element carried a default (`binding.Initializer != nil` was an
// unconditional refusal), so the destructuring statement never reached
// DestructuringWithDefaultsOf (ir_object_slots_destructuring.go) — the
// lowering route that already knows how to join a default on the
// exactly-undefined leg. After: the classifier admits the default and
// the existing lowering route serves it.
func TestKernelSummaryDirect_ADefaultedDestructureElementLowers(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	declaration := summaryDeclarationOf(t,
		"function f(options: { offset?: number; clamp?: boolean }): number { const { offset = 0, clamp } = options; return offset; }")
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	_, ok := RelowerSummaryBody(ctx, declaration)
	outcome, construct, _ := SummaryOutcomeOf(nil, declaration)
	t.Logf("ok=%v outcome=%q construct=%q", ok, outcome, construct)
	if !ok {
		t.Fatalf("f's body declined: outcome=%q construct=%q — a defaulted destructure element must lower", outcome, construct)
	}
	if construct == "a whole-record parameter use" {
		t.Errorf("construct = %q — the defaulted destructure still falls to the whole-record refusal", construct)
	}
}

// TestKernelSummaryDirect_ARenamedAndDefaultedDestructureLowers pins
// getCartesianPosition.tsx's own shape: `const { viewBox, position,
// offset = 0, parentViewBox: parentViewBoxFromOptions, clamp } =
// options;` — five elements, one defaulted, one renamed, the rest plain,
// every one naming a declared member of GetCartesianPositionOptions.
func TestKernelSummaryDirect_ARenamedAndDefaultedDestructureLowers(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
interface TrapezoidViewBox { x: number; }
interface CartesianViewBoxRequired { x: number; }
type CartesianLabelPosition = 'top' | 'bottom';
type GetCartesianPositionOptions = {
  viewBox: TrapezoidViewBox | CartesianViewBoxRequired;
  parentViewBox?: CartesianViewBoxRequired;
  offset?: number;
  position?: CartesianLabelPosition;
  clamp?: boolean;
};
function getCartesianPosition(options: GetCartesianPositionOptions): number {
  const { viewBox, position, offset = 0, parentViewBox: parentViewBoxFromOptions, clamp } = options;
  return offset;
}
`)
	declaration := entryEnvFunctionNamed(t, p, "getCartesianPosition")
	ctx := &FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}
	_, ok := RelowerSummaryBody(ctx, declaration)
	outcome, construct, _ := SummaryOutcomeOf(p.Checker, declaration)
	t.Logf("ok=%v outcome=%q construct=%q", ok, outcome, construct)
	if !ok {
		t.Fatalf("getCartesianPosition's body declined: outcome=%q construct=%q — a renamed+defaulted destructure must lower", outcome, construct)
	}
	if construct == "a whole-record parameter use" {
		t.Errorf("construct = %q — the renamed+defaulted destructure still falls to the whole-record refusal", construct)
	}
}
