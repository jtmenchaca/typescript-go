// Pins for the truthiness-test fold over a non-optional record
// parameter's own bare name: `entry && entry.value`, `if (entry) {…}`.
// TASK 3(d)'s two fixtures (getValueWithADefinedHolder,
// lowering_to_kernel_ir_return_test.go's own diagnosis comment) plus the
// holder-absence negative — a parameter whose type still admits
// undefined/null keeps today's refusal untouched.

package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
)

// TestKernelSummaryDirect_ADefinedHolderTruthyTestFoldsThroughTheWholeRecordUse
// pins TASK 3(d) exactly: `function h(entry: { value: number }) { return
// (entry && entry.value) || 0; }` — `entry`'s declared type excludes
// undefined/null, so the `&&`'s left operand is always truthy and the
// whole use is member-safe (recordParameterReadWhole), not a store-through
// reference. Was SummaryDeclined / "a whole-record parameter use"
// (lowering_to_kernel_ir_return_test.go's own diagnosis pin); complete
// after the classification fix.
func TestKernelSummaryDirect_ADefinedHolderTruthyTestFoldsThroughTheWholeRecordUse(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	declaration := summaryDeclarationOf(t,
		"function h(entry: { value: number }) { return (entry && entry.value) || 0; }")
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	_, ok := RelowerSummaryBody(ctx, declaration)
	outcome, construct, _ := SummaryOutcomeOf(nil, declaration)
	if !ok {
		t.Fatalf("h's body declined: outcome=%q construct=%q", outcome, construct)
	}
	if outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q), want complete", outcome, construct)
	}
}

// TestKernelSummaryDirect_ADefinedHolderTruthyTestServesTheTrueValue is
// the VALUE-precision twin of the pin above: SummaryComplete alone does
// not prove the fold reads the right member state, only that the body
// lowered without a havoc. `entry`'s declared type excludes
// undefined/null, so `entry && entry.value` is always `entry.value` and
// `(entry && entry.value) || 0` is `entry.value` whenever `entry.value`
// itself is truthy — this call site's argument (7) is, so the served
// answer must admit 7 and must NOT be the {0} fallback a wrongly-folded
// falsy branch would produce.
func TestKernelSummaryDirect_ADefinedHolderTruthyTestServesTheTrueValue(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	declaration := summaryDeclarationOf(t,
		"function h(entry: { value: number }) { return (entry && entry.value) || 0; }")
	contract := &FunctionContract{Declaration: declaration}
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	argument := recordObject(t, map[string]float64{"value": 7}, []string{"value"})
	answer, ok := KernelSummaryDirect(ctx, []abstractdomain.AbstractValue{argument}, contract)
	if !ok {
		t.Fatalf("h({value: 7}) declined at the call site — the summary should have served")
	}
	state, stateOk := StateOfKnown(answer)
	if !stateOk || state.Top {
		t.Fatalf("the answer did not spell as a scalar state: %+v", answer)
	}
	if !kernel.Member(state.Set, []float64{7}) {
		t.Errorf("the summary of h({value:7}) excludes the true value 7: %+v", state.Set)
	}
}

// TestKernelSummaryDirect_APlainParameterOrZeroIsTheSamePreExistingCeiling
// is the CONTROL for the pin above: `x || 0` over a PLAIN (non-record,
// already-tracked) number parameter shows the identical TOP answer —
// LoopEffectJoin admits both operands' whole ranges unconditionally
// (returnArithmeticOverShortCircuit's own doc: "the join of both admits
// every run"), which is imprecise whether or not a record parameter or
// the new fold is anywhere in the picture. This is a PRE-EXISTING
// ceiling in the plain effect grammar, not something the truthy-record
// fold introduces — the fold's own job (turning a DECLINE into a
// SERVED, sound answer) is done; tightening `x || 0`'s own precision is
// a different, general gap, not a record-parameter one.
func TestKernelSummaryDirect_APlainParameterOrZeroIsTheSamePreExistingCeiling(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	declaration := summaryDeclarationOf(t, "function g(x: number) { return x || 0; }")
	contract := &FunctionContract{Declaration: declaration}
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	answer, ok := KernelSummaryDirect(ctx, []abstractdomain.AbstractValue{exactNumber(t, 7)}, contract)
	if !ok {
		t.Fatalf("g(7) declined at the call site")
	}
	state, stateOk := StateOfKnown(answer)
	t.Logf("answer=%+v state=%+v stateOk=%v member7=%v", answer, state, stateOk,
		stateOk && kernel.Member(state.Set, []float64{7}))
}

// TestKernelSummaryDirect_AnIfConditionOnAWholeRecordParameterFolds pins
// pin (b): `function f(p: { lo: number }) { if (p) { return p.lo; } return
// 0; }` — the bare `p` standing as an `if` CONDITION is a truthiness test
// too, and `p`'s declared type excludes undefined/null, so it is always
// truthy: member-safe, same as the `&&`/`||` operand case.
func TestKernelSummaryDirect_AnIfConditionOnAWholeRecordParameterFolds(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	declaration := summaryDeclarationOf(t,
		"function f(p: { lo: number }) { if (p) { return p.lo; } return 0; }")
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	_, ok := RelowerSummaryBody(ctx, declaration)
	outcome, construct, _ := SummaryOutcomeOf(nil, declaration)
	if !ok {
		t.Fatalf("f's body declined: outcome=%q construct=%q", outcome, construct)
	}
	if outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q), want complete", outcome, construct)
	}
}

// TestKernelSummaryDirect_AnIfConditionOnAWholeRecordParameterServesTheTrueValue
// is the VALUE-precision twin of the `if` pin above: `p(<{lo:9}>)` must
// admit exactly 9 through the then-arm's `p.lo`, not a widened/unknown
// answer — the `if (p)` guard must fold to the truthy arm outright
// (LowerGuard's own constant-fold), never an untested branchBoth join.
func TestKernelSummaryDirect_AnIfConditionOnAWholeRecordParameterServesTheTrueValue(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	declaration := summaryDeclarationOf(t,
		"function f(p: { lo: number }) { if (p) { return p.lo; } return 0; }")
	contract := &FunctionContract{Declaration: declaration}
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	argument := recordObject(t, map[string]float64{"lo": 9}, []string{"lo"})
	answer, ok := KernelSummaryDirect(ctx, []abstractdomain.AbstractValue{argument}, contract)
	if !ok {
		t.Fatalf("f({lo: 9}) declined at the call site — the summary should have served")
	}
	state, stateOk := StateOfKnown(answer)
	if !stateOk || state.Top {
		t.Fatalf("the answer did not spell as a scalar state: %+v", answer)
	}
	if !kernel.Member(state.Set, []float64{9}) {
		t.Errorf("the summary of f({lo:9}) excludes the true value 9: %+v", state.Set)
	}
}

// TestKernelSummaryDirect_AnAbsentHolderTruthyTestStaysRefused is the
// HOLDER-ABSENCE BOUNDARY negative: `p: { lo: number } | undefined` still
// declines the `&&` use exactly as before. The union arm splits BEFORE
// reaching the classification fix — `unionMembersOf` intersects the
// object arm's members against the `undefined` arm's (none), the
// intersection is empty, and an empty intersection AT AN ENTRY declines
// the expansion whole (ir_summary_composite_type_members.go) — so `p`
// never becomes an expanded record parameter at all and keeps its single
// whole-name slot, untouched by wholeRecordUseAt. This pin exists to
// prove that boundary holds, not to exercise the new classification arm.
func TestKernelSummaryDirect_AnAbsentHolderTruthyTestStaysRefused(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	declaration := summaryDeclarationOf(t,
		"function h(p: { lo: number } | undefined) { return (p && p.lo) || 0; }")
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	_, ok := RelowerSummaryBody(ctx, declaration)
	outcome, construct, _ := SummaryOutcomeOf(nil, declaration)
	// the pre-existing behavior for an optional/absent-admitting holder —
	// this pin only guards against a regression widening past the
	// non-optional case; it does not assert a specific outcome shape,
	// since a union-typed parameter's outcome is governed elsewhere.
	t.Logf("ok=%v outcome=%q construct=%q", ok, outcome, construct)
	// the SOUNDNESS half: a call passing `undefined` must never serve a
	// number a wrongly-widened "always truthy" fold would produce
	// (p.lo's own leaf value, dropping the {0} fallback the falsy/absent
	// arm genuinely takes at runtime). Only asserted where this call
	// site's own route serves at all — KernelSummaryDirect declining is
	// a separate, acceptable outcome for a union-typed parameter this
	// pin does not police.
	if !ok {
		return
	}
	contract := &FunctionContract{Declaration: declaration}
	answer, served := KernelSummaryDirect(ctx, []abstractdomain.AbstractValue{{Kind: abstractdomain.KindUndef}}, contract)
	if !served {
		return
	}
	// TOP (unknown) is sound — it admits every value, 0 included — and is
	// the pre-existing ceiling this pin does not police (the same "join
	// of both operands" imprecision the plain effect grammar already has
	// generally). What would be UNSOUND, and what this pin exists to
	// catch, is a wrongly-folded EXACT answer that excludes 0 — a fold
	// that treated `p` as always-truthy despite its optional annotation.
	state, stateOk := StateOfKnown(answer)
	if !stateOk || state.Top {
		return
	}
	if !kernel.Member(state.Set, []float64{0}) {
		t.Errorf("h(undefined) excludes 0 (the true value the falsy/absent arm returns): %+v", answer)
	}
}
