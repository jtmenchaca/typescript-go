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
	"github.com/microsoft/typescript-go/internal/refinedts/diagnose"
	"github.com/microsoft/typescript-go/internal/refinedts/nameresolution"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/tracing"
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
	// a TUPLE type node — `[Age, Wide]` — states each position's own
	// type at an exact length, which is a strictly stronger claim than
	// the array node's star above: the slots may disagree, and reading
	// them per-position keeps that. The host reader already makes this
	// distinction on the RESOLVED side (host_type.go's IsTupleType
	// branch, which reads a required-element tuple positionally before
	// falling to the joined star); this is the SYNTAX side of the same
	// rule, and it matters because a written alias like `Age` resolves
	// to its refined set here while the host reader only ever sees the
	// underlying `number` a branded alias erases to.
	//
	// Only the all-required form, exactly as the host branch gates it:
	// an optional (`T?`), rest (`...T[]`), or named slot costs the
	// exact position-to-length pairing this reading depends on, and
	// such a tuple falls through to the host road rather than being
	// read here with a length it does not have.
	if ast.IsTupleTypeNode(node) {
		elements := node.AsTupleTypeNode().Elements
		if elements == nil || len(elements.Nodes) == 0 {
			return abstractdomain.AbstractValue{}, false
		}
		items := make([]abstractdomain.AbstractValue, 0, len(elements.Nodes))
		for _, element := range elements.Nodes {
			if ast.IsOptionalTypeNode(element) || ast.IsRestTypeNode(element) ||
				ast.IsNamedTupleMember(element) {
				return abstractdomain.AbstractValue{}, false
			}
			inner, ok := ReadTypeNode(c, element, at, depth+1)
			if !ok {
				return abstractdomain.AbstractValue{}, false
			}
			items = append(items, inner)
		}
		return abstractdomain.KnownList(items, abstractdomain.TrustProved), true
	}
	if ast.IsTypeReferenceNode(node) {
		typeName := node.AsTypeReferenceNode().TypeName
		// the declaration resolves by SYNTAX first (nameresolution's
		// file comment): the checker's resolver is not load-bearing
		// for a written name, and its answer was measured drifting
		// under concurrent per-entry checkers. The checker answers
		// only what syntax cannot see.
		if d := nameresolution.TypeDeclarationOf(c.BoundProgram(), typeName); d != nil {
			if ast.IsTypeAliasDeclaration(d) {
				aliasType := d.AsTypeAliasDeclaration().Type
				if ast.IsUnionTypeNode(aliasType) {
					value, ok := ReadTypeNode(c, aliasType, at, depth)
					diagnose.LogIf(diagnose.EventOn("typeread.typeNode"), "typeread.typeNode",
						"typeName", diagnose.NodeText(typeName), "road", "syntax", "declKind", "typeAlias.union",
						"ok", ok, "value", inlineSpellingOrEmpty(value, ok))
					return value, ok
				}
			} else if ast.IsEnumDeclaration(d) {
				value, ok := EnumValuesState(d.AsEnumDeclaration())
				diagnose.LogIf(diagnose.EventOn("typeread.typeNode"), "typeread.typeNode",
					"typeName", diagnose.NodeText(typeName), "road", "syntax", "declKind", "enum",
					"ok", ok, "value", inlineSpellingOrEmpty(value, ok))
				return value, ok
			}
			diagnose.LogIf(diagnose.EventOn("typeread.typeNode"), "typeread.typeNode",
				"typeName", diagnose.NodeText(typeName), "road", "syntax", "declKind", d.Kind.String(),
				"ok", false, "value", "<fallthrough>")
		} else if aliasSymbol := symbolAt(c, typeName); aliasSymbol != nil {
			for _, d := range aliasSymbol.Declarations {
				if ast.IsTypeAliasDeclaration(d) {
					aliasType := d.AsTypeAliasDeclaration().Type
					if ast.IsUnionTypeNode(aliasType) {
						value, ok := ReadTypeNode(c, aliasType, at, depth)
						diagnose.LogIf(diagnose.EventOn("typeread.typeNode"), "typeread.typeNode",
							"typeName", diagnose.NodeText(typeName), "road", "checker", "declKind", "typeAlias.union",
							"ok", ok, "value", inlineSpellingOrEmpty(value, ok))
						return value, ok
					}
					break
				}
			}
			for _, d := range aliasSymbol.Declarations {
				if ast.IsEnumDeclaration(d) {
					value, ok := EnumValuesState(d.AsEnumDeclaration())
					diagnose.LogIf(diagnose.EventOn("typeread.typeNode"), "typeread.typeNode",
						"typeName", diagnose.NodeText(typeName), "road", "checker", "declKind", "enum",
						"ok", ok, "value", inlineSpellingOrEmpty(value, ok))
					return value, ok
				}
			}
			diagnose.LogIf(diagnose.EventOn("typeread.typeNode"), "typeread.typeNode",
				"typeName", diagnose.NodeText(typeName), "road", "checker", "declKind", "<none-matched>",
				"ok", false, "value", "<fallthrough>")
		} else {
			diagnose.LogIf(diagnose.EventOn("typeread.typeNode"), "typeread.typeNode",
				"typeName", diagnose.NodeText(typeName), "road", "<none>", "declKind", "<none>",
				"ok", false, "value", "<fallthrough>")
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
		tracing.CountBy("host.symbolAtLocation", 1)
		symbol := c.GetSymbolAtLocation(typeName)
		tracing.CountBy("host.symbolInDefaultLib", 1)
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
	// the binder's own tables answer first (nameresolution's file
	// comment) — the checker settles only what they cannot
	if s := nameresolution.DeclarationSymbolOf(c.BoundProgram(), node); s != nil {
		diagnose.LogIf(diagnose.EventOn("typeread.symbolAt"), "typeread.symbolAt",
			"node", node.Text(), "road", "binder", "found", true)
		return s
	}
	tracing.CountBy("host.symbolAtLocation", 1)
	symbol := c.GetSymbolAtLocation(node)
	followedAlias := false
	if symbol != nil && (symbol.Flags&ast.SymbolFlagsAlias) != 0 {
		tracing.CountBy("host.aliasedSymbol", 1)
		symbol = c.GetAliasedSymbol(symbol)
		followedAlias = true
	}
	diagnose.LogIf(diagnose.EventOn("typeread.symbolAt"), "typeread.symbolAt",
		"node", node.Text(), "road", "checker", "followedAlias", followedAlias, "found", symbol != nil)
	return symbol
}
