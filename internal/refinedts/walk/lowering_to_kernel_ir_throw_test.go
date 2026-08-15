// split from lowering_to_kernel_ir_test.go — the escaping throw

package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

/* ── the escaping throw ──────────────────────────────────────────── */

func TestLoweringToKernelIR_AThrowOutsideAnyTryReturnsNothingAndEndsTheBlock(t *testing.T) {
	// a run that threw returns NOTHING — the kernel's fourth Outcome,
	// distinct from the absent value a bare `return;` writes:
	// `#ret := thrown` then the raise.
	context := loweringResultContext([]string{"x"}, []BindingKind{BindingKindNumber})
	stmts, ok := LowerStatements(context, loweringParse(t, `throw new Error("nope");`))
	if !ok {
		t.Fatalf("a throw outside any try declined — it leaves the body outright")
	}
	// the thrown expression's mentions havoc first (none here), then the
	// thrown ret and the raise — and the statement is READ whole: the
	// mention havoc covers the constructor's effects, so no porous mark
	if len(stmts) < 2 {
		t.Fatalf("len(stmts) = %d, want at least the thrown ret and the raise: %+v", len(stmts), stmts)
	}
	retAssign := stmts[len(stmts)-2]
	raise := stmts[len(stmts)-1]
	if retAssign.Target != context.Result.Ret ||
		retAssign.Effect.Kind != kernelbridge.LoopEffectThrown {
		t.Errorf("stmts[-2] = %+v, want `assign #ret thrown`", retAssign)
	}
	if raise.Target != context.Result.Done {
		t.Errorf("stmts[-1] = %+v, want the done raise", raise)
	}
	if context.FirstHavoc != "" {
		t.Errorf("FirstHavoc = %q, want none — the throw's mentions are enumerable, so the statement is read", context.FirstHavoc)
	}
}

func TestLoweringToKernelIR_AThrowInsideItsCatchingTryLowersAndTheCatchArmKeepsItsWrites(t *testing.T) {
	// the try route lowers to ONE branchBoth: the try arm walked whole —
	// the throw writing the thrown ret and raising done — and the catch
	// arm a SIBLING walked from the state as it stood, so the flag raised
	// in the try arm cannot gate the catch's own writes
	// (ThrowCoveredByItsTry holds the argument).
	context := loweringResultContext([]string{"x"}, []BindingKind{BindingKindNumber})
	source := loweringParse(t, `try { throw e; } catch (e) { x = 1; }`)
	stmts, ok := LowerStatements(context, source)
	if !ok {
		t.Fatalf("a try whose block throws declined — its own lowering covers the throw")
	}
	if len(stmts) != 1 || stmts[0].Kind != kernelbridge.IrStatementBranchBoth {
		t.Fatalf("stmts = %+v, want the one branchBoth", stmts)
	}
	branch := stmts[0]
	if len(branch.Then) != 2 {
		t.Fatalf("the try arm = %+v, want the thrown ret and the raise", branch.Then)
	}
	if branch.Then[0].Target != context.Result.Ret ||
		branch.Then[0].Effect.Kind != kernelbridge.LoopEffectThrown {
		t.Errorf("try arm stmts[0] = %+v, want `assign #ret thrown`", branch.Then[0])
	}
	if branch.Then[1].Target != context.Result.Done {
		t.Errorf("try arm stmts[1] = %+v, want the done raise", branch.Then[1])
	}
	if len(branch.Else) != 1 {
		t.Fatalf("the catch arm = %+v, want the one write to x", branch.Else)
	}
	write := branch.Else[0]
	if write.Kind != kernelbridge.IrStatementAssign || write.Target != 0 ||
		write.Effect.Kind != kernelbridge.LoopEffectConst {
		t.Errorf("the catch arm's statement = %+v, want `assign x {1}`", write)
	}
}
