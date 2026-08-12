package abstractdomain

import "testing"

// TestAFunctionIsAlwaysTruthyAndJoinsWithItself is the TS source's "a
// function is always truthy and joins with itself" test
// (host_function.test.ts).
func TestAFunctionIsAlwaysTruthyAndJoinsWithItself(t *testing.T) {
	truthy, known := Truthiness(HostFunction)
	if !known || !truthy {
		t.Errorf("Truthiness(HostFunction) = (%v, %v), want (true, true)", truthy, known)
	}
	joined := JoinKnown(HostFunction, HostFunction)
	if joined.Kind != KindHostFunction {
		t.Errorf("joined.Kind = %v, want %v", joined.Kind, KindHostFunction)
	}
}

// TestAMixedBarenessObjectJoinDropsCompleteness is the TS source's "a
// mixed-bareness object join drops completeness" test
// (host_function.test.ts): one arm inherits Object.prototype, the other
// (Object.create(null)) does not — a complete claim would let a
// missing-key read pick one arm's prototype story, and neither alone is
// sound.
func TestAMixedBarenessObjectJoinDropsCompleteness(t *testing.T) {
	proto := KnownObject(
		[]ObjectKey{{Name: "a", Value: KnownValues([]float64{1}, PrimitiveNumber, TrustProved)}},
		nil,
		true,
		TrustProved,
		false,
	)
	bare := KnownObject(
		[]ObjectKey{{Name: "a", Value: KnownValues([]float64{1}, PrimitiveNumber, TrustProved)}},
		nil,
		true,
		TrustProved,
		true,
	)
	joined := JoinKnown(proto, bare)
	if joined.Kind != KindObject {
		t.Fatalf("expected an object, got %+v", joined)
	}
	if joined.Complete {
		t.Errorf("joined.Complete = true, want false")
	}
	bothBare := JoinKnown(bare, bare)
	if bothBare.Kind != KindObject {
		t.Fatalf("expected an object, got %+v", bothBare)
	}
	if !bothBare.Complete {
		t.Errorf("bothBare.Complete = false, want true")
	}
	if !bothBare.BareProto {
		t.Errorf("bothBare.BareProto = false, want true")
	}
}
