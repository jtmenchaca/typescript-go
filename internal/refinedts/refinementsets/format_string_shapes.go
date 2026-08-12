// How a refined set READS. Every user-visible rendering of a set is
// decided in this family and nowhere else -- a hover, a diagnostic's
// message, the runtime surface's own validation text, and the guard a
// diagnostic offers as the repair.
//
// Presentation only: the sets themselves are untouched. The display
// folds (vacuous rays vanish, the tightest bound wins, an absorbed
// union side vanishes) and recognizes the string shapes the surface
// builds (the codepoint alphabet, its repetitions, the pattern
// chains), so z.string().length(2) reads "string of exactly 2
// characters" and not its codepoint arithmetic.
//
// ── THREE AUDIENCES, THREE VOCABULARIES ──────────────────────────
//
// The same set reads three ways, and which one is right depends on who
// is reading. They are separate functions rather than one with a
// switch, so that a form no one thought about cannot silently fall
// from one vocabulary into another:
//
//	FormatForHover      prose beside a type, in braces. Single-
//	                     character comparisons (>= <=), a two-sided
//	                     window chained over x, infinity as a symbol.
//	                     {integer, 0 <= x <= 100}
//
//	FormatForDiagnostics the set algebra, for a diagnostic or the
//	                     runtime's own error. ASCII comparisons joined
//	                     with &&, the algebra's own symbols (∪ ∖ · ×).
//	                     >= 0 && <= 100
//
//	FormatAsGuardCode    runnable TypeScript the reader can paste --
//	                     Number.isInteger(r) && r >= 0. Only forms the
//	                     narrowing layer verifiably lifts appear, so a
//	                     guard shown is a guard that works.
//
// The braces belong to the hover alone. FormatForHover returns nil
// where the set adds nothing tsc's own type line already says, and
// silence is the right answer there rather than a repetition.
//
// THIS file holds what the three audiences share: the string-shape
// recognition (the codepoint alphabet, its repetitions, the pattern
// chains) and the spelling folds (vacuous rays vanish, an absorbed
// union side vanishes, a repeated arm reads once). The sets themselves
// are untouched by everything here.

package refinementsets

import (
	"encoding/json"
	"math"
	"strconv"
)

// FormatNumber is formatNumber in the TS source.
func FormatNumber(x float64) string {
	if x == math.Inf(1) {
		return "+∞"
	}
	if x == math.Inf(-1) {
		return "−∞"
	}
	// a bound past the safe integers is unreadable as digit runs -- the
	// exponent form carries the same exact value in fewer glyphs
	if isIntegerValue(x) && math.Abs(x) > maxSafeInteger {
		return strconv.FormatFloat(x, 'e', -1, 64)
	}
	return formatJSNumber(x)
}

const maxSafeInteger = 9007199254740991

func isIntegerValue(x float64) bool {
	return x == math.Trunc(x) && !math.IsInf(x, 0)
}

// formatJSNumber renders a float64 the way JavaScript's String(x)
// would -- plain decimal for values in the ordinary range, which is
// all FormatNumber calls this with.
func formatJSNumber(x float64) string {
	return strconv.FormatFloat(x, 'g', -1, 64)
}

/* ── the string shapes ───────────────────────────────────────────── */

func IsCharacter(r RefinedSet) bool {
	return sameSetJSON(r, Codepoints)
}

func IsStrings(r RefinedSet) bool {
	return sameSetJSON(r, Strings)
}

// ConcatParts is a right-nested concatenation chain, flattened to its
// parts.
func ConcatParts(r RefinedSet) []RefinedSet {
	if len(r.Forms) == 1 && r.Forms[0].Form == FormConcatenation {
		return append(ConcatParts(*r.Forms[0].A_), ConcatParts(*r.Forms[0].B)...)
	}
	return []RefinedSet{r}
}

// SingletonPoint is the single codepoint a one-member singleton holds,
// or ok=false.
func SingletonPoint(r RefinedSet) (float64, bool) {
	if len(r.Forms) == 1 && r.Forms[0].Form == FormOneOf && len(r.Forms[0].W) == 1 {
		return r.Forms[0].W[0], true
	}
	return 0, false
}

// FromPoints is fromPoints in the TS source: the JSON-quoted string
// spelled by a codepoint sequence, or ok=false where a value sits
// outside the scalar range.
func FromPoints(points []float64) (string, bool) {
	runes := make([]rune, len(points))
	for i, p := range points {
		if p < 0 || p > 0x10FFFF || (p >= 0xD800 && p <= 0xDFFF) {
			return "", false
		}
		runes[i] = rune(p)
	}
	return jsonQuoteString(string(runes)), true
}

func characters(n int) string {
	if n == 1 {
		return "1 character"
	}
	return strconv.Itoa(n) + " characters"
}

// StringShapeOf is the string reading of a set, where it has one: the
// alphabet, its repetitions (length bounds), and the pattern chains
// (starts/ends/includes). ok=false where the set is not one of the
// shapes the surface builds -- the ordinary spelling then applies.
// Bare literal chains are NOT read here: a tuple of numbers and a
// string share one word, and only the checked position's sort can tell
// them apart (FormatStringLiteral, applied by the caller who knows the
// sort).
func StringShapeOf(r RefinedSet) (string, bool) {
	if IsCharacter(r) {
		return "character", true
	}
	if IsStrings(r) {
		return "string", true
	}
	// an intersection of string forms (z.string().includes("@") stacks
	// the pattern onto C*): every form must read, and a bare "string"
	// alongside real constraints says nothing
	if len(r.Forms) > 1 {
		var readings []string
		for _, f := range r.Forms {
			one, ok := StringShapeOf(MakeRefinedSet(f))
			if !ok {
				return "", false
			}
			readings = append(readings, one)
		}
		var constrained []string
		for _, reading := range readings {
			if reading != "string" {
				constrained = append(constrained, reading)
			}
		}
		if len(constrained) == 0 {
			return "string", true
		}
		return joinStrings(constrained, " and "), true
	}
	rep, ok := AsRepetition(r)
	if ok && IsCharacter(rep.Element) {
		if rep.Lo == 0 && rep.Hi == nil {
			return "string", true
		}
		if rep.Hi == nil {
			return "string of at least " + characters(rep.Lo), true
		}
		if rep.Lo == *rep.Hi {
			return "string of exactly " + characters(rep.Lo), true
		}
		if rep.Lo == 0 {
			return "string of at most " + characters(*rep.Hi), true
		}
		return "string of " + strconv.Itoa(rep.Lo) + " to " + strconv.Itoa(*rep.Hi) + " characters", true
	}
	// the pattern chains: a literal chunk around the set of all strings
	// -- C* is unambiguously stringy, so these read sort-free
	parts := ConcatParts(r)
	if len(parts) >= 2 {
		stars := make([]bool, len(parts))
		points := make([]float64, len(parts))
		hasPoint := make([]bool, len(parts))
		for i, p := range parts {
			stars[i] = IsStrings(p)
			pt, pOk := SingletonPoint(p)
			points[i] = pt
			hasPoint[i] = pOk
		}
		literalBetween := func(from, to int) (string, bool) {
			var collected []float64
			for i := from; i < to; i++ {
				if !hasPoint[i] {
					return "", false
				}
				collected = append(collected, points[i])
			}
			if len(collected) == 0 {
				return "", false
			}
			return FromPoints(collected)
		}
		anyBoolSlice := func(bs []bool) bool {
			for _, b := range bs {
				if b {
					return true
				}
			}
			return false
		}
		if stars[len(parts)-1] && !anyBoolSlice(stars[:len(parts)-1]) {
			if s, sOk := literalBetween(0, len(parts)-1); sOk {
				return "string starting with " + s, true
			}
		}
		if stars[0] && !anyBoolSlice(stars[1:]) {
			if s, sOk := literalBetween(1, len(parts)); sOk {
				return "string ending with " + s, true
			}
		}
		if stars[0] && stars[len(parts)-1] && len(parts) >= 3 && !anyBoolSlice(stars[1:len(parts)-1]) {
			if s, sOk := literalBetween(1, len(parts)-1); sOk {
				return "string containing " + s, true
			}
		}
	}
	return "", false
}

// StringLiteralPoints is the codepoints a set is the singleton chain
// of -- or ok=false. The raw walk behind FormatStringLiteral, exported
// so a SORTLESS caller (the union display) can gate on printability
// before spelling the chain as text.
func StringLiteralPoints(r RefinedSet) ([]float64, bool) {
	if len(r.Forms) == 1 && r.Forms[0].Form == FormEmptyTuple {
		return []float64{}, true
	}
	parts := ConcatParts(r)
	points := make([]float64, 0, len(parts))
	for _, part := range parts {
		p, ok := SingletonPoint(part)
		if !ok {
			return nil, false
		}
		points = append(points, p)
	}
	return points, true
}

// FormatStringLiteral is the literal string a set is the singleton of
// -- a chain of one-codepoint singletons -- or ok=false. A tuple of
// numbers has the same shape, so callers apply this only where the
// checked position's sort says string.
func FormatStringLiteral(r RefinedSet) (string, bool) {
	points, ok := StringLiteralPoints(r)
	if !ok {
		return "", false
	}
	if len(points) == 0 {
		return `""`, true
	}
	return FromPoints(points)
}

/* ── the scalar fold ─────────────────────────────────────────────── */

// satisfiesScalar answers whether the number satisfies every scalar
// form. The middle return is ok=false where a form is not
// scalar-evaluable -- the caller then makes no folding claim.
func satisfiesScalar(x float64, r RefinedSet) (bool, bool) {
	for _, f := range r.Forms {
		switch f.Form {
		case FormAtLeast:
			if !(x >= f.A) {
				return false, true
			}
		case FormAbove:
			if !(x > f.A) {
				return false, true
			}
		case FormAtMost:
			if !(x <= f.A) {
				return false, true
			}
		case FormBelow:
			if !(x < f.A) {
				return false, true
			}
		case FormInteger:
			if !isIntegerValue(x) {
				return false, true
			}
		case FormMultipleOf:
			if math.Mod(x, f.A) != 0 {
				return false, true
			}
		case FormOneOf:
			if !floatsInclude(f.W, x) {
				return false, true
			}
		case FormUnion:
			a, aOk := satisfiesScalar(x, *f.A_)
			b, bOk := satisfiesScalar(x, *f.B)
			if !aOk || !bOk {
				return false, false
			}
			if !a && !b {
				return false, true
			}
		case FormDifference:
			a, aOk := satisfiesScalar(x, *f.A_)
			b, bOk := satisfiesScalar(x, *f.B)
			if !aOk || !bOk {
				return false, false
			}
			if !a || b {
				return false, true
			}
		case FormEmptyTuple, FormConcatenation, FormStar, FormRepeat, FormRepeatWord:
			return false, false // a sequence form: not a scalar question
		default:
			UnreachedForm(f)
		}
	}
	return true, true
}

func floatsInclude(xs []float64, x float64) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// AbsorbUnion is one side of a union absorbed by the other: a finite
// side whose every member the other side provably holds says nothing
// -- the spelling keeps the other side alone.
func AbsorbUnion(a, b RefinedSet) (RefinedSet, bool) {
	finite := func(r RefinedSet) ([]float64, bool) {
		if len(r.Forms) == 1 && r.Forms[0].Form == FormOneOf {
			return r.Forms[0].W, true
		}
		return nil, false
	}
	aMembers, aOk := finite(a)
	if aOk && allSatisfyScalar(aMembers, b) {
		return b, true
	}
	bMembers, bOk := finite(b)
	if bOk && allSatisfyScalar(bMembers, a) {
		return a, true
	}
	return RefinedSet{}, false
}

func allSatisfyScalar(members []float64, r RefinedSet) bool {
	for _, x := range members {
		ok, _ := satisfiesScalar(x, r)
		if !ok {
			return false
		}
	}
	return true
}

// WithoutRepeatedArms is the union spelled without repeated arms: the
// union tree flattened, each arm kept once -- X | X is X, so a
// structurally repeated arm adds nothing to the spelling. ok=false
// where no arm repeats. The set itself is untouched either way.
func WithoutRepeatedArms(a, b RefinedSet) (RefinedSet, bool) {
	var armsOf func(s RefinedSet) []RefinedSet
	armsOf = func(s RefinedSet) []RefinedSet {
		if len(s.Forms) == 1 && s.Forms[0].Form == FormUnion {
			return append(armsOf(*s.Forms[0].A_), armsOf(*s.Forms[0].B)...)
		}
		return []RefinedSet{s}
	}
	arms := append(armsOf(a), armsOf(b)...)
	var distinct []RefinedSet
	for _, arm := range arms {
		seen := false
		for _, d := range distinct {
			if sameSetJSON(d, arm) {
				seen = true
				break
			}
		}
		if !seen {
			distinct = append(distinct, arm)
		}
	}
	if len(distinct) == len(arms) {
		return RefinedSet{}, false
	}
	rebuilt := distinct[len(distinct)-1]
	for i := len(distinct) - 2; i >= 0; i-- {
		rebuilt = MakeRefinedSet(Union(distinct[i], rebuilt))
	}
	return rebuilt, true
}

// FoldedForms is the forms after the spelling fold: the semantic
// tightest-ray fold, with the one PRESENTATIONAL extra that a
// non-strict ray to its own infinity says nothing and is dropped. The
// set itself is untouched.
func FoldedForms(r RefinedSet) []Refinement {
	folded := FoldRayForms(r.Forms)
	var out []Refinement
	for _, f := range folded {
		if f.Form == FormAtLeast && f.A == math.Inf(-1) {
			continue
		}
		if f.Form == FormAtMost && f.A == math.Inf(1) {
			continue
		}
		out = append(out, f)
	}
	return out
}

func joinStrings(parts []string, sep string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += sep
		}
		out += p
	}
	return out
}

// jsonQuoteString mirrors JSON.stringify(s) for a plain string value:
// a double-quoted, escaped literal.
func jsonQuoteString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}
