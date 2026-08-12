// from evaluation/trig_reduction.test.ts
//
// Range reduction over the certified π window: sin and cos answer
// beyond |x| ≤ 4, and every window is spot-checked against the
// host's own value — the standing rule for every enclosure. Skipped
// (never a faked pass) when the native kernel dylib is absent, the
// same gate kernelbridge's own round-trip tests use.

package walk

import (
	"math"
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/narrowing"
)

func loadTrigTestKernel(t *testing.T) {
	t.Helper()
	if !kernelbridge.KernelArtifactsPresent(kernelbridge.DylibPath) {
		t.Skip("native kernel dylib absent — build it first")
	}
	kernel, err := kernelbridge.LoadKernel(kernelbridge.DylibPath)
	if err != nil {
		t.Fatalf("LoadKernel: %v", err)
	}
	SetTransferKernel(kernel)
	narrowing.SetNarrowKernel(kernel)
}

func TestSinAndCosReduceBeyondTheSmallDomain(t *testing.T) {
	loadTrigTestKernel(t)
	inside := func(name string, at float64) {
		t.Helper()
		w, ok := TransferMathCall(name, []abstractdomain.AbstractValue{
			abstractdomain.KnownValues([]float64{at}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved),
		})
		if !ok {
			t.Fatalf("%s(%v): TransferMathCall answered not-transferred", name, at)
		}
		bounds, boundsOk := narrowing.BoundsOfKnown(w)
		if !boundsOk {
			t.Fatalf("%s(%v): BoundsOfKnown answered no window", name, at)
		}
		var truth float64
		if name == "sin" {
			truth = math.Sin(at)
		} else {
			truth = math.Cos(at)
		}
		if !(bounds.Lo <= truth) {
			t.Errorf("%s(%v): bounds.Lo = %v, want <= %v", name, at, bounds.Lo, truth)
		}
		if !(truth <= bounds.Hi) {
			t.Errorf("%s(%v): bounds.Hi = %v, want >= %v", name, at, bounds.Hi, truth)
		}
	}
	inside("sin", 10)
	inside("sin", -7.3)
	inside("sin", 100)
	inside("cos", 10)
	inside("cos", -55.25)
	inside("cos", 1000)
}
