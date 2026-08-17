// Pin for census row "return (new)" ×4 — util/ReduceCSSCalc.ts-style
// shape: `return new Something(args);`. lowerReturnStatement's existing
// `new` arm (lowering_to_kernel_ir_return.go, tried ahead of every arm
// this agent added) already routes through SummaryCallOrHavoc with
// target = #ret. This pin checks whether that existing arm already
// completes a write-and-call-free-argument constructor call, or
// whether it needed this agent's own call-door wiring to reach
// completion (the `new` arm calls SummaryCallOrHavoc directly, not
// through ir_return_call.go's construct-naming wrapper, so it does NOT
// benefit from the imported-hook recognizer the same way — a `new`
// through an UNRESOLVABLE class constructor has no imported-hook
// equivalent to fall back on).
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
)

// TestReturnNew_ConstructorWithSummaryCompletes pins a `new` whose
// class constructor DOES compile a summary (a same-file class with a
// plain constructor body) — the case the existing `new` arm's blob
// tier already serves.
func TestReturnNew_ConstructorWithSummaryCompletes(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
		class Point {
			x: number;
			y: number;
			constructor(x: number, y: number) {
				this.x = x;
				this.y = y;
			}
		}
		function makePoint(x: number, y: number) {
			return new Point(x, y);
		}
	`)
	declaration := entryEnvFunctionNamed(t, p, "makePoint")
	_, ok := RelowerSummaryBody(&FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	if !ok || outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q, ok %v), want SummaryComplete — Point's constructor compiles a summary", outcome, construct, ok)
	}
}
