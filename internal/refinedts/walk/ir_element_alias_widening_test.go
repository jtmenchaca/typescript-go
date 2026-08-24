// Pins for the element-alias widening (ir_object_slots_slot_index.go,
// ir_assignment.go, ir_opaque_havoc_enumeration.go): the Sankey
// updateDepthOfTargets family. A `const target = tree[i]` alias now
// admits bare uses in TEST and CALL-ARGUMENT positions; a write
// through the alias JOINS into the shared element slot (the weak
// update one-element-of-many needs); a hand-over havocs the element's
// member slots. A bare escape into a fresh binding still refuses.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

func elementAliasRelower(t *testing.T, source string, name string) (LoweredSummary, bool, SummaryOutcome, string) {
	t.Helper()
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	ClearElementAliasedLocals()
	p := entryEnvTestProgram(t, source)
	declaration := entryEnvFunctionNamed(t, p, name)
	summary, ok := RelowerSummaryBody(&FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(p.Checker, declaration)
	if !recorded {
		t.Fatalf("no outcome recorded for %s", name)
	}
	return summary, ok, outcome, construct
}

// TestElementAlias_GuardedWriteThroughAliasCompletes pins the Sankey
// shape: identity test on the alias, then a write through it.
func TestElementAlias_GuardedWriteThroughAliasCompletes(t *testing.T) {
	_, ok, outcome, construct := elementAliasRelower(t, `
		function updateDepth(tree: Array<{ depth: number, value: number }>, targetNode: number, curDepth: number): number {
			const target = tree[targetNode];
			if (target) {
				target.depth = curDepth + 1;
			}
			return curDepth;
		}
	`, "updateDepth")
	if !ok || outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q, ok %v), want SummaryComplete — the identity test branch-boths and the write joins into the element slot", outcome, construct, ok)
	}
}

// TestElementAlias_WriteThroughAliasLowersAsAJoin pins the weak update
// itself: the lowered statements write the element's member slot with a
// JOIN effect, never a bare replacement — the slot stands for every
// element, and a replacement would claim the written value for all.
func TestElementAlias_WriteThroughAliasLowersAsAJoin(t *testing.T) {
	summary, ok, _, _ := elementAliasRelower(t, `
		function writeDepth(tree: Array<{ depth: number, value: number }>, i: number, d: number): number {
			const target = tree[i];
			if (target) {
				target.depth = d;
			}
			return d;
		}
	`, "writeDepth")
	if !ok {
		t.Fatalf("writeDepth declined to lower")
	}
	foundJoin := false
	var scan func(statements []kernelbridge.IrStatement)
	scan = func(statements []kernelbridge.IrStatement) {
		for _, statement := range statements {
			if statement.Kind == kernelbridge.IrStatementAssign && statement.Effect.Kind == kernelbridge.LoopEffectJoin {
				foundJoin = true
			}
			scan(statement.Then)
			scan(statement.Else)
		}
	}
	scan(summary.Stmts)
	if !foundJoin {
		t.Errorf("no JOIN-shaped assignment in the lowered statements — the alias write must join into the shared slot, not replace it")
	}
}

// TestElementAlias_HandOverToACallLowersWithTheCallHavocked pins the
// call-argument admission: handing the alias to an unreadable callee no
// longer refuses the aliasing — the body LOWERS, with the call havocked
// (porous at "call mutate" is the honest label for a callee nothing
// read; the element's member slots ride the havoc set through
// ElementAliasHavocSlots). When the statement-call tiers serve such
// callees without a havoc note, this pin's outcome tightens to
// complete.
func TestElementAlias_HandOverToACallLowersWithTheCallHavocked(t *testing.T) {
	_, ok, outcome, construct := elementAliasRelower(t, `
		declare function mutate(n: { depth: number, value: number }): void;
		function touch(tree: Array<{ depth: number, value: number }>, i: number): number {
			const target = tree[i];
			if (target) {
				mutate(target);
			}
			return i;
		}
	`, "touch")
	if !ok {
		t.Fatalf("touch declined to lower — the hand-over admission must keep the body's route")
	}
	if outcome != SummaryComplete && !(outcome == SummaryPorous && construct == "call mutate") {
		t.Errorf("outcome = %q (construct %q), want complete or porous at exactly the call — never a refusal of the whole aliasing", outcome, construct)
	}
}

// TestElementAlias_ABareEscapeStillRefuses pins the boundary: the alias
// value flowing into a fresh binding is an identity the slots cannot
// spell, so the aliasing refuses and the body stays porous at the
// declaration — never a wrong claim.
func TestElementAlias_ABareEscapeStillRefuses(t *testing.T) {
	_, _, outcome, _ := elementAliasRelower(t, `
		function escape(tree: Array<{ depth: number, value: number }>, i: number): number {
			const target = tree[i];
			const q = target;
			if (q) {
				return 1;
			}
			return 0;
		}
	`, "escape")
	if outcome == SummaryComplete {
		t.Errorf("outcome = complete — a bare escape into a fresh binding must refuse the aliasing (porous is the honest answer)")
	}
}
