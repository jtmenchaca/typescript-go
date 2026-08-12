package primitives

import (
	"math"
	"math/big"
	"testing"
)

func canonical(d Dyadic) bool {
	return (d.Num == 0 && d.Exp == 0) || absInt64(d.Num)%2 == 1
}

func absInt64(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

func TestKnownDecompositions(t *testing.T) {
	cases := []struct {
		name string
		x    float64
		want Dyadic
	}{
		{"0", 0, Dyadic{Num: 0, Exp: 0}},
		{"-0", math.Copysign(0, -1), Dyadic{Num: 0, Exp: 0}},
		{"1", 1, Dyadic{Num: 1, Exp: 0}},
		{"3", 3, Dyadic{Num: 3, Exp: 0}},
		{"-3", -3, Dyadic{Num: -3, Exp: 0}},
		{"0.5", 0.5, Dyadic{Num: 1, Exp: -1}},
		{"0.75", 0.75, Dyadic{Num: 3, Exp: -2}},
		{"1024", 1024, Dyadic{Num: 1, Exp: 10}},
		// the least positive number: one step at the finest exponent
		{"MIN_VALUE", math.SmallestNonzeroFloat64, Dyadic{Num: 1, Exp: -1074}},
		{"MAX_SAFE_INTEGER", 9007199254740991, Dyadic{Num: 9007199254740991, Exp: 0}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := DyadicOfNumber(c.x)
			if err != nil {
				t.Fatalf("DyadicOfNumber(%v) returned error: %v", c.x, err)
			}
			if got != c.want {
				t.Errorf("DyadicOfNumber(%v) = %+v, want %+v", c.x, got, c.want)
			}
		})
	}
}

func Test0_1IsNotOneTenth_ThePairIsTheFloatsOwnValue(t *testing.T) {
	d, err := DyadicOfNumber(0.1)
	if err != nil {
		t.Fatalf("DyadicOfNumber(0.1) returned error: %v", err)
	}
	if !canonical(d) {
		t.Errorf("d = %+v is not canonical", d)
	}
	// exactly the float back, not 1/10
	if got := NumberOfDyadic(d); got != 0.1 {
		t.Errorf("NumberOfDyadic(d) = %v, want 0.1", got)
	}
	// and num·2^exp is NOT the rational 1/10: 10·num ≠ 2^-exp — checked
	// in exact integer arithmetic (the float product rounds into
	// equality past 2^53, which is the whole point)
	left := new(big.Int).Mul(big.NewInt(d.Num), big.NewInt(10))
	right := new(big.Int).Lsh(big.NewInt(1), uint(-d.Exp))
	if left.Cmp(right) == 0 {
		t.Errorf("10*num unexpectedly equals 2^-exp")
	}
}

func TestRoundTripIsBitExactAcrossTheRange(t *testing.T) {
	cases := []float64{
		1,
		-1,
		0.1,
		0.2,
		0.30000000000000004,
		math.Pi,
		2.220446049250313e-16, // Number.EPSILON
		math.SmallestNonzeroFloat64,
		-math.SmallestNonzeroFloat64,
		math.MaxFloat64,
		-math.MaxFloat64,
		math.Pow(2, -1022),       // the least normal
		math.Pow(2, -1022) * 0.5, // subnormal
		123456789.123456789,
	}
	for _, x := range cases {
		d, err := DyadicOfNumber(x)
		if err != nil {
			t.Fatalf("DyadicOfNumber(%v) returned error: %v", x, err)
		}
		if !canonical(d) {
			t.Errorf("DyadicOfNumber(%v) = %+v is not canonical", x, d)
		}
		got := NumberOfDyadic(d)
		if !objectIs(got, x) {
			t.Errorf("NumberOfDyadic(DyadicOfNumber(%v)) = %v, want %v (Object.is)", x, got, x)
		}
	}
}

// objectIs mirrors JavaScript's Object.is: unlike ==, it distinguishes
// +0/-0 and treats NaN as equal to itself.
func objectIs(a, b float64) bool {
	if math.IsNaN(a) && math.IsNaN(b) {
		return true
	}
	if a == 0 && b == 0 {
		return math.Signbit(a) == math.Signbit(b)
	}
	return a == b
}

func TestInitializedSweepRoundTrips(t *testing.T) {
	// a deterministic pseudo-random walk over magnitudes
	initialState := uint32(0x2545f491)
	next := func() float64 {
		initialState ^= initialState << 13
		initialState ^= initialState >> 17
		initialState ^= initialState << 5
		return float64(initialState) / 0xffffffff
	}
	for i := 0; i < 2000; i++ {
		magnitude := (next() - 0.5) * math.Pow(2, math.Floor(next()*128-64))
		d, err := DyadicOfNumber(magnitude)
		if err != nil {
			t.Fatalf("DyadicOfNumber(%v) returned error: %v", magnitude, err)
		}
		if !canonical(d) {
			t.Errorf("DyadicOfNumber(%v) = %+v is not canonical", magnitude, d)
		}
		got := NumberOfDyadic(d)
		if !objectIs(got, magnitude) && magnitude != 0 {
			t.Errorf("NumberOfDyadic(DyadicOfNumber(%v)) = %v, round trip failed", magnitude, got)
		}
	}
}

func TestNaNAndInfinitiesAreRefused(t *testing.T) {
	if _, err := DyadicOfNumber(math.NaN()); err == nil {
		t.Error("DyadicOfNumber(NaN) did not return an error")
	} else if !containsSubstring(err.Error(), "NaN") {
		t.Errorf("error %q does not mention NaN", err.Error())
	}
	if _, err := DyadicOfNumber(math.Inf(1)); err == nil {
		t.Error("DyadicOfNumber(+Inf) did not return an error")
	} else if !containsSubstring(err.Error(), "inf") {
		t.Errorf("error %q does not mention inf", err.Error())
	}
	if _, err := DyadicOfNumber(math.Inf(-1)); err == nil {
		t.Error("DyadicOfNumber(-Inf) did not return an error")
	} else if !containsSubstring(err.Error(), "inf") {
		t.Errorf("error %q does not mention inf", err.Error())
	}
}

func containsSubstring(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
