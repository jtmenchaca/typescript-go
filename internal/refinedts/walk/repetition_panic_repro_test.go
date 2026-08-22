package walk

import (
	"reflect"
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// TestRepetitionPanicRepro_LengthNarrowingIntoTernarySlice reproduces the
// crash-class defect: a sequence already bounded above (`.max(3)`)
// guarded by `.length > 5` — a contradictory length narrowing — feeds
// a ternary return and panics inside refinementsets.Repetition ("a
// repetition upper bound is a natural number >= the lower") instead of
// determining or declining.
func TestRepetitionPanicRepro_LengthNarrowingIntoTernarySlice(t *testing.T) {
	kernel := parseVocabKernel(t)
	source := `import * as z from "/surface/z.ts";
const zBounded = z.array(z.number()).max(3);
function f(xs: z.infer<typeof zBounded>): number {
  return xs.length > 5 ? xs.length : 0;
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
	// xs is bounded above by 3 (z.array(z.number()).max(3)), so
	// `xs.length > 5` guards a length window [6, 3] on its true side —
	// no value satisfies both bounds at once. TightenRepetition now
	// declines that row (empty intersection) instead of panicking; the
	// walk still runs BOTH arms unnarrowed (declining a narrowing row
	// is not the same claim as proving the branch unreachable), so the
	// determined set is the sound join: xs.length's own [0,3] window
	// on the true arm, joined with the literal 0 on the false arm.
	if returned.Kind != abstractdomain.KindSet {
		t.Fatalf("f(xs) determined %+v, want a KindSet — the sound join of both ternary arms", returned)
	}
	want := refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.AtMost(3), refinementsets.Integer)
	if !reflect.DeepEqual(returned.Set, want) {
		t.Fatalf("f(xs) determined set %+v, want %+v — [0,3] joined with {0} collapses to [0,3]", returned.Set, want)
	}
}
