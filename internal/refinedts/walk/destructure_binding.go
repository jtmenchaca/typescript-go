// from control_flow/destructure_binding.ts
//
// What a destructuring variable declaration does to the env. Nested
// patterns go through the shared reader (bindings/destructuring.ts,
// ported at walk/destructuring.go); the top-level object and array
// arms keep the extra behavior that reader does not: OPAQUE array
// rest, rest without seededBinding, defaults that residue only
// unknown (not undef), skipped nested array patterns, and alias
// linking for a destructured reference.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// destructureInto is destructureInto in the TS source: bind a NESTED
// destructuring pattern — the shared reader walks the pattern, and
// each bound name lands as a checked environment write.
func destructureInto(ctx *FlowContext, env Env, name *ast.Node, source abstractdomain.AbstractValue) {
	ReadDestructuring(name, source, func(text string, held abstractdomain.AbstractValue, at *ast.Node) {
		WriteBinding(ctx, env, text, silence.SeededBinding(ctx.P.Checker, held, at), at, "an initialized value")
	})
}

// BindDestructuringDeclaration is bindDestructuringDeclaration in
// the TS source: bind a destructuring declaration into the env.
// Returns true when the declaration was a pattern with an
// initializer (caller skips the identifier path).
func BindDestructuringDeclaration(ctx *FlowContext, env Env, declaration *ast.Node) bool {
	decl := declaration.AsVariableDeclaration()
	if ast.IsObjectBindingPattern(decl.Name()) && decl.Initializer != nil {
		bindObjectPattern(ctx, env, decl.Name(), decl.Initializer)
		return true
	}
	if ast.IsArrayBindingPattern(decl.Name()) && decl.Initializer != nil {
		bindArrayPattern(ctx, env, decl.Name(), decl.Initializer)
		return true
	}
	return false
}

func bindObjectPattern(ctx *FlowContext, env Env, pattern *ast.Node, initializer *ast.Node) {
	source := evaluateExpression(ctx, env, initializer)
	elements := pattern.AsBindingPattern().Elements.Nodes
	for _, element := range elements {
		be := element.AsBindingElement()
		// The TS source's element.name is typed ts.BindingName, never
		// absent; tsgo's *BindingElement.Name() field CAN be nil on a
		// parser-error-recovered node — IsIdentifier(nil) panics reading
		// nil.Kind, so the nil check must gate every call below (see
		// dataflowfacts/syntactic_facts.go's bindingNames for the fuller
		// note). A nil name reads as "not an identifier here", the same
		// branch the TS source's own `!ts.isIdentifier(element.name)`
		// takes for a nested pattern — sound because destructureInto/
		// ReadDestructuring already handles a nil/absent name as
		// nothing-bound.
		beName := be.Name()
		// a NESTED pattern under a key recurses element by element
		if beName == nil || !ast.IsIdentifier(beName) {
			var key string
			hasKey := false
			if be.PropertyName != nil && ast.IsIdentifier(be.PropertyName) {
				key, hasKey = be.PropertyName.Text(), true
			}
			var slot abstractdomain.AbstractValue
			if hasKey {
				slot = SlotOf(source, key)
			} else {
				slot = silence.Residue()
			}
			destructureInto(ctx, env, be.Name(), slot)
			continue
		}
		if be.DotDotDotToken != nil {
			// the rest is the source MINUS the picked keys — known
			// exactly when the source's key set is complete
			rest := silence.Residue()
			if source.Kind == abstractdomain.KindObject && source.Complete {
				picked := map[string]struct{}{}
				for _, other := range elements {
					oe := other.AsBindingElement()
					if oe.DotDotDotToken != nil {
						continue
					}
					oeName := oe.Name()
					if oe.PropertyName != nil && ast.IsIdentifier(oe.PropertyName) {
						picked[oe.PropertyName.Text()] = struct{}{}
					} else if oeName != nil && ast.IsIdentifier(oeName) {
						picked[oeName.Text()] = struct{}{}
					} else {
						picked[""] = struct{}{}
					}
				}
				var remaining []abstractdomain.ObjectKey
				for _, k := range source.Keys {
					if _, isPicked := picked[k.Name]; !isPicked {
						remaining = append(remaining, k)
					}
				}
				rest = abstractdomain.KnownObject(remaining, nil, true, abstractdomain.TrustProved, false)
			}
			WriteBinding(ctx, env, be.Name().Text(), rest, element, "an initialized value")
			continue
		}
		var key string
		hasKey := false
		if be.PropertyName != nil {
			if ast.IsIdentifier(be.PropertyName) {
				key, hasKey = be.PropertyName.Text(), true
			}
		} else {
			key, hasKey = be.Name().Text(), true
		}
		var held abstractdomain.AbstractValue
		if hasKey {
			held = SlotOf(source, key)
		} else {
			held = silence.Residue()
		}
		if be.Initializer != nil && held.Kind == abstractdomain.KindUnknown {
			held = silence.Residue()
		}
		WriteBinding(ctx, env, be.Name().Text(), silence.SeededBinding(ctx.P.Checker, held, be.Name()), element, "an initialized value")
		// a destructured REFERENCE is the holder's child — linked,
		// so a write through it reaches the holder
		if ast.IsIdentifier(initializer) {
			if _, ok := env.Get(initializer.Text()); ok && dataflowfacts.ReferenceTyped(ctx.P.Checker, be.Name()) {
				ctx.Aliases.Link(be.Name().Text(), initializer.Text())
			}
		}
	}
}

func bindArrayPattern(ctx *FlowContext, env Env, pattern *ast.Node, initializer *ast.Node) {
	source := evaluateExpression(ctx, env, initializer)
	for i, element := range pattern.AsBindingPattern().Elements.Nodes {
		if ast.IsOmittedExpression(element) {
			continue
		}
		be := element.AsBindingElement()
		// The TS source's `element.name` is typed ts.BindingName, never
		// absent (ts.isIdentifier(undefined) would itself throw, but the
		// type system rules that case out statically). tsgo's
		// *BindingElement.Name() field CAN be nil on a parser-error-
		// recovered node — IsIdentifier(nil) panics reading nil.Kind, so
		// the nil check must come first; a nil name binds nothing here,
		// the same "no name at this position" fallback
		// dataflowfacts/syntactic_facts.go's bindingNames uses.
		if be.Name() == nil || !ast.IsIdentifier(be.Name()) {
			continue
		}
		var held abstractdomain.AbstractValue
		if be.DotDotDotToken != nil {
			// the rest of an exact sequence is the remaining sequence;
			// the rest of an OPAQUE sequence is opaque with it
			switch {
			case source.Kind == abstractdomain.KindValues && source.KindTag == abstractdomain.PrimitiveArray:
				var rest []float64
				if i < len(source.Values) {
					rest = source.Values[i:]
				}
				held = abstractdomain.KnownValues(rest, abstractdomain.PrimitiveArray, abstractdomain.TrustLevelOf(source))
			case source.Kind == abstractdomain.KindList:
				var rest []abstractdomain.AbstractValue
				if i < len(source.Items) {
					rest = source.Items[i:]
				}
				held = abstractdomain.KnownList(rest, abstractdomain.TrustProved)
			case source.Kind == abstractdomain.KindUnknown && source.Opaque:
				held = abstractdomain.Opaque
			default:
				held = silence.Residue()
			}
		} else {
			// one slot, by the shared rules — an exact element, a
			// repeated item set, or the source's own opacity
			held = SlotOfIndex(source, i)
		}
		WriteBinding(ctx, env, be.Name().Text(), silence.SeededBinding(ctx.P.Checker, held, be.Name()), element, "an initialized value")
	}
}
