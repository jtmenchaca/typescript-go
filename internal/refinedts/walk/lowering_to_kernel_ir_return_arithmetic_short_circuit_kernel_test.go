// The kernel-gated end-to-end pin for returnArithmeticOverShortCircuit
// (lowering_to_kernel_ir_return_branch.go): `function pickYears(age:
// number, extra?: number): number { return age + (extra ?? 0); }`
// called as `pickYears(40)` — extra unpassed, so its entry state is
// PROVABLY ABSENT — determines #ret to EXACTLY {40}.
//
// STALE-DYLIB CHOICE, stated plainly: this test does NOT skip on a
// present-but-stale dylib, and it is written to FAIL against one. There
// is no wire-level signal this Go side can read to distinguish "the
// loaded kernel has the bottom-enclosure contract" from "it does not" —
// KernelArtifactsPresent only checks the dylib FILE exists, and the
// wire protocol between Go and the kernel carries no version/capability
// flag for this. The only honest options were (a) skip whenever a
// dylib is present at all, which would silently pass forever against a
// stale build and never catch the exact regression this restoration
// depends on, or (b) run for real and let a stale kernel's answer
// (unknown/top, from the old top-derived-empty-arm behavior) fail the
// assertion below. (b) is what this file does: KernelArtifactsPresent
// is still the ordinary skip (no dylib at all -> skip, never a faked
// pass), but a PRESENT, STALE dylib fails here loudly rather than
// passing quietly. Rebuild with `pnpm kernel` then `pnpm kernel:native`
// before trusting a green run of this test.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/narrowing"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

func TestReturnArithmeticOverShortCircuit_PickYearsEndToEnd(t *testing.T) {
	if !kernelbridge.KernelArtifactsPresent(kernelbridge.DylibPath) {
		t.Skip("native kernel dylib absent — build it first")
	}
	kernel, err := kernelbridge.LoadKernel(kernelbridge.DylibPath)
	if err != nil {
		t.Fatalf("LoadKernel: %v", err)
	}
	SetEngineKernel(kernel)
	SetTransferKernel(kernel)
	narrowing.SetNarrowKernel(kernel)

	// "age", "extra", "#done", "#ret" — the same four-slot layout
	// loweringResultContext builds, named directly here since this test
	// also needs to hand the kernel an ENTRY state per slot, which
	// loweringResultContext's callers never do.
	context := &LoweringContext{
		Bindings: []string{"age", "extra", "#done", "#ret"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber, BindingKindUnknown},
		Result:   &LoweringResult{Done: 2, Ret: 3},
	}
	stmts, ok := LowerStatements(context, loweringParse(t, "return age + (extra ?? 0);"))
	if !ok {
		t.Fatalf("LowerStatements ok = false, want true")
	}

	// pickYears(40): age is exactly 40, extra is UNPASSED — the
	// provably-absent state, the same {Set: emptySet, Absent: true}
	// shape kernel_delegation.go's own StateOfKnown answers for
	// abstractdomain.KindUndef. #done and #ret start unwritten (top).
	entry := []kernelbridge.KnownStateWire{
		{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{40}))},
		{Set: emptySet, Absent: true},
		{Top: true},
		{Top: true},
	}
	exit := kernel.Walk(entry, stmts)
	ret := exit[context.Result.Ret]
	if ret.Top {
		t.Fatalf("pickYears(40): #ret exit = top, want an exact {40} — this is the stale-kernel failure mode: the ABSENT arm's `age + extra` composition (provably unreachable here) widened the join instead of contributing nothing. Rebuild the kernel (pnpm kernel, pnpm kernel:native) before trusting this test.")
	}
	if !kernel.Member(ret.Set, []float64{40}) {
		t.Errorf("pickYears(40): member(#ret, [40]) = false, want true")
	}
	if kernel.Member(ret.Set, []float64{41}) {
		t.Errorf("pickYears(40): member(#ret, [41]) = true, want false — the exit set must be exactly {40}, not a wider claim")
	}
}

// TestReturnArithmeticOverShortCircuit_PadYearsOrEndToEnd is the `||`
// sibling of the pin above, for the syntax-coverage fixture's padYears
// row (e-class-and-function.ts's orShortCircuitCall): `function
// padYears(age: number, extra?: number): number { return age + (extra
// || 0); }` called as `padYears(40)` — extra unpassed, so its entry
// state is PROVABLY ABSENT (which is also FALSY under IrTestTruthyNum,
// the same narrowDefined/narrowing-adjacent split this branch's own
// truthiness test reads off the slot's state) — determines #ret to
// EXACTLY {40}.
//
// Same stale-dylib choice as the `??` pin above: this test does not
// skip on a present-but-stale dylib and is written to fail against one.
func TestReturnArithmeticOverShortCircuit_PadYearsOrEndToEnd(t *testing.T) {
	if !kernelbridge.KernelArtifactsPresent(kernelbridge.DylibPath) {
		t.Skip("native kernel dylib absent — build it first")
	}
	kernel, err := kernelbridge.LoadKernel(kernelbridge.DylibPath)
	if err != nil {
		t.Fatalf("LoadKernel: %v", err)
	}
	SetEngineKernel(kernel)
	SetTransferKernel(kernel)
	narrowing.SetNarrowKernel(kernel)

	context := &LoweringContext{
		Bindings: []string{"age", "extra", "#done", "#ret"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber, BindingKindUnknown},
		Result:   &LoweringResult{Done: 2, Ret: 3},
	}
	stmts, ok := LowerStatements(context, loweringParse(t, "return age + (extra || 0);"))
	if !ok {
		t.Fatalf("LowerStatements ok = false, want true")
	}

	// padYears(40): age is exactly 40, extra is UNPASSED — provably
	// absent, which is also provably falsy (an absent value can never
	// satisfy IrTestTruthyNum). #done and #ret start unwritten (top).
	entry := []kernelbridge.KnownStateWire{
		{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{40}))},
		{Set: emptySet, Absent: true},
		{Top: true},
		{Top: true},
	}
	exit := kernel.Walk(entry, stmts)
	ret := exit[context.Result.Ret]
	if ret.Top {
		t.Fatalf("padYears(40): #ret exit = top, want an exact {40} — the stale-kernel failure mode (see the `??` pin above): rebuild the kernel before trusting this test.")
	}
	if !kernel.Member(ret.Set, []float64{40}) {
		t.Errorf("padYears(40): member(#ret, [40]) = false, want true")
	}
	if kernel.Member(ret.Set, []float64{240}) {
		t.Errorf("padYears(40): member(#ret, [240]) = true, want false — the exit set must be exactly {40}, not a wider claim")
	}
}

// TestReturnArithmeticOverShortCircuit_GateYearsAndEndToEnd is the `&&`
// sibling, for the syntax-coverage fixture's gateYears row
// (e-class-and-function.ts's andShortCircuitCall): `function
// gateYears(age: number, extra: number): number { return age + (extra
// && 999); }` called as `gateYears(40, 0)` — extra is the EXACT value
// 0, which is PROVABLY FALSY under IrTestTruthyNum — determines #ret to
// EXACTLY {40} (the else/falsy arm, `age + extra` with extra pinned to
// 0, per returnArithmeticOverShortCircuit's own then/els swap for
// KindAmpersandAmpersandToken).
//
// Same stale-dylib choice as the two pins above.
func TestReturnArithmeticOverShortCircuit_GateYearsAndEndToEnd(t *testing.T) {
	if !kernelbridge.KernelArtifactsPresent(kernelbridge.DylibPath) {
		t.Skip("native kernel dylib absent — build it first")
	}
	kernel, err := kernelbridge.LoadKernel(kernelbridge.DylibPath)
	if err != nil {
		t.Fatalf("LoadKernel: %v", err)
	}
	SetEngineKernel(kernel)
	SetTransferKernel(kernel)
	narrowing.SetNarrowKernel(kernel)

	context := &LoweringContext{
		Bindings: []string{"age", "extra", "#done", "#ret"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber, BindingKindUnknown},
		Result:   &LoweringResult{Done: 2, Ret: 3},
	}
	stmts, ok := LowerStatements(context, loweringParse(t, "return age + (extra && 999);"))
	if !ok {
		t.Fatalf("LowerStatements ok = false, want true")
	}

	// gateYears(40, 0): age is exactly 40, extra is the EXACT value 0 —
	// provably falsy, the else arm (age + extra, extra pinned to 0).
	// #done and #ret start unwritten (top).
	entry := []kernelbridge.KnownStateWire{
		{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{40}))},
		{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{0}))},
		{Top: true},
		{Top: true},
	}
	exit := kernel.Walk(entry, stmts)
	ret := exit[context.Result.Ret]
	if ret.Top {
		t.Fatalf("gateYears(40, 0): #ret exit = top, want an exact {40} — the stale-kernel failure mode (see the `??` pin above): rebuild the kernel before trusting this test.")
	}
	if !kernel.Member(ret.Set, []float64{40}) {
		t.Errorf("gateYears(40, 0): member(#ret, [40]) = false, want true")
	}
	if kernel.Member(ret.Set, []float64{1039}) {
		t.Errorf("gateYears(40, 0): member(#ret, [1039]) = true, want false — the exit set must be exactly {40}, not the truthy arm's 1039")
	}
}
