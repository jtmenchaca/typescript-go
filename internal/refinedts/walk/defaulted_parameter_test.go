// Defaulted parameters: the default applies exactly where the runtime
// applies it — on an undefined entry and nowhere else — through a
// definedness branch in the summary's prelude. These bodies previously
// declined whole and determined nothing.
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
	// took the else arm, the default landed
	if !kernel.Member(state.Set, []float64{8}) {
		t.Errorf("f() excludes the true value 8: %+v", state.Set)
	}
	if kernel.Member(state.Set, []float64{3}) {
		t.Errorf("f() admits 3: %+v — no argument can produce it", state.Set)
	}
}

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
	// f(2) = 3 and the answer must admit it. The COMPILE joins branch
	// arms (a summary quantifies over all entries), so the default's
	// value rides the join beside the argument's — a bounded set, not
	// TOP, and never excluding the true value. Excluding the default on
	// a supplied argument needs a per-call conditional compile, which is
	// kernel work this representation does not have.
	if !kernel.Member(state.Set, []float64{3}) {
		t.Errorf("f(2) excludes the true value 3: %+v", state.Set)
	}
	if kernel.Member(state.Set, []float64{100}) {
		t.Errorf("f(2) admits 100: %+v — the join is bounded by the two arms", state.Set)
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
		t.Errorf("outcome = %q (construct %q), want complete — the default lowered under the definedness branch", outcome, construct)
	}
}

// A default the effect grammar cannot spell — an object literal —
// havocs its own slot and the body stays porous, never complete: a
// complete claim would serve an answer the inline walk (which reads
// the default) could beat.
func TestDefaultedParameter_AnUnspellableDefaultIsPorousNotComplete(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearSummaryOutcomes()
	outcome, construct := outcomeOf(t, summaryDeclarationOf(t,
		"function f(opts: object = {}) { return 1; }"))
	if outcome == SummaryComplete {
		t.Errorf("outcome = complete — the object default was not read, so complete overstates")
	}
	if outcome == SummaryPorous && construct != "a defaulted parameter" {
		t.Errorf("construct = %q, want %q", construct, "a defaulted parameter")
	}
}
