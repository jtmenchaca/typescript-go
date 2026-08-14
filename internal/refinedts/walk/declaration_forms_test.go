// The declaration forms the lowering refused wholesale: several
// declarators in one statement, and a declarator with no initializer.
// Both previously fell to the havoc floor and the body determined
// nothing through them.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
)

func TestDeclarationForms_MultipleDeclaratorsLowerExactly(t *testing.T) {
	answer, ok := booleanSummaryOf(t,
		"function f(n: number) { let a = 1, b = 2; return a + b + n; }",
		[]abstractdomain.AbstractValue{exactNumber(t, 2)})
	if !ok {
		t.Fatalf("a multi-declarator body declined the summary route")
	}
	kernel := kernelDelegationLoadKernel(t)
	state, stateOk := StateOfKnown(answer)
	if !stateOk || state.Top {
		t.Fatalf("the answer did not spell as a scalar state: %+v", answer)
	}
	// f(2) = 1 + 2 + 2 = 5, exactly
	if !kernel.Member(state.Set, []float64{5}) {
		t.Errorf("f(2) excludes 5: %+v", state.Set)
	}
	if kernel.Member(state.Set, []float64{6}) {
		t.Errorf("f(2) admits 6: %+v", state.Set)
	}
}

func TestDeclarationForms_MultipleDeclaratorsRecordComplete(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearSummaryOutcomes()
	outcome, construct := outcomeOf(t, summaryDeclarationOf(t,
		"function f(n: number) { let a = 1, b = 2; return a + b + n; }"))
	if outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q), want complete", outcome, construct)
	}
}

func TestDeclarationForms_AnUninitializedDeclaratorIsUndefinedNotUnknown(t *testing.T) {
	// `let x;` then a write then a read: the declaration holds exactly
	// undefined, the write replaces it, and f(2) answers 3 alone
	answer, ok := booleanSummaryOf(t,
		"function f(n: number) { let x; x = n + 1; return x; }",
		[]abstractdomain.AbstractValue{exactNumber(t, 2)})
	if !ok {
		t.Fatalf("an uninitialized-declarator body declined the summary route")
	}
	kernel := kernelDelegationLoadKernel(t)
	state, stateOk := StateOfKnown(answer)
	if !stateOk || state.Top {
		t.Fatalf("the answer did not spell as a scalar state: %+v", answer)
	}
	if !kernel.Member(state.Set, []float64{3}) {
		t.Errorf("f(2) excludes 3: %+v", state.Set)
	}
	if kernel.Member(state.Set, []float64{0}) {
		t.Errorf("f(2) admits 0: %+v — the declaration's undefined was overwritten", state.Set)
	}
}

func TestDeclarationForms_AnUninitializedDeclaratorRecordsComplete(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearSummaryOutcomes()
	outcome, construct := outcomeOf(t, summaryDeclarationOf(t,
		"function f(n: number) { let x; x = n + 1; return x; }"))
	if outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q), want complete", outcome, construct)
	}
}

func TestDeclarationForms_APatternFromAnEffectFreeSourceIsRead(t *testing.T) {
	// `const { a } = obj` where obj is opaque: a's value has no spelling
	// — a takes unknown, which is what is true of it — and the body is
	// read whole
	answer, ok := booleanSummaryOf(t,
		"function f(n: number) { const { a } = obj; return n + 1; }",
		[]abstractdomain.AbstractValue{exactNumber(t, 2)})
	if !ok {
		t.Fatalf("a pattern from an effect-free source declined the summary route")
	}
	kernel := kernelDelegationLoadKernel(t)
	state, stateOk := StateOfKnown(answer)
	if !stateOk || state.Top {
		t.Fatalf("the answer did not spell as a scalar state: %+v", answer)
	}
	if !kernel.Member(state.Set, []float64{3}) {
		t.Errorf("f(2) excludes 3: %+v", state.Set)
	}
	ClearSummaryOutcomes()
	outcome, construct := outcomeOf(t, summaryDeclarationOf(t,
		"function f(n: number) { const { a } = obj; return n + 1; }"))
	if outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q), want complete", outcome, construct)
	}
}

func TestDeclarationForms_APatternFromAnUnresolvableCallKeepsThePorousName(t *testing.T) {
	// the SOURCE call's effects are unenumerable, so the call havocs and
	// names itself — the pattern route carries that through rather than
	// silently claiming the call was read
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearSummaryOutcomes()
	outcome, construct := outcomeOf(t, summaryDeclarationOf(t,
		"function f(n: number) { const { a } = g(); return n + 1; }"))
	if outcome == SummaryComplete {
		t.Errorf("outcome = complete — the source call was never read, so complete overstates")
	}
	if outcome == SummaryPorous && construct == "" {
		t.Errorf("the porous body named no construct")
	}
}

func TestDeclarationForms_AMixedMultiDeclaratorStillDeclinesToTheFloor(t *testing.T) {
	// one declarator no route spells (an object literal in a MULTI
	// statement) declines the route — the statement keeps the floor and
	// the body stays porous, never wrongly complete
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearSummaryOutcomes()
	outcome, _ := outcomeOf(t, summaryDeclarationOf(t,
		"function f(n: number) { let a = 1, o = { k: n }; return a; }"))
	if outcome == SummaryComplete {
		t.Errorf("outcome = complete — the object declarator was never read, so complete overstates")
	}
}
