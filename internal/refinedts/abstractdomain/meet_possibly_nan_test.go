package abstractdomain

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// TestA6_sink_dead_AGuardsSetDropsTheNaNHalf pins what a guard on a
// possibly-NaN read proves. `d.getTime()` reads as "an integer time
// value, or NaN"; a guard's own set is met against it, and no refined
// set holds NaN (refinement_forms.go), so the conjunction keeps the
// real part only. Without this arm the meet fell through to the
// possibly-NaN side whole and every guarded Date read still carried NaN
// into its sink.
func TestA6_sink_dead_AGuardsSetDropsTheNaNHalf(t *testing.T) {
	timeValue := PossiblyNaN(KnownSet(
		refinementsets.MakeRefinedSet(
			refinementsets.Integer,
			refinementsets.AtLeast(-8.64e15),
			refinementsets.AtMost(8.64e15),
		),
		nil, TrustSpec, SetKindTagNone,
	))
	guardProved := KnownSet(
		refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.AtMost(150)),
		nil, TrustProved, SetKindTagNone,
	)

	met := MeetKnown(timeValue, guardProved)
	if met.Kind == KindPossiblyNaN {
		t.Errorf("the meet kept the NaN half: %+v", met)
	}
	// and the same both ways round — a meet states one conjunction
	if mirrored := MeetKnown(guardProved, timeValue); mirrored.Kind == KindPossiblyNaN {
		t.Errorf("the mirrored meet kept the NaN half: %+v", mirrored)
	}
}

// TestA6_sink_dead_APinnedValueAlsoDropsTheNaNHalf pins the same rule
// for an EXACT guard: `d.getUTCHours() === 5` pins {5}, and NaN passes
// no comparison, so the pinned tuple is the whole answer on that arm.
func TestA6_sink_dead_APinnedValueAlsoDropsTheNaNHalf(t *testing.T) {
	hours := PossiblyNaN(KnownSet(
		refinementsets.MakeRefinedSet(
			refinementsets.Integer, refinementsets.AtLeast(0), refinementsets.AtMost(23),
		),
		nil, TrustSpec, SetKindTagNone,
	))
	pinned := KnownValues([]float64{5}, PrimitiveNumber, TrustProved)

	met := MeetKnown(hours, pinned)
	if met.Kind == KindPossiblyNaN {
		t.Errorf("the meet against a pinned value kept the NaN half: %+v", met)
	}
}
