// split from ir_call_hoist_test.go — the accumulation's bookkeeping, loop
// bodies, and a declined statement's flush

package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

/* ── the accumulation's bookkeeping ──────────────────────────────── */

func TestCallHoist_TakeEmptiesTheAccumulationAndDropTruncatesToTheMark(t *testing.T) {
	context := &LoweringContext{}
	if taken := TakeHoisted(context); taken != nil {
		t.Errorf("TakeHoisted on an empty accumulation = %v, want nil", taken)
	}
	if mark := HoistedMark(context); mark != 0 {
		t.Errorf("HoistedMark on an empty accumulation = %d, want 0", mark)
	}
	context.Hoisted = []kernelbridge.IrStatement{
		{Kind: kernelbridge.IrStatementCall, Callee: 0},
		{Kind: kernelbridge.IrStatementCall, Callee: 1},
	}
	mark := HoistedMark(context)
	if mark != 2 {
		t.Fatalf("HoistedMark = %d, want 2", mark)
	}
	context.Hoisted = append(context.Hoisted, kernelbridge.IrStatement{Kind: kernelbridge.IrStatementCall, Callee: 2})
	DropHoistedFrom(context, mark)
	if len(context.Hoisted) != 2 {
		t.Errorf("len(Hoisted) after the drop = %d, want 2 — only what came after the mark goes", len(context.Hoisted))
	}
	taken := TakeHoisted(context)
	if len(taken) != 2 {
		t.Errorf("len(taken) = %d, want 2", len(taken))
	}
	if len(context.Hoisted) != 0 {
		t.Errorf("len(Hoisted) after the take = %d, want 0 — the take empties", len(context.Hoisted))
	}
	// an out-of-range mark leaves the accumulation alone rather than
	// panicking on a slice bound
	context.Hoisted = []kernelbridge.IrStatement{{Kind: kernelbridge.IrStatementCall}}
	DropHoistedFrom(context, 9)
	DropHoistedFrom(context, -1)
	if len(context.Hoisted) != 1 {
		t.Errorf("len(Hoisted) after out-of-range drops = %d, want 1", len(context.Hoisted))
	}
	DropHoistedFrom(nil, 0)
}

/* ── loop bodies are unchanged ───────────────────────────────────── */

func TestCallHoist_ALoopBodyIsUnchangedByHoisting(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearSummaryOutcomes()
	ctx, _, g := hoistCalleeProgram(t,
		"function g(n: number): number { return n + 1; }\n"+
			"function caller(y: number): number { return y; }\n")
	if _, has := SummaryBlobFor(ctx, g); !has {
		t.Skipf("the registry declined g's body")
	}
	context := hoistContext(kernel, []string{"i", "x"},
		[]BindingKind{BindingKindNumber, BindingKindNumber}, ctx,
		func(callee *ast.Node) *ast.Node { return g })
	// a loop whose BODY holds a call: the fold has no statement stream, so
	// the body must not lower through the fold — it falls to the havoc
	// floor exactly as it did before hoisting existed, and no call
	// statement is emitted
	lowered, ok := LowerStatements(context, hoistParse(t, `while (i < 10) { x = g(i) + 1; i = i + 1; }`))
	if !ok {
		t.Fatalf("the loop declined whole")
	}
	if calls := callStatementsOf(lowered); len(calls) != 0 {
		t.Errorf("call statements inside a loop lowering = %d, want 0 — the fold has no statement stream: %+v",
			len(calls), lowered)
	}
	if len(context.Hoisted) != 0 {
		t.Errorf("len(Hoisted) after the loop = %d, want 0", len(context.Hoisted))
	}
	// the flag is restored on the way out, so a later caller's own
	// bookkeeping is undisturbed
	if context.CanHoist {
		t.Errorf("CanHoist = true after LowerStatements returned, want the caller's own value restored")
	}
}

/* ── a declined statement leaves no orphan temps ─────────────────── */

func TestCallHoist_ADeclinedStatementLeavesNoCallStatementBehind(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearSummaryOutcomes()
	ctx, _, g := hoistCalleeProgram(t,
		"function g(n: number): number { return n + 1; }\n"+
			"function caller(y: number): number { return y; }\n")
	if _, has := SummaryBlobFor(ctx, g); !has {
		t.Skipf("the registry declined g's body")
	}
	// the RHS holds a hoistable call AND a shape no reading lowers — a
	// computed member on an untracked receiver. The statement falls to the
	// havoc floor, and the floor's contract is assignments of unknown and
	// NOTHING else: no call statement may sit ahead of it.
	context := hoistContext(kernel, []string{"x", "y"},
		[]BindingKind{BindingKindNumber, BindingKindNumber}, ctx,
		func(callee *ast.Node) *ast.Node { return g })
	lowered, ok := LowerStatements(context, hoistParse(t, `x = g(y) + obj[key].deep;`))
	if !ok {
		t.Fatalf("the statement declined whole rather than reaching the havoc floor")
	}
	for _, statement := range lowered {
		if statement.Kind == kernelbridge.IrStatementCall {
			t.Errorf("a declined statement leaked a hoisted call statement into the havoc floor's output: %+v", lowered)
		}
	}
	if len(context.Hoisted) != 0 {
		t.Errorf("len(Hoisted) after the declined statement = %d, want 0 — the drop truncates", len(context.Hoisted))
	}
}
