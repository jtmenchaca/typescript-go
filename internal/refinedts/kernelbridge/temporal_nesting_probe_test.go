package kernelbridge

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// TestTemporalGrammarWiresRoundTripThroughTheLiveKernel is the pin
// that used to be a diagnostic wireNestingCount reading -- these
// grammars (the ISO Temporal string family, temporal_string_grammars.go)
// measured hundreds of sequence-family form tags each (PlainDateTimeString
// alone: 451), well past the wire-nesting admission cap that used to
// decline every one of them before mkUnion's own canonicalization
// (refined_sets/automata.lean) made the derivative walk terminate on
// shapes like this without a cap at all
// (kernelbridge/wire_nesting_guard.go, removed). What matters now is
// not a count but the real behavior the cap used to block: does the
// wire actually CROSS to the kernel, DECODE there
// (boundary/decode_sets.lean's decodeSet, the one seam every set-
// producing wire path converges on), and answer a genuine membership
// question correctly. One concrete, valid ISO literal per grammar,
// asked through kernel.Member -- a `true` answer is only reachable if
// the wire encoded, crossed, decoded, and the kernel's derivative
// walk over the grammar's full (formerly-capped) depth actually
// terminated with the right verdict.
func TestTemporalGrammarWiresRoundTripThroughTheLiveKernel(t *testing.T) {
	if !KernelArtifactsPresent(DylibPath) {
		t.Skip("native kernel dylib absent — build it first")
	}
	kernel, err := LoadKernel(DylibPath)
	if err != nil {
		t.Fatalf("LoadKernel: %v", err)
	}

	cases := []struct {
		name    string
		set     refinementsets.RefinedSet
		literal string
	}{
		{"PlainDateTimeString", refinementsets.PlainDateTimeString, "2024-02-29T13:45:30"},
		{"ZonedDateTimeString", refinementsets.ZonedDateTimeString, "2024-02-29T13:45:30+01:00[Europe/Paris]"},
		{"InstantString", refinementsets.InstantString, "2024-02-29T13:45:30Z"},
		{"PlainTimeString", refinementsets.PlainTimeString, "13:45:30"},
		{"PlainYearMonthString", refinementsets.PlainYearMonthString, "2024-02"},
		{"PlainMonthDayString", refinementsets.PlainMonthDayString, "02-29"},
		{"DurationString", refinementsets.DurationString, "P1Y2M3DT4H5M6S"},
		{"DateSet", refinementsets.DateSet, "2024-02-29"},
		{"TimeSet", refinementsets.TimeSet, "13:45:30"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tuple := refinementsets.CodepointsOf(c.literal)
			if !kernel.Member(c.set, tuple) {
				t.Errorf("kernel.Member(%s, %q) = false, want true — the wire either failed to cross/decode or the kernel answered wrongly", c.name, c.literal)
			}
		})
	}
}
