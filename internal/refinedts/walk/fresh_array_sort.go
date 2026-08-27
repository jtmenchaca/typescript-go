// `sort()` / `toSorted()` on a FRESH sequence — one this walk just
// built and holds no tracked name for, like `[a, b, c].sort(cmp)` or
// `[10, 9].sort()`.
//
// readArraySortReverseMethods (array_method_models.go) owns the
// TRACKED-name case: it mutates the named binding to the new order
// through UpdateTrackedEnv. That reader gates on site.HasTrackedName,
// so a sort called directly on an array literal never reaches it — the
// same gap readFreshArrayFill closed for `.fill` on a fresh receiver.
// This reader fires only where NO tracked name exists to update, so the
// two never compete over the same call.
//
// WHAT THE ANSWER RESTS ON. SortIndexedProperties
// (specifications/javascript/spec.html, sec-sortindexedproperties)
// builds `items` by reading every present index of the receiver, then
// reorders that List in place at step-array-sort. Its stated conditions
// require "some mathematical permutation π of the non-negative integers
// less than itemCount, such that ... old[j] is exactly the same as
// new[π(j)]" — the result holds exactly the values the receiver held,
// rearranged. That is true of the implementation-defined order too: an
// inconsistent comparator (or a comparator-less sort whose ToString
// results disagree) leaves WHICH position each value lands in unstated,
// but never introduces a value that was not already in `items`.
//
// So the JOIN over the receiver's own items is what every position of
// the sorted sequence holds, whatever the comparator does. The reader
// answers the star of that join — a sequence whose every position holds
// the join, at the receiver's own count — rather than a KindList, since
// a permutation the walk did not compute cannot say which item sits at
// which index.
//
// The one case where the exact ORDER is derivable is the zero-argument
// sort over an exact numeric tuple: CompareArrayElements' default arm
// (sec-comparearrayelements, steps step-sortcompare-tostring-x/y) orders
// each pair by the ToString comparison, which is a consistent comparator
// whenever every element's decimal spelling is pinned — so the sort
// order is NOT implementation-defined there and the exact permutation
// stands. readArraySortReverseMethods already computes it for the
// tracked case; this reader runs the same reading for a fresh receiver,
// answering the exactly-ordered tuple instead of the join.

package walk

import (
	"sort"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// readFreshArraySort answers `.sort(...)` / `.toSorted(...)` on a
// receiver with no tracked name. nil where the call is not that shape,
// or where the receiver is not an exact sequence — the caller keeps
// looking.
func readFreshArraySort(site MethodCallSite) *abstractdomain.AbstractValue {
	ctx, e, receiver, method := site.Ctx, site.E, site.Receiver, site.Method
	if method != "sort" && method != "toSorted" {
		return nil
	}
	if site.HasTrackedName {
		return nil
	}
	call := e.AsCallExpression()
	var arguments []*ast.Node
	if call.Arguments != nil {
		arguments = call.Arguments.Nodes
	}
	if len(arguments) > 1 {
		return nil
	}
	items := ItemsOf(receiver)
	if items == nil {
		return nil
	}
	// an EMPTY receiver sorts to itself — the permutation over zero
	// positions, with no join to take
	if len(items) == 0 {
		out := abstractdomain.KnownList(nil, abstractdomain.TrustLevelOf(receiver))
		return &out
	}
	// the zero-argument form over an exact numeric tuple: the default
	// comparator's ToString ordering is consistent wherever every
	// element's decimal spelling is pinned, so the exact order stands
	if len(arguments) == 0 && receiver.Kind == abstractdomain.KindValues &&
		receiver.KindTag == abstractdomain.PrimitiveArray {
		if ordered, ok := lexicographicSortOf(ctx, receiver.Values); ok {
			out := abstractdomain.KnownValues(ordered, abstractdomain.PrimitiveArray, abstractdomain.TrustLevelOf(receiver))
			return &out
		}
	}
	// every other form — a comparator argument, or an element whose
	// decimal spelling the kernel declines — keeps the MULTISET and
	// loses the order: every position holds the join of the items.
	joined := items[0]
	for _, item := range items[1:] {
		joined = abstractdomain.JoinKnown(joined, item)
	}
	grade := abstractdomain.MinTrustLevel(abstractdomain.TrustLevelOf(receiver), abstractdomain.TrustLevelOf(joined))
	// a set-shaped join states the element for the tuple layer: the
	// sorted sequence is that element repeated exactly len(items)
	// times — the count is the receiver's own, which the permutation
	// preserves (itemCount in sec-sortindexedproperties)
	if elementSet, ok := abstractdomain.SetOfKnown(joined); ok {
		count := len(items)
		hi := count
		out := abstractdomain.KnownSet(
			refinementsets.Repetition(elementSet, count, &hi),
			nil, grade, abstractdomain.SetKindTagNone,
		)
		// every position the receiver held is present in the sorted
		// result — SortIndexedProperties appends one item per present
		// index and the write-back loop sets each of them, so the
		// result is dense at exactly that count
		out = abstractdomain.KnownSetDense(out)
		return &out
	}
	// a join with no set spelling (object items, a mixed shape) states
	// the same element at every position through the object-star, which
	// carries no count of its own
	if star, ok := abstractdomain.KnownObjectStar(joined, grade); ok {
		return &star
	}
	return nil
}

// lexicographicSortOf is the default comparator's own ordering over an
// exact numeric tuple: each element spelled through the kernel's proved
// decimal speller, the pairs ordered by that STRING comparison
// (sec-comparearrayelements, steps step-sortcompare-tostring-x/y), the
// permutation stable so equal spellings keep their relative places.
// (nil, false) where the kernel declines any element's spelling.
//
// The same reading readArraySortReverseMethods runs for a tracked
// receiver — kept here rather than shared because that reader also
// writes the result back through UpdateTrackedEnv, which this fresh
// receiver has no name to do.
func lexicographicSortOf(ctx *FlowContext, values []float64) ([]float64, bool) {
	type keyedValue struct {
		text  string
		value float64
	}
	keyed := make([]keyedValue, len(values))
	for i, v := range values {
		text, ok := ctx.Kernel.Decimal(v)
		if !ok {
			return nil, false
		}
		keyed[i] = keyedValue{text: text, value: v}
	}
	sort.SliceStable(keyed, func(a, b int) bool {
		return keyed[a].text < keyed[b].text
	})
	ordered := make([]float64, len(keyed))
	for i, k := range keyed {
		ordered[i] = k.value
	}
	return ordered, true
}
