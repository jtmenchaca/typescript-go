// Writes THROUGH a tracked per-slot list: `name[k] = v` where k is one
// exact in-range index (the written slot takes the value, every other
// slot keeps what it held), and `name.length = n` where n truncates
// (writeListLengthExactly, at the foot of this file).
//
// ReadIndexedWrite (index_operators.go) already answers this write for
// the two receiver shapes it knew — a flat exact number tuple
// (KindValues over PrimitiveArray) and an object's keyed store. A
// KindList is the third: the shape a TUPLE-typed binding wears, where
// each position carries its own AbstractValue rather than one bare
// number (typereading/type_node.go's tuple arm and host_type.go's both
// build it). Without a row for it, `arr[2] = v` on an `arr:
// [number, number, number]` parameter fell to that function's closing
// HavocEnv and the very next `arr[2]` read had nothing left to read.
//
// WHY THE OTHER SLOTS STAND. An Array exotic object's [[Set]] on a
// canonical numeric index below the length writes exactly that index
// and touches nothing else: sec-array-exotic-objects routes the store
// through OrdinaryDefineOwnProperty, and the only cross-slot effect an
// index write has there is ArraySetLength growing `length` — which an
// index already below the length does not reach. A KindList is
// hole-free by construction, every slot written by its own builder from
// its own walked source (array_literal.go states the rule from the
// build side, element_access.go from the read side), so the value after
// the write is the same list with one item replaced.
//
// THE TWO GATES, each dropping back to the caller's own havoc:
//
//   - ONE EXACT INTEGER INDEX. A window, a union of indices, or a
//     non-integer names the written slot only vaguely, and a list has
//     no vague slot to write.
//
//   - IN RANGE. An index at or past the length GROWS the array
//     (ArraySetLength again), changing the length claim this list's own
//     item count states; a negative index writes an ordinary
//     string-keyed property no element read answers. Neither is a slot
//     replacement, so neither is claimed here.

package walk

import (
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
)

// writeListSlotExactly replaces one slot of a tracked per-slot list.
// Reports whether it wrote — false leaves ReadIndexedWrite's own
// forgetting to run, so every shape this reader declines is handled
// exactly as it was before the reader existed.
func writeListSlotExactly(
	ctx *FlowContext,
	env Env,
	name string,
	receiver abstractdomain.AbstractValue,
	index abstractdomain.AbstractValue,
	value abstractdomain.AbstractValue,
) bool {
	if receiver.Kind != abstractdomain.KindList {
		return false
	}
	if index.Kind != abstractdomain.KindValues || index.KindTag != abstractdomain.PrimitiveNumber ||
		len(index.Values) != 1 || !isInteger(index.Values[0]) {
		return false
	}
	at := index.Values[0]
	if at < 0 || int(at) >= len(receiver.Items) {
		return false
	}
	items := make([]abstractdomain.AbstractValue, len(receiver.Items))
	copy(items, receiver.Items)
	items[int(at)] = value
	// the list's reads are only as good as the weakest slot in it: a
	// slot holding a weaker claim than the list carried lowers the
	// whole list's grade rather than the written value inheriting a
	// strength it was never derived with
	grade := abstractdomain.MinTrustLevel(abstractdomain.TrustLevelOf(receiver), abstractdomain.TrustLevelOf(value))
	UpdateTrackedEnv(ctx.Aliases, env, name, abstractdomain.KnownList(items, grade))
	return true
}

// writeListLengthExactly answers `name.length = n` on a tracked array,
// in EITHER shape the walk builds one in. Reports whether it wrote —
// false leaves the caller's own forgetting to run, exactly as it did
// before this reader existed.
//
// THE TWO SHAPES. A tuple-typed or mixed-slot binding is a KindList
// carrying one AbstractValue per position. A homogeneous exact literal
// (`const arr: number[] = [1, 2, 3]`) collapses instead to a
// PrimitiveArray-tagged KindValues whose Values ARE the per-slot
// scalars, in order (array_literal.go's flat branch). Both spell the
// same runtime array, and ArraySetLength's truncation reads the same on
// each: the first newLength positions keep exactly what they held. So
// both are truncated here — reading only the KindList shape left a
// plain `number[]` literal's `.length = 1` falling through to the
// caller's havoc, which discarded the array and with it the {1} its
// own `.length` read would then have answered.
//
// WHAT THE LENGTH WRITE DOES (sec-arraysetlength). The store lands on
// the Array exotic object's [[DefineOwnProperty]] for *"length"*, which
// runs ArraySetLength:
//
//   - the written value goes through ToUint32 and through ToNumber, and
//     a mismatch between the two throws a RangeError (steps 3-5). So a
//     value that is not an exact non-negative integer below 2^32 does
//     not produce a shorter or longer array at all — this reader claims
//     nothing for one, and the caller havocs.
//
//   - newLength < oldLength DELETES every own array-index property at
//     or above newLength, in descending order (step 17). On a
//     hole-free KindList every such delete succeeds, so what remains is
//     exactly the first newLength slots, each still holding what it
//     held. That is a KindList of the same items truncated, and its
//     own `.length` read then answers exactly {newLength}
//     (evaluate_property_access.go reads len(Items)).
//
//   - newLength >= oldLength only redefines *"length"* (step 13) and
//     touches no index. The present slots keep their values and the
//     added positions are HOLES — no own property at all. The domain
//     has no shape for a list that is partly per-slot and partly holes
//     (KindArrayHoles states an all-holes array, element set ∅), so
//     the grown array is not written here and the caller havocs. The
//     shape this needs is a per-slot list carrying a present/absent
//     mark per position; naming it here is what a later reading of it
//     would build.
func writeListLengthExactly(
	ctx *FlowContext,
	env Env,
	name string,
	receiver abstractdomain.AbstractValue,
	value abstractdomain.AbstractValue,
) bool {
	flatArray := receiver.Kind == abstractdomain.KindValues &&
		receiver.KindTag == abstractdomain.PrimitiveArray
	if receiver.Kind != abstractdomain.KindList && !flatArray {
		return false
	}
	if value.Kind != abstractdomain.KindValues || value.KindTag != abstractdomain.PrimitiveNumber ||
		len(value.Values) != 1 || !isInteger(value.Values[0]) {
		return false
	}
	newLength := value.Values[0]
	// ToUint32/ToNumber disagree outside [0, 2^32) — a RangeError, not a
	// resized array
	if newLength < 0 || newLength >= 4294967296 {
		return false
	}
	grade := abstractdomain.MinTrustLevel(abstractdomain.TrustLevelOf(receiver), abstractdomain.TrustLevelOf(value))
	if flatArray {
		// a grow leaves holes exactly as it does on the per-slot shape:
		// the added positions carry no own property, and a flat value
		// list has no way to spell an absent position either
		if int(newLength) >= len(receiver.Values) {
			return false
		}
		values := make([]float64, int(newLength))
		copy(values, receiver.Values[:int(newLength)])
		UpdateTrackedEnv(ctx.Aliases, env, name,
			abstractdomain.KnownValues(values, abstractdomain.PrimitiveArray, grade))
		return true
	}
	// a grow leaves holes the per-slot shape cannot carry (banner above)
	if int(newLength) >= len(receiver.Items) {
		return false
	}
	items := make([]abstractdomain.AbstractValue, int(newLength))
	copy(items, receiver.Items[:int(newLength)])
	UpdateTrackedEnv(ctx.Aliases, env, name, abstractdomain.KnownList(items, grade))
	return true
}
