// Pins importedHookFlattenedDeclarationStatement
// (ir_summary_call_flattened_target.go): an imported/free-callee call
// assigned into a declaration whose name is a FLATTENED record local —
// `const center = polarToCartesian(cx, cy, radius, angle);` where
// `center.x`/`center.y` are read later — rather than one scalar slot.
// Mirrors tmp/recharts-src/src/util/PolarUtils.ts's polarToCartesian,
// called from shape/Sector.tsx.
//
// Before this recognizer, callAssignmentShapeOf (ir_summary_call_surface.go)
// asked IndexOf for the BARE name "center" and found no slot — a
// flattened record's own name never has one, only its leaf paths do — so
// SummaryCallStatementOf declined the statement whole, and the body went
// porous at "call polarToCartesian".
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
)

func TestImportedHookFlattenedDeclarationStatement_PolarToCartesianCompletesTheBody(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	// center.x is read inside an IF condition rather than a bare `return
	// center.x`: a member read in RETURN position takes its own separate
	// route (lowering_to_kernel_ir_return.go, out of this file's scope —
	// "return (member center.x)" is that route's own pre-existing gap,
	// unrelated to whether the DECLARATION itself served) whose own
	// pin lives in ir_object_slots_test.go. This pin isolates the
	// declaration statement this file's recognizer owns.
	p := crossFileConstructedProgram(t,
		"export const polarToCartesian = (cx: number, cy: number, radius: number, angle: number) => ({ x: cx + radius, y: cy + angle });\n",
		"import { polarToCartesian } from \"./helper.ts\";\n"+
			"export function f(cx: number, cy: number, radius: number, angle: number): number {\n"+
			"  const center = polarToCartesian(cx, cy, radius, angle);\n"+
			"  if (center.x > 0) {\n"+
			"    return 1;\n"+
			"  }\n"+
			"  return 0;\n"+
			"}\n")
	fn := entryEnvFunctionNamed(t, p, "f")
	_, ok := RelowerSummaryBody(&FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}, fn)
	outcome, construct, recorded := SummaryOutcomeOf(p.Checker, fn)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	if !ok || outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q, ok %v), want SummaryComplete — polarToCartesian's arguments are write-and-call-free, so center.x/center.y should havoc unknown rather than decline the body", outcome, construct, ok)
	}
}

// TestImportedHookFlattenedDeclarationStatement_AWritingArgumentDeclines
// pins the negative: an argument that WRITES a tracked slot must still
// decline the recognizer, same as the scalar-target twin
// (TestImportedHookCallStatement_AWritingInlineArrowArgumentDeclinesAndTheHavocFloorStands).
func TestImportedHookFlattenedDeclarationStatement_AWritingArgumentDeclines(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := crossFileConstructedProgram(t,
		"export const makePoint = (cb: () => number): { x: number; y: number } => ({ x: cb(), y: cb() });\n",
		"import { makePoint } from \"./helper.ts\";\n"+
			"export function f(): number {\n"+
			"  let total = 0;\n"+
			"  const point = makePoint(() => { total = total + 1; return total; });\n"+
			"  return point.x + total;\n"+
			"}\n")
	fn := entryEnvFunctionNamed(t, p, "f")
	_, ok := RelowerSummaryBody(&FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}, fn)
	outcome, construct, recorded := SummaryOutcomeOf(p.Checker, fn)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	// `total` is havocked by the closure-argument decline path, not left
	// standing — the body may still complete or go porous depending on
	// which floor catches it, but it must never be COMPLETE while
	// pretending point.x/point.y and total all kept their stale values.
	if outcome == SummaryComplete {
		t.Errorf("outcome = %q (construct %q, ok %v), want NOT complete — the callback writes `total`, which must be havocked, not silently preserved", outcome, construct, ok)
	}
}
