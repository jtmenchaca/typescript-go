// split from effect_capture_census.go — the call arm of the walk

package walk

import "github.com/microsoft/typescript-go/internal/ast"

// visitCall walks a call or `new` expression, and reports the same
// thing visit does: true once the census has ended.
//
// the callee's own steps are consumed, and every argument
// that hands a captured name WHOLE to code declines — the callee
// may write through the reference and no row carries that back
func (census *captureCensus) visitCall(node *ast.Node) bool {
	var arguments []*ast.Node
	var callee *ast.Node
	if ast.IsCallExpression(node) {
		call := node.AsCallExpression()
		callee = call.Expression
		if call.Arguments != nil {
			arguments = call.Arguments.Nodes
		}
	} else {
		newExpression := node.AsNewExpression()
		callee = newExpression.Expression
		if newExpression.Arguments != nil {
			arguments = newExpression.Arguments.Nodes
		}
	}
	for _, argument := range arguments {
		head := Unwrapped(argument)
		if head != nil && ast.IsIdentifier(head) {
			if _, isBound := census.bound[head.Text()]; !isBound {
				// `f(settled)` on a scalar capture passes by value and is
				// safe, but nothing here knows the sort — the caller's
				// gate does, and it refuses a capture whose slot is not
				// scalar. Reading it is what the row is for.
				census.note(head.Text())
				continue
			}
		}
		census.visit(argument)
	}
	// a METHOD CALL ON A CAPTURE — `disconnectSource.removeListener(…)`
	// — is not a read of a leaf: the method name is a function, and
	// the leaf vocabulary holds values. It is recorded on the object
	// so the layout can decide what the call may move, and the callee
	// expression is consumed here rather than visited, which would
	// take the property-access arm and note the method as a member.
	if ast.IsCallExpression(node) {
		if head := Unwrapped(callee); head != nil && ast.IsPropertyAccessExpression(head) {
			access := head.AsPropertyAccessExpression()
			receiver := Unwrapped(access.Expression)
			if receiver != nil && ast.IsIdentifier(receiver) && ast.IsIdentifier(access.Name()) {
				if _, isBound := census.bound[receiver.Text()]; !isBound {
					if access.QuestionDotToken != nil {
						// an optional call on a capture — the receiver may be
						// absent, and no leaf carries "the members of a
						// maybe-absent object"
						census.declined = true
						return true
					}
					census.noteMethodCall(receiver.Text(), access.Name().Text())
					return false
				}
			}
			// a call through a DEEPER path on a capture
			// (`p.a.b(…)`). The method vocabulary names a member of
			// the capture ITSELF — MethodCalls is a list of member
			// names, and capturedMethodMoves resolves each against
			// the capture's own receiver — so a method one level
			// further down has no spelling here. Visiting the callee
			// instead would note "a.b" as a LEAF READ, which is
			// exactly wrong: a method is a function, not a value the
			// leaf holds. The census ends rather than mis-naming it.
			if pathRoot, _, pathOk := propertyPathOf(head); pathOk {
				if _, isBound := census.bound[pathRoot]; !isBound && pathRoot != "this" {
					census.declined = true
					return true
				}
			}
		}
	}
	census.visit(callee)
	return false
}
