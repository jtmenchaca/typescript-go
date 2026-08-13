// from evaluation/index_windows.ts
//
// The nonnegative-integer window an index lies in, and whether a
// sum row proves `s[a+b+io]` in bounds. Element-join of a sequence
// is the same exactness question a `T[]` argument asks of `result: T`.

package walk

import (
	"math"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/narrowing"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// ElementJoinOf is one element's knowledge, joined across a sequence
// — what a T[] argument contributes to a `result: T` link.
func ElementJoinOf(k abstractdomain.AbstractValue) *abstractdomain.AbstractValue {
	if k.Kind == abstractdomain.KindValues && (k.KindTag == abstractdomain.PrimitiveArray || k.KindTag == abstractdomain.PrimitiveNumber) {
		if len(k.Values) == 0 {
			return nil
		}
		out := abstractdomain.KnownSet(refinementsets.MakeRefinedSet(refinementsets.OneOf(k.Values)), nil, abstractdomain.TrustLevelOf(k), abstractdomain.SetKindTagNone)
		return &out
	}
	if k.Kind == abstractdomain.KindList {
		var joined *abstractdomain.AbstractValue
		for _, item := range k.Items {
			item := item
			if joined == nil {
				joined = &item
			} else {
				j := abstractdomain.JoinKnown(*joined, item)
				joined = &j
			}
		}
		return joined
	}
	if k.Kind == abstractdomain.KindSet {
		rep, ok := refinementsets.AsRepetition(k.Set)
		if !ok {
			return nil
		}
		element := abstractdomain.KnownSet(rep.Element, nil, abstractdomain.TrustLevelOf(k), abstractdomain.SetKindTagNone)
		// NaN rides beside the element where the sequence may hold it
		if k.NaNElements {
			out := abstractdomain.PossiblyNaN(element)
			return &out
		}
		return &element
	}
	return nil
}

// SideBoundsIn builds the SideBounds resolver over one environment.
func SideBoundsIn(ctx *FlowContext, env Env) narrowing.SideBounds {
	return func(e *ast.Node) (narrowing.Window, bool) {
		if !ReadsWithoutEffect(e) {
			return narrowing.Window{}, false
		}
		return narrowing.BoundsOfKnown(evaluateExpression(ctx, env, e))
	}
}

// IntegersOrInfinities: whether the set's forms include the
// OVERFLOW-HONEST integrality shape the add/sub transfer emits for
// unbounded integer operands: integers, or ±∞ (maxFloat + maxFloat
// rounds to Infinity, which is no integer). With the window's floor
// at 0 the −∞ arm is already gone, and a relational ceiling (`k <
// xs.length`) kills +∞ — so an in-bounds read through such an index
// is a read at an integer.
func IntegersOrInfinities(set refinementsets.RefinedSet) bool {
	onlyInteger := func(s refinementsets.RefinedSet) bool {
		return len(s.Forms) == 1 && s.Forms[0].Form == refinementsets.FormInteger
	}
	onlyInfinite := func(s refinementsets.RefinedSet) bool {
		if len(s.Forms) != 1 || s.Forms[0].Form != refinementsets.FormOneOf {
			return false
		}
		for _, v := range s.Forms[0].W {
			if !math.IsInf(v, 0) {
				return false
			}
		}
		return true
	}
	for _, f := range set.Forms {
		if f.Form != refinementsets.FormUnion {
			continue
		}
		if (onlyInteger(*f.A_) && onlyInfinite(*f.B)) || (onlyInteger(*f.B) && onlyInfinite(*f.A_)) {
			return true
		}
	}
	return false
}

// IndexWindow is the nonnegative-integer window an index's knowledge
// provably lies in — exact values by their spread, sets by their
// proved range. Nil where the knowledge admits a non-integer or a
// negative; the high side may be unbounded (the relational bound
// covers it, and it covers the +∞ arm of an overflow-honest integer
// sum the same way).
type IndexWindowResult struct {
	Lo float64
	Hi float64
}

func IndexWindow(index abstractdomain.AbstractValue) *IndexWindowResult {
	if index.Kind == abstractdomain.KindValues {
		if !abstractdomain.IsNumericKind(index) || len(index.Values) == 0 {
			return nil
		}
		lo, hi := index.Values[0], index.Values[0]
		for _, v := range index.Values {
			if !isInteger(v) || v < 0 {
				return nil
			}
			if v < lo {
				lo = v
			}
			if v > hi {
				hi = v
			}
		}
		return &IndexWindowResult{Lo: lo, Hi: hi}
	}
	if index.Kind == abstractdomain.KindSet && index.SetKindTag == abstractdomain.SetKindTagNone {
		r := RangeOfSet(index.Set)
		if r == nil {
			return nil
		}
		if !r.Int && !IntegersOrInfinities(index.Set) {
			return nil
		}
		if !(r.Lo >= 0) {
			return nil
		}
		return &IndexWindowResult{Lo: r.Lo, Hi: r.Hi}
	}
	return nil
}

// SumIndexInBounds: does a sum row prove `s[a + b + io]` in bounds
// under the array's length? The row says fl(a + b + ro) ≤ length;
// with a and b nonnegative integers on every run and the length
// under 2^32 (sec-array-exotic-objects), the computed sum equals the
// real one — an integer sum past 2^53 rounds to at least 2^53, which
// the anchor's bound rules out — so the real sum obeys the row and
// the read clears the last slot when io ≤ ro − 1 (one more under a
// strict row).
func SumIndexInBounds(
	ctx *FlowContext,
	env Env,
	indexExpression *ast.Node,
	lengthPlace dataflowfacts.PlaceKey,
) bool {
	parsed := dataflowfacts.SumPlacesOf(ctx.P.Checker, indexExpression)
	if parsed == nil {
		return false
	}
	rows := ctx.SumConstraints
	if len(rows) == 0 {
		return false
	}
	// the fl-exactness premise, per place: a nonnegative integer on
	// every run keeps the computed sum equal to the real one under
	// the anchor's 2^32 length bound
	nonnegInt := func(place dataflowfacts.PlaceKey) bool {
		if place.Path != "" {
			return false
		}
		held, ok := env.Get(place.BaseName)
		if !ok {
			return false
		}
		r := RangeOfKnown(held)
		return r != nil && r.Int && r.Lo >= 0
	}
	for _, term := range parsed.Terms {
		if !nonnegInt(term) {
			return false
		}
	}
	// every sum row entering the system carries the same vouching —
	// its own terms nonnegative integers under THIS length anchor
	var vouched []dataflowfacts.SumConstraint
	for _, row := range rows {
		if !dataflowfacts.SamePlace(row.Anchor, lengthPlace) {
			continue
		}
		allNonnegInt := true
		for _, term := range row.Terms {
			if !nonnegInt(term) {
				allNonnegInt = false
				break
			}
		}
		if allNonnegInt {
			vouched = append(vouched, row)
		}
	}
	if len(vouched) == 0 {
		return false
	}
	// the decider speaks about REALS, so the question is strict —
	// length − Σ terms > offset — and the terms' integrality closes
	// the gap to the last slot. The difference rows join the SAME
	// system, so `offset + length ≤ s.length` with `k < length`
	// proves `s[offset + k]` where no single row could.
	return dataflowfacts.MixedConstraintsImply(ctx.Kernel, ctx.DifferenceConstraints, vouched, dataflowfacts.MixedConstraintsTarget{
		Plus:   []dataflowfacts.PlaceKey{lengthPlace},
		Minus:  parsed.Terms[:],
		Bound:  parsed.Offset,
		Strict: true,
	})
}
