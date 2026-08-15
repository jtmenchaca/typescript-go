// from control_flow/iteration_element.ts
//
// What one element of an iterable is: the star's item set, a
// repetition's element, a tuple join, or residue when nothing speaks.

package walk

import (
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
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
	if iterable.Kind == abstractdomain.KindSet {
		// a sequence whose elements may include NaN (number[] initialized
		// from its type): NaN rides beside the element set
		withNaN := func(element abstractdomain.AbstractValue) abstractdomain.AbstractValue {
			if iterable.NaNElements {
				return abstractdomain.PossiblyNaN(element)
			}
			return element
		}
		// the star's item set, or a length-bounded sequence's repeated
		// item set — read the same way, one per arm the set unions over,
		// and the element is the JOIN of what the arms hold (whichever
		// arm the sequence is on, its elements wear that arm's item set)
		if arms, ok := RepetitionArmsOf(iterable.Set); ok {
			var element *abstractdomain.AbstractValue
			for _, rep := range arms {
				one := abstractdomain.KnownSet(rep.Element, nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone)
				if element == nil {
					element = &one
				} else {
					joined := abstractdomain.JoinKnown(*element, one)
					element = &joined
				}
			}
			if element != nil {
				return withNaN(*element)
			}
		}
	}
	// an OBJECT-STAR states one thing and it is exactly this question:
	// what one position holds. No count is involved, so the element
	// comes back whole — the same reading the `.next().value` route
	// gives for the same iterator.
	if element, ok := abstractdomain.ElementOfObjectStar(iterable); ok {
		return element
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
