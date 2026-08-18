// Pins destructuredCallDeclarationStatement
// (ir_summary_call_ret_destructure.go): `const { circleTangency } =
// getTangentCircle({...})` — Sector.tsx:91-103's own shape, and
// AGENT-BRIEF's construct (3) — where a same-file callee's compiled
// summary carries RetMembers (returnedLiteralShape,
// ir_summary_returned_shape.go) but the declaration's name is an OBJECT
// BINDING PATTERN, not a bare identifier.
//
// Before this recognizer: callAssignmentShapeOf (ir_summary_call_surface.go)
// only ever reads ast.IsIdentifier(d.Name()) — a binding-pattern name
// declines it outright, before summaryCallStatement or
// threadRetMemberRets is ever reached. DestructuringAssignmentsOf
// (ir_object_slots_destructuring.go) is the ONLY existing destructuring
// route, and it only reads a HOLDER that is already an identifier/this —
// a bare call expression on the right is not a holder it resolves. So
// the statement fell through every route to the havoc floor (or declined
// the body, depending on what surrounds it), and every bound name
// (`circleTangency`, `lineTangency`, `theta`) read unknown afterward
// despite the callee's own summary carrying an exact per-member RetShape.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// destructuredCallProgram builds the two-function probe body: a
// same-file callee whose every return is the same object literal (so
// the layout allocates RetMembers), and a caller destructuring the call
// result directly. Answers the caller's own declaration node and a
// FlowContext with the callee registered as a resolvable contract,
// mirroring ir_array_argument_use_diagnosis_test.go's
// arrayArgumentDiagnosis harness.
func destructuredCallProgram(t *testing.T, callSite string) (*FlowContext, *ast.Node) {
	t.Helper()
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearResolvedArrayParameters()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
		function getTangentCircle(cx: number, cy: number): { circleTangency: number, lineTangency: number, theta: number } {
			return { circleTangency: cx + cy, lineTangency: cx - cy, theta: cx * cy };
		}
		function f(cx: number, cy: number): number {
			`+callSite+`
			return circleTangency;
		}
	`)
	ctx := &FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}
	callee := entryEnvFunctionNamed(t, p, "getTangentCircle")
	symbol := p.Checker.GetSymbolAtLocation(callee.AsFunctionDeclaration().Name())
	if symbol == nil {
		t.Fatalf("no symbol for getTangentCircle")
	}
	ctx.Contracts[symbol] = &FunctionContract{Declaration: callee}
	return ctx, entryEnvFunctionNamed(t, p, "f")
}

// TestDestructuredCallDeclaration_SameKeyBindingReadsTheReturnedMember
// pins the PRE-FIX failure: `const { circleTangency } =
// getTangentCircle(cx, cy)` should read circleTangency as the exact sum
// cx+cy (the callee's own returned member), not decline or havoc it.
// "Complete" alone does not prove precision — a havoc-assign lowering
// also completes — so this pin inspects the produced IrStatementCall's
// Rets vector directly: threadRetMemberRets (ir_summary_call_ret_members.go)
// drops EVERY member row to -1 unconditionally today ("the statement
// route's target is an index, not a name, so nothing is spelled here"),
// so a caller that DOES hold a named slot for the one destructured
// member (circleTangency) still gets no row threaded. Pre-fix: every
// Rets entry among the RetMembers' own indices is -1. Post-fix: the
// circleTangency member's own row names a real caller slot.
func TestDestructuredCallDeclaration_SameKeyBindingReadsTheReturnedMember(t *testing.T) {
	ctx, fn := destructuredCallProgram(t, `const { circleTangency } = getTangentCircle(cx, cy);`)
	lowered, ok := RelowerSummaryBody(ctx, fn)
	if !ok {
		t.Fatalf("f's body declined whole")
	}
	calleeDecl := entryEnvFunctionNamed(t, ctx.P, "getTangentCircle")
	calleeShape, shapeOk := LowerSummaryBody(ctx, calleeDecl)
	if !shapeOk {
		t.Fatalf("getTangentCircle's own summary failed to lower")
	}
	if calleeShape.RetShape != RetShapeObject || len(calleeShape.RetMembers) == 0 {
		t.Fatalf("getTangentCircle's summary carries no RetMembers (RetShape=%v) — the callee-side premise this pin needs regressed", calleeShape.RetShape)
	}
	var circleTangencyIndex = -1
	for _, member := range calleeShape.RetMembers {
		if member.Name == "circleTangency" {
			circleTangencyIndex = member.Index
		}
	}
	if circleTangencyIndex < 0 {
		t.Fatalf("no RetMembers row named circleTangency: %+v", calleeShape.RetMembers)
	}
	var call *kernelbridge.IrStatement
	for i := range lowered.Stmts {
		if lowered.Stmts[i].Kind == kernelbridge.IrStatementCall {
			call = &lowered.Stmts[i]
			break
		}
	}
	if call == nil {
		t.Fatalf("f's lowering carries no IrStatementCall — the destructured declaration did not even reach the summary call route (stmts=%+v)", lowered.Stmts)
	}
	threaded := circleTangencyIndex < len(call.Rets) && call.Rets[circleTangencyIndex] >= 0
	outcome, construct, recorded := SummaryOutcomeOf(fn)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	t.Logf("destructured call declaration: outcome=%q construct=%q circleTangencyIndex=%d rets=%v threaded=%v",
		outcome, construct, circleTangencyIndex, call.Rets, threaded)
	if !threaded {
		t.Errorf("call.Rets[%d] (circleTangency's own RetMembers row) = -1 — the caller's destructured local never receives the callee's returned member, so circleTangency reads unknown despite the callee's exact summary", circleTangencyIndex)
	}
}
