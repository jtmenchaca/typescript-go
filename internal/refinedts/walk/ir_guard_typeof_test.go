// Pins TypeofRead's own split, consumed by lowerGuard (ir_guard.go): a
// `typeof x === "undefined"` / `!==` head is EXACTLY-UNDEFINED
// (sec-typeof-operator, tmp/ecma262/spec.html:20589-20590 — `typeof
// null` answers "object", never "undefined") and must lower to the
// flavored eqUndef test, not the conflated IrTestDefined the tag-quote
// shape below correctly keeps. Kernel-less: LowerGuard's typeof route
// reads only syntax and the caller's own Typeofs table.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

func TestLowerGuard_TypeofEqualsUndefinedLowersToEqUndefNotDefined(t *testing.T) {
	context := ifGuardContext([]string{"x"}, []BindingKind{BindingKindNumber})
	stmts, ok := LowerStatements(context, loweringParse(t, `if (typeof x === "undefined") { x = 0; } else { x = 1; }`))
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
		t.Errorf("branch.Test = %v, want IrTestEqUndef — `typeof x === \"undefined\"` is exactly-undefined, not IrTestDefined's either-admission split", branch.Test)
	}
	// the condition is true exactly when x is undefined, so the source's
	// THEN arm (x = 0) rides on eqUndef's own Then
	if len(branch.Then) != 1 || branch.Then[0].Target != 0 {
		t.Fatalf("branch.Then = %+v, want the source's then-arm write (x = 0)", branch.Then)
	}
	if len(branch.Else) != 1 || branch.Else[0].Target != 0 {
		t.Fatalf("branch.Else = %+v, want the source's else-arm write (x = 1)", branch.Else)
	}
}

func TestLowerGuard_TypeofNotEqualsUndefinedLowersToEqUndefWithArmsSwapped(t *testing.T) {
	context := ifGuardContext([]string{"x"}, []BindingKind{BindingKindNumber})
	stmts, ok := LowerStatements(context, loweringParse(t, `if (typeof x !== "undefined") { x = 0; } else { x = 1; }`))
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
	// `!==` negates the head: the source's THEN arm (x = 0, when x is
	// NOT undefined) is eqUndef's FALSE side, so it rides as Else
	if len(branch.Else) != 1 || branch.Else[0].Target != 0 {
		t.Fatalf("branch.Else = %+v, want the source's then-arm write (x = 0), swapped under negation", branch.Else)
	}
	if len(branch.Then) != 1 || branch.Then[0].Target != 0 {
		t.Fatalf("branch.Then = %+v, want the source's else-arm write (x = 1), swapped under negation", branch.Then)
	}
}

// TestLowerGuard_TypeofEqualsOwnTagStaysIrTestDefined pins the OTHER
// half of TypeofRead's split as correct and unchanged: quoting the
// slot's own known tag ("number") is not an undefined test at all — no
// scalar tag is ever "object" or "undefined", so a tag match means
// present and every miss (a different tag, undefined, OR null) means
// absent, which is exactly IrTestDefined's either-admission read.
func TestLowerGuard_TypeofEqualsOwnTagStaysIrTestDefined(t *testing.T) {
	context := ifGuardContext([]string{"x"}, []BindingKind{BindingKindNumber})
	context.Typeofs = []TypeofTag{TypeofTagNumber}
	stmts, ok := LowerStatements(context, loweringParse(t, `if (typeof x === "number") { x = 0; } else { x = 1; }`))
	if !ok {
		t.Fatalf("LowerStatements ok = false, want true")
	}
	if len(stmts) != 1 || stmts[0].Kind != kernelbridge.IrStatementBranch {
		t.Fatalf("stmts = %+v, want one branch statement", stmts)
	}
	branch := stmts[0]
	if branch.Test != kernelbridge.IrTestDefined {
		t.Errorf("branch.Test = %v, want IrTestDefined — quoting the slot's own tag is a definedness claim, not eqUndef", branch.Test)
	}
}
