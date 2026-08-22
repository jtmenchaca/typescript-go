// ISSUES.md's named trace: "TS: subscript on a map product reads
// may-be-undefined despite length floor 1 — `boosted[0]`, in-bounds
// discharge, the product's window floor — needs its own trace."
//
// The trace: a declared length floor (z.array(...).min(1)) reads back
// as a Repetition{Lo:1} entry value (BindEntryEnv/AbstractValueOfDeclared).
// `.map` DOES carry that same Lo forward — MapOutcome's own window
// derivation (callback_element_outcome.go) reads the receiver's
// Repetition and rebuilds the result as Repetition(outSet, window.Lo,
// window.Hi) whenever the receiver is a KindSet. So `boosted`'s window
// floor is not lost at the .map() step; the wiring between the
// parameter's declared floor and the map product's own window was
// already intact.
//
// The gap was downstream, at the READ: InBoundsElementOf's own
// proved-in-bounds arm (element_in_bounds.go) wrapped EVERY
// array-shaped Repetition read in PossiblyAbsent, in bounds or not —
// because a Repetition states membership-if-present, never density,
// and nothing in AbstractValue distinguished "arrived from a
// parameter, density unproven" from "built by .map(), which
// sec-array.prototype.map (CreateDataPropertyOrThrow at every index
// where kPresent holds, sec-array.prototype.map) proves dense wherever
// the receiver itself was present." KindSet's SeqDense/SeqDenseKnown
// pair (abstractdomain/abstract_value.go, mirroring KindArrayHoles's
// own Dense/DenseKnown) is that channel: MapOutcome marks its
// window-carrying result dense (KnownSetDense), and
// InBoundsElementOf's proved-in-bounds arm now reads the flag and
// skips the PossiblyAbsent wrap wherever it holds.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
)

// TestMapProductLengthFloor_IndexZeroReadIsDetermined pins the exact
// site ISSUES.md names: samples' declared length floor 1
// (z.array(...).min(1)) survives `.map`, and `boosted[0]` — in bounds
// under that very floor, over a receiver `.map` proved dense
// (MapOutcome's own KnownSetDense mark) — determines the bare element
// set outright, no maybe wrapper.
func TestMapProductLengthFloor_IndexZeroReadIsDetermined(t *testing.T) {
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
	if returned.Kind != abstractdomain.KindSet {
		t.Fatalf("boosted[0] determined %+v, want KindSet — "+
			"the length-floor-1 window survives .map (MapOutcome's own window "+
			"read) and .map proves the result dense (KnownSetDense), so the "+
			"proved-in-bounds arm answers the bare element with no maybe "+
			"wrapper", returned)
	}
}
