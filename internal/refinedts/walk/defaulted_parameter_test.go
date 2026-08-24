// Defaulted parameters: the default applies exactly where the runtime
// applies it — on an EXACTLY UNDEFINED entry and nowhere else — through
// an eqUndef branch in the summary's prelude
// (IteratorBindingInitialization's SingleNameBinding case,
// specifications/javascript/spec.html:10146). These bodies previously declined whole
// and determined nothing.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
)

func TestDefaultedParameter_AMissingArgumentTakesTheDefault(t *testing.T) {
	answer, ok := booleanSummaryOf(t,
		"function f(n: number = 7) { return n + 1; }",
		nil)
	if !ok {
		t.Fatalf("a defaulted-parameter body declined the summary route")
	}
	kernel := kernelDelegationLoadKernel(t)
	state, stateOk := StateOfKnown(answer)
	if !stateOk || state.Top {
		t.Fatalf("the answer did not spell as a scalar state: %+v", answer)
	}
	// f() = 7 + 1 = 8, and only 8: the entry was undefined, the branch
	// took the then arm (eqUndef's true side), the default landed
	if !kernel.Member(state.Set, []float64{8}) {
		t.Errorf("f() excludes the true value 8: %+v", state.Set)
	}
	if kernel.Member(state.Set, []float64{3}) {
		t.Errorf("f() admits 3: %+v — no argument can produce it", state.Set)
	}
}

// TestDefaultedParameter_ANullArgumentDoesNotTakeTheDefault: a NULL
// entry — never undefined — does not trigger the default at RUNTIME,
// and the kernel now PROVES it: the eqUndef branch is INPUT-GATED, so
// an entry that cannot be undefined (a null-only n) prunes the Then arm
// (n := 7) from the join outright. f(null) therefore EXCLUDES 8 — the
// default's value never reaches the return.
//
// What f(null) DOES admit is left unpinned here. Tracing the surviving
// Else arm: n keeps its null-only entry (no numeric set, no Undef
// admission, Null admission up), and `return n + 1` reads n through the
// ordinary NUMERIC var (walk.lean's flagged-read rule: an admission
// present on the read source raises the NaN flag), over an operand
// whose own numeric SET is empty — the same "arithmetic absorbs an
// empty/bottom set" reading lowering_to_kernel_ir_return_branch.go's
// short-circuit composition documents. Pinning the resulting set/NaN
// combination precisely needs a kernel trace of this exact walk, which
// this pass did not run; only the exclusion claim above is asserted.
func TestDefaultedParameter_ANullArgumentDoesNotTakeTheDefault(t *testing.T) {
	answer, ok := booleanSummaryOf(t,
		"function f(n: number = 7) { return n + 1; }",
		[]abstractdomain.AbstractValue{abstractdomain.Null})
	if !ok {
		t.Fatalf("a defaulted-parameter body declined with a null argument")
	}
	kernel := kernelDelegationLoadKernel(t)
	state, stateOk := StateOfKnown(answer)
	if stateOk && !state.Top && kernel.Member(state.Set, []float64{8}) {
		t.Errorf("f(null) admits 8: %+v — the gated eqUndef join should prune the unreachable default arm", state.Set)
	}
}

// TestDefaultedParameter_ASuppliedArgumentIgnoresTheDefault: an exact
// argument is definitely NOT undefined, so the gated join now prunes
// the default arm here too — f(2) EXCLUDES 8, and the surviving Else
// arm alone decides the answer: n stays exactly {2} (no absent
// admission at all, so the numeric read of n carries none either),
// and `return n + 1` computes the single value 3, exactly.
func TestDefaultedParameter_ASuppliedArgumentIgnoresTheDefault(t *testing.T) {
	answer, ok := booleanSummaryOf(t,
		"function f(n: number = 7) { return n + 1; }",
		[]abstractdomain.AbstractValue{exactNumber(t, 2)})
	if !ok {
		t.Fatalf("a defaulted-parameter body declined with a supplied argument")
	}
	kernel := kernelDelegationLoadKernel(t)
	state, stateOk := StateOfKnown(answer)
	if !stateOk || state.Top {
		t.Fatalf("the answer did not spell as a scalar state: %+v", answer)
	}
	// f(2) = 3, exactly: the gated join prunes the default arm entirely,
	// so nothing else rides in beside the one true value
	if !kernel.Member(state.Set, []float64{3}) {
		t.Errorf("f(2) excludes the true value 3: %+v", state.Set)
	}
	if kernel.Member(state.Set, []float64{8}) {
		t.Errorf("f(2) admits 8: %+v — a supplied exact argument cannot be undefined, so the gated join prunes the default arm", state.Set)
	}
	if kernel.Member(state.Set, []float64{100}) {
		t.Errorf("f(2) admits 100: %+v — the pruned join no longer carries the old unbounded arm", state.Set)
	}
}

func TestDefaultedParameter_ADefaultReadingAnEarlierParameter(t *testing.T) {
	answer, ok := booleanSummaryOf(t,
		"function f(a: number, b: number = a + 1) { return b; }",
		[]abstractdomain.AbstractValue{exactNumber(t, 2)})
	if !ok {
		t.Fatalf("a default reading an earlier parameter declined")
	}
	kernel := kernelDelegationLoadKernel(t)
	state, stateOk := StateOfKnown(answer)
	if !stateOk || state.Top {
		t.Fatalf("the answer did not spell as a scalar state: %+v", answer)
	}
	// f(2) → b defaults to a + 1 = 3
	if !kernel.Member(state.Set, []float64{3}) {
		t.Errorf("f(2) excludes 3: %+v", state.Set)
	}
}

func TestDefaultedParameter_TheBodyRecordsComplete(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearSummaryOutcomes()
	outcome, construct := outcomeOf(t, summaryDeclarationOf(t,
		"function f(n: number = 7) { return n + 1; }"))
	if outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q), want complete — the default lowered under the eqUndef branch", outcome, construct)
	}
}

// An OBJECT-LITERAL default is read as the inert value it is: creating
// `{}` moves nothing, its value has no scalar spelling, and the slot
// takes unknown under the eqUndef branch — the body is complete,
// and a default whose construction RUNS CODE still havocs and names
// itself.
func TestDefaultedParameter_AnObjectDefaultIsReadAsUnknown(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearSummaryOutcomes()
	outcome, construct := outcomeOf(t, summaryDeclarationOf(t,
		"function f(opts: object = {}) { return 1; }"))
	if outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q), want complete — `{}` moves nothing and unknown is its honest value", outcome, construct)
	}
	ClearSummaryOutcomes()
	running, runningConstruct := outcomeOf(t, summaryDeclarationOf(t,
		"function f(opts: object = make()) { return 1; }"))
	if running == SummaryComplete {
		t.Errorf("outcome = complete for a default that CALLS — make() runs code the prelude never spelled")
	}
	if running == SummaryPorous && runningConstruct != "a defaulted parameter" {
		t.Errorf("construct = %q, want %q", runningConstruct, "a defaulted parameter")
	}
}
