// split from effect_expression.go — exactly known string methods

package walk

import (
	"math"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/jsnum"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// exactStringMethodEffect computes a string method call whose receiver
// and every argument are EXACTLY KNOWN strings or numbers, and answers
// the result as its own exact tuple.
//
// Why only the exact case. The effect wire carries `const` (a set),
// `var`, `concat`, `join` and `orAbsent` — and no string-method
// operation of any kind. A method over a receiver the lowering only
// knows a SET for would need a kernel-side transfer to answer, and there
// is no wire field to send it through; that is a kernel work order, not
// something this side can spell. What IS spellable is the case where
// nothing is unknown: the whole call has one value, that value is a
// string, and an exact tuple is precisely how the wire carries a string.
// So the pinned calls are served exactly and every other one keeps the
// reading it has today.
//
// The transcribed methods, each read from the vendored spec rather than
// from memory:
//
//	slice(start, end)   — sec-string.prototype.slice
//	toUpperCase()       — sec-string.prototype.touppercase
//	toLowerCase()       — sec-string.prototype.tolowercase
//	trim()              — sec-string.prototype.trim
//	concat(…)           — sec-string.prototype.concat
//
// `replace`, `split`, `indexOf`, `match` and the rest are deliberately
// absent: replace carries pattern and `$`-substitution semantics, split
// answers an ARRAY rather than a string, and indexOf answers a number
// and belongs to the numeric reader, not this one.
//
// THE CODE-UNIT GATE. The spec indexes and measures strings in UTF-16
// CODE UNITS; Go indexes bytes and ranges runes, and the set encoding
// (refinementsets.CodepointsOf) is in CODE POINTS. The three agree
// exactly when every character is BMP and non-surrogate, which
// allBasicPlane checks on the receiver and on every string argument
// before any of this runs. A string carrying an astral character
// declines and keeps its old reading, so the divergence is refused
// rather than approximated.
func exactStringMethodEffect(context *LoweringContext, e *ast.Node) (kernelbridge.LoopEffect, bool) {
	if !ast.IsCallExpression(e) {
		return kernelbridge.LoopEffect{}, false
	}
	call := e.AsCallExpression()
	if call.QuestionDotToken != nil || !ast.IsPropertyAccessExpression(call.Expression) {
		return kernelbridge.LoopEffect{}, false
	}
	access := call.Expression.AsPropertyAccessExpression()
	if access.QuestionDotToken != nil || !ast.IsIdentifier(access.Name()) {
		return kernelbridge.LoopEffect{}, false
	}
	receiver, receiverOk := exactSyntacticStringOf(access.Expression)
	if !receiverOk || !allBasicPlane(receiver) {
		return kernelbridge.LoopEffect{}, false
	}
	var arguments []*ast.Node
	if call.Arguments != nil {
		arguments = call.Arguments.Nodes
	}
	result, ok := exactStringMethodResult(receiver, access.Name().Text(), arguments)
	if !ok {
		return kernelbridge.LoopEffect{}, false
	}
	return kernelbridge.LoopEffect{
		Kind: kernelbridge.LoopEffectConst,
		Set:  refinementsets.StringTuple(result),
	}, true
}

// exactStringMethodResult is the per-method computation, each step the
// spec's own. Answers false for a method not transcribed here or for an
// argument shape the method's steps cannot take exactly.
func exactStringMethodResult(receiver string, method string, arguments []*ast.Node) (string, bool) {
	switch method {
	case "toUpperCase":
		if len(arguments) != 0 {
			return "", false
		}
		return strings.ToUpper(receiver), true
	case "toLowerCase":
		if len(arguments) != 0 {
			return "", false
		}
		return strings.ToLower(receiver), true
	case "trim":
		// the spec trims the WhiteSpace and LineTerminator code points;
		// strings.TrimSpace trims Unicode space, which differs on a handful
		// of code points — so the trim is spelled out against the spec's own
		// set rather than delegated
		if len(arguments) != 0 {
			return "", false
		}
		return strings.Trim(receiver, ecmaWhitespace), true
	case "concat":
		out := receiver
		for _, argument := range arguments {
			text, ok := exactSyntacticStringOf(argument)
			if !ok || !allBasicPlane(text) {
				return "", false
			}
			out += text
		}
		return out, true
	case "slice":
		// sec-string.prototype.slice, steps 4-14, on a code-unit-indexable
		// receiver (the caller's allBasicPlane gate makes rune indexing the
		// same indexing)
		if len(arguments) == 0 || len(arguments) > 2 {
			return "", false
		}
		units := []rune(receiver)
		length := len(units)
		intStart, startOk := exactIntegerOf(arguments[0])
		if !startOk {
			return "", false
		}
		from := 0
		if intStart < 0 {
			from = max(length+intStart, 0)
		} else {
			from = min(intStart, length)
		}
		to := length
		if len(arguments) == 2 {
			intEnd, endOk := exactIntegerOf(arguments[1])
			if !endOk {
				return "", false
			}
			if intEnd < 0 {
				to = max(length+intEnd, 0)
			} else {
				to = min(intEnd, length)
			}
		}
		if from >= to {
			return "", true
		}
		return string(units[from:to]), true
	}
	return "", false
}

// ecmaWhitespace is the code points `String.prototype.trim` removes:
// the spec's WhiteSpace production (TAB, VT, FF, SP, NBSP, ZWNBSP, and
// the Unicode Space_Separator points) together with LineTerminator (LF,
// CR, LS, PS). Spelled as ESCAPES rather than as literal characters —
// the set includes a zero-width no-break space, which a Go compiler
// reads as a byte-order mark when it appears literally in source — and
// not delegated to strings.TrimSpace, whose set is Unicode's and
// differs.
const ecmaWhitespace = "\t\v\f \u00a0\ufeff\n\r\u2028\u2029" +
	"\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a" +
	"\u202f\u205f\u3000"

// allBasicPlane is whether every character is a BMP non-surrogate, which
// is when UTF-16 code units, Unicode code points, and Go runes all index
// and count the same. The exact string readings above run only on
// strings that pass.
func allBasicPlane(s string) bool {
	for _, r := range s {
		if r > 0xffff || (r >= 0xd800 && r <= 0xdfff) {
			return false
		}
	}
	return true
}

// exactSyntacticStringOf is the string an expression exactly IS, by syntax alone:
// a string literal, a substitution-free template, or a `+` chain of
// those. No name is read — a slot's set is a set, and this reader wants
// the one value case only.
func exactSyntacticStringOf(e *ast.Node) (string, bool) {
	head := Unwrapped(e)
	if head == nil {
		return "", false
	}
	if ast.IsStringLiteral(head) {
		return head.AsStringLiteral().Text, true
	}
	if ast.IsNoSubstitutionTemplateLiteral(head) {
		return head.AsNoSubstitutionTemplateLiteral().Text, true
	}
	if ast.IsBinaryExpression(head) {
		bin := head.AsBinaryExpression()
		if bin.OperatorToken.Kind != ast.KindPlusToken {
			return "", false
		}
		left, leftOk := exactSyntacticStringOf(bin.Left)
		if !leftOk {
			return "", false
		}
		right, rightOk := exactSyntacticStringOf(bin.Right)
		if !rightOk {
			return "", false
		}
		return left + right, true
	}
	return "", false
}

// exactIntegerOf is the integer an argument exactly IS — a numeric
// literal or its negation, with a whole value. A fractional or
// non-literal argument declines: ToIntegerOrInfinity would truncate it,
// and this reader serves the pinned case rather than modelling the
// coercion.
func exactIntegerOf(e *ast.Node) (int, bool) {
	head := Unwrapped(e)
	if head == nil {
		return 0, false
	}
	sign := 1
	if ast.IsPrefixUnaryExpression(head) {
		unary := head.AsPrefixUnaryExpression()
		switch unary.Operator {
		case ast.KindMinusToken:
			sign = -1
		case ast.KindPlusToken:
		default:
			return 0, false
		}
		head = Unwrapped(unary.Operand)
	}
	if head == nil || !ast.IsNumericLiteral(head) {
		return 0, false
	}
	value := float64(jsnum.FromString(head.AsNumericLiteral().Text))
	if value != math.Trunc(value) || math.IsInf(value, 0) || math.IsNaN(value) {
		return 0, false
	}
	return sign * int(value), true
}
