// split from ir_call_hoist_test.go — the ordering gate

package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
)

/* ── the ordering gate ───────────────────────────────────────────── */

func TestCallHoist_TheOrderingGateRefusesACallWritingASlotTheStatementAlsoReads(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearSummaryOutcomes()
	ClearResolvedRecordMembers()
	// `this.bump()` WRITES this.count. A statement that also READS
	// this.count around the call — `total = this.count + this.bump()` —
	// would read the POST-call value if the call were hoisted, where the
	// real run reads the pre-call one. The gate refuses.
	p := hoistProgram(t,
		"class Counter {\n"+
			"  count: number = 0;\n"+
			"  total: number = 0;\n"+
			"  bump(): number { this.count = this.count + 1; return this.count; }\n"+
			"  run(): void { this.total = this.count + this.bump(); }\n"+
			"}\n")
	ctx := &FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}
	bump := methodNamed(t, p, "bump")
	symbol := p.Checker.GetSymbolAtLocation(bump.Name())
	if symbol == nil {
		t.Fatalf("bump has no symbol to register a contract under")
	}
	ctx.Contracts[symbol] = &FunctionContract{Declaration: bump}
	shape, known := LowerSummaryBody(ctx, bump)
	if !known {
		t.Skipf("bump's body did not lower; there is no written bundle row for the gate to read")
	}
	writtenCount := false
	for _, entry := range shape.BundleEntries {
		if entry.Path == "this.count" && entry.Written {
			writtenCount = true
		}
	}
	if !writtenCount {
		t.Skipf("bump's layout has no WRITTEN this.count row; the gate has nothing to refuse on")
	}
	context := hoistContext(kernel,
		[]string{"this.count", "this.total"},
		[]BindingKind{BindingKindNumber, BindingKindNumber},
		ctx,
		func(callee *ast.Node) *ast.Node { return bump },
	)
	context.CanHoist = true
	statements := hoistParse(t, `this.total = this.count + this.bump();`)
	context.HoistStatement = statements[0]
	call := hoistCallInside(t, statements[0])
	if _, ok := HoistCallEffect(context, call); ok {
		t.Errorf("the gate admitted a hoist of a call that writes this.count, which the statement also reads")
	}
	if len(context.Hoisted) != 0 {
		t.Errorf("len(Hoisted) after a refused hoist = %d, want 0", len(context.Hoisted))
	}
	// and a caller that carries NO slot for the written field has nothing
	// for the reordering to falsify: the write set is empty, so the gate
	// admits. This is the other side of the same rule — the gate turns on
	// whether the caller holds knowledge the call could invalidate, not on
	// whether the callee writes at all.
	unheld := hoistContext(kernel,
		[]string{"other"},
		[]BindingKind{BindingKindNumber},
		ctx,
		func(callee *ast.Node) *ast.Node { return bump },
	)
	unheld.CanHoist = true
	unheldStatements := hoistParse(t, `other = other + this.bump();`)
	unheld.HoistStatement = unheldStatements[0]
	if !hoistingIsOrderSafe(unheld, hoistCallInside(t, unheldStatements[0]), bump) {
		t.Errorf("the gate refused a hoist whose write set the caller holds no slot for")
	}
}

func TestCallHoist_WithNoStatementNamedTheGateRefuses(t *testing.T) {
	// a reordering cannot be proved safe against a statement nobody named
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearSummaryOutcomes()
	ctx, _, g := hoistCalleeProgram(t,
		"function g(n: number): number { return n + 1; }\n"+
			"function caller(y: number): number { return g(y) + 1; }\n")
	if _, has := SummaryBlobFor(ctx, g); !has {
		t.Skipf("the registry declined g's body")
	}
	context := hoistContext(kernel, []string{"x", "y"},
		[]BindingKind{BindingKindNumber, BindingKindNumber}, ctx,
		func(callee *ast.Node) *ast.Node { return g })
	context.CanHoist = true
	context.HoistStatement = nil
	statements := hoistParse(t, `x = g(y) + 1;`)
	if _, ok := HoistCallEffect(context, hoistCallInside(t, statements[0])); ok {
		t.Errorf("the gate admitted a hoist with no statement to be ordered against")
	}
}

// hoistCallInside is the first call expression anywhere inside a
// statement — what the gate tests hand to the seam directly.
func hoistCallInside(t *testing.T, statement *ast.Node) *ast.Node {
	t.Helper()
	var found *ast.Node
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if found != nil || node == nil {
			return true
		}
		if ast.IsCallExpression(node) {
			found = node
			return true
		}
		node.ForEachChild(visit)
		return false
	}
	visit(statement)
	if found == nil {
		t.Fatalf("no call expression in the statement")
	}
	return found
}
