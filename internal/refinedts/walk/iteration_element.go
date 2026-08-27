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
			return silence.ResidueOf("the values set is empty, so no element position exists to join")
		}
		return *element
	}
	// an ARRAY-HOLES sequence's element is undefined at every position —
	// the present-element set is ∅, so there is nothing else a position
	// could hold (the same reading a KindList's Undef items give, just
	// without materializing one per slot)
	if iterable.Kind == abstractdomain.KindArrayHoles {
		return abstractdomain.Undef
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
			return silence.ResidueOf("the iteration element reader holds no model for this iterable's shape")
		}
		return *element
	}
	// a GRADED scalar — a checked declaration's return read through a
	// cast the iterated position's own shape does not match
	// (`unreadNumber() as unknown as number[]`) — is not opaque and not
	// a plain residue either: an element read off it is exactly as
	// unconstrained as the whole value already was, so the source's own
	// ground and grade carry forward. The same propagation
	// darkSlotOf (destructuring.go) applies to a member read, mirrored
	// here for an element read — without it CheckPossiblyNaN cannot
	// tell this claim apart from AfterReaders' own ungraded fallback
	// seed (nan_wrapper.go's two-case split).
	//
	// A COLLECTION IS NOT A SCALAR, and this arm is about a scalar. A
	// `Set<Age>`/`Map<K, V>` parameter arrives as a graded
	// KindCollection with no entries the walk watched, and reading it
	// through this arm answers THE SET ITSELF as the loop's element —
	// `for (const x of s)` then binds x to the collection, and a
	// return of x into a scalar position refutes as "a returned value
	// is an object". Its element is stated elsewhere and better: the
	// declared type argument, which declaredCollectionElement
	// (iteration_elements.go) reads at library grade. That reader runs
	// from the loop head only when this function leaves the element
	// UNKNOWN (loop_fixpoint.go gates the IterationElement call on
	// exactly that), so answering here is what kept it from being
	// asked at all.
	//
	// The same holds for the other structured kinds with their own
	// element readings — an object, a promise, a date, a regex: none
	// of them has "the whole value" as a position's contents, and each
	// either states its element through its own reader or states none.
	if iterable.Kind != abstractdomain.KindUnknown && iterable.Grade != "" &&
		!isStructuredIterableShape(iterable.Kind) {
		return iterable
	}
	return silence.ResidueOf("the iteration element reader holds no model for this iterable's shape")
}

// isStructuredIterableShape names the kinds whose element is NOT the
// value itself — every structured shape whose positions hold something
// other than the whole. The graded-scalar arm above excludes them so
// each reaches its own element reading (or none) rather than answering
// the container where an element was asked for.
//
// The sequence kinds are absent because the arms ABOVE that one
// already answer them exactly — a set's repetition arms, a values
// tuple's join, an array-holes undefined, a list's item join — so they
// never reach the scalar arm to be excluded from it.
func isStructuredIterableShape(kind abstractdomain.Kind) bool {
	switch kind {
	case abstractdomain.KindCollection, abstractdomain.KindObject,
		abstractdomain.KindPromise, abstractdomain.KindDate, abstractdomain.KindRegex:
		return true
	}
	return false
}
