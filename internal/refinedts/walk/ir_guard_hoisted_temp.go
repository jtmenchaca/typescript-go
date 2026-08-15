// split from ir_guard.go — the hoisted-temp helpers the `??` route uses:
// resolving a guard call's temp slot and the truthiness branch on it

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// callGuardSlot resolves a guard's CALL subexpression to the temp slot
// the hoist wrote it into — the slot the branch above then tests.
//
// `await f(…)` peels to the same call: the ret-as-inner convention
// means the callee's ret slot already holds the settled value, which is
// the very slot HoistCallEffect answers.
//
// The caller owns the evaluation-order argument; this only resolves the
// slot. A call the hoist refuses — no statement stream to hoist into, a
// callee with no compiled blob, a reordering the write sets make unsafe
// — answers nothing and the caller keeps its refusal.
func callGuardSlot(context *LoweringContext, left *ast.Node) (int, bool) {
	if context == nil || left == nil {
		return 0, false
	}
	// a left operand that WRITES is not a call standing alone, and the
	// hoist carries a call's own effects and never a write beside it
	if ContainsWrite(left) {
		return 0, false
	}
	callHead := left
	if operand, isAwait := AwaitedOperandOf(left); isAwait {
		callHead = Unwrapped(operand)
	}
	if !ast.IsCallExpression(callHead) {
		return 0, false
	}
	effect, hoisted := HoistCallEffect(context, callHead)
	if !hoisted || effect.Kind != kernelbridge.LoopEffectVar {
		return 0, false
	}
	return effect.Index, true
}

// guardTruthinessOn is the truthiness branch a slot takes inside a
// guard arm: the sorted test where the slot's sort names one, and the
// untested branch where it does not.
//
// TruthyNum and TruthyStr are the only truthiness tests on the wire, so
// a slot wearing neither sort — a hoisted temp among them — has no test
// to name. The untested branch is not a refusal: both arms walk from
// the state as it stood and the kernel joins their exits, which a
// concrete run's one arm is admitted by. What is lost is the narrowing
// the test would have put on the then arm, and nothing false is claimed.
func guardTruthinessOn(
	context *LoweringContext,
	on int,
	thn, els []kernelbridge.IrStatement,
) []kernelbridge.IrStatement {
	var test kernelbridge.IrBranchTest
	if context != nil && on < len(context.Sorts) {
		switch context.Sorts[on] {
		case BindingKindNumber:
			test = kernelbridge.IrTestTruthyNum
		case BindingKindString:
			test = kernelbridge.IrTestTruthyStr
		}
	}
	if test == "" {
		return []kernelbridge.IrStatement{{
			Kind: kernelbridge.IrStatementBranchBoth,
			Then: thn,
			Else: els,
		}}
	}
	return []kernelbridge.IrStatement{{
		Kind: kernelbridge.IrStatementBranch,
		On:   on,
		Test: test,
		Then: thn,
		Else: els,
	}}
}
