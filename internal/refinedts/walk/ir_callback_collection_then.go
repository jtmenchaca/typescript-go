// split from ir_callback_summary.go — the NON-ARRAY receivers: forEach
// over a flattened Map or Set, and `p.then(cb)` over a promise-held
// local
package walk

import (
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// collectionForEachStatement is `m.forEach(cb)` / `s.forEach(cb)` on a
// FLATTENED Map or Set: one call statement at the collection's slots,
// with no ret — forEach's own value is undefined and nothing reads it.
//
// The entry layout follows the JS callback signature. A Map calls back
// with (value, key, map): the first entry is the values slot's var, the
// second the keys slot's var, and the third the map itself, which no
// slot holds. A Set calls back with (value, value, set) — the second
// argument stands in for the key a Set does not have, and it is the same
// value — so the second entry reads the values slot again.
//
// One application covers the whole traversal for the same reason an
// array's does: the values slot holds the JOIN of everything the
// collection can hold, and cb's summary quantifies over all entries, so
// applying it at that join covers cb's image of each individual entry.
//
// `map` and `filter` on a collection do NOT come here — a Map and a Set
// have no such methods, so those spellings decline like any other
// unrecognized receiver.
func collectionForEachStatement(context *LoweringContext, source collectionCall) ([]kernelbridge.IrStatement, bool) {
	_, valsSlot, keysSlot, keysOk, ok := mapSlotsOf(context, source.Receiver)
	if !ok {
		return nil, false
	}
	arrow := callbackFunctionOf(context, source.Callback)
	if arrow == nil {
		return nil, false
	}
	entries := collectionCallbackEntries(context, len(arrow.Parameters()), valsSlot, keysSlot, keysOk)
	converted, convertedOk := convertArrow(context, source.Callback, entries)
	if !convertedOk {
		return nil, false
	}
	call, callOk := arrowCallStatement(context, converted, -1)
	if !callOk {
		return nil, false
	}
	return []kernelbridge.IrStatement{call}, true
}

// thenStatements is `p.then(cb)` where p is a PROMISE-HELD local — a
// local the await lowering flattened to one "p.inner" slot holding the
// settled value.
//
// The call is at that slot: `then`'s callback runs on exactly the value
// p settles to, and "p.inner" is where the lowering already put it. cb
// must declare EXACTLY ONE parameter — `then`'s second argument is a
// rejection handler and its callback takes only the settled value, so a
// second declared parameter is a shape this does not model.
//
// A statement that ASSIGNS the result — `q = p.then(cb)` — makes q a
// promise-held local too: `then` answers a promise, and by the
// ret-as-inner convention cb's #ret IS what that promise settles to. So
// q gets its own "q.inner" slot, cb's ret is mapped there, and the slot
// is registered so a later `await q` reads it back through the same
// route `const p = f(…)` set up. A bare `p.then(cb);` emits the call
// with no ret.
//
// What DECLINES: a receiver that is not a promise-held local (a call
// result, an imported promise, a chained `p.then(a).then(b)` whose
// receiver is a call rather than a name), `.catch` and `.finally`
// (their callbacks run on the REJECTION, which nothing here holds), a
// callback with a parameter count other than one, and a target name
// already holding a promise slot — one name, one slot.
func thenStatements(
	context *LoweringContext,
	source collectionCall,
	target string,
) ([]kernelbridge.IrStatement, bool) {
	innerSlot, held := promiseInnerSlotOf(context, source.Receiver)
	if !held {
		return nil, false
	}
	arrow := callbackFunctionOf(context, source.Callback)
	if arrow == nil {
		return nil, false
	}
	if len(arrow.Parameters()) != 1 {
		return nil, false
	}
	entries := []callbackEntry{slotCallbackEntry(context, innerSlot)}
	converted, convertedOk := convertArrow(context, source.Callback, entries)
	if !convertedOk {
		return nil, false
	}
	// a bare `p.then(cb);` — the callback runs, its answer goes nowhere
	if target == "" {
		call, callOk := arrowCallStatement(context, converted, -1)
		if !callOk {
			return nil, false
		}
		return []kernelbridge.IrStatement{call}, true
	}
	if context.Allocate == nil {
		return nil, false
	}
	// one name, one slot: a target already flattened is a second promise
	// under the same spelling, and reusing the first's slot would let two
	// unrelated settled values share it
	if _, already := promiseInnerSlotOf(context, target); already {
		return nil, false
	}
	// the sort is UNKNOWN: what cb returns is the kernel's answer, not
	// something this site reads off syntax. An unknown-sorted slot admits
	// the definedness test alone — coverage lost, never soundness.
	targetSlot, allocated := context.Allocate(target+promiseInnerSuffix, BindingKindUnknown, TypeofTagNone)
	if !allocated {
		return nil, false
	}
	call, callOk := arrowCallStatement(context, converted, targetSlot)
	if !callOk {
		return nil, false
	}
	holdPromiseInnerSlot(context, target, targetSlot)
	return []kernelbridge.IrStatement{call}, true
}
