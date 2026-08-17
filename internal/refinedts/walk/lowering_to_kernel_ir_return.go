// split from lowering_to_kernel_ir.go — the return statement's route

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// lowerReturnStatement is the walk's `return` route, exactly as it stood
// inside lowerStatementList. The route ENDS the statement list on every
// path — a return either lowers and the block is over, or it declines the
// body — so its answer is the list's answer and the caller returns it.
//
// `mark` is the statement's own hoist mark, which is all the two
// bookkeeping closures need: flush(out) empties this statement's
// accumulated hoists ahead of the route's own statements, dropHoists()
// truncates back to the mark where a reading declined.
//
// `return e`: the result slot takes e, the done flag raises, and
// the rest of this block never runs — dead statements simply do
// not lower. A bare `return` raises the flag alone; the result
// slot keeps its absent entry state, which IS the undefined
// return.
func lowerReturnStatement(
	context *LoweringContext,
	s *ast.Node,
	out []kernelbridge.IrStatement,
	mark int,
) ([]kernelbridge.IrStatement, bool) {
	flush := func(out []kernelbridge.IrStatement) []kernelbridge.IrStatement {
		return append(out, TakeHoisted(context)...)
	}
	dropHoists := func() { DropHoistedFrom(context, mark) }
	if context.Result == nil {
		// no result slot pair: this lowering is not a function body,
		// so there is nothing for a return to write or raise
		NoteDeclinedConstruct(context, "return with no result slot")
		return nil, false
	}
	raise := kernelbridge.IrStatement{
		Kind:   kernelbridge.IrStatementAssign,
		Target: context.Result.Done,
		Effect: kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectConst, Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{1}))},
	}
	rs := s.AsReturnStatement()
	if rs.Expression != nil {
		// the result slot's OWN sort decides the reading, so a
		// string-sorted body's `return a + b` concatenates where a
		// number-sorted one's adds. A slot the caller left unsorted
		// falls back to the returned expression's own spelling.
		sort := BindingKindNumber
		if context.Result.Ret < len(context.Sorts) &&
			context.Sorts[context.Result.Ret] != BindingKindUnknown {
			sort = context.Sorts[context.Result.Ret]
		} else if ast.IsStringLiteral(Unwrapped(rs.Expression)) {
			sort = BindingKindString
		}
		// `return age + (extra ?? 0)`: an ARITHMETIC operator over a
		// short-circuit operand, tried as a BRANCH before the plain effect
		// grammar below — returnArithmeticOverShortCircuit composes the
		// outer arithmetic inside each arm, narrowing the short circuit's
		// own left slot to what that arm's runtime actually proves, rather
		// than joining both possible operand sets before the arithmetic
		// runs (which is what the plain `RhsEffect` route below does, and
		// is why this route is tried first — sound, but wider than the
		// branch's answer wherever `??`/`&&`/`||` itself rules an operand
		// out on one side).
		//
		// EXACT ONLY UNDER A KERNEL WHOSE JOIN TREATS AN EMPTY-SET ARM AS
		// IDENTITY. This lowering was pulled once already: the
		// then-current kernel's enclosure world had no bottom — an empty
		// refined set (`oneOfEnc([])`) fell through to `Enclosure.top`
		// (unbounded), so the arm narrowed to a provably-unreachable state
		// contributed UNBOUNDED arithmetic to `KnownState.join`, widening
		// the whole answer back to unknown. It is restored now on the
		// contract that `oneOfEnc [] = bottom`, that arithmetic transfers
		// absorb bottom, and that `KnownState.join` treats a bottom/empty
		// state as identity (contributing neither enclosure content nor
		// flags) — see returnArithmeticOverShortCircuit's own doc
		// (lowering_to_kernel_ir_return_branch.go) for the full argument.
		// THE KERNEL ARTIFACTS MUST BE REBUILT (`pnpm kernel` then `pnpm
		// kernel:native`) before the judge reflects this lowering —
		// against a stale dylib the empty arm still reads top and the
		// join still widens, exactly the failure this route was pulled
		// for the first time.
		dropHoists()
		if branched, ok := returnArithmeticOverShortCircuit(context, rs.Expression, sort, raise); ok {
			out = flush(out)
			out = append(out, branched...)
			return out, true
		}
		dropHoists()
		// a SHORT-CIRCUIT- or TERNARY-shaped return takes the BRANCH
		// route ahead of the plain effect grammar: the effect grammar's
		// logical arm claims every `a && b` with either the operands'
		// whole JOIN (sound, wider than the branch's per-arm narrowing)
		// or a bare `unknown` — which lowered COMPLETE with no havoc
		// name, so the serving rule answered that unknown as the call's
		// own answer instead of declining to the walk route (Area.tsx's
		// `return stroke && stroke !== 'none' ? stroke : fill`). The
		// branch route narrows each arm under its own side and declines
		// cleanly, so the effect grammar below stays the fallback.
		if head := Unwrapped(rs.Expression); head != nil &&
			(ast.IsConditionalExpression(head) ||
				(ast.IsBinaryExpression(head) && isShortCircuitToken(head.AsBinaryExpression().OperatorToken.Kind))) {
			if branched, ok := returnBranchStatements(context, rs.Expression, sort, raise); ok {
				out = flush(out)
				out = append(out, branched...)
				return out, true
			}
			dropHoists()
		}
		effect, ok := RhsEffect(context, sort, rs.Expression)
		if ok {
			// `return this.a(this.b(x)) + 1`: the hoisted calls go out
			// first, then the result write reads their temps
			out = flush(out)
			out = append(out, kernelbridge.IrStatement{Kind: kernelbridge.IrStatementAssign, Target: context.Result.Ret, Effect: asVarStateEffect(effect)})
			out = append(out, raise)
			return out, true
		}
		dropHoists()
		// `return this.<field>.has(k)` / `return S.has(k)`: a MODEL CALL
		// on a once-assigned Map/Set field or module const — no
		// FunctionContract resolves for either, so InlineCall's own
		// route below would never reach them anyway; tried here, ahead
		// of it, the same way SummaryCallOrHavocNamed tries these two
		// recognizers ahead of its blob tier for an ordinary statement.
		// Both recognizers answer their own IrStatement directly against
		// a caller-supplied TARGET slot — context.Result.Ret is exactly
		// that target for a return in value position, so this return's
		// value writes straight into the same statement thisFieldMapCallOf/
		// moduleSetCallOf already model would have written for an
		// intermediate `const ok = …; return ok;` — the direct-return
		// spelling and the assign-then-return spelling now serve
		// identically. Neither recognizer's kind == "none" branch (Map's
		// own `.set`/`.clear`) can reach target >= 0, since neither
		// yields a readable result to return.
		returnHead := Unwrapped(rs.Expression)
		if statement, ok := thisFieldMapCallStatement(context, returnHead, context.Result.Ret); ok {
			out = flush(out)
			out = append(out, statement, raise)
			return out, true
		}
		if statement, ok := moduleSetCallStatement(context, returnHead, context.Result.Ret); ok {
			out = flush(out)
			out = append(out, statement, raise)
			return out, true
		}
		// `return await f(…)`: the ret-as-inner convention means the
		// callee's ret slot already holds the SETTLED value, so this
		// is exactly the `return f(…)` call lowering with the await
		// peeled off its operand. From an ASYNC body a bare `return
		// f(…)` with a resolvable callee settles the same way —
		// returning a promise from async adopts it — and `return
		// await s` on a tracked scalar is the identity read. Tried
		// ahead of the inlining route, which lowers the callee's body
		// into fresh slots instead.
		if awaited, awaitedOk := AwaitReturnStatements(context, rs.Expression, raise); awaitedOk {
			out = flush(out)
			out = append(out, awaited...)
			return out, true
		}
		dropHoists()
		// `return xs.reduce(cb, seed)` / `.find(cb)` / `.flatMap(cb)`
		// over a flattened array: the callback converts to its own
		// summary and the method's result lands straight in this
		// body's result slot. Ahead of the inlining route, whose
		// callee resolution has no reading for a collection method.
		head := Unwrapped(rs.Expression)
		if viaCallback, ok := SummaryCallbackReturnOf(context, head); ok {
			out = flush(out)
			out = append(out, viaCallback...)
			out = append(out, raise)
			return out, true
		}
		dropHoists()
		// `return f(…)`: the callee inlines and its result slot is
		// this return's value — absent where a path fell off, which
		// the copy carries
		if ast.IsCallExpression(head) {
			if inlined, ok := InlineCall(context, head); ok {
				out = flush(out)
				out = append(out, inlined.Stmts...)
				out = append(out, kernelbridge.IrStatement{
					Kind:   kernelbridge.IrStatementAssign,
					Target: context.Result.Ret,
					Effect: varStateEffect(inlined.RetIndex),
				})
				out = append(out, raise)
				return out, true
			}
			dropHoists()
		}
		// `return new C(…)`: the same door `const x = new C(…)` goes
		// through — SummaryCallOrHavoc's blob tier, which runs the
		// constructor's compiled summary and then writes the target
		// unknown, since a constructed instance has no scalar
		// spelling. The ret slot IS the target here, so the
		// constructor's effects on this body's tracked state land
		// before the flag rises, and the returned value reads as
		// unknown rather than as the absent an unwritten ret would
		// claim. A constructor with no blob declines back here and
		// the opaque return below still stands.
		if ast.IsNewExpression(head) {
			if constructed, ok := SummaryCallOrHavoc(context, head, context.Result.Ret); ok {
				out = flush(out)
				out = append(out, constructed...)
				out = append(out, raise)
				return out, true
			}
			dropHoists()
		}
		// a boolean-shaped return (`return typeof x === "number" &&
		// f(x)`): its truth writes {1}, its falsity {0} — the guard
		// machinery decides which, and only truthiness of any inlined
		// call is read, never its value
		if TestShaped(context, rs.Expression) {
			thn := []kernelbridge.IrStatement{
				{
					Kind:   kernelbridge.IrStatementAssign,
					Target: context.Result.Ret,
					Effect: kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectConst, Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{1}))},
				},
				raise,
			}
			els := []kernelbridge.IrStatement{
				{
					Kind:   kernelbridge.IrStatementAssign,
					Target: context.Result.Ret,
					Effect: kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectConst, Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{0}))},
				},
				raise,
			}
			if guarded, ok := LowerGuard(context, rs.Expression, thn, els); ok {
				out = flush(out)
				out = append(out, guarded...)
				return out, true
			}
			// the guard composer may HOIST before it refuses — a `??`
			// whose call left took its temp and whose right side then
			// read nothing — so the refusal truncates back, exactly as
			// every other route's decline does
			dropHoists()
		}
		// THE BRANCH-SHAPED RETURN: `return c ? a : b`,
		// `return a ?? b`, `return a && b`, `return a || b`. The
		// value is an OPERAND, not a boolean, so the guard route
		// above cannot spell it and the effect grammar cannot
		// either — an arm that calls or constructs needs a
		// STATEMENT, which the effect language has no room for. As
		// a branch it has room: each arm sinks `#ret := <arm>`
		// through this same return machinery, and the two arms
		// become the branch's then and else (returnBranchStatements
		// below holds the whole argument).
		if branched, ok := returnBranchStatements(context, rs.Expression, sort, raise); ok {
			out = flush(out)
			out = append(out, branched...)
			return out, true
		}
		dropHoists()
		// THE MEMBER-CARRYING RETURN: `return { type, dynamicMetadata }`
		// / `return [a, b]`, where the layout allocated one slot per
		// member (returnedLiteralShape). Each member's own effect goes
		// into its own slot and #ret takes unknown — the object itself
		// still has no scalar spelling, and the members are what the
		// caller reads back. Ahead of the inert return, which would
		// otherwise write the whole literal off as one unknown.
		if members, ok := returnMemberStatements(context, rs.Expression, raise); ok {
			out = flush(out)
			out = append(out, members...)
			return out, true
		}
		dropHoists()
		// THE INERT RETURN: a returned FUNCTION LITERAL (creating one
		// runs nothing, whatever its body holds — the census rules its
		// captures), and any other returned expression that MOVES
		// NOTHING (write- and call-free) AND READS NO FIELD OR ELEMENT
		// — `return this`, `return { }`. The value has no scalar
		// spelling, so unknown IS what the ret slot can say, and
		// evaluating the expression changed no state — the statement is
		// READ, not floored, and the body stays COMPLETE.
		//
		// A property/element read past this gate (`return this.#age`,
		// `return host && host.instance`) is NOT inert in the sense that
		// matters here: it is a real value this walk simply could not
		// spell a slot for (a PRIVATE name, an unresolvable receiver),
		// exactly the gap the OPAQUE RETURN below exists to flag. Before
		// this check, such a read took this same unknown-ret write but
		// skipped NoteFirstHavoc — so the body lowered "complete" with a
		// lost field read inside it, and applySummary's serving rule
		// ("only a COMPLETE body serves") served that unknown as the
		// call's own answer instead of declining to the walk route,
		// which reads the field correctly (a class field invariant, an
		// accessor's own backing field). Falling through to the opaque
		// return below keeps the unknown ret write identical and adds
		// exactly the one thing that was missing: the havoc note.
		if ast.IsFunctionLike(head) || (writeAndCallFree(head) && !containsPropertyOrElementRead(head)) {
			out = flush(out)
			out = append(out, kernelbridge.IrStatement{
				Kind:   kernelbridge.IrStatementAssign,
				Target: context.Result.Ret,
				Effect: unknownEffect,
			}, raise)
			return out, true
		}
		// THE OPAQUE RETURN. Every reading of the returned VALUE
		// declined — the effect grammar, the await forms, the inlining
		// route, the guard shape. What did NOT decline is the control
		// flow: this statement returns, here, unconditionally, and the
		// block ends. So the return lowers with its control shape exact
		// and its value unknown:
		//
		//	<the hoists this statement's readers produced>
		//	#ret  := unknown
		//	#done := {1}
		//
		// — the same two statements the readable return emits, with the
		// value part standing in for the reading that was not had.
		//
		// UNKNOWN, never absent: absent is the claim "this call returned
		// undefined", which is a claim, and a wrong one wherever the
		// expression had a value. Unknown claims nothing about the
		// value, which is exactly what is known here.
		//
		// The raise is what makes this sound rather than merely weak.
		// Dropping it — the havoc floor's option — would walk every
		// later statement as though this return had not happened, and a
		// later `return` would then overwrite the result slot: a WRONG
		// answer about the returned value. Raising it here is right for
		// the same reason it is right for a readable return: control
		// leaves the block at this statement on every path that reaches
		// it, so the flag is up on exactly the paths it should be, and
		// the continuation gating that the if and switch routes build
		// around a returning arm reads it unchanged.
		out = flush(out)
		out = append(out, kernelbridge.IrStatement{
			Kind:   kernelbridge.IrStatementAssign,
			Target: context.Result.Ret,
			Effect: unknownEffect,
		})
		out = append(out, raise)
		NoteFirstHavoc(context, OpaqueReturnName(s))
		return out, true
	}
	out = append(out, raise)
	return out, true
}
