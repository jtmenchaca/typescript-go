// DIAGNOSIS pin (not a fix pin): isolates exactly where a return-position
// `&&`/`||`/`??` over a call-shaped left operand blocks, to decide
// whether the fix belongs in a file this agent owns
// (lowering_to_kernel_ir_return.go, lowering_to_kernel_ir_return_members.go,
// new ir_return_*.go files) or in lowering_to_kernel_ir_return_branch.go,
// which this agent does not.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
)

// TestReturnBinaryDiagnosis_HookCallLeftOfAndAndStillBlocked isolates the
// LEFT-OPERAND-IS-AN-UNRESOLVABLE-CALL shape: `return
// useAppSelector(sel) && 1;`. shortCircuitLeftSlot
// (lowering_to_kernel_ir_return_branch.go) admits only a TRACKED SLOT
// READ or a call HOISTED to a temp via HoistCallEffect, which itself
// requires a compiled blob (ir_call_hoist.go) — an imported hook has
// none, so the hoist declines and the whole branch declines, falling to
// the return route's plain effect grammar, which also declines (no
// reading for a call inside `&&`), and finally to the opaque return.
// This pin RECORDS that gap rather than fixing it: closing it needs
// shortCircuitLeftSlot itself to admit an OPAQUE-SERVED left (this
// agent's ir_return_call.go arm, applied to the LEFT operand alone, its
// temp read twice) — a change to
// lowering_to_kernel_ir_return_branch.go, outside this agent's owned
// files.
func TestReturnBinaryDiagnosis_HookCallLeftOfAndAndStillBlocked(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := twoFileTestProgram(t, `
		import { useAppSelector } from './hooks';
		const selectPolarChartLayout = (state: { layout: 'centric' | 'radial' | 'horizontal' | 'vertical' }) => state.layout;
		export function useMaybeLayout() {
			return useAppSelector(selectPolarChartLayout) && 1;
		}
	`, returnHookCallHooksSource)
	declaration := entryEnvFunctionNamed(t, p, "useMaybeLayout")
	_, ok := RelowerSummaryBody(&FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(p.Checker, declaration)
	if !recorded {
		t.Fatalf("no outcome recorded for useMaybeLayout")
	}
	t.Logf("ok=%v outcome=%q construct=%q", ok, outcome, construct)
}
