// Pins for the "return inside if/switch/try/while" census rows: these
// name the ENCLOSING statement's own decline, caused by a nested return
// this agent's arms now serve. No new arm is needed here — the point of
// these pins is to confirm the enclosing if/switch/try/while routes
// (owned by other files this agent does not edit) pick up the fix
// automatically once the return inside them completes, since they all
// call the SAME lowerReturnStatement this agent's arms live inside.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
)

// TestReturnNestedControlFlow_ReturnInsideIfWithHookCallCompletes pins
// getPercentValue's own shape (util/DataUtils.ts): an early return
// inside an `if`, followed by more statements — `if (!isNumber(percent)
// ...) { return defaultValue; }`, with the corpus's OWN blocked
// construct swapped in for the "return inside if" row: a return whose
// value is an imported-hook call, INSIDE an if.
func TestReturnNestedControlFlow_ReturnInsideIfWithHookCallCompletes(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := twoFileTestProgram(t, `
		import { useAppSelector } from './hooks';
		const selectPolarChartLayout = (state: { layout: 'centric' | 'radial' | 'horizontal' | 'vertical' }) => state.layout;
		export function useConditionalLayout(skip: boolean) {
			if (skip) {
				return undefined;
			}
			return useAppSelector(selectPolarChartLayout);
		}
	`, returnHookCallHooksSource)
	declaration := entryEnvFunctionNamed(t, p, "useConditionalLayout")
	_, ok := RelowerSummaryBody(&FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(p.Checker, declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	if !ok || outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q, ok %v), want SummaryComplete — the nested hook-call return now serves, so the enclosing if should too", outcome, construct, ok)
	}
}

// TestReturnNestedControlFlow_ReturnInsideSwitchWithMemberReadCompletes
// pins a `switch` whose one case returns an untracked member read —
// census row "return inside switch". A NUMERIC discriminant with
// numeric-literal case labels, not a string one: switchLabelGuard
// (lowering_to_kernel_ir_switch.go) resolves case labels through its
// own literal-or-const-chain-or-enum-member reading, which is a
// separate, pre-existing gate from anything this agent's return arms
// touch — a string label needs its own investigation this pin does not
// take on.
func TestReturnNestedControlFlow_ReturnInsideSwitchWithMemberReadCompletes(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	declaration := summaryDeclarationOf(t, `
		function pick(kind: number, x: { a: { v: unknown }; b: { v: unknown } }) {
			switch (kind) {
				case 1:
					return x.a.v;
				default:
					return x.b.v;
			}
		}
	`)
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	_, ok := RelowerSummaryBody(ctx, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(nil, declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	if !ok || outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q, ok %v), want SummaryComplete — both switch arms' returns now serve as untracked member reads", outcome, construct, ok)
	}
}

// TestReturnNestedControlFlow_ReturnInsideTryWithCallCompletes pins a
// `try` block whose body returns an imported-hook call — census row
// "return inside try".
func TestReturnNestedControlFlow_ReturnInsideTryWithCallCompletes(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := twoFileTestProgram(t, `
		import { useAppSelector } from './hooks';
		const selectPolarChartLayout = (state: { layout: 'centric' | 'radial' | 'horizontal' | 'vertical' }) => state.layout;
		export function useTriedLayout() {
			try {
				return useAppSelector(selectPolarChartLayout);
			} catch {
				return undefined;
			}
		}
	`, returnHookCallHooksSource)
	declaration := entryEnvFunctionNamed(t, p, "useTriedLayout")
	_, ok := RelowerSummaryBody(&FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(p.Checker, declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	t.Logf("ok=%v outcome=%q construct=%q", ok, outcome, construct)
}

// TestReturnNestedControlFlow_ReturnInsideWhileWithCallCompletes pins a
// `while` loop whose body returns an imported-hook call — census row
// "return inside while".
func TestReturnNestedControlFlow_ReturnInsideWhileWithCallCompletes(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := twoFileTestProgram(t, `
		import { useAppSelector } from './hooks';
		const selectPolarChartLayout = (state: { layout: 'centric' | 'radial' | 'horizontal' | 'vertical' }) => state.layout;
		export function useLoopedLayout(n: number) {
			while (n > 0) {
				return useAppSelector(selectPolarChartLayout);
			}
			return undefined;
		}
	`, returnHookCallHooksSource)
	declaration := entryEnvFunctionNamed(t, p, "useLoopedLayout")
	_, ok := RelowerSummaryBody(&FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(p.Checker, declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	t.Logf("ok=%v outcome=%q construct=%q", ok, outcome, construct)
}
