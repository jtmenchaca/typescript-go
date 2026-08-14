// from control_flow/lowering_to_kernel_ir.ts
//
// Lowering: TypeScript statements → the kernel's flow IR. This is
// the adapter's half of the engine division — recognize the syntax,
// lower it, and let the kernel walk the whole body. The recognized
// subset is scalar and STRING straight-line code (arithmetic, string
// concatenation and templates, `null`/`undefined` writes), if/else on
// one binding or two, and while / for / do-while loops with a
// comparison head; everything else declines (nil), never guesses.
//
// `do body while (cond)` is composed from the two proved halves: the
// body's statements once, then the ordinary while loop.
//
// Tracked slots: tracked_bindings.go. Expression effects:
// effect_expression.go (shared with loop_effect.ts, not yet ported
// — see its own file). Slot context: ir_lowering_context.go.
// Assignments: ir_assignment.go. Guards: ir_guard.go. Loops:
// ir_loop.go. Call inlining: ir_inline_call.go. Summary call
// statements and the recursion havoc floor: ir_summary_call.go.
// Await, promise-held locals, and Promise.all: ir_await.go.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// LowerStatements is lowerStatements in the TS source: statements
// lowered to the IR, or (nil, false) where any one declines.
//
// A DECLINE also leaves a name behind — the construct it refused, in
// the source's own syntax — which the body-level owner reads with
// DeclinedConstructOf. The bookkeeping is here rather than inside the
// walk because only the outermost run knows the body's fate: an arm
// that declined and was stood in for by the havoc floor is not the
// body's decline, so a run that SUCCEEDS drops the name on the way out.
func LowerStatements(context *LoweringContext, statements []*ast.Node) ([]kernelbridge.IrStatement, bool) {
	EnterLoweringRun(context)
	lowered, ok := lowerStatementList(context, statements)
	// only the outermost leave consults the flag (LeaveLoweringRun reads
	// it at depth zero alone), so a nested arm's answer never clears a
	// name the body still owes
	LeaveLoweringRun(context, ok)
	return lowered, ok
}

// lowerStatementList is the walk itself — every route, in order, with
// the havoc floor last.
func lowerStatementList(context *LoweringContext, statements []*ast.Node) ([]kernelbridge.IrStatement, bool) {
	var out []kernelbridge.IrStatement
	// havocFloor is the LAST resort every composite route below falls
	// through to when its own reading declines: the statement's writable
	// slots take `unknown`, every other slot keeps its knowledge, and the
	// body keeps its route. It answers false only where the slot set is
	// genuinely unenumerable, and THAT is what still declines the body.
	//
	// A route that declines reaches here rather than returning, so "the
	// havoc floor is after every route" is true of the composite
	// statements too — an `if` whose arms did not lower, a `while` whose
	// head did not, a `for` with no condition each havoc rather than
	// costing the whole body its route.
	havocFloor := func(s *ast.Node) ([]kernelbridge.IrStatement, bool) {
		havoc, ok := OpaqueHavocStatements(context, s)
		if !ok {
			// the floor refused: name the construct it refused ON, in the
			// statement's own syntax — "throw inside try", "with statement",
			// "labeled break crossing out". The histogram is the work queue,
			// so its rows have to name something a reader can act on.
			NoteDeclinedConstruct(context, declinedFloorConstruct(s))
		}
		return havoc, ok
	}
	// THE HOIST STREAM. A call inside an EXPRESSION lowers to a temp-slot
	// call statement that must be emitted BEFORE the statement holding the
	// expression (ir_call_hoist.go). This is the statement stream it lands
	// in: the readers append to context.Hoisted, and every route below
	// flushes those ahead of its own statements.
	//
	// The two bookkeeping rules, applied uniformly below:
	//
	//	flush(out) — before appending a route's own statements, the hoists
	//	  this statement accumulated go out first, in order, and the
	//	  accumulation empties. Order is what makes the lowering right: the
	//	  temp must be written before the statement that reads it.
	//	dropHoists(mark) — a route whose reading DECLINED truncates the
	//	  accumulation back to where this statement started, so a declined
	//	  statement leaves no call statement behind for the havoc floor to
	//	  sit after. (Why that is cleanliness rather than soundness:
	//	  DropHoistedFrom's own comment.)
	//
	// The flag and the statement pointer are set for the whole loop and
	// RESTORED at the end: this same context is handed to FoldBody by the
	// loop routes, whose effect language has no statement stream, and a
	// hoist there would append a statement nothing emits.
	priorCanHoist, priorStatement := context.CanHoist, context.HoistStatement
	priorHoisted, priorTemps := context.Hoisted, context.HoistedTemp
	context.Hoisted, context.HoistedTemp = nil, nil
	defer func() {
		context.CanHoist, context.HoistStatement = priorCanHoist, priorStatement
		context.Hoisted, context.HoistedTemp = priorHoisted, priorTemps
	}()
	flush := func(out []kernelbridge.IrStatement) []kernelbridge.IrStatement {
		return append(out, TakeHoisted(context)...)
	}
	for index := 0; index < len(statements); index++ {
		s := statements[index]
		// the readers may hoist for THIS statement, and the ordering gate
		// measures a candidate against THIS statement's other slot mentions
		context.CanHoist = true
		context.HoistStatement = s
		// the per-node memo is THIS statement's: the same call node reached
		// again by a later statement's readers is a different run of the
		// site and gets its own temp
		context.HoistedTemp = nil
		mark := HoistedMark(context)
		dropHoists := func() { DropHoistedFrom(context, mark) }
		// `return e`: the result slot takes e, the done flag raises, and
		// the rest of this block never runs — dead statements simply do
		// not lower. A bare `return` raises the flag alone; the result
		// slot keeps its absent entry state, which IS the undefined
		// return.
		if ast.IsReturnStatement(s) {
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
				effect, ok := RhsEffect(context, sort, rs.Expression)
				if ok {
					// `return this.a(this.b(x)) + 1`: the hoisted calls go out
					// first, then the result write reads their temps
					out = flush(out)
					out = append(out, kernelbridge.IrStatement{Kind: kernelbridge.IrStatementAssign, Target: context.Result.Ret, Effect: effect})
					out = append(out, raise)
					return out, true
				}
				dropHoists()
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
				// `return f(…)`: the callee inlines and its result slot is
				// this return's value — absent where a path fell off, which
				// the copy carries
				head := Unwrapped(rs.Expression)
				if ast.IsCallExpression(head) {
					if inlined, ok := InlineCall(context, head); ok {
						out = flush(out)
						out = append(out, inlined.Stmts...)
						out = append(out, kernelbridge.IrStatement{
							Kind:   kernelbridge.IrStatementAssign,
							Target: context.Result.Ret,
							Effect: kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectVar, Index: inlined.RetIndex},
						})
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
		// THE ESCAPING THROW. `throw e` whose parent chain up to this
		// body's root passes through no `try` cannot reach a catch of this
		// body — it leaves the body outright. A run that threw returns
		// NOTHING, so no claim about the returned outcome can be wrong
		// about it, and the shape that says so is a return of nothing:
		//
		//	#ret  := absent
		//	#done := {1}
		//
		// Absent rather than unknown, and that direction is the honest
		// one: the run produced no returned value at all, and absent is
		// the weakest thing the result slot can hold that a later join
		// will not mistake for a value. The done flag then reads exactly
		// as it does for a `return` — the block ends here, later
		// statements are dead, and the apply route's allReturned reading
		// sees a path that left.
		//
		// A throw INSIDE a try keeps the decline (ir_opaque_havoc.go's
		// throwCarryingStatement holds the reasoning: raising the flag
		// would make the catch's own writes invisible to the walk, which
		// is a wrong claim, not a weak one). The report names it "throw
		// inside try" so the histogram row points at the construct.
		if ast.IsThrowStatement(s) {
			if context.Result == nil {
				NoteDeclinedConstruct(context, "throw with no result slot")
				dropHoists()
				return nil, false
			}
			if ThrowReachesATry(s) {
				NoteDeclinedConstruct(context, "throw inside try")
				dropHoists()
				return nil, false
			}
			out = append(out, kernelbridge.IrStatement{
				Kind:   kernelbridge.IrStatementAssign,
				Target: context.Result.Ret,
				Effect: kernelbridge.AbsentConst(),
			})
			out = append(out, kernelbridge.IrStatement{
				Kind:   kernelbridge.IrStatementAssign,
				Target: context.Result.Done,
				Effect: kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectConst, Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{1}))},
			})
			NoteFirstHavoc(context, "throw")
			return out, true
		}
		// `const p = { lo: 0, hi: n }` — a record local flattened into one
		// slot per LEAF lowers as N ordinary assignments at the
		// declaration's position, in literal order. Tried ahead of the
		// single-name reader, which has no slot for `p` itself.
		if assignments, ok := ObjectDeclarationAssignmentsOf(context, s); ok {
			out = flush(out)
			out = append(out, assignsOf(assignments)...)
			continue
		}
		dropHoists()
		// `const a = [1, 2, 3]` — an array local flattened into its two
		// slots: the count and the join of the elements.
		if assignments, ok := ArrayDeclarationAssignmentsOf(context, s); ok {
			out = flush(out)
			out = append(out, assignsOf(assignments)...)
			continue
		}
		dropHoists()
		// `const m = new Map(…)` / `new Set(…)` — a collection flattened
		// into its size, values, and (Map) keys slots.
		if assignments, ok := MapDeclarationAssignmentsOf(context, s); ok {
			out = flush(out)
			out = append(out, assignsOf(assignments)...)
			continue
		}
		dropHoists()
		// `const { x, y } = p` — a flattened record read leaf by leaf into
		// the destructured names.
		if assignments, ok := DestructuringAssignmentsOf(context, s); ok {
			out = flush(out)
			out = append(out, assignsOf(assignments)...)
			continue
		}
		dropHoists()
		// `p = q` / `p = { … }` — a whole record written leaf for leaf.
		// Ahead of the single-name reader, which has no slot for `p`.
		if assignments, ok := RecordAssignmentOf(context, s); ok {
			out = flush(out)
			out = append(out, assignsOf(assignments)...)
			continue
		}
		dropHoists()
		// `a.push(v)` — the length steps, the element slot joins.
		if assignments, ok := ArrayPushAssignmentsOf(context, s); ok {
			out = flush(out)
			out = append(out, assignsOf(assignments)...)
			continue
		}
		dropHoists()
		// `a[i] = v` — the element slot joins; the length is untouched.
		if assignments, ok := ArrayIndexWriteOf(context, s); ok {
			out = flush(out)
			out = append(out, assignsOf(assignments)...)
			continue
		}
		dropHoists()
		// `m.set(k, v)` / `s.add(v)` — the size may step, the keys and
		// values slots join.
		if assignments, ok := MapSetAssignmentsOf(context, s); ok {
			out = flush(out)
			out = append(out, assignsOf(assignments)...)
			continue
		}
		dropHoists()
		// `m.delete(k)` — the size may shrink (never below zero); the
		// keys and values slots keep their joins.
		if assignments, ok := MapDeleteAssignmentsOf(context, s); ok {
			out = flush(out)
			out = append(out, assignsOf(assignments)...)
			continue
		}
		dropHoists()
		// `let a = 1, b = 2` — several ordinary declarators in one
		// statement, each lowering by the single declarator's own rule.
		if assignments, ok := MultiDeclarationAssignmentsOf(context, s); ok {
			out = flush(out)
			out = append(out, assignsOf(assignments)...)
			continue
		}
		dropHoists()
		// `x = count + f(y)` / `let x = f(g(y)) + 1`: the RHS reading hoists
		// each call it met, left to right, and those statements go out ahead
		// of the assignment that reads their temps
		if assignment, ok := AssignmentOf(context, s); ok {
			out = flush(out)
			out = append(out, kernelbridge.IrStatement{Kind: kernelbridge.IrStatementAssign, Target: assignment.Target, Effect: assignment.Effect})
			continue
		}
		dropHoists()
		// `this.value = e` where value is a SETTER: the write runs a
		// body, so it lowers to the setter's own call statement — the
		// value at entry 0, the written this-fields riding back through
		// rets. Tried after AssignmentOf, whose target resolution has no
		// slot for an accessor-backed name.
		if viaSetter, ok := SetterWriteOf(context, s); ok {
			out = flush(out)
			out = append(out, viaSetter...)
			continue
		}
		dropHoists()
		// `await f(…)` in every statement position, plus the two promise
		// shapes: a promise HELD in a local and only ever awaited, and
		// `await Promise.all([…])` whose value is unused. Tried ahead of
		// the plain call route, which has no reading for an await node.
		if viaAwait, ok := AwaitStatementOf(context, s); ok {
			out = flush(out)
			out = append(out, viaAwait...)
			continue
		}
		dropHoists()
		// a CALLBACK-taking call (`xs.map(cb)` and its siblings) whose
		// callback converts: the hook lowers the whole site. Tried ahead
		// of the plain call route, whose callee resolution has no reading
		// for a collection method.
		if viaCallback, ok := SummaryCallbackStatementOf(context, s); ok {
			out = flush(out)
			out = append(out, viaCallback...)
			continue
		}
		dropHoists()
		// `f(…)` / `x = f(…)` where the callee has a compiled summary:
		// the call statement, applying the summary kernel-side. Tried
		// ahead of the inlining route, which lowers the callee's body
		// into fresh slots instead.
		if viaSummary, ok := SummaryCallStatementOf(context, s); ok {
			out = flush(out)
			out = append(out, viaSummary...)
			continue
		}
		dropHoists()
		if viaCall, ok := CallAssignmentOf(context, s); ok {
			out = flush(out)
			out = append(out, viaCall...)
			continue
		}
		dropHoists()
		// EVERY ROUTE PAST THIS POINT IS COMPOSITE, and none of them has a
		// statement position for a hoist of its OWN expressions:
		//
		//   - the for-of routes and the loop routes fold their bodies into
		//     the solver's effect-per-binding form (FoldBody, ir_loop.go),
		//     which has no statement stream at all;
		//   - the `if` and `switch` routes lower their ARMS through nested
		//     LowerStatements calls, which set the flag for themselves and
		//     restore it on the way out — an arm's own hoists belong inside
		//     that arm, never ahead of the whole branch, since the arm may
		//     not run;
		//   - the guard composer reads the CONDITION, which runs before
		//     either arm, but a hoist there would be a statement emitted
		//     ahead of a branch whose test is the very expression that was
		//     hoisted out of — the test would then read a temp written by a
		//     call the condition's own short-circuiting may never reach.
		//
		// So the flag goes DOWN here and every one of them reads exactly as
		// it did before hoisting existed. The nested LowerStatements calls
		// raise it again for their own statements.
		context.CanHoist = false
		// `for (const x of a)` over a flattened array — the loop whose
		// per-pass element effect is the element slot's var.
		if loop, ok := ArrayForOfLowering(context, s); ok {
			out = append(out, loop)
			continue
		}
		// `for (const v of m.values())` and the entries/keys forms over a
		// flattened collection — the same loop against the map's slots.
		if loop, ok := MapForOfLowering(context, s); ok {
			out = append(out, loop)
			continue
		}
		// `switch (x) { case "a": … }` — the chain of equality branches.
		if chain, ok := LowerSwitch(context, s); ok {
			out = append(out, chain...)
			// an arm that RETURNED: the rest of the block runs only where
			// the done flag stayed down, exactly as a returning if-arm does
			if context.Result != nil && RaisesDone(chain, context.Result.Done) {
				rest, restOk := LowerStatements(context, statements[index+1:])
				if !restOk {
					return nil, false
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
				return out, true
			}
			continue
		}
		if ast.IsIfStatement(s) {
			ifStmt := s.AsIfStatement()
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
				// the guard composer reads the head: single tests, typeof
				// folds, `!`/`&&`/`||` nesting, and inlined call guards
				guarded, guardedOk = LowerGuard(context, ifStmt.Expression, thn, els)
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
			// an arm that did not lower, or a condition the opaque branch
			// refuses (it WRITES, or it awaits, or it constructs), falls to
			// the havoc floor: the whole if havocs every slot either arm or
			// the head could have written, which is sound and keeps the body.
			if !guardedOk {
				havoc, havocOk := havocFloor(s)
				if !havocOk {
					return nil, false
				}
				out = append(out, havoc...)
				continue
			}
			out = append(out, guarded...)
			// an arm that may have RETURNED: the block's remainder runs
			// only where the done flag stayed down — the guard is an
			// ordinary branch on the flag, so the join over both paths
			// stays inside the proved walk
			if context.Result != nil && RaisesDone(guarded, context.Result.Done) {
				rest, ok := LowerStatements(context, statements[index+1:])
				if !ok {
					return nil, false
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
				return out, true
			}
			continue
		}
		if ast.IsWhileStatement(s) {
			while := s.AsWhileStatement()
			head, headOk := LoopHeadOf(context, while.Expression)
			body, bodyOk := LoopBodyOf(context, while.Statement, nil)
			if !headOk || !bodyOk {
				// a head or body the loop form cannot spell: the whole loop
				// havocs the union of what its head and body could write, ONCE.
				// A loop is its statements repeated and havoc is idempotent —
				// writing unknown into a slot twice leaves the same state — so
				// one pass covers every trip count, the zero-trip case included.
				havoc, havocOk := havocFloor(s)
				if !havocOk {
					return nil, false
				}
				out = append(out, havoc...)
				continue
			}
			out = append(out, LoopStatement(context, head, body))
			continue
		}
		if ast.IsDoStatement(s) {
			// `do body while (cond)` is the body ONCE, then the ordinary
			// while loop — the first pass runs unconditionally, and every
			// later pass is exactly what `while (cond) body` does. Both
			// halves are the proved forms already: the once-through is
			// ordinary statement lowering, the remainder the loop
			// statement. No kernel form is added.
			do := s.AsDoStatement()
			once, onceOk := LowerStatements(context, StatementsOf(do.Statement))
			head, headOk := LoopHeadOf(context, do.Expression)
			body, bodyOk := LoopBodyOf(context, do.Statement, nil)
			// a once-through that RETURNS would make the loop's remainder
			// conditional on the done flag, which the loop form cannot
			// express — the two proved halves do not compose there
			returns := onceOk && context.Result != nil && RaisesDone(once, context.Result.Done)
			if !onceOk || !headOk || !bodyOk || returns {
				havoc, havocOk := havocFloor(s)
				if !havocOk {
					return nil, false
				}
				out = append(out, havoc...)
				continue
			}
			out = append(out, once...)
			out = append(out, LoopStatement(context, head, body))
			continue
		}
		if ast.IsForStatement(s) {
			// for (init; cond; step) body — the init runs before the loop,
			// the step folds as the body's final statement
			forStmt := s.AsForStatement()
			var head LoopHead
			var body LoopBody
			headOk, bodyOk := false, false
			if forStmt.Condition != nil {
				head, headOk = LoopHeadOf(context, forStmt.Condition)
				body, bodyOk = LoopBodyOf(context, forStmt.Statement, forStmt.Incrementor)
			}
			// the init has to lower too, and it is lowered BEFORE the loop
			// statement is appended so a declining init leaves nothing behind
			var init AssignmentTarget
			initOk := forStmt.Initializer == nil
			if forStmt.Initializer != nil {
				if ast.IsVariableDeclarationList(forStmt.Initializer) {
					init, initOk = DeclarationAssignment(context, forStmt.Initializer.AsVariableDeclarationList().Declarations.Nodes)
				} else {
					init, initOk = AssignmentOfExpression(context, forStmt.Initializer)
				}
			}
			if !headOk || !bodyOk || !initOk {
				// no condition (`for (;;)`), an unreadable head, an unreadable
				// body, or an init the assignment grammar declines: the whole
				// for havocs the union of what its three clauses and its body
				// could write, once — idempotent, so one pass covers every trip
				// count including zero
				havoc, havocOk := havocFloor(s)
				if !havocOk {
					return nil, false
				}
				out = append(out, havoc...)
				continue
			}
			if forStmt.Initializer != nil {
				out = append(out, kernelbridge.IrStatement{Kind: kernelbridge.IrStatementAssign, Target: init.Target, Effect: init.Effect})
			}
			out = append(out, LoopStatement(context, head, body))
			continue
		}
		// THE LAST RESORT, after every route above including the if's own
		// branchBoth fallback: a statement nothing read still cannot do
		// anything but move slots, and the slots it could have moved are
		// enumerable — its assignment targets, the leaves of every
		// flattened local it mentions, and the names it declares. Each one
		// takes `unknown`, every other slot keeps its knowledge, and the
		// body keeps its route. (ir_opaque_havoc.go; false only where the
		// slot set genuinely cannot be enumerated, which is what still
		// declines the body.)
		//
		// The hoist accumulation is EMPTY here by the rules above — every
		// route that could have added to it either flushed on success or
		// truncated on decline, and the composite routes ran with hoisting
		// off. The drop states that invariant rather than trusting it: a
		// route added later that forgets its own truncation would otherwise
		// leak a call statement into the floor's output, and the floor's
		// whole contract is that it lowers to assignments of unknown and
		// nothing else.
		dropHoists()
		havoc, havocOk := havocFloor(s)
		if !havocOk {
			return nil, false
		}
		out = append(out, havoc...)
	}
	return out, true
}

// declinedFloorConstruct names the construct the havoc floor refused a
// statement over. The floor's own scan knows which node refused
// (DeclinedHavocConstruct); where it names nothing — the slot
// enumeration itself failed — the statement's own syntax is the name,
// which is still a row someone can act on.
func declinedFloorConstruct(s *ast.Node) string {
	if named := DeclinedHavocConstruct(s); named != "" {
		return named
	}
	return havocConstructName(s)
}

// ThrowReachesATry is whether a `throw` transfers to a `catch` of THIS
// body rather than leaving it — the one structural question the escaping
// throw's lowering turns on.
//
// Syntactic, by the parent chain: from the throw upwards, a `try`
// reached before the body's root means the throw goes to that try's
// catch (or through its finally and on out, which is the same thing for
// this question: statements of this body run after the throw, and the
// walk's done flag cannot spell "ran the catch, then ended"). The root
// is the enclosing function — or the source file, for the top-level
// statement lists the lowering tests hand in.
//
// A throw inside a nested FUNCTION is not this body's throw at all, and
// never reaches here: the statement routes never descend into one.
//
// The answer is TRUE for a chain the walk cannot follow (a detached
// node with no parent set), because the refusal only ever costs
// coverage while a wrong false would raise the done flag over a catch.
func ThrowReachesATry(throw *ast.Node) bool {
	if throw == nil {
		return true
	}
	for node := throw.Parent; node != nil; node = node.Parent {
		if ast.IsTryStatement(node) {
			return true
		}
		if ast.IsFunctionLike(node) || ast.IsClassLike(node) || ast.IsSourceFile(node) {
			return false
		}
	}
	// no parent chain at all: nothing was read, so nothing is claimed
	return true
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

// assignsOf turns a per-slot assignment list into the IR statements
// that write them, in order — the one shape every flattening lowering
// hands back.
func assignsOf(assignments []AssignmentTarget) []kernelbridge.IrStatement {
	out := make([]kernelbridge.IrStatement, 0, len(assignments))
	for _, assignment := range assignments {
		out = append(out, kernelbridge.IrStatement{
			Kind:   kernelbridge.IrStatementAssign,
			Target: assignment.Target,
			Effect: assignment.Effect,
		})
	}
	return out
}

// LowerSwitch is a `switch (x) { case k: … }` as a CHAIN of equality
// branches: the first case's test with its body as the then-arm and the
// rest of the chain as the else-arm, down to the default clause, which
// becomes the final else. Exactly the desugaring the language's own
// semantics gives a switch whose every case ends in break or return —
// which is the only shape lowered here.
//
// Fallthrough is NOT lowered: a case whose statements run on into the
// next clause takes two arms at once, which the chain does not spell.
// A case with NO statements at all is the grouped-label form (`case
// "a": case "b": …`), which is not fallthrough — nothing runs — so it
// folds into the next clause's test as a second equality.
//
// Total-or-decline: any clause whose statements or label do not lower
// declines the whole switch, and the statement then takes its former
// route (which is nothing — a switch has no other IR lowering).
func LowerSwitch(context *LoweringContext, statement *ast.Node) ([]kernelbridge.IrStatement, bool) {
	if !ast.IsSwitchStatement(statement) {
		return nil, false
	}
	switchStmt := statement.AsSwitchStatement()
	discriminant := switchStmt.Expression
	if _, tracked := IndexOf(context, discriminant); !tracked {
		return nil, false
	}
	clauses := switchStmt.CaseBlock.AsCaseBlock().Clauses.Nodes
	if len(clauses) == 0 {
		return nil, false
	}
	// arms, in clause order: the labels a clause tests (several where
	// empty clauses grouped ahead of it) and the statements it runs
	type switchArm struct {
		Labels     []*ast.Node // nil for the default clause
		Statements []*ast.Node
		IsDefault  bool
	}
	var arms []switchArm
	var pendingLabels []*ast.Node
	for _, clause := range clauses {
		body := clause.AsCaseOrDefaultClause().Statements.Nodes
		if ast.IsDefaultClause(clause) {
			// a default with grouped labels ahead of it would need those
			// labels to reach the default's own statements, which the chain
			// spells only as the final else — decline rather than mis-order
			if len(pendingLabels) > 0 {
				return nil, false
			}
			arms = append(arms, switchArm{Statements: body, IsDefault: true})
			continue
		}
		label := clause.AsCaseOrDefaultClause().Expression
		if len(body) == 0 {
			// a grouped label: nothing runs here, so it joins the next
			// clause's test
			pendingLabels = append(pendingLabels, label)
			continue
		}
		labels := append(append([]*ast.Node{}, pendingLabels...), label)
		pendingLabels = nil
		arms = append(arms, switchArm{Labels: labels, Statements: body})
	}
	// labels left over after the last clause reach nothing
	if len(pendingLabels) > 0 {
		return nil, false
	}
	// exactly one default, and it must be LAST — a default in the middle
	// runs before the cases after it only under fallthrough, which is not
	// lowered here
	for index, arm := range arms {
		if arm.IsDefault && index != len(arms)-1 {
			return nil, false
		}
	}
	// every non-default arm must END its run: break or return. Without
	// that the clause falls through into the next one, which the chain
	// does not spell.
	for _, arm := range arms {
		if arm.IsDefault {
			continue
		}
		if !armEndsItsRun(arm.Statements) {
			return nil, false
		}
	}
	// the chain is built from the LAST arm outwards: the default (or the
	// empty else where there is none) is the innermost else, and each
	// case's test wraps it
	var chain []kernelbridge.IrStatement
	tail := len(arms)
	if tail > 0 && arms[tail-1].IsDefault {
		lowered, ok := LowerStatements(context, stripTrailingBreak(arms[tail-1].Statements))
		if !ok {
			return nil, false
		}
		chain = lowered
		tail--
	}
	for index := tail - 1; index >= 0; index-- {
		arm := arms[index]
		body, ok := LowerStatements(context, stripTrailingBreak(arm.Statements))
		if !ok {
			return nil, false
		}
		// several grouped labels are an `||` of equalities: each label's
		// test takes the same then-arm, and its else is the next label's
		// test — the nesting LowerGuard already builds for `a || b`
		guarded := chain
		for labelIndex := len(arm.Labels) - 1; labelIndex >= 0; labelIndex-- {
			test, testOk := switchLabelGuard(context, discriminant, arm.Labels[labelIndex], body, guarded)
			if !testOk {
				return nil, false
			}
			guarded = test
		}
		chain = guarded
	}
	return chain, true
}

// switchLabelGuard is one `case k:` as its equality branch, built
// through the shared guard composer so the discriminant's sort decides
// the reading exactly as an `if (x === k)` head would.
func switchLabelGuard(
	context *LoweringContext,
	discriminant *ast.Node,
	label *ast.Node,
	thn []kernelbridge.IrStatement,
	els []kernelbridge.IrStatement,
) ([]kernelbridge.IrStatement, bool) {
	head := Unwrapped(label)
	// only a literal word or number is a case the chain can test: a
	// computed label compares two values the equality tests do not speak
	if _, isNumber := NumberOf(head); !isNumber && !ast.IsStringLiteral(head) {
		return nil, false
	}
	factory := ast.NewNodeFactory(ast.NodeFactoryHooks{})
	equality := factory.NewBinaryExpression(
		nil,
		discriminant,
		nil,
		factory.NewToken(ast.KindEqualsEqualsEqualsToken),
		label,
	)
	return LowerGuard(context, equality, thn, els)
}

// armEndsItsRun is whether a case clause's statements END the switch —
// a trailing `break` or `return`. Anything else falls through into the
// next clause, and the chain has no arm for that.
func armEndsItsRun(statements []*ast.Node) bool {
	if len(statements) == 0 {
		return false
	}
	last := statements[len(statements)-1]
	if ast.IsBreakStatement(last) {
		// a LABELLED break leaves some outer statement, not this switch
		return last.AsBreakStatement().Label == nil
	}
	if ast.IsReturnStatement(last) {
		return true
	}
	// a trailing block ends the run where its own last statement does
	if ast.IsBlock(last) {
		return armEndsItsRun(last.AsBlock().Statements.Nodes)
	}
	return false
}

// stripTrailingBreak drops the `break` that ended a case clause: the
// chain's arm already ends where the arm ends, so the break has nothing
// left to leave. A trailing `return` STAYS — it writes the result slot
// and raises the done flag, which the arm must carry.
func stripTrailingBreak(statements []*ast.Node) []*ast.Node {
	if len(statements) == 0 {
		return statements
	}
	last := statements[len(statements)-1]
	if ast.IsBreakStatement(last) && last.AsBreakStatement().Label == nil {
		return statements[:len(statements)-1]
	}
	if ast.IsBlock(last) {
		inner := stripTrailingBreak(last.AsBlock().Statements.Nodes)
		out := append([]*ast.Node{}, statements[:len(statements)-1]...)
		return append(out, inner...)
	}
	return statements
}
