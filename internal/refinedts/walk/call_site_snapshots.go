// from control_flow/call_site_snapshots.ts
//
// The call-site snapshots: the environment at every call, recorded
// DURING the one walk pass 3 already performs over each function
// body, and consumed by the call-site joins and callback seeding
// that used to re-walk the enclosing function once per query — 484
// walks for tailwindcss's staticUtility alone, each inlining every
// registration it passed (findings/read-once.md). The recording is
// owner-gated: a call records only under the walk of the function
// that LEXICALLY contains it, so an inline's walk of a callee body
// (whose calls belong to the callee) never writes a caller-specific
// state, and later fixpoint passes overwrite earlier ones — the last
// write is the stable pass. A query for a call no walk recorded
// falls back to the old per-query walk, counted, so the fallback's
// share is a number.

package walk

import (
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/tracing"
)

// snapshotStores is the TS source's `WeakMap<CheckerProgram, Map<ts.Node,
// Env>>` — one store per program, so a live editor's rebuilt program
// starts empty and the store dies with the program instance. Go has
// no weak maps; a regular map guarded by a mutex substitutes (same
// pattern as dataflowfacts's per-node memos): entries live exactly as
// long as the program that produced their nodes is in use by this
// port.
var (
	snapshotStoresMu sync.Mutex
	snapshotStores   = map[*program.CheckerProgram]map[*ast.Node]Env{}
)

// RecordCallSnapshot is recordCallSnapshot in the TS source: record
// the state a call's arguments evaluate in, when the call belongs to
// the owner being walked. The env is copied — the walk mutates its
// own map onward.
func RecordCallSnapshot(p *program.CheckerProgram, owner *ast.Node, call *ast.Node, env Env) {
	enclosing := dataflowfacts.EnclosingFunctionOf(call)
	if enclosing == nil {
		enclosing = p.Entry.AsNode()
	}
	if enclosing != owner {
		return
	}
	snapshotStoresMu.Lock()
	defer snapshotStoresMu.Unlock()
	store, ok := snapshotStores[p]
	if !ok {
		store = map[*ast.Node]Env{}
		snapshotStores[p] = store
	}
	store[call] = cloneEnv(env)
}

// CallSnapshotOf is callSnapshotOf in the TS source: the recorded
// state at a call, copied for the consumer to walk on, or (nil,
// false) where no owner walk recorded one (the consumer keeps its
// per-query walk as the counted fallback).
func CallSnapshotOf(p *program.CheckerProgram, call *ast.Node) (Env, bool) {
	snapshotStoresMu.Lock()
	var held Env
	if store, ok := snapshotStores[p]; ok {
		held = store[call]
	}
	snapshotStoresMu.Unlock()
	if held == nil {
		tracing.Count("snapshot.miss", 0)
		return nil, false
	}
	tracing.Count("snapshot.hit", 0)
	return cloneEnv(held), true
}
