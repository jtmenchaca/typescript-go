// split from lowering_to_kernel_ir_test.go — the hoist stream and the shared grammar

package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

func TestLoweringToKernelIR_HoistingIsInertWhereNoCallSubexpressionAppears(t *testing.T) {
	// the hoist stream is threaded through every route of LowerStatements;
	// a body with no call subexpression must lower to exactly what it
	// lowered to before, statement for statement
	kernel := kernelDelegationLoadKernel(t)
	context := &LoweringContext{
		Bindings: []string{"x", "y"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber},
		Narrow:   kernel.Narrow,
	}
	stmts, ok := LowerStatements(context, loweringParse(t, `
		y = x + 1;
		if (x === 0) { y = 100; } else { y = y * 2; }
		while (x < 10) { x = x + 1; }
	`))
	if !ok {
		t.Fatalf("LowerStatements ok = false, want true")
	}
	if len(stmts) != 3 {
		t.Errorf("len(stmts) = %d, want 3 — one assign, one branch, one loop", len(stmts))
	}
	for _, statement := range stmts {
		if statement.Kind == kernelbridge.IrStatementCall {
			t.Errorf("a call statement appeared in a body with no call: %+v", stmts)
		}
	}
	if len(context.Hoisted) != 0 {
		t.Errorf("len(Hoisted) = %d after a call-free body, want 0", len(context.Hoisted))
	}
	// the flag and the statement pointer are restored, so the context is
	// handed back exactly as the caller gave it
	if context.CanHoist || context.HoistStatement != nil {
		t.Errorf("CanHoist = %v, HoistStatement = %v after the walk, want the caller's own values restored",
			context.CanHoist, context.HoistStatement)
	}
}

func TestLoweringToKernelIR_ACallSubexpressionWithNoBlobStillFallsToTheHavocFloor(t *testing.T) {
	// with no registry there is no blob to hoist, so `x = fetch("n") + 1`
	// reads exactly as it did before hoisting existed: the havoc floor,
	// naming x's own slot and nothing else
	kernel := kernelDelegationLoadKernel(t)
	context := &LoweringContext{
		Bindings: []string{"x"},
		Sorts:    []BindingKind{BindingKindNumber},
		Narrow:   kernel.Narrow,
	}
	stmts, ok := LowerStatements(context, loweringParse(t, `x = fetch("nope") + 1;`))
	if !ok {
		t.Fatalf("LowerStatements ok = false, want the havoc floor")
	}
	if len(stmts) != 1 || stmts[0].Kind != kernelbridge.IrStatementAssign ||
		stmts[0].Target != 0 || stmts[0].Effect.Kind != kernelbridge.LoopEffectUnknown {
		t.Errorf("stmts = %+v, want one `assign 0 unknown`", stmts)
	}
}

func TestLoweringToKernelIR_TheSharedGrammarSpeaksMinMaxAndTheTernaryJoinInTheIR(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	// shapes the IR lowering used to decline: Math.min, a ternary
	stmts, ok := LowerStatements(&LoweringContext{
		Bindings: []string{"x", "y"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber},
		Narrow:   kernel.Narrow,
	}, loweringParse(t, `
		y = Math.min(x, 10);
		x = x > 5 ? 1 : 2;
	`))
	if !ok {
		t.Fatalf("LowerStatements ok = false, want true")
	}
	exit := kernel.Walk(
		[]kernelbridge.KnownStateWire{
			{Set: refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.AtMost(20))},
			{Top: true},
		},
		stmts,
	)
	y := exit[1]
	if y.Top {
		t.Fatalf("y.Top = true, want false")
	}
	// min caps at 10
	if kernel.Member(loweringSetOf(t, y), []float64{21}) {
		t.Errorf("member(y, [21]) = true, want false")
	}
	x := exit[0]
	if x.Top {
		t.Fatalf("x.Top = true, want false")
	}
	xSet := loweringSetOf(t, x)
	// the ternary joins its arms
	if !kernel.Member(xSet, []float64{1}) {
		t.Errorf("member(x, [1]) = false, want true")
	}
	if !kernel.Member(xSet, []float64{2}) {
		t.Errorf("member(x, [2]) = false, want true")
	}
	if kernel.Member(xSet, []float64{3}) {
		t.Errorf("member(x, [3]) = true, want false")
	}
}
