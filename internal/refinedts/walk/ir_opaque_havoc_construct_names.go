// split from ir_opaque_havoc.go — naming the construct

package walk

import (
	"strconv"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
)

/* ── naming the construct ────────────────────────────────────────── */

// havocCallName spells a havocked CALL for the outcome report: the
// callee's own spelling where it has one ("call fetch",
// "call this.injector.load"), and the bare word otherwise.
func havocCallName(call *ast.Node) string {
	callee := Unwrapped(call.AsCallExpression().Expression)
	if spelled, ok := calleeSpelling(callee); ok {
		return "call " + spelled
	}
	return "call"
}

// calleeSpelling reads a callee expression as a dotted path — `f`,
// `o.m`, `this.injector.load` — for the report. Anything else (a
// computed member, a call's result, a parenthesized function) has no
// one spelling.
func calleeSpelling(node *ast.Node) (string, bool) {
	var steps []string
	current := node
	for ast.IsPropertyAccessExpression(current) {
		access := current.AsPropertyAccessExpression()
		if !ast.IsIdentifier(access.Name()) {
			return "", false
		}
		steps = append(steps, access.Name().Text())
		current = Unwrapped(access.Expression)
	}
	switch {
	case ast.IsIdentifier(current):
		steps = append(steps, current.Text())
	case current.Kind == ast.KindThisKeyword:
		steps = append(steps, "this")
	default:
		return "", false
	}
	for left, right := 0, len(steps)-1; left < right; left, right = left+1, right-1 {
		steps[left], steps[right] = steps[right], steps[left]
	}
	return strings.Join(steps, "."), true
}

// havocConstructName spells a havocked STATEMENT for the outcome report.
// The words are the source construct's own, not a category: the report
// says "for-in" where a for-in havocked, so a reader can go to the
// syntax the coverage was lost at.
func havocConstructName(statement *ast.Node) string {
	switch {
	case ast.IsForInStatement(statement):
		return "for-in"
	case ast.IsForOfStatement(statement):
		if statement.AsForInOrOfStatement().AwaitModifier != nil {
			return "for await"
		}
		return "for-of"
	case ast.IsForStatement(statement):
		return "for"
	case ast.IsWhileStatement(statement):
		return "while"
	case ast.IsDoStatement(statement):
		return "do-while"
	case ast.IsTryStatement(statement):
		return "try"
	case ast.IsSwitchStatement(statement):
		return "switch"
	case ast.IsIfStatement(statement):
		return "if"
	case ast.IsVariableStatement(statement):
		return "declaration"
	case ast.IsLabeledStatement(statement):
		return "labeled statement"
	case ast.IsThrowStatement(statement):
		return "throw"
	case ast.IsReturnStatement(statement):
		return OpaqueReturnName(statement)
	case ast.IsExpressionStatement(statement):
		return havocExpressionName(Unwrapped(statement.AsExpressionStatement().Expression))
	case ast.IsBlock(statement):
		return "block"
	case ast.IsBreakStatement(statement):
		return "break"
	case ast.IsContinueStatement(statement):
		return "continue"
	case ast.IsEmptyStatement(statement):
		return "empty statement"
	case ast.IsDebuggerStatement(statement):
		return "debugger statement"
	case ast.IsFunctionDeclaration(statement):
		return "function declaration"
	case ast.IsClassDeclaration(statement):
		return "class declaration"
	case ast.IsWithStatement(statement):
		return "with statement"
	}
	// no arm matched: carry the syntax KIND's own number rather than the
	// bare word "statement". The number is not a construct name, but it
	// separates the rows and points at the exact ast.Kind to add an arm
	// for above — which is what a reader needs to turn the row into one.
	return "statement kind " + strconv.Itoa(int(statement.Kind))
}
