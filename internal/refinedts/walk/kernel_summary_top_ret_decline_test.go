// Pin for the serve-only-when-it-determines rule (kernel_summaries.go's
// applySummary): a COMPLETE body whose ret comes back TOP/unknown must
// DECLINE rather than serve silence.Residue() with ok=true — a served
// empty answer preempts the walk-based recovery
// (InlineContractCall/InlineContractBody) that can determine a real
// value, which is exactly the failure
// TestOverloadGroup_ACallWithinTheImplementationsArityDeterminesAValue
// (call_shape_contracts_test.go) worked around by unseating the kernel
// entirely rather than trusting the summary route's own decline.
//
// This file pins the same pickYears(40) shape with the kernel LEFT
// SEATED — the condition the workaround above avoided — because the
// fix changes what applySummary itself answers, not just which route a
// test chooses to exercise.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
)

// TestApplySummary_PickYearsServesTheExactWindow pins the seated
// route's measured answer (2026-08-22): pickYears' compiled summary
// lowers `age + (extra ?? 0)` as a branch on extra's definedness
// (lowering_to_kernel_ir_return_branch.go), and the kernel's join
// answers the exact [40,40] integer window — a DETERMINED serve, not
// TOP. The serve-only-when-it-determines rule in applySummary (an
// unknown ret returns ok=false and falls through to the walk-based
// recovery) stands behind this: if the summary route ever regresses to
// a TOP ret for this shape, the decline keeps the walk's exact answer
// reachable and the service-level divergence test still holds silence.
func TestApplySummary_PickYearsServesTheExactWindow(t *testing.T) {
	kernel := yieldContractKernel(t)
	source := "function pickYears(age: number): number;\n" +
		"function pickYears(age: number, extra: number): number;\n" +
		"function pickYears(age: number, extra?: number): number {\n" +
		"  return age + (extra ?? 0);\n" +
		"}\n"
	contract, ctx, _ := yieldContractOf(t, source, "pickYears")
	ctx.Kernel = kernel
	// deliberately left SEATED — the condition
	// TestOverloadGroup_ACallWithinTheImplementationsArityDeterminesAValue
	// avoids by calling SetEngineKernel(nil); this test is ABOUT the
	// seated route's own answer.

	age40 := abstractdomain.KnownValues([]float64{40}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
	summarized, ok := KernelSummaryDirectOn(ctx, []abstractdomain.AbstractValue{age40}, contract, unknownReceiver())
	if !ok {
		t.Fatalf("KernelSummaryDirectOn declined pickYears(40) — want the determined serve (the [40,40] integer window) measured when this pin was written")
	}
	if summarized.Kind == abstractdomain.KindUnknown {
		t.Fatalf("KernelSummaryDirectOn served an unknown answer for pickYears(40) — the serve-only-when-it-determines rule must decline this instead of serving it")
	}
}

// TestFullFixture_PickYearsServiceShape_GoodLegDeterminesSilently pins
// the same shape through the full service Check() route — the door the
// judge takes, and the shape
// TestKernelDivergence_OverloadedCall_ImplementationAnswersOneArg
// (service/syntax_wave_kernel_divergence_test.go) already reproduces at
// that layer. Kept here too, beside the applySummary-level pin above,
// so a regression that only shows up once every sibling contract is
// registered (walkContractBodies scheduling, the service test's own
// header) has a walk-package twin naming the mechanism directly rather
// than only the symptom.
//
// NOT RUN by this agent — EDIT-ONLY lane, no test execution; the
// orchestrator runs this alongside
// TestKernelDivergence_OverloadedCall_ImplementationAnswersOneArg.
func TestFullFixture_PickYearsServiceShape_GoodLegDeterminesSilently(t *testing.T) {
	kernel := yieldContractKernel(t)
	source := "function pickYears(age: number): number;\n" +
		"function pickYears(age: number, extra: number): number;\n" +
		"function pickYears(age: number, extra?: number): number {\n" +
		"  return age + (extra ?? 0);\n" +
		"}\n" +
		"function caller(): number {\n" +
		"  return pickYears(40);\n" +
		"}\n"
	contract, ctx, diagnostics := yieldContractOf(t, source, "caller")
	ctx.Kernel = kernel
	// SEATED, unlike TestOverloadGroup_ACallWithinTheImplementationsArityDeterminesAValue's
	// own SetEngineKernel(nil) — this test's whole point is that the
	// summary route's own decline, not the absence of the kernel, is
	// what lets the walk-based recovery answer exactly.

	var sink []abstractdomain.AbstractValue
	ctx.ReturnSink = &sink
	AnalyzeFunction(ctx, contract, nil)
	if len(sink) == 0 {
		t.Fatalf("caller's body recorded no return value")
	}
	returned := JoinSinkSummarized(sink)
	// The measured determination (2026-08-22) arrives as the exact
	// [40,40] integer window in set form — a singleton, just not
	// spelled KindValues. The pin holds determination and silence, not
	// the representation.
	if returned.Kind == abstractdomain.KindUnknown {
		t.Fatalf("pickYears(40) stayed unknown with the kernel seated — a served TOP ret preempted the walk-based recovery")
	}
	if len(*diagnostics) != 0 {
		t.Errorf("caller's good leg reported %d diagnostics with the kernel seated, want 0: %+v", len(*diagnostics), *diagnostics)
	}
}
