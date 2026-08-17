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
	outcome, construct, _ := SummaryOutcomeOf(declaration)
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
