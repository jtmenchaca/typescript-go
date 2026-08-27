// from service/hover_plain_type.ts
//
// Plain-/literal-bearing type walks that decide whether a type node
// states anything the refinement reader could miss.

package service

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/tracing"
)

// PlainTypeNode is plainTypeNode in the TS source: a type node with no
// refinement to read — a keyword type, an array of one, a literal, a
// shape (object literal, interface, class, function), or unions,
// intersections, and tuples of those. Everything the type says is the
// type itself. The seen-set breaks recursive shapes.
func PlainTypeNode(p *program.CheckerProgram, node *ast.Node, seen map[*ast.Node]bool) bool {
	switch node.Kind {
	case ast.KindNumberKeyword, ast.KindStringKeyword, ast.KindBooleanKeyword,
		ast.KindBigIntKeyword, ast.KindUnknownKeyword, ast.KindAnyKeyword,
		ast.KindObjectKeyword, ast.KindUndefinedKeyword, ast.KindVoidKeyword,
		ast.KindNeverKeyword, ast.KindSymbolKeyword:
		return true
	}
	if ast.IsLiteralTypeNode(node) {
		return true
	}
	if ast.IsParenthesizedTypeNode(node) {
		return PlainTypeNode(p, node.AsParenthesizedTypeNode().Type, seen)
	}
	if ast.IsArrayTypeNode(node) {
		return PlainTypeNode(p, node.AsArrayTypeNode().ElementType, seen)
	}
	if ast.IsTypeOperatorNode(node) {
		return PlainTypeNode(p, node.AsTypeOperatorNode().Type, seen)
	}
	if ast.IsUnionTypeNode(node) {
		for _, t := range node.AsUnionTypeNode().Types.Nodes {
			if !PlainTypeNode(p, t, seen) {
				return false
			}
		}
		return true
	}
	if ast.IsIntersectionTypeNode(node) {
		for _, t := range node.AsIntersectionTypeNode().Types.Nodes {
			if !PlainTypeNode(p, t, seen) {
				return false
			}
		}
		return true
	}
	if ast.IsTupleTypeNode(node) {
		for _, element := range node.AsTupleTypeNode().Elements.Nodes {
			held := element
			if ast.IsNamedTupleMember(element) {
				held = element.AsNamedTupleMember().Type
			}
			if !PlainTypeNode(p, held, seen) {
				return false
			}
		}
		return true
	}
	// an inline object type of plain members states shape only —
	// nothing the refinement reader could have read
	if ast.IsTypeLiteralNode(node) {
		for _, member := range node.AsTypeLiteralNode().Members.Nodes {
			if !plainMember(p, member, seen) {
				return false
			}
		}
		return true
	}
	// a function type states no VALUE refinement — the checker reads
	// calls, never function values
	if ast.IsFunctionTypeNode(node) || ast.IsConstructorTypeNode(node) {
		return true
	}
	// type-level machinery — mapped, conditional — is a shape
	// computation: with no literal content anywhere inside, nothing
	// here was the refinement reader's to read
	if (ast.IsMappedTypeNode(node) || ast.IsConditionalTypeNode(node)) &&
		!containsLiteralType(node) {
		return true
	}
	// an indexed access SELECTS a member: the selected shape decides
	if ast.IsIndexedAccessTypeNode(node) {
		return PlainTypeNode(p, node.AsIndexedAccessTypeNode().ObjectType, seen)
	}
	if ast.IsTypeReferenceNode(node) {
		reference := node.AsTypeReferenceNode()
		if ast.IsIdentifier(reference.TypeName) {
			var typeArguments []*ast.Node
			if reference.TypeArguments != nil {
				typeArguments = reference.TypeArguments.Nodes
			}
			return plainReference(p, reference.TypeName, typeArguments, seen)
		}
	}
	// a heritage clause's `extends ReadonlyArray<string>` — the same
	// reference, spelled as an expression
	if ast.IsExpressionWithTypeArguments(node) {
		withArgs := node.AsExpressionWithTypeArguments()
		if ast.IsIdentifier(withArgs.Expression) {
			var typeArguments []*ast.Node
			if withArgs.TypeArguments != nil {
				typeArguments = withArgs.TypeArguments.Nodes
			}
			return plainReference(p, withArgs.Expression, typeArguments, seen)
		}
	}
	return false
}

// LiteralBearingType is literalBearingType in the TS source: whether a
// RESOLVED type carries literal content anywhere a value reader could
// state — the semantic backstop for annotations the syntax walk cannot
// follow. Unsure answers true: "may hold literals" keeps the row a
// reader gap, never a false determination.
func LiteralBearingType(p *program.CheckerProgram, t *checker.Type, depth int, seen map[*checker.Type]bool, at *ast.Node) bool {
	if t == nil {
		return false
	}
	if depth >= 4 {
		return true
	}
	if seen[t] {
		return false
	}
	seen[t] = true
	c := p.Checker
	flags := t.Flags()
	if (flags & (checker.TypeFlagsStringLiteral | checker.TypeFlagsNumberLiteral |
		checker.TypeFlagsBigIntLiteral | checker.TypeFlagsEnumLiteral)) != 0 {
		return true
	}
	// `boolean` arrives as the union of its two literals — the full
	// pair is the plain keyword; a LONE true or false is content
	if (flags & checker.TypeFlagsBoolean) != 0 {
		return false
	}
	if (flags & checker.TypeFlagsBooleanLiteral) != 0 {
		return true
	}
	if (flags & (checker.TypeFlagsUnion | checker.TypeFlagsIntersection)) != 0 {
		// `boolean | undefined` arrives flattened to {true, false,
		// undefined}: the true/false PAIR is the plain keyword again —
		// a LONE boolean literal is content
		booleanLiterals := 0
		for _, part := range t.Types() {
			if (part.Flags() & checker.TypeFlagsBooleanLiteral) != 0 {
				booleanLiterals++
			}
		}
		if booleanLiterals == 1 {
			return true
		}
		for _, part := range t.Types() {
			if (part.Flags()&checker.TypeFlagsBooleanLiteral) == 0 &&
				LiteralBearingType(p, part, depth+1, seen, at) {
				return true
			}
		}
		return false
	}
	if (flags & checker.TypeFlagsObject) != 0 {
		if (t.ObjectFlags() & checker.ObjectFlagsReference) != 0 {
			tracing.CountBy("host.typeArguments", 1)
			for _, argument := range c.GetTypeArguments(t) {
				if LiteralBearingType(p, argument, depth+1, seen, at) {
					return true
				}
			}
		}
		tracing.CountBy("host.propertiesOfType", 1)
		properties := c.GetPropertiesOfType(t)
		if len(properties) > 48 {
			return true
		}
		for _, property := range properties {
			if (property.Flags & ast.SymbolFlagsMethod) != 0 {
				continue
			}
			// a tuple's `length` is the literal count — structure, not
			// value content
			if property.Name == "length" {
				continue
			}
			// a synthetic member (a tuple slot) has no declaration; the
			// asking position stands in as the location
			declaration := property.ValueDeclaration
			if declaration == nil && len(property.Declarations) > 0 {
				declaration = property.Declarations[0]
			}
			if declaration == nil {
				declaration = at
			}
			if declaration == nil {
				return true
			}
			tracing.CountBy("host.typeOfSymbolAtLocation", 1)
			held := c.GetTypeOfSymbolAtLocation(property, declaration)
			if LiteralBearingType(p, held, depth+1, seen, at) {
				return true
			}
		}
		return false
	}
	if (flags & checker.TypeFlagsTypeParameter) != 0 {
		tracing.CountBy("host.constraintOfType", 1)
		constraint := c.GetConstraintOfType(t)
		return constraint != nil && LiteralBearingType(p, constraint, depth+1, seen, at)
	}
	// deferred machinery the checker left unresolved may hide literals
	if (flags & (checker.TypeFlagsIndex | checker.TypeFlagsIndexedAccess |
		checker.TypeFlagsConditional | checker.TypeFlagsSubstitution |
		checker.TypeFlagsTemplateLiteral | checker.TypeFlagsStringMapping)) != 0 {
		return true
	}
	// any, unknown, never, void, string, number, bigint, symbol —
	// nothing literal to read
	return false
}

// plainReference is plainReference in the TS source: a NAMED type
// reference with nothing to read — the default lib's shape utilities,
// an alias of a plain body, an interface of plain members (heritage
// followed), a class, a type parameter.
func plainReference(p *program.CheckerProgram, name *ast.Node, typeArguments []*ast.Node, seen map[*ast.Node]bool) bool {
	text := name.Text()
	if text == "Readonly" || text == "Record" || text == "Partial" || text == "Required" {
		if ResolvesToDefaultLib(p, name) {
			allPlain := true
			for _, argument := range typeArguments {
				if !PlainTypeNode(p, argument, seen) {
					allPlain = false
					break
				}
			}
			if allPlain {
				return true
			}
		}
	}
	symbol := SymbolAt(p, name)
	if symbol == nil || len(symbol.Declarations) == 0 {
		return false
	}
	declaration := symbol.Declarations[0]
	if seen[declaration] {
		return true // a recursive shape is plain
	}
	seen[declaration] = true
	// generic ARGUMENTS must be plain too — the body may place them
	// anywhere
	for _, argument := range typeArguments {
		if !PlainTypeNode(p, argument, seen) {
			return false
		}
	}
	if ast.IsTypeAliasDeclaration(declaration) {
		return PlainTypeNode(p, declaration.AsTypeAliasDeclaration().Type, seen)
	}
	if ast.IsInterfaceDeclaration(declaration) {
		interfaceDecl := declaration.AsInterfaceDeclaration()
		if interfaceDecl.HeritageClauses != nil {
			for _, clause := range interfaceDecl.HeritageClauses.Nodes {
				for _, t := range clause.AsHeritageClause().Types.Nodes {
					if !PlainTypeNode(p, t, seen) {
						return false
					}
				}
			}
		}
		for _, member := range interfaceDecl.Members.Nodes {
			if !plainMember(p, member, seen) {
				return false
			}
		}
		return true
	}
	if ast.IsClassDeclaration(declaration) {
		return true
	}
	if ast.IsTypeParameterDeclaration(declaration) {
		return true
	}
	return false
}

// plainMember is plainMember in the TS source: a shape member with
// nothing to read. A member CARRYING literal types is readable
// content, so it keeps the shape unsupported.
func plainMember(p *program.CheckerProgram, member *ast.Node, seen map[*ast.Node]bool) bool {
	if ast.IsPropertySignatureDeclaration(member) {
		memberType := member.AsPropertySignatureDeclaration().Type
		return memberType != nil &&
			!containsLiteralType(memberType) &&
			PlainTypeNode(p, memberType, seen)
	}
	if ast.IsMethodSignatureDeclaration(member) || ast.IsCallSignatureDeclaration(member) ||
		ast.IsConstructSignatureDeclaration(member) {
		return true
	}
	if ast.IsIndexSignatureDeclaration(member) {
		return PlainTypeNode(p, member.AsIndexSignatureDeclaration().Type, seen)
	}
	return false
}

// containsLiteralType is containsLiteralType in the TS source: whether
// a type node syntactically carries a literal type (the null literal
// aside — absence, not a value).
func containsLiteralType(node *ast.Node) bool {
	if ast.IsLiteralTypeNode(node) &&
		node.AsLiteralTypeNode().Literal.Kind != ast.KindNullKeyword {
		return true
	}
	found := false
	node.ForEachChild(func(child *ast.Node) bool {
		if containsLiteralType(child) {
			found = true
			return true
		}
		return false
	})
	return found
}
