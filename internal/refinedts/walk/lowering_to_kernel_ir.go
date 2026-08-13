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
// ir_loop.go. Call inlining: ir_inline_call.go.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// LowerStatements is lowerStatements in the TS source: statements
// lowered to the IR, or (nil, false) where any one declines.
func LowerStatements(context *LoweringContext, statements []*ast.Node) ([]kernelbridge.IrStatement, bool) {
	var out []kernelbridge.IrStatement
	for index := 0; index < len(statements); index++ {
		s := statements[index]
		// `return e`: the result slot takes e, the done flag raises, and
		// the rest of this block never runs — dead statements simply do
		// not lower. A bare `return` raises the flag alone; the result
		// slot keeps its absent entry state, which IS the undefined
		// return.
		if ast.IsReturnStatement(s) {
			if context.Result == nil {
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
					out = append(out, kernelbridge.IrStatement{Kind: kernelbridge.IrStatementAssign, Target: context.Result.Ret, Effect: effect})
					out = append(out, raise)
					return out, true
				}
				// `return f(…)`: the callee inlines and its result slot is
				// this return's value — absent where a path fell off, which
				// the copy carries
				head := Unwrapped(rs.Expression)
				if ast.IsCallExpression(head) {
					if inlined, ok := InlineCall(context, head); ok {
						out = append(out, inlined.Stmts...)
						out = append(out, kernelbridge.IrStatement{
							Kind:   kernelbridge.IrStatementAssign,
							Target: context.Result.Ret,
							Effect: kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectVar, Index: inlined.RetIndex},
						})
						out = append(out, raise)
						return out, true
					}
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
						out = append(out, guarded...)
						return out, true
					}
				}
				return nil, false
			}
			out = append(out, raise)
			return out, true
		}
		// `const p = { lo: 0, hi: n }` — a record local flattened into one
		// slot per LEAF lowers as N ordinary assignments at the
		// declaration's position, in literal order. Tried ahead of the
		// single-name reader, which has no slot for `p` itself.
		if assignments, ok := ObjectDeclarationAssignmentsOf(context, s); ok {
			out = append(out, assignsOf(assignments)...)
			continue
		}
		// `const a = [1, 2, 3]` — an array local flattened into its two
		// slots: the count and the join of the elements.
		if assignments, ok := ArrayDeclarationAssignmentsOf(context, s); ok {
			out = append(out, assignsOf(assignments)...)
			continue
		}
		// `const { x, y } = p` — a flattened record read leaf by leaf into
		// the destructured names.
		if assignments, ok := DestructuringAssignmentsOf(context, s); ok {
			out = append(out, assignsOf(assignments)...)
			continue
		}
		// `p = q` / `p = { … }` — a whole record written leaf for leaf.
		// Ahead of the single-name reader, which has no slot for `p`.
		if assignments, ok := RecordAssignmentOf(context, s); ok {
			out = append(out, assignsOf(assignments)...)
			continue
		}
		// `a.push(v)` — the length steps, the element slot joins.
		if assignments, ok := ArrayPushAssignmentsOf(context, s); ok {
			out = append(out, assignsOf(assignments)...)
			continue
		}
		// `a[i] = v` — the element slot joins; the length is untouched.
		if assignments, ok := ArrayIndexWriteOf(context, s); ok {
			out = append(out, assignsOf(assignments)...)
			continue
		}
		if assignment, ok := AssignmentOf(context, s); ok {
			out = append(out, kernelbridge.IrStatement{Kind: kernelbridge.IrStatementAssign, Target: assignment.Target, Effect: assignment.Effect})
			continue
		}
		// `f(…)` / `x = f(…)` where the callee has a compiled summary:
		// the call statement, applying the summary kernel-side. Tried
		// ahead of the inlining route, which lowers the callee's body
		// into fresh slots instead.
		if viaSummary, ok := SummaryCallStatementOf(context, s); ok {
			out = append(out, viaSummary...)
			continue
		}
		if viaCall, ok := CallAssignmentOf(context, s); ok {
			out = append(out, viaCall...)
			continue
		}
		// `for (const x of a)` over a flattened array — the loop whose
		// per-pass element effect is the element slot's var.
		if loop, ok := ArrayForOfLowering(context, s); ok {
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
			if !thnOk || !elsOk {
				return nil, false
			}
			// the guard composer reads the head: single tests, typeof
			// folds, `!`/`&&`/`||` nesting, and inlined call guards
			guarded, ok := LowerGuard(context, ifStmt.Expression, thn, els)
			if !ok {
				return nil, false
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
				return nil, false
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
			if !onceOk {
				return nil, false
			}
			head, headOk := LoopHeadOf(context, do.Expression)
			body, bodyOk := LoopBodyOf(context, do.Statement, nil)
			if !headOk || !bodyOk {
				return nil, false
			}
			// a once-through that RETURNS would make the loop's remainder
			// conditional on the done flag, which the loop form cannot
			// express — decline rather than walk a body the flag should
			// have skipped
			if context.Result != nil && RaisesDone(once, context.Result.Done) {
				return nil, false
			}
			out = append(out, once...)
			out = append(out, LoopStatement(context, head, body))
			continue
		}
		if ast.IsForStatement(s) {
			// for (init; cond; step) body — the init runs before the loop,
			// the step folds as the body's final statement
			forStmt := s.AsForStatement()
			if forStmt.Condition == nil {
				return nil, false
			}
			head, headOk := LoopHeadOf(context, forStmt.Condition)
			body, bodyOk := LoopBodyOf(context, forStmt.Statement, forStmt.Incrementor)
			if !headOk || !bodyOk {
				return nil, false
			}
			if forStmt.Initializer != nil {
				var init AssignmentTarget
				var initOk bool
				if ast.IsVariableDeclarationList(forStmt.Initializer) {
					init, initOk = DeclarationAssignment(context, forStmt.Initializer.AsVariableDeclarationList().Declarations.Nodes)
				} else {
					init, initOk = AssignmentOfExpression(context, forStmt.Initializer)
				}
				if !initOk {
					return nil, false
				}
				out = append(out, kernelbridge.IrStatement{Kind: kernelbridge.IrStatementAssign, Target: init.Target, Effect: init.Effect})
			}
			out = append(out, LoopStatement(context, head, body))
			continue
		}
		return nil, false
	}
	return out, true
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
