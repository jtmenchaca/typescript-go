// The Array constructor's own RESULT model — `Array(…)` and
// `new Array(…)` build the same array: the one algorithm at sec-array
// (oldids sec-array-len, sec-array-items; tmp/ecma262/spec.html) runs
// for both spellings — a call without NewTarget takes the active
// function object and continues through the same steps. The rows,
// each a step of that algorithm:
//
//   - no arguments → ArrayCreate(0): the empty array.
//   - one NUMBER argument len → an array whose length is exactly
//     ToUint32(len) and whose slots are HOLES — reading one answers
//     undefined, the same claim an elision's slot wears
//     (array_literal.go). A len that ToUint32 does not fix exactly
//     throws a RangeError instead ("If SameValueZero(intLength,
//     length) is false, throw a RangeError exception") — the argument
//     contract in builtin_contracts.go fires there, and no array
//     exists for this model to answer. Below the materialization
//     ceiling the holes build a KindList (every consumer — spread,
//     destructuring, iteration, join — reads it exactly); at or past
//     it, KindArrayHoles carries the same claim (exact length, every
//     slot a hole) without allocating one AbstractValue per slot.
//   - one NON-number argument → the one-element array of it ("If
//     length is not a Number … CreateDataPropertyOrThrow(array, "0",
//     length)"). Answered only where the value's sort is pinned
//     non-number — an argument that MAY be a number may be a length.
//   - two or more arguments → the array of them in order, the same
//     build the array literal and Array.of make.
//
// The argument-side judgment (the ToUint32-exactness contract) is
// builtin_contracts.go's and runs before this in both callers; this
// file answers only the VALUE.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
)

// arrayConstructionHoleLimit caps the MATERIALIZED hole list — the
// same ceiling readArrayFrom's counted form keeps. Past it, the array
// is not built element-by-element: KnownArrayHoles carries the same
// exact claim (length pinned, every slot undefined) as one struct
// with a length field, no per-slot allocation. The ceiling now bounds
// only which of the two REPRESENTATIONS answers, never whether the
// construction itself is claimed.
const arrayConstructionHoleLimit = 10_000

// ReadArrayConstruction answers what `Array(…)` / `new Array(…)` on
// the default-lib constructor BUILDS, or nil where no row above
// speaks (a spread argument, a length the walk has not pinned to one
// exact integer, an argument whose sort could be number).
func ReadArrayConstruction(ctx *FlowContext, env Env, e *ast.Node) *abstractdomain.AbstractValue {
	callee := calleeOf(e)
	if callee == nil || !ast.IsIdentifier(callee) || callee.Text() != "Array" ||
		!resolvesToDefaultLib(ctx, callee) {
		return nil
	}
	args, hasArgs := callArguments(e)
	if !hasArgs || len(args) == 0 {
		// ArrayCreate(0): the empty array, exactly
		out := abstractdomain.KnownValues([]float64{}, abstractdomain.PrimitiveArray, abstractdomain.TrustSpec)
		return &out
	}
	for _, argument := range args {
		if ast.IsSpreadElement(argument) {
			// how many positions the spread fills decides which row runs;
			// an unexpanded spread pins none of them
			return nil
		}
	}
	if len(args) == 1 {
		known := evaluateExpression(ctx, env, args[0])
		grade := abstractdomain.MinTrustLevel(abstractdomain.TrustSpec, abstractdomain.TrustLevelOf(known))
		// one NUMBER argument: the length row
		if known.Kind == abstractdomain.KindValues && known.KindTag == abstractdomain.PrimitiveNumber &&
			len(known.Values) == 1 {
			length := known.Values[0]
			if !isNonNegativeInteger(length) || length > 4294967295 {
				// ToUint32 does not fix this length exactly: the
				// construction throws (the contract row fired), so there
				// is no array to answer
				return nil
			}
			if length == 0 {
				out := abstractdomain.KnownValues([]float64{}, abstractdomain.PrimitiveArray, grade)
				return &out
			}
			if length > arrayConstructionHoleLimit {
				// past the materialization ceiling: the same claim — length
				// exactly this, every slot a hole — without allocating one
				// AbstractValue per slot. Dense=false: sec-array's algorithm
				// never calls CreateDataPropertyOrThrow for a hole array, so
				// no index is an own property (SPARSE) — unlike
				// Array.from({length: n})'s dense fill (array_method_models.go).
				out := abstractdomain.KnownArrayHoles(int(length), grade, false)
				return &out
			}
			items := make([]abstractdomain.AbstractValue, int(length))
			for i := range items {
				items[i] = abstractdomain.Undef
			}
			out := abstractdomain.KnownList(items, grade)
			return &out
		}
		// one argument whose sort is PINNED non-number: the one-element
		// array of it. A value that may still be a number (a residue, a
		// number set, possibly-NaN) may be a length, and neither row can
		// claim it.
		if arrayArgumentPinnedNonNumber(known) {
			out := abstractdomain.KnownList([]abstractdomain.AbstractValue{known}, grade)
			return &out
		}
		return nil
	}
	// two or more arguments: the array of them in order — the array
	// literal's own build (all-scalar lists collapse to the flat number
	// tuple, anything else stays the exact list)
	items := make([]abstractdomain.AbstractValue, len(args))
	for i, argument := range args {
		items[i] = evaluateExpression(ctx, env, argument)
	}
	flat := true
	floor := abstractdomain.TrustSpec
	for _, item := range items {
		floor = abstractdomain.MinTrustLevel(floor, abstractdomain.TrustLevelOf(item))
		if !(item.Kind == abstractdomain.KindValues && len(item.Values) == 1 &&
			item.KindTag == abstractdomain.PrimitiveNumber) {
			flat = false
		}
	}
	if flat {
		values := make([]float64, len(items))
		for i, item := range items {
			values[i] = item.Values[0]
		}
		out := abstractdomain.KnownValues(values, abstractdomain.PrimitiveArray, floor)
		return &out
	}
	out := abstractdomain.KnownList(items, floor)
	return &out
}

// arrayArgumentPinnedNonNumber: the argument's knowledge PINS a sort
// that is not number, so sec-array's "If length is not a Number" arm
// is the one that runs. NaN and possibly-NaN stay out — NaN IS a
// Number and takes the throwing length arm; an unknown, a number set,
// and a maybe-wrapper pin nothing.
func arrayArgumentPinnedNonNumber(known abstractdomain.AbstractValue) bool {
	switch known.Kind {
	case abstractdomain.KindValues:
		return known.KindTag != abstractdomain.PrimitiveNumber
	case abstractdomain.KindList, abstractdomain.KindArrayHoles, abstractdomain.KindObject, abstractdomain.KindObjectStar,
		abstractdomain.KindCollection, abstractdomain.KindDate, abstractdomain.KindRegex,
		abstractdomain.KindSymbol, abstractdomain.KindBigints, abstractdomain.KindHostFunction,
		abstractdomain.KindPromise, abstractdomain.KindUndef:
		return true
	default:
		return false
	}
}
