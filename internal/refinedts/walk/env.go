// The environment: a binding's name to its AbstractValue.
//
// Env is a mutable HANDLE onto a reference-counted copy-on-write map.
// Assignments mutate the handle in place — call-by-sharing, exactly
// the semantics the plain Go map had. Clone is O(1): it bumps the
// map's holder count, and a write through a handle copies the map
// only while OTHER handles still hold it — the count drops as holders
// diverge, so a branch split whose both sides write still pays ONE
// copy total (the second writer finds itself alone), never more than
// the old eager cloneEnv paid, and a clone that is never written (a
// call-site snapshot, a memo image, a read-only branch side) never
// pays at all.
//
// Representation: a plain Go map behind the holder count, chosen BY
// MEASUREMENT (2026-08-12) after two rejected designs: a 32-way HAMT
// (recharts +24%, nest +54% — the walk does vastly more Get/Set than
// Clone, and a trie pays per-operation what the map pays per clone)
// and a shared-flag COW whose Range marked the map shared (nest +83%
// — range-then-write is the walk's hottest pattern, and every such
// write re-copied the whole map).
//
// Range visits entries in SORTED NAME ORDER, presence-checked against
// the live map — the old Go map's own semantics (same-key updates
// visible mid-visit, deleted keys skipped) plus determinism the
// random map order never had. The name list is snapshotted, so
// deleting through the handle while ranging is safe.
//
// A nil handle reads as empty: Get misses, Len is 0, Range and Names
// visit nothing, Clone answers a fresh empty environment — the old
// code indexed nil maps safely for reads. Writes on a nil handle
// panic, exactly as writing a nil map did.
//
// The holder count is a plain int: an environment chain is owned by
// ONE walk goroutine end to end (every cross-entry store — snapshots,
// join memos, loop images — is keyed per check and read by that
// check's own goroutine), so no atomics ride the hot path.

package walk

import (
	"sort"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
)

// Env is a mutable handle onto a reference-counted copy-on-write map
// from a binding's name to its AbstractValue.
type Env = *EnvState

// EnvState is the handle: the map, the count of handles holding this
// exact map, and a sorted-name cache invalidated by every write.
type EnvState struct {
	m    map[string]abstractdomain.AbstractValue
	refs *int
	keys []string // sorted names, nil when stale
}

// NewEnv is the old Env{}: a fresh, owned, empty environment.
func NewEnv() Env {
	refs := 1
	return &EnvState{m: map[string]abstractdomain.AbstractValue{}, refs: &refs}
}

// EnvOfBindings adopts a freshly built bindings map as an
// environment — the pins builders return plain maps and hand
// ownership over. A nil map answers a nil handle (the "nothing
// pinned" sentinel the callback seams read).
func EnvOfBindings(bindings map[string]abstractdomain.AbstractValue) Env {
	if bindings == nil {
		return nil
	}
	refs := 1
	return &EnvState{m: bindings, refs: &refs}
}

// Get answers a binding's value. Nil-safe: a nil handle misses.
func (e *EnvState) Get(name string) (abstractdomain.AbstractValue, bool) {
	if e == nil {
		return abstractdomain.AbstractValue{}, false
	}
	v, ok := e.m[name]
	return v, ok
}

// ensureOwned copies the map while other handles still hold it — the
// deferred half of Clone, paid once per handle that actually writes,
// and not at all by the LAST writer standing.
func (e *EnvState) ensureOwned() {
	if *e.refs == 1 {
		return
	}
	owned := make(map[string]abstractdomain.AbstractValue, len(e.m))
	for k, v := range e.m {
		owned[k] = v
	}
	*e.refs--
	refs := 1
	e.m = owned
	e.refs = &refs
}

// Set writes a binding. Panics on a nil handle, as the nil map did.
func (e *EnvState) Set(name string, v abstractdomain.AbstractValue) {
	e.ensureOwned()
	e.m[name] = v
	e.keys = nil
}

// Delete removes a binding. Panics on a nil handle, as the nil map did.
func (e *EnvState) Delete(name string) {
	e.ensureOwned()
	delete(e.m, name)
	e.keys = nil
}

// Len is the entry count. Nil-safe.
func (e *EnvState) Len() int {
	if e == nil {
		return 0
	}
	return len(e.m)
}

// sortedKeys answers the cached sorted name list, rebuilding it when
// a write staled it.
func (e *EnvState) sortedKeys() []string {
	if e.keys == nil {
		names := make([]string, 0, len(e.m))
		for name := range e.m {
			names = append(names, name)
		}
		sort.Strings(names)
		e.keys = names
	}
	return e.keys
}

// Range visits entries in sorted name order, presence-checked against
// the live map: a same-key update inside the callback is visible to
// later visits (the old map's behavior), a Delete skips the entry,
// and the snapshotted name list makes deleting while ranging safe.
// Return false to stop. Nil-safe: a nil handle visits nothing.
func (e *EnvState) Range(visit func(name string, v abstractdomain.AbstractValue) bool) {
	if e == nil {
		return
	}
	names := e.sortedKeys()
	for _, name := range names {
		if v, ok := e.m[name]; ok {
			if !visit(name, v) {
				return
			}
		}
	}
}

// Names answers the sorted binding names, as the caller's own copy —
// safe to iterate while deleting through the handle.
func (e *EnvState) Names() []string {
	if e == nil {
		return nil
	}
	return append([]string(nil), e.sortedKeys()...)
}

// Clone answers a second handle onto the same entries, O(1): the
// holder count rises, and whichever handle writes while others still
// hold the map copies it then. A nil handle clones to a fresh empty
// environment.
func (e *EnvState) Clone() Env {
	if e == nil {
		return NewEnv()
	}
	*e.refs++
	return &EnvState{m: e.m, refs: e.refs, keys: e.keys}
}

// AsMap answers the entries as a plain map copy — the read-only
// boundary to packages that cannot name walk's Env (dataflowfacts).
func (e *EnvState) AsMap() map[string]abstractdomain.AbstractValue {
	out := make(map[string]abstractdomain.AbstractValue, e.Len())
	if e == nil {
		return out
	}
	for k, v := range e.m {
		out[k] = v
	}
	return out
}
