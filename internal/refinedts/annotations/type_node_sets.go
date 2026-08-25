// Type-node forms that state a set or object shape: parentheses,
// intersection, union, maybe (`T | undefined`), literals, keyof,
// arrays / `Array<T>`, and inline `{ key: T }` literals.
//
// Ported 1:1 from annotations/type_node_sets.ts.

package annotations

import (
	"strconv"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/jsnum"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// starOfElement is starOfElement in the TS source: the star of a
// compiled ELEMENT -- `X[]` and `Array<X>` share it. A dependent
// element bound survives as ElementDepends; chart bounds refuse (they
// would go silently unjudged under a star).
func starOfElement(element AnnotationOfTypeResult) AnnotationOfTypeResult {
	if element.Stated == nil {
		return element // unsupported or plain TypeScript passes through
	}
	if element.Stated.Kind == DeclaredSet {
		if element.Stated.Temporal != nil && (element.Stated.Temporal.HasMin || element.Stated.Temporal.HasMax) {
			// stated chart bounds under an array position would go
			// silently unjudged -- refused loudly instead
			return AnnotationOfTypeResult{Unsupported: "chart bounds under an array position are not checked — " +
				"state the bound where the element is read"}
		}
		out := &DeclaredRefinement{
			Kind:           DeclaredSet,
			Set:            setPtr(refinementsets.MakeRefinedSet(refinementsets.Star(derefSet(element.Stated.Set)))),
			LibraryAdapter: element.Stated.LibraryAdapter,
		}
		if element.Stated.Depends != nil {
			out.ElementDepends = element.Stated.Depends
		}
		return AnnotationOfTypeResult{Stated: out}
	}
	if element.Stated.Kind == DeclaredVariable {
		out := *element.Stated
		out.StarDepth = element.Stated.StarDepth + 1
		return AnnotationOfTypeResult{Stated: &out}
	}
	return AnnotationOfTypeResult{} // arrays of objects live in the graph via z.ref
}

type literalTypeSetResult struct {
	set   refinementsets.RefinedSet
	label string
	ok    bool
	// kindTag is the sort the literal was written under — "boolean" for
	// `true` and `false`, "" for a number or string literal, which need
	// no tag (their forms already say which layer they live on). A
	// boolean literal's set is OneOf{1} or OneOf{0}, indistinguishable
	// from the numbers 1 and 0 by forms alone, so the position states
	// the sort the same way the bare `boolean` keyword arm does.
	kindTag string
}

func literalTypeSet(node *ast.Node) literalTypeSetResult {
	if !ast.IsLiteralTypeNode(node) {
		return literalTypeSetResult{}
	}
	literal := node.AsLiteralTypeNode().Literal
	if ast.IsNumericLiteral(literal) {
		text := literal.AsNumericLiteral().Text
		v := float64(jsnum.FromString(text))
		return literalTypeSetResult{set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{v})), label: text, ok: true}
	}
	if ast.IsPrefixUnaryExpression(literal) {
		unary := literal.AsPrefixUnaryExpression()
		if unary.Operator == ast.KindMinusToken && ast.IsNumericLiteral(unary.Operand) {
			v := -float64(jsnum.FromString(unary.Operand.AsNumericLiteral().Text))
			return literalTypeSetResult{set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{v})), label: jsnum.Number(v).String(), ok: true}
		}
	}
	if ast.IsStringLiteral(literal) {
		text := literal.AsStringLiteral().Text
		return literalTypeSetResult{set: refinementsets.StringTuple(text), label: strconv.Quote(text), ok: true}
	}
	if literal.Kind == ast.KindTrueKeyword {
		return literalTypeSetResult{set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{1})), label: "true", ok: true, kindTag: "boolean"}
	}
	if literal.Kind == ast.KindFalseKeyword {
		return literalTypeSetResult{set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{0})), label: "false", ok: true, kindTag: "boolean"}
	}
	return literalTypeSetResult{}
}

// annotationOfTypeSets is annotationOfTypeSets in the TS source. The
// TS union of `undefined` (not a set-shaped node) vs `null` (plain
// TypeScript) is carried as (result, matched bool): matched=false
// means "not a set-shaped node, keep reading other arms"; matched=true
// with a zero-value result.Stated/Unsupported means plain TypeScript.
func annotationOfTypeSets(p *program.CheckerProgram, typeNode *ast.Node, registry AnnotationRegistry, objects ObjectRegistry, bindings map[*ast.Symbol]*DeclaredRefinement) (AnnotationOfTypeResult, bool) {
	// a bare primitive keyword -- `string`, `number`, `boolean` with no
	// zod wrapper at all -- states its OWN ground set: every string is
	// exactly C* (refinementsets.Strings), every number is exactly R-bar
	// (refinementsets.Numbers, the same set z.number()'s own root
	// compiles to, chain_root_constructor.go's "number" case), every
	// boolean is exactly {0,1} tagged "boolean" (mirroring z.boolean()'s
	// own root, chain_root_constructor.go's "boolean" case, so a reader
	// past this point still tells a boolean from a two-member numeric
	// literal set). This is not a new claim about what TypeScript's own
	// primitive types mean -- transcribing them is the same "state the
	// spec-fixed ground fact" move typereading/recipes.go already makes
	// for evaluation (StringGround/NumberWithNaN/BooleanCodes); until
	// this arm existed, a plain `string`/`number`/`boolean` parameter or
	// return type read as nil-nil ("plain TypeScript"), so every
	// downstream consumer of a DeclaredRefinement -- assignability,
	// Grounded, the fact exporter's entry rows -- silently skipped a
	// position TypeScript itself already fully specifies.
	if typeNode.Kind == ast.KindStringKeyword {
		return AnnotationOfTypeResult{Stated: &DeclaredRefinement{Kind: DeclaredSet, Set: setPtr(refinementsets.Strings)}}, true
	}
	if typeNode.Kind == ast.KindNumberKeyword {
		return AnnotationOfTypeResult{Stated: &DeclaredRefinement{Kind: DeclaredSet, Set: setPtr(refinementsets.Numbers)}}, true
	}
	if typeNode.Kind == ast.KindBooleanKeyword {
		return AnnotationOfTypeResult{Stated: &DeclaredRefinement{
			Kind:    DeclaredSet,
			Set:     setPtr(refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{0, 1}))),
			KindTag: "boolean",
		}}, true
	}
	// (X) -- parentheses state nothing
	if ast.IsParenthesizedTypeNode(typeNode) {
		return annotationOfType(p, typeNode.AsParenthesizedTypeNode().Type, registry, objects, bindings), true
	}
	// X & Y -- the CONJUNCTION of readable set statements: the forms
	// concatenate (every member holds), and a dependent bound rides
	// alongside its base. A plain-TypeScript member adds nothing here
	// (its shape is tsc's); temporal or worn members stay plain -- the
	// merge would misread them.
	if ast.IsIntersectionTypeNode(typeNode) {
		var forms []refinementsets.Refinement
		var depends []DependentBound
		libraryAdapter := ""
		readable := 0
		for _, member := range typeNode.AsIntersectionTypeNode().Types.Nodes {
			read := annotationOfType(p, member, registry, objects, bindings)
			if read.Stated == nil && read.Unsupported == "" {
				continue // plain TypeScript
			}
			if read.Unsupported != "" {
				return read, true
			}
			if read.Stated.Kind != DeclaredSet {
				return AnnotationOfTypeResult{}, true
			}
			if (read.Stated.Temporal != nil) || read.Stated.KindTag != "" {
				return AnnotationOfTypeResult{}, true
			}
			forms = append(forms, derefSet(read.Stated.Set).Forms...)
			depends = append(depends, read.Stated.Depends...)
			if libraryAdapter == "" {
				libraryAdapter = read.Stated.LibraryAdapter
			}
			readable++
		}
		if readable == 0 {
			return AnnotationOfTypeResult{}, true
		}
		out := &DeclaredRefinement{Kind: DeclaredSet, Set: setPtr(refinementsets.MakeRefinedSet(forms...)), LibraryAdapter: libraryAdapter}
		if len(depends) > 0 {
			out.Depends = depends
		}
		return AnnotationOfTypeResult{Stated: out}, true
	}
	// X[] -- the star of the element (a variable element marks the
	// position `T[]`; an object element stays outside the tuple
	// layer)
	if ast.IsArrayTypeNode(typeNode) {
		element := annotationOfType(p, typeNode.AsArrayTypeNode().ElementType, registry, objects, bindings)
		if element.Stated == nil {
			return element, true
		}
		return starOfElement(element), true
	}
	// keyof over a readable object SHAPE: the union of its key words
	// -- exact strings the set language speaks
	if ast.IsTypeOperatorNode(typeNode) && typeNode.AsTypeOperatorNode().Operator == ast.KindKeyOfKeyword {
		target := typeNode.AsTypeOperatorNode().Type
		if ast.IsTypeReferenceNode(target) && ast.IsIdentifier(target.AsTypeReferenceNode().TypeName) {
			symbol := symbolAt(p.Checker, target.AsTypeReferenceNode().TypeName)
			if symbol != nil && len(symbol.Declarations) > 0 {
				if declaration := symbol.Declarations[0]; ast.IsTypeAliasDeclaration(declaration) {
					target = declaration.AsTypeAliasDeclaration().Type
				}
			}
		}
		if ast.IsTypeLiteralNode(target) {
			var names []string
			for _, member := range target.AsTypeLiteralNode().Members.Nodes {
				if !ast.IsPropertySignatureDeclaration(member) {
					return AnnotationOfTypeResult{}, true
				}
				name := member.AsPropertySignatureDeclaration().Name()
				if ast.IsIdentifier(name) {
					names = append(names, name.AsIdentifier().Text)
				} else if ast.IsStringLiteral(name) {
					names = append(names, name.AsStringLiteral().Text)
				} else {
					return AnnotationOfTypeResult{}, true
				}
			}
			if len(names) == 0 {
				return AnnotationOfTypeResult{}, true
			}
			set := refinementsets.StringTuple(names[0])
			for _, name := range names[1:] {
				set = refinementsets.MakeRefinedSet(refinementsets.Union(set, refinementsets.StringTuple(name)))
			}
			return AnnotationOfTypeResult{Stated: &DeclaredRefinement{Kind: DeclaredSet, Set: setPtr(set)}}, true
		}
		return AnnotationOfTypeResult{}, true
	}

	// a LITERAL type -- `3`, `"big"`, `true` -- and unions of them
	if single := literalTypeSet(typeNode); single.ok {
		return AnnotationOfTypeResult{Stated: &DeclaredRefinement{
			Kind: DeclaredSet, Set: setPtr(single.set), KindTag: single.kindTag,
			Word: &WordSpelling{Text: single.label, Covers: len(single.set.Forms)},
		}}, true
	}
	// X | undefined (or | null): the inner statement, possibly absent
	// -- exactly one readable branch plus absent branches reads as
	// maybe
	if ast.IsUnionTypeNode(typeNode) {
		// the ALL-LITERAL union first: every present branch a literal
		// type -- the union of their singletons, absence riding
		// beside
		{
			var pieces []literalTypeSetResult
			absent := false
			allLiteral := true
			for _, branch := range typeNode.AsUnionTypeNode().Types.Nodes {
				if branch.Kind == ast.KindUndefinedKeyword ||
					(ast.IsLiteralTypeNode(branch) && branch.AsLiteralTypeNode().Literal.Kind == ast.KindNullKeyword) {
					absent = true
					continue
				}
				piece := literalTypeSet(branch)
				if !piece.ok {
					allLiteral = false
					break
				}
				pieces = append(pieces, piece)
			}
			if allLiteral && len(pieces) > 0 {
				set := pieces[0].set
				label := pieces[0].label
				for _, piece := range pieces[1:] {
					set = refinementsets.MakeRefinedSet(refinementsets.Union(set, piece.set))
					label += " | " + piece.label
				}
				inner := &DeclaredRefinement{Kind: DeclaredSet, Set: setPtr(set), Word: &WordSpelling{Text: label, Covers: len(set.Forms)}}
				if absent {
					return AnnotationOfTypeResult{Stated: &DeclaredRefinement{Kind: DeclaredPossiblyUndefined, Inner: inner}}, true
				}
				return AnnotationOfTypeResult{Stated: inner}, true
			}
		}
		var inner *DeclaredRefinement
		sawAbsent := false
		for _, branch := range typeNode.AsUnionTypeNode().Types.Nodes {
			if branch.Kind == ast.KindUndefinedKeyword {
				sawAbsent = true
				continue
			}
			if ast.IsLiteralTypeNode(branch) && branch.AsLiteralTypeNode().Literal.Kind == ast.KindNullKeyword {
				sawAbsent = true
				continue
			}
			read := annotationOfType(p, branch, registry, objects, bindings)
			if read.Stated == nil {
				return read, true // plain TypeScript or unsupported
			}
			if inner != nil {
				return AnnotationOfTypeResult{}, true // two readable branches: unread
			}
			inner = read.Stated
		}
		if inner == nil || !sawAbsent {
			return AnnotationOfTypeResult{}, true
		}
		return AnnotationOfTypeResult{Stated: &DeclaredRefinement{Kind: DeclaredPossiblyUndefined, Inner: inner}}, true
	}
	// `{ port: Port }` -- an inline object statement: each member's
	// type reads as its key's statement, an optional member admits
	// absence. Only a FULLY readable literal states anything -- one
	// plain-TS member keeps the whole literal plain TypeScript.
	if ast.IsTypeLiteralNode(typeNode) {
		var keys []ObjectKeySpec
		for _, member := range typeNode.AsTypeLiteralNode().Members.Nodes {
			if !ast.IsPropertySignatureDeclaration(member) {
				return AnnotationOfTypeResult{}, true
			}
			sig := member.AsPropertySignatureDeclaration()
			if sig.Type == nil || (!ast.IsIdentifier(sig.Name()) && !ast.IsStringLiteral(sig.Name())) {
				return AnnotationOfTypeResult{}, true
			}
			read := annotationOfType(p, sig.Type, registry, objects, bindings)
			if read.Stated == nil {
				return read, true
			}
			stated := read.Stated
			inner := stated
			if stated.Kind == DeclaredPossiblyUndefined {
				inner = stated.Inner
			}
			var value ObjectKeyValue
			hasValue := false
			if inner.Kind == DeclaredSet {
				value = ObjectKeyValue{Kind: KeyValueSet, Set: inner.Set}
				hasValue = true
			} else if inner.Kind == DeclaredObject {
				value = ObjectKeyValue{Kind: KeyValueObject, Object: inner.Object}
				hasValue = true
			}
			if !hasValue {
				return AnnotationOfTypeResult{}, true // a variable member: plain TS
			}
			absent := sig.PostfixToken != nil || stated.Kind == DeclaredPossiblyUndefined
			var count refinementsets.RefinedSet
			if absent {
				count = refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{0, 1}))
			} else {
				count = refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{1}))
			}
			var name string
			if ast.IsIdentifier(sig.Name()) {
				name = sig.Name().AsIdentifier().Text
			} else {
				name = sig.Name().AsStringLiteral().Text
			}
			keys = append(keys, ObjectKeySpec{Name: name, Count: setPtr(count), MayBeAbsent: absent, At: sig.Type, Value: value})
		}
		return AnnotationOfTypeResult{Stated: &DeclaredRefinement{Kind: DeclaredObject, Object: &ObjectAnnotation{Keys: keys}}}, true
	}

	// Array<X> / ReadonlyArray<X> -- the same star X[] spells, the
	// generic way around
	if ast.IsTypeReferenceNode(typeNode) && ast.IsIdentifier(typeNode.AsTypeReferenceNode().TypeName) {
		typeName := typeNode.AsTypeReferenceNode().TypeName.AsIdentifier().Text
		if (typeName == "Array" || typeName == "ReadonlyArray") &&
			resolvesToDefaultLib(p.Checker, typeNode.AsTypeReferenceNode().TypeName) {
			typeArgs := typeNode.AsTypeReferenceNode().TypeArguments
			if typeArgs != nil && len(typeArgs.Nodes) == 1 {
				element := annotationOfType(p, typeArgs.Nodes[0], registry, objects, bindings)
				return starOfElement(element), true
			}
		}
	}
	return AnnotationOfTypeResult{}, false
}
