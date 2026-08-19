// from assignability/refine_on_exact.ts
//
// Run an unread `.refine` predicate on an exact value — a tiny
// evaluator over the guard shapes the refine reader recognizes,
// computed with the host's own semantics on the one concrete value
// (the string-read oracle rule: `.length` counts UTF-16 units,
// affix tests are the host's). Single-parameter bodies only; a block
// body reads as its const bindings followed by its one return. Null
// anywhere the body leaves the language — never a guess.
//
// Every row here computes a real JS result on a KNOWN receiver with
// KNOWN arguments, or declines. The evaluator never approximates: a
// row whose spec text leaves the answer implementation-approximated
// (`**` off its pinned branches), or whose Unicode tables this file
// cannot transcribe (case mapping past ASCII), or whose regex
// features RE2 cannot express, declines instead of guessing.

package walk

import (
	"math"
	"strings"
	"unicode"

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
	// The statements the block binds before its return — each a const
	// whose initializer this same evaluator computes. An expression
	// body has none.
	var bindingStatements []*ast.Node
	var expression *ast.Node
	if ast.IsBlock(body) {
		statements := body.AsBlock().Statements.Nodes
		if len(statements) == 0 {
			return false, false
		}
		last := statements[len(statements)-1]
		if !ast.IsReturnStatement(last) {
			return false, false
		}
		expression = last.AsReturnStatement().Expression
		if expression == nil {
			return false, false
		}
		bindingStatements = statements[:len(statements)-1]
	} else {
		expression = body
	}

	// The names the block's consts bound, filled in source order
	// before the return is evaluated. A const's initializer therefore
	// reads only the parameter and the consts declared above it.
	bindings := map[string]any{}

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
			if bound, isBound := bindings[e.Text()]; isBound {
				return bound, true
			}
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
		// the string reads, on the one value with arguments this same
		// evaluator computes — the host computes them
		if ast.IsCallExpression(e) {
			call := e.AsCallExpression()
			if !ast.IsPropertyAccessExpression(call.Expression) {
				return nil, false
			}
			propAccess := call.Expression.AsPropertyAccessExpression()
			method := propAccess.Name().Text()
			var arguments []*ast.Node
			if call.Arguments != nil {
				arguments = call.Arguments.Nodes
			}

			// `/re/.test(x)` — the regex is a literal, so its source
			// and flags are in hand; the argument is whatever this
			// evaluator computes. RE2 cannot express JS lookaround and
			// a few JS-only escapes, so a pattern regexToGo will not
			// compile declines rather than matching something else.
			if method == "test" && ast.IsRegularExpressionLiteral(propAccess.Expression) && len(arguments) == 1 {
				subject, subjectOk := evaluate(arguments[0])
				if !subjectOk {
					return nil, false
				}
				subjectStr, subjectIsStr := subject.(string)
				if !subjectIsStr {
					return nil, false
				}
				text := propAccess.Expression.Text()
				lastSlash := strings.LastIndex(text, "/")
				if lastSlash <= 0 {
					return nil, false
				}
				flags := text[lastSlash+1:]
				// a STICKY or GLOBAL pattern reads the regex object's
				// mutable lastIndex across calls, which this evaluator
				// does not track
				if strings.ContainsAny(flags, "yg") {
					return nil, false
				}
				compiled, translated := regexToGo(text[1:lastSlash], flags)
				if !translated {
					return nil, false
				}
				return compiled.MatchString(subjectStr), true
			}

			receiver, receiverOk := evaluate(propAccess.Expression)
			if !receiverOk {
				return nil, false
			}
			receiverStr, receiverIsStr := receiver.(string)
			if !receiverIsStr {
				return nil, false
			}

			if len(arguments) == 0 {
				switch method {
				// TrimString removes leading and/or trailing white
				// space, where white space is WhiteSpace ∪
				// LineTerminator (sec-trimstring) — a pinned code-point
				// set, not Go's unicode.IsSpace
				case "trim":
					return strings.TrimFunc(receiverStr, isJSWhiteSpace), true
				case "trimStart":
					return strings.TrimLeftFunc(receiverStr, isJSWhiteSpace), true
				case "trimEnd":
					return strings.TrimRightFunc(receiverStr, isJSWhiteSpace), true
				// toLowerCase/toUpperCase map by the Unicode Default
				// Case Conversion, including the multi-code-point
				// SpecialCasing rows (sec-string.prototype.tolowercase,
				// sec-string.prototype.touppercase). Those tables are
				// not transcribed here, so only ASCII — where the
				// mapping is the pinned a–z/A–Z shift — computes; any
				// other code point declines.
				case "toLowerCase":
					if !isASCII(receiverStr) {
						return nil, false
					}
					return asciiLower(receiverStr), true
				case "toUpperCase":
					if !isASCII(receiverStr) {
						return nil, false
					}
					return asciiUpper(receiverStr), true
				}
				return nil, false
			}

			if len(arguments) == 1 {
				argument, argumentOk := evaluate(arguments[0])
				if !argumentOk {
					return nil, false
				}
				if argumentStr, argumentIsStr := argument.(string); argumentIsStr {
					switch method {
					case "includes":
						return strings.Contains(receiverStr, argumentStr), true
					case "startsWith":
						return strings.HasPrefix(receiverStr, argumentStr), true
					case "endsWith":
						return strings.HasSuffix(receiverStr, argumentStr), true
					}
					return nil, false
				}
				if argumentNum, argumentIsNum := argument.(float64); argumentIsNum {
					index, indexIsInteger := integerOrInfinity(argumentNum)
					if !indexIsInteger {
						return nil, false
					}
					units := utf16UnitsOf(receiverStr)
					switch method {
					// charAt reads the code unit at position, and the
					// EMPTY string off the ends
					// (sec-string.prototype.charat)
					case "charAt":
						if index < 0 || index >= len(units) {
							return "", true
						}
						return utf16ToString([]uint16{units[index]}), true
					// at counts a negative index from the end and
					// answers UNDEFINED off the ends
					// (sec-string.prototype.at) — undefined is not one
					// of this evaluator's three value sorts, so an
					// out-of-range read declines
					case "at":
						if index < 0 {
							index += len(units)
						}
						if index < 0 || index >= len(units) {
							return nil, false
						}
						return utf16ToString([]uint16{units[index]}), true
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
			case ast.KindAsteriskAsteriskToken:
				leftNum, leftIsNum := left.(float64)
				rightNum, rightIsNum := right.(float64)
				if leftIsNum && rightIsNum {
					return exponentiate(leftNum, rightNum)
				}
				return nil, false
			}
			return nil, false
		}
		return nil, false
	}
	// bind the block's consts in order: each must be a `const name =
	// <expression this evaluator computes>`, and anything else — a
	// let, a destructuring pattern, an `if`, a call — leaves the
	// language and voids the whole evaluation
	for _, statement := range bindingStatements {
		if !ast.IsVariableStatement(statement) {
			return false, false
		}
		declarationList := statement.AsVariableStatement().DeclarationList
		if declarationList == nil || (declarationList.Flags&ast.NodeFlagsConst) == 0 {
			return false, false
		}
		for _, declaration := range declarationList.AsVariableDeclarationList().Declarations.Nodes {
			declared := declaration.AsVariableDeclaration()
			declaredName := declared.Name()
			if declaredName == nil || !ast.IsIdentifier(declaredName) || declared.Initializer == nil {
				return false, false
			}
			bound, boundOk := evaluate(declared.Initializer)
			if !boundOk {
				return false, false
			}
			bindings[declaredName.Text()] = bound
		}
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

// exponentiate answers `base ** exponent` where Number::exponentiate
// (sec-numeric-types-number-exponentiate) pins the answer, and
// declines where it does not.
//
// The spec pins steps 1–12 — the NaN, zero-exponent, infinite-base,
// zero-base, and infinite-exponent branches — and calls the final
// step "an implementation-approximated value". math.Pow is one such
// implementation, not the spec's answer, so this evaluator does not
// read it on the approximated branch.
//
// The one further case that computes is the kernel's own carve-out
// (refined-lean/proofs/integer_pow.lean): an integer base with
// a non-negative integer exponent whose exact power fits binary64's
// mantissa is REPRESENTABLE, so every correctly rounded
// implementation returns it and there is nothing left to approximate.
// Anything else declines.
func exponentiate(base, exponent float64) (any, bool) {
	// step 1: an NaN exponent gives NaN — not one of this evaluator's
	// three value sorts, so it declines rather than naming it
	if math.IsNaN(exponent) {
		return nil, false
	}
	// step 2: a zero exponent (either sign) gives 1, even of NaN
	if exponent == 0 {
		return float64(1), true
	}
	// step 3: an NaN base past a nonzero exponent gives NaN
	if math.IsNaN(base) {
		return nil, false
	}
	// steps 4–5: the infinite bases
	if math.IsInf(base, 1) {
		if exponent > 0 {
			return math.Inf(1), true
		}
		return float64(0), true
	}
	if math.IsInf(base, -1) {
		if exponent > 0 {
			if isOddIntegral(exponent) {
				return math.Inf(-1), true
			}
			return math.Inf(1), true
		}
		if isOddIntegral(exponent) {
			return math.Copysign(0, -1), true
		}
		return float64(0), true
	}
	// steps 6–7: the signed zero bases
	if base == 0 {
		negativeZeroBase := math.Signbit(base)
		if exponent > 0 {
			if negativeZeroBase && isOddIntegral(exponent) {
				return math.Copysign(0, -1), true
			}
			return float64(0), true
		}
		if negativeZeroBase && isOddIntegral(exponent) {
			return math.Inf(-1), true
		}
		return math.Inf(1), true
	}
	// steps 9–10: the infinite exponents over a finite nonzero base —
	// the |base| = 1 row gives NaN, which declines
	if math.IsInf(exponent, 0) {
		magnitude := math.Abs(base)
		if magnitude == 1 {
			return nil, false
		}
		if (magnitude > 1) == math.IsInf(exponent, 1) {
			return math.Inf(1), true
		}
		return float64(0), true
	}
	// the exact integer-power carve-out: integer base, non-negative
	// integer exponent, every partial product inside binary64's
	// mantissa
	if base != math.Trunc(base) || exponent != math.Trunc(exponent) || exponent < 0 {
		return nil, false
	}
	product := float64(1)
	for k := 0; k < int(exponent); k++ {
		product *= base
		if math.Abs(product) > 1<<53 {
			return nil, false
		}
	}
	return product, true
}

// isOddIntegral is the spec's "an odd integral Number" test
// (sec-numeric-types-number-exponentiate) — an integer whose halving
// is not one.
func isOddIntegral(v float64) bool {
	return v == math.Trunc(v) && math.Mod(v, 2) != 0
}

// integerOrInfinity is ToIntegerOrInfinity (sec-tointegerorinfinity)
// on a number this evaluator already computed, restricted to the
// finite integers a UTF-16 index can name: NaN reads as 0, and an
// infinity or an out-of-int magnitude declines.
func integerOrInfinity(v float64) (int, bool) {
	if math.IsNaN(v) {
		return 0, true
	}
	if math.IsInf(v, 0) {
		return 0, false
	}
	truncated := math.Trunc(v)
	if math.Abs(truncated) > 1<<31 {
		return 0, false
	}
	return int(truncated), true
}

// isJSWhiteSpace is the code-point set TrimString removes
// (sec-trimstring): WhiteSpace — TAB, VT, FF, ZWNBSP, and general
// category Space_Separator (which holds SPACE and NO-BREAK SPACE) —
// together with LineTerminator: LF, CR, LS, PS. Go's unicode.IsSpace
// is a different set (it holds NEL and not ZWNBSP), so the set is
// written out here.
func isJSWhiteSpace(r rune) bool {
	switch r {
	// WhiteSpace: TAB, VT, FF, ZWNBSP
	case 0x0009, 0x000B, 0x000C, 0xFEFF:
		return true
	// LineTerminator: LF, CR, LS, PS
	case 0x000A, 0x000D, 0x2028, 0x2029:
		return true
	}
	// USP: general category Space_Separator, which holds SPACE
	// (U+0020) and NO-BREAK SPACE (U+00A0)
	return unicode.Is(unicode.Zs, r)
}

// isASCII reports whether every code point is under U+0080 — the
// range where case mapping is the pinned a–z/A–Z shift and no
// SpecialCasing row applies.
func isASCII(s string) bool {
	for _, r := range s {
		if r > 0x7F {
			return false
		}
	}
	return true
}

// asciiLower/asciiUpper map A–Z and a–z and leave every other ASCII
// code point alone — the Unicode Default Case Conversion restricted
// to the range isASCII admits.
func asciiLower(s string) string {
	units := []byte(s)
	for i, c := range units {
		if c >= 'A' && c <= 'Z' {
			units[i] = c + ('a' - 'A')
		}
	}
	return string(units)
}

func asciiUpper(s string) string {
	units := []byte(s)
	for i, c := range units {
		if c >= 'a' && c <= 'z' {
			units[i] = c - ('a' - 'A')
		}
	}
	return string(units)
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
