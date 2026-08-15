// Map and Set locals flattened into scalar slots for the flow IR.
//
// A body that keeps a Map or a Set in a local — `const m = new Map()` —
// sets into it, reads out of it, asks its size, and walks it. The
// kernel's walk is over a vector of scalar slots, so such a local is
// carried as THREE slots for a Map and TWO for a Set:
//
//   - "m.size", holding the entry count as an ordinary number;
//   - "m.vals", holding the JOIN of every value the collection can hold
//     (for a Set, its members);
//   - "m.keys" — Map only — holding the join of every key.
//
// Every proved transfer applies unchanged; the kernel never learns the
// slots came from one collection. The value and key slots are weak
// summaries exactly like an array's element slot: a write joins in
// rather than replacing, so a read always over-approximates.
//
// The recognized uses, total-or-decline over EVERY occurrence of the
// name:
//
//   - `m.size` → the size slot's var.
//   - `m.set(k, v)` → size := join(size, size + 1) — a set may OVERWRITE
//     an existing key, in which case the count does not move, so both
//     readings ride; keys := join(keys, k); vals := join(vals, v).
//   - `s.add(v)` → the same without the key half.
//   - `m.get(k)` → orAbsent(vals): the value or undefined, which is
//     exactly what a missing key yields.
//   - `m.delete(k)` / `s.delete(v)` → size := join(integer ≥ 0, size).
//     A delete may MISS, so the count either stays or drops by one; the
//     two-slot world has no spelling for "the old value minus at most
//     one", so the honest claim is the whole non-negative integer ray
//     joined with the old reading — removal never grows the collection,
//     and the join keeps the old reading admitted for the miss. Keys and
//     values are untouched: dropping an entry never adds a value, so the
//     joined summaries stay sound.
//   - `for (const v of s)` / `for (const v of m.values())` → the
//     ordinary loop lowering with the binding's per-pass effect the vals
//     slot's var.
//   - `for (const k of m.keys())` → the same against the keys slot.
//   - `for (const [k, v] of m)` / `of m.entries()`, the pattern exactly
//     two plain identifiers → k from keys, v from vals.
//   - `const a = [...m.values()]` / `Array.from(m.values())` / `[...s]`
//     → an ARRAY local bridged onto these slots (ir_array_slots.go).
//   - `const n = new Map(m)` / `const t = new Set(s)` over an
//     already-flattened sibling of the SAME kind → a COPY: the new
//     collection's slots take the sibling's, read var for var. The two
//     hold the same values, so they wear the same sorts, and every
//     reader treats the copy exactly as it treats a seeded collection.
//   - `m.forEach(cb)` / `s.forEach(cb)` → ONE call statement at the
//     value slot (and the key slot second, for a Map), with no ret —
//     forEach's own value is undefined and nothing reads it. The
//     callback closure-converts through the ordinary machinery
//     (ir_callback_summary.go), so a callback that WRITES a capture
//     declines the statement rather than moving state the slots do not
//     carry. One application covers the whole traversal: the value slot
//     holds the join of everything the collection can hold and the
//     callback's summary quantifies over all entries.
//
//   - `if (m.has(k))`, the has call standing as the WHOLE test of an if
//     (parens and any leading `!` stripped) → nothing. The slots carry no
//     per-key knowledge, so the read answers no slot and writes none; the
//     statement lowers through the opaque branch, which claims nothing
//     about the condition and joins both arms. The has ARGUMENT still
//     scans — it may mention the collection again, and that occurrence
//     gets its own ruling.
//
// Everything else declines the collection: an alias, an argument, a
// return, `clear()`, a computed method name, an element access `m[k]`,
// or any method not listed. A `has` ANYWHERE but an if
// test declines too — `const b = m.has(k)` would put a value in a slot
// that has no reading for it, `f(m.has(k))` hands it out of sight, a
// conjunct in `m.has(k) && k > 0` sits under a reading that would have
// to speak for it, and a `while` head reaches the loop form, which has
// no opaque variant and declines on its own. Only the if test is
// admitted, because there the opaque branch spells exactly what is known
// about the result: nothing.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
)

// MapLocal is one flattened Map or Set local: the declaration it came
// from, the name it was spelled under, whether it is a Map (keys are
// tracked) or a Set, the slot names, and the seed literal's entries in
// source order.
//
// A COPY-CONSTRUCTED collection — `const n = new Map(m)` over an
// already-flattened sibling — has no seed literal of its own; it carries
// the sibling it was built from instead, and its slots are written from
// that sibling's. Every OTHER field, and every recognizer answer below,
// is identical to a seeded collection's: downstream readers cannot tell
// the two apart, which is the whole point of the copy.
type MapLocal struct {
	Declaration  *ast.Node // VariableDeclaration
	Name         string
	IsMap        bool
	SizeSlotName string // "m.size"
	ValsSlotName string // "m.vals"
	KeysSlotName string // "m.keys" — empty for a Set
	// SeedKeys, SeedVals: the seed literal's key and value expressions,
	// in source order. A Set's SeedKeys is nil. Both nil for an empty
	// `new Map()` / `new Set()`.
	//
	// A copy-constructed collection inherits the SIBLING's seed
	// expressions here, so MapValueSort, MapKeySort, and the two typeof
	// readings answer for it exactly as they answer for the sibling —
	// the slots hold the same values, so they wear the same sorts.
	SeedKeys []*ast.Node
	SeedVals []*ast.Node
	// CopiedFrom: the spelled name of the already-flattened collection
	// this one was constructed from, or "" for an ordinary construction.
	// Its slots are "<CopiedFrom>.size" / ".vals" / ".keys".
	CopiedFrom string
}

// The three slot spellings a flattened collection wears below its name.
const (
	mapSizeSuffix = ".size"
	mapValsSuffix = ".vals"
	mapKeysSuffix = ".keys"
)

// MapLocalOf is the recognizer: a declaration `const m = new Map()` (or
// `new Set()`, seeded or empty) whose every use in the body is one of
// the recognized forms becomes the slot family "m.size" / "m.vals" (and
// "m.keys" for a Map); anything else declines.
func MapLocalOf(body *ast.Node, declaration *ast.Node) (MapLocal, bool) {
	isMap, seed, isConstruction := collectionConstructionOf(declaration)
	if !isConstruction {
		return MapLocal{}, false
	}
	if !ast.IsIdentifier(declaration.AsVariableDeclaration().Name()) {
		return MapLocal{}, false
	}
	name := declaration.AsVariableDeclaration().Name().Text()
	keys, vals, seedOk := seedEntriesOf(seed, isMap)
	if !seedOk {
		return MapLocal{}, false
	}
	// a seed row's own expression must not mention the collection — it
	// would read slots the declaration has not written yet
	for _, entry := range append(append([]*ast.Node{}, keys...), vals...) {
		if mentionsName(entry, name) {
			return MapLocal{}, false
		}
	}
	if !usesAreAllCollectionForms(body, declaration, name, isMap) {
		return MapLocal{}, false
	}
	local := MapLocal{
		Declaration:  declaration,
		Name:         name,
		IsMap:        isMap,
		SizeSlotName: name + mapSizeSuffix,
		ValsSlotName: name + mapValsSuffix,
		SeedKeys:     keys,
		SeedVals:     vals,
	}
	if isMap {
		local.KeysSlotName = name + mapKeysSuffix
	}
	return local, true
}

// copiedMapLocalOf is the COPY recognizer: `const n = new Map(m)` /
// `const t = new Set(s)` over a collection the recognizer already
// admitted. The result is a MapLocal in every respect — the same slot
// spellings, the same use scan, the same sort and typeof answers — so
// every downstream reader treats it exactly as it treats a seeded
// collection's local. What differs is only where the slots' VALUES come
// from: the sibling's own size, values, and keys slots.
//
// A copy over a collection that is NOT flattened declines: without the
// source slots there is nothing to copy from. A copy across KINDS
// declines too — `new Set(m)` over a Map reads entry pairs, which one
// value slot cannot hold, and `new Map(s)` over a Set reads members as
// pairs, which they are not.
func copiedMapLocalOf(body *ast.Node, declaration *ast.Node, flattenedSibling func(name string) (MapLocal, bool)) (MapLocal, bool) {
	if flattenedSibling == nil {
		return MapLocal{}, false
	}
	isMap, seed, isConstruction := collectionConstructionOf(declaration)
	if !isConstruction {
		return MapLocal{}, false
	}
	if !ast.IsIdentifier(declaration.AsVariableDeclaration().Name()) {
		return MapLocal{}, false
	}
	source, isCopy := copySourceOf(seed)
	if !isCopy {
		return MapLocal{}, false
	}
	sibling, flattened := flattenedSibling(source)
	if !flattened {
		return MapLocal{}, false
	}
	// the kinds must match: the slot families only line up where both
	// collections carry the same ones
	if sibling.IsMap != isMap {
		return MapLocal{}, false
	}
	name := declaration.AsVariableDeclaration().Name().Text()
	// a collection cannot be copied from itself — at the point the
	// construction runs, its own slots have not been written yet
	if source == name {
		return MapLocal{}, false
	}
	if !usesAreAllCollectionForms(body, declaration, name, isMap) {
		return MapLocal{}, false
	}
	local := MapLocal{
		Declaration:  declaration,
		Name:         name,
		IsMap:        isMap,
		SizeSlotName: name + mapSizeSuffix,
		ValsSlotName: name + mapValsSuffix,
		// the sibling's seed expressions ride in the ordinary Seed fields,
		// so MapValueSort, MapKeySort and the typeof readings answer for
		// this local exactly as they answer for the one it copied
		SeedKeys:   sibling.SeedKeys,
		SeedVals:   sibling.SeedVals,
		CopiedFrom: source,
	}
	if isMap {
		local.KeysSlotName = name + mapKeysSuffix
	}
	return local, true
}

// MapLocalsOf runs the recognizer over a body's collected locals and
// answers the ones that flatten, keyed by declaration.
//
// TWO passes: the ordinary constructions flatten first, then the COPIES
// (`const n = new Map(m)`), which need the table the first pass built to
// know their source is flattened. A copy of a copy resolves on a later
// pass, and the passes stop as soon as one adds nothing — a cycle among
// copies cannot arise (a source must be declared before the copy reads
// it), and the fixed bound keeps the walk finite regardless.
func MapLocalsOf(body *ast.Node, locals []*ast.Node) map[*ast.Node]MapLocal {
	out := map[*ast.Node]MapLocal{}
	for _, declaration := range locals {
		if local, ok := MapLocalOf(body, declaration); ok {
			out[declaration] = local
		}
	}
	byName := map[string]MapLocal{}
	for _, collection := range out {
		byName[collection.Name] = collection
	}
	flattenedSibling := func(name string) (MapLocal, bool) {
		held, found := byName[name]
		return held, found
	}
	for added := true; added; {
		added = false
		for _, declaration := range locals {
			if _, already := out[declaration]; already {
				continue
			}
			local, ok := copiedMapLocalOf(body, declaration, flattenedSibling)
			if !ok {
				continue
			}
			out[declaration] = local
			byName[local.Name] = local
			added = true
		}
	}
	return out
}
