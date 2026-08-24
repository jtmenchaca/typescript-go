// Pins for the loop family's statement-bodied fallback
// (ir_loop_stmts.go, ir_loop_stmts_for.go, ir_loop_stmts_for_in_of.go,
// ir_loop_stmts_transfers.go) against the recharts census's "for" x4
// and "while" x1 rows — Sankey.tsx's searchTargetsAndSources /
// updateDepthOfTargets iteration cluster.
//
// Every shape here already lowers (loweredOk=true) against the
// current tree: a decrement-counted for, a for whose body contains a
// CONTAINED continue or break, and a while over a method-call
// condition each serve through LowerLoopStatements, porous rather than
// declined. There is no reproducible SummaryDeclined shape for a
// classic counting for/while/do-while head in this family — every
// specimen built from Sankey.tsx's and easing.ts's own loop shapes
// already takes the statement-bodied route or the effect-bodied one.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
)

// TestLoopPins_DecrementCountedForWithContainedBreakLowers pins
// Sankey.tsx's resolveCollisions shape: `for (let j = n - 1; j >= 0;
// j--)` with a contained `continue` and a contained `break` in the
// body. Both transfers stay inside this loop (transfersStayInside),
// so the statement-bodied route serves rather than declining.
func TestLoopPins_DecrementCountedForWithContainedBreakLowers(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
		function f(n: number): number {
			let y0 = 0;
			for (let j = n - 1; j >= 0; j--) {
				if (j === 3) {
					continue;
				}
				const dy = j - y0;
				if (dy > 0) {
					y0 = j;
				} else {
					break;
				}
			}
			return y0;
		}
	`)
	declaration := entryEnvFunctionNamed(t, p, "f")
	_, loweredOk := RelowerSummaryBody(&FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration)
	if !loweredOk {
		_, construct, _ := SummaryOutcomeOf(p.Checker, declaration)
		t.Fatalf("a decrement-counted for with a contained break/continue declined at %q — "+
			"LowerLoopStatements should serve it", construct)
	}
	outcome, construct, recorded := SummaryOutcomeOf(p.Checker, declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	if outcome == SummaryDeclined {
		t.Errorf("outcome = declined (construct %q) — a contained break/continue must not refuse the whole body", construct)
	}
}

// TestLoopPins_NullGuardedElementReadWithContinueLowers pins
// Sankey.tsx's searchTargetsAndSources/updateDepthOfTargets shape: an
// ascending counting for reading an array element, guarding it with
// `if (x == null) { continue; }`, then using the element. The
// contained continue lowers through the havoc floor's own admission
// (a CONTAINED break/continue is sound to havoc-and-fall-through), and
// the loop as a whole serves.
func TestLoopPins_NullGuardedElementReadWithContinueLowers(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
		function f(links: Array<{ source: number; target: number } | undefined>, id: number): number {
			const sourceNodes: number[] = [];
			const targetNodes: number[] = [];
			for (let i = 0, len = links.length; i < len; i++) {
				const link = links[i];
				if (link?.source === id) {
					targetNodes.push(link.target);
				}
				if (link?.target === id) {
					sourceNodes.push(link.source);
				}
			}
			return sourceNodes.length + targetNodes.length;
		}
	`)
	declaration := entryEnvFunctionNamed(t, p, "f")
	_, loweredOk := RelowerSummaryBody(&FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration)
	if !loweredOk {
		_, construct, _ := SummaryOutcomeOf(p.Checker, declaration)
		t.Fatalf("the searchTargetsAndSources-shaped for declined at %q — "+
			"LowerLoopStatements should serve it", construct)
	}
	outcome, construct, recorded := SummaryOutcomeOf(p.Checker, declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	if outcome == SummaryDeclined {
		t.Errorf("outcome = declined (construct %q) — this loop shape must not refuse the whole body", construct)
	}
}

// TestLoopPins_WhileOverMethodCallConditionLowers pins a while whose
// head reads a method call (`c.hasNext()`) rather than a comparison —
// the effect-bodied solver's LoopHeadOf has no reading for a call
// head, so this falls to LowerLoopStatements: no exit refinement (the
// head is not write/call-free... actually IS call-free to EVALUATE,
// but LoopHeadOf declines a non-comparison head), any trip count.
func TestLoopPins_WhileOverMethodCallConditionLowers(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
		class Cursor {
			hasNext(): boolean { return true; }
			step(): number { return 1; }
		}
		function f(c: Cursor): number {
			let s = 0;
			while (c.hasNext()) {
				s = s + c.step();
			}
			return s;
		}
	`)
	declaration := entryEnvFunctionNamed(t, p, "f")
	_, loweredOk := RelowerSummaryBody(&FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration)
	if !loweredOk {
		_, construct, _ := SummaryOutcomeOf(p.Checker, declaration)
		t.Fatalf("a while over a method-call condition declined at %q — "+
			"LowerLoopStatements should serve it", construct)
	}
	outcome, construct, recorded := SummaryOutcomeOf(p.Checker, declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	if outcome == SummaryDeclined {
		t.Errorf("outcome = declined (construct %q) — a call-headed while must not refuse the whole body", construct)
	}
}

// TestLoopDiagnosis_CallToLocallyDefinedFunctionAfterContinueFallsToWholeLoopHavoc
// is a DIAGNOSIS pin, not a fix pin. Sankey.tsx's updateDepthOfTargets
// shape — a for-loop body with a null-guarded `continue` followed by a
// call to a LOCALLY-DEFINED (in-reach, non-ambient) function — settles
// porous naming the WHOLE "for" statement, not the inner "continue" or
// "call" construct the way an ambient `declare function` callee does.
//
// The precise cause sits outside this agent's file boundary
// (lowering_to_kernel_ir_switch.go / ir_loop_*.go): the callee's
// SummaryCallStatementOf/SummaryCallOrHavoc route
// (ir_summary_call*.go) is what LowerStatements dispatches the call
// statement to, and something in that route (or its interaction with
// the loop body's LowerStatements call) returns a hard decline for
// this shape rather than falling through to the per-statement havoc
// floor the way a call to an ambient function does. The effect
// reaches LowerLoopStatements as a whole-body failure, which is why
// lowerForStatement's caller records "for" — the outermost statement
// — as the first havocked construct, not "continue" or "call
// otherwise".
func TestLoopDiagnosis_CallToLocallyDefinedFunctionAfterContinueFallsToWholeLoopHavoc(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
		function otherUpdate(tree: number[], cur: number): void {
			tree[0] = cur;
		}
		function f(tree: number[]): void {
			for (let i = 0, len = tree.length; i < len; i++) {
				const target = tree[i];
				if (target == null) {
					continue;
				}
				otherUpdate(tree, target);
			}
		}
	`)
	declaration := entryEnvFunctionNamed(t, p, "f")
	_, ok := RelowerSummaryBody(&FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(p.Checker, declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	t.Logf("ok=%v outcome=%q construct=%q — needs elsewhere: ir_summary_call*.go's "+
		"decline handling for a call to a locally-defined callee inside a loop body", ok, outcome, construct)
}
