// The boolean-valued effects: a comparison, instanceof/in, `!`, and
// the short-circuit joins. Each converts bodies that previously
// determined nothing — the value of every one of these operators is
// exactly true or false (or one of its operands), so the effect grammar
// spells it without reading state it cannot see.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
)

// booleanSummaryOf runs the whole summary route on a source's first
// declaration and answers the served value.
func booleanSummaryOf(t *testing.T, source string, args []abstractdomain.AbstractValue) (abstractdomain.AbstractValue, bool) {
	t.Helper()
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearSummaryOutcomes()
	declaration := summaryDeclarationOf(t, source)
	contract := &FunctionContract{Declaration: declaration}
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	return KernelSummaryDirect(ctx, args, contract)
}

func TestBooleanEffect_AComparisonReturnServesTheTwoValueSet(t *testing.T) {
	answer, ok := booleanSummaryOf(t,
		"function f(a: number, b: number) { return a === b; }",
		[]abstractdomain.AbstractValue{exactNumber(t, 1), exactNumber(t, 2)})
	if !ok {
		t.Fatalf("a comparison return declined the summary route")
	}
	kernel := kernelDelegationLoadKernel(t)
	state, stateOk := StateOfKnown(answer)
	if !stateOk || state.Top {
		t.Fatalf("the answer did not spell as a scalar state: %+v", answer)
	}
	// the set is exactly {0,1}: both truth values admitted, nothing else
	if !kernel.Member(state.Set, []float64{0}) || !kernel.Member(state.Set, []float64{1}) {
		t.Errorf("the two-value set excludes a truth value: %+v", state.Set)
	}
	if kernel.Member(state.Set, []float64{2}) {
		t.Errorf("the two-value set admits 2: %+v — the comparison's value is only true or false", state.Set)
	}
}

func TestBooleanEffect_ANegationReturnServesTheTwoValueSet(t *testing.T) {
	answer, ok := booleanSummaryOf(t,
		"function f(a: number) { return !a; }",
		[]abstractdomain.AbstractValue{exactNumber(t, 1)})
	if !ok {
		t.Fatalf("a `!x` return declined the summary route")
	}
	kernel := kernelDelegationLoadKernel(t)
	state, stateOk := StateOfKnown(answer)
	if !stateOk || state.Top {
		t.Fatalf("the answer did not spell as a scalar state: %+v", answer)
	}
	if kernel.Member(state.Set, []float64{7}) {
		t.Errorf("`!a` admits 7: %+v", state.Set)
	}
}

func TestBooleanEffect_ALogicalOrReturnJoinsItsOperands(t *testing.T) {
	// f(0, 5) returns 5 at runtime; the join admits both operands' sets,
	// so 0 and 5 are in and 7 is out
	answer, ok := booleanSummaryOf(t,
		"function f(a: number, b: number) { return a || b; }",
		[]abstractdomain.AbstractValue{exactNumber(t, 0), exactNumber(t, 5)})
	if !ok {
		t.Fatalf("an `a || b` return declined the summary route")
	}
	kernel := kernelDelegationLoadKernel(t)
	state, stateOk := StateOfKnown(answer)
	if !stateOk || state.Top {
		t.Fatalf("the answer did not spell as a scalar state: %+v", answer)
	}
	if !kernel.Member(state.Set, []float64{5}) {
		t.Errorf("the join excludes the true runtime value 5: %+v", state.Set)
	}
	if kernel.Member(state.Set, []float64{7}) {
		t.Errorf("the join admits 7: %+v", state.Set)
	}
}

// A comparison whose operand CALLS is not admissible as the pair — the
// call moves state the pair claims nothing about. The body still
// lowers (the return havocs), so the outcome is recorded rather than
// declined; what must not happen is a served two-value answer.
func TestBooleanEffect_ACallingOperandKeepsTheOldPath(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearSummaryOutcomes()
	declaration := summaryDeclarationOf(t,
		"function f(a: number) { return g() === a; }")
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	if _, ok := RelowerSummaryBody(ctx, declaration); !ok {
		t.Fatalf("the body declined outright — the call should havoc, not refuse")
	}
	outcome, _, recorded := SummaryOutcomeOf(nil, declaration)
	if !recorded || outcome == SummaryComplete {
		t.Errorf("outcome = %q — a comparison over a call must not read as complete", outcome)
	}
}
