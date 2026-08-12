// Literal arguments on a schema chain: a number, a string, or a
// list of strings, read from syntax (or a name bound to one). The
// chain compiler and the zod def vocabulary both ask here.
//
// Ported 1:1 from annotations/chain_args.ts. The TS source reads
// through a CheckerProgram (p.host) -- the CheckerHost/tsgo-oracle
// adapter. Per the port's convention, that adapter does not port:
// numberArg reads *checker.Checker directly via symbolAt.

package annotations

import (
	"math"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/jsnum"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
)

// NumberArg is numberArg in the TS source: a numeric argument -- a
// literal, its negation, Infinity, or a name bound to one.
func NumberArg(p *program.CheckerProgram, e *ast.Node) (float64, bool) {
	if ast.IsNumericLiteral(e) {
		return float64(jsnum.FromString(e.AsNumericLiteral().Text)), true
	}
	if ast.IsPrefixUnaryExpression(e) {
		unary := e.AsPrefixUnaryExpression()
		if unary.Operator == ast.KindMinusToken {
			inner, ok := NumberArg(p, unary.Operand)
			if !ok {
				return 0, false
			}
			return -inner, true
		}
	}
	if ast.IsIdentifier(e) {
		if e.AsIdentifier().Text == "Infinity" {
			return math.Inf(1), true
		}
		symbol := symbolAt(p.Checker, e)
		if symbol != nil {
			declaration := symbol.ValueDeclaration
			if declaration != nil && ast.IsVariableDeclaration(declaration) {
				initializer := declaration.AsVariableDeclaration().Initializer
				if initializer != nil {
					return NumberArg(p, initializer)
				}
			}
		}
	}
	return 0, false
}

// StringArg is stringArg in the TS source: a string argument -- a
// literal or a no-substitution template.
func StringArg(e *ast.Node) (string, bool) {
	if ast.IsStringLiteral(e) {
		return e.AsStringLiteral().Text, true
	}
	if ast.IsNoSubstitutionTemplateLiteral(e) {
		return e.AsNoSubstitutionTemplateLiteral().Text, true
	}
	return "", false
}

// StringList is stringList in the TS source.
func StringList(e *ast.Node) ([]string, bool) {
	bare := e
	if ast.IsAsExpression(bare) {
		bare = bare.AsAsExpression().Expression
	}
	if !ast.IsArrayLiteralExpression(bare) {
		return nil, false
	}
	var out []string
	for _, element := range bare.AsArrayLiteralExpression().Elements.Nodes {
		s, ok := StringArg(element)
		if !ok {
			return nil, false
		}
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}
