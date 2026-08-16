// from evaluation/number_range.ts
//
// Closed numeric bounds read off a refinement set. This READ survives
// for the walk's own uses (loop solving, graph composition); the
// operator transfers no longer consume it — the kernel reads
// enclosures itself.

package walk

import (
	"math"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/primitives"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// NumberRange is closed bounds (±∞ allowed), whether every element is
// an integer, and an optional power-of-two STEP every element is a
// multiple of. This READ survives for the walk's own uses (loop
// solving, graph composition); the operator transfers no longer
// consume it — the kernel reads enclosures itself.
type NumberRange struct {
	Lo   float64
	Hi   float64
	Int  bool
	Step *float64
	// LoStrict/HiStrict: a strict lower/upper bound (`above` / `below`
	// forms) — the closed lo/hi still enclose soundly.
	LoStrict bool
	HiStrict bool
}

// stepOfValue is the largest power of two dividing a finite value;
// +Inf for 0 (divisible by everything), nil where unreadable.
func stepOfValue(v float64) *float64 {
	if !isFinite(v) {
		return nil
	}
	if v == 0 {
		inf := math.Inf(1)
		return &inf
	}
	// the guard above already rules out NaN and ±∞, so DyadicOfNumber
	// cannot error here — the TS source's own throw is unreachable
	// under this call site's own invariant.
	d, err := primitives.DyadicOfNumber(v)
	if err != nil {
		panic(err)
	}
	s := math.Pow(2, float64(d.Exp))
	return &s
}

// stepOfDivisor is the power-of-two step a divisor states, when it is
// one.
func stepOfDivisor(d float64) *float64 {
	if !isFinite(d) || d == 0 {
		return nil
	}
	pair, err := primitives.DyadicOfNumber(d)
	if err != nil {
		panic(err)
	}
	if abs64(pair.Num) == 1 {
		s := math.Pow(2, float64(pair.Exp))
		return &s
	}
	return nil
}

func isFinite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

func abs64(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

// isInteger mirrors Number.isInteger: finite and equal to its own
// truncation — false for NaN and ±∞, unlike a bare Trunc comparison.
func isInteger(v float64) bool {
	return isFinite(v) && v == math.Trunc(v)
}

// tighten intersects a step constraint: the COARSER set wins, i.e.
// the larger power (multiples of 4 ∧ multiples of 2 = multiples of
// 4).
func tighten(step *float64, s *float64) *float64 {
	if s == nil {
		return step
	}
	if step == nil {
		return s
	}
	out := math.Max(*step, *s)
	return &out
}

// RangeOfSet is a sound enclosing range of a 1-tuple-layer set —
// every element of the set lies within it. Nil when the set holds no
// single numbers (sequence forms, the bare root) — never a guess.
// Strict bounds are tracked so the integer form can tighten them:
// `below 3 ∧ integer` encloses in [−∞, 2], which is exactly what lets
// a counted loop's invariant certify.
func RangeOfSet(set refinementsets.RefinedSet) *NumberRange {
	if len(set.Forms) == 0 {
		return nil // the root holds tuples too
	}
	lo := math.Inf(-1)
	loStrict := false
	hi := math.Inf(1)
	hiStrict := false
	int_ := false
	var step *float64

	raiseLo := func(a float64, strict bool) {
		if a > lo {
			lo = a
			loStrict = strict
		} else if a == lo && strict {
			loStrict = true
		}
	}
	lowerHi := func(a float64, strict bool) {
		if a < hi {
			hi = a
			hiStrict = strict
		} else if a == hi && strict {
			hiStrict = true
		}
	}

	for _, form := range set.Forms {
		switch form.Form {
		case refinementsets.FormAtLeast:
			raiseLo(form.A, false)
		case refinementsets.FormAbove:
			raiseLo(form.A, true)
		case refinementsets.FormAtMost:
			lowerHi(form.A, false)
		case refinementsets.FormBelow:
			lowerHi(form.A, true)
		case refinementsets.FormInteger:
			int_ = true
		case refinementsets.FormMultipleOf:
			// multiples of an integer step are integers
			if isInteger(form.A) {
				int_ = true
			}
			step = tighten(step, stepOfDivisor(form.A))
		case refinementsets.FormOneOf:
			if len(form.W) == 0 {
				break // ∅ — no constraint readable
			}
			minW, maxW := form.W[0], form.W[0]
			allInt := true
			for _, v := range form.W {
				if v < minW {
					minW = v
				}
				if v > maxW {
					maxW = v
				}
				if !isInteger(v) {
					allInt = false
				}
			}
			raiseLo(minW, false)
			lowerHi(maxW, false)
			if allInt {
				int_ = true
			}
			// the members' common power of two
			common := math.Inf(1)
			commonOk := true
			for _, v := range form.W {
				s := stepOfValue(v)
				if s == nil {
					commonOk = false
					break
				}
				common = math.Min(common, *s)
			}
			if commonOk && isFinite(common) {
				step = tighten(step, &common)
			}
		case refinementsets.FormUnion:
			a := RangeOfSet(*form.A_)
			b := RangeOfSet(*form.B)
			if a == nil || b == nil {
				break // unreadable — no constraint
			}
			raiseLo(math.Min(a.Lo, b.Lo), false)
			lowerHi(math.Max(a.Hi, b.Hi), false)
			if a.Int && b.Int {
				int_ = true
			}
			// both branches must carry the step for the union to
			if a.Step != nil && b.Step != nil {
				m := math.Min(*a.Step, *b.Step)
				step = tighten(step, &m)
			}
		case refinementsets.FormDifference:
			a := RangeOfSet(*form.A_)
			if a == nil {
				break
			}
			raiseLo(a.Lo, false)
			lowerHi(a.Hi, false)
			if a.Int {
				int_ = true
			}
		default:
			return nil // a sequence form: not a single number
		}
	}
	// resolve to closed bounds; the integer form tightens strict ones
	if int_ {
		if isFinite(lo) {
			if loStrict {
				lo = math.Floor(lo) + 1
			} else {
				lo = math.Ceil(lo)
			}
		}
		if isFinite(hi) {
			if hiStrict {
				hi = math.Ceil(hi) - 1
			} else {
				hi = math.Floor(hi)
			}
		}
		loStrict = false
		hiStrict = false
	}
	// a whole step ≥ 1 makes every element an integer
	if step != nil && *step >= 1 {
		int_ = true
	}
	if step != nil && *step == 1 {
		step = nil // the integer form says it already
	}
	return &NumberRange{Lo: lo, Hi: hi, Int: int_, Step: step, LoStrict: loStrict, HiStrict: hiStrict}
}

// astralCodepointFloor mirrors refinementsets' own unexported
// astralFloor (codepoint_sets.go): the first scalar value that costs
// two UTF-16 code units (sec-ecmascript-language-types-string-type).
// Not importable — refinementsets keeps it package-private — so this
// is the same literal, restated where astralFreeSet needs it.
const astralCodepointFloor = 0x10000

// astralFreeSet is whether a codepoint alphabet is PROVEN to sit
// entirely below the astral floor — the same "unit indexing and
// scalar indexing coincide" gate refinementsets.AstralFree checks
// against an exact []float64 tuple, read here off a RefinedSet's sound
// enclosing range instead, so a bound (`OneOf([44])`, a comma
// separator) qualifies without needing a materialized value list.
// Unreadable (a sequence form) or unbounded above answers false —
// never a guess.
func astralFreeSet(set refinementsets.RefinedSet) bool {
	r := RangeOfSet(set)
	if r == nil || !isFinite(r.Hi) {
		return false
	}
	hi := r.Hi
	if r.HiStrict {
		// a strict upper bound admits nothing AT hi, so the enclosed
		// values sit below it already
		hi = math.Nextafter(hi, math.Inf(-1))
	}
	return hi < astralCodepointFloor
}

// RangeOfKnown is the range of what is known, when it is one number.
// A refinement variable ranges over its bound — exact for the
// universal reading, since every singleton of the bound is an
// admissible T.
func RangeOfKnown(k abstractdomain.AbstractValue) *NumberRange {
	if k.Kind == abstractdomain.KindValues {
		// a string- or array-sorted word has no numeric range —
		// reading one here would be the cross-sort reread
		if !abstractdomain.IsNumericKind(k) {
			return nil
		}
		if len(k.Values) != 1 {
			return nil
		}
		v := k.Values[0]
		return &NumberRange{Lo: v, Hi: v, Int: isInteger(v), Step: stepOfValue(v)}
	}
	if k.Kind == abstractdomain.KindSet {
		return RangeOfSet(k.Set)
	}
	if k.Kind == abstractdomain.KindVariable && k.StarDepth == 0 {
		return RangeOfSet(k.Bound)
	}
	return nil
}
