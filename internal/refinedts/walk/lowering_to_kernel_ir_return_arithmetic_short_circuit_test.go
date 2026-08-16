// Pins returnArithmeticOverShortCircuit (lowering_to_kernel_ir_return_branch.go):
// `return age + (extra ?? 0)` lowers as a BRANCH on extra's slot, each
// arm assigning `#ret` the WHOLE composed arithmetic — `age + extra` on
// the defined arm, `age + 0` on the absent arm — rather than the plain
// effect grammar's single `add(var(age), join(var(extra), const(0)))`.
//
// This shape-only test runs with no kernel loaded (LowerStatements
// reads syntax and the caller's context, nothing native) — the
// kernel-gated pin beside it (TestReturnArithmeticOverShortCircuit_PickYearsEndToEnd)
// is what needs the rebuilt dylib to answer the tight join.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

func TestReturnArithmeticOverShortCircuit_NullishCoalesceOverAdditionLowersAsABranchComposingTheWholeArithmeticPerArm(t *testing.T) {
	context := loweringResultContext([]string{"age", "extra"}, []BindingKind{BindingKindNumber, BindingKindNumber})
	stmts, ok := LowerStatements(context, loweringParse(t, "return age + (extra ?? 0);"))
	if !ok {
		t.Fatalf("LowerStatements ok = false, want true")
	}
	if len(stmts) != 1 {
		t.Fatalf("len(stmts) = %d, want 1 — the one branch statement: %+v", len(stmts), stmts)
	}
	branch := stmts[0]
	if branch.Kind != kernelbridge.IrStatementBranch {
		t.Fatalf("stmts[0].Kind = %v, want IrStatementBranch", branch.Kind)
	}
	extraSlot := 1 // "age", "extra", "#done", "#ret" in that order
	if branch.On != extraSlot {
		t.Errorf("branch.On = %d, want %d (extra's own slot)", branch.On, extraSlot)
	}
	if branch.Test != kernelbridge.IrTestDefined {
		t.Errorf("branch.Test = %v, want IrTestDefined — `??` asks definedness", branch.Test)
	}

	assertComposedArm := func(label string, arm []kernelbridge.IrStatement, wantRightKind kernelbridge.LoopEffectKind, wantRightIndex int) {
		t.Helper()
		if len(arm) != 2 {
			t.Fatalf("%s arm has %d statements, want 2 (the #ret assign and the raise): %+v", label, len(arm), arm)
		}
		assign := arm[0]
		if assign.Kind != kernelbridge.IrStatementAssign || assign.Target != context.Result.Ret {
			t.Fatalf("%s arm[0] = %+v, want an assign to #ret", label, assign)
		}
		effect := assign.Effect
		if effect.Kind != kernelbridge.LoopEffectBinary || effect.Op != kernelbridge.LoopOpAdd {
			t.Fatalf("%s arm's #ret effect = %+v, want the WHOLE composed add, not a bare join", label, effect)
		}
		if effect.A == nil || effect.A.Kind != kernelbridge.LoopEffectVar || effect.A.Index != 0 {
			t.Errorf("%s arm's add.A = %+v, want var(age) (slot 0)", label, effect.A)
		}
		if effect.B == nil || effect.B.Kind != wantRightKind {
			t.Fatalf("%s arm's add.B = %+v, want Kind %v", label, effect.B, wantRightKind)
		}
		if wantRightKind == kernelbridge.LoopEffectVar && effect.B.Index != wantRightIndex {
			t.Errorf("%s arm's add.B.Index = %d, want %d (extra's own slot)", label, effect.B.Index, wantRightIndex)
		}
		if arm[1].Kind != kernelbridge.IrStatementAssign || arm[1].Target != context.Result.Done {
			t.Errorf("%s arm[1] = %+v, want the done raise", label, arm[1])
		}
	}
	// the DEFINED arm: age + extra, extra read as the tracked slot itself
	assertComposedArm("then (defined)", branch.Then, kernelbridge.LoopEffectVar, extraSlot)
	// the ABSENT arm: age + 0, the short circuit's own right operand
	assertComposedArm("else (absent)", branch.Else, kernelbridge.LoopEffectConst, 0)
	if !RaisesDone(stmts, context.Result.Done) {
		t.Errorf("RaisesDone = false, want true — every arm returns")
	}
}

// TestReturnArithmeticOverShortCircuit_StringSortNeverLowersThroughThisBranch
// pins the sort gate: `sort == BindingKindNumber` is this route's own
// admission test, checked before anything else. A string-sorted result
// never reaches this branch at all — SequenceEffectOf's own
// concatenation has no `??`/`&&`/`||` arm either (it reads only a `+`
// chain of sequence parts), so a string-sorted `name + (suffix ?? "")`
// falls all the way to the opaque/inert return below instead, which
// still lowers (control flow exact, value unknown) but never through
// IrStatementBranch or IrStatementBranchBoth — this test's job is only
// to confirm the sort gate keeps this route out of that decision
// entirely, not to characterize what the fallback route does with it.
func TestReturnArithmeticOverShortCircuit_StringSortNeverLowersThroughThisBranch(t *testing.T) {
	context := loweringResultContext([]string{"name", "suffix"}, []BindingKind{BindingKindString, BindingKindString})
	context.Sorts[len(context.Sorts)-1] = BindingKindString // #ret reads as string-sorted
	raise := kernelbridge.IrStatement{Kind: kernelbridge.IrStatementAssign, Target: context.Result.Done}
	expression := loweringParse(t, `name + (suffix ?? "");`)[0].AsExpressionStatement().Expression
	if _, ok := returnArithmeticOverShortCircuit(context, expression, BindingKindString, raise); ok {
		t.Errorf("returnArithmeticOverShortCircuit succeeded under sort == BindingKindString, want a decline — the gate is sort == BindingKindNumber")
	}
}

// TestReturnArithmeticOverShortCircuit_OrOverAdditionLowersAsABranchOnTruthiness
// pins the `||` arm the syntax-coverage fixture's padYears row exercises
// (e-class-and-function.ts's orShortCircuitCall): `return age + (extra ||
// 0)` branches on extra's own slot under IrTestTruthyNum (extra is a
// plain `number` parameter, so its sort is BindingKindNumber, unlike the
// `??` sibling above, which tests definedness regardless of sort) —
// `||` keeps the LEFT value where it is truthy and takes the RIGHT
// otherwise, the reverse assignment from `&&` below.
func TestReturnArithmeticOverShortCircuit_OrOverAdditionLowersAsABranchOnTruthiness(t *testing.T) {
	context := loweringResultContext([]string{"age", "extra"}, []BindingKind{BindingKindNumber, BindingKindNumber})
	stmts, ok := LowerStatements(context, loweringParse(t, "return age + (extra || 0);"))
	if !ok {
		t.Fatalf("LowerStatements ok = false, want true")
	}
	if len(stmts) != 1 {
		t.Fatalf("len(stmts) = %d, want 1 — the one branch statement: %+v", len(stmts), stmts)
	}
	branch := stmts[0]
	if branch.Kind != kernelbridge.IrStatementBranch {
		t.Fatalf("stmts[0].Kind = %v, want IrStatementBranch", branch.Kind)
	}
	extraSlot := 1 // "age", "extra", "#done", "#ret" in that order
	if branch.On != extraSlot {
		t.Errorf("branch.On = %d, want %d (extra's own slot)", branch.On, extraSlot)
	}
	if branch.Test != kernelbridge.IrTestTruthyNum {
		t.Errorf("branch.Test = %v, want IrTestTruthyNum — `||` asks truthiness under extra's number sort", branch.Test)
	}

	assertComposedArm := func(label string, arm []kernelbridge.IrStatement, wantRightKind kernelbridge.LoopEffectKind, wantRightIndex int) {
		t.Helper()
		if len(arm) != 2 {
			t.Fatalf("%s arm has %d statements, want 2 (the #ret assign and the raise): %+v", label, len(arm), arm)
		}
		assign := arm[0]
		if assign.Kind != kernelbridge.IrStatementAssign || assign.Target != context.Result.Ret {
			t.Fatalf("%s arm[0] = %+v, want an assign to #ret", label, assign)
		}
		effect := assign.Effect
		if effect.Kind != kernelbridge.LoopEffectBinary || effect.Op != kernelbridge.LoopOpAdd {
			t.Fatalf("%s arm's #ret effect = %+v, want the WHOLE composed add, not a bare join", label, effect)
		}
		if effect.A == nil || effect.A.Kind != kernelbridge.LoopEffectVar || effect.A.Index != 0 {
			t.Errorf("%s arm's add.A = %+v, want var(age) (slot 0)", label, effect.A)
		}
		if effect.B == nil || effect.B.Kind != wantRightKind {
			t.Fatalf("%s arm's add.B = %+v, want Kind %v", label, effect.B, wantRightKind)
		}
		if wantRightKind == kernelbridge.LoopEffectVar && effect.B.Index != wantRightIndex {
			t.Errorf("%s arm's add.B.Index = %d, want %d (extra's own slot)", label, effect.B.Index, wantRightIndex)
		}
		if arm[1].Kind != kernelbridge.IrStatementAssign || arm[1].Target != context.Result.Done {
			t.Errorf("%s arm[1] = %+v, want the done raise", label, arm[1])
		}
	}
	// the TRUTHY arm: age + extra, extra read as the tracked slot itself —
	// `||` keeps the left value where it is truthy, the "then" arm since
	// this route's IrTestTruthyNum test is affirmed on the truthy side
	assertComposedArm("then (truthy)", branch.Then, kernelbridge.LoopEffectVar, extraSlot)
	// the FALSY arm: age + 0, the short circuit's own right operand
	assertComposedArm("else (falsy)", branch.Else, kernelbridge.LoopEffectConst, 0)
	if !RaisesDone(stmts, context.Result.Done) {
		t.Errorf("RaisesDone = false, want true — every arm returns")
	}
}

// TestReturnArithmeticOverShortCircuit_AndOverAdditionLowersAsABranchWithArmsSwapped
// pins the `&&` arm the syntax-coverage fixture's gateYears row
// exercises (e-class-and-function.ts's andShortCircuitCall): `return age
// + (extra && 999)` branches on extra's own slot under IrTestTruthyNum,
// but with the then/else assignment REVERSED from `||`'s — `&&` returns
// the RIGHT operand where the left is truthy and the LEFT value itself
// where it is not, the mirror image of returnShortCircuitStatements'
// own then/els swap for the same token.
func TestReturnArithmeticOverShortCircuit_AndOverAdditionLowersAsABranchWithArmsSwapped(t *testing.T) {
	context := loweringResultContext([]string{"age", "extra"}, []BindingKind{BindingKindNumber, BindingKindNumber})
	stmts, ok := LowerStatements(context, loweringParse(t, "return age + (extra && 999);"))
	if !ok {
		t.Fatalf("LowerStatements ok = false, want true")
	}
	if len(stmts) != 1 {
		t.Fatalf("len(stmts) = %d, want 1 — the one branch statement: %+v", len(stmts), stmts)
	}
	branch := stmts[0]
	if branch.Kind != kernelbridge.IrStatementBranch {
		t.Fatalf("stmts[0].Kind = %v, want IrStatementBranch", branch.Kind)
	}
	extraSlot := 1 // "age", "extra", "#done", "#ret" in that order
	if branch.On != extraSlot {
		t.Errorf("branch.On = %d, want %d (extra's own slot)", branch.On, extraSlot)
	}
	if branch.Test != kernelbridge.IrTestTruthyNum {
		t.Errorf("branch.Test = %v, want IrTestTruthyNum — `&&` asks truthiness under extra's number sort", branch.Test)
	}

	assertComposedArm := func(label string, arm []kernelbridge.IrStatement, wantRightKind kernelbridge.LoopEffectKind, wantRightIndex int) {
		t.Helper()
		if len(arm) != 2 {
			t.Fatalf("%s arm has %d statements, want 2 (the #ret assign and the raise): %+v", label, len(arm), arm)
		}
		assign := arm[0]
		if assign.Kind != kernelbridge.IrStatementAssign || assign.Target != context.Result.Ret {
			t.Fatalf("%s arm[0] = %+v, want an assign to #ret", label, assign)
		}
		effect := assign.Effect
		if effect.Kind != kernelbridge.LoopEffectBinary || effect.Op != kernelbridge.LoopOpAdd {
			t.Fatalf("%s arm's #ret effect = %+v, want the WHOLE composed add, not a bare join", label, effect)
		}
		if effect.A == nil || effect.A.Kind != kernelbridge.LoopEffectVar || effect.A.Index != 0 {
			t.Errorf("%s arm's add.A = %+v, want var(age) (slot 0)", label, effect.A)
		}
		if effect.B == nil || effect.B.Kind != wantRightKind {
			t.Fatalf("%s arm's add.B = %+v, want Kind %v", label, effect.B, wantRightKind)
		}
		if wantRightKind == kernelbridge.LoopEffectVar && effect.B.Index != wantRightIndex {
			t.Errorf("%s arm's add.B.Index = %d, want %d (extra's own slot)", label, effect.B.Index, wantRightIndex)
		}
		if arm[1].Kind != kernelbridge.IrStatementAssign || arm[1].Target != context.Result.Done {
			t.Errorf("%s arm[1] = %+v, want the done raise", label, arm[1])
		}
	}
	// the TRUTHY arm: age + 999, the short circuit's own right operand —
	// `&&` takes the right operand where the left is truthy, which is why
	// this arm reads the CONST right side while `||`'s truthy arm above
	// reads the tracked VAR — the then/els swap this function's own doc
	// states for KindAmpersandAmpersandToken
	assertComposedArm("then (truthy)", branch.Then, kernelbridge.LoopEffectConst, 0)
	// the FALSY arm: age + extra, extra read as the tracked slot itself —
	// `&&` returns the left value itself where it is falsy
	assertComposedArm("else (falsy)", branch.Else, kernelbridge.LoopEffectVar, extraSlot)
	if !RaisesDone(stmts, context.Result.Done) {
		t.Errorf("RaisesDone = false, want true — every arm returns")
	}
}
