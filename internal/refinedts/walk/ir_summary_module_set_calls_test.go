// Pins for moduleSetCallStatement (ir_summary_module_set_calls.go),
// wired into SummaryCallOrHavocNamed (ir_summary_call.go). The fixture
// mirrors tmp/recharts-src/src/util/svgPropertiesNoEvents.ts:316-322 —
// `const SVGElementPropKeySet = new Set<string>(SVGElementPropKeys); …
// return SVGElementPropKeySet.has(key);`
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
)

// TestModuleSetCallStatement_ANamedBooleanServesThroughAssignment pins
// the ASSIGNMENT-position shape (`const ok = S.has(key); return ok;`),
// which routes through the statement lowering this file's recognizer is
// wired into. `S` is a top-level const set literally, and the fixture's
// own module-level array initializer is included so the recognizer sees
// the exact real-world shape (`new Set<string>(SVGElementPropKeys)`, an
// argument that is itself an identifier, not a literal).
func TestModuleSetCallStatement_ANamedBooleanServesThroughAssignment(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
		const SVGElementPropKeys = ['aria-label', 'aria-hidden'] as const;
		const SVGElementPropKeySet = new Set<string>(SVGElementPropKeys);
		function isSvgElementPropKey(key: string): boolean {
			const ok = SVGElementPropKeySet.has(key);
			return ok;
		}
	`)
	declaration := entryEnvFunctionNamed(t, p, "isSvgElementPropKey")
	_, ok := RelowerSummaryBody(&FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(p.Checker, declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	if !ok || outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q, ok %v), want SummaryComplete", outcome, construct, ok)
	}
}

// TestModuleSetCallStatement_TheFixtureShapeDirectReturnOutcome pins the
// EXACT fixture shape — `return SVGElementPropKeySet.has(key);`, the call
// straight in return position, no intermediate declaration. This route
// (lowerReturnStatement, lowering_to_kernel_ir_return.go) tries TestShaped/
// LowerGuard and RhsEffect/EffectOf's Opaque chain before ever reaching
// this recognizer's own wiring point (SummaryCallOrHavocNamed) — this pin
// states plainly whether one of those already carries a bare boolean-typed
// call through, or whether the direct-return shape needs its own hook
// beyond this file's territory (ir_summary_call_statement.go,
// ir_summary_field_map_calls.go, and new files).
func TestModuleSetCallStatement_TheFixtureShapeDirectReturnOutcome(t *testing.T) {
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
	t.Logf("direct-return fixture shape lowered ok=%v, outcome=%q, construct=%q", ok, outcome, construct)
}

// TestModuleSetCallStatement_DeleteAlsoServes pins `.delete(x)` on the
// same module-const receiver in a discarded-but-declared position
// (assigned to a name the body then reads), matching `.has`'s own
// boolean claim.
func TestModuleSetCallStatement_DeleteAlsoServes(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
		const Seen = new Set<string>();
		function forget(key: string): boolean {
			const removed = Seen.delete(key);
			return removed;
		}
	`)
	declaration := entryEnvFunctionNamed(t, p, "forget")
	_, ok := RelowerSummaryBody(&FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(p.Checker, declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	if !ok || outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q, ok %v), want SummaryComplete", outcome, construct, ok)
	}
}

// TestModuleSetCallStatement_AReassignedConstDoesNotServe pins the
// soundness boundary: a `let`-declared (not `const`) module binding must
// NOT serve — its identity could move, unlike a genuine once-assigned
// const.
func TestModuleSetCallStatement_ALetBindingDoesNotServe(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
		let MutableSet = new Set<string>();
		function isMember(key: string): boolean {
			const ok = MutableSet.has(key);
			return ok;
		}
	`)
	declaration := entryEnvFunctionNamed(t, p, "isMember")
	_, ok := RelowerSummaryBody(&FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(p.Checker, declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	t.Logf("let-bound receiver lowered ok=%v, outcome=%q, construct=%q", ok, outcome, construct)
	if outcome == SummaryComplete {
		t.Errorf("a let-bound (non-const) module Set served as SummaryComplete — onceAssignedModuleSetConst's const gate should have refused it")
	}
}
