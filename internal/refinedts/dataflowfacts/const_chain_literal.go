// Resolving an expression through const-to-const links to the literal
// it names.
//
// The sites that need a literal (loop bounds, case labels, product
// guards, comparison operands, loop conditions, element-access
// indices) each used to test the expression for a literal token and
// stop there, so `const N = 10` read as nothing. This file holds the
// one resolver they all call.
//
// It sits in this package rather than in walk because the place
// reading here needs it too: an element access whose index is a
// const-bound literal (`const i = 0; xs[i]`) names one slot, and walk
// imports this package, so the resolver has to live at or below it.

package dataflowfacts

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/jsnum"
	"github.com/microsoft/typescript-go/internal/refinedts/tracing"
)

// ConstChainDepth is how many const-to-const links the resolver
// follows before it stops — the same cap literalKnown uses.
const ConstChainDepth = 8

// constChainSymbolAt is symbolAt in the TS source
// (service/program_resolution.ts): the symbol behind a node, followed
// THROUGH import aliases — an imported name resolves to its
// declaration in the exporting file, so registries keyed by
// declaration symbols answer for imported names too. Inlined per
// package per PORT.md (no ready-made wrapper yet;
// service/program_resolution.ts is not ported).
func constChainSymbolAt(c *checker.Checker, node *ast.Node) *ast.Symbol {
	tracing.CountBy("host.symbolAtLocation", 1)
	symbol := c.GetSymbolAtLocation(node)
	if symbol != nil && (symbol.Flags&ast.SymbolFlagsAlias) != 0 {
		tracing.CountBy("host.aliasedSymbol", 1)
		symbol = c.GetAliasedSymbol(symbol)
	}
	return symbol
}

// constChainNumberOf is a literal number, through a leading minus —
// read through jsnum.FromString (the checker's own ECMA
// StringToNumber), never strconv.ParseFloat, which diverges on
// hex/octal/binary prefixes (PORT.md).
func constChainNumberOf(e *ast.Node) (float64, bool) {
	if ast.IsNumericLiteral(e) {
		return float64(jsnum.FromString(e.AsNumericLiteral().Text)), true
	}
	if ast.IsPrefixUnaryExpression(e) {
		unary := e.AsPrefixUnaryExpression()
		if unary.Operator == ast.KindMinusToken && ast.IsNumericLiteral(unary.Operand) {
			return -float64(jsnum.FromString(unary.Operand.AsNumericLiteral().Text)), true
		}
	}
	return 0, false
}

// ConstChainLiteral follows an expression to the literal token it
// names and returns that token: the expression itself when it is
// already a literal, or the initializer reached by following an
// identifier through const-to-const links.
//
// It only fires for a CONST binding whose initializer chain is
// entirely const declarations with literal or identifier
// initializers. A let, a var, a parameter, an import, or a computed
// initializer anywhere in the chain declines, because only a const
// bound to a literal holds that literal at every reachable point —
// any other binding can hold a different value where the caller
// reads it. A nil checker declines every identifier, which leaves
// the caller reading literal tokens only.
//
// Returns the literal node and true, or nil and false.
func ConstChainLiteral(c *checker.Checker, e *ast.Node) (*ast.Node, bool) {
	return constChainLiteral(c, e, 0)
}

func constChainLiteral(c *checker.Checker, e *ast.Node, depth int) (*ast.Node, bool) {
	if e == nil {
		return nil, false
	}
	if ast.IsParenthesizedExpression(e) {
		return constChainLiteral(c, e.AsParenthesizedExpression().Expression, depth)
	}
	if isLiteralToken(e) {
		return e, true
	}
	if c == nil || !ast.IsIdentifier(e) || depth >= ConstChainDepth {
		return nil, false
	}
	initializer, ok := ConstInitializerOf(c, e)
	if !ok {
		return nil, false
	}
	return constChainLiteral(c, initializer, depth+1)
}

// isLiteralToken is the set of tokens the resolver reads as a value:
// a numeric literal (through a leading minus), a string literal, a
// no-substitution template, and the two boolean keywords.
func isLiteralToken(e *ast.Node) bool {
	if ast.IsNumericLiteral(e) || ast.IsStringLiteral(e) || ast.IsNoSubstitutionTemplateLiteral(e) {
		return true
	}
	if e.Kind == ast.KindTrueKeyword || e.Kind == ast.KindFalseKeyword {
		return true
	}
	if ast.IsPrefixUnaryExpression(e) {
		unary := e.AsPrefixUnaryExpression()
		return unary.Operator == ast.KindMinusToken && ast.IsNumericLiteral(unary.Operand)
	}
	return false
}

// FalsyLiteral is whether a literal token this resolver reads denotes a
// value ToBoolean sends to false: the `false` keyword, a numeric literal
// whose value is zero (0, 0.0, and -0 alike — the minus form arrives as
// the prefix expression, and −0 is falsy), and the empty string. Every
// other literal is truthy, and a non-literal node answers false because
// this reading says nothing about it.
func FalsyLiteral(e *ast.Node) bool {
	if e == nil {
		return false
	}
	if e.Kind == ast.KindFalseKeyword {
		return true
	}
	if ast.IsStringLiteral(e) {
		return e.AsStringLiteral().Text == ""
	}
	if ast.IsNoSubstitutionTemplateLiteral(e) {
		return e.Text() == ""
	}
	if n, ok := constChainNumberOf(e); ok {
		return n == 0
	}
	return false
}

// ConstInitializerOf reads the initializer of the const declaration an
// identifier resolves to. A binding that is not a const variable
// declaration with an initializer answers nothing, which is what
// stops a let, a var, a parameter, and an import.
func ConstInitializerOf(c *checker.Checker, name *ast.Node) (*ast.Node, bool) {
	symbol := constChainSymbolAt(c, name)
	if symbol == nil || symbol.ValueDeclaration == nil {
		return nil, false
	}
	declaration := symbol.ValueDeclaration
	if !ast.IsVariableDeclaration(declaration) {
		return nil, false
	}
	if declaration.Parent == nil || !ast.IsVariableDeclarationList(declaration.Parent) {
		return nil, false
	}
	if (declaration.Parent.Flags & ast.NodeFlagsConst) == 0 {
		return nil, false
	}
	initializer := declaration.AsVariableDeclaration().Initializer
	if initializer == nil {
		return nil, false
	}
	return initializer, true
}

// ConstChainNumber is the number a const chain names: the resolver
// followed by the literal number reading, for the sites that want the
// value rather than the token.
func ConstChainNumber(c *checker.Checker, e *ast.Node) (float64, bool) {
	literal, ok := ConstChainLiteral(c, e)
	if !ok {
		return 0, false
	}
	return constChainNumberOf(literal)
}
