// Pins pushSlotEffectsOf's (ir_array_push.go) call-argument arm: `a.push(f())`
// where `f()` is write-and-call-free (no argument of ITS own writes a
// tracked slot) but its own VALUE has no scalar effect spelling — a call
// is not an effect grammar leaf (RhsEffect declines it outright,
// effect_expression.go's own dispatch). Before this arm, every push
// whose pushed value was a call declined the whole push, and the
// statement fell to the general call floor, which havocs the array's
// OWN slots too (len AND elem) — losing the exact length step the
// push's own arithmetic could still answer even though the pushed
// element's value cannot be pinned.
//
// THE SOUND ANSWER: the length still steps by exactly one (the call
// contributes exactly one pushed value, whatever it evaluates to), and
// the element slot JOINS with unknown rather than declining outright —
// the weak update the ordinary push already performs, just fed unknown
// instead of an exact value for this one pushed value.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
)

func TestArrayPush_ACallValuedArgumentStepsLengthAndJoinsUnknown(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
		declare function getThing(): number;
		export function f(n: number): number {
			const result: number[] = [];
			for (let i = 0; i < n; i++) {
				result.push(getThing());
			}
			return result.length;
		}
	`)
	fn := entryEnvFunctionNamed(t, p, "f")
	_, ok := RelowerSummaryBody(&FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}, fn)
	outcome, construct, recorded := SummaryOutcomeOf(p.Checker, fn)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	if !ok || outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q, ok %v), want SummaryComplete — getThing() takes no arguments to write through, so the push's own length arithmetic should still serve", outcome, construct, ok)
	}
}

// TestArrayPush_ACallWithAWritingArgumentDeclines pins the negative: a
// pushed call whose OWN argument writes a tracked slot must not be
// treated as free.
func TestArrayPush_ACallWithAWritingArgumentDeclines(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
		declare function getThing(seed: number): number;
		export function f(n: number): number {
			const result: number[] = [];
			let total = 0;
			for (let i = 0; i < n; i++) {
				result.push(getThing((total = total + 1)));
			}
			return total;
		}
	`)
	fn := entryEnvFunctionNamed(t, p, "f")
	_, ok := RelowerSummaryBody(&FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}, fn)
	outcome, construct, recorded := SummaryOutcomeOf(p.Checker, fn)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	if outcome == SummaryComplete {
		t.Errorf("outcome = %q (construct %q, ok %v), want NOT complete — getThing's own argument writes `total`, which must be havocked", outcome, construct, ok)
	}
}
