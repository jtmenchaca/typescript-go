// Map and Set locals flattened into scalar slots — the size, the joined
// values, and (for a Map) the joined keys — recognized from real source
// and lowered. The kernel-walking cases are skipped (never a faked pass)
// when the native kernel dylib is absent, the same gate every other walk
// test here uses.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// mapLoweringContext is the slot layout the lowering tests below share:
// whatever slot names the case needs, under the sorts it names.
func mapLoweringContext(kernel *kernelbridge.RefinedTSKernel, bindings []string, sorts []BindingKind) *LoweringContext {
	return &LoweringContext{
		Bindings: bindings,
		Sorts:    sorts,
		Typeofs:  make([]TypeofTag, len(bindings)),
		Narrow:   kernel.Narrow,
	}
}

// mapLocalsOfSource parses a function body and answers the flattened
// collections its locals contribute.
func mapLocalsOfSource(t *testing.T, source string) map[*ast.Node]MapLocal {
	t.Helper()
	declaration := summaryDeclarationOf(t, source)
	body := declaration.Body()
	locals, ok := CollectLocals(body)
	if !ok {
		t.Fatalf("CollectLocals ok = false, want the collection local collected")
	}
	return MapLocalsOf(body, locals.Locals)
}

func TestMapSlots_AnEmptyMapNamesThreeSlots(t *testing.T) {
	flattened := mapLocalsOfSource(t,
		"function f(k: number) { const m = new Map(); m.set(k, 1); return m.size; }")
	if len(flattened) != 1 {
		t.Fatalf("MapLocalsOf found %d flattened collections, want 1", len(flattened))
	}
	for _, local := range flattened {
		if !local.IsMap {
			t.Errorf("IsMap = false, want true")
		}
		if local.SizeSlotName != "m.size" || local.ValsSlotName != "m.vals" || local.KeysSlotName != "m.keys" {
			t.Errorf("slot names = %q, %q, %q, want m.size, m.vals, m.keys",
				local.SizeSlotName, local.ValsSlotName, local.KeysSlotName)
		}
		if got := len(MapLocalSlots(local)); got != 3 {
			t.Errorf("len(MapLocalSlots) = %d, want 3", got)
		}
	}
}

func TestMapSlots_AnEmptySetNamesTwoSlotsAndNoKeys(t *testing.T) {
	flattened := mapLocalsOfSource(t,
		"function f(v: number) { const s = new Set(); s.add(v); return s.size; }")
	if len(flattened) != 1 {
		t.Fatalf("MapLocalsOf found %d flattened collections, want 1", len(flattened))
	}
	for _, local := range flattened {
		if local.IsMap {
			t.Errorf("IsMap = true, want false")
		}
		if local.KeysSlotName != "" {
			t.Errorf("KeysSlotName = %q, want empty — a Set tracks no keys", local.KeysSlotName)
		}
		slots := MapLocalSlots(local)
		if len(slots) != 2 {
			t.Fatalf("len(MapLocalSlots) = %d, want 2", len(slots))
		}
		if slots[0].Name != "s.size" || slots[1].Name != "s.vals" {
			t.Errorf("slot names = %q, %q, want s.size, s.vals", slots[0].Name, slots[1].Name)
		}
	}
}

func TestMapSlots_ASeededMapReadsItsValueSortAndTypeofFromTheRows(t *testing.T) {
	flattened := mapLocalsOfSource(t,
		`function f(k: number) { const m = new Map([["a", 1], ["b", 2]]); m.set(k, 3); return m.size; }`)
	if len(flattened) != 1 {
		t.Fatalf("MapLocalsOf found %d flattened collections, want 1", len(flattened))
	}
	for _, local := range flattened {
		if got := MapValueSort(local); got != BindingKindNumber {
			t.Errorf("value sort = %q, want number", got)
		}
		if got := MapValueTypeof(local); got != TypeofTagNumber {
			t.Errorf("value typeof = %q, want number", got)
		}
		if got := MapKeySort(local); got != BindingKindString {
			t.Errorf("key sort = %q, want string — both seeded keys are word literals", got)
		}
		if got := MapKeyTypeof(local); got != TypeofTagString {
			t.Errorf("key typeof = %q, want string", got)
		}
	}
}

func TestMapSlots_AHasCallAsAWholeIfTestKeepsTheCollectionFlattened(t *testing.T) {
	flattened := mapLocalsOfSource(t,
		"function f(k: number) { const m = new Map(); m.set(k, 1); if (m.has(k)) { return 1; } return 0; }")
	if len(flattened) != 1 {
		t.Errorf("a has() in test position declined the collection — the read touches no slot, and the opaque branch claims nothing about the condition")
	}
}

func TestMapSlots_ANegatedHasTestKeepsTheCollectionFlattened(t *testing.T) {
	negated := mapLocalsOfSource(t,
		"function f(k: number) { const m = new Map(); m.set(k, 1); if (!m.has(k)) { return 1; } return 0; }")
	if len(negated) != 1 {
		t.Errorf("a negated has() test declined the collection — `!` says no more about the result than the bare form")
	}
	parenthesized := mapLocalsOfSource(t,
		"function f(k: number) { const m = new Map(); m.set(k, 1); if ((m.has(k))) { return 1; } return 0; }")
	if len(parenthesized) != 1 {
		t.Errorf("a parenthesized has() test declined the collection — the parens carry nothing")
	}
}

func TestMapSlots_AHasCallInAWhileHeadStillDeclinesTheCollection(t *testing.T) {
	flattened := mapLocalsOfSource(t,
		"function f(k: number) { const m = new Map(); m.set(k, 1); while (m.has(k)) { m.delete(k); } return m.size; }")
	if len(flattened) != 0 {
		t.Errorf("a has() while head flattened the collection — the loop form has no opaque variant, so the body declines anyway")
	}
}

func TestMapSlots_AHasCallOutsideATestPositionStillDeclinesTheCollection(t *testing.T) {
	bound := mapLocalsOfSource(t,
		"function f(k: number) { const m = new Map(); m.set(k, 1); const b = m.has(k); return b; }")
	if len(bound) != 0 {
		t.Errorf("a has() bound to a local flattened — its result would land in a slot with no reading for it")
	}
	handedOut := mapLocalsOfSource(t,
		"function f(k: number) { const m = new Map(); m.set(k, 1); g(m.has(k)); return m.size; }")
	if len(handedOut) != 0 {
		t.Errorf("a has() passed to a callee flattened — the result leaves this body's sight")
	}
	compound := mapLocalsOfSource(t,
		"function f(k: number) { const m = new Map(); m.set(k, 1); if (m.has(k) && k > 0) { return 1; } return 0; }")
	if len(compound) != 0 {
		t.Errorf("a has() inside a compound test flattened — only the WHOLE test is admitted; a conjunct sits under a reading that would have to speak for it")
	}
	returned := mapLocalsOfSource(t,
		"function f(k: number) { const m = new Map(); m.set(k, 1); return m.has(k); }")
	if len(returned) != 0 {
		t.Errorf("a returned has() flattened — the result is the body's answer, which no slot spells")
	}
}

func TestMapSlots_ASetPredicateAsAWholeIfTestKeepsTheCollectionFlattened(t *testing.T) {
	for _, method := range []string{"isSubsetOf", "isSupersetOf", "isDisjointFrom"} {
		flattened := mapLocalsOfSource(t,
			"function f(v: number, other: Set<number>) { const s = new Set(); s.add(v); if (s."+method+"(other)) { return 1; } return 0; }")
		if len(flattened) != 1 {
			t.Errorf("%s in test position declined the collection — write-free and boolean, same shape as has()", method)
		}
	}
}

func TestMapSlots_ANegatedSetPredicateTestKeepsTheCollectionFlattened(t *testing.T) {
	negated := mapLocalsOfSource(t,
		"function f(v: number, other: Set<number>) { const s = new Set(); s.add(v); if (!s.isSubsetOf(other)) { return 1; } return 0; }")
	if len(negated) != 1 {
		t.Errorf("a negated isSubsetOf test declined the collection — `!` says no more about the result than the bare form")
	}
}

func TestMapSlots_ASetPredicateOnAMapDeclinesTheCollection(t *testing.T) {
	// none of the three exist on Map's own interface
	// (lib.es2025.collection.d.ts declares them on Set/ReadonlySet only)
	flattened := mapLocalsOfSource(t,
		"function f(k: number, other: any) { const m = new Map(); m.set(k, 1); if ((m as any).isSubsetOf(other)) { return 1; } return 0; }")
	if len(flattened) != 0 {
		t.Errorf("isSubsetOf on a Map flattened — the predicate is a Set-only operation")
	}
}

func TestMapSlots_ASetPredicateInAWhileHeadStillDeclinesTheCollection(t *testing.T) {
	flattened := mapLocalsOfSource(t,
		"function f(v: number, other: Set<number>) { const s = new Set(); s.add(v); while (s.isSubsetOf(other)) { s.delete(v); } return s.size; }")
	if len(flattened) != 0 {
		t.Errorf("isSubsetOf in a while head flattened — the loop form has no opaque variant, so the body declines anyway")
	}
}

func TestMapSlots_ASetPredicateOutsideATestPositionStillDeclinesTheCollection(t *testing.T) {
	bound := mapLocalsOfSource(t,
		"function f(v: number, other: Set<number>) { const s = new Set(); s.add(v); const b = s.isSubsetOf(other); return b; }")
	if len(bound) != 0 {
		t.Errorf("isSubsetOf bound to a local flattened — its result would land in a slot with no reading for it")
	}
	returned := mapLocalsOfSource(t,
		"function f(v: number, other: Set<number>) { const s = new Set(); s.add(v); return s.isSubsetOf(other); }")
	if len(returned) != 0 {
		t.Errorf("a returned isSubsetOf flattened — the result is the body's answer, which no slot spells")
	}
}

func TestMapSlots_AUnionOverTwoFlattenedSetsProducesANewFlattenedLocal(t *testing.T) {
	flattened := mapLocalsOfSource(t,
		"function f(x: number, y: number) { const a = new Set(); a.add(x); const b = new Set(); b.add(y); const u = a.union(b); return u.size; }")
	// three flattened locals: a, b, and the producer u
	if len(flattened) != 3 {
		t.Fatalf("MapLocalsOf found %d flattened collections, want 3 (a, b, u)", len(flattened))
	}
	found := false
	for _, local := range flattened {
		if local.Name == "u" {
			found = true
			if local.ProducerMethod != "union" {
				t.Errorf("ProducerMethod = %q, want %q", local.ProducerMethod, "union")
			}
			if local.ProducerReceiver != "a" || local.ProducerArgument != "b" {
				t.Errorf("producer operands = %q, %q, want a, b", local.ProducerReceiver, local.ProducerArgument)
			}
			if local.IsMap {
				t.Errorf("IsMap = true, want false — every producer is a Set")
			}
		}
	}
	if !found {
		t.Fatalf("u did not flatten as a producer local")
	}
}

func TestMapSlots_TheOtherThreeSetAlgebraProducersAlsoFlatten(t *testing.T) {
	for _, method := range []string{"intersection", "difference", "symmetricDifference"} {
		flattened := mapLocalsOfSource(t,
			"function f(x: number, y: number) { const a = new Set(); a.add(x); const b = new Set(); b.add(y); const u = a."+method+"(b); return u.size; }")
		if len(flattened) != 3 {
			t.Errorf("%s: MapLocalsOf found %d flattened collections, want 3 (a, b, u)", method, len(flattened))
		}
	}
}

func TestMapSlots_AProducerOverAnUnflattenedOperandDeclinesJustTheProducer(t *testing.T) {
	// b is passed to g(), which declines b's own flattening; a stays
	// flattened (its own uses are all recognized forms) but u cannot —
	// there is no source to join b's vals from
	flattened := mapLocalsOfSource(t,
		"function f(x: number, y: number) { const a = new Set(); a.add(x); const b = new Set(); b.add(y); g(b); const u = a.union(b); return u.size; }")
	names := map[string]bool{}
	for _, local := range flattened {
		names[local.Name] = true
	}
	if names["u"] {
		t.Errorf("u flattened over an unflattened operand b — there is nothing to join from")
	}
}

func TestMapSlots_AProducerOverAMapOperandDeclines(t *testing.T) {
	flattened := mapLocalsOfSource(t,
		"function f(k: number, y: number) { const m = new Map(); m.set(k, 1); const b = new Set(); b.add(y); const u = (m as any).union(b); return 1; }")
	names := map[string]bool{}
	for _, local := range flattened {
		names[local.Name] = true
	}
	if names["u"] {
		t.Errorf("u flattened over a Map receiver — none of the four algebra methods exist on Map's interface")
	}
}

func TestMapSlots_AProducerWithMismatchedValueSortsDeclines(t *testing.T) {
	flattened := mapLocalsOfSource(t,
		`function f() { const a = new Set([1, 2]); const b = new Set(["x", "y"]); const u = a.union(b); return u.size; }`)
	names := map[string]bool{}
	for _, local := range flattened {
		names[local.Name] = true
	}
	if names["u"] {
		t.Errorf("u flattened over mismatched value sorts — one value slot cannot wear both number and string")
	}
}

func TestMapSlots_AClearKeepsTheCollectionFlattened(t *testing.T) {
	flattened := mapLocalsOfSource(t,
		"function f(k: number) { const m = new Map(); m.set(k, 1); m.clear(); return m.size; }")
	if len(flattened) != 1 {
		t.Errorf("a collection with a clear() declined — MapClearAssignmentsOf resets every slot to the fresh-empty state")
	}
}

func TestMapSlots_AClearWithAnArgumentDeclinesTheCollection(t *testing.T) {
	// `clear()` takes no arguments; a call spelled with one is not the
	// recognized form and costs the whole collection its flattening,
	// exactly like any other unrecognized method call
	flattened := mapLocalsOfSource(t,
		"function f(k: number) { const m = new Map(); m.set(k, 1); (m as any).clear(k); return m.size; }")
	if len(flattened) != 0 {
		t.Errorf("a clear() with an argument flattened — that is not the recognized zero-argument form")
	}
}

func TestMapSlots_AForEachKeepsTheCollectionFlattened(t *testing.T) {
	// the callback is a NAME, not an arrow — CollectLocals declines a body
	// with a nested function outright, and what is under test here is the
	// forEach itself. The use scan admits a one-argument forEach: the
	// callback-summary route (collectionForEachStatement) reads the vals
	// and keys slots and converts the named callback, so the receiver's
	// occurrence is consumed. Whether the callback converts is the
	// lowering's question, not this scan's.
	flattened := mapLocalsOfSource(t,
		"function f(k: number) { const m = new Map(); m.set(k, 1); m.forEach(cb); return m.size; }")
	if len(flattened) != 1 {
		t.Errorf("a collection with a forEach() declined — the summary route reads the slots and converts the callback")
	}
}

func TestMapSlots_AnAliasedCollectionDeclines(t *testing.T) {
	flattened := mapLocalsOfSource(t,
		"function f(k: number) { const m = new Map(); m.set(k, 1); const other = m; return other.size; }")
	if len(flattened) != 0 {
		t.Errorf("an aliased collection flattened — the alias reads a value the slots do not build")
	}
}

func TestMapSlots_PassingTheCollectionToACalleeDeclines(t *testing.T) {
	flattened := mapLocalsOfSource(t,
		"function f(k: number) { const m = new Map(); m.set(k, 1); g(m); return m.size; }")
	if len(flattened) != 0 {
		t.Errorf("a collection passed as an argument flattened — the callee may do anything to it")
	}
}

func TestMapSlots_AnElementAccessDeclinesTheCollection(t *testing.T) {
	flattened := mapLocalsOfSource(t,
		"function f(k: number) { const m = new Map(); m.set(k, 1); return m[k]; }")
	if len(flattened) != 0 {
		t.Errorf("a collection read by element access flattened — an index read is not a get")
	}
}

func TestMapSlots_ASpreadSeedDeclines(t *testing.T) {
	flattened := mapLocalsOfSource(t,
		"function f(k: number) { const s = new Set([1, ...[2, 3]]); s.add(k); return s.size; }")
	if len(flattened) != 0 {
		t.Errorf("a spread seed flattened — its count is not the literal's own rows")
	}
}

func TestMapSlots_ASeedFromAVariableDeclines(t *testing.T) {
	flattened := mapLocalsOfSource(t,
		"function f(xs: number[]) { const s = new Set(xs); return s.size; }")
	if len(flattened) != 0 {
		t.Errorf("a collection seeded from a variable flattened — its rows name no expressions to join")
	}
}

func TestMapSlots_AWeakMapDeclines(t *testing.T) {
	flattened := mapLocalsOfSource(t,
		"function f(k: number) { const m = new WeakMap(); return 1; }")
	if len(flattened) != 0 {
		t.Errorf("a WeakMap flattened — only Map and Set are recognized")
	}
}

func TestMapSlots_AGetOrInsertKeepsTheCollectionFlattened(t *testing.T) {
	flattened := mapLocalsOfSource(t,
		"function f(k: number) { const m = new Map(); const r = m.getOrInsert(k, 1); return r; }")
	if len(flattened) != 1 {
		t.Errorf("a collection with a getOrInsert() declined — the plain-value form is a recognized Map operation")
	}
}

func TestMapSlots_AGetOrInsertOnASetDeclinesTheCollection(t *testing.T) {
	flattened := mapLocalsOfSource(t,
		"function f(v: number) { const s = new Set(); s.add(v); (s as any).getOrInsert(v, 1); return s.size; }")
	if len(flattened) != 0 {
		t.Errorf("a Set with a getOrInsert() flattened — getOrInsert is a Map-only operation")
	}
}

func TestMapSlots_AGetOrInsertComputedDeclinesTheCollection(t *testing.T) {
	// the callback is a NAME, not an arrow — CollectLocals declines a
	// body with a nested function outright, and what is under test here
	// is the getOrInsertComputed call itself
	flattened := mapLocalsOfSource(t,
		"function f(k: number) { const m = new Map(); const r = m.getOrInsertComputed(k, cb); return r; }")
	if len(flattened) != 0 {
		t.Errorf("a getOrInsertComputed() flattened — its callback second argument needs machinery this scan does not carry")
	}
}

// TestMapSlots_MapGroupByDeclines names the refusal: `Map.groupBy` is a
// static call, not a `new Map(…)` construction, so
// constructionOfNewExpression never recognizes the initializer at all —
// NOT-YET-BUILT (a construction reader for this call shape, plus a
// callback-summary-driven fresh size/key family), and separately a hard
// wall for the value slot specifically: groupBy's value shape is `T[]`,
// which the scalar-only vals slot cannot hold (ir_map_syntax.go declines
// every array/object seed value the same way).
func TestMapSlots_MapGroupByDeclines(t *testing.T) {
	// the callback is a NAME, not an arrow — CollectLocals declines a
	// body with a nested function outright, and what is under test here
	// is Map.groupBy's own construction shape
	flattened := mapLocalsOfSource(t,
		`function f(xs: number[]) { const g = Map.groupBy(xs, keySelector); return g.size; }`)
	if len(flattened) != 0 {
		t.Errorf("Map.groupBy flattened — it is a static call, not a new-Map(...) construction, and its value shape (T[]) is not scalar")
	}
}
