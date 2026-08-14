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

// RepetitionArmsOf splits a sequence set into the repetition arms a
// per-arm reading walks. A set is the INTERSECTION of its forms, so a
// set that already reads back as one repetition is its own single arm.
// Otherwise one form carrying a UNION splits into that union's two
// sides, each re-intersected with the set's other forms — dropping the
// siblings would widen the arm, and an arm has to say exactly what the
// value on that side says. The split repeats through nested unions, so
// a left-folded ladder of repetitions yields every rung.
//
// The second result is false when some arm is not repetition-shaped:
// callers that JOIN per-arm readings need every arm to speak, and
// callers that CHECK every arm need every arm to be checkable.
//
// This is the one place the union-arm split lives: elementAt below,
// arrayRest below, ElementOf (iteration_element.go) and WriteElement
// (assignments.go) all read their arms from here.
func RepetitionArmsOf(set refinementsets.RefinedSet) ([]refinementsets.Repeated, bool) {
	if rep, ok := refinementsets.AsRepetition(set); ok {
		return []refinementsets.Repeated{rep}, true
	}
	unionIndex := -1
	for i, form := range set.Forms {
		if form.Form == refinementsets.FormUnion && form.A_ != nil && form.B != nil {
			unionIndex = i
			break
		}
	}
	if unionIndex < 0 {
		return nil, false
	}
	// each side of the union, wearing the constraints that stood beside it
	var arms []refinementsets.Repeated
	for _, side := range []refinementsets.RefinedSet{*set.Forms[unionIndex].A_, *set.Forms[unionIndex].B} {
		conjoined := refinementsets.MakeRefinedSet()
		conjoined.Forms = append(conjoined.Forms, set.Forms[:unionIndex]...)
		conjoined.Forms = append(conjoined.Forms, side.Forms...)
		conjoined.Forms = append(conjoined.Forms, set.Forms[unionIndex+1:]...)
		sideArms, ok := RepetitionArmsOf(conjoined)
		if !ok {
			return nil, false
		}
		arms = append(arms, sideArms...)
	}
	return arms, true
}

// elementAt is one element of a sequence SET: a repetition (a stated
// tuple, a bounded array) repeats one item set, and every in-range
// position wears it. A set that is a UNION of repetitions answers the
// JOIN of its arms' readings — every arm the value could be on says
// what position i holds there, and the join says what holds on all of
// them. Unknown only where no arm is repetition-shaped.
func elementAt(source elementAtSource, i int) abstractdomain.AbstractValue {
	arms, ok := RepetitionArmsOf(source.set)
	if !ok {
		return silence.Residue()
	}
	var read *abstractdomain.AbstractValue
	for _, rep := range arms {
		one := elementAtRepetition(rep, i)
		if read == nil {
			one := one
			read = &one
		} else {
			joined := abstractdomain.JoinKnown(*read, one)
			read = &joined
		}
	}
	if read == nil {
		return silence.Residue()
	}
	// NaN rides beside the element where the sequence may hold it
	if source.nanElements {
		return abstractdomain.PossiblyNaN(*read)
	}
	return *read
}

// elementAtRepetition is what position i holds on ONE repetition arm:
// the repeated item set inside the length window, and the absent value
// past a known ceiling — a sequence of at most Hi items has nothing at
// position Hi or beyond, and reading a slot past the end answers
// undefined (sec-array-exotic-objects, the same rule
// element_in_bounds.go reads out-of-range positions by).
func elementAtRepetition(rep refinementsets.Repeated, i int) abstractdomain.AbstractValue {
	if rep.Hi != nil && i >= *rep.Hi {
		return abstractdomain.Undef
	}
	return abstractdomain.KnownSet(rep.Element, nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone)
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

// objectRest is the rest of an object after the picked keys: the
// source's keys minus the ones the pattern picked by name. The rest is
// COMPLETE — those keys and no others — only when the source's own key
// set is complete and every pick names its key. An INCOMPLETE source
// still answers the keys it knows about (the unknown remainder may put
// more keys in the rest, never take these away), and a COMPUTED pick
// costs only completeness (it may carry off one more key, and which
// one it cannot say) — the same "these keys, possibly more" partial
// object_literal.go's openOnUnknownKey writes.
func objectRest(pattern *ast.Node, source abstractdomain.AbstractValue) abstractdomain.AbstractValue {
	if source.Kind != abstractdomain.KindObject {
		return silence.Residue()
	}
	obp := pattern.AsBindingPattern()
	picked := map[string]struct{}{}
	// whether some pick's key is hidden behind a computed name
	computedPick := false
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
		} else if beName := be.Name(); beName != nil && ast.IsIdentifier(beName) {
			key, hasKey = beName.Text(), true
		}
		if !hasKey {
			// a computed pick hides WHICH key it took: every plainly
			// named key still leaves the rest, and the one the computed
			// name reached leaves with it
			computedPick = true
			continue
		}
		picked[key] = struct{}{}
	}
	remaining := []abstractdomain.ObjectKey{}
	for _, k := range source.Keys {
		if _, ok := picked[k.Name]; !ok {
			held := k.Value
			// a computed pick may have carried THIS key off: it holds
			// what it held, or it is not in the rest at all
			if computedPick {
				held = abstractdomain.PossiblyUndefined(held, "", false, false)
			}
			remaining = append(remaining, abstractdomain.ObjectKey{Name: k.Name, Value: held})
		}
	}
	// the rest lists exactly these keys only when the source listed
	// exactly its own: keys the source never named land in the rest too
	return abstractdomain.KnownObject(remaining, nil, source.Complete, abstractdomain.TrustProved, false)
}

// arrayRest is the rest of a sequence from slot `from` on — exact for
// an exact sequence, and for a SET-shaped one the same set with its
// length window shifted down by `from`: dropping the first `from`
// items leaves sequences of the same repeated element, between lo-from
// and hi-from long (floored at zero, since a sequence shorter than
// `from` leaves an empty rest). Unknown where no arm is
// repetition-shaped.
func arrayRest(source abstractdomain.AbstractValue, from int) abstractdomain.AbstractValue {
	if source.Kind == abstractdomain.KindList {
		return abstractdomain.KnownList(source.Items[from:], abstractdomain.TrustProved)
	}
	if source.Kind == abstractdomain.KindValues && source.KindTag == abstractdomain.PrimitiveArray {
		return abstractdomain.KnownValues(source.Values[from:], abstractdomain.PrimitiveArray, abstractdomain.TrustLevelOf(source))
	}
	if source.Kind == abstractdomain.KindSet && source.SetKindTag == abstractdomain.SetKindTagNone {
		arms, ok := RepetitionArmsOf(source.Set)
		if !ok {
			return silence.Residue()
		}
		var rest *abstractdomain.AbstractValue
		for _, rep := range arms {
			lo := rep.Lo - from
			if lo < 0 {
				lo = 0
			}
			var hi *int
			if rep.Hi != nil {
				ceiling := *rep.Hi - from
				if ceiling < 0 {
					ceiling = 0
				}
				hi = &ceiling
			}
			one := abstractdomain.KnownSet(
				refinementsets.Repetition(rep.Element, lo, hi),
				nil,
				abstractdomain.TrustLevelOf(source),
				abstractdomain.SetKindTagNone,
			)
			if rest == nil {
				one := one
				rest = &one
			} else {
				joined := abstractdomain.JoinKnown(*rest, one)
				rest = &joined
			}
		}
		if rest == nil {
			return silence.Residue()
		}
		out := *rest
		// the rest holds the same items the source did, NaN among them
		out.NaNElements = source.NaNElements
		return out
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
