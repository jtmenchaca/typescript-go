// split from lowering_to_kernel_ir.go — one arm of a branch-shaped return

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// returnValueStatements lowers ONE branch arm of a returned ternary or
// short circuit: the statements that write `#ret` from the arm's
// expression and raise the done flag.
//
// This is the return route's own value vocabulary, reached from inside a
// branch arm rather than from the statement stream — a served call takes
// the call route, a `new` takes the constructor route, an inert value
// reads unknown, and an arm with no reading at all takes unknown plus
// the mention havoc that covers whatever its evaluation could have
// moved. The raise is appended by every path, so the flag is up on
// exactly the arms that run to a return.
//
// NO HOISTING INSIDE AN ARM. context.CanHoist is lowered for the arm's
// readers and restored after: a hoisted call statement is emitted BEFORE
// the statement holding the expression, which for an arm means before
// the BRANCH — running unconditionally what the arm runs only on its own
// side. Any hoists an arm's readers did produce are dropped with it.
func returnValueStatements(
	context *LoweringContext,
	arm *ast.Node,
	sort BindingKind,
	raise kernelbridge.IrStatement,
) ([]kernelbridge.IrStatement, bool) {
	if context == nil || context.Result == nil || arm == nil {
		return nil, false
	}
	priorCanHoist := context.CanHoist
	context.CanHoist = false
	mark := HoistedMark(context)
	defer func() {
		context.CanHoist = priorCanHoist
		DropHoistedFrom(context, mark)
	}()
	assign := func(effect kernelbridge.LoopEffect) []kernelbridge.IrStatement {
		return []kernelbridge.IrStatement{
			{Kind: kernelbridge.IrStatementAssign, Target: context.Result.Ret, Effect: effect},
			raise,
		}
	}
	// the arm's value where the effect grammar spells it — `0`, `'unknown'`,
	// a tracked name, an arithmetic or concatenation of them
	if effect, ok := RhsEffect(context, sort, arm); ok {
		return assign(effect), true
	}
	head := Unwrapped(arm)
	// a NESTED ternary or short circuit — nest's `result instanceof Promise
	// ? … : result instanceof NestApplication ? proxy : result` — is the
	// same shape one level down, and lowers as the branch inside this arm
	if branched, ok := returnBranchStatements(context, arm, sort, raise); ok {
		return branched, true
	}
	// `xs.reduce(cb, seed)` and its siblings: the callback converts to its
	// own summary and the method's result lands in the ret slot
	if viaCallback, ok := SummaryCallbackReturnOf(context, head); ok {
		return append(viaCallback, raise), true
	}
	// `f(…)` / `await f(…)`: the callee inlines and its result slot is the
	// arm's value. The await peels off first — the ret-as-inner convention
	// means the callee's ret slot already holds the settled value.
	callHead := head
	if operand, isAwait := AwaitedOperandOf(head); isAwait {
		callHead = Unwrapped(operand)
	}
	if ast.IsCallExpression(callHead) {
		if inlined, ok := InlineCall(context, callHead); ok {
			out := append([]kernelbridge.IrStatement{}, inlined.Stmts...)
			out = append(out, kernelbridge.IrStatement{
				Kind:   kernelbridge.IrStatementAssign,
				Target: context.Result.Ret,
				Effect: varEffect(inlined.RetIndex),
			})
			return append(out, raise), true
		}
	}
	// `new C(…)`: the constructor's compiled summary runs and the ret slot
	// takes unknown, a constructed instance having no scalar spelling
	if ast.IsNewExpression(callHead) {
		if constructed, ok := SummaryCallOrHavoc(context, callHead, context.Result.Ret); ok {
			return append(constructed, raise), true
		}
	}
	// AN INERT ARM: a function literal (creating one runs nothing) or any
	// expression that moves nothing. Unknown is what the ret slot can say
	// about a value with no scalar spelling, and nothing moved.
	//
	// A function literal is inert to EVALUATE and not inert to HAND OVER:
	// the caller receives it and may call it at a time no statement here
	// places, and every tracked name it writes is a name nothing after
	// this may believe. So the arm asks the census gate and keeps its
	// decline where the answer is yes — the havoc route below then covers
	// exactly those names. (writeAndCallFree descends THROUGH a function
	// literal, so the second disjunct already refuses a writing closure
	// nested in a larger expression; the gate is what the first disjunct
	// needs, which admits the literal whole.)
	if ast.IsFunctionLike(head) || writeAndCallFree(head) {
		if !ClosureEscapesTrackedWrite(context, head) {
			return assign(unknownEffect), true
		}
	}
	// AN ARM WITH NO READING. Its value is unknown, and whatever its
	// evaluation could have moved is havocked at the arm's own position —
	// the havoc floor's rule, applied inside the branch rather than in
	// place of it.
	//
	// THE ENUMERABILITY GATE IS ASKED FIRST, the same way the floor's two
	// other entries ask it (OpaqueHavocStatements and OpaqueCallHavoc,
	// ir_opaque_havoc.go). The enumerator itself never says no — it walks
	// the syntax and answers the slots it found — so the impossibilities
	// are havocEnumerable's to name, and an arm that skipped the gate
	// would havoc a mention set that does not bound what the arm moved.
	// Most of those impossibilities cannot appear in an ARM at all, an
	// arm being an expression: a `return`, a `throw`, a `with`, a bare
	// break or continue are statements, and the only statement positions
	// inside an expression sit in a function body, which the gate's scan
	// stops at because that body's transfers are its own. The one that
	// does reach here is a bare `eval(…)` — `c ? eval(s) : 1` — whose
	// code runs in THIS scope and may write any binding in it, so no
	// mention set bounds it. That one keeps the decline.
	if !havocEnumerable(arm) {
		return nil, false
	}
	slots, enumerable := havocSlotsOfStatement(context, arm)
	if !enumerable {
		return nil, false
	}
	out := havocAssignments(slots)
	return append(out, assign(unknownEffect)...), true
}
