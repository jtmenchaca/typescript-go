// The row this file pins: n-dual-stack.ts:54
// (reassignedCalleeFallsToGoFlow). `let call = nextYear; call = (age:
// number) => age + 1;` then `const ok: Age = call(10);` — an IN-SET
// line (call(10) = 11) that must stay silent.
//
// The bug this pins: InlineStoredClosure required a CONST binding
// outright (a let could have been rebound anywhere between
// declaration and call, so resolving its callee unconditionally would
// be unsound) — so `call`, being let-bound, never resolved to any
// closure at all. ContractOf also declines (call's own symbol was
// never registered — only nextYear's was, under nextYear's OWN
// declaration node). The call fell to UnmodeledCallResult, which
// answers KindUnknown (nothing in scope states call's return type as
// Age), correctly reported as 7002 in itself — but the ROW expects
// 11 to be determined and silent, since at this exact call site `call`
// unambiguously holds the arrow.
//
// The fix, immediatelyPrecedingClosureAssignment (inliner.go): a LET
// binding now also qualifies when the call's own immediately
// preceding SIBLING statement, in the same statement list, is a plain
// `name = <arrow-or-function-expression>` reassignment of it — no
// scan past a block boundary, no branch crossed. This is narrower than
// the const case on purpose: every OTHER let-bound callee (one
// reassigned across a branch, or read further than one statement from
// its last write) still declines, exactly as before.
//
// The marked twin (line 57, `return call(120);`) is NOT immediately
// preceded by the reassignment (it is the LAST statement, preceded by
// `void ok;`), so immediatelyPrecedingClosureAssignment declines for
// it and it is expected to keep firing through whatever mechanism
// already fires it today (unaffected by this fix) — pinned here too
// so a regression that accidentally widens the pin past one statement
// shows immediately.
//
// No kernel needed: ContractOf/InlineStoredClosure resolution and the
// evaluated scalar are asserted directly, the same as
// super_and_array_ctor_test.go's no-kernel half
// (TestSuperCallContract_*, TestContractOf_*).
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
)

// reassignedLetCalleeOwnReturn is the OUTER function's own return
// statement — both fixtures in this file nest a `function nextYear`
// declaration ahead of it, and that nested function has its own
// `return age + 1;` which superArrayFirstNode's unconstrained descent
// would find FIRST (it walks every node in source order with no
// function-boundary stop). The outer body's own return is the one this
// file means to read, so the walk here stops at any nested function/
// arrow boundary — the same boundary scanStaticWrites
// (class_static_field_invariants.go) and AssignedNames already respect
// for the same reason.
func reassignedLetCalleeOwnReturn(t *testing.T, root *ast.Node) *ast.Node {
	t.Helper()
	var found *ast.Node
	var visit func(node *ast.Node)
	visit = func(node *ast.Node) {
		if found != nil || node == nil {
			return
		}
		if node != root && (ast.IsFunctionDeclaration(node) || ast.IsFunctionExpression(node) || ast.IsArrowFunction(node)) {
			return
		}
		if ast.IsReturnStatement(node) {
			found = node
			return
		}
		node.ForEachChild(func(child *ast.Node) bool {
			visit(child)
			return found != nil
		})
	}
	visit(root)
	if found == nil {
		t.Fatalf("no return statement in the function's own body (not counting a nested function's)")
	}
	return found
}

const reassignedLetCalleeSource = "function reassignedCalleeFallsToGoFlow(): number {\n" +
	"  function nextYear(age: number): number {\n" +
	"    return age + 1;\n" +
	"  }\n" +
	"  let call = nextYear;\n" +
	"  call = (age: number) => age + 1;\n" +
	"  return call(10);\n" +
	"}\n"

// TestReassignedLetCallee_TheImmediatelyPrecedingReassignmentPinsTheArrow
// is the row's own in-set leg: call(10) must evaluate to the exact
// scalar 11, read off the arrow assigned one statement earlier.
func TestReassignedLetCallee_TheImmediatelyPrecedingReassignmentPinsTheArrow(t *testing.T) {
	p := entryEnvTestProgram(t, reassignedLetCalleeSource)
	ctx := superArrayContracts(t, p)
	fn := entryEnvFunctionNamed(t, p, "reassignedCalleeFallsToGoFlow")
	returned := reassignedLetCalleeOwnReturn(t, fn.Body())

	callExpr := returned.AsReturnStatement().Expression
	if !ast.IsCallExpression(callExpr) {
		t.Fatalf("reassignedCalleeFallsToGoFlow's return is not a call expression")
	}

	value := evaluateExpression(ctx, NewEnv(), callExpr)
	if value.Kind != abstractdomain.KindValues || len(value.Values) != 1 || value.Values[0] != 11 {
		spelled, _ := abstractdomain.FormatAbstractValue(value)
		t.Errorf("call(10) = %q, want the exact scalar 11 (the arrow assigned the immediately preceding statement)", spelled)
	}
}

// TestImmediatelyPrecedingClosureAssignment_DeclinesPastOneStatement is
// the marked twin's own shape: a call NOT immediately preceded by the
// reassignment (an unrelated statement sits between them) must not
// resolve — the general reassigned-callee case stays declined.
func TestImmediatelyPrecedingClosureAssignment_DeclinesPastOneStatement(t *testing.T) {
	source := "function f(): number {\n" +
		"  function nextYear(age: number): number {\n" +
		"    return age + 1;\n" +
		"  }\n" +
		"  let call = nextYear;\n" +
		"  call = (age: number) => age + 1;\n" +
		"  const ok = call(10);\n" +
		"  void ok;\n" +
		"  return call(120);\n" +
		"}\n"
	p := entryEnvTestProgram(t, source)
	fn := entryEnvFunctionNamed(t, p, "f")
	returned := reassignedLetCalleeOwnReturn(t, fn.Body())
	callExpr := returned.AsReturnStatement().Expression
	if !ast.IsCallExpression(callExpr) {
		t.Fatalf("f's return is not a call expression")
	}
	calleeExpression := callExpr.AsCallExpression().Expression
	if pinned := immediatelyPrecedingClosureAssignment(calleeExpression); pinned != nil {
		t.Errorf("immediatelyPrecedingClosureAssignment pinned a callee two statements past its last reassignment — got a node, want nil")
	}
}
