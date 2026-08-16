// The open upper bound of Math.random() carried through multiplication
// and Math.floor. sec-math.random (vendored spec): the result is
// "greater than or equal to +0𝔽 but strictly less than 1𝔽" — so over
// doubles the set is [0, nextafter(1, -Inf)], random * 121 stays
// strictly under 121, and Math.floor of it reaches at most 120. The
// kernel's binary transfers read closed corners only, so binaryImage
// tightens each strict ray to its float neighbor before posing
// (tightenStrictBounds). The 122 control pins that the tightening never
// weakens a refutation: floor(random * 122) still reaches 121, outside
// the Age set [0, 120]. Skipped (never a faked pass) when the native
// kernel dylib is absent, the same gate trig_reduction_test.go uses.

package walk

import (
	"math"
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

func loadRandomOpenBoundKernel(t *testing.T) {
	t.Helper()
	if !kernelbridge.KernelArtifactsPresent(kernelbridge.DylibPath) {
		t.Skip("native kernel dylib absent — build it first")
	}
	kernel, err := kernelbridge.LoadKernel(kernelbridge.DylibPath)
	if err != nil {
		t.Fatalf("LoadKernel: %v", err)
	}
	SetTransferKernel(kernel)
}

// randomValue is the set Math.random() pins: [0, 1) per
// sec-math.random, the same construction math_transfer.go serves.
func randomValue() abstractdomain.AbstractValue {
	return abstractdomain.KnownSet(
		refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.Below(1)),
		nil,
		abstractdomain.TrustSpec,
		abstractdomain.SetKindTagNone,
	)
}

func exactFactor(v float64) abstractdomain.AbstractValue {
	return abstractdomain.KnownValues([]float64{v}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
}

func TestTightenStrictBounds_StrictRaysCloseAtTheFloatNeighbor(t *testing.T) {
	tightened := tightenStrictBounds(refinementsets.MakeRefinedSet(
		refinementsets.AtLeast(0),
		refinementsets.Below(1),
	))
	sawAtMost := false
	for _, f := range tightened.Forms {
		switch f.Form {
		case refinementsets.FormBelow, refinementsets.FormAbove:
			t.Errorf("a strict ray survived the tightening: %+v", f)
		case refinementsets.FormAtMost:
			sawAtMost = true
			want := math.Nextafter(1, math.Inf(-1))
			if f.A != want {
				t.Errorf("below 1 tightened to <= %v, want <= %v (the float neighbor)", f.A, want)
			}
		}
	}
	if !sawAtMost {
		t.Errorf("below 1 produced no closed upper ray: %+v", tightened.Forms)
	}

	// `above 0` steps to the least positive double
	above := tightenStrictBounds(refinementsets.MakeRefinedSet(refinementsets.Above(0)))
	if len(above.Forms) != 1 || above.Forms[0].Form != refinementsets.FormAtLeast {
		t.Fatalf("above 0 tightened to %+v, want one atLeast form", above.Forms)
	}
	if want := math.Nextafter(0, math.Inf(1)); above.Forms[0].A != want {
		t.Errorf("above 0 tightened to >= %v, want >= %v", above.Forms[0].A, want)
	}

	// closed rays and infinite strict rays pass through unchanged
	closed := refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.AtMost(120), refinementsets.Integer)
	if got := tightenStrictBounds(closed); len(got.Forms) != len(closed.Forms) {
		t.Errorf("a closed set changed under tightening: %+v", got.Forms)
	}
}

func TestRandomTimes121_FloorReachesAtMost120(t *testing.T) {
	loadRandomOpenBoundKernel(t)
	product := TransferBinary(OpMul, randomValue(), exactFactor(121))
	window := RangeOfKnown(product)
	if window == nil {
		t.Fatalf("random * 121 posed no window: %+v", product)
	}
	// the true reachable maximum fl(nextafter(1,-Inf) * 121) sits
	// inside the window — the host spot check every enclosure gets
	trueMax := math.Nextafter(1, math.Inf(-1)) * 121
	if !(window.Lo <= 0 && trueMax <= window.Hi) {
		t.Errorf("the product window [%v, %v] excludes a reachable value (0 or %v)", window.Lo, window.Hi, trueMax)
	}
	if !(window.Hi < 121) {
		t.Errorf("random * 121 reads as reaching %v, want strictly under 121 — the open bound was dropped", window.Hi)
	}

	floored, ok := TransferMathCall("floor", []abstractdomain.AbstractValue{product})
	if !ok {
		t.Fatalf("Math.floor was not transferred")
	}
	flooredWindow := RangeOfKnown(floored)
	if flooredWindow == nil {
		t.Fatalf("floor(random * 121) posed no window: %+v", floored)
	}
	if flooredWindow.Lo != 0 || flooredWindow.Hi != 120 || !flooredWindow.Int {
		t.Errorf("floor(random * 121) = [%v, %v] int=%v, want [0, 120] int=true",
			flooredWindow.Lo, flooredWindow.Hi, flooredWindow.Int)
	}
}

func TestRandomTimes122_FloorStillReaches121(t *testing.T) {
	// the soundness control: floor(random * 122) reaches 121 at runtime
	// (fl(nextafter(1,-Inf) * 122) floors to 121), so the window must
	// keep 121 — the Age refutation of Math.floor(Math.random() * 122)
	// survives the tightening
	loadRandomOpenBoundKernel(t)
	product := TransferBinary(OpMul, randomValue(), exactFactor(122))
	floored, ok := TransferMathCall("floor", []abstractdomain.AbstractValue{product})
	if !ok {
		t.Fatalf("Math.floor was not transferred")
	}
	window := RangeOfKnown(floored)
	if window == nil {
		t.Fatalf("floor(random * 122) posed no window: %+v", floored)
	}
	if window.Hi != 121 {
		t.Errorf("floor(random * 122) caps at %v, want exactly 121 — the true maximum, kept", window.Hi)
	}
	if window.Lo != 0 || !window.Int {
		t.Errorf("floor(random * 122) = [%v, %v] int=%v, want lo 0 and integer", window.Lo, window.Hi, window.Int)
	}
}
