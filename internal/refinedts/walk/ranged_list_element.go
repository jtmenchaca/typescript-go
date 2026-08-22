// Element access into a KindList (an exact, hole-free array literal or
// built sequence — element_access.go's own doc on why a KindList is
// hole-free by construction) at a RANGED, set-shaped numeric index —
// `words[code]` where `code` is a plain number, unpinned to one exact
// value. Mirrors the kernel-side bounded-range read the Rust producer
// already lands for this shape: the runtime lands on exactly ONE
// position each run, and every position the index's own admitted
// range overlaps with [0, len) contributes its element to the join;
// where the index ALSO admits a value outside [0, len) — negative, a
// non-integer (ToPropertyKey stringifies it to a name no array index
// names), or at/past len — that run reads no own property and answers
// undefined (sec-array-exotic-objects' OrdinaryGet). Never a silent
// clamp, never a decline for a plainly bounded window: the in-bounds
// join stays exact, the possibly-undefined arm rides beside it only
// where the index's own range genuinely reaches outside [0, len).
package walk

import (
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
)

// RangedListElementOf answers a KindList read at a ranged (KindSet,
// SetKindTagNone) index, or nil where the index's range cannot be
// enclosed at all (RangeOfSet answers nil only for a sequence-shaped
// or empty set, neither of which a plain numeric ground ever is).
func RangedListElementOf(receiver abstractdomain.AbstractValue, index abstractdomain.AbstractValue) *abstractdomain.AbstractValue {
	enclosing := RangeOfSet(index.Set)
	if enclosing == nil {
		return nil
	}
	length := len(receiver.Items)

	// the in-bounds run: every integer position in [0, len) the
	// index's own range overlaps.
	lowestInBounds := 0
	if enclosing.Lo > 0 {
		lowestInBounds = int(enclosing.Lo)
		if float64(lowestInBounds) < enclosing.Lo || (enclosing.LoStrict && float64(lowestInBounds) == enclosing.Lo) {
			lowestInBounds++ // the range's own floor sits above this integer
		}
	}
	highestInBounds := length - 1
	if enclosing.Hi < float64(highestInBounds) {
		hi := enclosing.Hi
		if enclosing.HiStrict && hi == float64(int(hi)) {
			hi-- // `below` excludes its own integral ceiling
		}
		if hi < float64(highestInBounds) {
			highestInBounds = int(hi)
		}
	}

	var joined *abstractdomain.AbstractValue
	for i := lowestInBounds; i <= highestInBounds; i++ {
		if i < 0 || i >= length {
			continue
		}
		item := receiver.Items[i]
		if joined == nil {
			joined = &item
		} else {
			next := abstractdomain.JoinKnown(*joined, item)
			joined = &next
		}
	}

	// does the index's own range admit a position OUTSIDE [0, len) —
	// negative, non-integer, or at/past the array's own length? Any of
	// these reads exactly undefined at runtime (a numeric-stringified
	// key no array index names, OrdinaryGet's own miss).
	admitsOutOfBounds := enclosing.Lo < 0 || !enclosing.Int ||
		enclosing.Hi > float64(length-1) ||
		(enclosing.Hi == float64(length-1) && enclosing.HiStrict)

	if joined == nil {
		if admitsOutOfBounds {
			out := abstractdomain.Undef
			return &out
		}
		// no in-bounds position AND no out-of-bounds admission: the
		// index's own range encloses no runnable integer position at
		// all (a length-0 array, or a range this walk cannot place) —
		// the honest gap, not a claim
		return nil
	}
	if !admitsOutOfBounds {
		return joined
	}
	out := abstractdomain.PossiblyAbsent(*joined, abstractdomain.AbsentFlavorUndefOnly, abstractdomain.TrustSpec, true, true)
	return &out
}
