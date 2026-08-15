// split from ir_callback_summary.go — the callback ENTRY LAYOUT: what
// each declared parameter is filled with at a site, per collection
// shape, and the sort evidence the summary is compiled under
package walk

import (
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// callbackEntry is what ONE declared callback parameter is filled with
// at this site: the effect the call statement passes, and the sort and
// typeof evidence the summary is compiled under for that entry. The two
// halves must agree — a summary compiled with a number-sorted entry is
// applied to an effect the site promises is a number — so they are laid
// out together rather than derived twice.
//
// An entry the site cannot supply is spelled with an ABSENT effect and
// the unknown sort; an absent entry promises nothing, so the body's own
// reads of that parameter answer unknown.
type callbackEntry struct {
	Effect kernelbridge.LoopEffect
	Sort   BindingKind
	Typeof TypeofTag
}

// absentCallbackEntry is the entry a parameter the site cannot supply
// takes — the third parameter of an array or collection callback (the
// collection itself, which no slot holds), and every parameter past it.
func absentCallbackEntry() callbackEntry {
	return callbackEntry{
		Effect: kernelbridge.AbsentConst(),
		Sort:   BindingKindUnknown,
		Typeof: TypeofTagNone,
	}
}

// slotCallbackEntry is the entry a parameter fed from a CALLER SLOT
// takes: a `var` of that slot, wearing the sort and typeof the caller's
// layout already carries for it. A slot outside the layout answers the
// absent entry rather than an out-of-range read.
func slotCallbackEntry(context *LoweringContext, slot int) callbackEntry {
	if slot < 0 || slot >= len(context.Sorts) || slot >= len(context.Typeofs) {
		return absentCallbackEntry()
	}
	return callbackEntry{
		Effect: varEffect(slot),
		Sort:   context.Sorts[slot],
		Typeof: context.Typeofs[slot],
	}
}

// indexCallbackEntry is the entry an ARRAY callback's SECOND parameter
// takes — the index the traversal is at. The two-slot flattening holds
// no per-position index, but it does not have to: every concrete index
// a traversal hands out is a non-negative integer, so the integer-≥0
// constant set is an honest entry rather than a claim about which
// position the pass is on. Entering that instead of absent is what lets
// `(x, i) => x + i` convert — an absent entry has no number sort, and
// arithmetic admits only the number sort.
func indexCallbackEntry() callbackEntry {
	return callbackEntry{
		Effect: kernelbridge.LoopEffect{
			Kind: kernelbridge.LoopEffectConst,
			Set:  nonNegativeIntegerSet(),
		},
		Sort:   BindingKindNumber,
		Typeof: TypeofTagNumber,
	}
}

// arrayCallbackEntries is the entry layout an ARRAY callback's declared
// parameters take: the element, the index, then the array itself (which
// no slot holds) and anything past it absent.
func arrayCallbackEntries(context *LoweringContext, declared int, elementSlot int) []callbackEntry {
	entries := make([]callbackEntry, declared)
	for index := range entries {
		switch index {
		case 0:
			entries[index] = slotCallbackEntry(context, elementSlot)
		case 1:
			entries[index] = indexCallbackEntry()
		default:
			entries[index] = absentCallbackEntry()
		}
	}
	return entries
}

// collectionCallbackEntries is the entry layout a MAP or SET callback's
// declared parameters take under `forEach(cb)`: the value, then the KEY
// for a Map. A Set's second parameter is the VALUE AGAIN — Set.forEach
// calls back with (value, value, set), the second argument standing in
// for the key a Set does not have — so it reads the same slot. The third
// parameter is the collection itself, which no slot holds.
func collectionCallbackEntries(context *LoweringContext, declared int, valsSlot int, keysSlot int, isMap bool) []callbackEntry {
	entries := make([]callbackEntry, declared)
	for index := range entries {
		switch index {
		case 0:
			entries[index] = slotCallbackEntry(context, valsSlot)
		case 1:
			if isMap {
				entries[index] = slotCallbackEntry(context, keysSlot)
				continue
			}
			entries[index] = slotCallbackEntry(context, valsSlot)
		default:
			entries[index] = absentCallbackEntry()
		}
	}
	return entries
}

// arrowParameterSorts is the sort and typeof evidence each DECLARED
// parameter entry wears at this site, read off the entry layout the site
// built — so the summary is compiled over exactly the values each entry
// can hold.
//
// Without this the arrow's own arithmetic would decline: an unannotated
// `x => x + 1` reads its parameter through declaredParamSort, which is
// unknown for an unannotated parameter, and arithmetic admits only the
// number sort. The declaration route must stay annotation-read — its
// summary quantifies over callers no lowering can see — but an arrow
// argument has exactly one site and that site knows the sort.
func arrowParameterSorts(entries []callbackEntry) []parameterSlotSort {
	if len(entries) == 0 {
		return nil
	}
	sorts := make([]parameterSlotSort, len(entries))
	for index, entry := range entries {
		sorts[index] = parameterSlotSort{Sort: entry.Sort, TypeofTag: entry.Typeof}
	}
	return sorts
}
