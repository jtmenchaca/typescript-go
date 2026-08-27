// The residue-reason sweep's collection_models.go family: WeakMap/
// WeakSet untracked-key and incomplete-miss reads, ordinary Map/Set
// incomplete-miss and unreadable-key reads, getOrInsert, delete, and
// the spec-fixed-methods fallback — each pinned by DIRECT reader call
// rather than through the RTS7002-on-`age`-write source pattern
// (residue_reason_test.go's own header explains why: Map.get
// havoc+worn, CALLS.md §6, replaces an ambient .get/.has call's
// residue with the declared return type's ground before any
// diagnostic sees it). See residue_reason_test.go's header for the
// sibling map.
package walk

import (
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
)

// residueReasonCollectionCtx is a bare FlowContext good enough for
// collection_models.go's readers: no kernel question runs on any of
// these paths (collectionKey/weakEntryIdentity/Complete are pure Go
// checks), so nil Kernel is fine — the same standing promiseShapesCtx
// takes for its own direct-call site.
func residueReasonCollectionCtx(p *program.CheckerProgram) *FlowContext {
	return &FlowContext{
		P:         p,
		Registry:  annotations.AnnotationRegistry{},
		Objects:   annotations.ObjectRegistry{},
		Contracts: map[*ast.Symbol]*FunctionContract{},
		Aliases:   dataflowfacts.NewAliasClasses(),
		Declared:  map[string]*annotations.DeclaredRefinement{},
		Report:    func(assignability.RefinementDiagnostic) {},
	}
}

// residueReasonFindCall is promiseShapesCall's pattern generalized: the
// index-th CallExpression under root whose callee names method (a
// property access OR — for the bare cases below — any call reachable
// by source order), used to hand collection_models.go's readers a REAL
// AST call node without routing the whole fixture through
// EvaluateCallExpression (whose Map.get havoc+worn standing, CALLS.md
// §6, replaces an ambient .get/.has call's residue with the declared
// return type's ground before any diagnostic sees it — the trace this
// follow-up's gate run surfaced).
func residueReasonFindCall(t *testing.T, root *ast.Node, method string, index int) *ast.Node {
	t.Helper()
	var found *ast.Node
	seen := 0
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if found != nil {
			return true
		}
		if ast.IsCallExpression(node) {
			callee := Unwrapped(node.AsCallExpression().Expression)
			if ast.IsPropertyAccessExpression(callee) &&
				callee.AsPropertyAccessExpression().Name().Text() == method {
				if seen == index {
					found = node
					return true
				}
				seen++
			}
		}
		node.ForEachChild(visit)
		return found != nil
	}
	root.ForEachChild(visit)
	if found == nil {
		t.Fatalf("no call to .%s (index %d) under the given root", method, index)
	}
	return found
}

// TestCheckAssignability_CollectionModels_WeakMapUntrackedKeyNamesItsOwnReader
// pins collection_models.go's readWeakEntrySet arm DIRECTLY: a WeakMap
// `.set` whose key isn't a plain, never-reassigned identifier can't be
// tracked by reference identity, so the write's own answer names its
// own reason.
//
// The key is a FRESH OBJECT LITERAL (`{}`), not a parameter name: a
// plain, never-reassigned identifier — even a parameter — IS exactly
// what weakEntryIdentity tracks (its own doc: "a plain, never-reassigned
// local identifier"), so `wm.set(k, 1)` with `k: object` a bare
// parameter reaches the IDENTITY-FOUND branch and answers a real
// KindCollection write, not this residue — a stronger determination,
// not this arm's own case. `{}` is not `ast.IsIdentifier` at all, so
// weakEntryIdentity fails immediately and the untracked-key residue
// this test means to pin is the one that actually runs.
//
// A source-level pin (`let age = wm.set(k, 1)`) cannot reach this
// site's ResidueReason: `.set`'s declared return is the receiver type
// itself (WeakMap<object, number>), not a bodiless ambient's bare
// return, and check_assignability.go's ResidueReason read is gated to
// known.Kind == KindUnknown at the CHECKED position — whatever
// evaluates `wm.set(k, 1)` as an expression's own value in a full walk
// does not preserve the residue this reader returns as a *value*
// (the gate run's "got bare AlertText" result). Calling the reader
// directly is the trace's own conclusion, not a weaker substitute.
func TestCheckAssignability_CollectionModels_WeakMapUntrackedKeyNamesItsOwnReader(t *testing.T) {
	p := entryEnvTestProgram(t, "function f(): void {\n"+
		"  const wm = new WeakMap<object, number>();\n"+
		"  wm.set({}, 1);\n"+
		"}\n")
	call := residueReasonFindCall(t, p.Entry.AsNode(), "set", 0)
	ctx := residueReasonCollectionCtx(p)
	site := MethodCallSite{
		Ctx: ctx, Env: NewEnv(), E: call,
		TrackedName: "wm", HasTrackedName: true,
		Receiver: abstractdomain.AbstractValue{Kind: abstractdomain.KindCollection, CollectionFlavor: abstractdomain.FlavorMap, Complete: true},
		Method:   "set",
	}
	got := readWeakEntrySet(site, site.Receiver)
	if got == nil {
		t.Fatalf("readWeakEntrySet(wm.set(k, 1)) = nil, want the untracked-key residue")
	}
	if got.Kind != abstractdomain.KindUnknown {
		t.Fatalf("readWeakEntrySet(wm.set(k, 1)) = %+v, want KindUnknown", *got)
	}
	if !strings.Contains(got.ResidueReason, "never-reassigned identifier") {
		t.Errorf("ResidueReason = %q, want it to name the never-reassigned-identifier reader", got.ResidueReason)
	}
}

// TestCheckAssignability_CollectionModels_WeakMapIncompleteMissNamesItsOwnReader
// pins collection_models.go's readWeakEntryGet arm DIRECTLY: a `.get`
// miss on a WeakMap the walk cannot prove complete stays honestly
// unknown, naming its own reason rather than claiming absence. Direct
// call for the same reason as the .set pin above — Map.get havoc+worn
// (CALLS.md §6) replaces a source-level `.get` read's value before any
// diagnostic reaches it.
func TestCheckAssignability_CollectionModels_WeakMapIncompleteMissNamesItsOwnReader(t *testing.T) {
	p := entryEnvTestProgram(t, "function f(): void {\n"+
		"  const key = {};\n"+
		"  const wm = new WeakMap<object, number>();\n"+
		"  wm.get(key);\n"+
		"}\n")
	call := residueReasonFindCall(t, p.Entry.AsNode(), "get", 0)
	ctx := residueReasonCollectionCtx(p)
	site := MethodCallSite{
		Ctx: ctx, Env: NewEnv(), E: call,
		TrackedName: "wm", HasTrackedName: true,
		Receiver: abstractdomain.AbstractValue{Kind: abstractdomain.KindCollection, CollectionFlavor: abstractdomain.FlavorMap, Complete: false},
		Method:   "get",
	}
	got := readWeakEntryGet(site, site.Receiver)
	if got == nil {
		t.Fatalf("readWeakEntryGet(wm.get(key)) = nil, want the incomplete-miss residue")
	}
	if got.Kind != abstractdomain.KindUnknown {
		t.Fatalf("readWeakEntryGet(wm.get(key)) = %+v, want KindUnknown", *got)
	}
	if !strings.Contains(got.ResidueReason, "an untracked call could have set this exact key too") {
		t.Errorf("ResidueReason = %q, want it to name the incomplete-record reason", got.ResidueReason)
	}
}

// TestCheckAssignability_CollectionModels_CollectionIncompleteMissNamesItsOwnReader
// pins collection_models.go's readCollectionGetHas arm DIRECTLY: a
// `.get` miss, with a primitive exact key, on an ordinary Map the walk
// cannot prove complete names its own reason. Direct call for the same
// Map.get havoc+worn reason as the two pins above.
func TestCheckAssignability_CollectionModels_CollectionIncompleteMissNamesItsOwnReader(t *testing.T) {
	p := entryEnvTestProgram(t, "function f(): void {\n"+
		"  const m = new Map<string, number>();\n"+
		"  m.get(\"k\");\n"+
		"}\n")
	call := residueReasonFindCall(t, p.Entry.AsNode(), "get", 0)
	ctx := residueReasonCollectionCtx(p)
	site := MethodCallSite{
		Ctx: ctx, Env: NewEnv(), E: call,
		TrackedName: "m", HasTrackedName: true,
		Receiver: abstractdomain.AbstractValue{Kind: abstractdomain.KindCollection, CollectionFlavor: abstractdomain.FlavorMap, Complete: false},
		Method:   "get",
	}
	got := readCollectionGetHas(site)
	if got == nil {
		t.Fatalf("readCollectionGetHas(m.get(\"k\")) = nil, want the incomplete-miss residue")
	}
	if got.Kind != abstractdomain.KindUnknown {
		t.Fatalf("readCollectionGetHas(m.get(\"k\")) = %+v, want KindUnknown", *got)
	}
	if !strings.Contains(got.ResidueReason, "a miss only answers when every entry is named") {
		t.Errorf("ResidueReason = %q, want it to name the incomplete-record reason", got.ResidueReason)
	}
}

// TestCheckAssignability_CollectionModels_CollectionUnreadableKeyNamesItsOwnReader
// pins collection_models.go's readCollectionGetHas arm DIRECTLY: a
// `.get` key that isn't one primitive exact value isn't a key
// collectionKey's value-equality reading can compare, so the read
// names its own reason. Direct call for the same Map.get havoc+worn
// reason as the pins above.
//
// The key is a FRESH OBJECT LITERAL, not a parameter name: a plain,
// never-reassigned object-typed identifier is exactly what
// weakEntryIdentity tracks (collection_models.go's own doc), and
// readCollectionGetHas tries readWeakEntryGet FIRST — generalized to
// ANY KindCollection with a trackable-identity key, not only an actual
// WeakMap/WeakSet — so `m.get(k)` with `k: object` a bare identifier
// reaches that reference-identity answer (a real, sound KindUndef for
// a fresh empty complete record) rather than this arm's own
// unreadable-key residue — a stronger determination, not a miss. `{}`
// is not `ast.IsIdentifier`, so weakEntryIdentity fails immediately and
// the unreadable-key residue this test means to pin is the one that
// actually runs.
func TestCheckAssignability_CollectionModels_CollectionUnreadableKeyNamesItsOwnReader(t *testing.T) {
	p := entryEnvTestProgram(t, "function f(): void {\n"+
		"  const m = new Map<object, number>();\n"+
		"  m.get({});\n"+
		"}\n")
	call := residueReasonFindCall(t, p.Entry.AsNode(), "get", 0)
	ctx := residueReasonCollectionCtx(p)
	site := MethodCallSite{
		Ctx: ctx, Env: NewEnv(), E: call,
		TrackedName: "m", HasTrackedName: true,
		Receiver: abstractdomain.AbstractValue{Kind: abstractdomain.KindCollection, CollectionFlavor: abstractdomain.FlavorMap, Complete: true},
		Method:   "get",
	}
	got := readCollectionGetHas(site)
	if got == nil {
		t.Fatalf("readCollectionGetHas(m.get(k)) = nil, want the unreadable-key residue")
	}
	if got.Kind != abstractdomain.KindUnknown {
		t.Fatalf("readCollectionGetHas(m.get(k)) = %+v, want KindUnknown", *got)
	}
	if !strings.Contains(got.ResidueReason, "collectionKey's value-equality reading can compare") {
		t.Errorf("ResidueReason = %q, want it to name the unreadable-key reason", got.ResidueReason)
	}
}

// TestCheckAssignability_CollectionModels_GetOrInsertMaybePresentNamesItsOwnReader
// pins collection_models.go's getOrInsert arm DIRECTLY: a key that may
// already be present against a record the walk cannot prove complete
// leaves the old value unnamed, so the answer names its own reason.
//
// The source-level pin reached comparison_decision.go's OWN
// kernel-declined sentence instead (AlertText + "The kernel declined
// the question") — a different residue site (this test's own
// nil-kernel walk hits a live comparison somewhere else in the body
// before getOrInsert's own residue reaches the `age` position). Direct
// call sidesteps that ordering entirely.
func TestCheckAssignability_CollectionModels_GetOrInsertMaybePresentNamesItsOwnReader(t *testing.T) {
	p := entryEnvTestProgram(t, "function f(): void {\n"+
		"  const m = new Map<string, number>();\n"+
		"  m.getOrInsert(\"k\", 2);\n"+
		"}\n")
	call := residueReasonFindCall(t, p.Entry.AsNode(), "getOrInsert", 0)
	ctx := residueReasonCollectionCtx(p)
	receiver := abstractdomain.AbstractValue{Kind: abstractdomain.KindCollection, CollectionFlavor: abstractdomain.FlavorMap, Complete: false}
	site := MethodCallSite{
		Ctx: ctx, Env: NewEnv(), E: call,
		TrackedName: "m", HasTrackedName: true,
		Receiver: receiver,
		Method:   "getOrInsert",
	}
	got := readCollectionMethods(site)
	if got == nil {
		t.Fatalf("readCollectionMethods(m.getOrInsert(\"k\", 2)) = nil, want the maybe-present residue")
	}
	if got.Kind != abstractdomain.KindUnknown {
		t.Fatalf("readCollectionMethods(m.getOrInsert(\"k\", 2)) = %+v, want KindUnknown", *got)
	}
	if !strings.Contains(got.ResidueReason, "the old value") {
		t.Errorf("ResidueReason = %q, want it to name the maybe-present reason", got.ResidueReason)
	}
}

// TestCheckAssignability_CollectionModels_GetOrInsertUnreadableKeyNamesItsOwnReader
// pins collection_models.go's getOrInsert arm: a key that isn't one
// primitive exact value still writes the collection, and where the
// receiver also spells NO value type there is nothing to name the
// inserted-or-held value with — the residue names that. A receiver
// that DOES spell `Map<K, V>` answers V's stated set joined with the
// default instead (MapStatedValueOfGetOrInsert;
// A8.xfer.getorinsert's own e2e pin), so this pin's map deliberately
// states no V.
func TestCheckAssignability_CollectionModels_GetOrInsertUnreadableKeyNamesItsOwnReader(t *testing.T) {
	residueReasonExpectSentence(t, "function f(k: object): void {\n"+
		"  const m = new Map();\n"+
		"  let age = m.getOrInsert(k, 1);\n"+
		"  age;\n"+
		"}\n", "inserted-or-held value isn't named")
}

// TestCheckAssignability_CollectionModels_DeleteIncompleteMissNamesItsOwnReader
// pins collection_models.go's write-dispatch `.delete` arm DIRECTLY: a
// delete miss on a record the walk cannot prove complete answers the
// boolean GROUND {0, 1} — `delete` returns *true* or *false* on every
// run (sec-map.prototype.delete), so the sort is determined even
// though the incomplete record leaves WHICH boolean unpinned.
//
// The source-level pin reached comparison_decision.go's own
// kernel-declined sentence instead, the same wrong-site-first failure
// GetOrInsertMaybePresent hit. Direct call sidesteps it.
func TestCheckAssignability_CollectionModels_DeleteIncompleteMissNamesItsOwnReader(t *testing.T) {
	p := entryEnvTestProgram(t, "function f(): void {\n"+
		"  const s = new Set<string>();\n"+
		"  s.delete(\"k\");\n"+
		"}\n")
	call := residueReasonFindCall(t, p.Entry.AsNode(), "delete", 0)
	ctx := residueReasonCollectionCtx(p)
	receiver := abstractdomain.AbstractValue{Kind: abstractdomain.KindCollection, CollectionFlavor: abstractdomain.FlavorSet, Complete: false}
	site := MethodCallSite{
		Ctx: ctx, Env: NewEnv(), E: call,
		TrackedName: "s", HasTrackedName: true,
		Receiver: receiver,
		Method:   "delete",
	}
	got := readCollectionMethods(site)
	if got == nil {
		t.Fatalf("readCollectionMethods(s.delete(\"k\")) = nil, want the boolean ground")
	}
	if got.Kind != abstractdomain.KindValues || got.KindTag != abstractdomain.PrimitiveBoolean {
		t.Fatalf("readCollectionMethods(s.delete(\"k\")) = %+v, want the boolean ground {0, 1}", *got)
	}
	if len(got.Values) != 2 || got.Values[0] != 0 || got.Values[1] != 1 {
		t.Errorf("Values = %v, want {0, 1}", got.Values)
	}
}

// TestCheckAssignability_CollectionModels_SpecFixedMapGetNamesItsOwnReader
// pins collection_models.go's spec-fixed-methods arm DIRECTLY: a `.get`
// on a Map receiver the walk no longer pins exactly (rejoined by a
// loop, or any other route that leaves the tracked name un-refetchable
// as a KindCollection) still answers the spec's own possibly-undefined
// shape, naming its own reason for the held value.
//
// Direct call for the same Map.get havoc+worn reason as the earlier
// .get pins — this arm is reached only once the tracked-name refetch
// in readCollectionGetHas/readCollectionMethods has ALREADY missed
// (collectionReceiver stays site.Receiver, not KindCollection), which
// a hand-built site reproduces directly by handing a non-collection
// Receiver with HasTrackedName true and TrackedName absent from env.
func TestCheckAssignability_CollectionModels_SpecFixedMapGetNamesItsOwnReader(t *testing.T) {
	p := entryEnvTestProgram(t, "function f(): void {\n"+
		"  let m = new Map<string, number>();\n"+
		"  m.get(\"k\");\n"+
		"}\n")
	call := residueReasonFindCall(t, p.Entry.AsNode(), "get", 0)
	ctx := residueReasonCollectionCtx(p)
	pa := Unwrapped(call.AsCallExpression().Expression).AsPropertyAccessExpression()
	site := MethodCallSite{
		Ctx: ctx, Env: NewEnv(), E: call, ReceiverExpression: pa.Expression,
		// no tracked name: the same shape a loop-rejoined `m` reaches this
		// arm with — readCollectionGetHas/readCollectionMethods's own
		// tracked-name refetch already failed by the time this row runs
		Receiver: abstractdomain.Unknown,
		Method:   "get",
	}
	got := readCollectionMethods(site)
	if got == nil {
		t.Fatalf("readCollectionMethods(m.get(\"k\")) = nil, want the spec-fixed possibly-undefined residue")
	}
	if got.Kind != abstractdomain.KindPossiblyUndefined || got.Inner == nil {
		t.Fatalf("readCollectionMethods(m.get(\"k\")) = %+v, want KindPossiblyUndefined wrapping the residue", *got)
	}
	if got.Inner.Kind != abstractdomain.KindUnknown {
		t.Fatalf("readCollectionMethods(m.get(\"k\")).Inner = %+v, want KindUnknown", *got.Inner)
	}
	if !strings.Contains(got.Inner.ResidueReason, "a loop rejoined it") {
		t.Errorf("Inner.ResidueReason = %q, want it to name the not-pinned-exactly reason", got.Inner.ResidueReason)
	}
}
