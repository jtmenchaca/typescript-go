// TERMS.md §2, step 4: numbers cross the boundary as exact integer
// pairs n·2^e — every finite JavaScript number is exactly one, so the
// kernel never touches a float. This file is that decomposition, read
// straight off the IEEE 754 bits.
//
// Canonical form: num is odd, or the pair is {num: 0, exp: 0}. Two
// dyadics denote the same number exactly when their canonical forms
// are equal, so canonical output makes value equality visible.

package primitives

import (
	"errors"
	"math"
)

type Dyadic struct {
	Num int64
	Exp int64
}

const implicitBit int64 = 4503599627370496 // 2^52

// DyadicOfNumber is the exact integer pair a finite JavaScript number is.
// Returns an error on NaN (not an element of ℝ̄ — the boundary rejects it)
// and on ±∞ (they cross as the strings "-inf" / "+inf", not as pairs).
func DyadicOfNumber(x float64) (Dyadic, error) {
	if math.IsNaN(x) {
		return Dyadic{}, errors.New("NaN is not an element of ℝ̄")
	}
	if math.IsInf(x, 0) {
		return Dyadic{}, errors.New(
			`±∞ cross the boundary as "-inf" / "+inf", not as integer pairs`,
		)
	}
	if x == 0 {
		return Dyadic{Num: 0, Exp: 0}, nil
	}
	bits := math.Float64bits(x)
	hi := uint32(bits >> 32)
	lo := uint32(bits)
	negative := (hi & 0x80000000) != 0
	biased := (hi >> 20) & 0x7ff
	num := int64(hi&0xfffff)*4294967296 + int64(lo) // HIGH_WORD = 2^32
	var exp int64
	if biased == 0 {
		exp = -1074 // subnormal: no implicit bit
	} else {
		num += implicitBit
		exp = int64(biased) - 1075
	}
	for num%2 == 0 {
		num /= 2
		exp += 1
	}
	if negative {
		num = -num
	}
	return Dyadic{Num: num, Exp: exp}, nil
}

// NumberOfDyadic is the number a dyadic denotes. Exact whenever that value is
// a JavaScript number (in particular for every output of
// DyadicOfNumber — the round trip is bit-exact); a dyadic whose
// value is not representable rounds like any float operation.
func NumberOfDyadic(d Dyadic) float64 {
	return float64(d.Num) * math.Pow(2, float64(d.Exp))
}
