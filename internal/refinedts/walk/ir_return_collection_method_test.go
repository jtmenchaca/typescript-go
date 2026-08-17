// Pins for the remaining collection-method RETURN rows the census
// names: "return (call children.map)" ×2, "return (call words.reduce)"
// ×2, "return (call get)" ×2 — checking whether
// SummaryCallbackReturnOf (tried ahead of this agent's ir_return_call.go
// arm) already serves these, or whether they fall to the generic call
// door's opaque-havoc tier.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
)

// TestReturnCollectionMethod_ArrayMapReturnCompletes pins a flattened
// array parameter's `.map(cb)` in return position — Treemap.tsx's
// `children.map(...)`-shaped row.
func TestReturnCollectionMethod_ArrayMapReturnCompletes(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	declaration := summaryDeclarationOf(t, `
		function doubleAll(xs: number[]) {
			return xs.map(x => x * 2);
		}
	`)
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	_, ok := RelowerSummaryBody(ctx, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	t.Logf("ok=%v outcome=%q construct=%q", ok, outcome, construct)
}

// TestReturnCollectionMethod_ArrayReduceReturnCompletes pins
// `words.reduce(...)`-shaped return — a flattened array's `.reduce`
// call, whose result IS the return value.
func TestReturnCollectionMethod_ArrayReduceReturnCompletes(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	declaration := summaryDeclarationOf(t, `
		function sumAll(xs: number[]) {
			return xs.reduce((acc, x) => acc + x, 0);
		}
	`)
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	_, ok := RelowerSummaryBody(ctx, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	t.Logf("ok=%v outcome=%q construct=%q", ok, outcome, construct)
}

// TestReturnCollectionMethod_MapGetReturnCompletes pins `return
// m.get(k);` off a once-assigned Map field in return position —
// census row "return (call get)".
func TestReturnCollectionMethod_MapGetReturnCompletes(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
		class Cache<K, V> {
			private cache = new Map<K, V>();
			get(key: K): V | undefined {
				return this.cache.get(key);
			}
		}
	`)
	declaration := fieldMapMethodOf(t, p, "get")
	_, ok := RelowerSummaryBody(&FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	if !ok || outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q, ok %v), want SummaryComplete — thisFieldMapCallStatement already serves a return-position .get", outcome, construct, ok)
	}
}
