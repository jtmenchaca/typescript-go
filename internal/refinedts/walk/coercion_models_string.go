// from evaluation/coercion_models.ts
//
// The String-target coercion primitives: String.fromCharCode's unit
// assembly and its ToUint16 step. Split from coercion_models.go per
// file-length discipline.

package walk

import "math"

// jsFromCharCode is String.fromCharCode (sec-string.fromcharcode):
// each argument becomes the code unit whose numeric value is
// ℝ(? ToUint16(_next_)) and the result is their concatenation. False
// where the units spell a LONE surrogate — the code-point encoding
// cannot carry half a pair, so that shape keeps the sort-level answer
// instead of a silently wrong tuple.
func jsFromCharCode(values []float64) (string, bool) {
	units := make([]uint16, len(values))
	for i, v := range values {
		units[i] = jsToUint16(v)
	}
	for i := 0; i < len(units); i++ {
		u := units[i]
		if u >= 0xD800 && u <= 0xDBFF {
			if i+1 < len(units) && units[i+1] >= 0xDC00 && units[i+1] <= 0xDFFF {
				i++
				continue
			}
			return "", false
		}
		if u >= 0xDC00 && u <= 0xDFFF {
			return "", false
		}
	}
	return utf16ToString(units), true
}

// jsToUint16 is ToUint16 (sec-touint16): ToIntegerOrInfinity — NaN
// reads 0, the rest truncate — then ToFixedSizeInteger(int, ~unsigned~,
// 16): ±∞ read 0, the rest take modulo 2^16 (sec-tofixedsizeinteger).
func jsToUint16(v float64) uint16 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	truncated := math.Trunc(v)
	m := math.Mod(truncated, 65536)
	if m < 0 {
		m += 65536
	}
	return uint16(m)
}
