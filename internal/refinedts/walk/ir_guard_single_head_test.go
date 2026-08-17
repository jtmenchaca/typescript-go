// Pins TestOf's strict-flavored equality reading (ir_guard_single_head.go):
// `x === undefined` / `x === null` lower to the split eqUndef/eqNull
// tests rather than the conflated IrTestDefined, and loose `x == null`
// still declines through this route exactly as it did before the split.
// Kernel-less: LowerStatements reads only syntax and the caller's
// context for these shapes, TestOf never calls context.Narrow.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

func ifGuardContext(names []string, sorts []BindingKind) *LoweringContext {
	return &LoweringContext{Bindings: names, Sorts: sorts}
}

func TestTestOf_StrictEqualsUndefinedLowersToEqUndefNotDefined(t *testing.T) {
	context := ifGuardContext([]string{"x"}, []BindingKind{BindingKindNumber})
	stmts, ok := LowerStatements(context, loweringParse(t, `if (x === undefined) { x = 0; } else { x = 1; }`))
	if !ok {
		t.Fatalf("LowerStatements ok = false, want true")
	}
	if len(stmts) != 1 || stmts[0].Kind != kernelbridge.IrStatementBranch {
		t.Fatalf("stmts = %+v, want one branch statement", stmts)
	}
	branch := stmts[0]
	if branch.On != 0 {
		t.Errorf("branch.On = %d, want 0 (x's own slot)", branch.On)
	}
	if branch.Test != kernelbridge.IrTestEqUndef {
		t.Errorf("branch.Test = %v, want IrTestEqUndef — `x === undefined` is the strict flavored test", branch.Test)
	}
}

func TestTestOf_StrictNotEqualsUndefinedLowersToEqUndefWithArmsSwapped(t *testing.T) {
	context := ifGuardContext([]string{"x"}, []BindingKind{BindingKindNumber})
	stmts, ok := LowerStatements(context, loweringParse(t, `if (x !== undefined) { x = 0; } else { x = 1; }`))
	if !ok {
		t.Fatalf("LowerStatements ok = false, want true")
	}
	if len(stmts) != 1 || stmts[0].Kind != kernelbridge.IrStatementBranch {
		t.Fatalf("stmts = %+v, want one branch statement", stmts)
	}
	branch := stmts[0]
	if branch.Test != kernelbridge.IrTestEqUndef {
		t.Errorf("branch.Test = %v, want IrTestEqUndef", branch.Test)
	}
	// `!==` negates the head: the source's THEN arm (x = 0, when x !==
	// undefined) is the eqUndef test's FALSE arm, so it rides as Else
	if len(branch.Else) != 1 || branch.Else[0].Target != 0 {
		t.Fatalf("branch.Else = %+v, want the source's then-arm write (x = 0), swapped under negation", branch.Else)
	}
	if len(branch.Then) != 1 || branch.Then[0].Target != 0 {
		t.Fatalf("branch.Then = %+v, want the source's else-arm write (x = 1), swapped under negation", branch.Then)
	}
}

func TestTestOf_StrictEqualsNullLowersToEqNull(t *testing.T) {
	context := ifGuardContext([]string{"x"}, []BindingKind{BindingKindNumber})
	stmts, ok := LowerStatements(context, loweringParse(t, `if (x === null) { x = 0; } else { x = 1; }`))
	if !ok {
		t.Fatalf("LowerStatements ok = false, want true")
	}
	if len(stmts) != 1 || stmts[0].Kind != kernelbridge.IrStatementBranch {
		t.Fatalf("stmts = %+v, want one branch statement", stmts)
	}
	branch := stmts[0]
	if branch.Test != kernelbridge.IrTestEqNull {
		t.Errorf("branch.Test = %v, want IrTestEqNull — `x === null` is the strict flavored test", branch.Test)
	}
}

// TestTestOf_LooseEqualsNullDoesNotLowerThroughTheFlavoredTests pins the
// deliberate exclusion: `x == null` is true of BOTH undefined and null,
// which neither eqUndef nor eqNull states — this shape must keep
// whatever its pre-existing lowering was (a decline through this
// number-sorted eqSlot-only `==` route), never the flavored strict test.
func TestTestOf_LooseEqualsNullDoesNotLowerThroughTheFlavoredTests(t *testing.T) {
	context := ifGuardContext([]string{"x"}, []BindingKind{BindingKindNumber})
	result, ok := TestOf(context, loweringParse(t, `x == null;`)[0].AsExpressionStatement().Expression)
	if ok && (result.Test == kernelbridge.IrTestEqUndef || result.Test == kernelbridge.IrTestEqNull) {
		t.Errorf("TestOf(x == null) = %+v, want no flavored test — loose equality admits both values", result)
	}
}
