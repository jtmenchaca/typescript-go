// from control_flow/iteration_element.ts
//
// What one element of an iterable is: the star's item set, a
// repetition's element, a tuple join, or residue when nothing speaks.

package walk

import (
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// ElementOf is elementOf in the TS source: what one element of the
// iterable is — the star's item set, the join of an exact tuple's
// members, or — for a sequence of a refinement variable — the
// variable itself.
func ElementOf(iterable abstractdomain.AbstractValue) abstractdomain.AbstractValue {
	// the elements of an OPAQUE iterable arrive from outside with it
	if iterable.Kind == abstractdomain.KindUnknown && iterable.Opaque {
		return abstractdomain.Opaque
	}
	if iterable.Kind == abstractdomain.KindSet && len(iterable.Set.Forms) == 1 {
		// a sequence whose elements may include NaN (number[] initialized
		// from its type): NaN rides beside the element set
		withNaN := func(element abstractdomain.AbstractValue) abstractdomain.AbstractValue {
			if iterable.NaNElements {
				return abstractdomain.PossiblyNaN(element)
			}
			return element
		}
		only := iterable.Set.Forms[0]
		if only.Form == refinementsets.FormStar {
			return withNaN(abstractdomain.KnownSet(*only.A_, nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone))
		}
		// a length-bounded sequence (a nonempty array) is a repetition,
		// and its elements wear the repeated item set the same way
		if rep, ok := refinementsets.AsRepetition(refinementsets.MakeRefinedSet(only)); ok {
			return withNaN(abstractdomain.KnownSet(rep.Element, nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone))
		}
	}
	if iterable.Kind == abstractdomain.KindVariable && iterable.StarDepth > 0 {
		out := iterable
		out.StarDepth = iterable.StarDepth - 1
		return out
	}
	if iterable.Kind == abstractdomain.KindValues {
		var element *abstractdomain.AbstractValue
		for _, v := range iterable.Values {
			one := abstractdomain.KnownValues([]float64{v}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
			if element == nil {
				element = &one
			} else {
				joined := abstractdomain.JoinKnown(*element, one)
				element = &joined
			}
		}
		if element == nil {
			return silence.Residue()
		}
		return *element
	}
	// a LIST's element is the join of its items' knowledge
	if iterable.Kind == abstractdomain.KindList {
		var element *abstractdomain.AbstractValue
		for _, item := range iterable.Items {
			if element == nil {
				item := item
				element = &item
			} else {
				joined := abstractdomain.JoinKnown(*element, item)
				element = &joined
			}
		}
		if element == nil {
			return silence.Residue()
		}
		return *element
	}
	return silence.Residue()
}
