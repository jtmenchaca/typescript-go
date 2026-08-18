// Pin for the negated pure-builtin test: `return !Number.isFinite(a)`
// refused where `return Number.isFinite(a)` lowers. The unnegated call
// reads through pureBuiltinEffect (effect_expression.go's call arm) as
// the exact two-value set; the negated one used to fail the `!` arm's
// writeAndCallFree gate — the operand IS a call — and fall to the
// opaque return, porous naming "return (! call Number.isFinite)".
// `!e` over an operand whose evaluation moves nothing is exactly true
// or false (sec-logical-not-operator, tmp/ecma262/spec.html), and a
// pure-builtin read's own gates already prove the operand moves
// nothing, so the negation carries the same two-value set the
// unnegated call does.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
)

func TestNegatedPureBuiltinReturn_LowersLikeTheUnnegatedForm(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
		function keeps(a: number): boolean {
			return Number.isFinite(a);
		}
		function flips(a: number): boolean {
			return !Number.isFinite(a);
		}
	`)
	ctx := &FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}
	keeps := entryEnvFunctionNamed(t, p, "keeps")
	if _, ok := RelowerSummaryBody(ctx, keeps); !ok {
		t.Fatalf("the unnegated form declined — the premise (unnegated lowers) is gone")
	}
	if outcome, construct, _ := SummaryOutcomeOf(keeps); outcome != SummaryComplete {
		t.Fatalf("unnegated: outcome=%q construct=%q, want complete", outcome, construct)
	}
	flips := entryEnvFunctionNamed(t, p, "flips")
	if _, ok := RelowerSummaryBody(ctx, flips); !ok {
		t.Fatalf("the negated form declined whole")
	}
	if outcome, construct, _ := SummaryOutcomeOf(flips); outcome != SummaryComplete {
		t.Errorf("negated: outcome=%q construct=%q, want complete — `!` over a pure-builtin read is still exactly the two-value set", outcome, construct)
	}
}
