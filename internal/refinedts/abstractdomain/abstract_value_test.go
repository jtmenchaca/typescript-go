package abstractdomain

import "testing"

// TestAnObjectKnownsKeysRecordCarriesNoPrototype is the TS source's "an
// object known's keys record carries no prototype" test
// (abstract_value.test.ts). The TS source's point is that
// Object.create(null) keeps the keys record from answering
// Object.prototype.toString for keys["toString"] — the zod survey's
// prototype-collision class (#5266, #5098). ObjectKey (this package) is
// an ordered slice, not a Go map with a prototype, so there is no
// prototype to root against; the equivalent guarantee this test checks
// is that a lookup answers ONLY the named keys, never a well-known
// Object.prototype member that was never set.
func TestAnObjectKnownsKeysRecordCarriesNoPrototype(t *testing.T) {
	built := KnownObject(
		[]ObjectKey{{Name: "a", Value: KnownValues([]float64{1}, PrimitiveNumber, TrustProved)}},
		nil,
		true,
		TrustProved,
		false,
	)
	if built.Kind != KindObject {
		t.Fatalf("not an object known: %+v", built)
	}
	if _, ok := lookupKey(built.Keys, "toString"); ok {
		t.Errorf("keys[\"toString\"] should be absent")
	}
	if _, ok := lookupKey(built.Keys, "constructor"); ok {
		t.Errorf("keys[\"constructor\"] should be absent")
	}
	if _, ok := lookupKey(built.Keys, "hasOwnProperty"); ok {
		t.Errorf("keys[\"hasOwnProperty\"] should be absent")
	}
	a, ok := lookupKey(built.Keys, "a")
	if !ok {
		t.Fatalf("keys[\"a\"] should be present")
	}
	want := KnownValues([]float64{1}, PrimitiveNumber, TrustProved)
	if !SameKnown(a, want) {
		t.Errorf("keys[\"a\"] = %+v, want %+v", a, want)
	}
}
