// Pins for the opaque hoist at condition-first positions
// (HoistOpaqueCallTemp, ir_call_hoist.go; wired in
// shortCircuitLeftSlot and ConditionTestSlot): a short circuit whose
// LEFT operand is an imported hook call — `return useAppSelector(sel)
// ?? fallback` — hoists the call to a temp through the statement-call
// tiers and branches on the temp, instead of declining the whole
// return to the opaque floor.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
)

// TestShortCircuitHookLeft_ImportedHookNullishCompletes pins the
// census's `return (binary ??)` family head: the hook tier serves the
// hoisted temp with no havoc note, so the body completes.
func TestShortCircuitHookLeft_ImportedHookNullishCompletes(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := twoFileTestProgram(t, `
		import { useAppSelectorish } from './hooks';
		const selectCount = (state: { layout: 'centric' | 'radial' | 'horizontal' | 'vertical' }): number => 1;
		export function useCountOrZero(): number {
			return useAppSelectorish(selectCount) ?? 0;
		}
	`, importedHookHooksSource)
	declaration := entryEnvFunctionNamed(t, p, "useCountOrZero")
	_, ok := RelowerSummaryBody(&FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(p.Checker, declaration)
	if !recorded {
		t.Fatalf("no outcome recorded for useCountOrZero")
	}
	if !ok || outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q, ok %v), want SummaryComplete — the hook left operand hoists to a temp and the branch tests it", outcome, construct, ok)
	}
}
