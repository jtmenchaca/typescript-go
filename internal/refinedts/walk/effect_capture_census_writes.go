// split from effect_capture_census.go — the write arms of the walk

package walk

import "github.com/microsoft/typescript-go/internal/ast"

// capturedWriteRefuses runs the write arms over one node — an
// assignment, an increment or decrement, and a `delete` — and reports
// whether the target moves a place no row spells. A served member
// write is noted on the object capture on the way through.
func (census *captureCensus) capturedWriteRefuses(node *ast.Node) bool {
	if ast.IsBinaryExpression(node) {
		binary := node.AsBinaryExpression()
		operator := binary.OperatorToken.Kind
		if operator >= ast.KindFirstAssignment && operator <= ast.KindLastAssignment {
			if _, refuses := census.capturedMemberWrite(binary.Left, false); refuses {
				return true
			}
		}
	}
	if ast.IsPrefixUnaryExpression(node) {
		unary := node.AsPrefixUnaryExpression()
		if unary.Operator == ast.KindPlusPlusToken || unary.Operator == ast.KindMinusMinusToken {
			if _, refuses := census.capturedMemberWrite(unary.Operand, false); refuses {
				return true
			}
		}
	}
	if ast.IsPostfixUnaryExpression(node) {
		unary := node.AsPostfixUnaryExpression()
		if unary.Operator == ast.KindPlusPlusToken || unary.Operator == ast.KindMinusMinusToken {
			if _, refuses := census.capturedMemberWrite(unary.Operand, false); refuses {
				return true
			}
		}
	}
	if ast.IsDeleteExpression(node) {
		if _, refuses := census.capturedMemberWrite(node.AsDeleteExpression().Expression, true); refuses {
			return true
		}
	}
	return false
}
