// Pins for the record reassignment's SELF-SPREAD arm and the
// evaluation-order guard (ir_object_slots_record_assignment.go).
// `p = { ...p, x1: e }` is the CartesianAxis.tsx AxisLine shape — the
// census's assignment×5 cluster — and lowers to the explicit rows
// alone. The order guard: JavaScript evaluates every initializer
// against the OLD record, so a row reading a leaf an earlier row wrote
// declines rather than lowering to a stale-reading sequence.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
)

// TestRecordSelfSpread_AxisLineShapeCompletes pins the corpus shape: a
// flattened record local reassigned via `{ ...props, <explicit rows> }`
// in both arms of a branch.
func TestRecordSelfSpread_AxisLineShapeCompletes(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
		function axisLineish(offset: { left: number, top: number }, cond: boolean): number {
			let props = { x1: 0, y1: 0, x2: 0, y2: 0 };
			if (cond) {
				props = { ...props, x1: offset.left, y1: offset.top };
			} else {
				props = { ...props, x2: offset.left, y2: offset.top };
			}
			return props.x1;
		}
	`)
	declaration := entryEnvFunctionNamed(t, p, "axisLineish")
	_, ok := RelowerSummaryBody(&FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(declaration)
	if !recorded {
		t.Fatalf("no outcome recorded for axisLineish")
	}
	if !ok || outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q, ok %v), want SummaryComplete — the self-spread reassignment lowers to its explicit rows", outcome, construct, ok)
	}
}

// TestRecordRows_AStaleReadDeclinesRatherThanServingTheWrongValue pins
// the order guard on the exact-shape branch: `p = { b: 3, a: p.b }`
// reads the OLD b at runtime (the literal evaluates before the
// rebind), so the sequential lowering must NOT serve a = 3.
func TestRecordRows_AStaleReadDeclinesRatherThanServingTheWrongValue(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
		function staleRead(): number {
			let p = { a: 1, b: 2 };
			p = { b: 3, a: p.b };
			return p.a;
		}
	`)
	declaration := entryEnvFunctionNamed(t, p, "staleRead")
	ctx := &FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}
	_, _ = RelowerSummaryBody(ctx, declaration)
	result, served := SummaryResultIn(ctx, declaration, nil)
	if !served {
		return // no summary claim at all — nothing wrong can be served
	}
	if result.Kind == abstractdomain.KindValues && len(result.Values) == 1 && result.Values[0] == 3 {
		t.Errorf("staleRead served exactly 3 — the runtime answer is 2 (the literal reads the OLD b); the order guard must decline this row")
	}
}
