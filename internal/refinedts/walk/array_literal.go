// from evaluation/array_literal.ts
//
// Array-literal evaluation: one pass builds the item list; all-scalar
// lists collapse to the flat number tuple, anything else stays a LIST
// with element knowledge per slot. A spread flattens an exact
// sequence; a spread whose length is not pinned costs the exact slots
// and nothing else — the answer becomes the star of what the elements
// admit, these elements at an unstated length.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// EvaluateArrayLiteral evaluates an array literal expression.
func EvaluateArrayLiteral(ctx *FlowContext, env Env, e *ast.Node) abstractdomain.AbstractValue {
	lit := e.AsArrayLiteralExpression()
	var items []abstractdomain.AbstractValue
	// a spread whose LENGTH is not pinned costs the exact item list, and
	// only that: every element still contributes what it admits, and the
	// answer is the sequence of those admissions at an unstated length.
	// The elements walked before the spread are collected here so their
	// knowledge survives it.
	lengthKnown := true
	var elements []abstractdomain.AbstractValue
	for _, element := range lit.Elements.Nodes {
		// an ELISION — the gap in `[1, , 3]` — is the one way a literal
		// builds a HOLE: the slot exists and counts toward the length, and
		// reading it answers undefined. It is the only element the walk
		// cannot evaluate, so the slot wears the absence outright rather
		// than the read sites having to doubt every literal-built list.
		// Every OTHER slot here is written by this loop from its own
		// element, which is what lets the element reads treat a KindList
		// as hole-free.
		if ast.IsOmittedExpression(element) {
			items = append(items, abstractdomain.Undef)
			elements = append(elements, abstractdomain.Undef)
			continue
		}
		// a spread of an exact sequence flattens its elements in place
		if ast.IsSpreadElement(element) {
			spread := evaluateExpression(ctx, env, element.AsSpreadElement().Expression)
			if spread.Kind == abstractdomain.KindValues && spread.KindTag == abstractdomain.PrimitiveArray {
				for _, v := range spread.Values {
					one := abstractdomain.KnownValues([]float64{v}, abstractdomain.PrimitiveNumber, abstractdomain.TrustLevelOf(spread))
					items = append(items, one)
					elements = append(elements, one)
				}
				continue
			}
			if spread.Kind == abstractdomain.KindList {
				items = append(items, spread.Items...)
				elements = append(elements, spread.Items...)
				continue
			}
			// `[...xs]` alone is a COPY: same length window, same
			// elements, same order — the value claim carries whole,
			// measures included
			if len(lit.Elements.Nodes) == 1 && spread.Kind == abstractdomain.KindSet && spread.SetKindTag == abstractdomain.SetKindTagNone {
				return spread
			}
			// `[...new Array(n)]` alone, n past the materialization
			// ceiling: sec-runtime-semantics-arrayaccumulation's
			// `SpreadElement : ... AssignmentExpression` row
			// (specifications/javascript/spec.html) drains the receiver's own
			// GetIterator/IteratorStepValue result at each position — the
			// ARRAY exotic object's own iterator (%ArrayIteratorPrototype%
			// .next) yields VALUES by reading Get(array, ToString(index)),
			// which answers undefined at any own-or-inherited miss
			// (OrdinaryGet) — so the spread produces n copies of
			// undefined, the same length and element claim the receiver
			// itself already carries. The result is DENSE whatever the
			// receiver was: ArrayAccumulation's SpreadElement row runs
			// CreateDataPropertyOrThrow at every position
			// (sec-runtime-semantics-arrayaccumulation), so the copy
			// affirms density even when a sparse `new Array(n)` fed it —
			// Object.keys of the spread answers every index.
			if len(lit.Elements.Nodes) == 1 && spread.Kind == abstractdomain.KindArrayHoles {
				densified := spread
				densified.Dense = true
				densified.DenseKnown = true
				return densified
			}
			// a spread of a BUILT collection materializes its entries in
			// place, in entry order — a Set's members one apiece, a Map's
			// [key, value] pairs (collectionSpreadItems and its clauses)
			if collectionItems, ok := collectionSpreadItems(spread); ok {
				items = append(items, collectionItems...)
				elements = append(elements, collectionItems...)
				continue
			}
			// a values()/keys()/entries() view over a receiver the walk
			// holds exactly drains those same exact items — read off the
			// environment, which the view call's own evaluation above
			// left intact (the views are on the read-only list)
			if viewItems, ok := drainedViewItems(ctx, env, element.AsSpreadElement().Expression); ok {
				items = append(items, viewItems...)
				elements = append(elements, viewItems...)
				continue
			}
			// an unpinned spread loses the LENGTH — its own elements are
			// what it admits, and the walk keeps building past it
			lengthKnown = false
			spreadElement := ElementOf(spread)
			// the walk holds no items for a builtin ITERATOR — `[...m.values()]`
			// spreads a view over a collection it never tracked — but the
			// iterator's own type argument states what one element is, and a
			// spread yields every element the iterator yields
			// (sec-runtime-semantics-arrayaccumulation). So the element the
			// star is built over comes from the same reading `.next().value`
			// takes, and only the LENGTH stays unstated.
			//
			// A GENERATOR call's own evaluated value is deliberately OPAQUE
			// (GeneratorCallResult, generator_element.go — the call builds
			// an object, not the body's return value), so spreadElement
			// reads Opaque here even though builtinIteratorSequenceOf's own
			// GeneratorSequenceOf arm CAN answer the element by reading the
			// callee's yields off the CALL NODE, not off this already-opaque
			// value. Trying the reader whenever the value read nothing
			// itself — Opaque included — is what lets `[...gen()]` reach
			// that arm; a plain KindUnknown-but-not-opaque value is not the
			// only shape a spread's own evaluation can leave unanswered.
			if spreadElement.Kind == abstractdomain.KindUnknown {
				if sequence, ok := builtinIteratorSequenceOf(ctx, element.AsSpreadElement().Expression); ok {
					// `[...gen()]` ALONE is the drained sequence itself, not
					// merely a position folded through sequenceOfElements —
					// same reasoning as the `[...xs]`-alone COPY above. That
					// distinction carries real information here: the drain
					// may carry a proven LOWER BOUND (GeneratorSequenceOf
					// stating a generator's leading unconditional yields as
					// a Repetition, not a bare Star), and folding down to
					// just spreadElement and re-starring below
					// (sequenceOfElements's own scalarPositionSet) would
					// throw that bound away — every fold there rebuilds a
					// fresh lo=0 star, having no way to see the bound the
					// per-element read already discarded.
					if len(lit.Elements.Nodes) == 1 {
						return sequence
					}
					spreadElement = ElementOf(sequence)
				}
			}
			elements = append(elements, spreadElement)
			continue
		}
		walked := evaluateExpression(ctx, env, element)
		items = append(items, walked)
		elements = append(elements, walked)
	}
	// the item list is exact only when every element's POSITION is; past
	// an unpinned spread the answer is the star of what the elements
	// admit — these elements, length unknown
	if !lengthKnown {
		return sequenceOfElements(elements)
	}
	flat := true
	for _, item := range items {
		if !(item.Kind == abstractdomain.KindValues && len(item.Values) == 1 && item.KindTag == abstractdomain.PrimitiveNumber) {
			flat = false
			break
		}
	}
	if flat {
		values := make([]float64, len(items))
		floor := abstractdomain.TrustProved
		for i, item := range items {
			values[i] = item.Values[0]
			floor = abstractdomain.MinTrustLevel(floor, abstractdomain.TrustLevelOf(item))
		}
		return abstractdomain.KnownValues(values, abstractdomain.PrimitiveArray, floor)
	}
	return abstractdomain.KnownList(items, abstractdomain.TrustProved)
}

// sequenceOfElements is the array an unpinned spread built: a sequence
// over the union of what its elements admit, at ANY length. Star claims
// only "every member lies in this set", never a count, so it is sound
// exactly when every element poses a SCALAR set — one element that
// poses none leaves nothing to say about an arbitrary position, and the
// whole sequence goes quiet, wearing its elements' provenance.
//
// The scalar gate is the load-bearing one. A set is read here as the
// membership claim for ONE position, so an element whose own set spells
// a longer tuple (an exact string is its code units; a nested sequence
// is its members) would have those units read back as separate
// positions of THIS array — the tuple layer concatenates rather than
// nests. Such an element poses no one-position claim, so it goes quiet
// with the rest rather than flattening into a false element set.
//
// A HOLE element (KindUndef — an elision, or a hole array's spread
// element) is the one exception the scalar gate carves out on purpose:
// it poses no set MEMBER, but "this position may be absent" is still a
// sound per-position claim, so it folds onto the union afterward via
// abstractdomain.PossiblyUndefined rather than failing the whole
// literal to unknown (`[...new Array(n), 1]`: every position is either
// absent or exactly 1).
func sequenceOfElements(elements []abstractdomain.AbstractValue) abstractdomain.AbstractValue {
	// every element GRAPH-shaped: the positions hold records or class
	// instances, so the claim is the object-star's — the join of what
	// the elements admit, at a length this literal no longer pins (an
	// unpinned spread is what brought the walk here). The tuple layer
	// holds none of it, which is why the scalar walk below would go
	// quiet on the very same values.
	if star, ok := objectStarOfElements(elements); ok {
		return star
	}
	var union *refinementsets.RefinedSet
	grade := abstractdomain.TrustProved
	sawUndef := false
	for _, element := range elements {
		// a HOLE position (an elision, or a hole array's own element
		// read) contributes no set member — undefined is not a member of
		// the kernel's number/string universe a RefinedSet ranges over —
		// but it is a legitimate per-position CONTRIBUTION on its own:
		// the position may simply be absent. Remembered here and folded
		// onto the whole star claim afterward, the same way
		// abstractdomain.PossiblyUndefined wraps any other known value
		// (finding 5: the one way a possibly-absent value is built).
		if element.Kind == abstractdomain.KindUndef {
			sawUndef = true
			grade = abstractdomain.MinTrustLevel(grade, abstractdomain.TrustLevelOf(element))
			continue
		}
		set, ok := scalarPositionSet(element)
		if !ok {
			return abstractdomain.UnknownOver(elements)
		}
		grade = abstractdomain.MinTrustLevel(grade, abstractdomain.TrustLevelOf(element))
		if union == nil {
			union = &set
		} else {
			joined := unionOf(*union, set)
			union = &joined
		}
	}
	if union == nil {
		// every position was a hole — nothing built a set to wrap, so
		// there is no star claim PossiblyUndefined could sit around;
		// stay quiet rather than overclaim
		return silence.Residue()
	}
	star := abstractdomain.KnownSet(
		refinementsets.MakeRefinedSet(refinementsets.Star(*union)),
		nil,
		grade,
		abstractdomain.SetKindTagNone,
	)
	if sawUndef {
		return abstractdomain.PossiblyUndefined(star, "", false, false)
	}
	return star
}

// objectStarOfElements builds the object-star over a literal's
// elements when EVERY one is graph-shaped: the value at an arbitrary
// position is one of them, so the element claim is their join. One
// non-graph element (a number beside the records) leaves no single
// per-position reading the graph holds, and the scalar walk gets its
// turn instead.
func objectStarOfElements(elements []abstractdomain.AbstractValue) (abstractdomain.AbstractValue, bool) {
	if len(elements) == 0 {
		return abstractdomain.AbstractValue{}, false
	}
	joined := elements[0]
	for _, element := range elements[1:] {
		joined = abstractdomain.JoinKnown(joined, element)
	}
	grade := abstractdomain.TrustProved
	for _, element := range elements {
		grade = abstractdomain.MinTrustLevel(grade, abstractdomain.TrustLevelOf(element))
	}
	return abstractdomain.KnownObjectStar(joined, grade)
}

// scalarPositionSet is the set ONE array position admits, where the
// value's own set is a claim about a single scalar. An exact number is
// its singleton; a set-known value speaks for one position only when
// its own forms are scalar ones (a range, an integer, a membership) —
// a sequence form spells several positions and answers false, as does
// every value the tuple layer cannot pose at all.
func scalarPositionSet(element abstractdomain.AbstractValue) (refinementsets.RefinedSet, bool) {
	switch element.Kind {
	case abstractdomain.KindValues:
		if element.KindTag != abstractdomain.PrimitiveNumber || len(element.Values) != 1 {
			return refinementsets.RefinedSet{}, false
		}
		return refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{element.Values[0]})), true
	case abstractdomain.KindSet:
		if element.SetKindTag != abstractdomain.SetKindTagNone {
			return refinementsets.RefinedSet{}, false
		}
		// RangeOfSet is nil exactly where the set holds no single
		// numbers — a sequence form or the bare root
		if RangeOfSet(element.Set) == nil {
			return refinementsets.RefinedSet{}, false
		}
		return element.Set, true
	default:
		return refinementsets.RefinedSet{}, false
	}
}
