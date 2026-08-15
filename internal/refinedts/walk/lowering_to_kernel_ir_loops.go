// split from lowering_to_kernel_ir.go — the while, do-while and for routes

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// lowerWhileStatement is the walk's `while` route, exactly as it stood
// inside lowerStatementList: the solver's head-and-body form first, the
// statement-bodied loop where either half declines, and the havoc floor
// where that declines too.
//
// The three answers are the walk's own three — (out, false, true) to carry
// on with the next statement, (out, true, true) where the block's remainder
// was consumed, (nil, false, false) where the body declines.
func lowerWhileStatement(
	context *LoweringContext,
	s *ast.Node,
	out []kernelbridge.IrStatement,
	statements []*ast.Node,
	index int,
) ([]kernelbridge.IrStatement, bool, bool) {
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
				return nil, false, false
			}
			out = appended
			if consumed {
				return out, true, true
			}
			return out, false, true
		}
		// and where THAT declines too — a head that moves state, a body
		// statement nothing read — the whole loop havocs the union of
		// what its head and body could write, ONCE. A loop is its
		// statements repeated and havoc is idempotent — writing unknown
		// into a slot twice leaves the same state — so one pass covers
		// every trip count, the zero-trip case included.
		havoc, havocOk := havocFloorStatements(context, s)
		if !havocOk {
			return nil, false, false
		}
		out = append(out, havoc...)
		return out, false, true
	}
	out = append(out, LoopStatement(context, head, body))
	return out, false, true
}

// lowerDoStatement is the walk's `do … while` route, exactly as it stood
// inside lowerStatementList.
//
// `do body while (cond)` is the body ONCE, then the ordinary
// while loop — the first pass runs unconditionally, and every
// later pass is exactly what `while (cond) body` does. Both
// halves are the proved forms already: the once-through is
// ordinary statement lowering, the remainder the loop
// statement. No kernel form is added.
func lowerDoStatement(
	context *LoweringContext,
	s *ast.Node,
	out []kernelbridge.IrStatement,
	statements []*ast.Node,
	index int,
) ([]kernelbridge.IrStatement, bool, bool) {
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
				return nil, false, false
			}
			out = appended
			if consumed {
				return out, true, true
			}
			return out, false, true
		}
		havoc, havocOk := havocFloorStatements(context, s)
		if !havocOk {
			return nil, false, false
		}
		out = append(out, havoc...)
		return out, false, true
	}
	out = append(out, once...)
	out = append(out, LoopStatement(context, head, body))
	return out, false, true
}

// lowerForStatement is the walk's `for` route, exactly as it stood inside
// lowerStatementList.
//
// for (init; cond; step) body — the init runs before the loop,
// the step folds as the body's final statement
func lowerForStatement(
	context *LoweringContext,
	s *ast.Node,
	out []kernelbridge.IrStatement,
	statements []*ast.Node,
	index int,
) ([]kernelbridge.IrStatement, bool, bool) {
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
				return nil, false, false
			}
			out = appended
			if consumed {
				return out, true, true
			}
			return out, false, true
		}
		// and where that declines too — an init the assignment grammar
		// refuses, a head that moves state — the whole for havocs the
		// union of what its three clauses and its body could write,
		// once — idempotent, so one pass covers every trip count
		// including zero
		havoc, havocOk := havocFloorStatements(context, s)
		if !havocOk {
			return nil, false, false
		}
		out = append(out, havoc...)
		return out, false, true
	}
	out = append(out, init...)
	out = append(out, LoopStatement(context, head, body))
	return out, false, true
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
