// Pins for onceAssignedMapFieldSizeEffect (ir_assignment_effect_read.go):
// `this.<field>.size` on a once-assigned Map/Set field, read as a
// PROPERTY (never a call — thisFieldMapCallOf's own CallExpression gate
// cannot see it, which is why this needed a separate reader in EffectOf's
// Opaque chain rather than an entry in fieldMapMethods).
//
// A CHECKER-BACKED program is required (entryEnvTestProgram), the same
// requirement ir_summary_field_map_calls_test.go's own header states:
// onceAssignedMapField's resolvesToDefaultLib dereferences ctx.P.Checker
// unconditionally.
//
// BEFORE: LRUCache.size (tmp/recharts-src/src/util/LRUCache.ts:38-40,
// `return this.cache.size;`) went porous at
// "return (member this.cache.size)" — pinned by
// TestThisFieldMapCallStatement_ClearAndSizeMethodsComplete
// (ir_summary_field_map_calls_test.go), which only LOGS the outcome
// rather than asserting it, since it predates this fix.
//
// AFTER: completes. VALUE PIN: a non-negative integer (AtLeast(0)) —
// sec-map.prototype.size / sec-set.prototype.size define size as "the
// number of elements," never negative; not exact, since nothing here
// tracks the field's contents.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
)

// TestOnceAssignedMapFieldSizeEffect_LRUCacheSizeMethodCompletes pins the
// EXACT recharts fixture (LRUCache.ts:38-40) through the full summary
// lowering — the before/after outcome pin.
func TestOnceAssignedMapFieldSizeEffect_LRUCacheSizeMethodCompletes(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
		class LRUCache<K, V> {
			private cache = new Map<K, V>();
			size(): number {
				return this.cache.size;
			}
		}
	`)
	declaration := fieldMapMethodOf(t, p, "size")
	_, ok := RelowerSummaryBody(&FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	if !ok || outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q, ok %v), want SummaryComplete — this.cache.size should read as a non-negative integer, not go porous", outcome, construct, ok)
	}
}

// TestOnceAssignedMapFieldSizeEffect_UsedInComparisonCompletes pins the
// SECOND recharts occurrence in the same file: LRUCache.set's
// `else if (this.cache.size >= this.maxSize)` — `.size` read inside a
// comparison rather than returned directly, which goes through EffectOf
// via the comparison operator's own operand reading rather than through
// lowerReturnStatement at all. Kept beside the return-position pin above
// to show the one reader (onceAssignedMapFieldSizeEffect) serves both
// call sites, since EffectOf is shared machinery, not return-specific.
func TestOnceAssignedMapFieldSizeEffect_UsedInComparisonCompletes(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
		class LRUCache<K, V> {
			private cache = new Map<K, V>();
			private maxSize: number;
			isFull(): boolean {
				return this.cache.size >= this.maxSize;
			}
		}
	`)
	declaration := fieldMapMethodOf(t, p, "isFull")
	_, ok := RelowerSummaryBody(&FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	if !ok || outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q, ok %v), want SummaryComplete", outcome, construct, ok)
	}
}

// TestOnceAssignedMapFieldSizeEffect_ReassignedFieldDoesNotServe pins the
// soundness boundary: a field the class body reassigns AFTER its
// declaration (never a Map/Set field this recognizer's onceAssignedMapField
// gate should admit) must not serve — the shared gate thisFieldMapCallOf
// already relies on for .get/.has/.delete/.set/.clear.
func TestOnceAssignedMapFieldSizeEffect_ReassignedFieldDoesNotServe(t *testing.T) {
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
			size(): number {
				return this.cache.size;
			}
		}
	`)
	declaration := fieldMapMethodOf(t, p, "size")
	_, ok := RelowerSummaryBody(&FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	t.Logf("reassigned-field size() lowered ok=%v, outcome=%q, construct=%q", ok, outcome, construct)
	if outcome == SummaryComplete {
		t.Errorf("a REASSIGNED field's .size served as SummaryComplete — onceAssignedMapField's reassignment gate should have refused it")
	}
}
