// End-to-end pins for the equality/typeof widening of
// wholeRecordUseAt — TASK 2 of the parameter-entries fix wave
// (AGENT-BRIEF). Mirrors ir_summary_record_parameter_destructure_
// test.go's own end-to-end shape: a kernel-backed RelowerSummaryBody
// call, read back through SummaryOutcomeOf, asserting SummaryComplete
// now that recordParameterUseOf classifies an equality/typeof operand
// as recordParameterReadWhole instead of refusing the whole body.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
)

func TestKernelSummaryDirect_AStrictEqualityTestOverAWholeRecordParameterPins(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	declaration := summaryDeclarationOf(t,
		"function f(p: { lo: number, hi: number }) { if (p === undefined) { return 0; } return p.lo; }")
	_, ok := RelowerSummaryBody(&FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration)
	outcome, construct, _ := SummaryOutcomeOf(declaration)
	if !ok {
		t.Fatalf("declined: outcome = %q, construct = %q", outcome, construct)
	}
	if outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q), want complete", outcome, construct)
	}
}

func TestKernelSummaryDirect_ATypeofTestOverAWholeRecordParameterPins(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	declaration := summaryDeclarationOf(t,
		"function f(p: { lo: number, hi: number }) { if (typeof p === 'object') { return p.lo; } return 0; }")
	_, ok := RelowerSummaryBody(&FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration)
	outcome, construct, _ := SummaryOutcomeOf(declaration)
	if !ok {
		t.Fatalf("declined: outcome = %q, construct = %q", outcome, construct)
	}
	if outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q), want complete", outcome, construct)
	}
}
