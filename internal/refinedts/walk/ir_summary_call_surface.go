// split from ir_summary_call.go — the lowering-side statement surface and the assignment shape it reads

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// SummaryCallStatementOf is the lowering-side entry: a call expression
// STATEMENT (`f(…)`), or a call assigned into a tracked slot (`x =
// f(…)`, `const x = f(…)`), where the callee has a compiled summary —
// or, where the callee's own build is in flight, the havoc floor.
// Declines where the callee resolves to nothing, where an argument does
// not lower, or where an assignment's target has no slot — and the
// statement then takes the inlining route, exactly as before.
func SummaryCallStatementOf(context *LoweringContext, statement *ast.Node) ([]kernelbridge.IrStatement, bool) {
	// `f(…);` — the value goes nowhere, but the call still runs
	if ast.IsExpressionStatement(statement) {
		e := Unwrapped(statement.AsExpressionStatement().Expression)
		if ast.IsCallExpression(e) || ast.IsNewExpression(e) {
			return SummaryCallOrHavoc(context, e, -1)
		}
	}
	target, rhs, ok := callAssignmentShapeOf(context, statement)
	if !ok {
		// `const { a, b } = f(...)` where `f` has a COMPILED SUMMARY whose
		// returns carry a per-member RetShape — callAssignmentShapeOf only
		// ever finds a BARE identifier's own slot, so a binding-pattern
		// name declines it outright before summaryCallStatement is ever
		// reached. Tried first: a callee this lowering can read a summary
		// for gets its members threaded by NAME rather than only havocked.
		if destructured, destructuredOk := destructuredCallDeclarationStatement(context, statement); destructuredOk {
			return destructured, true
		}
		// `const center = f(...)` where `center` is a FLATTENED record
		// local (no scalar slot of its own, only leaf paths) — the shape
		// above only ever finds a BARE name's own slot. Tried here, once
		// the scalar and destructured routes have already declined: an
		// imported/free callee whose arguments prove write-and-call-free
		// still has a sound answer for a flattened target — havoc every
		// leaf — even though it has none for a scalar target's own single
		// slot spelling.
		if flattened, flattenedOk := importedHookFlattenedDeclarationStatement(context, statement); flattenedOk {
			return flattened, true
		}
		return nil, false
	}
	head := Unwrapped(rhs)
	if !ast.IsCallExpression(head) && !ast.IsNewExpression(head) {
		return nil, false
	}
	return SummaryCallOrHavoc(context, head, target)
}

// callAssignmentShapeOf is the `let x = e` / `x = e` shape both call
// routes read: the tracked target slot and the right side. Declines
// where the target has no slot.
func callAssignmentShapeOf(context *LoweringContext, statement *ast.Node) (target int, rhs *ast.Node, ok bool) {
	if ast.IsVariableStatement(statement) {
		declarations := statement.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes
		if len(declarations) != 1 {
			return 0, nil, false
		}
		d := declarations[0].AsVariableDeclaration()
		if !ast.IsIdentifier(d.Name()) || d.Initializer == nil {
			return 0, nil, false
		}
		index, found := IndexOf(context, d.Name())
		if !found {
			return 0, nil, false
		}
		return index, d.Initializer, true
	}
	if !ast.IsExpressionStatement(statement) {
		return 0, nil, false
	}
	e := Unwrapped(statement.AsExpressionStatement().Expression)
	if !ast.IsBinaryExpression(e) {
		return 0, nil, false
	}
	bin := e.AsBinaryExpression()
	if bin.OperatorToken.Kind != ast.KindEqualsToken {
		return 0, nil, false
	}
	index, found := IndexOf(context, bin.Left)
	if !found {
		return 0, nil, false
	}
	return index, bin.Right, true
}
