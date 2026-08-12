// Default-lib mapped utilities over a readable type: Promise and
// Readonly read through; Partial/Required/Pick/Omit keep or select
// keys and change only what absence is allowed.
//
// Ported 1:1 from annotations/type_node_utilities.ts.

package annotations

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// annotationOfTypeUtilities is annotationOfTypeUtilities in the TS
// source. matched=false means "not a default-lib utility"; matched
// with a zero AnnotationOfTypeResult means plain TypeScript.
func annotationOfTypeUtilities(p *program.CheckerProgram, typeNode *ast.Node, registry AnnotationRegistry, objects ObjectRegistry, bindings map[*ast.Symbol]*DeclaredRefinement) (AnnotationOfTypeResult, bool) {
	if !ast.IsTypeReferenceNode(typeNode) || !ast.IsIdentifier(typeNode.AsTypeReferenceNode().TypeName) {
		return AnnotationOfTypeResult{}, false
	}
	name := typeNode.AsTypeReferenceNode().TypeName
	nameText := name.AsIdentifier().Text
	typeArgs := typeNode.AsTypeReferenceNode().TypeArguments

	// Promise<X> -- the position states the RESOLVED value: an async
	// return judges the value returned, and an await reads it back
	if nameText == "Promise" && resolvesToDefaultLib(p.Checker, name) && typeArgs != nil && len(typeArgs.Nodes) == 1 {
		return annotationOfType(p, typeArgs.Nodes[0], registry, objects, bindings), true
	}

	// Readonly<X> reads through -- the wrapper narrows mutability, a
	// shape fact; the VALUES admitted are exactly X's
	if nameText == "Readonly" && resolvesToDefaultLib(p.Checker, name) && typeArgs != nil && len(typeArgs.Nodes) == 1 {
		return annotationOfType(p, typeArgs.Nodes[0], registry, objects, bindings), true
	}

	// Partial/Required keep X's keys and change only what absence is
	// allowed; Pick/Omit select keys. The VALUES admitted at each
	// surviving key are exactly X's -- the mapped utilities are key
	// surgery, never value surgery.
	if (nameText == "Partial" || nameText == "Required" || nameText == "Pick" || nameText == "Omit") &&
		resolvesToDefaultLib(p.Checker, name) && typeArgs != nil && len(typeArgs.Nodes) >= 1 {
		inner := annotationOfType(p, typeArgs.Nodes[0], registry, objects, bindings)
		if inner.Stated == nil {
			return inner, true
		}
		if inner.Stated.Kind != DeclaredObject {
			return AnnotationOfTypeResult{}, true
		}
		keys := inner.Stated.Object.Keys
		unread := inner.Stated.Object.Unread
		if nameText == "Partial" || nameText == "Required" {
			absent := nameText == "Partial"
			out := make([]ObjectKeySpec, len(keys))
			for i, key := range keys {
				key.MayBeAbsent = absent
				if absent {
					key.Count = setPtr(refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{0, 1})))
				} else {
					key.Count = setPtr(refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{1})))
				}
				out[i] = key
			}
			return AnnotationOfTypeResult{Stated: &DeclaredRefinement{Kind: DeclaredObject, Object: &ObjectAnnotation{Keys: out, Unread: unread}}}, true
		}
		// Pick / Omit: the second argument names keys as string
		// literals (or a union of them); anything else stays plain
		// TypeScript
		if len(typeArgs.Nodes) < 2 {
			return AnnotationOfTypeResult{}, true
		}
		namesNode := typeArgs.Nodes[1]
		named := map[string]bool{}
		var literals []*ast.Node
		if ast.IsUnionTypeNode(namesNode) {
			literals = namesNode.AsUnionTypeNode().Types.Nodes
		} else {
			literals = []*ast.Node{namesNode}
		}
		for _, literal := range literals {
			if !ast.IsLiteralTypeNode(literal) || !ast.IsStringLiteral(literal.AsLiteralTypeNode().Literal) {
				return AnnotationOfTypeResult{}, true
			}
			named[literal.AsLiteralTypeNode().Literal.AsStringLiteral().Text] = true
		}
		var kept []ObjectKeySpec
		for _, key := range keys {
			if nameText == "Pick" {
				if named[key.Name] {
					kept = append(kept, key)
				}
			} else if !named[key.Name] {
				kept = append(kept, key)
			}
		}
		// a dependent row whose sibling did not survive the surgery
		// is unstatable here -- dropped, while surviving pairs keep
		// proving
		return AnnotationOfTypeResult{Stated: &DeclaredRefinement{Kind: DeclaredObject, Object: &ObjectAnnotation{Keys: SanitizeDepends(kept), Unread: unread}}}, true
	}
	return AnnotationOfTypeResult{}, false
}
