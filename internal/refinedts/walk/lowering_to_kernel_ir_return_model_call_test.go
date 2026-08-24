// Pins for HOOK 1 of the fix brief: a direct-return MODEL CALL —
// `return this.<field>.get(k)` / `return S.has(k)` — reaching
// thisFieldMapCallStatement (ir_summary_field_map_calls.go, read-only)
// and moduleSetCallStatement (ir_summary_module_set_calls.go, read-only)
// through a new arm in lowerReturnStatement
// (lowering_to_kernel_ir_return.go), tried right after RhsEffect
// declines and ahead of the await/inlining routes.
//
// BEFORE: both bodies went porous — LRUCache.size at
// "return (member this.cache.size)" (a PROPERTY read, fixed by
// onceAssignedMapFieldSizeEffect in ir_assignment_effect_read.go, pinned
// separately below) and isSvgElementPropKey's direct-return shape at
// "return (call SVGElementPropKeySet.has)" (ir_summary_module_call_test.go's
// own TestModuleSetCallStatement_TheFixtureShapeDirectReturnOutcome pin,
// which recorded the construct name before this fix and still runs
// unchanged as this hook's own before-state document).
//
// AFTER: both complete.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
)

// TestLowerReturnStatement_DirectReturnModuleSetHasCompletes pins the
// EXACT recharts fixture shape (svgPropertiesNoEvents.ts:318-323):
// `return SVGElementPropKeySet.has(key);`, the call straight in return
// position. VALUE PIN: the boolean pair {0,1} — moduleSetCallStatement's
// own "boolean" kind claim (sec-set.prototype.has always returns exactly
// true or false).
func TestLowerReturnStatement_DirectReturnModuleSetHasCompletes(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
		const SVGElementPropKeys = ['aria-label', 'aria-hidden'] as const;
		const SVGElementPropKeySet = new Set<string>(SVGElementPropKeys);
		function isSvgElementPropKey(key: string): boolean {
			return SVGElementPropKeySet.has(key);
		}
	`)
	declaration := entryEnvFunctionNamed(t, p, "isSvgElementPropKey")
	_, ok := RelowerSummaryBody(&FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(p.Checker, declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	if !ok || outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q, ok %v), want SummaryComplete — direct-return `SVGElementPropKeySet.has(key)` should serve exactly as the assign-then-return spelling already does", outcome, construct, ok)
	}
}

// TestLowerReturnStatement_DirectReturnThisFieldMapHasCompletes pins the
// class-field twin: `return this.cache.has(key);` — no intermediate
// `const`, the has call straight in return position.
func TestLowerReturnStatement_DirectReturnThisFieldMapHasCompletes(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
		class LRUCache<K, V> {
			private cache = new Map<K, V>();
			has(key: K): boolean {
				return this.cache.has(key);
			}
		}
	`)
	declaration := fieldMapMethodOf(t, p, "has")
	_, ok := RelowerSummaryBody(&FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(p.Checker, declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	if !ok || outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q, ok %v), want SummaryComplete", outcome, construct, ok)
	}
}

// TestLowerReturnStatement_DirectReturnThisFieldMapGetStillCompletesUnknown
// pins the "value" kind sibling: `return this.cache.get(key);` — the
// held value is not claimed (Map.get returns the value or undefined,
// neither of which this reading tracks), but the body still completes
// with an unknown ret rather than going porous, since nothing here loses
// track of a write.
func TestLowerReturnStatement_DirectReturnThisFieldMapGetStillCompletesUnknown(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
		class LRUCache<K, V> {
			private cache = new Map<K, V>();
			get(key: K): V | undefined {
				return this.cache.get(key);
			}
		}
	`)
	declaration := fieldMapMethodOf(t, p, "get")
	_, ok := RelowerSummaryBody(&FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(p.Checker, declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	if !ok || outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q, ok %v), want SummaryComplete", outcome, construct, ok)
	}
}
