// The hover vocabulary: prose beside a type, in braces --
// single-character comparisons (>= <=), a two-sided window chained
// over x, infinity as a symbol. {integer, 0 <= x <= 100}. Split from
// display.ts per the v2 tree; the folds and string shapes it applies
// live in format_string_shapes.go.
//
// The editor plugin puts the text inside tsserver's own type line. A
// rendering that OPENS WITH A BRACE is a suffix -- it appends after
// the host type. Anything else REPLACES the right-hand side, so a
// temporal statement reads Temporal.PlainDate {...} rather than
// doubling the host type. ReplacesHostType states that rule for the
// plugin, so the decision lives here with the vocabularies it belongs
// to.

package refinementsets

import (
	"math"
	"strconv"
	"strings"
)

// ReplacesHostType is whether a rendering REPLACES the host type on
// the line, or rides after it. A brace opens a suffix; anything else
// names the type itself. The editor plugin asks this rather than
// deciding it.
func ReplacesHostType(display string) bool {
	return !strings.HasPrefix(display, "{")
}

// Two placeholders, one convention: value is the value, length is its
// length. Both are the mathematical italic, and both chain the same
// way -- 0 <= value <= 100, 3 <= length <= 5. A one-sided bound puts
// the placeholder first so it reads in the same direction:
// length >= 1.
//
// Every word here is one the SURFACE uses, so a hover reads back the
// vocabulary the developer wrote: integer (z.int), multipleOf, length,
// startsWith, endsWith, includes. No synonyms.
//
// The set algebra does not appear. A union is |, the union bar a
// TypeScript developer already reads as "or"; a difference is ≠, from
// the comparison family already on screen.

const hoverValue = "𝑥"
const hoverLength = "𝑙𝑒𝑛"

type hoverBound struct {
	a      float64
	strict bool
}

// chain is a bound pair over one placeholder: chained where both sides
// are known, the placeholder first where only one is.
func chain(placeholder string, lower, upper *hoverBound, formatAt func(float64) string) (string, bool) {
	if formatAt == nil {
		formatAt = FormatNumber
	}
	if lower != nil && upper != nil {
		lowerOp := "≤"
		if lower.strict {
			lowerOp = "<"
		}
		upperOp := "≤"
		if upper.strict {
			upperOp = "<"
		}
		return formatAt(lower.a) + " " + lowerOp + " " + placeholder + " " + upperOp + " " + formatAt(upper.a), true
	}
	if lower != nil {
		op := "≥"
		if lower.strict {
			op = ">"
		}
		return placeholder + " " + op + " " + formatAt(lower.a), true
	}
	if upper != nil {
		op := "≤"
		if upper.strict {
			op = "<"
		}
		return placeholder + " " + op + " " + formatAt(upper.a), true
	}
	return "", false
}

func countBound(n float64) hoverBound {
	return hoverBound{a: n, strict: false}
}

// orSide is one side of an "or". Facts are joined by commas meaning
// "and", so a side with more than one of them needs brackets to say
// where it ends.
func orSide(facts []string) string {
	if len(facts) > 1 {
		return "(" + joinStrings(facts, ", ") + ")"
	}
	return facts[0]
}

// unambiguousStringLiteral is the quoted string a set is UNAMBIGUOUSLY
// the literal of -- a concatenation chain, or the empty tuple. A lone
// scalar singleton has the same shape as a one-character string and
// only the checked position's sort could tell them apart, so it is
// never read here.
func unambiguousStringLiteral(s RefinedSet) (string, bool) {
	if len(s.Forms) == 1 && (s.Forms[0].Form == FormConcatenation || s.Forms[0].Form == FormEmptyTuple) {
		return FormatStringLiteral(s)
	}
	return "", false
}

// stringFactsInHover is the facts a STRING set states, in the hover's
// vocabulary -- length over hoverLength, the pattern methods by the
// surface's own names. ok=false where the set is not a string shape at
// all; an empty, ok=true slice where it is every string and so says
// nothing.
func stringFactsInHover(r RefinedSet) ([]string, bool) {
	if IsCharacter(r) {
		return []string{hoverLength + " = 1"}, true
	}
	if IsStrings(r) {
		return []string{}, true
	}
	// an intersection of string forms: every form must read
	if len(r.Forms) > 1 {
		var facts []string
		for _, f := range r.Forms {
			one, ok := stringFactsInHover(MakeRefinedSet(f))
			if !ok {
				return nil, false
			}
			facts = append(facts, one...)
		}
		return facts, true
	}
	rep, repOk := AsRepetition(r)
	if repOk && IsCharacter(rep.Element) {
		if rep.Lo == 0 && rep.Hi == nil {
			return []string{}, true
		}
		if rep.Hi != nil && rep.Lo == *rep.Hi {
			return []string{hoverLength + " = " + strconv.Itoa(rep.Lo)}, true
		}
		var lower, upper *hoverBound
		if rep.Lo != 0 {
			b := countBound(float64(rep.Lo))
			lower = &b
		}
		if rep.Hi != nil {
			b := countBound(float64(*rep.Hi))
			upper = &b
		}
		bounded, boundedOk := chain(hoverLength, lower, upper, nil)
		if !boundedOk {
			return []string{}, true
		}
		return []string{bounded}, true
	}
	// the pattern chains, by the method that built them
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
		anyBool := func(bs []bool) bool {
			for _, b := range bs {
				if b {
					return true
				}
			}
			return false
		}
		if stars[len(parts)-1] && !anyBool(stars[:len(parts)-1]) {
			if s, sOk := literalBetween(0, len(parts)-1); sOk {
				return []string{"startsWith " + s}, true
			}
		}
		if stars[0] && !anyBool(stars[1:]) {
			if s, sOk := literalBetween(1, len(parts)); sOk {
				return []string{"endsWith " + s}, true
			}
		}
		if stars[0] && stars[len(parts)-1] && len(parts) >= 3 && !anyBool(stars[1:len(parts)-1]) {
			if s, sOk := literalBetween(1, len(parts)-1); sOk {
				return []string{"includes " + s}, true
			}
		}
	}
	return nil, false
}

// hoverFacts is everything a set states, as separate facts in the
// hover's vocabulary. ok=false where the set says nothing worth
// showing.
func hoverFacts(r RefinedSet) ([]string, bool) {
	if len(r.Forms) == 0 {
		return nil, false
	}

	if strFacts, ok := stringFactsInHover(r); ok {
		if len(strFacts) == 0 {
			return nil, false
		}
		return strFacts, true
	}

	// a SEQUENCE: its length, and what each element is. hoverLength is
	// true of an array exactly as it is of a string, so there is one
	// vocabulary rather than two.
	if len(r.Forms) == 1 && r.Forms[0].Form == FormStar {
		element, ok := hoverFacts(*r.Forms[0].A_)
		if !ok {
			return nil, false
		}
		return []string{"each " + joinStrings(element, ", ")}, true
	}
	if rep, ok := AsRepetition(r); ok {
		var facts []string
		var bounded string
		if rep.Hi != nil && rep.Lo == *rep.Hi {
			bounded = hoverLength + " = " + strconv.Itoa(rep.Lo)
		} else {
			var lower, upper *hoverBound
			if rep.Lo != 0 {
				b := countBound(float64(rep.Lo))
				lower = &b
			}
			if rep.Hi != nil {
				b := countBound(float64(*rep.Hi))
				upper = &b
			}
			bounded, _ = chain(hoverLength, lower, upper, nil)
		}
		if bounded != "" {
			facts = append(facts, bounded)
		}
		if element, elOk := hoverFacts(rep.Element); elOk {
			facts = append(facts, "each "+joinStrings(element, ", "))
		}
		if len(facts) == 0 {
			return nil, false
		}
		return facts, true
	}

	folded := FoldedForms(r)
	if len(folded) == 0 {
		return nil, false
	}
	// the ordering rule: what KIND of number it is, then the range,
	// then which values, then what it excludes
	var kind []string
	var membership []string
	var excluded []string
	var rest []string
	var lower, upper *hoverBound
	for _, f := range folded {
		switch f.Form {
		case FormInteger:
			kind = append(kind, "integer")
		case FormMultipleOf:
			kind = append(kind, "multipleOf "+FormatNumber(f.A))
		case FormAtLeast:
			b := hoverBound{a: f.A, strict: false}
			lower = &b
		case FormAbove:
			b := hoverBound{a: f.A, strict: true}
			lower = &b
		case FormAtMost:
			b := hoverBound{a: f.A, strict: false}
			upper = &b
		case FormBelow:
			b := hoverBound{a: f.A, strict: true}
			upper = &b
		case FormOneOf:
			parts := make([]string, len(f.W))
			for i, w := range f.W {
				parts[i] = FormatNumber(w)
			}
			membership = append(membership, joinStrings(parts, " | "))
		case FormUnion:
			// a union reads as the union BAR, each side in this same
			// vocabulary -- never as the algebra's ∪
			if absorbed, ok := AbsorbUnion(*f.A_, *f.B); ok {
				if one, oneOk := hoverFacts(absorbed); oneOk {
					rest = append(rest, joinStrings(one, ", "))
				}
				break
			}
			side := func(s RefinedSet) (string, bool) {
				if lit, litOk := unambiguousStringLiteral(s); litOk {
					return lit, true
				}
				facts, factsOk := hoverFacts(s)
				if !factsOk || len(facts) == 0 {
					return "", false
				}
				return orSide(facts), true
			}
			// a structurally repeated arm across the union tree reads
			// once -- X | X is X, so the spelling drops the repeat
			if deduped, ok := WithoutRepeatedArms(*f.A_, *f.B); ok {
				one, oneOk := side(deduped)
				if !oneOk {
					return nil, false
				}
				rest = append(rest, one)
				break
			}
			a, aOk := side(*f.A_)
			b, bOk := side(*f.B)
			if !aOk || !bOk {
				// a union side the hover vocabulary cannot say would leak
				// the algebra register -- the hover stays silent instead
				return nil, false
			}
			rest = append(rest, a+" | "+b)
		case FormDifference:
			// what it excludes, as a disequality -- a base with nothing
			// of its own to say (the number universal) contributes no
			// conjunct, and the ≠ still speaks
			a, aOk := hoverFacts(*f.A_)
			var removed string
			removedOk := false
			if len(f.B.Forms) == 1 && f.B.Forms[0].Form == FormOneOf {
				parts := make([]string, len(f.B.Forms[0].W))
				for i, w := range f.B.Forms[0].W {
					parts[i] = FormatNumber(w)
				}
				removed = joinStrings(parts, " | ")
				removedOk = true
			}
			if !removedOk {
				rest = append(rest, FormatForm(f))
			} else {
				if aOk && len(a) > 0 {
					rest = append(rest, joinStrings(a, ", "))
				}
				excluded = append(excluded, "≠ "+removed)
			}
		case FormEmptyTuple, FormConcatenation, FormStar, FormRepeat, FormRepeatWord:
			rest = append(rest, FormatForm(f))
		default:
			UnreachedForm(f)
		}
	}
	// the strict +-infinity window is zod's own .finite() -- one word
	// the surface uses, not a chained comparison against two infinities
	if lower != nil && upper != nil &&
		lower.a == math.Inf(-1) && lower.strict &&
		upper.a == math.Inf(1) && upper.strict {
		kind = append(kind, "finite")
		lower = nil
		upper = nil
	}
	// integer inside the +-(2^53 - 1) window is exactly
	// Number.isSafeInteger -- the standard term, not sixteen-digit
	// bounds; z.int() states this very set
	if containsString(kind, "integer") &&
		lower != nil && upper != nil &&
		lower.a == -9007199254740991 && !lower.strict &&
		upper.a == 9007199254740991 && !upper.strict {
		for i, k := range kind {
			if k == "integer" {
				kind[i] = "safe integer"
				break
			}
		}
		lower = nil
		upper = nil
	}
	rangeStr, rangeOk := chain(hoverValue, lower, upper, nil)
	var out []string
	out = append(out, kind...)
	if rangeOk {
		out = append(out, rangeStr)
	}
	out = append(out, membership...)
	out = append(out, rest...)
	out = append(out, excluded...)
	return out, true
}

func containsString(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// FormatForHover is the hover rendering of a stated set: its facts in
// braces after tsc's own type, so `type Port = number` reads on as
// `number {integer, 1 <= x <= 65535}`. A set that is exactly ONE value
// renders as that value instead -- the type language can say it, so it
// replaces the type rather than annotating it. ok=false where the set
// adds nothing tsc's type does not already carry.
func FormatForHover(r RefinedSet) (string, bool) {
	folded := FoldedForms(r)
	if len(folded) == 1 && folded[0].Form == FormOneOf && len(folded[0].W) == 1 {
		if _, stringOk := stringFactsInHover(r); !stringOk {
			return FormatNumber(folded[0].W[0]), true
		}
	}
	// exactly one string: the literal type, like the one number above
	if literal, ok := unambiguousStringLiteral(r); ok {
		return literal, true
	}
	facts, ok := hoverFacts(r)
	if !ok || len(facts) == 0 {
		return "", false
	}
	return "{" + joinStrings(facts, ", ") + "}", true
}
