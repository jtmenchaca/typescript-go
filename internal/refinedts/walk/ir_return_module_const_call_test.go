// Pin for census row "return (call FIXED_CSS_LENGTH_UNITS.includes)":
// util/ReduceCSSCalc.ts's `return FIXED_CSS_LENGTH_UNITS.includes(unit
// as SupportedUnits);` — a `.includes()` call on a MODULE-LEVEL const
// array literal, in return position. Checks whether ir_return_call.go's
// SummaryCallOrHavocNamed delegation reaches ArrayNumericReadEffect's
// own `.includes` row (ir_assignment_effect_read.go) through the plain
// effect grammar FIRST (RhsEffect, tried ahead of every arm this agent
// added) — in which case no new work is needed here — or whether it
// falls all the way to this agent's call arm's opaque-havoc tier.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
)

func TestReturnModuleConstCall_ArrayIncludesOnModuleConstCompletes(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
		const FIXED_CSS_LENGTH_UNITS: ReadonlyArray<string> = ['cm', 'mm', 'pt', 'pc', 'in', 'Q', 'px'];
		function isSupportedUnit(unit: string) {
			return FIXED_CSS_LENGTH_UNITS.includes(unit);
		}
	`)
	declaration := entryEnvFunctionNamed(t, p, "isSupportedUnit")
	_, ok := RelowerSummaryBody(&FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	t.Logf("ok=%v outcome=%q construct=%q", ok, outcome, construct)
}
