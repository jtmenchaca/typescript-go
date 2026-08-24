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
	// A BOOLEAN-VALUED LEFT takes the other route entirely, and is tried
	// first because it needs no slot at all — see
	// returnBooleanLeftShortCircuit for why the two-reads-of-one-slot
	// constraint below does not bind it.
	if boolLeft, ok := returnBooleanLeftShortCircuit(context, binary, sort, raise); ok {
		return boolLeft, true
	}
	// A NON-OPTIONAL RECORD PARAMETER'S OWN BARE NAME takes the same
	// no-slot route, for the same reason: the value's TRUTH is provable
	// from the annotation alone, so there is nothing to read twice.
	if truthyLeft, ok := returnTruthyRecordLeftShortCircuit(context, binary, sort, raise); ok {
		return truthyLeft, true
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
	//
	// IrTestDefined is the CORRECT (either-admission) test for `??` here:
	// CoalesceExpression's runtime semantics run the right operand
	// whenever the left is undefined OR null (sec-binary-logical-
	// operators, specifications/javascript/spec.html:21140-21143), so definedness —
	// not the flavored eqUndef split — is what picks the side.
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
	// the arm holding `a` writes the slot it just tested — the same
	// value verbatim, under the narrowing the test put on that side, so
	// it rides the whole-state copy rather than the numeric var read
	leftArm := []kernelbridge.IrStatement{
		{Kind: kernelbridge.IrStatementAssign, Target: context.Result.Ret, Effect: varStateEffect(on)},
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
	if hoisted && effect.Kind == kernelbridge.LoopEffectVar {
		return effect.Index, true
	}
	// no servable blob: the OPAQUE twin — sound at this position because
	// the left operand runs first and unconditionally
	// (HoistOpaqueCallTemp's own doc), so `return useAppSelector(sel) ??
	// fallback` branches on the hook tier's temp instead of declining
	// the whole return.
	return HoistOpaqueCallTemp(context, callHead)
}

// returnBooleanLeftShortCircuit lowers `return a && b` / `a || b` where
// the LEFT operand's value is a BOOLEAN — `content != null &&
// typeof content === 'function'` (Label.tsx:221), `stroke !== 'none' && b`,
// `!x || b`, and any `&&`/`||` nest of those.
//
// WHY THIS LEFT NEEDS NO SLOT, which is the whole point. The route above
// admits only a left whose evaluation lands in a slot readable twice,
// because there `a` is both the test AND one of the two returned values,
// and the wire has no test-and-reuse. A boolean-valued left has no such
// problem: the value it contributes is decided by WHICH ARM RAN, so the
// arm writes a CONSTANT and never reads the left back.
//
//	a && b  →  the falsy arm returns `a`, and a falsy boolean is exactly
//	           `false`; the truthy arm returns `b`.
//	a || b  →  the truthy arm returns `a`, and a truthy boolean is exactly
//	           `true`; the falsy arm returns `b`.
//
// `false` rides {0} and `true` rides {1} — the same encoding
// booleanPairEffect uses for every boolean-valued operator, so the
// constant here is exact, not a widening.
//
// `??` IS DELIBERATELY EXCLUDED. A boolean is never null or undefined,
// so `a ?? b` with a boolean-valued `a` always evaluates to `a` and the
// right operand is dead. That is a true and stronger claim than a branch
// — but it is a claim about an expression whose right side never runs,
// which the plain effect grammar's own join already covers soundly, and
// spelling it here would need a one-armed form this route does not build.
// It keeps the refusal, named.
//
// WHAT PICKS THE SIDE is LowerGuard on the left operand itself — the
// same composer the ternary arm above calls, and the reason this route
// reaches shapes the slot route cannot: LowerGuard reads comparisons
// (TestOf), typeof folds, Number.isNaN, `!` swaps, and `&&`/`||`
// nesting, none of which resolve to a single slot. It is handed the two
// prepared arms and returns the statements that run the right one.
//
// LowerGuard's own position rule is satisfied: a returned short
// circuit's LEFT operand runs on every path through the expression and
// runs FIRST, which is exactly the unconditional position
// LowerGuard's entry point documents.
//
// A left LowerGuard cannot read keeps the refusal — there is no untested
// fallback here, because without a test neither arm's constant is
// justified: an untested branch would claim `false` on a path the source
// may have returned `true` on. That is the one place this route must
// decline rather than weaken.
func returnBooleanLeftShortCircuit(
	context *LoweringContext,
	binary *ast.BinaryExpression,
	sort BindingKind,
	raise kernelbridge.IrStatement,
) ([]kernelbridge.IrStatement, bool) {
	kind := binary.OperatorToken.Kind
	if kind != ast.KindAmpersandAmpersandToken && kind != ast.KindBarBarToken {
		return nil, false
	}
	if !booleanValuedExpression(binary.Left) {
		return nil, false
	}
	// the arm that returns the LEFT operand's own value writes the
	// constant that value provably is on that side
	constantArm := func(w float64) []kernelbridge.IrStatement {
		return []kernelbridge.IrStatement{
			{Kind: kernelbridge.IrStatementAssign, Target: context.Result.Ret, Effect: constNumber(w)},
			raise,
		}
	}
	rightArm, rightOk := returnValueStatements(context, binary.Right, sort, raise)
	if !rightOk {
		return nil, false
	}
	// `a && b` runs `b` where the left HELD and returns `false` where it
	// did not; `a || b` returns `true` where the left held and runs `b`
	// where it did not
	thn, els := rightArm, constantArm(0)
	if kind == ast.KindBarBarToken {
		thn, els = constantArm(1), rightArm
	}
	return LowerGuard(context, binary.Left, thn, els)
}

// returnTruthyRecordLeftShortCircuit lowers `return entry && b` / `entry
// || b` where the LEFT operand is a NON-OPTIONAL RECORD PARAMETER'S OWN
// BARE NAME — `(entry && entry.value) || 0` (TASK 3(d)'s fixture) is this
// shape one level up: the outer `||`'s left is itself `entry && entry.value`,
// so this route resolves the inner `&&` and the outer `||` composes it
// through the ordinary recursion in returnBranchStatements/
// returnShortCircuitStatements.
//
// WHY THIS LEFT NEEDS NO SLOT, the same argument
// returnBooleanLeftShortCircuit makes for a boolean-valued left. The
// route above (shortCircuitLeftSlot) exists because an ordinary left
// operand is read TWICE — once as the test, once as one of the two
// returned values — and the wire has no test-and-reuse, so the left must
// resolve to a slot readable twice. A record parameter's bare name is
// TRACKED (declaredParamSort/recordParamMembersOf's own trust grade
// proves its truth without reading any slot at all — ToBoolean answers
// true for every Object, sec-toboolean, specifications/javascript/spec.html), so the
// arm that would return it is UNREACHABLE and never needs reading back:
//
//	entry && b  →  the record is always truthy, so the whole is always `b`
//	entry || b  →  the record is always truthy, so the whole is always
//	               the record itself — but that arm has no slot to read
//	               the record BACK from (recordParameterUseOf expands it
//	               away), so `||` stays refused here; only `&&` folds.
//
// `??` is excluded for the same reason `||` is: the LEFT is the value on
// the defined arm, which needs the same slot this route does not have.
func returnTruthyRecordLeftShortCircuit(
	context *LoweringContext,
	binary *ast.BinaryExpression,
	sort BindingKind,
	raise kernelbridge.IrStatement,
) ([]kernelbridge.IrStatement, bool) {
	if binary.OperatorToken.Kind != ast.KindAmpersandAmpersandToken {
		return nil, false
	}
	if truthyRecordParameterName(Unwrapped(binary.Left)) == "" {
		return nil, false
	}
	// the record is always truthy, so `a && b` is always `b` — no branch,
	// no test, the right operand's own reading stands for the whole
	return returnValueStatements(context, binary.Right, sort, raise)
}

// booleanValuedExpression answers whether evaluating this expression
// yields exactly `true` or exactly `false` — never one of its operands.
// That is what lets the caller above write a CONSTANT for the arm that
// returns the expression's own value.
//
// The three shapes, and why each is exact:
//
//   - a COMPARISON (and `instanceof`/`in`): every one of these produces a
//     boolean by definition, which is the same fact booleanBinaryTokens
//     (effect_expression.go) already states and reads as the {0,1} set.
//   - `!e`: logical NOT produces a boolean whatever its operand is
//     (sec-logical-not-operator, specifications/javascript/spec.html) — the same rule
//     testShaped's own `negated` flag rides.
//   - `a && b` / `a || b` where BOTH sides are themselves boolean-valued:
//     the value is one of the two operands, and both are booleans, so the
//     whole is one. A short circuit with a non-boolean side is NOT
//     boolean-valued (`a && b.name` evaluates to a string), which is why
//     this recurses into both sides rather than admitting the token.
//
// A bare identifier, a call, a member read, and a literal are all absent
// here on purpose: `x && b` returns `x` itself on the falsy side, whose
// value the caller cannot write as a constant. Those keep the slot route
// above, which reads the left back from its own slot.
func booleanValuedExpression(e *ast.Node) bool {
	head := Unwrapped(e)
	if ast.IsPrefixUnaryExpression(head) {
		return head.AsPrefixUnaryExpression().Operator == ast.KindExclamationToken
	}
	if !ast.IsBinaryExpression(head) {
		return false
	}
	bin := head.AsBinaryExpression()
	kind := bin.OperatorToken.Kind
	if kind == ast.KindAmpersandAmpersandToken || kind == ast.KindBarBarToken {
		return booleanValuedExpression(bin.Left) && booleanValuedExpression(bin.Right)
	}
	_, isBoolean := booleanBinaryTokens[kind]
	return isBoolean
}

// isShortCircuitToken is the same three-token gate logicalTokens states
// (effect_expression.go), asked here of a syntax node's operator rather
// than a map lookup, since this file works with the AST's own token kind
// before any effect exists to check a map against.
func isShortCircuitToken(kind ast.Kind) bool {
	return kind == ast.KindQuestionQuestionToken || kind == ast.KindAmpersandAmpersandToken || kind == ast.KindBarBarToken
}

// returnArithmeticOverShortCircuit lowers `return age + (extra ?? 0)` —
// an ARITHMETIC operator with exactly one short-circuit operand — as a
// BRANCH whose two arms each compute the WHOLE arithmetic expression,
// the short-circuit sub-expression narrowed to what that arm's own
// runtime proves it: the tracked slot itself on the arm where it is
// defined/truthy, the short circuit's own right operand on the arm
// where it is not.
//
// WHY THE PLAIN EFFECT GRAMMAR IS NOT ENOUGH. `RhsEffect`/`EffectOf`
// already lowers `age + (extra ?? 0)` as `add(var(age), join(var(extra),
// const(0)))` — sound, but LoopEffectJoin admits BOTH operands' sets at
// once (effect_expression.go's own doc on logicalTokens: "the join of
// both admits every run"), so the add's right side reads as possibly
// absent-flagged whenever `extra` might be, even on the runs where `??`
// itself guarantees a defined result. Composing the SAME add inside a
// branch, once per arm, lets each arm's `extra` reading be the exact
// narrowed value that arm's runtime actually has — the join happens
// AFTER the arithmetic, at the branch's own exit, not before it.
//
// THIS LOWERING IS EXACT ONLY UNDER A KERNEL WHOSE JOIN TREATS AN
// EMPTY-SET ARM AS IDENTITY. A prior attempt at this shape was removed
// because the THEN-current kernel had no bottom/unreachable enclosure:
// `oneOfEnc([])` fell through to `Enclosure.top` (unbounded), so the
// arm narrowed to the provably-unreachable branch (e.g. `extra` proved
// absent on the defined-tested arm of some OTHER read) contributed
// UNBOUNDED arithmetic to `KnownState.join`, widening the whole answer
// back to unknown. This restoration depends on the sibling kernel fix
// (`oneOfEnc [] = bottom`, arithmetic transfers absorbing bottom,
// `KnownState.join` treating a bottom/empty state as identity) — the
// KERNEL ARTIFACTS MUST BE REBUILT (`pnpm kernel` then `pnpm
// kernel:native`) before the judge reflects this lowering; against a
// stale dylib the empty arm still reads top and the join still widens,
// exactly the failure this lowering was pulled for.
//
// Gated on `sort == BindingKindNumber`: string concatenation over a
// short-circuit operand stays with the plain `RhsEffect`/`SequenceEffectOf`
// route above (lowering_to_kernel_ir_return.go), which already composes
// a string join correctly — this route only tightens the NUMERIC
// arithmetic reading, where the kernel's or-absent flag is the thing
// costing precision.
func returnArithmeticOverShortCircuit(
	context *LoweringContext,
	expression *ast.Node,
	sort BindingKind,
	raise kernelbridge.IrStatement,
) ([]kernelbridge.IrStatement, bool) {
	if context == nil || context.Result == nil || expression == nil || sort != BindingKindNumber {
		return nil, false
	}
	head := Unwrapped(expression)
	if !ast.IsBinaryExpression(head) {
		return nil, false
	}
	bin := head.AsBinaryExpression()
	if _, arithmetic := binOps[bin.OperatorToken.Kind]; !arithmetic {
		return nil, false
	}
	// EXACTLY ONE operand is a short circuit: two would need two
	// branches nested inside one arithmetic read, which this route does
	// not attempt, and neither would leave nothing for this lowering to
	// tighten over the plain effect grammar's own answer.
	leftHead := Unwrapped(bin.Left)
	rightHead := Unwrapped(bin.Right)
	leftIsShort := ast.IsBinaryExpression(leftHead) && isShortCircuitToken(leftHead.AsBinaryExpression().OperatorToken.Kind)
	rightIsShort := ast.IsBinaryExpression(rightHead) && isShortCircuitToken(rightHead.AsBinaryExpression().OperatorToken.Kind)
	if leftIsShort == rightIsShort {
		return nil, false
	}
	shortNode := rightHead
	if leftIsShort {
		shortNode = leftHead
	}
	shortBin := shortNode.AsBinaryExpression()
	kind := shortBin.OperatorToken.Kind
	on, tracked := shortCircuitLeftSlot(context, shortBin.Left)
	if !tracked {
		return nil, false
	}
	// the short circuit's OWN right operand, read as an ordinary
	// arithmetic effect — this is what the outer expression reads on the
	// arm where the left operand did not carry
	rightOperandEffect, rightOperandOk := RhsEffect(context, sort, shortBin.Right)
	if !rightOperandOk {
		return nil, false
	}
	// the two composed readings of the OUTER arithmetic, one per arm:
	// definedArm substitutes the tracked slot itself for the short
	// circuit's whole value (sound on the arm the test proved it
	// defined/truthy); absentArm substitutes the short circuit's own
	// right operand (sound on the arm the test proved it absent/falsy)
	definedEffect, definedOk := composedArithmeticEffect(context, sort, bin, shortNode, varEffect(on))
	if !definedOk {
		return nil, false
	}
	absentEffect, absentOk := composedArithmeticEffect(context, sort, bin, shortNode, rightOperandEffect)
	if !absentOk {
		return nil, false
	}
	assign := func(effect kernelbridge.LoopEffect) []kernelbridge.IrStatement {
		return []kernelbridge.IrStatement{
			{Kind: kernelbridge.IrStatementAssign, Target: context.Result.Ret, Effect: effect},
			raise,
		}
	}
	// which test picks the side, and which arm is "defined" versus
	// "absent" — the SAME rule returnShortCircuitStatements states: `??`
	// asks definedness; `&&`/`||` ask truthiness under the slot's sort,
	// falling to the untested branch where neither sort applies.
	//
	// IrTestDefined is correct (either-admission) for `??`: the right
	// operand runs whenever the left is undefined OR null (CoalesceExpression,
	// sec-binary-logical-operators, specifications/javascript/spec.html:21140-21143).
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
	// `a && b`: the defined/truthy arm evaluates to `b`, so the outer
	// arithmetic on THAT arm reads the short circuit's right operand,
	// and the other arm reads the tracked slot — the reverse of `??`
	// and `||`, exactly as returnShortCircuitStatements' own then/els
	// swap states.
	then, els := assign(definedEffect), assign(absentEffect)
	if kind == ast.KindAmpersandAmpersandToken {
		then, els = assign(absentEffect), assign(definedEffect)
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

// composedArithmeticEffect reads the OUTER arithmetic binary `outer` as
// an effect, except at `target` — the short circuit sub-node exactly —
// where it splices in `substitute` instead of recursing further. This
// is the one-node substitution the branch's two arms need: the outer
// shape lowers through the ordinary arithmetic recursion everywhere
// else, and only the short circuit's own contribution differs per arm.
//
// Recurses only through further arithmetic binaries and paren/cast
// wrappers — the same shapes `binOps` and `LowerEffectExpression`'s own
// unwrap arm admit — so a target buried under, say, a nested ternary
// never reaches here (returnBranchStatements' ternary arm is a
// different route entirely). Every other leaf reads through the
// ordinary `RhsEffect`, so a name, a literal, a call hoist, or any
// other operand this file already knows how to read composes exactly
// as it would outside a branch.
func composedArithmeticEffect(
	context *LoweringContext,
	sort BindingKind,
	outer *ast.BinaryExpression,
	target *ast.Node,
	substitute kernelbridge.LoopEffect,
) (kernelbridge.LoopEffect, bool) {
	var walkNode func(node *ast.Node) (kernelbridge.LoopEffect, bool)
	walkNode = func(node *ast.Node) (kernelbridge.LoopEffect, bool) {
		head := Unwrapped(node)
		if head == target {
			return substitute, true
		}
		if ast.IsBinaryExpression(head) {
			bin := head.AsBinaryExpression()
			if op, arithmetic := binOps[bin.OperatorToken.Kind]; arithmetic {
				a, aOk := walkNode(bin.Left)
				b, bOk := walkNode(bin.Right)
				if !aOk || !bOk {
					return kernelbridge.LoopEffect{}, false
				}
				return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectBinary, Op: op, A: &a, B: &b}, true
			}
		}
		return RhsEffect(context, sort, node)
	}
	left, leftOk := walkNode(outer.Left)
	right, rightOk := walkNode(outer.Right)
	if !leftOk || !rightOk {
		return kernelbridge.LoopEffect{}, false
	}
	op := binOps[outer.OperatorToken.Kind]
	return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectBinary, Op: op, A: &left, B: &right}, true
}
