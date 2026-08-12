// from bindings/destructuring.ts
//
// Reading a destructuring pattern against a knowledge state — ONE
// reader for every binding site: a variable statement's pattern
// (statements.ts), a callback parameter's (closures.ts), a for-of
// element's (loops.ts). Each site supplies only its BIND action;
// what a slot holds, how patterns nest, what a rest collects, and
// what a default admits are decided here, once.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// elementAtSource is the {set, nanElements} shape SlotOf reads an
// element from — the TS source's inline object-typed parameter.
type elementAtSource struct {
	set         refinementsets.RefinedSet
	nanElements bool
}

// elementAt is one element of a sequence SET: a repetition (a stated
// tuple, a bounded array) repeats one item set, and every in-range
// position wears it. Unknown outside the range or on shapes the
// repetition reading cannot parse.
func elementAt(source elementAtSource, i int) abstractdomain.AbstractValue {
	rep, ok := refinementsets.AsRepetition(source.set)
	if !ok {
		return silence.Residue()
	}
	if rep.Hi != nil && i >= *rep.Hi {
		return silence.Residue()
	}
	element := abstractdomain.KnownSet(rep.Element, nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone)
	// NaN rides beside the element where the sequence may hold it
	if source.nanElements {
		return abstractdomain.PossiblyNaN(element)
	}
	return element
}

// darkSlotOf is the OPAQUE-or-residue fallback shared by both slot
// readers below: a slot of an OPAQUE value is opaque too — the whole
// structure entered from outside the file's determination.
func darkSlotOf(source abstractdomain.AbstractValue) abstractdomain.AbstractValue {
	if source.Kind == abstractdomain.KindUnknown && source.Opaque {
		return abstractdomain.Opaque
	}
	return silence.Residue()
}

// SlotOf is what one slot of a destructuring source holds by NAME —
// an object's key, by the same rules the top-level branches use. (TS
// `slotOf(source, key: string)`; Go has no `string | number` union,
// so the two shapes split into SlotOf and SlotOfIndex, per PORT.md's
// no-union-twin convention.)
func SlotOf(source abstractdomain.AbstractValue, key string) abstractdomain.AbstractValue {
	dark := darkSlotOf(source)
	if source.Kind != abstractdomain.KindObject {
		return dark
	}
	for _, k := range source.Keys {
		if k.Name == key {
			return k.Value
		}
	}
	return dark
}

// SlotOfIndex is what one slot of a destructuring source holds by
// POSITION — a sequence's element. (TS `slotOf(source, key: number)`.)
func SlotOfIndex(source abstractdomain.AbstractValue, i int) abstractdomain.AbstractValue {
	dark := darkSlotOf(source)
	if source.Kind == abstractdomain.KindValues && source.KindTag == abstractdomain.PrimitiveArray {
		if i < len(source.Values) {
			return abstractdomain.KnownValues([]float64{source.Values[i]}, abstractdomain.PrimitiveNumber, abstractdomain.TrustLevelOf(source))
		}
		return dark
	}
	if source.Kind == abstractdomain.KindList {
		// a list's length is exact, so a slot past its end is EXACTLY
		// the absent value
		if i < len(source.Items) {
			return source.Items[i]
		}
		return abstractdomain.Undef
	}
	if source.Kind == abstractdomain.KindSet {
		return elementAt(elementAtSource{set: source.Set, nanElements: source.NaNElements}, i)
	}
	return dark
}

// withDefault is a defaulted slot: the initializer runs exactly when
// the slot is absent, so a slot that may be absent admits values the
// walk did not read — unknown, honestly. A slot that cannot be
// absent keeps its knowledge (the default never runs).
func withDefault(held abstractdomain.AbstractValue) abstractdomain.AbstractValue {
	if held.Kind == abstractdomain.KindUndef || held.Kind == abstractdomain.KindPossiblyUndefined || held.Kind == abstractdomain.KindUnknown {
		return silence.Residue()
	}
	return held
}

// objectRest is the rest of an object after the picked keys — known
// exactly when the source's key set is complete and every picked key
// is a plain name.
func objectRest(pattern *ast.Node, source abstractdomain.AbstractValue) abstractdomain.AbstractValue {
	if source.Kind != abstractdomain.KindObject || !source.Complete {
		return silence.Residue()
	}
	obp := pattern.AsBindingPattern()
	picked := map[string]struct{}{}
	for _, element := range obp.Elements.Nodes {
		be := element.AsBindingElement()
		if be.DotDotDotToken != nil {
			continue
		}
		var key string
		hasKey := false
		if be.PropertyName != nil {
			if ast.IsIdentifier(be.PropertyName) {
				key, hasKey = be.PropertyName.Text(), true
			}
		} else if ast.IsIdentifier(be.Name()) {
			key, hasKey = be.Name().Text(), true
		}
		if !hasKey {
			return silence.Residue() // a computed pick hides its key
		}
		picked[key] = struct{}{}
	}
	remaining := []abstractdomain.ObjectKey{}
	for _, k := range source.Keys {
		if _, ok := picked[k.Name]; !ok {
			remaining = append(remaining, k)
		}
	}
	return abstractdomain.KnownObject(remaining, nil, true, abstractdomain.TrustProved, false)
}

// arrayRest is the rest of a sequence from slot `from` on — exact
// for an exact sequence, unknown anywhere else.
func arrayRest(source abstractdomain.AbstractValue, from int) abstractdomain.AbstractValue {
	if source.Kind == abstractdomain.KindList {
		return abstractdomain.KnownList(source.Items[from:], abstractdomain.TrustProved)
	}
	if source.Kind == abstractdomain.KindValues && source.KindTag == abstractdomain.PrimitiveArray {
		return abstractdomain.KnownValues(source.Values[from:], abstractdomain.PrimitiveArray, abstractdomain.TrustLevelOf(source))
	}
	return silence.Residue()
}

// ReadDestructuring binds a destructuring pattern against a source:
// each identifier takes its slot's knowledge (through its default,
// where one is written), each inner pattern recurses, and each rest
// collects what remains. The caller's `bind` decides what binding
// MEANS at its site — a checked environment write, a parameter map
// entry, a loop-body initialState.
func ReadDestructuring(pattern *ast.Node, source abstractdomain.AbstractValue, bind func(name string, held abstractdomain.AbstractValue, at *ast.Node)) {
	// The TS source's `pattern` parameter is typed ts.BindingName
	// (Identifier | BindingPattern), never absent — every recursive
	// call passes element.name, whose static type rules out undefined.
	// tsgo's *BindingElement.Name() field CAN be nil on a parser-error-
	// recovered node (no such static guarantee in the Go AST); a nil
	// pattern here binds nothing, the sound fallback (same one
	// dataflowfacts/syntactic_facts.go's bindingNames and
	// destructure_binding.go's bindObjectPattern/bindArrayPattern use
	// for the identical shape).
	if pattern == nil {
		return
	}
	if ast.IsIdentifier(pattern) {
		bind(pattern.Text(), source, pattern)
		return
	}
	if ast.IsObjectBindingPattern(pattern) {
		obp := pattern.AsBindingPattern()
		for _, element := range obp.Elements.Nodes {
			be := element.AsBindingElement()
			beName := be.Name()
			if be.DotDotDotToken != nil {
				if beName == nil || !ast.IsIdentifier(beName) {
					continue
				}
				bind(beName.Text(), objectRest(pattern, source), beName)
				continue
			}
			var key string
			hasKey := false
			if be.PropertyName != nil {
				if ast.IsIdentifier(be.PropertyName) {
					key, hasKey = be.PropertyName.Text(), true
				}
			} else if beName != nil && ast.IsIdentifier(beName) {
				key, hasKey = beName.Text(), true
			}
			var held abstractdomain.AbstractValue
			if !hasKey {
				held = silence.Residue()
			} else {
				held = SlotOf(source, key)
			}
			next := held
			if be.Initializer != nil {
				next = withDefault(held)
			}
			ReadDestructuring(beName, next, bind)
		}
		return
	}
	// array binding pattern
	abp := pattern.AsBindingPattern()
	for i, element := range abp.Elements.Nodes {
		if ast.IsOmittedExpression(element) {
			continue
		}
		be := element.AsBindingElement()
		beName := be.Name()
		if be.DotDotDotToken != nil {
			if beName == nil || !ast.IsIdentifier(beName) {
				continue
			}
			bind(beName.Text(), arrayRest(source, i), beName)
			continue
		}
		held := SlotOfIndex(source, i)
		next := held
		if be.Initializer != nil {
			next = withDefault(held)
		}
		ReadDestructuring(beName, next, bind)
	}
}
