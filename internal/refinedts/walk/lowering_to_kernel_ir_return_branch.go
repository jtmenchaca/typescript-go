// split from lowering_to_kernel_ir.go — the branch-shaped return

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// returnBranchStatements lowers a returned TERNARY or SHORT-CIRCUIT as a
// BRANCH rather than as an effect.
//
// The shapes, and why the effect grammar cannot hold them:
//
//	return c ? a : b
//	return a ?? b        return a && b        return a || b
//
// each evaluate to one OPERAND, not to a boolean, so the guard route
// above — which writes {1} on the true path and {0} on the false one —
// would be a wrong claim about the value. The effect grammar is the
// other door, and it is gated on the whole expression moving nothing
// (effect_expression.go's conditional and short-circuit arms): an arm
// holding a call, a `new`, or an await needs a STATEMENT to run, and an
// effect is not a statement. Every nest instance of this shape has such
// an arm, so both doors are shut and the return falls to the opaque
// floor with its value unknown AND its arms' effects lost.
//
// As a branch there is room for both. Each arm lowers `#ret := <arm>`
// through returnValueStatements, which is the SAME statement vocabulary
// the return route walks — an arm that is a served call takes the call
// route, an arm with no spelling takes unknown plus its own mention
// havoc — and the raise rides inside each arm, so the flag is up on
// exactly the paths that returned.
//
// Where the condition reads as a test the branch carries it; where it
// does not, branchBoth carries no test and both arms walk from the state
// as it stood. Both are sound: a concrete run takes one arm, and the
// join over the two admits it either way.
//
// A CONDITION THAT RUNS CODE takes the hoist first (ConditionTestSlot):
// the condition evaluates UNCONDITIONALLY and FIRST in source order, so
// a statement position ahead of the branch is exactly where its call
// belongs, and after the hoist what the branch reads is a temp.
func returnBranchStatements(
	context *LoweringContext,
	expression *ast.Node,
	sort BindingKind,
	raise kernelbridge.IrStatement,
) ([]kernelbridge.IrStatement, bool) {
	if context == nil || context.Result == nil || expression == nil {
		return nil, false
	}
	head := Unwrapped(expression)
	if ast.IsConditionalExpression(head) {
		cond := head.AsConditionalExpression()
		// a CONDITION that writes is the one outright refusal: the branch
		// puts the condition's evaluation nowhere, so a write inside it
		// would be a move no statement here accounts for. (The same rule
		// the effect grammar's ternary arm states.)
		if ContainsWrite(cond.Condition) {
			return nil, false
		}
		// a condition that RUNS something is hoisted where it can be — the
		// hoisted temp then stands in for the whole condition — and refused
		// where it cannot. OpaqueTestableCondition is the branch routes'
		// own test for "evaluating this moves nothing the walk carries",
		// and it is asked of what the branch will actually read.
		hoistedTest, viaHoist := ConditionTestSlot(context, cond.Condition)
		if !viaHoist && !OpaqueTestableCondition(cond.Condition) {
			return nil, false
		}
		thn, thnOk := returnValueStatements(context, cond.WhenTrue, sort, raise)
		els, elsOk := returnValueStatements(context, cond.WhenFalse, sort, raise)
		if !thnOk || !elsOk {
			return nil, false
		}
		if viaHoist {
			// the condition already ran, into its temp; what remains is the
			// branch on that temp. A temp wearing the number or the string
			// sort takes its truthiness test; an unknown-sorted one has no
			// truthiness test on the wire and takes the untested branch,
			// both arms riding and joining.
			return []kernelbridge.IrStatement{conditionBranchOn(context, hoistedTest, thn, els)}, true
		}
		if guarded, ok := LowerGuard(context, cond.Condition, thn, els); ok {
			return guarded, true
		}
		return []kernelbridge.IrStatement{{
			Kind: kernelbridge.IrStatementBranchBoth,
			Then: thn,
			Else: els,
		}}, true
	}
	if ast.IsBinaryExpression(head) {
		return returnShortCircuitStatements(context, head.AsBinaryExpression(), sort, raise)
	}
	return nil, false
}

// returnShortCircuitStatements lowers `return a ?? b`, `return a && b`,
// and `return a || b` as a branch on the LEFT operand.
//
// THE EVALUATION-ORDER ARGUMENT, which is what constrains the left side.
// A short-circuit evaluates `a` exactly ONCE, and `a` is both the test
// and one of the two returned values:
//
//	a ?? b   →  a where a is DEFINED, b where it is null/undefined
//	a || b   →  a where a is TRUTHY,  b otherwise
//	a && b   →  b where a is TRUTHY,  a otherwise
//
// The wire has no test-and-reuse: a branch names a slot to test, and an
// arm names an effect to write, with no way to say "the value already
// computed for the test". So the only left operands this route admits
// are ones whose evaluation lands in a SLOT that can be read twice — the
// test reads slot `on`, the arm reads slot `on` again, and two reads of
// a slot are the same value with nothing run between them.
//
// TWO SPELLINGS SATISFY THAT, and both land here.
//
//   - A TRACKED SLOT READ, which is what IndexOf answers, and what
//     LowerGuard's own `??` arm requires of its left side (ir_guard.go's
//     definedness branch). Evaluating it moves nothing, so reading it
//     twice is reading one value.
//
//   - A SERVED CALL, hoisted to its temp slot ahead of the branch —
//     `f() ?? b`, `this.get() || b`. The hoist (ir_call_hoist.go) emits
//     the call statement BEFORE the statement holding the expression,
//     which is exactly where `a`'s single evaluation belongs: the left
//     operand of a short circuit is the FIRST thing the expression
//     evaluates, and the branch that follows tests what it produced.
//     After the hoist the two reads are two reads of the temp, with the
//     call standing before both — the same one-value discipline the
//     tracked-slot spelling has.
//
// WHAT THE TEMP CARRIES, which is what the earlier refusal here got
// wrong. The hoisted temp wears the UNKNOWN sort, and the refusal read
// that as "carries no definedness". Sort and STATE are different things:
// summaryCallStatement threads `rets[outIndex] = target`, so the temp's
// state is the callee's own ret out-state verbatim — absence included,
// which is what the orAbsent vocabulary and the returned/thrown split
// put there. IrTestDefined reads that STATE (the kernel's narrowDefined
// splits any KnownState, `.top` included, into its defined and absent
// halves) and never consults the sort. So a served callee whose summary
// says "a value or undefined" hands this branch exactly the definedness
// it tests, and one whose summary says nothing hands it `.top`, which
// narrowDefined still splits soundly — the arms then claim only what
// each side's own writes claim.
//
// TRUTHINESS IS THE SORTED TEST, and that is where `&&`/`||` differ:
// TruthyNum and TruthyStr are the only two on the wire, so a slot
// wearing neither sort — an unknown-sorted temp among them — has no
// truthiness test to name. It does NOT decline: a branch with no test
// (branchBoth) walks both arms from the state as it stood and joins
// their exits, which is what a concrete run's one arm is admitted by.
// Each arm's effects and its `#ret` write ride either way; what is lost
// is only the narrowing the test would have put on the left arm. That is
// strictly more than the decline served and claims nothing false.
//
// The RIGHT operand rides as an ordinary arm — the full statement
// vocabulary, calls included — because it sits inside a branch arm,
// which is a statement position.
func returnShortCircuitStatements(
	context *LoweringContext,
	binary *ast.BinaryExpression,
	sort BindingKind,
	raise kernelbridge.IrStatement,
) ([]kernelbridge.IrStatement, bool) {
	kind := binary.OperatorToken.Kind
	if kind != ast.KindQuestionQuestionToken && kind != ast.KindAmpersandAmpersandToken &&
		kind != ast.KindBarBarToken {
		return nil, false
	}
	on, tracked := shortCircuitLeftSlot(context, binary.Left)
	if !tracked {
		return nil, false
	}
	// which test picks the side is the operator's own rule: `??` asks
	// definedness, which every slot answers; `&&`/`||` ask truthiness
	// under the slot's sort, and a slot wearing neither the number nor
	// the string sort has no truthiness test on the wire — that side takes
	// the untested branch instead, both arms riding and joining.
	test := kernelbridge.IrTestDefined
	tested := true
	if kind != ast.KindQuestionQuestionToken {
		switch context.Sorts[on] {
		case BindingKindNumber:
			test = kernelbridge.IrTestTruthyNum
		case BindingKindString:
			test = kernelbridge.IrTestTruthyStr
		default:
			tested = false
		}
	}
	// the arm holding `a` writes the slot it just tested — the same read,
	// under the narrowing the test put on that side
	leftArm := []kernelbridge.IrStatement{
		{Kind: kernelbridge.IrStatementAssign, Target: context.Result.Ret, Effect: varEffect(on)},
		raise,
	}
	rightArm, rightOk := returnValueStatements(context, binary.Right, sort, raise)
	if !rightOk {
		return nil, false
	}
	// `a && b` returns `b` where the test HOLDS and `a` where it does not;
	// `a ?? b` and `a || b` are the other way round
	then, els := leftArm, rightArm
	if kind == ast.KindAmpersandAmpersandToken {
		then, els = rightArm, leftArm
	}
	if !tested {
		return []kernelbridge.IrStatement{{
			Kind: kernelbridge.IrStatementBranchBoth,
			Then: then,
			Else: els,
		}}, true
	}
	return []kernelbridge.IrStatement{{
		Kind: kernelbridge.IrStatementBranch,
		On:   on,
		Test: test,
		Then: then,
		Else: els,
	}}, true
}

// shortCircuitLeftSlot resolves a short circuit's LEFT operand to the
// slot the branch tests and the left arm reads — the two reads of one
// value the form needs.
//
// A tracked read answers its own slot: evaluating it moves nothing, so
// the second read finds what the first did.
//
// A CALL answers its hoisted temp. The hoist is sound at this position
// for the reason the whole form turns on: in `a ?? b`, `a` is the first
// thing the expression evaluates and it runs UNCONDITIONALLY — the short
// circuit decides only whether `b` runs. Emitting the call statement
// ahead of the branch therefore preserves source order exactly, and the
// hoist's own ordering gate (hoistingIsOrderSafe) still measures the
// call's write set against the rest of the statement, so a call that
// writes something the statement reads around it refuses here as it does
// anywhere.
//
// A call the hoist refuses — no compiled blob, no statement stream, an
// unsafe reordering — answers nothing, and the route keeps its decline.
func shortCircuitLeftSlot(context *LoweringContext, left *ast.Node) (int, bool) {
	head := Unwrapped(left)
	if on, tracked := IndexOf(context, head); tracked {
		return on, true
	}
	// `await f(…)` peels to the same call: the ret-as-inner convention
	// means the callee's ret slot already holds the settled value, which
	// is the very slot HoistCallEffect answers
	callHead := head
	if operand, isAwait := AwaitedOperandOf(head); isAwait {
		callHead = Unwrapped(operand)
	}
	// THE LEFT OPERAND MUST BE THE CALL WHOLE. `(a && f()) ?? b` reaches
	// here with a BinaryExpression left and is refused: the `&&` inside it
	// decides whether `f()` runs, so hoisting the call ahead of the branch
	// would run it on a path the source does not. What makes `f() ?? b`
	// admissible is precisely that nothing stands above the call — it is
	// the first thing the expression evaluates, on every path.
	if !ast.IsCallExpression(callHead) {
		return 0, false
	}
	effect, hoisted := HoistCallEffect(context, callHead)
	if !hoisted || effect.Kind != kernelbridge.LoopEffectVar {
		return 0, false
	}
	return effect.Index, true
}
