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
//
// This file is the dispatcher alone. The routes it dispatches to live
// beside it: lowering_to_kernel_ir_return.go (the return statement),
// _return_branch.go and _return_arm.go (the branch-shaped return),
// _return_members.go (the returned value's member slots),
// _throw.go (throw coverage), _flattening.go (the declaration,
// record, collection and call routes), _condition.go (the if route
// and the condition hoist), _loops.go (while, do-while, for),
// _switch.go and _switch_labels.go (switch lowering).

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
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
				havoc, havocOk := havocFloorStatements(context, s)
				if !havocOk {
					return nil, false
				}
				out = append(out, havoc...)
				continue
			}
		}
		// `return e` — the whole statement, and the whole block: the route
		// ends the list on every path (lowering_to_kernel_ir_return.go).
		if ast.IsReturnStatement(s) {
			return lowerReturnStatement(context, s, out, mark)
		}
		// `throw e` — the escaping throw, which ends the list the same way
		// (lowering_to_kernel_ir_throw.go).
		if ast.IsThrowStatement(s) {
			return lowerThrowStatement(context, s, out, mark)
		}
		// THE STRAIGHT-LINE ROUTES, in the order they were always tried in:
		// the flattened declarations and destructurings, the record,
		// array and collection writes, the ordinary and chained
		// assignments, the setter write, the await forms, and the call
		// statements (lowering_to_kernel_ir_flattening.go).
		lowered, handled := lowerFlatteningRoutes(context, s, out, mark)
		out = lowered
		if handled {
			continue
		}
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
			// a HEAD THAT RAN — `switch (await f())` — hoisted its call to a
			// temp, and that call statement goes out ahead of the chain that
			// followed it, which is where the source runs it
			out = flush(out)
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
		// `if (c) { … } else { … }` — the guard, the arms, and the
		// remainder gated on a returning arm
		// (lowering_to_kernel_ir_condition.go).
		if ast.IsIfStatement(s) {
			appended, consumed, ifOk := lowerIfStatement(context, s, out, statements, index, mark)
			if !ifOk {
				return nil, false
			}
			out = appended
			if consumed {
				return out, true
			}
			continue
		}
		// the three loop heads — `while`, `do … while`, `for` — each with
		// the statement-bodied form and then the floor behind it
		// (lowering_to_kernel_ir_loops.go).
		if ast.IsWhileStatement(s) {
			appended, consumed, whileOk := lowerWhileStatement(context, s, out, statements, index)
			if !whileOk {
				return nil, false
			}
			out = appended
			if consumed {
				return out, true
			}
			continue
		}
		if ast.IsDoStatement(s) {
			appended, consumed, doOk := lowerDoStatement(context, s, out, statements, index)
			if !doOk {
				return nil, false
			}
			out = appended
			if consumed {
				return out, true
			}
			continue
		}
		if ast.IsForStatement(s) {
			appended, consumed, forOk := lowerForStatement(context, s, out, statements, index)
			if !forOk {
				return nil, false
			}
			out = appended
			if consumed {
				return out, true
			}
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
		havoc, havocOk := havocFloorStatements(context, s)
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

// havocFloorStatements is the LAST resort every composite route above falls
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
func havocFloorStatements(context *LoweringContext, s *ast.Node) ([]kernelbridge.IrStatement, bool) {
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
