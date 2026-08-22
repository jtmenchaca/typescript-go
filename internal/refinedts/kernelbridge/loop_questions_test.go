// PremiseKey's "values" branch: a crash-class regression guard. A
// literal json.Marshal of a []float64 (what marshalWireValue does)
// errors on a non-finite member, and PremiseKey used to panic on that
// error rather than key the premise. These pin the fixed rule: every
// member spells with cacheNumberString, so a finite tuple keys exactly
// as the old marshalWireValue spelling did, and a tuple holding
// +Inf/-Inf/NaN keys injectively instead of panicking.
package kernelbridge

import (
	"math"
	"testing"
)

func TestPremiseKey_NonFiniteValuesDoNotPanic(t *testing.T) {
	premises := []InvariantPremise{
		{Kind: InvariantPremiseValues, Values: []float64{math.Inf(1)}},
		{Kind: InvariantPremiseValues, Values: []float64{math.Inf(-1)}},
		{Kind: InvariantPremiseValues, Values: []float64{math.NaN()}},
		{Kind: InvariantPremiseValues, Values: []float64{0, math.Inf(1), math.NaN()}},
	}
	for _, p := range premises {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("PremiseKey panicked on values %v: %v", p.Values, r)
				}
			}()
			key, ok := PremiseKey(p)
			if !ok {
				t.Fatalf("PremiseKey(%v) declined; a values premise always keys", p.Values)
			}
			if key == "" {
				t.Fatalf("PremiseKey(%v) returned an empty key", p.Values)
			}
		}()
	}
}

// The three non-finite sentinels, and the plain finite spelling, must
// all differ from one another — the same injectivity CanonicalPairOfSetAndTuple's
// cacheNumberString fix established for the member-question cache key.
func TestPremiseKey_NonFiniteValuesKeyInjectively(t *testing.T) {
	posInf, ok := PremiseKey(InvariantPremise{Kind: InvariantPremiseValues, Values: []float64{math.Inf(1)}})
	if !ok {
		t.Fatalf("PremiseKey(+Inf) declined")
	}
	negInf, ok := PremiseKey(InvariantPremise{Kind: InvariantPremiseValues, Values: []float64{math.Inf(-1)}})
	if !ok {
		t.Fatalf("PremiseKey(-Inf) declined")
	}
	nan, ok := PremiseKey(InvariantPremise{Kind: InvariantPremiseValues, Values: []float64{math.NaN()}})
	if !ok {
		t.Fatalf("PremiseKey(NaN) declined")
	}
	finite, ok := PremiseKey(InvariantPremise{Kind: InvariantPremiseValues, Values: []float64{0}})
	if !ok {
		t.Fatalf("PremiseKey(0) declined")
	}
	keys := map[string]string{"+inf": posInf, "-inf": negInf, "nan": nan, "finite-0": finite}
	seen := map[string]string{}
	for name, key := range keys {
		if other, exists := seen[key]; exists {
			t.Errorf("%q and %q collided on the same key %q", name, other, key)
		}
		seen[key] = name
	}
}

// A finite tuple keys exactly as marshalWireValue's plain JSON numeral
// spelling did before the fix — the regression pin.
func TestPremiseKey_FiniteValuesKeyUnchanged(t *testing.T) {
	key, ok := PremiseKey(InvariantPremise{Kind: InvariantPremiseValues, Values: []float64{1, 2, 3}})
	if !ok {
		t.Fatalf("PremiseKey([1,2,3]) declined")
	}
	if want := "[1,2,3]"; key != want {
		t.Errorf("PremiseKey([1,2,3]) = %q, want %q (marshalWireValue's own spelling)", key, want)
	}

	single, ok := PremiseKey(InvariantPremise{Kind: InvariantPremiseValues, Values: []float64{0}})
	if !ok {
		t.Fatalf("PremiseKey([0]) declined")
	}
	if want := "[0]"; single != want {
		t.Errorf("PremiseKey([0]) = %q, want %q", single, want)
	}

	empty, ok := PremiseKey(InvariantPremise{Kind: InvariantPremiseValues, Values: []float64{}})
	if !ok {
		t.Fatalf("PremiseKey([]) declined")
	}
	if want := "[]"; empty != want {
		t.Errorf("PremiseKey([]) = %q, want %q", empty, want)
	}
}
