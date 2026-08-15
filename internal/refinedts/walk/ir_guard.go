// from control_flow/ir_guard.ts
//
// Guard lowering for the flow IR: single tests, typeof folds,
// Number.isNaN, and `!`/`&&`/`||` nesting against prepared arms.
//
// Two shapes here carry an evaluation-order argument rather than a
// state reading, and both are written at their own site:
//
//   - a CALL left operand of `??` hoists to a temp ahead of the branch,
//     sound only where the `??` runs on every path through the
//     condition — lowerGuard's flag carries that, and a `??` under a
//     short circuit's right side refuses;
//   - a tracked leaf wearing neither the number nor the string sort has
//     no truthiness test on the wire and takes the untested branch, both
//     arms riding and joining, rather than costing the guard its
//     lowering.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// LowerGuard is lowerGuard in the TS source: a condition lowered
// against prepared arms: `!` swaps, `&&`/`||` nest (the shared arm
// rides in both branches — plain data, and the runtime evaluation
// order is preserved: the right side runs only where the left
// decided nothing). A typeof head folds under the slot's evidence; a
// call head inlines its callee and branches on the result slot's
// truthiness. Nil where no leaf reads.
//
// Its callers all hand it a condition in an UNCONDITIONAL position —
// an `if` head, a returned ternary's condition, an equality composed
// for a switch arm — which is what the hoisting route below turns on,
// so the entry point says so and the recursion carries it.
func LowerGuard(context *LoweringContext, condition *ast.Node, thn, els []kernelbridge.IrStatement) ([]kernelbridge.IrStatement, bool) {
	return lowerGuard(context, condition, thn, els, true /*unconditional*/)
}

// lowerGuard is the reading, carrying whether the subexpression it is
// handed RUNS ON EVERY PATH THROUGH THE CONDITION.
//
// WHY THE FLAG EXISTS. Every leaf below reads state and runs nothing,
// with one exception: a call left operand of `??` hoists its call to a
// temp emitted BEFORE the branch (`callGuardSlot`). That placement is
// source order only where the operand runs on every path. It does in
// `if (f() ?? b)`, where `f()` is the first thing the condition
// evaluates. It does NOT in `if (a && (f() ?? b))`, where the `&&`
// above decides whether `f()` runs at all — hoisting there would run
// `f()` on a path the source never runs it on, so the flag is down for
// everything under a short circuit's RIGHT side and the route refuses.
//
// `!` preserves it (a negation runs its operand either way), and a
// short circuit's LEFT side preserves it (the left operand of `&&`/`||`
// is the first thing evaluated); a short circuit's RIGHT side clears
// it. The `??` route's own two arms are read with the flag down for the
// same reason: the left arm is reached only where the left was defined
// and the right only where it was absent.
func lowerGuard(
	context *LoweringContext,
	condition *ast.Node,
	thn, els []kernelbridge.IrStatement,
	unconditional bool,
) ([]kernelbridge.IrStatement, bool) {
	head := Unwrapped(condition)
	if ast.IsPrefixUnaryExpression(head) {
		unary := head.AsPrefixUnaryExpression()
		if unary.Operator == ast.KindExclamationToken {
			return lowerGuard(context, unary.Operand, els, thn, unconditional)
		}
	}
	if ast.IsBinaryExpression(head) {
		bin := head.AsBinaryExpression()
		kind := bin.OperatorToken.Kind
		if kind == ast.KindAmpersandAmpersandToken {
			inner, ok := lowerGuard(context, bin.Right, thn, els, false /*unconditional*/)
			if !ok {
				return nil, false
			}
			return lowerGuard(context, bin.Left, inner, els, unconditional)
		}
		if kind == ast.KindBarBarToken {
			inner, ok := lowerGuard(context, bin.Right, thn, els, false /*unconditional*/)
			if !ok {
				return nil, false
			}
			return lowerGuard(context, bin.Left, thn, inner, unconditional)
		}
		if kind == ast.KindQuestionQuestionToken {
			// `a ?? b` in guard position: the value is `a` where `a` is
			// DEFINED and `b` where it is not, so the truth of the whole is
			// the truth of whichever side supplied the value. The definedness
			// branch on the left place picks the side, and each side's own
			// guard decides the arms underneath it — the left's truthiness in
			// the then arm, the right's whole reading in the else.
			//
			// This is definedness and not truthiness: `0 ?? b` is 0, and a
			// truthiness test on the left would wrongly hand it to `b`.
			left := Unwrapped(bin.Left)
			on, tracked := IndexOf(context, left)
			if tracked {
				defined, definedOk := lowerGuard(context, bin.Left, thn, els, false /*unconditional*/)
				if !definedOk {
					return nil, false
				}
				absent, absentOk := lowerGuard(context, bin.Right, thn, els, false /*unconditional*/)
				if !absentOk {
					return nil, false
				}
				return []kernelbridge.IrStatement{{
					Kind: kernelbridge.IrStatementBranch,
					On:   on,
					Test: kernelbridge.IrTestDefined,
					Then: defined,
					Else: absent,
				}}, true
			}
			// A CALL LEFT OPERAND — `if (f() ?? b)`, `if (this.get() ?? b)`.
			// The call hoists to a temp emitted ahead of the branch, and the
			// definedness branch reads that temp. Two things make this the
			// same form the tracked spelling has, not a weaker one:
			//
			// EVALUATION ORDER. `f()` is the FIRST thing this condition
			// evaluates and it runs on every path through it — the `??`
			// decides only whether `b` runs. So the statement position ahead
			// of the branch reproduces source order rather than reordering.
			// The flag above is what carries that: a `??` reached under a
			// short circuit's right side has it down and refuses here.
			//
			// WHAT THE TEMP CARRIES. The temp wears the unknown SORT, which
			// is not its STATE: summaryCallStatement threads the callee's ret
			// out-state into it verbatim, absence included — the orAbsent and
			// returned/thrown vocabulary put it there. IrTestDefined reads
			// that state and never consults the sort, so a served callee
			// whose summary says "a value or undefined" hands this branch the
			// definedness it tests, and one that says nothing hands `.top`,
			// which the kernel's narrowDefined still splits soundly.
			//
			// THE SLOT IS READ TWICE and both reads are of the temp: the
			// branch tests it, and the defined arm's truthiness reads it
			// again. Re-lowering `bin.Left` for that arm would take the call
			// route below and inline a SECOND run of the same call, so the
			// arm branches on the temp directly instead.
			if !unconditional {
				return nil, false
			}
			temp, hoisted := callGuardSlot(context, left)
			if !hoisted {
				return nil, false
			}
			absent, absentOk := lowerGuard(context, bin.Right, thn, els, false /*unconditional*/)
			if !absentOk {
				return nil, false
			}
			// the defined arm's own truthiness on the temp, where its sort
			// names a test; where it does not, the arm rides untested and
			// both sides join, which claims nothing about which one ran
			defined := guardTruthinessOn(context, temp, thn, els)
			return []kernelbridge.IrStatement{{
				Kind: kernelbridge.IrStatementBranch,
				On:   temp,
				Test: kernelbridge.IrTestDefined,
				Then: defined,
				Else: absent,
			}}, true
		}
	}
	if viaTypeof, ok := TypeofRead(context, head); ok {
		if viaTypeof.IsConstant {
			if viaTypeof.Value {
				return thn, true
			}
			return els, true
		}
		then, elseArm := thn, els
		if !viaTypeof.Positive {
			then, elseArm = els, thn
		}
		return []kernelbridge.IrStatement{{
			Kind: kernelbridge.IrStatementBranch,
			On:   viaTypeof.On,
			Test: kernelbridge.IrTestDefined,
			Then: then,
			Else: elseArm,
		}}, true
	}
	if IsNanShape(head) {
		on, ok := IndexOf(context, head.AsCallExpression().Arguments.Nodes[0])
		if !ok {
			return nil, false
		}
		return []kernelbridge.IrStatement{{Kind: kernelbridge.IrStatementBranch, On: on, Test: kernelbridge.IrTestIsNan, Then: thn, Else: els}}, true
	}
	if single, ok := TestOf(context, head); ok {
		then, elseArm := thn, els
		if single.Swapped {
			then, elseArm = els, thn
		}
		stmt := kernelbridge.IrStatement{
			Kind: kernelbridge.IrStatementBranch,
			On:   single.On,
			Test: single.Test,
			Then: then,
			Else: elseArm,
		}
		if single.HasW {
			w := single.W
			stmt.W = &w
		}
		if single.HasOnB {
			stmt.OnB = single.OnB
		}
		if single.Points != nil {
			stmt.Points = single.Points
		}
		return []kernelbridge.IrStatement{stmt}, true
	}
	if ast.IsCallExpression(head) {
		inlined, ok := InlineCall(context, head)
		if !ok {
			return nil, false
		}
		test := kernelbridge.IrTestTruthyNum
		if inlined.RetSort == BindingKindString {
			test = kernelbridge.IrTestTruthyStr
		}
		out := append([]kernelbridge.IrStatement{}, inlined.Stmts...)
		out = append(out, kernelbridge.IrStatement{Kind: kernelbridge.IrStatementBranch, On: inlined.RetIndex, Test: test, Then: thn, Else: els})
		return out, true
	}
	// A TRACKED SLOT WEARING NEITHER SORT — the head is a place the walk
	// carries, but TruthyNum and TruthyStr are the only truthiness tests
	// on the wire and its sort names neither, so there is no test to put
	// on it. That is not a reason to refuse the whole guard: both arms
	// walk from the state as it stood and the kernel joins their exits,
	// which a concrete run's one arm is admitted by. What is lost is the
	// narrowing the test would have put on each arm; every arm's own
	// effects and writes ride either way, and nothing false is claimed.
	//
	// Reached only after every sorted reading declined, so a slot that
	// HAS a test keeps it. The head must still be a tracked place:
	// evaluating one moves nothing, and an untested branch over a head
	// that RUNS something would leave the run unaccounted for — those
	// keep the refusal and reach the caller's own floor.
	if _, tracked := IndexOf(context, head); tracked {
		return []kernelbridge.IrStatement{{
			Kind: kernelbridge.IrStatementBranchBoth,
			Then: thn,
			Else: els,
		}}, true
	}
	return nil, false
}
