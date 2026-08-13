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
		// slot per key lowers as N ordinary assignments at the
		// declaration's position, in literal order. Tried ahead of the
		// single-name reader, which has no slot for `p` itself.
		if assignments, ok := ObjectDeclarationAssignmentsOf(context, s); ok {
			for _, assignment := range assignments {
				out = append(out, kernelbridge.IrStatement{Kind: kernelbridge.IrStatementAssign, Target: assignment.Target, Effect: assignment.Effect})
			}
			continue
		}
		if assignment, ok := AssignmentOf(context, s); ok {
			out = append(out, kernelbridge.IrStatement{Kind: kernelbridge.IrStatementAssign, Target: assignment.Target, Effect: assignment.Effect})
			continue
		}
		if viaCall, ok := CallAssignmentOf(context, s); ok {
			out = append(out, viaCall...)
			continue
		}
		if ast.IsIfStatement(s) {
			ifStmt := s.AsIfStatement()
			thn, thnOk := LowerStatements(context, StatementsOf(ifStmt.ThenStatement))
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
