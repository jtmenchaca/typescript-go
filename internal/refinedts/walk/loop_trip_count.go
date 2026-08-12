// from control_flow/loop_trip_count.ts
//
// Literal trip counts and push-only argument collection for loops
// whose body only ever pushes onto a named array.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/jsnum"
)

// LiteralTripCount is literalTripCount in the TS source: the exact
// iteration count of `for (let i = A; i < B; i++)` with numeric
// literals, a unit increment, an index the body never writes, and no
// break, continue, or return — (0, false) anywhere the count is not
// pinned by the syntax alone.
func LiteralTripCount(loop *ast.Node) (int, bool) {
	if !ast.IsForStatement(loop) {
		return 0, false
	}
	forStmt := loop.AsForStatement()
	initializer := forStmt.Initializer
	if initializer == nil || !ast.IsVariableDeclarationList(initializer) {
		return 0, false
	}
	declarations := initializer.AsVariableDeclarationList().Declarations.Nodes
	if len(declarations) != 1 {
		return 0, false
	}
	declaration := declarations[0].AsVariableDeclaration()
	if !ast.IsIdentifier(declaration.Name()) || declaration.Initializer == nil || !ast.IsNumericLiteral(declaration.Initializer) {
		return 0, false
	}
	index := declaration.Name().Text()
	start := float64(jsnum.FromString(declaration.Initializer.AsNumericLiteral().Text))
	condition := forStmt.Condition
	if condition == nil || !ast.IsBinaryExpression(condition) {
		return 0, false
	}
	cond := condition.AsBinaryExpression()
	if !ast.IsIdentifier(cond.Left) || cond.Left.Text() != index || !ast.IsNumericLiteral(cond.Right) {
		return 0, false
	}
	bound := float64(jsnum.FromString(cond.Right.AsNumericLiteral().Text))
	strict := cond.OperatorToken.Kind == ast.KindLessThanToken
	if !strict && cond.OperatorToken.Kind != ast.KindLessThanEqualsToken {
		return 0, false
	}
	incrementor := forStmt.Incrementor
	unitStep := false
	if incrementor != nil {
		if ast.IsPostfixUnaryExpression(incrementor) {
			unary := incrementor.AsPostfixUnaryExpression()
			unitStep = unary.Operator == ast.KindPlusPlusToken && ast.IsIdentifier(unary.Operand) && unary.Operand.Text() == index
		} else if ast.IsPrefixUnaryExpression(incrementor) {
			unary := incrementor.AsPrefixUnaryExpression()
			unitStep = unary.Operator == ast.KindPlusPlusToken && ast.IsIdentifier(unary.Operand) && unary.Operand.Text() == index
		}
	}
	if !unitStep {
		return 0, false
	}
	if start != float64(int(start)) || bound != float64(int(bound)) {
		return 0, false
	}
	escapes := false
	var scan func(node *ast.Node)
	scan = func(node *ast.Node) {
		if escapes {
			return
		}
		if ast.IsBreakStatement(node) || ast.IsContinueStatement(node) || ast.IsReturnStatement(node) {
			escapes = true
		}
		if ast.IsBinaryExpression(node) {
			bin := node.AsBinaryExpression()
			if bin.OperatorToken.Kind >= ast.KindFirstAssignment && bin.OperatorToken.Kind <= ast.KindLastAssignment &&
				ast.IsIdentifier(bin.Left) && bin.Left.Text() == index {
				escapes = true
			}
		}
		if ast.IsPostfixUnaryExpression(node) {
			unary := node.AsPostfixUnaryExpression()
			if node != incrementor && ast.IsIdentifier(unary.Operand) && unary.Operand.Text() == index {
				escapes = true
			}
		}
		if ast.IsPrefixUnaryExpression(node) {
			unary := node.AsPrefixUnaryExpression()
			if node != incrementor && ast.IsIdentifier(unary.Operand) && unary.Operand.Text() == index {
				escapes = true
			}
		}
		node.ForEachChild(func(child *ast.Node) bool {
			scan(child)
			return false
		})
	}
	scan(forStmt.Statement)
	if escapes {
		return 0, false
	}
	count := bound - start
	if !strict {
		count++
	}
	if count < 0 {
		count = 0
	}
	return int(count), true
}

// TopLevelPushArguments is topLevelPushArguments in the TS source:
// how many elements one iteration pushes when EVERY push sits
// unconditionally at the body's top level — (0, false) when any push
// hides deeper (a conditional push breaks the count).
func TopLevelPushArguments(loop *ast.Node, name string) (int, bool) {
	var body *ast.Node
	if ast.IsForStatement(loop) {
		body = loop.AsForStatement().Statement
	}
	if body == nil || !ast.IsBlock(body) {
		return 0, false
	}
	count := 0
	for _, statement := range body.AsBlock().Statements.Nodes {
		if !ast.IsExpressionStatement(statement) {
			continue
		}
		e := statement.AsExpressionStatement().Expression
		if !ast.IsCallExpression(e) {
			continue
		}
		call := e.AsCallExpression()
		if !ast.IsPropertyAccessExpression(call.Expression) {
			continue
		}
		access := call.Expression.AsPropertyAccessExpression()
		if !ast.IsIdentifier(access.Expression) || access.Expression.Text() != name || access.Name().Text() != "push" {
			continue
		}
		if call.Arguments != nil {
			count += len(call.Arguments.Nodes)
		}
	}
	if count == 0 {
		return 0, false
	}
	return count, true
}

// PushOnlyArguments is pushOnlyArguments in the TS source: the
// arguments of every `name.push(...)` in the loop, or (nil, false)
// when the name appears ANYWHERE else inside it — an assignment,
// another method, a read, an argument — since then pushing is not
// the whole story of the binding.
func PushOnlyArguments(loop *ast.Node, name string) ([]*ast.Node, bool) {
	var collected []*ast.Node
	disqualified := false
	var visit func(node *ast.Node)
	visit = func(node *ast.Node) {
		if disqualified {
			return
		}
		if ast.IsIdentifier(node) && node.Text() == name {
			access := node.Parent
			var call *ast.Node
			if access != nil {
				call = access.Parent
			}
			if access != nil && ast.IsPropertyAccessExpression(access) &&
				access.AsPropertyAccessExpression().Expression == node &&
				access.AsPropertyAccessExpression().Name().Text() == "push" &&
				call != nil && ast.IsCallExpression(call) &&
				call.AsCallExpression().Expression == access {
				if call.AsCallExpression().Arguments != nil {
					collected = append(collected, call.AsCallExpression().Arguments.Nodes...)
				}
			} else {
				disqualified = true
			}
			return
		}
		node.ForEachChild(func(child *ast.Node) bool {
			visit(child)
			return false
		})
	}
	visit(loop)
	if disqualified {
		return nil, false
	}
	return collected, true
}
