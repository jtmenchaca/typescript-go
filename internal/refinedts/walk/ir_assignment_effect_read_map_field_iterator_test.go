// Pins for onceAssignedMapFieldIteratorReadEffect
// (ir_assignment_effect_read.go): `this.<field>.keys().next().value` on
// a once-assigned Map/Set field.
//
// BEFORE: LRUCache.set (tmp/recharts-src/src/util/LRUCache.ts:22-32)
// went porous at construct "declaration" —
// TestThisFieldMapCallStatement_LRUCacheSetOutcome
// (ir_summary_field_map_calls_test.go) is that pin's own before-state
// document, and its sibling
// TestThisFieldMapCallStatement_LRUCacheSetWithoutTheChainedIteratorReadCompletes
// isolates the SAME body with the chained-iterator line removed to show
// it is the one remaining blocker.
//
// AFTER: the chained read itself lowers as UNKNOWN, havoc-free — this
// file's own before/after pin, isolated to the one declaration.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
)

// TestOnceAssignedMapFieldIteratorReadEffect_KeysNextValueCompletes pins
// the EXACT recharts fixture line in isolation: a method whose only
// statement is `const firstKey = this.cache.keys().next().value;`.
func TestOnceAssignedMapFieldIteratorReadEffect_KeysNextValueCompletes(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
		class LRUCache<K, V> {
			private cache = new Map<K, V>();
			peekOldest(): K | undefined {
				const firstKey = this.cache.keys().next().value;
				return firstKey;
			}
		}
	`)
	declaration := fieldMapMethodOf(t, p, "peekOldest")
	_, ok := RelowerSummaryBody(&FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	if !ok || outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q, ok %v), want SummaryComplete", outcome, construct, ok)
	}
}

// TestOnceAssignedMapFieldIteratorReadEffect_FullLRUCacheSetMethodCompletes
// pins the FULL recharts `set` body (LRUCache.ts:22-32) — the same
// fixture TestThisFieldMapCallStatement_LRUCacheSetOutcome pins as
// porous at "declaration" — now completing with the chained-iterator
// read included. This is the direct before/after comparison for the
// brief's HOOK 2.
func TestOnceAssignedMapFieldIteratorReadEffect_FullLRUCacheSetMethodCompletes(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
		class LRUCache<K, V> {
			private cache = new Map<K, V>();
			private maxSize: number;
			set(key: K, value: V): void {
				if (this.cache.has(key)) {
					this.cache.delete(key);
				} else if (this.cache.size >= this.maxSize) {
					const firstKey = this.cache.keys().next().value;
					if (firstKey != null) {
						this.cache.delete(firstKey);
					}
				}
				this.cache.set(key, value);
			}
		}
	`)
	declaration := fieldMapMethodOf(t, p, "set")
	_, ok := RelowerSummaryBody(&FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	if !ok || outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q, ok %v), want SummaryComplete — the chained-iterator read was the last blocker LRUCache.set had left", outcome, construct, ok)
	}
}

// TestOnceAssignedMapFieldIteratorReadEffect_ValuesAndEntriesAlsoServe
// pins the `.values()`/`.entries()` siblings of `.keys()`
// (mapIteratorMethods carries all three).
func TestOnceAssignedMapFieldIteratorReadEffect_ValuesAndEntriesAlsoServe(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
		class Registry<K, V> {
			private items = new Map<K, V>();
			firstValue(): V | undefined {
				const v = this.items.values().next().value;
				return v;
			}
		}
	`)
	declaration := fieldMapMethodOf(t, p, "firstValue")
	_, ok := RelowerSummaryBody(&FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	if !ok || outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q, ok %v), want SummaryComplete", outcome, construct, ok)
	}
}

// TestOnceAssignedMapFieldIteratorReadEffect_ReassignedFieldDoesNotServe
// pins the soundness boundary shared with the `.size` and `.get`/`.has`
// readers: a REASSIGNED field must not serve.
func TestOnceAssignedMapFieldIteratorReadEffect_ReassignedFieldDoesNotServe(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
		class Bucket<K, V> {
			private cache = new Map<K, V>();
			reset(): void {
				this.cache = new Map<K, V>();
			}
			peekOldest(): K | undefined {
				const firstKey = this.cache.keys().next().value;
				return firstKey;
			}
		}
	`)
	declaration := fieldMapMethodOf(t, p, "peekOldest")
	_, ok := RelowerSummaryBody(&FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	t.Logf("reassigned-field iterator read lowered ok=%v, outcome=%q, construct=%q", ok, outcome, construct)
	if outcome == SummaryComplete {
		t.Errorf("a REASSIGNED field's chained iterator read served as SummaryComplete — onceAssignedMapField's reassignment gate should have refused it")
	}
}
