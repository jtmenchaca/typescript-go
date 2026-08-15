// split from ir_loop_stmts.go — the three-clause for: its condition
// read for inertness, its initializer as the prelude, its incrementor
// as the body's last statement

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

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
	// statement machinery — a declaration list by the declaration rule
	// (one assignment per declarator, in source order), a bare expression
	// by the assignment rule
	prelude, preludeOk := forInitializerAssignments(context, forStmt.Initializer)
	if !preludeOk {
		return nil, kernelbridge.IrStatement{}, false
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
	// the condition is tested AFTER the incrementor, so the state the
	// loop is left at is the state that failed it — the exit refinement
	// reads exactly right here. `for (;;)` has no condition and refines
	// by nothing, as it must.
	return prelude, loopStmtsWithHead(context, forStmt.Condition, body), true
}

// forInitializerAssignments is what a for's initializer clause writes,
// ahead of the loop — one assignment statement per declarator, in
// source order.
//
// `for (let i = 0, len = xs.length; …)` declares TWO names in one
// clause and both are ordinary declarators the single-declarator rule
// already reads; nothing about the clause makes the second one
// different from the first, and the runtime writes them left to right,
// which is the order they are emitted in. A clause holding no
// declaration at all (`for (i = 0; …)`) is an assigning expression and
// goes through the expression rule as before.
//
// All-or-nothing: one declarator no rule spells declines the whole
// route, exactly as a single unreadable declarator always did — the
// loop then falls to the havoc floor rather than running with an
// initializer half-written.
//
// A nil clause (`for (; i < n; i++)`) writes nothing and is not a
// refusal.
func forInitializerAssignments(
	context *LoweringContext, initializer *ast.Node,
) ([]kernelbridge.IrStatement, bool) {
	if initializer == nil {
		return nil, true
	}
	var written []AssignmentTarget
	if ast.IsVariableDeclarationList(initializer) {
		for _, declaration := range initializer.AsVariableDeclarationList().Declarations.Nodes {
			assignments, ok := declaratorAssignments(context, declaration)
			if !ok {
				return nil, false
			}
			written = append(written, assignments...)
		}
	} else {
		assignment, ok := AssignmentOfExpression(context, initializer)
		if !ok {
			return nil, false
		}
		written = append(written, assignment)
	}
	out := make([]kernelbridge.IrStatement, 0, len(written))
	for _, assignment := range written {
		out = append(out, kernelbridge.IrStatement{
			Kind:   kernelbridge.IrStatementAssign,
			Target: assignment.Target,
			Effect: assignment.Effect,
		})
	}
	return out, true
}
