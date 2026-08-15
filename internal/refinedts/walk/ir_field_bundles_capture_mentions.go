// split from ir_field_bundles.go — the nested-function mention scan

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
)

// captureMentions walks a NESTED FUNCTION's subtree and answers the
// declared fields it READS through the receiver and the receiver
// METHODS it CALLS — provided every receiver mention is one of exactly
// those two shapes. Anything else — a write target, an optional or
// computed step, an undeclared member read, a bare mention — answers
// (nil, nil, false) and the caller keeps the escape.
//
// A read is admissible from inside a closure that runs at ANY later
// time because reading moves nothing. A method CALL is admissible only
// conditionally: the consumer must compute the transitive write set of
// every collected method and treat those fields as movable at every
// call statement — captureMentions only collects the names.
func captureMentions(
	c *checker.Checker,
	node *ast.Node,
	isReceiver func(*ast.Node) bool,
	byName map[string]BundleField,
) ([]string, []string, bool) {
	var reads []string
	var calledMethods []string
	readOnly := true
	var visit func(child *ast.Node) bool
	visit = func(child *ast.Node) bool {
		if !readOnly || child == nil {
			return true
		}
		// any write form whose subtree mentions the receiver fails the
		// admission — the target may be a field, and a field written at an
		// unplaceable time is exactly what the escape guards
		if ast.IsBinaryExpression(child) {
			operator := child.AsBinaryExpression().OperatorToken.Kind
			if operator >= ast.KindFirstAssignment && operator <= ast.KindLastAssignment {
				if mentionsReceiverNode(child.AsBinaryExpression().Left, isReceiver) {
					readOnly = false
					return true
				}
			}
		}
		if ast.IsPrefixUnaryExpression(child) {
			operator := child.AsPrefixUnaryExpression().Operator
			if (operator == ast.KindPlusPlusToken || operator == ast.KindMinusMinusToken) &&
				mentionsReceiverNode(child.AsPrefixUnaryExpression().Operand, isReceiver) {
				readOnly = false
				return true
			}
		}
		if ast.IsPostfixUnaryExpression(child) {
			operator := child.AsPostfixUnaryExpression().Operator
			if (operator == ast.KindPlusPlusToken || operator == ast.KindMinusMinusToken) &&
				mentionsReceiverNode(child.AsPostfixUnaryExpression().Operand, isReceiver) {
				readOnly = false
				return true
			}
		}
		if ast.IsDeleteExpression(child) && mentionsReceiverNode(child, isReceiver) {
			readOnly = false
			return true
		}
		// `Object.assign(this, source)` — a store into fields nothing
		// names, exactly the computed store's shape: every field may have
		// moved, and the declaration still bounds the set. ComputedWrite
		// admits it under the all-field havoc bracketing instead of the
		// escape. The SOURCE arguments walk on as ordinary expressions.
		if ast.IsCallExpression(child) {
			assignCall := child.AsCallExpression()
			if assignCall.QuestionDotToken == nil && ast.IsPropertyAccessExpression(assignCall.Expression) {
				assignAccess := assignCall.Expression.AsPropertyAccessExpression()
				if assignAccess.QuestionDotToken == nil &&
					ast.IsIdentifier(assignAccess.Expression) && assignAccess.Expression.Text() == "Object" &&
					ast.IsIdentifier(assignAccess.Name()) && assignAccess.Name().Text() == "assign" &&
					assignCall.Arguments != nil && len(assignCall.Arguments.Nodes) > 0 &&
					isReceiver(Unwrapped(assignCall.Arguments.Nodes[0])) {
					readOnly = false // reached only inside captures — a capture that
					// reshapes the receiver is beyond the capture rules
					return true
				}
			}
		}
		// a CALL whose callee is a receiver access is a METHOD call. The
		// method's body may write fields, so the call is not a read — but
		// it is not blind either: the method NAME is collected, and the
		// consumer decides whether the transitive write set of every
		// collected method is computable. A non-identifier or optional
		// callee step fails outright. The ARGUMENTS walk on under the
		// same rules.
		//
		// A SYMBOL-KEYED callee (`this[S](…)`) is the same call under a
		// different spelling and takes the same arm, collected under its
		// `#sym:` name. The stable key names ONE member, which is all this
		// collection needs; whether that member is a METHOD whose body the
		// closure can walk or a FIELD holding a function value is
		// CaptureWriteSetWith's question, and it answers it identically for
		// both spellings — a name resolving to a method-with-body
		// contributes that body's writes, and a name resolving to anything
		// else makes the whole set incomputable and the caller keeps the
		// escape. Deciding it here instead would refuse the symbol-keyed
		// field call one stage earlier than the dotted one spells the same
		// refusal, for no reason the two shapes justify.
		if ast.IsCallExpression(child) {
			call := child.AsCallExpression()
			callee := Unwrapped(call.Expression)
			if ast.IsPropertyAccessExpression(callee) &&
				isReceiver(Unwrapped(callee.AsPropertyAccessExpression().Expression)) {
				access := callee.AsPropertyAccessExpression()
				if call.QuestionDotToken != nil || access.QuestionDotToken != nil ||
					!ast.IsIdentifier(access.Name()) {
					readOnly = false
					return true
				}
				calledMethods = append(calledMethods, access.Name().Text())
				for _, argument := range call.Arguments.Nodes {
					visit(argument)
				}
				return false
			}
			if ast.IsElementAccessExpression(callee) &&
				isReceiver(Unwrapped(callee.AsElementAccessExpression().Expression)) {
				name, isSymbolKey := SymbolKeyedFieldName(c, callee)
				if call.QuestionDotToken != nil || !isSymbolKey {
					readOnly = false
					return true
				}
				calledMethods = append(calledMethods, name)
				for _, argument := range call.Arguments.Nodes {
					visit(argument)
				}
				return false
			}
		}
		// a plain declared-field READ: record it and step over the
		// receiver spelling it consumed
		if ast.IsPropertyAccessExpression(child) {
			access := child.AsPropertyAccessExpression()
			if isReceiver(Unwrapped(access.Expression)) {
				if access.QuestionDotToken != nil || !ast.IsIdentifier(access.Name()) {
					readOnly = false
					return true
				}
				name := access.Name().Text()
				if _, declared := byName[name]; !declared {
					readOnly = false
					return true
				}
				reads = append(reads, name)
				return false
			}
		}
		// a SYMBOL-KEYED read: `this[S]` under a stable symbol const names
		// one declared field, so it is admissible on the same ground the
		// dotted read is — reading moves nothing. Every other bracketed
		// key names no field and fails below.
		//
		// A `this[S](…)` CALL never reaches here: the call arm above takes
		// it, admitting it where the class declares a method for S and
		// refusing it where nothing does. What is left in this position is
		// a read of the slot's own value, which is what the field rules
		// answer.
		if ast.IsElementAccessExpression(child) {
			element := child.AsElementAccessExpression()
			if isReceiver(Unwrapped(element.Expression)) {
				name, isSymbolKey := SymbolKeyedFieldName(c, child)
				if !isSymbolKey {
					readOnly = false
					return true
				}
				if _, declared := byName[name]; !declared {
					readOnly = false
					return true
				}
				reads = append(reads, name)
				return false
			}
		}
		// any OTHER receiver occurrence — bare, computed, spread — fails
		if isReceiver(child) && !isPropertyStepName(child) {
			readOnly = false
			return true
		}
		child.ForEachChild(visit)
		return false
	}
	node.ForEachChild(visit)
	if !readOnly {
		return nil, nil, false
	}
	return reads, calledMethods, true
}

// mentionsReceiverNode is mentionsReceiver over a receiver PREDICATE
// rather than a name — the nested-function admission tests subtrees
// with the census's own isReceiver.
func mentionsReceiverNode(node *ast.Node, isReceiver func(*ast.Node) bool) bool {
	if node == nil {
		return false
	}
	if isReceiver(node) && !isPropertyStepName(node) {
		return true
	}
	found := false
	node.ForEachChild(func(child *ast.Node) bool {
		if found {
			return true
		}
		if mentionsReceiverNode(child, isReceiver) {
			found = true
			return true
		}
		return false
	})
	return found
}

// mentionsReceiver answers whether a subtree names the receiver at all —
// the test a nested function is judged by, where WHAT it does with the
// bundle is beyond this scan's reach and any mention is an escape.
func mentionsReceiver(node *ast.Node, receiverName string) bool {
	found := false
	var visit func(child *ast.Node) bool
	visit = func(child *ast.Node) bool {
		if found || child == nil {
			return true
		}
		if child.Kind == ast.KindThisKeyword && receiverName == "this" {
			found = true
			return true
		}
		if ast.IsIdentifier(child) && child.Text() == receiverName && !isPropertyStepName(child) {
			found = true
			return true
		}
		child.ForEachChild(visit)
		return false
	}
	node.ForEachChild(visit)
	return found
}
