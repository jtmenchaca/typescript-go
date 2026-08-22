// ISSUES.md's named trace: "TS: subscript on a map product reads
// may-be-undefined despite length floor 1 — `boosted[0]`, in-bounds
// discharge, the product's window floor — needs its own trace."
//
// The trace: a declared length floor (z.array(...).min(1)) reads back
// as a Repetition{Lo:1} entry value (BindEntryEnv/AbstractValueOfDeclared).
// `.map` DOES carry that same Lo forward — MapOutcome's own window
// derivation (callback_element_outcome.go:100-114) reads the receiver's
// Repetition and rebuilds the result as Repetition(outSet, window.Lo,
// window.Hi) whenever the receiver is a KindSet. So `boosted`'s window
// floor is not lost at the .map() step; the wiring between the
// parameter's declared floor and the map product's own window is intact.
//
// The loss is downstream, at the READ: InBoundsElementOf's own
// proved-in-bounds arm (element_in_bounds.go:266-286, already pinned at
// TestInBoundsElementOf_ProvedInBoundsHoleReadIsUndefOnly,
// element_access_absent_flavor_test.go) wraps EVERY array-shaped
// Repetition read in PossiblyAbsent, in bounds or not — because a
// Repetition states membership-if-present, never density, and nothing
// in AbstractValue distinguishes "arrived from a parameter, density
// unproven" from "built by .map(), which sec-array.prototype.map
// (CreateDataPropertyOrThrow at every index 0..len-1) proves dense."
// KindList is the only shape this package carries for a proven-dense
// sequence, and it requires exact per-item values .map can't produce
// from a set-shaped source element — there is no "repetition, but
// proven dense" shape today. Building one is a new representational
// channel (mirroring KindObject's Complete flag or KindArrayHoles's
// Dense/DenseKnown pair) and is out of this unit's scope; this file
// pins today's DETERMINED (not silent) answer at the named site so the
// next unit that adds the density channel has a red test to turn green.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
)

// TestMapProductLengthFloor_IndexZeroReadStillWearsAbsence pins the
// exact site ISSUES.md names: samples' declared length floor 1
// (z.array(...).min(1)) survives `.map`, and `boosted[0]` — in bounds
// under that very floor — still determines KindPossiblyUndefined /
// AbsentFlavorUndefOnly rather than the bare element. Not a wiring
// gap: the floor DID reach the read (a floor of 0 would still leave
// the read exactly as porous, since underFloor would be false and no
// OTHER arm would prove it in bounds either) — the absence wrapper is
// InBoundsElementOf's own deliberate density stance, documented in
// this file's own header.
func TestMapProductLengthFloor_IndexZeroReadStillWearsAbsence(t *testing.T) {
	kernel := parseVocabKernel(t)
	source := `import * as z from "/surface/z.ts";
const zSamples = z.array(z.number()).min(1);
function f(samples: z.infer<typeof zSamples>): number | undefined {
  const boosted = samples.map(s => s);
  return boosted[0];
}
`
	_, ctx, contract := factExportContractOf(t, kernel, source, "f")

	var sink []abstractdomain.AbstractValue
	ctx.ReturnSink = &sink
	AnalyzeFunction(ctx, contract, nil)
	if len(sink) == 0 {
		t.Fatalf("f's body recorded no return value")
	}
	returned := JoinSinkSummarized(sink)
	if returned.Kind != abstractdomain.KindPossiblyUndefined {
		t.Fatalf("boosted[0] determined %+v, want KindPossiblyUndefined — "+
			"the length-floor-1 window survives .map (MapOutcome's own window "+
			"read), so this is the proved-in-bounds arm wearing its own "+
			"deliberate absence wrapper, not an unproved read", returned)
	}
	if returned.AbsentSide != abstractdomain.AbsentFlavorUndefOnly {
		t.Errorf("boosted[0].AbsentSide = %v, want AbsentFlavorUndefOnly", returned.AbsentSide)
	}
	if returned.Inner == nil {
		t.Fatalf("boosted[0].Inner = nil, want the element set the floor proved in bounds")
	}
	if returned.Inner.Kind != abstractdomain.KindSet {
		t.Errorf("boosted[0].Inner.Kind = %v, want KindSet — the mapped element's own set", returned.Inner.Kind)
	}
}
