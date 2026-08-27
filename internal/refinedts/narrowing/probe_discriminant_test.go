package narrowing

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

func TestProbeDiscriminantKeepsSiblingMember(t *testing.T) {
	circleArm := abstractdomain.KnownObject(
		[]abstractdomain.ObjectKey{
			{Name: "kind", Value: abstractdomain.KnownValues(refinementsets.CodepointsOf("circle"), abstractdomain.PrimitiveString, abstractdomain.TrustProved)},
			{Name: "r", Value: abstractdomain.PossiblyNaN(abstractdomain.KnownSet(refinementsets.Numbers, nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone))},
		},
		nil, false, abstractdomain.TrustProved, false,
	)
	squareArm := abstractdomain.KnownObject(
		[]abstractdomain.ObjectKey{
			{Name: "kind", Value: abstractdomain.KnownValues(refinementsets.CodepointsOf("square"), abstractdomain.PrimitiveString, abstractdomain.TrustProved)},
			{Name: "side", Value: abstractdomain.PossiblyNaN(abstractdomain.KnownSet(refinementsets.Numbers, nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone))},
		},
		nil, false, abstractdomain.TrustProved, false,
	)
	union := abstractdomain.KindUnionOf([]abstractdomain.AbstractValue{circleArm, squareArm})
	t.Logf("union.Kind = %v", union.Kind)

	n := Narrowed{
		Path:      []string{"kind"},
		Exact:     refinementsets.CodepointsOf("circle"),
		ExactSort: abstractdomain.PrimitiveString,
	}
	narrowed := ApplyNarrowed(union, n)
	t.Logf("narrowed.Kind = %v", narrowed.Kind)
	if narrowed.Kind != abstractdomain.KindObject {
		t.Fatalf("expected KindObject, got %v", narrowed.Kind)
	}
	for _, k := range narrowed.Keys {
		t.Logf("key=%s kind=%v", k.Name, k.Value.Kind)
	}
	rVal, found := objectKeyValue(narrowed, "r")
	if !found {
		t.Fatalf("expected r to be present")
	}
	if rVal.Kind == abstractdomain.KindUnknown {
		t.Errorf("r narrowed to Unknown — the arm's declared member set was lost")
	}
}
