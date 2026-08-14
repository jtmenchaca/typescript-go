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
			// an unpinned spread loses the LENGTH — its own elements are
			// what it admits, and the walk keeps building past it
			lengthKnown = false
			elements = append(elements, ElementOf(spread))
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
func sequenceOfElements(elements []abstractdomain.AbstractValue) abstractdomain.AbstractValue {
	var union *refinementsets.RefinedSet
	grade := abstractdomain.TrustProved
	for _, element := range elements {
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
		return silence.Residue()
	}
	return abstractdomain.KnownSet(
		refinementsets.MakeRefinedSet(refinementsets.Star(*union)),
		nil,
		grade,
		abstractdomain.SetKindTagNone,
	)
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
