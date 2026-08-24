// The ReduceCSSCalc.ts termination pin: the exact minimized
// reproducer (/private/tmp/killer_slice10.ts) that hung the kernel's
// seqSubset ask before mkUnion's own canonicalization
// (refined_sets/automata.lean's `A.structEq B` collapse, built the
// same session this test was written). A while-loop reassigns a
// string across repeated regex-driven `.replace()` calls
// (calculateArithmetic), and a second while-loop feeds that output
// through the SAME transform again (calculateParentheses ->
// calculateArithmetic) — the shape whose repeated derivation grew the
// kernel's Union term without bound (sequence_concatenation_widen.go's
// file comment has the full measured history: 2, then 4, then 6
// alphabet members before the third ask never returned).
package walk

import (
	"testing"
	"time"

	"github.com/microsoft/typescript-go/internal/ast"
)

// reduceCSSCalcTerminationProbeReady gates this test on purpose: the
// dylib on disk when this test was written still predates the
// mkUnion fix (built 21:18, before refined_sets/automata.lean's edit
// and before refinements/grammar.lean's structEq), so running the
// probe against it would either hang the suite for real or (if it
// happens to terminate for an unrelated reason) prove nothing about
// the fix. Flip to true once `pnpm kernel:native` has rebuilt the
// dylib from the fixed source — the coordinator's own checkpoint,
// not an environment variable (the standing rule: behavior is
// configured by arguments/constants, never ambient process state).
const reduceCSSCalcTerminationProbeReady = true

// killerSlice10Source is /private/tmp/killer_slice10.ts, copied
// verbatim — the minimized ReduceCSSCalc.ts reproducer, not a mirror.
const killerSlice10Source = `
function calculateArithmetic(expr: string | undefined): string {
  if (expr == null) {
    return 'NaN';
  }
  let newExpr = expr;
  while (newExpr.includes('x')) {
    newExpr = newExpr.replace(/x/, 'y');
  }
  return newExpr;
}

const PARENTHESES_REGEX = /\(([^()]*)\)/;

function calculateParentheses(expr: string): string {
  let newExpr = expr;
  let match: ReturnType<typeof RegExp.prototype.exec> | null;
  // eslint-disable-next-line no-cond-assign
  while ((match = PARENTHESES_REGEX.exec(newExpr)) != null) {
    const [, parentheticalExpression] = match;
    newExpr = newExpr.replace(PARENTHESES_REGEX, calculateArithmetic(parentheticalExpression));
  }

  return newExpr;
}

export function evaluateExpression(expression: string): string {
  let newExpr = expression.replace(/\s+/g, '');
  newExpr = calculateParentheses(newExpr);
  newExpr = calculateArithmetic(newExpr);
  return newExpr;
}
`

// TestReduceCSSCalc_EvaluateExpressionTerminatesUnderATimeout is the
// end-to-end pin: relowering evaluateExpression's body — the exact
// source that hung before — completes within a generous timeout
// instead of blocking forever. A hang fails LOUDLY (t.Fatal from the
// timeout branch), never silently; the goroutine leaked past the
// timeout is a known, accepted cost of testing an unbounded-time
// defect (there is no way to cancel mid-FFI-call), not something this
// test tries to clean up.
func TestReduceCSSCalc_EvaluateExpressionTerminatesUnderATimeout(t *testing.T) {
	if !reduceCSSCalcTerminationProbeReady {
		t.Skip("reduceCSSCalcTerminationProbeReady is false — flip it after kernel:native rebuilds the dylib from the mkUnion fix")
	}
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, killerSlice10Source)
	declaration := entryEnvFunctionNamed(t, p, "evaluateExpression")
	ctx := &FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}

	done := make(chan struct{})
	go func() {
		RelowerSummaryBody(ctx, declaration)
		close(done)
	}()

	select {
	case <-done:
		// terminated -- the outcome itself (complete/porous/declined) is
		// not what this test pins; ANY recorded outcome without a hang
		// is the fix working. A crash-worthy defect is a hang, never a
		// weak verdict (CLAUDE.md's determined/undetermined rule is about
		// what the checker DETERMINES, not about whether it RETURNS).
		if _, _, recorded := SummaryOutcomeOf(p.Checker, declaration); !recorded {
			t.Fatalf("RelowerSummaryBody returned but recorded no outcome for evaluateExpression")
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("RelowerSummaryBody(evaluateExpression) did not return within 30s — the ReduceCSSCalc.ts hang is still live")
	}
}
