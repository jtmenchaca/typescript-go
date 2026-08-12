// A local copy of annotations/chain_args.go's numberArg/stringArg/
// stringList, duplicated here rather than imported: annotations
// (schema_chain_compiler.go) depends on libraryadapters, which
// depends on this zod package, so a zod -> annotations import would
// close the cycle. chain_args.ts itself has no dependency on
// library_adapters in the TS source (chain_args.ts imports only
// program_host.ts and program_resolution.ts) -- the duplication is
// purely a consequence of Go's no-cycles rule, not a divergence in
// the ported logic; the two copies must be kept in sync by hand if
// numberArg/stringArg/stringList ever change (see
// annotations/chain_args.go).
package zod

import (
	"math"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/jsnum"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
)

func symbolAt(c *checker.Checker, node *ast.Node) *ast.Symbol {
	symbol := c.GetSymbolAtLocation(node)
	if symbol != nil && (symbol.Flags&ast.SymbolFlagsAlias) != 0 {
		symbol = c.GetAliasedSymbol(symbol)
	}
	return symbol
}

// numberArg is numberArg in the TS source (chain_args.ts).
func numberArg(p *program.CheckerProgram, e *ast.Node) (float64, bool) {
	if ast.IsNumericLiteral(e) {
		return float64(jsnum.FromString(e.AsNumericLiteral().Text)), true
	}
	if ast.IsPrefixUnaryExpression(e) {
		unary := e.AsPrefixUnaryExpression()
		if unary.Operator == ast.KindMinusToken {
			inner, ok := numberArg(p, unary.Operand)
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
					return numberArg(p, initializer)
				}
			}
		}
	}
	return 0, false
}

// stringArg is stringArg in the TS source (chain_args.ts).
func stringArg(e *ast.Node) (string, bool) {
	if ast.IsStringLiteral(e) {
		return e.AsStringLiteral().Text, true
	}
	if ast.IsNoSubstitutionTemplateLiteral(e) {
		return e.AsNoSubstitutionTemplateLiteral().Text, true
	}
	return "", false
}

// stringList is stringList in the TS source (chain_args.ts).
func stringList(e *ast.Node) ([]string, bool) {
	bare := e
	if ast.IsAsExpression(bare) {
		bare = bare.AsAsExpression().Expression
	}
	if !ast.IsArrayLiteralExpression(bare) {
		return nil, false
	}
	var out []string
	for _, element := range bare.AsArrayLiteralExpression().Elements.Nodes {
		s, ok := stringArg(element)
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
