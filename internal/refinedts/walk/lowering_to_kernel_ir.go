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
	"github.com/microsoft/typescript-go/internal/jsnum"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
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
	// THE CAPTURE HAVOCS: a body that admitted a method-calling capture
	// carries the captured methods' transitive write set in
	// CaptureHavocSlots. The stored closure may run inside ANY code the
	// body executes, so every code-running statement is bracketed by
	// unknown-assigns of that set — BEFORE, so nothing the statement
	// reads pretends those fields held still across earlier code, and
	// AFTER, so nothing later does. A code-running statement that is not
	// a return or a throw is floored outright: its fine-grained routes
	// could interleave a served call between a havoc and a field read.
	captureHavocSet := map[int]struct{}{}
	for _, slot := range context.CaptureHavocSlots {
		if slot >= 0 {
			captureHavocSet[slot] = struct{}{}
		}
	}
	captureHavocAfter := false
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
		if captureHavocAfter {
			out = append(out, havocAssignments(captureHavocSet)...)
			captureHavocAfter = false
		}
		if len(captureHavocSet) > 0 && (StatementRunsCode(s) || StatementStoresElement(s)) {
			out = append(out, havocAssignments(captureHavocSet)...)
			captureHavocAfter = true
			if !ast.IsReturnStatement(s) && !throwCarryingStatement(s) {
				havoc, havocOk := havocFloor(s)
				if !havocOk {
					return nil, false
				}
				out = append(out, havoc...)
				continue
			}
		}
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
							Effect: kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectVar, Index: inlined.RetIndex},
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
				// NOTHING (write- and call-free) — `return this`,
				// `return host && host.instance`, `return x ? a[k] : a`,
				// `return { }`. The value has no scalar spelling, so unknown
				// IS what the ret slot can say, and evaluating the expression
				// changed no state — the statement is READ, not floored.
				if ast.IsFunctionLike(head) || writeAndCallFree(head) {
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
		// THE ESCAPING THROW. `throw e` whose parent chain up to this
		// body's root passes through no `try` cannot reach a catch of this
		// body — it leaves the body outright. A run that threw returns
		// NOTHING, so no claim about the returned outcome can be wrong
		// about it, and the shape that says so is a return of nothing:
		//
		//	#ret  := thrown
		//	#done := {1}
		//
		// THROWN, not absent. The run produced no returned value at all,
		// and the kernel has a constructor for exactly that — its fourth
		// Outcome, distinct from the absent VALUE a bare `return;` writes.
		// Sending absent here is what made `if (x) throw new E(); return v`
		// serve `v ∪ undefined`: the throw arm's absent merged into the ret
		// set, and nothing downstream could tell it from a path that really
		// returned undefined. Writing thrown keeps the two apart, and the
		// ret row's returned half (KnownStateWire.Returned) then reads the
		// real return alone.
		//
		// The done flag reads exactly as it does for a `return` — the block
		// ends here, later statements are dead, and the apply route's
		// allReturned reading sees a path that left.
		//
		// A throw INSIDE a try no longer declines by default. The old
		// reasoning — raising the flag would make the catch's writes
		// invisible — is about a catch that CONTINUES this statement list.
		// Under the try route's branchBoth the catch is a SIBLING arm,
		// walked from the state as it stood, so the flag raised in the try
		// arm cannot reach it. ThrowCoveredByItsTry is the test for
		// exactly that shape; every other enclosing try keeps the decline.
		if ast.IsThrowStatement(s) {
			if context.Result == nil {
				NoteDeclinedConstruct(context, "throw with no result slot")
				dropHoists()
				return nil, false
			}
			// A throw INSIDE a try is sound too where the enclosing try's
			// own lowering covers it — ThrowCoveredByItsTry holds the whole
			// argument. A throw under any OTHER try keeps the decline, and
			// the report names it "throw inside try" so the row points at
			// the construct.
			if ThrowReachesATry(s) && !ThrowCoveredByItsTry(s) {
				NoteDeclinedConstruct(context, "throw inside try")
				dropHoists()
				return nil, false
			}
			// the thrown EXPRESSION's own effects: `throw new E(x)` runs a
			// constructor that may move whatever the arguments mention. The
			// mention havoc covers exactly that — the floor's own rule —
			// and with it the whole statement is READ: control exact (ret
			// absent, done raised — a throw produces no value, and code
			// after the call only runs when no throw happened), value
			// nothing, effects covered. Only an unenumerable expression
			// keeps the porous mark.
			if slots, enumerable := havocSlotsOfStatement(context, s); enumerable {
				out = append(out, havocAssignments(slots)...)
			} else {
				NoteFirstHavoc(context, "throw")
			}
			out = append(out, kernelbridge.IrStatement{
				Kind:   kernelbridge.IrStatementAssign,
				Target: context.Result.Ret,
				Effect: kernelbridge.ThrownConst(),
			})
			out = append(out, kernelbridge.IrStatement{
				Kind:   kernelbridge.IrStatementAssign,
				Target: context.Result.Done,
				Effect: kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectConst, Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{1}))},
			})
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
		// `const { x = 1 } = p` — the leaf-exact destructuring with
		// per-element defaults under the definedness branch.
		if viaDefaults, ok := DestructuringWithDefaultsOf(context, s); ok {
			out = flush(out)
			out = append(out, viaDefaults...)
			continue
		}
		dropHoists()
		// `const { a } = call()` and every other pattern source the exact
		// route above declined: the bound names take unknown — which is
		// what is true of them — and the source's call lowers through the
		// call machinery. AFTER the leaf-exact route, so a flattened
		// record's pattern keeps its real values.
		if viaPattern, ok := PatternAssignmentsOf(context, s); ok {
			out = flush(out)
			out = append(out, viaPattern...)
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
		// `const f = () => { … }` — a closure held in a local: creation
		// runs nothing, the name takes unknown, admitted only when the
		// closure touches no tracked state.
		if viaClosure, ok := FunctionValuedDeclarationOf(context, s); ok {
			out = flush(out)
			out = append(out, viaClosure...)
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
		// `x1 = x2 = e` — the chain writes every link, innermost first,
		// each outer one copying the slot inside it. Ahead of the ordinary
		// assignment rule, whose one-target read takes the outer link and
		// then declines on a right side no effect grammar spells.
		if assignments, ok := ChainedAssignmentStatementOf(context, s); ok {
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
		// EVERY OTHER for-of and for-in: an iterable nothing flattened, a
		// binding pattern the two routes above do not spell, `for await`.
		// Both of those routes need the collection's slots to give the
		// binding its per-pass value; this one does not read the value at
		// all — the bound names take unknown and the body's statements ride
		// under the statement-bodied loop. Before this, every such head fell
		// to the floor and havocked each leaf the loop so much as MENTIONED.
		if ast.IsForOfStatement(s) || ast.IsForInStatement(s) {
			if prelude, loop, stmtsOk := LowerLoopStatements(context, s); stmtsOk {
				appended, consumed, appendOk := appendLoopGatingRest(context, out, prelude, loop, statements, index)
				if !appendOk {
					return nil, false
				}
				out = appended
				if consumed {
					return out, true
				}
				continue
			}
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
		// `try { … } catch { … }` — the opaque branch over the two
		// completions: the try whole, or an interrupted prefix's havoc
		// then the catch (ir_try_lowering.go).
		if ast.IsTryStatement(s) {
			if viaTry, ok := LowerTryStatement(context, s); ok {
				out = append(out, viaTry...)
				// an arm that RETURNED gates the rest on the done flag,
				// exactly as a returning switch arm does
				if context.Result != nil && RaisesDone(viaTry, context.Result.Done) {
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
				// a head or body the SOLVER's form cannot spell still has the
				// statement-bodied form: no condition read, any trip count, and
				// the body's own write set havocked kernel-side while every
				// other slot keeps its knowledge (ir_loop_stmts.go)
				if prelude, loop, stmtsOk := LowerLoopStatements(context, s); stmtsOk {
					appended, consumed, appendOk := appendLoopGatingRest(context, out, prelude, loop, statements, index)
					if !appendOk {
						return nil, false
					}
					out = appended
					if consumed {
						return out, true
					}
					continue
				}
				// and where THAT declines too — a head that moves state, a body
				// statement nothing read — the whole loop havocs the union of
				// what its head and body could write, ONCE. A loop is its
				// statements repeated and havoc is idempotent — writing unknown
				// into a slot twice leaves the same state — so one pass covers
				// every trip count, the zero-trip case included.
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
				// the statement-bodied form before the floor: it admits any
				// trip count including zero, which is weaker than "at least
				// once" and never wrong about a run that took more
				if prelude, loop, stmtsOk := LowerLoopStatements(context, s); stmtsOk {
					appended, consumed, appendOk := appendLoopGatingRest(context, out, prelude, loop, statements, index)
					if !appendOk {
						return nil, false
					}
					out = appended
					if consumed {
						return out, true
					}
					continue
				}
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
			// statement is appended so a declining init leaves nothing behind.
			// A clause declaring several names writes one assignment per
			// declarator, in source order — the same reading the
			// statement-bodied route uses, so the two cannot disagree about
			// which initializers lower.
			init, initOk := forInitializerAssignments(context, forStmt.Initializer)
			if !headOk || !bodyOk || !initOk {
				// no condition (`for (;;)`), an unreadable head, or a body the
				// FOLD's grammar declines: the statement-bodied form takes it —
				// the init before the loop, the step as the body's last
				// statement, and no claim about the trip count
				if prelude, loop, stmtsOk := LowerLoopStatements(context, s); stmtsOk {
					appended, consumed, appendOk := appendLoopGatingRest(context, out, prelude, loop, statements, index)
					if !appendOk {
						return nil, false
					}
					out = appended
					if consumed {
						return out, true
					}
					continue
				}
				// and where that declines too — an init the assignment grammar
				// refuses, a head that moves state — the whole for havocs the
				// union of what its three clauses and its body could write,
				// once — idempotent, so one pass covers every trip count
				// including zero
				havoc, havocOk := havocFloor(s)
				if !havocOk {
					return nil, false
				}
				out = append(out, havoc...)
				continue
			}
			out = append(out, init...)
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
	// the trailing havoc: a code-running LAST statement (a return whose
	// expression called, a final registration) leaves the havoc set
	// moved, and the write-back reads the exit state
	if captureHavocAfter {
		out = append(out, havocAssignments(captureHavocSet)...)
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

// ThrowCoveredByItsTry is whether a `throw` inside a try is one the
// enclosing try's OWN lowering already covers — the question that turns
// "throw inside try" from a decline into an ordinary read.
//
// THE ARGUMENT. LowerTryStatement builds one statement:
//
//	branchBoth
//	  then: the try block's statements, lowered whole
//	  else: havoc(every slot the try block could write)
//	        catch parameter := unknown
//	        the catch block's statements
//
// and branchBoth's contract (kernelbridge/loop_questions.go) is that
// BOTH arms walk from the state as it stood and their exits join. The
// arms are siblings, not a sequence. So whatever the then arm writes —
// including the done flag — is not what the else arm reads. That is the
// exact hole in the old decline's reasoning: it argued that raising the
// flag at a throw would gate the catch's own statements behind the
// flag's falsity, which is true only where the catch CONTINUES the
// statement list the throw sat in. Under this route it does not.
//
// What the two arms cover, run by run:
//
//   - a run that completed the try normally IS the then arm, and the
//     throw statement never executed on it. The then arm lowering the
//     throw as `ret := absent; done := {1}` and ending the block costs
//     that run nothing, because the run did not reach the throw.
//   - a run that threw at statement k ran statements 1..k-1 and then the
//     catch. Its state at catch entry differs from the entry state only
//     in slots written by that prefix. The else arm's prefix havoc is
//     havocSlotsOfStatement over the WHOLE try block, which is a
//     superset of any prefix's write set, so the catch walks from a
//     state weaker than the real one — for every k, the explicit throw's
//     own k included. That is what already made the route sound for an
//     interrupted run, and an explicit `throw` is only one more value of
//     k, not a new kind of interruption.
//
// So the then arm is right about the runs it stands for and the else arm
// covers the rest, which is the whole obligation.
//
// THE THROW'S OWN EXPRESSION. `throw new E(x)` runs a constructor before
// control transfers. The then arm's throw route havocs the statement's
// mention set (havocSlotsOfStatement over the throw) before writing ret
// and done, so that path carries the effects. The else arm carries them
// too, by the same superset reasoning: the throw statement is inside the
// try block, so its mentions are in the block's own enumeration. Nothing
// the expression could move escapes both arms.
//
// THE SHAPE THIS IS TRUE OF. Only the try LowerTryStatement actually
// serves: a catch clause present and no finally. A `finally` runs on
// every completion and the route declines it outright, so a throw under
// one has no branchBoth above it at all; a try with no catch does not
// catch the throw, which then leaves the body through the enclosing
// frames — a different question the escaping-throw route is not in a
// position to answer here. Both keep the decline.
//
// The nearest enclosing try is the one that catches, so only that one is
// consulted; a throw nested in an inner try under an outer one is the
// inner try's business. The walk stops at a function boundary for the
// same reason ThrowReachesATry does.
func ThrowCoveredByItsTry(throw *ast.Node) bool {
	if throw == nil {
		return false
	}
	for node := throw.Parent; node != nil; node = node.Parent {
		if ast.IsTryStatement(node) {
			tryStmt := node.AsTryStatement()
			// a throw sitting in the CATCH block is not caught by this try
			// at all — it leaves through the enclosing frames, and the arm
			// structure above says nothing about it
			if tryStmt.CatchClause != nil && tryStmt.CatchClause.Contains(throw) {
				return false
			}
			return tryStmt.CatchClause != nil && tryStmt.FinallyBlock == nil
		}
		if ast.IsFunctionLike(node) || ast.IsClassLike(node) {
			return false
		}
	}
	return false
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

// loopBodyRaisesDone is whether a lowered LOOP statement's own body
// writes the done flag — a `return` inside `for`, `for-of`, `while` or
// `do-while`. RaisesDone reads a statement LIST and does not descend
// into a loop's body, which is right for its callers (an if arm's
// statements are the arm) and wrong for this question, so the loop's
// body is unwrapped here and handed to RaisesDone.
//
// Both loop forms carry their body in a field of their own: the
// statement-bodied loop in Stmts, the effect-bodied loop in Body, which
// is one EFFECT per binding and has no statements to walk — an effect
// vector cannot spell a return at all, so that form answers false and
// the reading is complete.
func loopBodyRaisesDone(loop kernelbridge.IrStatement, done int) bool {
	if loop.Kind != kernelbridge.IrStatementLoopStmts {
		return false
	}
	return RaisesDone(loop.Stmts, done)
}

// appendLoopGatingRest appends a lowered loop and, where its body could
// have RETURNED, gates the block's remainder on the done flag — the same
// shape a returning if arm, switch arm, and try arm each build.
//
// The gate is what makes a return inside a loop READ rather than a wrong
// claim. Without it the statements after the loop lower unconditionally,
// so a run that returned on trip 3 would have the kernel walk them
// anyway and a later `return` would overwrite the result slot the loop's
// own return wrote. With it, the remainder sits in the else arm of a
// branch on the flag, and the run that returned walks the empty then arm
// instead — exactly the continuation gating every other returning
// construct already gets.
//
// The flag itself is one of the slots the loop's own havoc covers, so
// after a loop whose body may or may not have returned the flag reads
// unknown and the branch admits both paths. That is the honest reading:
// the trip count is not claimed, so which of them happened is not known.
//
// Answers (out, true) with the remainder consumed, or (out, false)
// meaning the caller carries on with the next statement itself.
func appendLoopGatingRest(
	context *LoweringContext,
	out []kernelbridge.IrStatement,
	prelude []kernelbridge.IrStatement,
	loop kernelbridge.IrStatement,
	statements []*ast.Node,
	index int,
) ([]kernelbridge.IrStatement, bool, bool) {
	out = append(out, prelude...)
	out = append(out, loop)
	if context.Result == nil || !loopBodyRaisesDone(loop, context.Result.Done) {
		return out, false, true
	}
	rest, restOk := LowerStatements(context, statements[index+1:])
	if !restOk {
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
// branches: each case's test with its own statements as the then-arm and
// the rest of the chain as the else-arm, down to the default arm, which
// becomes the innermost else. Exactly the desugaring the language's own
// semantics gives a switch whose every reached run ends in break or
// return.
//
// CLAUSE ORDER IS NOT TEST ORDER. A switch evaluates the discriminant
// once and then compares it against EVERY case label, in clause order,
// whatever position the default clause sits in; only when no label
// matched does the default's statements run. So the chain does not need
// the default to be the LAST clause — it needs the default to be the
// innermost ELSE, which is where "no label matched" lands however the
// clauses were written. `switch (k) { default: d(); break; case 1:
// a(); break; }` and the same two clauses swapped are the same runs,
// and both lower to `if k===1 then a() else d()`. The arms are
// therefore collected by what they TEST — one list of case arms and one
// default arm — rather than by where their clause sat.
//
// FALLTHROUGH IS CONCATENATION. A case that does not end its run
// continues into the next clause's statements, so the arm a matching
// run actually executes is its own statements followed by the next
// clause's, and the next's, until a clause that ends the run. That
// concatenation is what each arm lowers here: `case 1: case 2: f();
// break;` gives the empty clause 1 the statements of clause 2, and
// `case 1: g(); case 2: f(); break;` gives clause 1 `g(); f();`. A
// chain that reaches the end of the clause list without ever ending its
// run keeps the refusal — nothing there says where the run stops.
//
// Total-or-decline: any arm whose statements or label do not lower
// declines the whole switch, and the statement then takes its former
// route (which is nothing — a switch has no other IR lowering).
func LowerSwitch(context *LoweringContext, statement *ast.Node) ([]kernelbridge.IrStatement, bool) {
	if !ast.IsSwitchStatement(statement) {
		return nil, false
	}
	switchStmt := statement.AsSwitchStatement()
	discriminant := switchStmt.Expression
	// EVERY REFUSAL BELOW NAMES ITSELF. A switch that declines here falls
	// to the havoc floor, which refuses any contained `return` and then
	// reports "return inside switch" — a row that names the return rather
	// than the thing the switch route actually could not read. The names
	// set here are what the floor's DeclinedHavocConstruct reports
	// instead, so each row points at the construct to go and build a
	// reading for.
	_, discriminantTracked := IndexOf(context, discriminant)
	// an untracked discriminant that EVALUATES effect-free still lowers,
	// as the tested-nothing chain below; one whose evaluation moves state
	// has no statement position here and keeps its name
	if !discriminantTracked && !OpaqueTestableCondition(discriminant) {
		NoteSwitchRefusal(statement, "switch on an untracked discriminant")
		return nil, false
	}
	clauses := switchStmt.CaseBlock.AsCaseBlock().Clauses.Nodes
	if len(clauses) == 0 {
		NoteSwitchRefusal(statement, "switch with no clauses")
		return nil, false
	}
	arms, armsOk := switchArmsOf(statement, clauses)
	if !armsOk {
		return nil, false
	}
	// the chain is built from the DEFAULT outwards: the default arm (or
	// the empty else where there is none — the run that matched no label
	// and fell past the switch) is the innermost else, and each case's
	// test wraps it
	var chain []kernelbridge.IrStatement
	if arms.HasDefault {
		lowered, ok := LowerStatements(context, stripTrailingBreak(arms.Default))
		if !ok {
			// the arm's own walk already named what it refused ON, and that
			// name is the one worth keeping — it points inside the clause
			NoteSwitchRefusal(statement, "switch whose default arm did not lower")
			return nil, false
		}
		chain = lowered
	}
	for index := len(arms.Cases) - 1; index >= 0; index-- {
		arm := arms.Cases[index]
		body, ok := LowerStatements(context, stripTrailingBreak(arm.Statements))
		if !ok {
			NoteSwitchRefusal(statement, "switch whose case arm did not lower")
			return nil, false
		}
		if !discriminantTracked {
			// no slot to test: the arm and the rest of the chain are
			// SIBLINGS, both possible, and their exits join. A concrete run
			// takes exactly one of them and the join admits it either way —
			// the arm's own effects ride, and no claim is made about which
			// label matched.
			chain = []kernelbridge.IrStatement{{
				Kind: kernelbridge.IrStatementBranchBoth,
				Then: body,
				Else: chain,
			}}
			continue
		}
		// several labels reaching one arm are an `||` of equalities: each
		// label's test takes the same then-arm, and its else is the next
		// label's test — the nesting LowerGuard already builds for `a || b`
		guarded := chain
		for labelIndex := len(arm.Labels) - 1; labelIndex >= 0; labelIndex-- {
			test, testOk := switchLabelGuard(context, discriminant, arm.Labels[labelIndex], body, guarded)
			if !testOk {
				NoteSwitchRefusal(statement, "switch on a case label that is not a literal")
				return nil, false
			}
			guarded = test
		}
		chain = guarded
	}
	return chain, true
}

// switchCaseArm is one case arm: the labels that reach it and the
// statements a run reaching it executes — its clause's own statements
// followed by every clause it falls through into.
type switchCaseArm struct {
	Labels     []*ast.Node
	Statements []*ast.Node
}

// switchArms is the whole switch read as arms: the case arms in clause
// order, and the default's statements where a default clause exists.
type switchArms struct {
	Cases      []switchCaseArm
	Default    []*ast.Node
	HasDefault bool
}

// switchArmsOf turns the clause list into the arms the chain tests.
//
// The two readings the clause list needs:
//
//   - each clause's RUN is the concatenation of its own statements and
//     those of every clause after it, up to and including the first that
//     ends the run (break or return). An empty clause therefore shares
//     the following clause's run with no statements of its own, which is
//     the grouped-label form, and a non-empty falling-through clause
//     shares it with its own statements in front.
//   - the DEFAULT clause is not part of any case's run order. It is the
//     arm reached when no label matched, so it is collected on its own
//     and its own run is read the same way, from its own clause forward.
//
// Refuses, by name: a run that reaches the end of the clause list
// without ever ending (nothing says where it stops), a second default
// clause (two arms for one "no label matched"), and an empty trailing
// clause whose run is therefore empty and unended — except the LAST
// clause of all, whose empty run is a no-op arm that ends the switch by
// having nothing left to fall into.
func switchArmsOf(statement *ast.Node, clauses []*ast.Node) (switchArms, bool) {
	// the run of clause `start`: its statements and the following
	// clauses' until one ends the run. (nil, false) where none does.
	runFrom := func(start int) ([]*ast.Node, bool) {
		var run []*ast.Node
		for index := start; index < len(clauses); index++ {
			body := clauses[index].AsCaseOrDefaultClause().Statements.Nodes
			run = append(run, body...)
			if armEndsItsRun(body) {
				return run, true
			}
		}
		// the run walked off the end of the clause list. An empty run is
		// the LAST clause with no statements — nothing runs and there is
		// nothing to fall into, which is an ended run of zero statements.
		return run, len(run) == 0
	}
	out := switchArms{}
	var pendingLabels []*ast.Node
	for index, clause := range clauses {
		if ast.IsDefaultClause(clause) {
			if out.HasDefault {
				NoteSwitchRefusal(statement, "switch with a second default clause")
				return switchArms{}, false
			}
			run, ended := runFrom(index)
			if !ended {
				NoteSwitchRefusal(statement, "switch with a falling-through case")
				return switchArms{}, false
			}
			// labels grouped ahead of the default reach the default's own
			// run, which is already the chain's innermost else — every one
			// of them lands there by matching no OTHER label, so the arm
			// needs no test of its own and the labels are dropped
			pendingLabels = nil
			out.Default = run
			out.HasDefault = true
			continue
		}
		label := clause.AsCaseOrDefaultClause().Expression
		body := clause.AsCaseOrDefaultClause().Statements.Nodes
		if len(body) == 0 {
			// an empty clause runs nothing of its own: its label joins the
			// next clause's run, which the following clause will collect
			pendingLabels = append(pendingLabels, label)
			continue
		}
		run, ended := runFrom(index)
		if !ended {
			NoteSwitchRefusal(statement, "switch with a falling-through case")
			return switchArms{}, false
		}
		labels := append(append([]*ast.Node{}, pendingLabels...), label)
		pendingLabels = nil
		out.Cases = append(out.Cases, switchCaseArm{Labels: labels, Statements: run})
	}
	// labels left over after the last clause: their run is what runFrom
	// answers from the FIRST of them, which is empty (every clause after
	// it was empty too, or they would have been collected above)
	if len(pendingLabels) > 0 {
		out.Cases = append(out.Cases, switchCaseArm{Labels: pendingLabels})
	}
	return out, true
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
	// the label as the literal TOKEN it names: itself where it is already
	// one, the const it is bound to, or the enum member's own value
	literal, literalOk := switchLabelLiteral(context, label)
	if !literalOk {
		return nil, false
	}
	factory := ast.NewNodeFactory(ast.NodeFactoryHooks{})
	equality := factory.NewBinaryExpression(
		nil,
		discriminant,
		nil,
		factory.NewToken(ast.KindEqualsEqualsEqualsToken),
		literal,
	)
	return LowerGuard(context, equality, thn, els)
}

// switchLabelLiteral is the literal token a case label names, which is
// what the equality test reads: TestOf takes the right operand
// syntactically, so a label that only NAMES a literal has to hand the
// token over rather than the name.
//
// Three routes, in the order they cost:
//
//   - the label is already a literal word or number;
//   - the label follows const-to-const links to one
//     (dataflowfacts.ConstChainLiteral — the same resolver the walk side's
//     SwitchLabelValuesWith reads its labels through, so the two agree
//     about which labels resolve);
//   - the label is an enum member, whose value the checker holds as the
//     member's literal type. tsgo spells a number literal's value as
//     jsnum.Number, a NAMED float64, so numberLiteralValue reads it; the
//     token handed back is freshly made from that value, since an enum
//     member has no literal token of its own to point at.
//
// (nil, false) for anything else — a computed label compares two values
// the equality tests do not speak.
func switchLabelLiteral(context *LoweringContext, label *ast.Node) (*ast.Node, bool) {
	head := Unwrapped(label)
	if _, isNumber := NumberOf(head); isNumber {
		return head, true
	}
	if ast.IsStringLiteral(head) {
		return head, true
	}
	if context == nil || context.Flow == nil || context.Flow.P == nil || context.Flow.P.Checker == nil {
		return nil, false
	}
	c := context.Flow.P.Checker
	if resolved, ok := dataflowfacts.ConstChainLiteral(c, head); ok {
		if _, isNumber := NumberOf(resolved); isNumber {
			return resolved, true
		}
		if ast.IsStringLiteral(resolved) {
			return resolved, true
		}
	}
	// an enum member read — `case MyEnum.A:`. The checker's own literal
	// type for the member IS the value, at the same grade the enum
	// reader elsewhere takes it at.
	if !ast.IsPropertyAccessExpression(head) {
		return nil, false
	}
	receiverSymbol := symbolAt(c, head.AsPropertyAccessExpression().Expression)
	if receiverSymbol == nil {
		return nil, false
	}
	isEnum := false
	for _, declaration := range receiverSymbol.Declarations {
		if ast.IsEnumDeclaration(declaration) {
			isEnum = true
			break
		}
	}
	if !isEnum {
		return nil, false
	}
	memberType := c.GetTypeAtLocation(head)
	factory := ast.NewNodeFactory(ast.NodeFactoryHooks{})
	if memberType.IsNumberLiteral() {
		// a member the checker never pinned (a computed one) has no value
		// at all, and no token can be made for it
		value, ok := numberLiteralValue(memberType.AsLiteralType().Value())
		if !ok {
			return nil, false
		}
		// a NEGATIVE member spells as a minus over its magnitude, which is
		// the same shape NumberOf reads for a written `case -1:`
		if value < 0 {
			magnitude := factory.NewNumericLiteral(jsnum.Number(-value).String(), ast.TokenFlagsNone)
			return factory.NewPrefixUnaryExpression(ast.KindMinusToken, magnitude), true
		}
		return factory.NewNumericLiteral(jsnum.Number(value).String(), ast.TokenFlagsNone), true
	}
	if memberType.IsStringLiteral() {
		text, ok := memberType.AsLiteralType().Value().(string)
		if !ok {
			return nil, false
		}
		return factory.NewStringLiteral(text, ast.TokenFlagsNone), true
	}
	return nil, false
}

// armEndsItsRun is whether a case clause's statements END the switch —
// a trailing `break` or `return`. Anything else falls through into the
// next clause, whose statements the arm then also runs.
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
		// and a condition that RUNS something has no statement position
		// either — OpaqueTestableCondition is the branch routes' own test
		// for exactly that, and the if route reads it for the same reason.
		if !OpaqueTestableCondition(cond.Condition) {
			return nil, false
		}
		thn, thnOk := returnValueStatements(context, cond.WhenTrue, sort, raise)
		els, elsOk := returnValueStatements(context, cond.WhenFalse, sort, raise)
		if !thnOk || !elsOk {
			return nil, false
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
// are ones whose evaluation MOVES NOTHING and can therefore be read
// twice — the test reads slot `on`, the arm reads slot `on` again, and
// two reads of a slot are the same value with nothing run between them.
// That is exactly a TRACKED SLOT read, which is what IndexOf answers,
// and it is what LowerGuard's own `??` arm already requires of its left
// side (ir_guard.go's definedness branch).
//
// A left operand that RUNS something — `f() ?? b`, `this.get() || b` —
// is declined here rather than hoisted. Hoisting would put the call
// before the branch, which is where it belongs for `a`'s single
// evaluation; but the hoisted temp is unknown-sorted and carries no
// definedness or truthiness the branch could test, so the test would
// have nothing to read and the branch would degrade to branchBoth with
// a call already run — no better than the floor, and with an extra
// statement standing between the reader and the truth. The plain
// sentence is the honest answer: this route reads a short circuit whose
// left side is a tracked slot, and no other.
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
	left := Unwrapped(binary.Left)
	// the left side is read TWICE — once as the branch's test, once as an
	// arm's value — so it must be a slot, whose two reads are one value
	on, tracked := IndexOf(context, left)
	if !tracked {
		return nil, false
	}
	// which test picks the side is the operator's own rule: `??` asks
	// definedness, `&&`/`||` ask truthiness under the slot's sort. A slot
	// wearing neither the number nor the string sort has no truthiness
	// test on the wire, so those two operators decline there.
	test := kernelbridge.IrTestDefined
	if kind != ast.KindQuestionQuestionToken {
		switch context.Sorts[on] {
		case BindingKindNumber:
			test = kernelbridge.IrTestTruthyNum
		case BindingKindString:
			test = kernelbridge.IrTestTruthyStr
		default:
			return nil, false
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
	return []kernelbridge.IrStatement{{
		Kind: kernelbridge.IrStatementBranch,
		On:   on,
		Test: test,
		Then: then,
		Else: els,
	}}, true
}

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
	// place of it. An expression whose write set is not enumerable keeps
	// the decline: there would be nothing to stand in for what it moved.
	slots, enumerable := havocSlotsOfStatement(context, arm)
	if !enumerable {
		return nil, false
	}
	out := havocAssignments(slots)
	return append(out, assign(unknownEffect)...), true
}

/* ── the returned value's members ────────────────────────────────── */

// returnMemberStatements lowers a return whose value is the LITERAL the
// layout allocated member slots for: each member's own effect written
// into its own slot, then the scalar #ret written unknown and the flag
// raised.
//
// #ret stays UNKNOWN, and that is not a loss here. The object itself has
// no scalar spelling — it never had one — and the members now carry what
// the caller actually reads. A caller taking the direct apply route
// rebuilds the object from the member exits (applySummary); one taking
// the statement route keeps reading #ret and gets the same unknown it
// always got, so nothing that worked before reads differently.
//
// A MEMBER the effect grammar cannot spell takes unknown in ITS OWN slot
// rather than refusing the whole return — a partial object beats a whole
// unknown, and unknown in one member claims nothing about that member
// while the readable ones keep their values.
//
// What this does NOT do is run code. A member whose value expression
// would MOVE something — a call, a `new`, a write — is not lowered as an
// effect at all: the effect grammar has no statement position inside it,
// so those members take unknown and the statement's own mention havoc is
// what covers what they moved. A literal whose evaluation is not
// write-and-call free therefore declines back to the caller's routes,
// where the opaque return's havoc floor serves it exactly as before.
func returnMemberStatements(
	context *LoweringContext,
	returned *ast.Node,
	raise kernelbridge.IrStatement,
) ([]kernelbridge.IrStatement, bool) {
	if context == nil || context.Result == nil || returned == nil {
		return nil, false
	}
	if context.RetShape == RetShapeNone || len(context.RetMembers) == 0 {
		return nil, false
	}
	head := Unwrapped(returned)
	if head == nil {
		return nil, false
	}
	// the members' values are read as EFFECTS, which have no room for a
	// statement — so a literal that runs code keeps the floor that covers
	// what it ran
	if !writeAndCallFree(head) {
		return nil, false
	}
	switch context.RetShape {
	case RetShapeObject:
		if !ast.IsObjectLiteralExpression(head) {
			return nil, false
		}
		return objectReturnMemberStatements(context, head, raise), true
	case RetShapeArray:
		if !ast.IsArrayLiteralExpression(head) {
			return nil, false
		}
		return arrayReturnMemberStatements(context, head, raise), true
	}
	return nil, false
}

// objectReturnMemberStatements writes each key of a returned object
// literal into the slot the layout gave it.
//
// A key this literal does NOT spell is left alone: its slot keeps
// whatever the path it is on left there, which for a body's single return
// is the absent entry state — "this returned object has no such key" —
// and for one arm of a several-arm body is the join the exits carry.
// Writing absent here would say the same thing on the arms that omit the
// key; leaving it says it without an extra statement.
func objectReturnMemberStatements(
	context *LoweringContext,
	literal *ast.Node,
	raise kernelbridge.IrStatement,
) []kernelbridge.IrStatement {
	var out []kernelbridge.IrStatement
	written := map[int]struct{}{}
	for _, property := range literal.AsObjectLiteralExpression().Properties.Nodes {
		name, named := retMemberNameOf(property)
		if !named {
			continue
		}
		slot, held := RetMemberSlotOf(context, name)
		if !held {
			continue
		}
		value := retMemberValueOf(property)
		effect := unknownEffect
		if value != nil {
			// the member's own slot sort decides the reading, the way the
			// scalar ret's sort decides the whole-value one
			if read, ok := RhsEffect(context, context.Sorts[slot], value); ok {
				effect = read
			}
		}
		out = append(out, kernelbridge.IrStatement{
			Kind: kernelbridge.IrStatementAssign, Target: slot, Effect: effect,
		})
		written[slot] = struct{}{}
	}
	// a member slot ANOTHER arm spells and this one does not must not keep
	// a value this path never wrote — the slot is one binding across the
	// whole body, so a write on an earlier statement would otherwise be
	// read as this return's member. Absent is what this path says about a
	// key its literal has no property for.
	for _, entry := range context.RetMembers {
		if _, already := written[entry.Index]; already {
			continue
		}
		out = append(out, kernelbridge.IrStatement{
			Kind: kernelbridge.IrStatementAssign, Target: entry.Index, Effect: kernelbridge.AbsentConst(),
		})
	}
	// the object value itself has no scalar spelling; the members carry it
	out = append(out, kernelbridge.IrStatement{
		Kind: kernelbridge.IrStatementAssign, Target: context.Result.Ret, Effect: unknownEffect,
	})
	return append(out, raise)
}

// retMemberValueOf is the expression one literal property holds: the
// property assignment's initializer, or — for a shorthand — the name
// itself, which reads as the local of that name.
func retMemberValueOf(property *ast.Node) *ast.Node {
	if ast.IsPropertyAssignment(property) {
		return property.AsPropertyAssignment().Initializer
	}
	if ast.IsShorthandPropertyAssignment(property) {
		return property.AsShorthandPropertyAssignment().Name()
	}
	return nil
}

// arrayReturnMemberStatements writes the returned array literal's LENGTH
// — exact, the element count, since the shape reader refused every
// spread — and the JOIN of its elements into the ".elem" slot.
//
// The element slot takes the same WEAK UPDATE a flattened local array's
// element slot takes (ir_array_slots.go's convention): one slot stands
// for every position, so it must hold something true of them all. The
// join is built as a branch over the elements — each arm writing one
// element's effect — which is exactly how the exits join, so `[a, b]`
// leaves ".elem" holding a value true of both. An element the effect
// grammar cannot spell makes the whole join unknown: an arm claiming
// nothing joins to nothing.
func arrayReturnMemberStatements(
	context *LoweringContext,
	literal *ast.Node,
	raise kernelbridge.IrStatement,
) []kernelbridge.IrStatement {
	elements := literal.AsArrayLiteralExpression().Elements.Nodes
	var out []kernelbridge.IrStatement
	if lenSlot, held := RetMemberSlotOf(context, "len"); held {
		out = append(out, kernelbridge.IrStatement{
			Kind:   kernelbridge.IrStatementAssign,
			Target: lenSlot,
			Effect: kernelbridge.LoopEffect{
				Kind: kernelbridge.LoopEffectConst,
				Set:  refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{float64(len(elements))})),
			},
		})
	}
	if elemSlot, held := RetMemberSlotOf(context, "elem"); held {
		out = append(out, elementJoinAssignments(context, elemSlot, elements)...)
	}
	out = append(out, kernelbridge.IrStatement{
		Kind: kernelbridge.IrStatementAssign, Target: context.Result.Ret, Effect: unknownEffect,
	})
	return append(out, raise)
}

// elementJoinAssignments writes the JOIN of a literal's elements into one
// slot, as nested branches whose arms each write one element. The kernel
// joins branch arms by the proved exact join, so the slot ends holding a
// value true of every element — the weak update an array's one element
// slot needs.
//
// The branch is the BRANCH-BOTH shape (IrStatementBranchBoth): it tests
// nothing, walks both arms and joins them. An EMPTY literal writes
// absent — `[]` has no element, and absent is what a read of one would
// find.
func elementJoinAssignments(
	context *LoweringContext,
	elemSlot int,
	elements []*ast.Node,
) []kernelbridge.IrStatement {
	assign := func(effect kernelbridge.LoopEffect) kernelbridge.IrStatement {
		return kernelbridge.IrStatement{Kind: kernelbridge.IrStatementAssign, Target: elemSlot, Effect: effect}
	}
	if len(elements) == 0 {
		return []kernelbridge.IrStatement{assign(kernelbridge.AbsentConst())}
	}
	effectOf := func(element *ast.Node) kernelbridge.LoopEffect {
		if ast.IsOmittedExpression(element) {
			return kernelbridge.AbsentConst()
		}
		if read, ok := RhsEffect(context, context.Sorts[elemSlot], element); ok {
			return read
		}
		return unknownEffect
	}
	// one element: no join to build, the slot simply holds it
	out := []kernelbridge.IrStatement{assign(effectOf(elements[0]))}
	for _, element := range elements[1:] {
		// each further element joins in as the other arm of a condition
		// nothing reads — the kernel walks both and joins them, which is
		// the weak update this slot needs
		out = []kernelbridge.IrStatement{{
			Kind: kernelbridge.IrStatementBranchBoth,
			Then: out,
			Else: []kernelbridge.IrStatement{assign(effectOf(element))},
		}}
	}
	return out
}
