// The VALUE-precision pins the LRUCache fires demanded: the recharts
// diagnostics at LRUCache.ts:15 and :23 claimed `if (value !==
// undefined)` and `if (this.cache.has(key))` are provably false on
// every run. Both conditions are plainly reachable at runtime (set()
// fills the cache between calls). The wrong value was the FIELD
// INVARIANT: `private cache = new Map()` is never REASSIGNED, so the
// invariant answered the initializer's pinned complete-empty
// collection, and the collection models then answered `.get` exactly
// undefined and `.has` exactly false. widenInvariantCollections
// (class_field_invariants.go) sheds the pinned contents while keeping
// the Map identity; these pins hold that line.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

const fieldMapLRUCacheSource = `
	class LRUCache<K, V> {
		private cache = new Map<K, V>();
		private maxSize: number;
		get(key: K): V | undefined {
			const value = this.cache.get(key);
			if (value !== undefined) {
				this.cache.delete(key);
				this.cache.set(key, value);
			}
			return value;
		}
		set(key: K, value: V): void {
			if (this.cache.has(key)) {
				this.cache.delete(key);
			}
			this.cache.set(key, value);
		}
	}
`

// TestThisFieldMapCallStatement_GetLoweredStatementsCarryAnUnknownValueNotAbsent
// pins the SUMMARY side, which was sound all along: `value` takes a
// plain unknown effect and the branch splits on eqUndef — the summary
// never proved either arm dead.
func TestThisFieldMapCallStatement_GetLoweredStatementsCarryAnUnknownValueNotAbsent(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, fieldMapLRUCacheSource)
	declaration := fieldMapMethodOf(t, p, "get")
	summary, ok := RelowerSummaryBody(&FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration)
	if !ok {
		t.Fatalf("get declined to lower")
	}
	if len(summary.Stmts) < 2 {
		t.Fatalf("get lowered to %d statements, want at least the value assign and the branch", len(summary.Stmts))
	}
	valueAssign := summary.Stmts[0]
	if valueAssign.Kind != kernelbridge.IrStatementAssign || valueAssign.Effect.Kind != kernelbridge.LoopEffectUnknown {
		t.Errorf("stmt[0] = %+v, want an assign of a plain unknown effect into value's slot", valueAssign)
	}
	branch := summary.Stmts[1]
	if branch.Kind != kernelbridge.IrStatementBranch || branch.Test != kernelbridge.IrTestEqUndef || branch.On != valueAssign.Target {
		t.Errorf("stmt[1] = %+v, want an eqUndef branch on value's own slot", branch)
	}
}

// TestThisFieldMapWalkRoute_TheDeadBranchFiresAreGone pins the WALK
// side: analyzing get() and set() through the ordinary body walk fires
// no 7001 — the `value !== undefined` and `this.cache.has(key)` tests
// are undecidable, not provably false, once the field invariant sheds
// the empty-map contents.
func TestThisFieldMapWalkRoute_TheDeadBranchFiresAreGone(t *testing.T) {
	kernel := superArrayLoadKernel(t)
	p := entryEnvTestProgram(t, fieldMapLRUCacheSource)
	ctx := superArrayContracts(t, p)
	ctx.Kernel = kernel
	for _, method := range []string{"get", "set"} {
		var diagnostics []assignability.RefinementDiagnostic
		bodyCtx := *ctx
		bodyCtx.Report = func(d assignability.RefinementDiagnostic) {
			diagnostics = append(diagnostics, d)
		}
		declaration := fieldMapMethodOf(t, p, method)
		AnalyzeFunction(&bodyCtx, &FunctionContract{Declaration: declaration}, nil)
		for _, d := range diagnostics {
			if d.Code == 7001 {
				t.Errorf("%s's own body fired 7001 (%s) — the cache field invariant still pins the initializer's empty contents", method, d.MessageText)
			}
		}
	}
}
