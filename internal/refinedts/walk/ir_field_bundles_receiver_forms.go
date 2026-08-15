// split from ir_field_bundles.go — the receiver forms and what they consume

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
)

// receiverMethodBindOf recognizes `<receiver>.<m>.bind(<receiver>)` —
// the deferred method call. The callee must be a plain two-step access
// ending in `bind`, no optional steps, and the FIRST argument must be
// the receiver itself; extra arguments (partial application) are
// allowed and walk as ordinary expressions.
func receiverMethodBindOf(node *ast.Node, isReceiver func(*ast.Node) bool) (string, bool) {
	if node == nil || !ast.IsCallExpression(node) {
		return "", false
	}
	call := node.AsCallExpression()
	if call.QuestionDotToken != nil || call.Arguments == nil || len(call.Arguments.Nodes) == 0 {
		return "", false
	}
	if !isReceiver(Unwrapped(call.Arguments.Nodes[0])) {
		return "", false
	}
	outer := Unwrapped(call.Expression)
	if !ast.IsPropertyAccessExpression(outer) {
		return "", false
	}
	outerAccess := outer.AsPropertyAccessExpression()
	if outerAccess.QuestionDotToken != nil || !ast.IsIdentifier(outerAccess.Name()) ||
		outerAccess.Name().Text() != "bind" {
		return "", false
	}
	inner := Unwrapped(outerAccess.Expression)
	if !ast.IsPropertyAccessExpression(inner) {
		return "", false
	}
	innerAccess := inner.AsPropertyAccessExpression()
	if innerAccess.QuestionDotToken != nil || !ast.IsIdentifier(innerAccess.Name()) ||
		!isReceiver(Unwrapped(innerAccess.Expression)) {
		return "", false
	}
	return innerAccess.Name().Text(), true
}

// consumeBindMentions marks a recognized bind's receiver spellings as
// accounted for: the method access chain and the first argument.
func consumeBindMentions(consumed map[*ast.Node]struct{}, node *ast.Node) {
	call := node.AsCallExpression()
	consumed[call.Arguments.Nodes[0]] = struct{}{}
	consumed[Unwrapped(call.Arguments.Nodes[0])] = struct{}{}
	outer := Unwrapped(call.Expression)
	consumed[call.Expression] = struct{}{}
	consumed[outer] = struct{}{}
	inner := Unwrapped(outer.AsPropertyAccessExpression().Expression)
	consumed[outer.AsPropertyAccessExpression().Expression] = struct{}{}
	consumed[inner] = struct{}{}
	consumeReceiver(consumed, inner)
}

// consumeReceiver marks the receiver spelling inside a recognized access
// as accounted for, so the walk does not count it again as a bare
// mention. It marks the access's own receiver expression and everything
// the parens and casts around it wrap.
func consumeReceiver(consumed map[*ast.Node]struct{}, access *ast.Node) {
	var receiver *ast.Node
	if ast.IsPropertyAccessExpression(access) {
		receiver = access.AsPropertyAccessExpression().Expression
	} else if ast.IsElementAccessExpression(access) {
		receiver = access.AsElementAccessExpression().Expression
	}
	for receiver != nil {
		consumed[receiver] = struct{}{}
		unwrapped := Unwrapped(receiver)
		if unwrapped == receiver {
			return
		}
		receiver = unwrapped
	}
}

// isPropertyStepName answers whether an identifier is the NAME half of a
// property access — the `wrapper` in `holder.wrapper`. Such an identifier
// spells a step, never the object, so a receiver-shaped name in that
// position is nobody's mention of the receiver. Both walks in this file
// apply the rule, so it lives in one place.
func isPropertyStepName(node *ast.Node) bool {
	if node == nil || !ast.IsIdentifier(node) {
		return false
	}
	parent := node.Parent
	if parent == nil {
		return false
	}
	if ast.IsPropertyAccessExpression(parent) {
		return parent.AsPropertyAccessExpression().Name() == node
	}
	return false
}
