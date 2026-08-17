// Pins for thisFieldMapCallStatement (ir_summary_field_map_calls.go),
// wired into SummaryCallOrHavocNamed (ir_summary_call.go). The fixture
// mirrors tmp/recharts-src/src/util/LRUCache.ts's `get` and `set`
// methods exactly.
//
// A CHECKER-BACKED program is required here, not the bare-parse
// bundleMethodOf harness other kernel_summary_direct_test.go cases
// use: onceAssignedMapField's resolvesToDefaultLib dereferences
// ctx.P.Checker unconditionally to decide whether the field's `new
// Map(...)` initializer resolves to the default lib's own
// constructor — a nil checker crashes it (pinned by this file's first
// draft, which used bundleMethodOf and panicked).
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
)

// fieldMapMethodOf finds the method named `text` in the first class
// declaration of a checker-backed program's entry source file — the
// class-method twin of entryEnvFunctionNamed (entry_env_test.go).
func fieldMapMethodOf(t *testing.T, p *program.CheckerProgram, text string) *ast.Node {
	t.Helper()
	for _, statement := range p.Entry.Statements.Nodes {
		if !ast.IsClassDeclaration(statement) {
			continue
		}
		for _, member := range statement.AsClassDeclaration().Members.Nodes {
			if !ast.IsMethodDeclaration(member) {
				continue
			}
			name := member.Name()
			if name != nil && ast.IsIdentifier(name) && name.Text() == text {
				return member
			}
		}
	}
	t.Fatalf("no method named %s in the entry source", text)
	return nil
}

// TestThisFieldMapCallStatement_LRUCacheGetCompletes pins LRUCache's
// `get` body: `this.cache.get`/`.delete`/`.set` on a once-assigned Map
// field, the middle two in bare-statement (target -1) position, the
// first as a value-target call. Before this wiring the body went
// porous at "call this.cache.get" — SummaryCallOrHavocNamed's blob tier
// never resolves a callee for a method-call expression with no
// FunctionContract, so it fell to the opaque-havoc floor.
func TestThisFieldMapCallStatement_LRUCacheGetCompletes(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
		class LRUCache<K, V> {
			private cache = new Map<K, V>();
			get(key: K): V | undefined {
				const value = this.cache.get(key);
				if (value !== undefined) {
					this.cache.delete(key);
					this.cache.set(key, value);
				}
				return value;
			}
		}
	`)
	declaration := fieldMapMethodOf(t, p, "get")
	summary, ok := RelowerSummaryBody(&FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration)
	if !ok {
		outcome, construct, recorded := SummaryOutcomeOf(declaration)
		t.Fatalf("LRUCache.get declined to lower (outcome %q, construct %q, recorded %v) — the fixture should complete", outcome, construct, recorded)
	}
	_ = summary
	outcome, construct, recorded := SummaryOutcomeOf(declaration)
	if !recorded {
		t.Fatalf("no outcome recorded for LRUCache.get")
	}
	if outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q), want SummaryComplete — this.cache.get/.delete/.set should all serve", outcome, construct)
	}
}

// TestThisFieldMapCallStatement_LRUCacheSetOutcome pins LRUCache's
// full `set` body: `this.cache.has`, `.delete`, `.size` (a property
// read in a comparison), `.keys().next().value` (the chained iterator
// read, served by ir_assignment_effect_read.go's once-assigned-map-
// field arm), and `.set`. Every construct now serves — the body is
// COMPLETE. (This pin originally asserted the porous pre-fix state and
// carried its own instruction to flip when the chained read landed.)
func TestThisFieldMapCallStatement_LRUCacheSetOutcome(t *testing.T) {
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
		t.Fatalf("no outcome recorded for LRUCache.set")
	}
	if !ok || outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q, ok %v), want SummaryComplete — has/delete/size-compare/keys().next().value/set all serve", outcome, construct, ok)
	}
}

// TestThisFieldMapCallStatement_LRUCacheSetWithoutTheChainedIteratorReadCompletes
// isolates the SAME set body with the `this.cache.keys().next().value` line
// removed — pinning that the chained-iterator read (a `.keys()` call this
// recognizer's fieldMapMethods table has no entry for; the CALL that
// produces the iterator resolves no callee, so it falls straight to the
// opaque-havoc floor) is the ONLY remaining blocker: `.has`, the two
// `.delete`s, `.size` (used only in a comparison, never assigned into a
// declaration), and `.set` all serve once it is gone.
func TestThisFieldMapCallStatement_LRUCacheSetWithoutTheChainedIteratorReadCompletes(t *testing.T) {
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
					this.cache.delete(key);
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
		t.Errorf("outcome = %q (construct %q, ok %v), want SummaryComplete — with the chained-iterator read removed, every remaining construct is has/delete/size-compare/set", outcome, construct, ok)
	}
}

// TestThisFieldMapCallStatement_ClearAndSizeMethodsComplete pins the
// remaining two LRUCache methods: `clear` (a bare `.clear()` call,
// kind "none", target -1) and `size` (a bare `.size` PROPERTY read,
// not a call at all — outside thisFieldMapCallStatement's own recognizer,
// which only matches CallExpression nodes; this states whatever the
// property-access route already answers for it).
func TestThisFieldMapCallStatement_ClearAndSizeMethodsComplete(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	clearProgram := entryEnvTestProgram(t, `
		class LRUCache<K, V> {
			private cache = new Map<K, V>();
			clear(): void {
				this.cache.clear();
			}
		}
	`)
	clearDecl := fieldMapMethodOf(t, clearProgram, "clear")
	_, clearOk := RelowerSummaryBody(&FlowContext{P: clearProgram, Contracts: map[*ast.Symbol]*FunctionContract{}}, clearDecl)
	clearOutcome, clearConstruct, clearRecorded := SummaryOutcomeOf(clearDecl)
	if !clearRecorded {
		t.Fatalf("no outcome recorded for LRUCache.clear")
	}
	if clearOutcome != SummaryComplete {
		t.Errorf("clear: outcome = %q (construct %q, ok %v), want SummaryComplete", clearOutcome, clearConstruct, clearOk)
	}

	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	sizeProgram := entryEnvTestProgram(t, `
		class LRUCache<K, V> {
			private cache = new Map<K, V>();
			size(): number {
				return this.cache.size;
			}
		}
	`)
	sizeDecl := fieldMapMethodOf(t, sizeProgram, "size")
	_, sizeOk := RelowerSummaryBody(&FlowContext{P: sizeProgram, Contracts: map[*ast.Symbol]*FunctionContract{}}, sizeDecl)
	sizeOutcome, sizeConstruct, sizeRecorded := SummaryOutcomeOf(sizeDecl)
	if !sizeRecorded {
		t.Fatalf("no outcome recorded for LRUCache.size")
	}
	t.Logf("LRUCache.size lowered ok=%v, outcome=%q, construct=%q", sizeOk, sizeOutcome, sizeConstruct)
}
