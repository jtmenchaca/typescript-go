// Pins localClosureOf (ir_summary_call_closure_blob.go): a callee whose
// symbol resolves through symbolAt's alias hop to a VariableDeclaration
// in ANOTHER source file must NOT be read as a body-local closure — only
// a same-file `const f = () => {...}` is one.
//
// Before this fix, localClosureOf asked only "does the symbol's
// ValueDeclaration hold a function-literal initializer", with no
// same-file check. An IMPORTED arrow (tmp/recharts-src/src/util/
// PolarUtils.ts's polarToCartesian, called from shape/Sector.tsx)
// matched that reading exactly as a real local closure would, so
// ClosureCallStatementOf/closureCallHavocNamed (ir_summary_call.go)
// claimed the site AHEAD of importedHookCallStatement
// (ir_summary_imported_hook_calls.go) — and closureCallHavocNamed's
// route always calls NoteFirstHavoc through OpaqueCallHavoc, marking the
// body porous at "call polarToCartesian" even though the site's actual
// output (target := unknown, nothing else havocked, since the imported
// closure's write set is empty) was identical to what the imported-hook
// tier would have served without the porous mark.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
)

func TestLocalClosureOf_AnImportedArrowIsNotALocalClosure(t *testing.T) {
	p := crossFileConstructedProgram(t,
		"export const polarToCartesian = (cx: number, cy: number, radius: number, angle: number) => ({ x: cx + radius, y: cy + angle });\n",
		"import { polarToCartesian } from \"./helper.ts\";\n"+
			"export function f(cx: number, cy: number, radius: number, angle: number): number {\n"+
			"  const center = polarToCartesian(cx, cy, radius, angle);\n"+
			"  return cx;\n"+
			"}\n")
	flow := &FlowContext{P: p}
	context := &LoweringContext{Flow: flow}
	callNode := crossFileConstructedFirstNode(t, p, "call expression", ast.IsCallExpression)
	callee := Unwrapped(callNode.AsCallExpression().Expression)
	_, ok := localClosureOf(context, callee)
	if ok {
		t.Errorf("localClosureOf matched an IMPORTED arrow as a body-local closure — cross-file declarations must decline here, leaving the imported-hook tier first refusal")
	}
}
