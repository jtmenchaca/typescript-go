// The counted-loop lowering: a literal-bounded `for` lowers to
// "loopCounted" — the kernel-portable twin of the walk-side exact
// unroll (loop_unroll.go) — carrying the exact trip count and the same
// per-binding effect vector the effect-bodied "loop" form carries.
// Shape assertions only; this file does not run the walk or the
// kernel.

package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

func TestLoweringToKernelIR_ALiteralBoundedForLowersToLoopCounted(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := &LoweringContext{
		Bindings: []string{"i", "sum"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber},
		Typeofs:  []TypeofTag{TypeofTagNumber, TypeofTagNumber},
		Narrow:   kernel.Narrow,
	}
	// `i` starts at 0, the literal-bounded condition names 3 as the
	// (exclusive) bound, and the unit increment leaves `i` unwritten by
	// anything else in the body — exactly LiteralTripCountWith's own
	// gates (loop_trip_count.go), so the count is exact: 3. The
	// initializer must be a DECLARATION (`let i = 0`) — LiteralTripCountWith
	// gates on ast.IsVariableDeclarationList and declines a bare
	// reassignment initializer outright (loop_trip_count.go:39).
	stmts, ok := LowerStatements(context, loweringParse(t, `
		for (let i = 0; i < 3; i++) { sum = sum + i; }
	`))
	if !ok {
		t.Fatalf("a literal-bounded for with a foldable body declined to lower")
	}
	if len(stmts) != 2 {
		t.Fatalf("len(stmts) = %d, want 2 — the initializer assign and the loop: %+v", len(stmts), stmts)
	}
	loop := stmts[1]
	if loop.Kind != kernelbridge.IrStatementLoopCounted {
		t.Fatalf("stmts[1].Kind = %q, want %q — a literal-bounded for should lower exactly, not solve: %+v",
			loop.Kind, kernelbridge.IrStatementLoopCounted, loop)
	}
	if loop.Count != 3 {
		t.Errorf("loop.Count = %d, want 3 — the syntax pins i in [0, 3)", loop.Count)
	}
	// the counted form carries one effect per binding, the same
	// per-binding shape the effect-bodied loop's Body field holds —
	// never the statement-bodied loop's Stmts, and never a
	// cond/after/condCmp head, since nothing is solved or certified
	if len(loop.CountedBody) != len(context.Bindings) {
		t.Fatalf("len(loop.CountedBody) = %d, want %d (one effect per binding): %+v",
			len(loop.CountedBody), len(context.Bindings), loop)
	}
	if loop.Cond != nil || loop.After != nil || loop.CondCmp != nil {
		t.Errorf("the counted loop carries a head: %+v — its trip count is exact, so nothing is refined at an exit the fold already computes exactly", loop)
	}
	if loop.Written != nil || loop.Body != nil {
		t.Errorf("the counted loop carries the effect-bodied loop's own fields: %+v", loop)
	}
	if loop.Stmts != nil {
		t.Errorf("the counted loop carries the statement-bodied loop's Stmts field: %+v", loop)
	}
}

func TestLoweringToKernelIR_AForPastTheUnrollBudgetFallsBackToTheSolvedLoop(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := &LoweringContext{
		Bindings: []string{"i", "sum"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber},
		Typeofs:  []TypeofTag{TypeofTagNumber, TypeofTagNumber},
		Narrow:   kernel.Narrow,
	}
	// LoopUnrollBudget is 128 (loop_unroll.go); a bound of 200 pins an
	// exact count the syntax states, but past the budget the kernel
	// twin declines the exact form and the ordinary solved "loop"
	// carries it — the same fallback the walk-side exact unroll takes
	// (UnrollLiteralBoundedLoop: "count > LoopUnrollBudget" declines).
	stmts, ok := LowerStatements(context, loweringParse(t, `
		for (let i = 0; i < 200; i++) { sum = sum + i; }
	`))
	if !ok {
		t.Fatalf("a literal-bounded for past the budget declined to lower entirely")
	}
	if len(stmts) != 2 {
		t.Fatalf("len(stmts) = %d, want 2: %+v", len(stmts), stmts)
	}
	loop := stmts[1]
	if loop.Kind != kernelbridge.IrStatementLoop {
		t.Fatalf("stmts[1].Kind = %q, want %q — past LoopUnrollBudget the exact form must decline: %+v",
			loop.Kind, kernelbridge.IrStatementLoop, loop)
	}
}

func TestLoweringToKernelIR_ANonLiteralForFallsBackToTheSolvedLoop(t *testing.T) {
	context := &LoweringContext{
		Bindings: []string{"i", "sum", "n"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber},
		Typeofs:  []TypeofTag{TypeofTagNumber, TypeofTagNumber, TypeofTagNumber},
	}
	// the bound is a tracked binding, not a literal (or a const-to-const
	// chain LiteralTripCountWith's checker could resolve, and this test
	// runs with no checker at all — loweringParse builds none) — so no
	// exact count is pinned by the syntax alone, and the ordinary solved
	// form carries it exactly as it did before this form existed
	stmts, ok := LowerStatements(context, loweringParse(t, `
		for (let i = 0; i < n; i++) { sum = sum + i; }
	`))
	if !ok {
		t.Fatalf("a tracked-bound for with a foldable body declined to lower")
	}
	if len(stmts) != 2 {
		t.Fatalf("len(stmts) = %d, want 2: %+v", len(stmts), stmts)
	}
	loop := stmts[1]
	if loop.Kind != kernelbridge.IrStatementLoop {
		t.Fatalf("stmts[1].Kind = %q, want %q — a non-literal bound must not pin a count: %+v",
			loop.Kind, kernelbridge.IrStatementLoop, loop)
	}
}
