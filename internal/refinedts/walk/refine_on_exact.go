// from assignability/refine_on_exact.ts
//
// Run an unread `.refine` predicate on an exact value — a tiny
// evaluator over the guard shapes the refine reader recognizes,
// computed with the host's own semantics on the one concrete value
// (the string-read oracle rule: `.length` counts UTF-16 units,
// affix tests are the host's). Single-parameter expression bodies
// only; a block with one return reads the same. Null anywhere the
// body leaves the language — never a guess.

package walk

import (
	"math"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/jsnum"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
)

// RefineDecidedOnExact is refineDecidedOnExact in the TS source. The
// (bool, bool) pair stands in for TS's `boolean | null`: the second
// bool is "decided" — false means the body left the language (the TS
// null).
func RefineDecidedOnExact(predicate *ast.Node, known abstractdomain.AbstractValue) (decided bool, ok bool) {
	if known.Kind != abstractdomain.KindValues {
		return false, false
	}
	params := predicate.Parameters()
	if len(params) != 1 {
		return false, false
	}
	parameterName := params[0].Name()
	if parameterName == nil || !ast.IsIdentifier(parameterName) {
		return false, false
	}
	name := parameterName.Text()

	var stringValue string
	var numberValue float64
	var isString, isNumber bool
	switch known.KindTag {
	case abstractdomain.PrimitiveString:
		stringValue = stringOfCodepoints(known.Values)
		isString = true
	case abstractdomain.PrimitiveNumber:
		if len(known.Values) == 1 {
			numberValue = known.Values[0]
			isNumber = true
		}
	}
	if !isString && !isNumber {
		return false, false
	}

	body := predicate.Body()
	if body == nil {
		return false, false
	}
	var expression *ast.Node
	if ast.IsBlock(body) {
		statements := body.AsBlock().Statements.Nodes
		if len(statements) == 1 && ast.IsReturnStatement(statements[0]) {
			expression = statements[0].AsReturnStatement().Expression
		}
		if expression == nil {
			return false, false
		}
	} else {
		expression = body
	}

	// evaluate returns (value, decided) where value is one of
	// string | float64 | bool per the TS union — carried here as
	// interface{} since the recursive evaluator must return any of
	// the three.
	var evaluate func(e *ast.Node) (any, bool)
	evaluate = func(e *ast.Node) (any, bool) {
		if ast.IsParenthesizedExpression(e) {
			return evaluate(e.AsParenthesizedExpression().Expression)
		}
		if ast.IsIdentifier(e) {
			if e.Text() != name {
				return nil, false
			}
			if isString {
				return stringValue, true
			}
			return numberValue, true
		}
		if ast.IsNumericLiteral(e) {
			return float64(jsnum.FromString(e.Text())), true
		}
		if ast.IsStringLiteral(e) || ast.IsNoSubstitutionTemplateLiteral(e) {
			return e.Text(), true
		}
		if e.Kind == ast.KindTrueKeyword {
			return true, true
		}
		if e.Kind == ast.KindFalseKeyword {
			return false, true
		}
		if ast.IsPrefixUnaryExpression(e) {
			unary := e.AsPrefixUnaryExpression()
			if unary.Operator == ast.KindExclamationToken {
				inner, innerOk := evaluate(unary.Operand)
				if !innerOk {
					return nil, false
				}
				b, isBool := inner.(bool)
				if !isBool {
					return nil, false
				}
				return !b, true
			}
			if unary.Operator == ast.KindMinusToken {
				inner, innerOk := evaluate(unary.Operand)
				if !innerOk {
					return nil, false
				}
				n, isNum := inner.(float64)
				if !isNum {
					return nil, false
				}
				return -n, true
			}
			return nil, false
		}
		// `x.length` — the host's own UTF-16 count on the one value
		if ast.IsPropertyAccessExpression(e) && e.AsPropertyAccessExpression().Name().Text() == "length" {
			receiver, receiverOk := evaluate(e.AsPropertyAccessExpression().Expression)
			if !receiverOk {
				return nil, false
			}
			s, isStr := receiver.(string)
			if !isStr {
				return nil, false
			}
			return float64(utf16Length(s)), true
		}
		// the affix and search tests, on the one value with literal
		// arguments — the host computes them
		if ast.IsCallExpression(e) {
			call := e.AsCallExpression()
			if ast.IsPropertyAccessExpression(call.Expression) && len(call.Arguments.Nodes) == 1 {
				propAccess := call.Expression.AsPropertyAccessExpression()
				receiver, receiverOk := evaluate(propAccess.Expression)
				argument, argumentOk := evaluate(call.Arguments.Nodes[0])
				if receiverOk && argumentOk {
					receiverStr, receiverIsStr := receiver.(string)
					argumentStr, argumentIsStr := argument.(string)
					if receiverIsStr && argumentIsStr {
						switch propAccess.Name().Text() {
						case "includes":
							return strings.Contains(receiverStr, argumentStr), true
						case "startsWith":
							return strings.HasPrefix(receiverStr, argumentStr), true
						case "endsWith":
							return strings.HasSuffix(receiverStr, argumentStr), true
						}
					}
				}
				return nil, false
			}
			return nil, false
		}
		if ast.IsBinaryExpression(e) {
			bin := e.AsBinaryExpression()
			op := bin.OperatorToken.Kind
			if op == ast.KindAmpersandAmpersandToken {
				left, leftOk := evaluate(bin.Left)
				if !leftOk {
					return nil, false
				}
				if !truthyOf(left) {
					return left, true
				}
				return evaluate(bin.Right)
			}
			if op == ast.KindBarBarToken {
				left, leftOk := evaluate(bin.Left)
				if !leftOk {
					return nil, false
				}
				if truthyOf(left) {
					return left, true
				}
				return evaluate(bin.Right)
			}
			left, leftOk := evaluate(bin.Left)
			right, rightOk := evaluate(bin.Right)
			if !leftOk || !rightOk {
				return nil, false
			}
			switch op {
			case ast.KindGreaterThanToken:
				return comparePredicateOperands(left, right, func(a, b float64) bool { return a > b }, func(a, b string) bool { return a > b })
			case ast.KindGreaterThanEqualsToken:
				return comparePredicateOperands(left, right, func(a, b float64) bool { return a >= b }, func(a, b string) bool { return a >= b })
			case ast.KindLessThanToken:
				return comparePredicateOperands(left, right, func(a, b float64) bool { return a < b }, func(a, b string) bool { return a < b })
			case ast.KindLessThanEqualsToken:
				return comparePredicateOperands(left, right, func(a, b float64) bool { return a <= b }, func(a, b string) bool { return a <= b })
			case ast.KindEqualsEqualsEqualsToken:
				return predicateOperandsEqual(left, right), true
			case ast.KindExclamationEqualsEqualsToken:
				eq := predicateOperandsEqual(left, right)
				return !eq.(bool), true
			case ast.KindPercentToken:
				leftNum, leftIsNum := left.(float64)
				rightNum, rightIsNum := right.(float64)
				if leftIsNum && rightIsNum {
					return mod(leftNum, rightNum), true
				}
				return nil, false
			}
			return nil, false
		}
		return nil, false
	}
	outcome, outcomeOk := evaluate(expression)
	if !outcomeOk {
		return false, false
	}
	// the predicate's verdict is ToBoolean of its result (vendored
	// zod: a falsy result files the issue)
	return truthyOf(outcome), true
}

func truthyOf(v any) bool {
	switch value := v.(type) {
	case bool:
		return value
	case float64:
		return value != 0
	case string:
		return value != ""
	}
	return false
}

func predicateOperandsEqual(a, b any) any {
	switch left := a.(type) {
	case float64:
		right, ok := b.(float64)
		return ok && left == right
	case string:
		right, ok := b.(string)
		return ok && left == right
	case bool:
		right, ok := b.(bool)
		return ok && left == right
	}
	return false
}

func comparePredicateOperands(a, b any, numCmp func(a, b float64) bool, strCmp func(a, b string) bool) (any, bool) {
	if leftNum, leftIsNum := a.(float64); leftIsNum {
		if rightNum, rightIsNum := b.(float64); rightIsNum {
			return numCmp(leftNum, rightNum), true
		}
		return nil, false
	}
	if leftStr, leftIsStr := a.(string); leftIsStr {
		if rightStr, rightIsStr := b.(string); rightIsStr {
			return strCmp(leftStr, rightStr), true
		}
		return nil, false
	}
	return nil, false
}

// mod mirrors JS's `%` operator (truncated remainder — sign follows
// the dividend), which is exactly Go's math.Mod.
func mod(a, b float64) float64 {
	return math.Mod(a, b)
}

// stringOfCodepoints mirrors String.fromCodePoint(...values).
func stringOfCodepoints(values []float64) string {
	var b strings.Builder
	for _, v := range values {
		b.WriteRune(rune(int32(v)))
	}
	return b.String()
}

// utf16Length mirrors JS's `.length` — a count of UTF-16 code units,
// not Go's byte or rune count.
func utf16Length(s string) int {
	n := 0
	for _, r := range s {
		if r > 0xFFFF {
			n += 2
		} else {
			n++
		}
	}
	return n
}
