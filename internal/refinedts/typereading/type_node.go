// Syntax adapter: *ast.Node (a TypeNode) → AbstractValue. Callers use
// ReadTypeNode / ReadDeclaredType, not this file.
//
// The TS source reads through a CheckerProgram (p.host) -- the
// CheckerHost/tsgo-oracle adapter. Per the port's convention, that
// adapter does not port: this file reads *checker.Checker directly.

package typereading

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/jsnum"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// ReadTypeNode is readTypeNode in the TS source.
func ReadTypeNode(c *checker.Checker, node *ast.Node, at *ast.Node, depth int) (abstractdomain.AbstractValue, bool) {
	switch node.Kind {
	case ast.KindStringKeyword:
		return StringGround(), true
	case ast.KindNumberKeyword:
		return NumberWithNaN(), true
	case ast.KindBooleanKeyword:
		return BooleanCodes(), true
	case ast.KindSymbolKeyword:
		return UnknownSymbol(), true
	}
	if ast.IsLiteralTypeNode(node) {
		literal := node.AsLiteralTypeNode().Literal
		if ast.IsStringLiteral(literal) {
			return abstractdomain.KnownValues(
				refinementsets.CodepointsOf(literal.AsStringLiteral().Text),
				abstractdomain.PrimitiveString,
				abstractdomain.TrustProved,
			), true
		}
		if ast.IsNumericLiteral(literal) {
			return abstractdomain.KnownValues(
				[]float64{float64(jsnum.FromString(literal.AsNumericLiteral().Text))},
				abstractdomain.PrimitiveNumber,
				abstractdomain.TrustProved,
			), true
		}
		return abstractdomain.AbstractValue{}, false
	}
	if ast.IsParenthesizedTypeNode(node) {
		return ReadTypeNode(c, node.AsParenthesizedTypeNode().Type, at, depth)
	}
	if ast.IsTypeOperatorNode(node) {
		return ReadTypeNode(c, node.AsTypeOperatorNode().Type, at, depth)
	}
	if ast.IsArrayTypeNode(node) {
		inner, ok := ReadTypeNode(c, node.AsArrayTypeNode().ElementType, at, depth+1)
		if !ok {
			return abstractdomain.AbstractValue{}, false
		}
		return StarOfElement(inner)
	}
	if ast.IsTypeReferenceNode(node) {
		typeName := node.AsTypeReferenceNode().TypeName
		aliasSymbol := symbolAt(c, typeName)
		if aliasSymbol != nil {
			for _, d := range aliasSymbol.Declarations {
				if ast.IsTypeAliasDeclaration(d) {
					aliasType := d.AsTypeAliasDeclaration().Type
					if ast.IsUnionTypeNode(aliasType) {
						return ReadTypeNode(c, aliasType, at, depth)
					}
					break
				}
			}
			for _, d := range aliasSymbol.Declarations {
				if ast.IsEnumDeclaration(d) {
					return EnumValuesState(d.AsEnumDeclaration())
				}
			}
		}
	}
	if ast.IsUnionTypeNode(node) {
		var arms []abstractdomain.AbstractValue
		sawAbsent := false
		for _, branch := range node.AsUnionTypeNode().Types.Nodes {
			if branch.Kind == ast.KindUndefinedKeyword ||
				(ast.IsLiteralTypeNode(branch) && branch.AsLiteralTypeNode().Literal.Kind == ast.KindNullKeyword) {
				sawAbsent = true
				continue
			}
			inner, ok := ReadTypeNode(c, branch, at, depth)
			if !ok {
				return abstractdomain.AbstractValue{}, false
			}
			arms = append(arms, inner)
		}
		present, ok := PresentUnion(arms, sawAbsent)
		if !ok {
			return abstractdomain.AbstractValue{}, false
		}
		if sawAbsent {
			return present, true
		}
		if len(arms) > 1 {
			return present, true
		}
		return abstractdomain.AbstractValue{}, false
	}
	if depth < 2 {
		members := membersOf(c, node)
		if members != nil {
			var keys []abstractdomain.ObjectKey
			seededAny := false
			for _, member := range members {
				if !ast.IsPropertySignatureDeclaration(member) {
					continue
				}
				propertySignature := member.AsPropertySignatureDeclaration()
				if propertySignature.Type == nil || !ast.IsIdentifier(propertySignature.Name()) {
					continue
				}
				inner, ok := ReadTypeNode(c, propertySignature.Type, at, depth+1)
				if !ok {
					continue
				}
				if propertySignature.PostfixToken != nil {
					inner = abstractdomain.PossiblyUndefined(inner, "", false, false)
				}
				keys = append(keys, abstractdomain.ObjectKey{
					Name:  propertySignature.Name().AsIdentifier().Text,
					Value: inner,
				})
				seededAny = true
			}
			if seededAny {
				return abstractdomain.KnownObject(keys, nil, false, abstractdomain.TrustProved, false), true
			}
		}
	}
	return abstractdomain.AbstractValue{}, false
}

// membersOf is membersOf in the TS source.
func membersOf(c *checker.Checker, node *ast.Node) []*ast.Node {
	if ast.IsTypeLiteralNode(node) {
		return node.AsTypeLiteralNode().Members.Nodes
	}
	if ast.IsTypeReferenceNode(node) {
		typeName := node.AsTypeReferenceNode().TypeName
		symbol := c.GetSymbolAtLocation(typeName)
		if c.SymbolInDefaultLib(symbol) {
			return nil
		}
		if symbol == nil {
			return nil
		}
		var interfaceMembers []*ast.Node
		for _, d := range symbol.Declarations {
			if ast.IsInterfaceDeclaration(d) {
				interfaceMembers = append(interfaceMembers, d.AsInterfaceDeclaration().Members.Nodes...)
			}
		}
		if len(interfaceMembers) > 0 {
			return interfaceMembers
		}
		for _, d := range symbol.Declarations {
			if ast.IsTypeAliasDeclaration(d) {
				aliasType := d.AsTypeAliasDeclaration().Type
				if ast.IsTypeLiteralNode(aliasType) {
					return aliasType.AsTypeLiteralNode().Members.Nodes
				}
				return nil
			}
		}
	}
	return nil
}

// symbolAt is symbolAt in the TS source (service/program_resolution.ts):
// the symbol behind a node, followed THROUGH import aliases -- an
// imported name resolves to its declaration in the exporting file.
// Inlined here rather than ported from service/ (out of scope per
// PORT.md's adapter-layer rule) since the alias-following itself is
// two checker calls, not adapter plumbing.
func symbolAt(c *checker.Checker, node *ast.Node) *ast.Symbol {
	symbol := c.GetSymbolAtLocation(node)
	if symbol != nil && (symbol.Flags&ast.SymbolFlagsAlias) != 0 {
		symbol = c.GetAliasedSymbol(symbol)
	}
	return symbol
}
