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

func TestMapSlots_AClearDeclinesTheCollection(t *testing.T) {
	flattened := mapLocalsOfSource(t,
		"function f(k: number) { const m = new Map(); m.set(k, 1); m.clear(); return m.size; }")
	if len(flattened) != 0 {
		t.Errorf("a collection with a clear() flattened — the value slot would keep values the collection no longer holds")
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
