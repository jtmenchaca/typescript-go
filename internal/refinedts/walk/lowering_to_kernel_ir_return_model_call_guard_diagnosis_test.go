// Diagnosis pin for HOOK 3 of the fix brief: a boolean-typed
// field-map/module-set model call IN GUARD POSITION —
// `if (S.has(key)) { … }`.
//
// FIRST PROBE (both-arms-return): `if (S.has(key)) { return true; }
// return false;` completes today — but that shape does not actually
// exercise TestShaped/LowerGuard's own CallExpression handling at all,
// since each `return` inside the if/else arms is an INDEPENDENT
// statement that resolves through HOOK 1's own new arm in
// lowerReturnStatement (lowering_to_kernel_ir_return.go): the `if`
// itself lowers as an ordinary two-armed branch (control_flow's own
// route, not this file's), and each arm's return is a full return
// statement in its own right. So this probe's "complete" answer is HOOK
// 1's completion showing through, not evidence TestShaped reads a model
// call.
//
// SECOND PROBE (the real guard-narrowing shape): `if (S.has(key)) { x =
// 1; } return x;` — the model call decides which BRANCH runs, with no
// return inside either arm, so nothing but LowerGuard's own condition
// reading can carry it. TestShaped is not asked here at all (this is an
// `if` STATEMENT's condition, not a boolean-shaped RETURN — TestShaped's
// only caller is the return route); the `if` statement's own condition
// lowering is IfStatement's route (control_flow/ir_if.go or its
// successor), calling LowerGuard directly. Confirms the actual blocker:
// LowerGuard's own CallExpression arm (ir_guard.go:270-282) tries only
// InlineCall, which never resolves a callee for a field-map/module-set
// model call — neither ir_guard.go nor the if-statement's own IR
// lowering file is in this agent's exclusive territory (ir_guard.go is
// the one shared condition-tree file every narrowing channel folds
// through, named explicitly in AGENT-BRIEF.md); this pin states the
// blocker precisely rather than build around it.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
)

func TestLoweringToKernelIR_ModelCallInGuardPositionBothArmsReturnDiagnosis(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
		const SVGElementPropKeys = ['aria-label', 'aria-hidden'] as const;
		const SVGElementPropKeySet = new Set<string>(SVGElementPropKeys);
		function isSvgElementPropKey(key: string): boolean {
			if (SVGElementPropKeySet.has(key)) {
				return true;
			}
			return false;
		}
	`)
	declaration := entryEnvFunctionNamed(t, p, "isSvgElementPropKey")
	_, ok := RelowerSummaryBody(&FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	t.Logf("both-arms-return guard shape lowered ok=%v, outcome=%q, construct=%q — completes via HOOK 1's return-arm route, not via TestShaped/LowerGuard reading the condition", ok, outcome, construct)
}

func TestLoweringToKernelIR_ModelCallInGuardPositionRealGuardNarrowingDiagnosis(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
		const SVGElementPropKeys = ['aria-label', 'aria-hidden'] as const;
		const SVGElementPropKeySet = new Set<string>(SVGElementPropKeys);
		function firstMatch(key: string): number {
			let x = 0;
			if (SVGElementPropKeySet.has(key)) {
				x = 1;
			}
			return x;
		}
	`)
	declaration := entryEnvFunctionNamed(t, p, "firstMatch")
	_, ok := RelowerSummaryBody(&FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	t.Logf("real guard-narrowing shape (model call decides a branch with no per-arm return) lowered ok=%v, outcome=%q, construct=%q", ok, outcome, construct)
}
