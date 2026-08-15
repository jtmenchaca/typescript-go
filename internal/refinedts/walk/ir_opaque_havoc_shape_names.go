// split from ir_opaque_havoc.go — naming the expression shape

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
)

// OpaqueReturnName spells a return whose VALUE no reading lowered:
// "return (call this.x.y)", "return (object literal)" — the word
// `return`, then the returned expression's own syntax in parentheses,
// so the histogram row names the shape a reader can go and build a
// reading for. A bare `return` (which always lowers) spells itself.
//
// The value is what was lost; the control flow was not. The name says
// only "return", never "return declined", because the statement did
// lower — porously.
func OpaqueReturnName(statement *ast.Node) string {
	if statement == nil || !ast.IsReturnStatement(statement) {
		return "return"
	}
	expression := statement.AsReturnStatement().Expression
	if expression == nil {
		return "return"
	}
	return "return (" + returnedShapeName(Unwrapped(expression)) + ")"
}

// returnedShapeName is the returned expression's own syntax, spelled
// plainly. A CALL keeps the callee's dotted path, which is the one
// spelling that tells a reader which callee to teach the lowering
// about; everything else says what kind of expression it is.
func returnedShapeName(e *ast.Node) string {
	if e == nil {
		return "expression"
	}
	switch {
	case ast.IsCallExpression(e):
		return havocCallName(e)
	case ast.IsAwaitExpression(e):
		return "await " + returnedShapeName(Unwrapped(e.AsAwaitExpression().Expression))
	case ast.IsObjectLiteralExpression(e):
		return "object literal"
	case ast.IsArrayLiteralExpression(e):
		return "array literal"
	case ast.IsNewExpression(e):
		return "new"
	case ast.IsElementAccessExpression(e):
		return "computed member"
	case ast.IsPropertyAccessExpression(e):
		if spelled, ok := calleeSpelling(e); ok {
			return "member " + spelled
		}
		return "member"
	case ast.IsIdentifier(e):
		return "name " + e.Text()
	case ast.IsTaggedTemplateExpression(e):
		// the TAG runs a body over the parts, so the shape a reader must
		// build is the tag's, not the template's
		if spelled, ok := calleeSpelling(Unwrapped(e.AsTaggedTemplateExpression().Tag)); ok {
			return "tagged template " + spelled
		}
		return "tagged template"
	case ast.IsTemplateExpression(e):
		// a template whose parts all read lowers through SequenceEffectOf
		// before ever reaching here, so one that arrives has a part with no
		// sequence reading. Naming that PART is the work-queue entry; the
		// word "template" alone is not.
		return "template over " + templatePartName(e)
	case ast.IsConditionalExpression(e):
		return "conditional"
	case ast.IsBinaryExpression(e):
		return binaryShapeName(e)
	case ast.IsFunctionLike(e):
		return "function"
	case ast.IsVoidExpression(e):
		// `void e` discards a value and runs `e` — the shape to build a
		// reading for is the operand's
		return "void " + returnedShapeName(Unwrapped(e.AsVoidExpression().Expression))
	case ast.IsSatisfiesExpression(e):
		// Unwrapped strips parens, `as`, and `!`, but not `satisfies`
		return returnedShapeName(Unwrapped(e.AsSatisfiesExpression().Expression))
	case ast.IsTypeOfExpression(e):
		return "typeof"
	case ast.IsSpreadElement(e):
		return "spread"
	case ast.IsYieldExpression(e):
		return "yield"
	case ast.IsPrefixUnaryExpression(e):
		return prefixShapeName(e)
	case ast.IsPostfixUnaryExpression(e):
		return "postfix " + operatorWord(e.AsPostfixUnaryExpression().Operator)
	case ast.IsClassLike(e):
		return "class expression"
	case ast.IsRegularExpressionLiteral(e):
		return "regular expression"
	case ast.IsBigIntLiteral(e):
		return "bigint literal"
	case e.Kind == ast.KindThisKeyword:
		return "this"
	case e.Kind == ast.KindNullKeyword:
		return "null"
	case e.Kind == ast.KindTrueKeyword, e.Kind == ast.KindFalseKeyword:
		return "boolean literal"
	case ast.IsNumericLiteral(e):
		return "number literal"
	case ast.IsStringLiteral(e), ast.IsNoSubstitutionTemplateLiteral(e):
		return "string literal"
	}
	return "expression"
}

// templatePartName is the FIRST substitution of a template that has no
// spelled name — the part a reader would go and build a sequence
// reading for. A template whose every substitution is a plain name or
// path (and so is only untracked, not unspellable) names the first of
// those instead, since that IS the missing slot.
func templatePartName(e *ast.Node) string {
	spans := e.AsTemplateExpression().TemplateSpans.Nodes
	for _, span := range spans {
		part := Unwrapped(span.AsTemplateSpan().Expression)
		if spelled, ok := SpelledNameOf(part); ok {
			return "name " + spelled
		}
		return returnedShapeName(part)
	}
	return "no substitution"
}

// binaryShapeName spells a binary expression by its OPERATOR, so the row
// says which operator wants a reading rather than the bare word
// "binary". An ASSIGNMENT reads as the assignment it is.
func binaryShapeName(e *ast.Node) string {
	bin := e.AsBinaryExpression()
	kind := bin.OperatorToken.Kind
	if kind >= ast.KindFirstAssignment && kind <= ast.KindLastAssignment {
		return "assignment"
	}
	switch kind {
	case ast.KindCommaToken:
		return "comma"
	case ast.KindAmpersandAmpersandToken:
		return "binary &&"
	case ast.KindBarBarToken:
		return "binary ||"
	case ast.KindQuestionQuestionToken:
		return "binary ??"
	case ast.KindInstanceOfKeyword:
		return "binary instanceof"
	case ast.KindInKeyword:
		return "binary in"
	case ast.KindPlusToken:
		return "binary +"
	}
	return "binary"
}

// prefixShapeName spells a prefix unary by its operator and its operand,
// so `!!(a && b)` says what it is rather than falling to "expression".
func prefixShapeName(e *ast.Node) string {
	unary := e.AsPrefixUnaryExpression()
	return operatorWord(unary.Operator) + " " + returnedShapeName(Unwrapped(unary.Operand))
}

// operatorWord is a unary operator's own spelling for the report.
func operatorWord(operator ast.Kind) string {
	switch operator {
	case ast.KindExclamationToken:
		return "!"
	case ast.KindMinusToken:
		return "-"
	case ast.KindPlusToken:
		return "+"
	case ast.KindTildeToken:
		return "~"
	case ast.KindPlusPlusToken:
		return "++"
	case ast.KindMinusMinusToken:
		return "--"
	}
	return "unary"
}

// havocExpressionName spells the EXPRESSION an unreadable expression
// statement stands on — the shape the coverage was lost at.
func havocExpressionName(e *ast.Node) string {
	switch {
	case ast.IsCallExpression(e):
		return havocCallName(e)
	case ast.IsElementAccessExpression(e):
		return "computed member"
	case ast.IsAwaitExpression(e):
		// the awaited SHAPE is what wants a reading, not the await
		return "await " + returnedShapeName(Unwrapped(e.AsAwaitExpression().Expression))
	case ast.IsNewExpression(e):
		return "new"
	case ast.IsDeleteExpression(e):
		return "delete"
	case ast.IsBinaryExpression(e):
		bin := e.AsBinaryExpression()
		if bin.OperatorToken.Kind >= ast.KindFirstAssignment &&
			bin.OperatorToken.Kind <= ast.KindLastAssignment {
			if ast.IsElementAccessExpression(Unwrapped(bin.Left)) {
				return "computed member"
			}
			return "assignment"
		}
		// `a && void a.then(…)` and `(p = f(p)) && …` in statement
		// position: the operator names the shape, and a short-circuit
		// whose LEFT side is the thing that ran says so
		return binaryShapeName(e)
	}
	// every other expression shares the returned expression's naming: a
	// `void` chain, a `satisfies` wrapper, a spread, a yield, a prefix
	// `!`. The two positions ask the same question — what syntax was this
	// — so they answer through the same reading rather than diverging.
	return returnedShapeName(e)
}
