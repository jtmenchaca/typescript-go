// The STATEMENT-BODIED loop: every loop the effect-bodied route
// declines, lowered as its body's own statements under the kernel's
// `loopStmts` form rather than dropped to the havoc floor.
//
// What the two forms claim, and why this one is weaker but still
// worth having:
//
//	the effect-bodied loop (ir_loop.go) reads a head comparison and
//	  folds the body into one effect per binding, and the kernel's
//	  solver then CERTIFIES an invariant — `let i = 0; while (i < 10)
//	  i++` exits with i in {10}. That is the precise answer and it
//	  keeps its priority everywhere below.
//	this one reads NO condition at all. Any number of trips may run,
//	  zero included, and the kernel havocs the body's own write set —
//	  which it computes from the body statements itself and never
//	  trusts from the wire — leaving every other slot's knowledge
//	  intact.
//
// The floor it replaces havocked the union of the head's and the
// body's MENTIONS: every leaf of every flattened local named anywhere
// in the loop, whether written or only read, plus the body's own
// targets. This form havocs the body's WRITES. So a loop that reads a
// record and writes one counter now keeps the record.
//
// THE HEAD MUST MOVE NOTHING. No condition is read, so any state the
// head moves would simply be lost: `while (m.has(k++))` steps k on
// every trip and this form would walk the loop as though it had not.
// So the while/do condition, the for's condition, and the for-of/for-in
// iterable each pass writeAndCallFree, and a head that does not
// declines the route back to the floor.
//
// The for's INITIALIZER and INCREMENTOR do move state and are lowered
// rather than refused: the initializer runs ONCE before the loop, so it
// rides back as a PRELUDE the caller appends ahead of the loop
// statement; the incrementor runs at the end of every trip, so it is
// appended INTO the body statements. That is exactly what a trip is —
// body, then step.
//
// A for-of/for-in BINDING (`for (const x of xs)`) assigns its bound
// names UNKNOWN at the top of the body: the iterated value has no
// spelling here, and unknown is what is true of it. An EXPRESSION
// binding (`for (this.x of xs)`) goes through the ordinary assignment
// machinery where it resolves as a target, and declines where it does
// not — the loop would otherwise write a slot nothing recorded.
//
// THE TRANSFERS ARE THE ONE REFUSAL. `break` and `continue` have no
// reading in the ordinary statement walk — each falls to the havoc
// floor, which writes the slots the statement could move and then FALLS
// THROUGH. Inside a loop body that is exactly wrong for a transfer that
// LEAVES the loop:
//
//	`while (c) { … break outer; … }` — the run departs for a label
//	  outside this loop, and a body that swallowed the break would have
//	  the kernel walk the rest of the trip and then the statements after
//	  the loop as though control had never left. That is a WRONG claim
//	  about where the run went, not a weak one.
//
// A transfer that stays INSIDE the loop is a different thing and is
// admitted: `break` and `continue` targeting this loop only change the
// TRIP COUNT, and this form already claims nothing about the trip count
// — any number of trips, zero included. Whatever prefix of the body a
// trip ran, the kernel's havoc of the body's write set covers it.
//
// So the gate is containment, decided by the havoc floor's own
// containedTransfer against THIS loop statement, and a transfer that
// crosses out declines the route back to the floor — which refuses it
// too, and the body then declines naming the construct, exactly as it
// did before this form existed.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// LowerLoopStatements is the statement-bodied loop for while,
// do-while, for, for-of and for-in.
//
// The prelude is the statements that must be emitted BEFORE the loop —
// today the for's initializer alone, which runs once. The caller
// appends the prelude first and the loop statement after; both or
// neither, since a declining route answers ok=false with nothing built.
//
// Declines (nil, zero, false) where the head moves state, where any
// body statement does not lower, where the initializer or incrementor
// does not lower, or where a for-of/for-in binding names nothing this
// lowering can write.
func LowerLoopStatements(
	context *LoweringContext, statement *ast.Node,
) (prelude []kernelbridge.IrStatement, loop kernelbridge.IrStatement, ok bool) {
	if context == nil || statement == nil {
		return nil, kernelbridge.IrStatement{}, false
	}
	// a transfer that leaves THIS loop is the one refusal — the reasoning
	// is in the file header, and the containment test is the havoc
	// floor's own
	if !transfersStayInside(statement) {
		return nil, kernelbridge.IrStatement{}, false
	}
	switch {
	case ast.IsWhileStatement(statement):
		while := statement.AsWhileStatement()
		if !writeAndCallFree(while.Expression) {
			return nil, kernelbridge.IrStatement{}, false
		}
		body, bodyOk := LowerStatements(context, StatementsOf(while.Statement))
		if !bodyOk {
			return nil, kernelbridge.IrStatement{}, false
		}
		return nil, loopStmtsStatement(body), true
	case ast.IsDoStatement(statement):
		// `do body while (cond)` runs the body at least once and this form
		// admits ANY trip count including zero, so the same statement
		// covers it: the zero-trip case the kernel also admits is a state
		// the real run cannot reach, which is a weaker claim, never a
		// wrong one. (The exact once-then-loop composition stays in the
		// effect-bodied route, which keeps its priority.)
		do := statement.AsDoStatement()
		if !writeAndCallFree(do.Expression) {
			return nil, kernelbridge.IrStatement{}, false
		}
		body, bodyOk := LowerStatements(context, StatementsOf(do.Statement))
		if !bodyOk {
			return nil, kernelbridge.IrStatement{}, false
		}
		return nil, loopStmtsStatement(body), true
	case ast.IsForStatement(statement):
		return lowerForStatements(context, statement)
	case ast.IsForOfStatement(statement), ast.IsForInStatement(statement):
		return lowerForInOrOfStatements(context, statement)
	}
	return nil, kernelbridge.IrStatement{}, false
}

// lowerForStatements is `for (init; cond; step) body`: the init before
// the loop, the condition read for nothing but its inertness, and the
// step as the body's last statement.
//
// `for (;;)` — no condition at all — is admitted here, unlike the
// effect-bodied route which needs a head to solve against. Nothing is
// claimed about the trip count either way.
func lowerForStatements(
	context *LoweringContext, statement *ast.Node,
) ([]kernelbridge.IrStatement, kernelbridge.IrStatement, bool) {
	forStmt := statement.AsForStatement()
	// the CONDITION is read for nothing, so it must cost nothing: a head
	// that assigns or calls would move state this form never walks
	if forStmt.Condition != nil && !writeAndCallFree(forStmt.Condition) {
		return nil, kernelbridge.IrStatement{}, false
	}
	// the INITIALIZER runs once, ahead of the loop, through the ordinary
	// statement machinery — a declaration list by the declaration rule, a
	// bare expression by the assignment rule
	var prelude []kernelbridge.IrStatement
	if forStmt.Initializer != nil {
		var init AssignmentTarget
		initOk := false
		if ast.IsVariableDeclarationList(forStmt.Initializer) {
			init, initOk = DeclarationAssignment(
				context, forStmt.Initializer.AsVariableDeclarationList().Declarations.Nodes)
		} else {
			init, initOk = AssignmentOfExpression(context, forStmt.Initializer)
		}
		if !initOk {
			return nil, kernelbridge.IrStatement{}, false
		}
		prelude = append(prelude, kernelbridge.IrStatement{
			Kind:   kernelbridge.IrStatementAssign,
			Target: init.Target,
			Effect: init.Effect,
		})
	}
	body, bodyOk := LowerStatements(context, StatementsOf(forStmt.Statement))
	if !bodyOk {
		return nil, kernelbridge.IrStatement{}, false
	}
	// the INCREMENTOR ends every trip, so it is the body's final
	// statement — that is what a trip IS, body then step
	if forStmt.Incrementor != nil {
		step, stepOk := AssignmentOfExpression(context, forStmt.Incrementor)
		if !stepOk {
			return nil, kernelbridge.IrStatement{}, false
		}
		body = append(body, kernelbridge.IrStatement{
			Kind:   kernelbridge.IrStatementAssign,
			Target: step.Target,
			Effect: step.Effect,
		})
	}
	return prelude, loopStmtsStatement(body), true
}

// lowerForInOrOfStatements is `for (x of xs)` and `for (k in o)`: the
// binding written unknown at the top of every trip, then the body.
//
// `for await (const x of xs)` is admitted too. The awaited value has no
// spelling the slots carry, and unknown is exactly what this form
// writes for the bound name either way — the await changes what the
// value IS, not what is known about it.
func lowerForInOrOfStatements(
	context *LoweringContext, statement *ast.Node,
) ([]kernelbridge.IrStatement, kernelbridge.IrStatement, bool) {
	forInOrOf := statement.AsForInOrOfStatement()
	// the ITERABLE is evaluated once, before the first trip, and this
	// form reads nothing of it — so it must move nothing
	if !writeAndCallFree(forInOrOf.Expression) {
		return nil, kernelbridge.IrStatement{}, false
	}
	binding, bindingOk := forOfBindingAssignments(context, forInOrOf.Initializer)
	if !bindingOk {
		return nil, kernelbridge.IrStatement{}, false
	}
	body, bodyOk := LowerStatements(context, StatementsOf(forInOrOf.Statement))
	if !bodyOk {
		return nil, kernelbridge.IrStatement{}, false
	}
	// the bound names take their per-trip value FIRST: the body reads
	// them, so the write has to stand ahead of it
	return nil, loopStmtsStatement(append(binding, body...)), true
}

// forOfBindingAssignments is what one trip writes for a for-of/for-in
// head, ahead of the body.
//
// A DECLARATION head (`for (const x of xs)`, `for (const [k, v] of m)`)
// writes UNKNOWN into every bound name that has a slot: the iterated
// value has no spelling here, and unknown claims nothing about it. A
// bound name with NO slot needs no write — nothing lowered can read it
// later either.
//
// An EXPRESSION head (`for (this.x of xs)`) writes through the ordinary
// assignment target resolution, and declines where the target does not
// resolve: the real run writes that place on every trip, and a loop
// that skipped the write would leave the walk claiming an old value.
func forOfBindingAssignments(
	context *LoweringContext, initializer *ast.Node,
) ([]kernelbridge.IrStatement, bool) {
	if initializer == nil {
		return nil, false
	}
	if ast.IsVariableDeclarationList(initializer) {
		declarations := initializer.AsVariableDeclarationList().Declarations.Nodes
		if len(declarations) != 1 {
			return nil, false
		}
		name := declarations[0].AsVariableDeclaration().Name()
		if name == nil {
			return nil, false
		}
		var spelled []string
		switch {
		case ast.IsIdentifier(name):
			spelled = []string{name.Text()}
		case ast.IsObjectBindingPattern(name), ast.IsArrayBindingPattern(name):
			spelled = boundPatternNames(name)
		default:
			return nil, false
		}
		slots := map[int]struct{}{}
		for _, text := range spelled {
			if slot, found := slotIndexOfName(context, text); found {
				slots[slot] = struct{}{}
			}
			// a flattened local bound by the head — its leaves move too
			for _, leaf := range flattenedSlotsUnder(context, text) {
				if leaf >= 0 {
					slots[leaf] = struct{}{}
				}
			}
		}
		return havocAssignments(slots), true
	}
	// the expression head: `for (this.x of xs)`, `for (a[i] of xs)`
	slot, tracked := IndexOf(context, Unwrapped(initializer))
	if !tracked {
		return nil, false
	}
	return []kernelbridge.IrStatement{{
		Kind:   kernelbridge.IrStatementAssign,
		Target: slot,
		Effect: unknownEffect,
	}}, true
}

// transfersStayInside is whether every `break` and `continue` in a
// loop's subtree targets something INSIDE that subtree — the one thing
// this form must be right about, since the ordinary statement walk has
// no reading for either and lets both fall through.
//
// The containment test is havoc_floor's containedTransfer, unchanged and
// against this loop STATEMENT as the root, so the two routes can never
// disagree about which transfers leave: a bare `break` finds this loop
// (or a switch or loop nested in its body) as its target and is
// contained; a `break outer` finds its labelled statement only if that
// label sits inside the subtree.
//
// A nested FUNCTION's transfers are its own business — a `break` inside
// a callback leaves the callback's loop, not this one — so the scan
// stops at a function or class boundary, exactly as the floor's does.
func transfersStayInside(statement *ast.Node) bool {
	inside := true
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if !inside {
			return true
		}
		if ast.IsBreakStatement(node) || ast.IsContinueStatement(node) {
			if !containedTransfer(node, statement) {
				inside = false
			}
			return true
		}
		if ast.IsFunctionLike(node) || ast.IsClassLike(node) {
			return false
		}
		node.ForEachChild(visit)
		return false
	}
	visit(statement)
	return inside
}

// loopStmtsStatement wraps a lowered body as the kernel's
// statement-bodied loop. Stmts is the whole carrying field: no Written,
// no Cond, no After, no CondCmp — the kernel reads the write set off
// the statements itself.
func loopStmtsStatement(body []kernelbridge.IrStatement) kernelbridge.IrStatement {
	return kernelbridge.IrStatement{
		Kind:  kernelbridge.IrStatementLoopStmts,
		Stmts: body,
	}
}
