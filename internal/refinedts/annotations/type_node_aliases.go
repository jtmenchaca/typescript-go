// Type aliases, enums, type parameters, infer-conditionals, and the
// surface's `z.infer` / `z.Gte` family — names that resolve to a
// stated annotation rather than a structural type node.
//
// Ported 1:1 from annotations/type_node_aliases.ts.

package annotations

import (
	"strconv"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/jsnum"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// annotationOfTypeAliases is annotationOfTypeAliases in the TS
// source. matched=false means "not an alias-shaped node".
func annotationOfTypeAliases(p *program.CheckerProgram, typeNode *ast.Node, registry AnnotationRegistry, objects ObjectRegistry, bindings map[*ast.Symbol]*DeclaredRefinement) (AnnotationOfTypeResult, bool) {
	// `X extends { k: infer R } ? A : B` -- the infer-extracting
	// conditional: when every member of the pattern is an infer slot
	// and X reads as an object carrying those keys, each slot binds
	// its key's statement and the TRUE branch reads under the
	// bindings; a missing key takes the FALSE branch. Any other
	// conditional stays plain TypeScript -- never guessed at.
	if ast.IsConditionalTypeNode(typeNode) {
		cond := typeNode.AsConditionalTypeNode()
		if !ast.IsTypeLiteralNode(cond.ExtendsType) {
			return AnnotationOfTypeResult{}, true
		}
		checked := annotationOfType(p, cond.CheckType, registry, objects, bindings)
		if checked.Stated == nil {
			return checked, true
		}
		if checked.Stated.Kind != DeclaredObject {
			return AnnotationOfTypeResult{}, true
		}
		byName := map[string]ObjectKeySpec{}
		for _, key := range checked.Stated.Object.Keys {
			byName[key.Name] = key
		}
		child := map[*ast.Symbol]*DeclaredRefinement{}
		for k, v := range bindings {
			child[k] = v
		}
		for _, member := range cond.ExtendsType.AsTypeLiteralNode().Members.Nodes {
			if !ast.IsPropertySignatureDeclaration(member) {
				return AnnotationOfTypeResult{}, true
			}
			sig := member.AsPropertySignatureDeclaration()
			if sig.Type == nil || !ast.IsInferTypeNode(sig.Type) {
				return AnnotationOfTypeResult{}, true
			}
			if !ast.IsIdentifier(sig.Name()) && !ast.IsStringLiteral(sig.Name()) {
				return AnnotationOfTypeResult{}, true
			}
			var memberName string
			if ast.IsIdentifier(sig.Name()) {
				memberName = sig.Name().AsIdentifier().Text
			} else {
				memberName = sig.Name().AsStringLiteral().Text
			}
			key, ok := byName[memberName]
			if !ok {
				return annotationOfType(p, cond.FalseType, registry, objects, bindings), true
			}
			var slot *DeclaredRefinement
			if key.Value.Kind == KeyValueSet {
				slot = &DeclaredRefinement{Kind: DeclaredSet, Set: key.Value.Set, KindTag: key.Value.KindTag}
			} else if key.Value.Kind == KeyValueObject {
				slot = &DeclaredRefinement{Kind: DeclaredObject, Object: key.Value.Object}
			}
			if slot == nil {
				return AnnotationOfTypeResult{}, true
			}
			inferTypeParamName := sig.Type.AsInferTypeNode().TypeParameter.AsTypeParameterDeclaration().Name()
			inferSymbol := symbolAt(p.Checker, inferTypeParamName)
			if inferSymbol == nil {
				return AnnotationOfTypeResult{}, true
			}
			child[inferSymbol] = slot
		}
		return annotationOfType(p, cond.TrueType, registry, objects, child), true
	}

	if !ast.IsTypeReferenceNode(typeNode) {
		return AnnotationOfTypeResult{}, false
	}
	name := typeNode.AsTypeReferenceNode().TypeName

	if ast.IsQualifiedName(name) {
		qualified := name.AsQualifiedName()
		if !resolvesToAnnotationRoot(p, qualified.Left) {
			return AnnotationOfTypeResult{}, true
		}
		// a DIALECT type the checker cannot read (z.ZodType
		// annotations, an infer over an unread schema) is plain
		// TypeScript -- silent, never a loud refusal
		zod := libraryAdapterOfNode(p, qualified.Left) != nil
		// z.Gte<"name"> and family -- the dependent bounds against
		// the named sibling parameter's value. The base here is the
		// whole ground; an intersection supplies the rest.
		rightText := qualified.Right.Text()
		var dependentOp string
		switch rightText {
		case "Gte":
			dependentOp = "ge"
		case "Gt":
			dependentOp = "gt"
		case "Lte":
			dependentOp = "le"
		case "Lt":
			dependentOp = "lt"
		}
		if dependentOp != "" && !zod {
			typeArgs := typeNode.AsTypeReferenceNode().TypeArguments
			if typeArgs != nil && len(typeArgs.Nodes) > 0 {
				argument := typeArgs.Nodes[0]
				if ast.IsLiteralTypeNode(argument) && ast.IsStringLiteral(argument.AsLiteralTypeNode().Literal) {
					return AnnotationOfTypeResult{Stated: &DeclaredRefinement{
						Kind: DeclaredSet,
						Set:  setPtr(refinementsets.MakeRefinedSet()),
						Depends: []DependentBound{
							{Op: dependentOp, Param: argument.AsLiteralTypeNode().Literal.AsStringLiteral().Text},
						},
					}}, true
				}
			}
			return AnnotationOfTypeResult{Unsupported: "z." + rightText + " takes the sibling parameter's name as a string literal"}, true
		}
		if rightText != "infer" {
			if zod {
				return AnnotationOfTypeResult{}, true
			}
			return AnnotationOfTypeResult{Unsupported: "z." + rightText + " is not a readable type"}, true
		}
		typeArgs := typeNode.AsTypeReferenceNode().TypeArguments
		if typeArgs == nil || len(typeArgs.Nodes) == 0 || !ast.IsTypeQueryNode(typeArgs.Nodes[0]) {
			if zod {
				return AnnotationOfTypeResult{}, true
			}
			return AnnotationOfTypeResult{Unsupported: "z.infer takes `typeof X` for a stated annotation X"}, true
		}
		exprName := typeArgs.Nodes[0].AsTypeQueryNode().ExprName
		if !ast.IsIdentifier(exprName) {
			if zod {
				return AnnotationOfTypeResult{}, true
			}
			return AnnotationOfTypeResult{Unsupported: "z.infer takes `typeof X` for a stated annotation X"}, true
		}
		symbol := symbolAt(p.Checker, exprName)
		if symbol != nil {
			if object := objects[symbol]; object != nil {
				return AnnotationOfTypeResult{Stated: &DeclaredRefinement{Kind: DeclaredObject, Object: object}}, true
			}
			if annotation := registry[symbol]; annotation != nil {
				return AnnotationOfTypeResult{Stated: &DeclaredRefinement{
					Kind:           DeclaredSet,
					Set:            annotation.Set,
					Refine:         annotation.Refine,
					Temporal:       annotation.Temporal,
					LibraryAdapter: annotation.LibraryAdapter,
					Measures:       annotation.Measures,
					Unread:         annotation.Unread,
					Word:           annotation.Word,
				}}, true
			}
			// an ARRAY OF RECORDS: `z.array(z.object(...))` with the
			// element stated inline, or `z.array(z.ref(X))` -- the
			// cardinality path read at a position. The old refusal
			// threw the inline statement away; inference reads it.
			records := ArrayOfRecords(p, symbol, registry, objects)
			if records.Stated != nil || records.Unsupported != "" {
				return records, true
			}
		}
		if zod {
			return AnnotationOfTypeResult{}, true
		}
		return AnnotationOfTypeResult{Unsupported: "'" + exprName.AsIdentifier().Text + "' is not a stated annotation the checker read"}, true
	}

	// a type alias may name an annotation reference -- follow it
	// (through an import alias, so `import { Port }` reads the
	// same). A GENERIC alias instantiates: each readable type
	// argument binds its parameter, and the alias body unfolds under
	// those bindings. A symbol MERGING a value and a type (`const
	// Pct` beside `type Pct`) lists the value declaration first --
	// the alias is found wherever it sits, or the annotation silently
	// went unread.
	symbol := symbolAt(p.Checker, name)
	var declaration *ast.Node
	if symbol != nil {
		for _, d := range symbol.Declarations {
			if ast.IsTypeAliasDeclaration(d) {
				declaration = d
				break
			}
		}
		if declaration == nil && len(symbol.Declarations) > 0 {
			declaration = symbol.Declarations[0]
		}
	}
	if declaration != nil && ast.IsTypeAliasDeclaration(declaration) {
		aliasDecl := declaration.AsTypeAliasDeclaration()
		inner := bindings
		typeArgs := typeNode.AsTypeReferenceNode().TypeArguments
		if aliasDecl.TypeParameters != nil && typeArgs != nil {
			child := map[*ast.Symbol]*DeclaredRefinement{}
			for k, v := range bindings {
				child[k] = v
			}
			for i, parameter := range aliasDecl.TypeParameters.Nodes {
				if i >= len(typeArgs.Nodes) {
					break
				}
				argument := typeArgs.Nodes[i]
				read := annotationOfType(p, argument, registry, objects, bindings)
				if read.Stated == nil {
					continue
				}
				parameterSymbol := symbolAt(p.Checker, parameter.AsTypeParameterDeclaration().Name())
				if parameterSymbol != nil {
					child[parameterSymbol] = read.Stated
				}
			}
			inner = child
		}
		return annotationOfType(p, aliasDecl.Type, registry, objects, inner), true
	}
	// a TS ENUM used as a type: the members are the values -- string
	// initializers, numeric initializers, and the auto-increment
	// defaults -- so the position states their union
	if declaration != nil && ast.IsEnumDeclaration(declaration) {
		type piece struct {
			set   refinementsets.RefinedSet
			label string
		}
		var pieces []piece
		next := 0.0
		for _, member := range declaration.AsEnumDeclaration().Members.Nodes {
			initializer := member.AsEnumMember().Initializer
			if initializer == nil {
				pieces = append(pieces, piece{set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{next})), label: jsnum.Number(next).String()})
				next++
				continue
			}
			if ast.IsStringLiteral(initializer) {
				text := initializer.AsStringLiteral().Text
				pieces = append(pieces, piece{set: refinementsets.StringTuple(text), label: strconv.Quote(text)})
				continue
			}
			if ast.IsNumericLiteral(initializer) {
				v := float64(jsnum.FromString(initializer.AsNumericLiteral().Text))
				pieces = append(pieces, piece{set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{v})), label: jsnum.Number(v).String()})
				next = v + 1
				continue
			}
			return AnnotationOfTypeResult{}, true // a computed member: plain TypeScript
		}
		if len(pieces) == 0 {
			return AnnotationOfTypeResult{}, true
		}
		set := pieces[0].set
		label := pieces[0].label
		for _, p := range pieces[1:] {
			set = refinementsets.MakeRefinedSet(refinementsets.Union(set, p.set))
			label += " | " + p.label
		}
		return AnnotationOfTypeResult{Stated: &DeclaredRefinement{
			Kind: DeclaredSet, Set: setPtr(set),
			Word: &WordSpelling{Text: label, Covers: len(set.Forms)},
		}}, true
	}
	// a type parameter under an INSTANTIATION binding reads as its
	// bound argument -- how a generic alias's body sees Vitals where
	// it wrote T
	if symbol != nil {
		if bound, ok := bindings[symbol]; ok {
			return AnnotationOfTypeResult{Stated: bound}, true
		}
	}
	// a type parameter is a REFINEMENT VARIABLE: `T extends B`
	// states T subset-of B; unconstrained, the bound is the root.
	// Only a bound read from a stated annotation is grounded (checked
	// against); `extends number` bounds by R-bar without grounding.
	if symbol != nil && declaration != nil && ast.IsTypeParameterDeclaration(declaration) {
		bound := refinementsets.MakeRefinedSet()
		boundGrounded := false
		var boundObject *ObjectAnnotation
		constraint := declaration.AsTypeParameterDeclaration().Constraint
		if constraint != nil {
			if constraint.Kind == ast.KindNumberKeyword {
				bound = refinementsets.Numbers
			} else {
				read := annotationOfType(p, constraint, registry, objects, nil)
				if read.Unsupported != "" {
					return read, true
				}
				if read.Stated != nil && read.Stated.Kind == DeclaredSet {
					if read.Stated.Temporal != nil && (read.Stated.Temporal.HasMin || read.Stated.Temporal.HasMax) {
						// a bound's chart constraints would go
						// silently unjudged
						return AnnotationOfTypeResult{Unsupported: "chart bounds on a type-parameter bound are not checked — " +
							"state the bound where the value is read"}, true
					}
					bound = derefSet(read.Stated.Set)
					boundGrounded = true
				}
				// `T extends <object annotation>`: T subset-of the
				// object -- every argument owes the object's
				// obligations, and a key read through T wears its
				// key's statement
				if read.Stated != nil && read.Stated.Kind == DeclaredObject {
					boundObject = read.Stated.Object
				}
				// a variable bound stays ungrounded: plain TS
			}
		}
		return AnnotationOfTypeResult{Stated: &DeclaredRefinement{
			Kind: DeclaredVariable, Symbol: symbol, Bound: setPtr(bound), BoundGrounded: boundGrounded,
			BoundObject: boundObject, StarDepth: 0,
		}}, true
	}
	return AnnotationOfTypeResult{}, true
}
