// split from lowering_to_kernel_ir.go — the if route and its condition reading

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// lowerIfStatement is the walk's `if` route, exactly as it stood inside
// lowerStatementList: the head read as a guard (or hoisted to a temp, or
// carried as the branch that tests nothing), the two arms lowered through
// nested LowerStatements calls, and the block's remainder gated on the done
// flag where an arm may have returned.
//
// The three answers are the walk's own three: (out, false, true) where the
// statement is done and the list carries on, (out, true, true) where the
// route consumed the block's remainder and the list is over, and
// (nil, false, false) where the body declines — the same convention
// appendLoopGatingRest answers in.
func lowerIfStatement(
	context *LoweringContext,
	s *ast.Node,
	out []kernelbridge.IrStatement,
	statements []*ast.Node,
	index int,
	mark int,
) ([]kernelbridge.IrStatement, bool, bool) {
	flush := func(out []kernelbridge.IrStatement) []kernelbridge.IrStatement {
		return append(out, TakeHoisted(context)...)
	}
	dropHoists := func() { DropHoistedFrom(context, mark) }
	ifStmt := s.AsIfStatement()
	// A HEAD THAT RUNS CODE — `if (await f()) { … }`,
	// `if (this.get()) { … }` — hoists its call to a temp ahead of
	// the branch, and the branch then tests the temp. The condition
	// runs unconditionally and FIRST in source order, so the
	// statement immediately before the branch is exactly where its
	// call belongs (ConditionTestSlot holds the whole argument).
	// Taken BEFORE the arms are walked: the nested LowerStatements
	// calls below own their own accumulation and restore this one,
	// so the head's temp survives them and flushes ahead of the
	// branch.
	headSlot, headHoisted := ConditionTestSlot(context, ifStmt.Expression)
	// `if (i < a.length) { … a[i] … }`: inside the THEN arm the
	// index is proved in range, so an index read there answers the
	// element slot outright rather than the or-absent wrapping. The
	// bound is held for that arm alone and dropped straight after.
	var dropBound func()
	if indexName, arrayName, bounded := BoundIndexOfTest(context, ifStmt.Expression); bounded {
		dropBound = HoldBoundIndex(context, indexName, arrayName)
	}
	thn, thnOk := LowerStatements(context, StatementsOf(ifStmt.ThenStatement))
	if dropBound != nil {
		dropBound()
	}
	els, elsOk := LowerStatements(context, StatementsOf(ifStmt.ElseStatement))
	var guarded []kernelbridge.IrStatement
	guardedOk := false
	if thnOk && elsOk {
		if headHoisted {
			// the head already ran, into its temp; what remains is the
			// branch on that temp — its truthiness where the sort names
			// a test, untested where it does not
			guarded = []kernelbridge.IrStatement{conditionBranchOn(context, headSlot, thn, els)}
			guardedOk = true
		} else {
			// the guard composer reads the head: single tests, typeof
			// folds, `!`/`&&`/`||` nesting, and inlined call guards
			guarded, guardedOk = LowerGuard(context, ifStmt.Expression, thn, els)
		}
		if !guardedOk && OpaqueTestableCondition(ifStmt.Expression) {
			// the last BRANCH route, after every reading declined: a
			// condition that RUNS nothing the lowering must account for
			// still gets its statement, as the branch that tests nothing.
			// Both arms walk from the state as it stood and the kernel
			// joins their exits, so an unreadable test costs precision at
			// the merge and never costs the body its lowering.
			guarded = []kernelbridge.IrStatement{{
				Kind: kernelbridge.IrStatementBranchBoth,
				Then: thn,
				Else: els,
			}}
			guardedOk = true
		}
	}
	// an arm that did not lower, or a condition no route read (it
	// WRITES, or it runs something the hoist refused), falls to the
	// havoc floor: the whole if havocs every slot either arm or the
	// head could have written, which is sound and keeps the body.
	if !guardedOk {
		dropHoists()
		havoc, havocOk := havocFloorStatements(context, s)
		if !havocOk {
			return nil, false, false
		}
		out = append(out, havoc...)
		return out, false, true
	}
	// the head's own call statement goes out FIRST, then the branch
	// that reads its temp
	out = flush(out)
	out = append(out, guarded...)
	// an arm that may have RETURNED: the block's remainder runs
	// only where the done flag stayed down — the guard is an
	// ordinary branch on the flag, so the join over both paths
	// stays inside the proved walk
	if context.Result != nil && RaisesDone(guarded, context.Result.Done) {
		rest, ok := LowerStatements(context, statements[index+1:])
		if !ok {
			return nil, false, false
		}
		if len(rest) > 0 {
			out = append(out, kernelbridge.IrStatement{
				Kind: kernelbridge.IrStatementBranch,
				On:   context.Result.Done,
				Test: kernelbridge.IrTestTruthyNum,
				Then: nil,
				Else: rest,
			})
		}
		return out, true, true
	}
	return out, false, true
}

// OpaqueTestableCondition is whether an `if` head no reading lowered may
// still stand as the branch that tests NOTHING. The walk claims nothing
// about the condition, so the only thing it must be sure of is that
// EVALUATING the condition changes no state the walk carries and hands
// nothing to a route that would otherwise have lowered it:
//
//   - no write anywhere in the subtree (ContainsWrite) — `if (m.has(k++))`
//     steps k, and skipping the step would leave the walk's slot behind
//     the real one;
//   - no `await` and no `yield`. Neither has a reading in a CONDITION:
//     ir_await.go's routes are statement-position and return-position
//     only (AwaitStatementOf, AwaitReturnStatements), and LowerGuard has
//     no await leaf — an awaited call in a head reaches LowerGuard's
//     final call-expression route, whose head is the await node, not a
//     call, so InlineCall declines and the guard declines. So today such
//     a head has no lowering anywhere and this gate is what keeps it a
//     DECLINE rather than a silently skipped settle;
//   - no `new`, no `delete`, no tagged template, no spread, and no
//     nested function, class, or loop shape. Each either constructs
//     something the slots do not carry, mutates through an operand, or
//     hides a body — none has a condition-position reading, so each
//     declines rather than riding as "no claim".
//
// An ordinary CALL is admitted: a callee cannot write the caller's
// tracked slots — a collection or record passed to one declines the
// local outright (ir_map_slots.go, the record recognizers), and a scalar
// is passed by value — so a call in the head runs nothing this walk
// carries. Its RESULT is exactly what the branch declines to read.
func OpaqueTestableCondition(condition *ast.Node) bool {
	if ContainsWrite(condition) {
		return false
	}
	admitted := true
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if !admitted {
			return true
		}
		if ast.IsAwaitExpression(node) || ast.IsYieldExpression(node) ||
			ast.IsNewExpression(node) || ast.IsDeleteExpression(node) ||
			ast.IsTaggedTemplateExpression(node) || ast.IsSpreadElement(node) ||
			ast.IsFunctionLike(node) || ast.IsClassLike(node) {
			admitted = false
			return true
		}
		node.ForEachChild(visit)
		return false
	}
	visit(condition)
	return admitted
}

// ConditionTestSlot is the CONDITION-position hoist: a branch head that
// runs code — `f()`, `this.get()`, `await f()` — lowered to a temp slot
// the branch then tests, with the run itself emitted as a statement
// ahead of the branch.
//
// A CALL, and a call whole. `new C()` is not one of these: the hoist
// builds a call statement from a resolved callee's compiled blob, and a
// constructor has no such door here. Nor is a COMPOSED head — `a && f()`
// — and that refusal is the load-bearing one: the `&&` above decides
// whether `f()` runs at all, so a temp written ahead of the branch would
// run it on a path the source never does. The shape check below is what
// enforces it, admitting only a head that IS the call after the
// parentheses and one `await` come off.
//
// THE EVALUATION-ORDER ARGUMENT, which is what makes this position
// different from an arm's. A branch's ARMS run conditionally, so hoisting
// out of one would run unconditionally what the source runs on one side
// — that is why returnValueStatements lowers CanHoist for the arms it
// reads. The CONDITION is the other case entirely: it runs
// UNCONDITIONALLY, and it runs FIRST, before either arm and before
// anything the branch decides. Emitting its call as the statement
// immediately preceding the branch therefore reproduces source order
// exactly rather than reordering anything. The hoist's own ordering gate
// (hoistingIsOrderSafe) still measures the call's write set against the
// rest of the statement, so a condition whose call writes a slot the
// statement reads around it refuses here as it does everywhere.
//
// WHAT COMES BACK is the temp's slot and the test the branch may put on
// it. The temp holds the callee's ret out-state verbatim
// (summaryCallStatement's `rets[outIndex] = target`), so a truthiness
// test under the temp's sort reads what the callee returned. Where the
// temp wears neither the number nor the string sort there is no
// truthiness test on the wire and the caller branches untested; the
// condition still RAN, which is the whole reason this route exists.
//
// A condition the hoist refuses — no compiled blob, no statement stream,
// an unsafe reordering — answers nothing, and the caller keeps whatever
// refusal it had.
func ConditionTestSlot(context *LoweringContext, condition *ast.Node) (int, bool) {
	if context == nil || condition == nil {
		return 0, false
	}
	head := Unwrapped(condition)
	// a condition that WRITES is refused before this route is ever
	// reached, and refused again here: the hoist carries a call's own
	// effects, never a write standing beside it
	if ContainsWrite(head) {
		return 0, false
	}
	// `await f(…)` peels to the same call — the ret-as-inner convention
	// means the callee's ret slot already holds the settled value
	callHead := head
	if operand, isAwait := AwaitedOperandOf(head); isAwait {
		callHead = Unwrapped(operand)
	}
	// THE WHOLE CONDITION MUST BE THE CALL. A composed head — `a && f()`,
	// `f() || b`, `f() > 0 && g()` — is a BinaryExpression here and is
	// refused: the operators above the call decide whether it runs, so its
	// statement written ahead of the branch would run it unconditionally
	// where the source runs it on one path. `if (await f())` and
	// `if (this.get())` are the admitted spelling, and for those the
	// condition IS the call and runs on every path through the branch.
	if !ast.IsCallExpression(callHead) {
		return 0, false
	}
	effect, hoisted := HoistCallEffect(context, callHead)
	if !hoisted || effect.Kind != kernelbridge.LoopEffectVar {
		return 0, false
	}
	return effect.Index, true
}

// conditionBranchOn is the branch a hoisted condition's temp takes: the
// truthiness test where the temp's sort names one, and the untested
// branch where it does not.
//
// The untested shape claims nothing about which arm ran, and both arms'
// effects and writes ride and join — which is what a concrete run's one
// arm is admitted by. It is the same no-test fallback the if route and
// the returned ternary already take for a head no leaf reads.
func conditionBranchOn(
	context *LoweringContext,
	on int,
	thn, els []kernelbridge.IrStatement,
) kernelbridge.IrStatement {
	var test kernelbridge.IrBranchTest
	if on < len(context.Sorts) {
		switch context.Sorts[on] {
		case BindingKindNumber:
			test = kernelbridge.IrTestTruthyNum
		case BindingKindString:
			test = kernelbridge.IrTestTruthyStr
		}
	}
	if test == "" {
		return kernelbridge.IrStatement{
			Kind: kernelbridge.IrStatementBranchBoth,
			Then: thn,
			Else: els,
		}
	}
	return kernelbridge.IrStatement{
		Kind: kernelbridge.IrStatementBranch,
		On:   on,
		Test: test,
		Then: thn,
		Else: els,
	}
}
