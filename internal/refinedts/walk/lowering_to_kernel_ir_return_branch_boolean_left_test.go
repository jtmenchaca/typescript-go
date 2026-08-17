// Pins returnBooleanLeftShortCircuit (lowering_to_kernel_ir_return_branch.go):
// `return a && b` / `a || b` whose LEFT operand's value is a BOOLEAN
// lowers as a BRANCH whose left-side arm writes the constant that value
// provably is — {0} for `&&`'s falsy side, {1} for `||`'s truthy side —
// rather than reading the left back from a slot it has none of.
//
// Before this route, shortCircuitLeftSlot admitted only a tracked slot
// read or a hoisted call, so every comparison-headed short circuit
// refused the branch entirely. The recharts row that names the shape is
// Label.tsx:221, `return content != null && typeof content === 'function'`.
//
// These are shape-only tests: LowerStatements reads syntax and the
// caller's context, nothing native, so no kernel is loaded.
package walk

import (
	"reflect"
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// exactConstantArm asserts an arm is the two statements a returned
// constant emits — `#ret := {want}` then the done raise.
func exactConstantArm(t *testing.T, label string, arm []kernelbridge.IrStatement, context *LoweringContext, want float64) {
	t.Helper()
	if len(arm) != 2 {
		t.Fatalf("%s arm has %d statements, want 2 (the #ret assign and the raise): %+v", label, len(arm), arm)
	}
	assign := arm[0]
	if assign.Kind != kernelbridge.IrStatementAssign || assign.Target != context.Result.Ret {
		t.Fatalf("%s arm[0] = %+v, want an assign to #ret", label, assign)
	}
	if assign.Effect.Kind != kernelbridge.LoopEffectConst {
		t.Fatalf("%s arm's #ret effect = %+v, want the exact boolean constant", label, assign.Effect)
	}
	wantSet := refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{want}))
	if !reflect.DeepEqual(assign.Effect.Set, wantSet) {
		t.Errorf("%s arm's #ret constant = %+v, want exactly {%v}", label, assign.Effect.Set, want)
	}
	if arm[1].Kind != kernelbridge.IrStatementAssign || arm[1].Target != context.Result.Done {
		t.Errorf("%s arm[1] = %+v, want the done raise", label, arm[1])
	}
}

// trackedCopyArm asserts an arm returns a tracked slot's whole state.
func trackedCopyArm(t *testing.T, label string, arm []kernelbridge.IrStatement, context *LoweringContext, slot int) {
	t.Helper()
	if len(arm) != 2 {
		t.Fatalf("%s arm has %d statements, want 2: %+v", label, len(arm), arm)
	}
	assign := arm[0]
	if assign.Kind != kernelbridge.IrStatementAssign || assign.Target != context.Result.Ret {
		t.Fatalf("%s arm[0] = %+v, want an assign to #ret", label, assign)
	}
	if assign.Effect.Kind != kernelbridge.LoopEffectVarState || assign.Effect.Index != slot {
		t.Errorf("%s arm's #ret effect = %+v, want varState(%d)", label, assign.Effect, slot)
	}
}

// TestReturnBooleanLeftShortCircuit_AndOverAComparisonBranchesAndWritesFalseOnTheFalsyArm
// is the `&&` direction: `a > 0 && b` returns `b` where the comparison
// held and the comparison's own value — exactly `false`, {0} — where it
// did not.
func TestReturnBooleanLeftShortCircuit_AndOverAComparisonBranchesAndWritesFalseOnTheFalsyArm(t *testing.T) {
	context := loweringResultContext([]string{"a", "b"}, []BindingKind{BindingKindNumber, BindingKindNumber})
	raise := kernelbridge.IrStatement{Kind: kernelbridge.IrStatementAssign, Target: context.Result.Done}
	expression := loweringParse(t, "return a > 0 && b;")[0].AsReturnStatement().Expression
	stmts, ok := returnBranchStatements(context, expression, BindingKindNumber, raise)
	if !ok {
		t.Fatalf("returnBranchStatements ok = false, want true — a comparison left needs no slot")
	}
	if len(stmts) != 1 || stmts[0].Kind != kernelbridge.IrStatementBranch {
		t.Fatalf("stmts = %+v, want the one branch statement", stmts)
	}
	branch := stmts[0]
	if branch.On != 0 {
		t.Errorf("branch.On = %d, want 0 (a's own slot — the comparison's tracked side)", branch.On)
	}
	// the HELD arm returns `b` itself; the falsy arm returns the
	// comparison's own value, which is exactly false
	trackedCopyArm(t, "then (a > 0 held)", branch.Then, context, 1)
	exactConstantArm(t, "else (a > 0 failed)", branch.Else, context, 0)
}

// TestReturnBooleanLeftShortCircuit_OrOverAComparisonWritesTrueOnTheHeldArm
// is the `||` mirror: the held arm returns the comparison's own value —
// exactly `true`, {1} — and the failed arm runs the right operand.
func TestReturnBooleanLeftShortCircuit_OrOverAComparisonWritesTrueOnTheHeldArm(t *testing.T) {
	context := loweringResultContext([]string{"a", "b"}, []BindingKind{BindingKindNumber, BindingKindNumber})
	raise := kernelbridge.IrStatement{Kind: kernelbridge.IrStatementAssign, Target: context.Result.Done}
	expression := loweringParse(t, "return a > 0 || b;")[0].AsReturnStatement().Expression
	stmts, ok := returnBranchStatements(context, expression, BindingKindNumber, raise)
	if !ok {
		t.Fatalf("returnBranchStatements ok = false, want true")
	}
	if len(stmts) != 1 || stmts[0].Kind != kernelbridge.IrStatementBranch {
		t.Fatalf("stmts = %+v, want the one branch statement", stmts)
	}
	branch := stmts[0]
	exactConstantArm(t, "then (a > 0 held)", branch.Then, context, 1)
	trackedCopyArm(t, "else (a > 0 failed)", branch.Else, context, 1)
}

// TestReturnBooleanLeftShortCircuit_AStrictEqualityLeftLowersLikeTheLabelRow
// pins the recharts Label.tsx:221 shape as far as the guard machinery
// reads it: `return content !== null && typeof content === 'function'`
// — a strict-equality left and a typeof right, both boolean-valued —
// lowers as a branch whose falsy arm is exactly {0}.
//
// The row AS WRITTEN in recharts spells the left `content != null`, with
// a LOOSE `!=`, which the sibling refusal test below pins as still
// unlowered and names why.
func TestReturnBooleanLeftShortCircuit_AStrictEqualityLeftLowersLikeTheLabelRow(t *testing.T) {
	context := loweringResultContext([]string{"content"}, []BindingKind{BindingKindNumber})
	raise := kernelbridge.IrStatement{Kind: kernelbridge.IrStatementAssign, Target: context.Result.Done}
	expression := loweringParse(t, "return content !== null && typeof content === 'function';")[0].AsReturnStatement().Expression
	stmts, ok := returnBranchStatements(context, expression, BindingKindNumber, raise)
	if !ok {
		t.Fatalf("returnBranchStatements ok = false, want true — a strict-equality left is boolean-valued and LowerGuard reads it")
	}
	if len(stmts) == 0 {
		t.Fatalf("stmts is empty, want at least the branch")
	}
	if !RaisesDone(stmts, context.Result.Done) {
		t.Errorf("RaisesDone = false, want true — every arm of this return returns")
	}
}

// TestReturnBooleanLeftShortCircuit_TheLabelRowsLooseInequalityLeftIsNotYetRead
// pins Label.tsx:221 EXACTLY as recharts writes it —
// `return content != null && typeof content === 'function'` — as still
// unlowered, and names the one reason.
//
// The left is a LOOSE `!=`. TestOf (ir_guard_single_head.go) has no arm
// for loose `!=` at all: only `===`, `!==`, and `==` are read, because a
// loose `!= null` is true of BOTH absent values and no flavored test on
// the wire splits it that way. So LowerGuard reads nothing for this
// left, and this route declines rather than falling to an untested
// branch — without a test, neither arm's constant is justified, and
// claiming {0} on a path the source may have returned {1} on would be a
// wrong answer about the returned value.
//
// WHAT WOULD LOWER IT is a guard-side reading for loose `!= null` /
// `== null` as the EITHER-ABSENT test — the same admission IrTestDefined
// already carries (it splits any KnownState into its defined and absent
// halves, both flavors together), which is exactly loose null-equality's
// own semantics (sec-abstract-equality-comparison: null and undefined
// compare equal to each other and to nothing else). That reading belongs
// to TestOf/LowerGuard (ir_guard_single_head.go, ir_guard.go), not to
// this file; this test is the pin that flips green when it lands.
func TestReturnBooleanLeftShortCircuit_TheLabelRowsLooseInequalityLeftIsNotYetRead(t *testing.T) {
	context := loweringResultContext([]string{"content"}, []BindingKind{BindingKindNumber})
	raise := kernelbridge.IrStatement{Kind: kernelbridge.IrStatementAssign, Target: context.Result.Done}
	expression := loweringParse(t, "return content != null && typeof content === 'function';")[0].AsReturnStatement().Expression
	binary := Unwrapped(expression).AsBinaryExpression()
	// the left IS boolean-valued — this route recognizes the shape
	if !booleanValuedExpression(binary.Left) {
		t.Errorf("booleanValuedExpression(`content != null`) = false, want true — a comparison is boolean-valued whatever its token")
	}
	// what refuses is the guard reading of the loose token, one layer down
	arm := []kernelbridge.IrStatement{{Kind: kernelbridge.IrStatementAssign, Target: context.Result.Ret}}
	if _, ok := LowerGuard(context, binary.Left, arm, arm); ok {
		t.Errorf("LowerGuard read `content != null` — if this now lowers, the return below should too and this test's expectation is stale")
	}
	if _, ok := returnBooleanLeftShortCircuit(context, binary, BindingKindNumber, raise); ok {
		t.Errorf("returnBooleanLeftShortCircuit accepted a left LowerGuard cannot read — without a test neither arm's constant is justified")
	}
}

// TestReturnBooleanLeftShortCircuit_ANegationLeftIsBooleanValuedToo pins
// the `!` shape: logical NOT produces a boolean whatever its operand is,
// so `!a || b` writes {1} on the arm where `!a` held.
func TestReturnBooleanLeftShortCircuit_ANegationLeftIsBooleanValuedToo(t *testing.T) {
	context := loweringResultContext([]string{"a", "b"}, []BindingKind{BindingKindNumber, BindingKindNumber})
	raise := kernelbridge.IrStatement{Kind: kernelbridge.IrStatementAssign, Target: context.Result.Done}
	expression := loweringParse(t, "return !a || b;")[0].AsReturnStatement().Expression
	stmts, ok := returnBranchStatements(context, expression, BindingKindNumber, raise)
	if !ok {
		t.Fatalf("returnBranchStatements ok = false, want true — `!a` is boolean-valued")
	}
	if len(stmts) != 1 || stmts[0].Kind != kernelbridge.IrStatementBranch {
		t.Fatalf("stmts = %+v, want the one branch statement", stmts)
	}
	// `!` swaps LowerGuard's arms, so the constant {1} sits on the arm the
	// branch's Else names — what matters is that one arm is exactly {1}
	// and the other copies b
	branch := stmts[0]
	exactConstantArm(t, "the !a-held arm", branch.Else, context, 1)
	trackedCopyArm(t, "the !a-failed arm", branch.Then, context, 1)
}

// TestReturnBooleanLeftShortCircuit_ANestOfComparisonsIsBooleanValued
// pins the recursion in booleanValuedExpression: `(a > 0 && b < 5) && a`
// has a left that is itself a short circuit of two comparisons, whose
// value is therefore a boolean, so the whole lowers through this route.
func TestReturnBooleanLeftShortCircuit_ANestOfComparisonsIsBooleanValued(t *testing.T) {
	context := loweringResultContext([]string{"a", "b"}, []BindingKind{BindingKindNumber, BindingKindNumber})
	raise := kernelbridge.IrStatement{Kind: kernelbridge.IrStatementAssign, Target: context.Result.Done}
	expression := loweringParse(t, "return (a > 0 && b < 5) && a;")[0].AsReturnStatement().Expression
	if _, ok := returnBranchStatements(context, expression, BindingKindNumber, raise); !ok {
		t.Fatalf("returnBranchStatements ok = false, want true — a nest of comparisons is boolean-valued")
	}
}

/* ── the refusals, each for its own stated reason ─────────────────── */

// TestReturnBooleanLeftShortCircuit_APlainTrackedLeftKeepsTheSlotRoute is
// the negative control that keeps the two routes apart: `a && b` with a
// bare identifier left is NOT boolean-valued (it evaluates to `a` itself
// on the falsy side, whose value no constant spells), so it must keep
// taking shortCircuitLeftSlot's whole-state copy — never the boolean
// constant, which would be a wrong answer about the returned value.
func TestReturnBooleanLeftShortCircuit_APlainTrackedLeftKeepsTheSlotRoute(t *testing.T) {
	context := loweringResultContext([]string{"a", "b"}, []BindingKind{BindingKindNumber, BindingKindNumber})
	raise := kernelbridge.IrStatement{Kind: kernelbridge.IrStatementAssign, Target: context.Result.Done}
	expression := loweringParse(t, "return a && b;")[0].AsReturnStatement().Expression
	binary := Unwrapped(expression).AsBinaryExpression()
	if _, ok := returnBooleanLeftShortCircuit(context, binary, BindingKindNumber, raise); ok {
		t.Fatalf("returnBooleanLeftShortCircuit accepted a bare identifier left — `a && b` returns `a` itself where a is falsy, which no boolean constant spells")
	}
	// and the slot route still answers it, reading a's own state back
	stmts, ok := returnBranchStatements(context, expression, BindingKindNumber, raise)
	if !ok {
		t.Fatalf("returnBranchStatements ok = false — the slot route still owns this shape")
	}
	branch := stmts[0]
	trackedCopyArm(t, "else (a falsy)", branch.Else, context, 0)
}

// TestReturnBooleanLeftShortCircuit_NullishOverABooleanLeftKeepsTheRefusal
// pins the one shape this route deliberately declines. A boolean is
// never null or undefined, so `a > 0 ?? b` always evaluates to the
// comparison and the right operand is dead — a true and stronger claim
// than a branch, but one needing a one-armed form this route does not
// build. It refuses rather than lowering a branch whose second arm
// cannot run.
func TestReturnBooleanLeftShortCircuit_NullishOverABooleanLeftKeepsTheRefusal(t *testing.T) {
	context := loweringResultContext([]string{"a", "b"}, []BindingKind{BindingKindNumber, BindingKindNumber})
	raise := kernelbridge.IrStatement{Kind: kernelbridge.IrStatementAssign, Target: context.Result.Done}
	expression := loweringParse(t, "return a > 0 ?? b;")[0].AsReturnStatement().Expression
	binary := Unwrapped(expression).AsBinaryExpression()
	if _, ok := returnBooleanLeftShortCircuit(context, binary, BindingKindNumber, raise); ok {
		t.Errorf("returnBooleanLeftShortCircuit accepted `??` over a boolean left — the right operand is dead there, which this route does not spell")
	}
}

// TestReturnBooleanLeftShortCircuit_ALeftNoGuardReadsKeepsTheRefusal pins
// the decline this route must keep rather than weaken: without a test,
// neither arm's constant is justified — an untested branch would claim
// `false` on a path the source may have returned `true` on. `q.deep > 0`
// has no slot in this context, so LowerGuard reads nothing and the route
// refuses instead of falling to an untested branch.
func TestReturnBooleanLeftShortCircuit_ALeftNoGuardReadsKeepsTheRefusal(t *testing.T) {
	context := loweringResultContext([]string{"b"}, []BindingKind{BindingKindNumber})
	raise := kernelbridge.IrStatement{Kind: kernelbridge.IrStatementAssign, Target: context.Result.Done}
	expression := loweringParse(t, "return unresolvable.deep > 0 && b;")[0].AsReturnStatement().Expression
	binary := Unwrapped(expression).AsBinaryExpression()
	if _, ok := returnBooleanLeftShortCircuit(context, binary, BindingKindNumber, raise); ok {
		t.Errorf("returnBooleanLeftShortCircuit accepted a left no guard reads — without a test neither constant is justified")
	}
}
