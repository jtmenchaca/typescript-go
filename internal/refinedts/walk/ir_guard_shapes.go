// split from ir_guard.go — the syntactic shape predicates: Number.isNaN
// recognition and boolean-shapedness

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
)

// IsNanShape is isNanShape in the TS source: whether a call is
// spelled `Number.isNaN(one argument)` — the syntactic recognition,
// the same trust the Math reads carry. The GLOBAL isNaN coerces
// through ToNumber first and is NOT this test.
func IsNanShape(head *ast.Node) bool {
	if !ast.IsCallExpression(head) {
		return false
	}
	call := head.AsCallExpression()
	if !ast.IsPropertyAccessExpression(call.Expression) {
		return false
	}
	access := call.Expression.AsPropertyAccessExpression()
	return ast.IsIdentifier(access.Expression) && access.Expression.Text() == "Number" &&
		ast.IsIdentifier(access.Name()) && access.Name().Text() == "isNaN" &&
		call.Arguments != nil && len(call.Arguments.Nodes) == 1
}

// TestShaped is testShaped in the TS source: whether an expression
// is BOOLEAN-SHAPED — a test or a connective composition of tests,
// whose value the guard desugaring may spell as {1}/{0}. A call is
// admitted only where composition can resolve it (guard position
// decides truthiness alone, so a callee's exact value never leaks).
//
// THE VALUE CLAIM IS WHAT THIS PREDICATE IS ABOUT. Its one caller
// (the return route) writes {1} on the true path and {0} on the false
// one, so answering true asserts the expression's VALUE is the boolean
// its truth decides. That holds for a comparison, and it does NOT hold
// for a bare short-circuit: `return host && host.instance` evaluates to
// `host.instance`, not to `true`, and claiming {1} for it would be a
// wrong answer about the returned value.
//
// So a place's TRUTHINESS is admitted only where a `!` has already
// forced the result to a boolean — `!x`, and the `!!(a && b.patch)`
// nest writes — which is what `negated` carries down the walk. Under a
// negation the whole subtree's value is `true` or `false` whatever its
// leaves evaluate to, so a leaf that reads only as a truth is enough;
// with no negation in force each leaf must be a comparison, which is
// exactly what this predicate admitted before.
//
// `a ?? b` is the DEFINEDNESS branch, not a truthiness one: it takes
// the right side only when the left is null or undefined. It rides the
// same rule — under a negation the value is a boolean, so the left
// place needs only a slot to test definedness on.
func TestShaped(context *LoweringContext, e *ast.Node) bool {
	return testShaped(context, e, false /*negated*/)
}

// testShaped is the reading, carrying whether a `!` above it has
// already forced the value to a boolean.
func testShaped(context *LoweringContext, e *ast.Node, negated bool) bool {
	head := Unwrapped(e)
	if ast.IsPrefixUnaryExpression(head) {
		unary := head.AsPrefixUnaryExpression()
		if unary.Operator == ast.KindExclamationToken {
			// `!e` is a boolean whatever e evaluates to, so everything under
			// it may read as a truth alone
			return testShaped(context, unary.Operand, true /*negated*/)
		}
	}
	if ast.IsBinaryExpression(head) {
		bin := head.AsBinaryExpression()
		kind := bin.OperatorToken.Kind
		if kind == ast.KindAmpersandAmpersandToken || kind == ast.KindBarBarToken {
			return testShaped(context, bin.Left, negated) && testShaped(context, bin.Right, negated)
		}
		if kind == ast.KindQuestionQuestionToken {
			// only under a negation: `a ?? b` bare evaluates to a or to b,
			// neither of which is the boolean the caller would write
			if !negated {
				return false
			}
			// the left side must be a tracked place for the definedness
			// branch to have something to test; the right side rides in the
			// else arm and is read as any other test-shaped expression
			//
			// lowerGuard also takes a CALL left here, through the hoist — but
			// only where the `??` runs on every path through the condition,
			// which is a fact about the position this `??` sits in and not
			// about the expression. This reading walks the expression alone
			// and cannot tell an unconditional `??` from one under a short
			// circuit's right side, so it keeps the narrower answer: a call
			// left is reported unshaped here, and the caller's other routes
			// read the return. Widening it would promise a reading lowerGuard
			// refuses whenever the flag is down.
			if _, tracked := IndexOf(context, Unwrapped(bin.Left)); !tracked {
				return false
			}
			return testShaped(context, bin.Right, negated)
		}
		if kind == ast.KindEqualsEqualsEqualsToken || kind == ast.KindExclamationEqualsEqualsToken ||
			kind == ast.KindEqualsEqualsToken {
			return true
		}
		_, hasCmp := CmpOps[kind]
		return hasCmp
	}
	if ast.IsTypeOfExpression(head) {
		return false
	}
	if ast.IsCallExpression(head) {
		return IsNanShape(head) || context.ResolveCallee != nil
	}
	return negated && truthyLeafShaped(context, head)
}

// truthyLeafShaped is whether a bare place reads as a truthiness leaf.
//
// Every TRACKED place does. A slot wearing the number or the string
// sort takes TestOf's own truthiness test; one wearing neither has no
// truthiness test on the wire and takes lowerGuard's untested branch,
// both arms riding and joining. Either way lowerGuard answers
// statements for the leaf, which is what this question is asked for.
//
// A boolean-shaped return reading an untested leaf still writes a
// boolean: the then arm writes {1}, the else writes {0}, and the join
// over an untested branch is {0,1} — the whole of what that return can
// be, claiming nothing about which. The two questions are asked of the
// same slot through the same IndexOf, so TestShaped and lowerGuard
// cannot disagree about which leaves read.
func truthyLeafShaped(context *LoweringContext, head *ast.Node) bool {
	_, tracked := IndexOf(context, head)
	return tracked
}
