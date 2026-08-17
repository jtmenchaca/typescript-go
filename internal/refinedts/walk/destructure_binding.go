// from control_flow/destructure_binding.ts
//
// What a destructuring variable declaration does to the env. A NESTED
// pattern at either arm's top level (an object key holding a pattern,
// an array element holding a pattern) recurses through the shared
// reader (bindings/destructuring.ts, ported at walk/destructuring.go);
// the top-level object and array arms keep the extra behavior that
// reader does not: OPAQUE array rest, rest without seededBinding, a
// default's value JOINED with the member's own reading (not residued
// to unknown), a computed object key that pins exactly one string, and
// alias linking for a destructured reference.

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

// bindingElementKey is the property name ONE object-pattern element
// picks: a plain identifier property name (`{ age }`, `{ age: b }`)
// reads its text directly; a COMPUTED name (`{ [k]: age }`) evaluates
// the key expression in the CURRENT env and, where it pins exactly one
// string (exactStringName, object_literal.go's own write-side rule —
// ToPropertyKey of a string is that string unchanged), reads the same
// slot a plain name would. Any other computed value — a number, a set
// of strings, an unresolved read — names no one key, so the pick stays
// honestly unknown rather than guessing a key.
func bindingElementKey(ctx *FlowContext, env Env, be *ast.BindingElement) (string, bool) {
	if be.PropertyName != nil {
		if ast.IsIdentifier(be.PropertyName) {
			return be.PropertyName.Text(), true
		}
		if ast.IsComputedPropertyName(be.PropertyName) {
			keyValue := evaluateExpression(ctx, env, be.PropertyName.AsComputedPropertyName().Expression)
			return exactStringName(keyValue)
		}
		return "", false
	}
	beName := be.Name()
	if beName != nil && ast.IsIdentifier(beName) {
		return beName.Text(), true
	}
	return "", false
}

// withDefaultValue is a defaulted slot's true binding: the default
// expression runs exactly when the slot is absent, so the bound name
// is the JOIN of what the slot holds when present and what the
// default evaluates to when it is not.
//
//   - held is exactly Undef (the member is PROVABLY absent — a
//     complete source without that key, SlotOf's now-precise reading)
//     — the default always runs; the binding IS the default's value,
//     no join needed.
//   - held is PossiblyUndefined(x) — the member may or may not be
//     there; the binding is the join of x and the default's value.
//   - held is a real value that is never absent — the default never
//     runs; held stands as read.
//   - held is generic Unknown (neither provably absent nor a read
//     value) — whether the default ran cannot be told, so the
//     honest answer stays unknown.
func withDefaultValue(ctx *FlowContext, env Env, held abstractdomain.AbstractValue, initializer *ast.Node) abstractdomain.AbstractValue {
	if held.Kind == abstractdomain.KindUndef {
		return evaluateExpression(ctx, env, initializer)
	}
	if held.Kind == abstractdomain.KindPossiblyUndefined {
		present := *held.Inner
		defaultValue := evaluateExpression(ctx, env, initializer)
		return abstractdomain.JoinKnown(present, defaultValue)
	}
	if held.Kind == abstractdomain.KindUnknown {
		return silence.Residue()
	}
	return held
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
			key, hasKey := bindingElementKey(ctx, env, be)
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
		key, hasKey := bindingElementKey(ctx, env, be)
		var held abstractdomain.AbstractValue
		if hasKey {
			held = SlotOf(source, key)
		} else {
			held = silence.Residue()
		}
		if be.Initializer != nil {
			held = withDefaultValue(ctx, env, held, be.Initializer)
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
	// `const { promise, resolve } = Promise.withResolvers()` — the
	// resolve/reject member's own declared symbol pairs with the
	// promise member's tracked name, so a later `resolve(arg)` call
	// (promise_with_resolvers.go) can find its promise
	PairPromiseWithResolversBindings(ctx, initializer, elements)
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
		beName := be.Name()
		if beName == nil {
			continue
		}
		// a NESTED pattern at this position (`[[first]]`, `[{ age }]`)
		// recurses element by element through the shared reader, the
		// same way bindObjectPattern's nested branch does above —
		// without this branch the name inside the nested pattern is
		// never bound at all, and reads back through the checker's own
		// wide static type instead of the destructured slot
		if !ast.IsIdentifier(beName) {
			if be.DotDotDotToken != nil {
				// a nested pattern can never sit after `...` syntactically
				continue
			}
			destructureInto(ctx, env, beName, SlotOfIndex(source, i))
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
			// the default expression runs exactly when the slot is
			// provably absent — the same join bindObjectPattern's leaf
			// branch already applies through withDefaultValue
			if be.Initializer != nil {
				held = withDefaultValue(ctx, env, held, be.Initializer)
			}
		}
		WriteBinding(ctx, env, be.Name().Text(), silence.SeededBinding(ctx.P.Checker, held, be.Name()), element, "an initialized value")
	}
}
