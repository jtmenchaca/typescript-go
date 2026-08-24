// ElementOf's fallthrough used to answer a bare silence.Residue() with
// no reason — the naked generic residue construct B names. Every path
// that reaches "nothing else speaks" now carries a ResidueReason: the
// values-set-empty arm, the list-empty arm, and the final fallthrough
// each name what blocked them, matching the ResidueOf idiom other
// readers already use (math_transfer.go, element_access.go).

package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
)

func TestElementOf_UnmodeledShapeFallthroughNamesItsReader(t *testing.T) {
	// KindObject is not one of ElementOf's named arms (KindSet,
	// KindValues, KindArrayHoles, KindList, KindVariable, the
	// object-star route) — it reaches the bare fallthrough at the
	// bottom of the function.
	unmodeled := abstractdomain.KnownObject(nil, nil, true, abstractdomain.TrustProved, false)
	got := ElementOf(unmodeled)
	if got.Kind != abstractdomain.KindUnknown {
		t.Fatalf("ElementOf(KindObject) kind = %v, want KindUnknown", got.Kind)
	}
	const want = "the iteration element reader holds no model for this iterable's shape"
	if got.ResidueReason != want {
		t.Errorf("ElementOf(KindObject) ResidueReason = %q, want %q", got.ResidueReason, want)
	}
}

func TestElementOf_EmptyValuesSetNamesWhyNoElementJoins(t *testing.T) {
	empty := abstractdomain.KnownValues(nil, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
	got := ElementOf(empty)
	if got.Kind != abstractdomain.KindUnknown {
		t.Fatalf("ElementOf(KindValues{}) kind = %v, want KindUnknown", got.Kind)
	}
	const want = "the values set is empty, so no element position exists to join"
	if got.ResidueReason != want {
		t.Errorf("ElementOf(KindValues{}) ResidueReason = %q, want %q", got.ResidueReason, want)
	}
}

func TestElementOf_EmptyListNamesNoModelForTheShape(t *testing.T) {
	empty := abstractdomain.KnownList(nil, abstractdomain.TrustProved)
	got := ElementOf(empty)
	if got.Kind != abstractdomain.KindUnknown {
		t.Fatalf("ElementOf(KindList{}) kind = %v, want KindUnknown", got.Kind)
	}
	const want = "the iteration element reader holds no model for this iterable's shape"
	if got.ResidueReason != want {
		t.Errorf("ElementOf(KindList{}) ResidueReason = %q, want %q", got.ResidueReason, want)
	}
}
