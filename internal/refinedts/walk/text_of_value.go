// from evaluation/text_of_value.ts
//
// What a value becomes as TEXT — the one conversion, mirroring the
// spec's ToString (sec-tostring): a number spells its decimal form, a
// boolean the word true or false, undefined and null their own words,
// a plain object "[object Object]" through Object.prototype.toString
// (sec-object.prototype.tostring), and an array joins its elements
// with commas (sec-array.prototype.tostring → join). Template
// literals, String(x), and joins all read through here, so any stated
// format downstream judges the same text the runtime produces.

package walk

import (
	"strconv"
	"strings"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// TextReading is TextReading in the TS source.
type TextReading struct {
	// Exact: the codepoints of the ONE exact string, when the text is
	// exact. Nil (with HasExact false) otherwise.
	Exact    []float64
	HasExact bool
	// Set: the set of strings the text can be — always present.
	Set refinementsets.RefinedSet
	// Grade: the trust level of this reading.
	Grade abstractdomain.TrustLevel
}

func exactText(text string, grade abstractdomain.TrustLevel) TextReading {
	points := refinementsets.CodepointsOf(text)
	return TextReading{Exact: points, HasExact: true, Set: refinementsets.StringTuple(text), Grade: grade}
}

func unionOf(a, b refinementsets.RefinedSet) refinementsets.RefinedSet {
	return refinementsets.MakeRefinedSet(refinementsets.Union(a, b))
}

// stringOf builds a string from codepoint values, mirroring
// String.fromCodePoint(...values).
func stringOf(values []float64) string {
	var b strings.Builder
	for _, v := range values {
		b.WriteRune(rune(int32(v)))
	}
	return b.String()
}

// numericSetText is the string image of a provably-numeric scalar
// set: today's unbounded star of digit codepoints (refinementsets.
// Strings), tightened to the exact digit-count window a non-negative
// integer window forces. A number's decimal spelling (Number::
// toString, sec-numeric-types-number-tostring) never pads a leading
// zero, so every integer in [lo, hi] with lo >= 0 spells EXACTLY
// digitCount(v) digit codepoints, and digitCount is monotonic in v --
// the window's own digit-count span is [digitCount(lo), digitCount
// (hi)]. Where lo and hi share a digit count, that count is exact
// (repeat(Digits, n, n)); where they differ, the derived shape is the
// repetition window over that span (repeat(Digits, loCount, hiCount)).
// Anything wider than a plain non-negative integer window (negatives,
// non-integers, an unbounded side) has no digit-count fact to read
// off syntactically here, and keeps today's unbounded star.
func numericSetText(set refinementsets.RefinedSet) refinementsets.RefinedSet {
	lo, hi, ok := refinementsets.NonNegativeIntegerBounds(set)
	if !ok {
		return refinementsets.Strings
	}
	loCount := digitCountOf(lo)
	hiCount := digitCountOf(hi)
	return refinementsets.Repetition(refinementsets.Digits, loCount, &hiCount)
}

// digitCountOf is the decimal digit count of a non-negative integer,
// mirroring Number::toString's radix-10 spelling with no leading
// zero: 0 spells as "0" (one digit), and every v >= 1 spells as
// floor(log10(v)) + 1 digits. Walks by repeated division rather than
// formatting the float, so it stays exact at the double integers this
// window ever carries (no float64 -> string round-trip to drift on).
func digitCountOf(v float64) int {
	n := int64(v)
	if n == 0 {
		return 1
	}
	count := 0
	for n > 0 {
		count++
		n /= 10
	}
	return count
}

// textIsAlwaysAString reports whether ToString on a value of this kind
// LANDS a string on every run that completes (sec-tostring). A symbol
// throws at step 2, and a bare-prototype object reaches ToPrimitive
// with neither toString nor valueOf to call, so it throws too — those
// two answer no. Every other kind either converts directly or reaches
// a primitive through the prototype it carries. ADDED IN GO: the TS
// source has no twin, because its union arm voids the whole reading
// instead of widening one arm.
func textIsAlwaysAString(known abstractdomain.AbstractValue) bool {
	switch known.Kind {
	case abstractdomain.KindSymbol:
		return false
	case abstractdomain.KindObject:
		return !known.BareProto
	case abstractdomain.KindValues, abstractdomain.KindSet, abstractdomain.KindNaN,
		abstractdomain.KindUndef, abstractdomain.KindNull, abstractdomain.KindList, abstractdomain.KindCollection,
		abstractdomain.KindPromise, abstractdomain.KindDate, abstractdomain.KindRegex,
		abstractdomain.KindHostFunction, abstractdomain.KindBigints:
		return true
	case abstractdomain.KindPossiblyUndefined, abstractdomain.KindPossiblyNaN:
		return known.Inner != nil && textIsAlwaysAString(*known.Inner)
	case abstractdomain.KindKindUnion:
		for _, arm := range known.Arms {
			if !textIsAlwaysAString(arm) {
				return false
			}
		}
		return len(known.Arms) > 0
	default:
		// variable and unknown: the kind itself is not pinned, so
		// whether ToString lands is not pinned either
		return false
	}
}

// TextOfKnown is textOfKnown in the TS source: the text a value
// becomes, or (TextReading{}, false) where the conversion is not
// modeled (a function's source text; an object whose own toString the
// walk cannot run; a bare-prototype object, which THROWS). The caller
// passes the kernel's proved decimal speller; the host's own String()
// stands in at spec grade where the kernel declines.
func TextOfKnown(decimal func(v float64) (string, bool), known abstractdomain.AbstractValue) (TextReading, bool) {
	switch known.Kind {
	case abstractdomain.KindValues:
		if known.KindTag == abstractdomain.PrimitiveString {
			return TextReading{
				Exact:    known.Values,
				HasExact: true,
				Set:      refinementsets.StringTuple(stringOf(known.Values)),
				Grade:    abstractdomain.TrustLevelOf(known),
			}, true
		}
		if known.KindTag == abstractdomain.PrimitiveNumber || known.KindTag == abstractdomain.PrimitiveBoolean {
			texts := make([]string, 0, len(known.Values))
			grade := abstractdomain.TrustLevelOf(known)
			for _, v := range known.Values {
				if known.KindTag == abstractdomain.PrimitiveBoolean {
					if v == 0 {
						texts = append(texts, "false")
					} else {
						texts = append(texts, "true")
					}
					continue
				}
				lifted, ok := decimal(v)
				if !ok {
					grade = abstractdomain.MinTrustLevel(grade, abstractdomain.TrustSpec)
					lifted = strconv.FormatFloat(v, 'g', -1, 64)
				}
				texts = append(texts, lifted)
			}
			if len(texts) == 1 {
				return exactText(texts[0], grade), true
			}
			set := refinementsets.StringTuple(texts[0])
			for i := 1; i < len(texts); i++ {
				set = unionOf(set, refinementsets.StringTuple(texts[i]))
			}
			return TextReading{Exact: nil, HasExact: false, Set: set, Grade: grade}, true
		}
		if known.KindTag == abstractdomain.PrimitiveArray {
			// Array.prototype.toString is join(",") — each number
			// element spells its decimal form
			// (sec-array.prototype.tostring)
			pieces := make([]string, 0, len(known.Values))
			grade := abstractdomain.TrustLevelOf(known)
			for _, v := range known.Values {
				lifted, ok := decimal(v)
				if !ok {
					grade = abstractdomain.MinTrustLevel(grade, abstractdomain.TrustSpec)
					lifted = strconv.FormatFloat(v, 'g', -1, 64)
				}
				pieces = append(pieces, lifted)
			}
			return exactText(strings.Join(pieces, ","), grade), true
		}
		return TextReading{}, false
	case abstractdomain.KindNaN:
		return exactText("NaN", abstractdomain.TrustLevelOf(known)), true
	case abstractdomain.KindUndef:
		// KindUndef is exactly-undefined (the null/undefined split lives
		// on KindNull now) — sec-tostring step 3: ToString(undefined) is
		// "undefined", one exact word.
		return exactText("undefined", abstractdomain.TrustLevelOf(known)), true
	case abstractdomain.KindNull:
		// sec-tostring step 4: ToString(null) is "null", one exact word.
		return exactText("null", abstractdomain.TrustLevelOf(known)), true
	case abstractdomain.KindSet:
		// a provably scalar set is numeric: its text is SOME numeric
		// spelling — the exact form is out of reach, but the result is
		// a string. A sequence-shaped set could be a string (its own
		// text) or an array (joined elements) — only the caller's
		// declared type tells them apart, so it stays out of reach
		// here.
		if known.SetKindTag == abstractdomain.SetKindTagNone && refinementsets.OnOneTupleLayer(known.Set) {
			return TextReading{
				Exact:    nil,
				HasExact: false,
				Set:      numericSetText(known.Set),
				Grade:    abstractdomain.MinTrustLevel(abstractdomain.TrustLevelOf(known), abstractdomain.TrustSpec),
			}, true
		}
		return TextReading{}, false
	case abstractdomain.KindObject:
		// a complete plain object with the default prototype and no own
		// toString/valueOf converts through Object.prototype.toString:
		// exactly "[object Object]". A bare-prototype object THROWS
		// (no toString, no valueOf) — not a text at all.
		if known.BareProto {
			return TextReading{}, false
		}
		if known.Complete {
			hasToString, hasValueOf := false, false
			for _, key := range known.Keys {
				if key.Name == "toString" {
					hasToString = true
				}
				if key.Name == "valueOf" {
					hasValueOf = true
				}
			}
			if !hasToString && !hasValueOf {
				return exactText(
					"[object Object]",
					abstractdomain.MinTrustLevel(abstractdomain.TrustLevelOf(known), abstractdomain.TrustSpec),
				), true
			}
		}
		return TextReading{}, false
	case abstractdomain.KindList:
		// join(",") of each item's text; an undefined item joins as the
		// empty string (sec-array.prototype.join step for undefined).
		// Every item contributes its own reading's SET, so one item whose
		// text is wider than a single string widens the join instead of
		// voiding it: the pieces concatenate with the comma between them,
		// and the whole join is exact only when every piece is.
		pieces := make([][]float64, 0, len(known.Items))
		sets := make([]refinementsets.RefinedSet, 0, len(known.Items))
		hasExact := true
		grade := abstractdomain.TrustLevelOf(known)
		for _, item := range known.Items {
			if item.Kind == abstractdomain.KindUndef {
				pieces = append(pieces, []float64{})
				sets = append(sets, refinementsets.StringTuple(""))
				continue
			}
			inner, ok := TextOfKnown(decimal, item)
			if !ok {
				// an item with no text reading at all: the join's own text
				// is out of reach, since nothing bounds that position
				return TextReading{}, false
			}
			grade = abstractdomain.MinTrustLevel(grade, inner.Grade)
			if inner.HasExact {
				pieces = append(pieces, inner.Exact)
			} else {
				hasExact = false
				pieces = append(pieces, nil)
			}
			sets = append(sets, inner.Set)
		}
		comma := refinementsets.CodepointsOf(",")
		commaSet := refinementsets.StringTuple(",")
		if hasExact {
			var joined []float64
			for i, piece := range pieces {
				if i > 0 {
					joined = append(joined, comma...)
				}
				joined = append(joined, piece...)
			}
			return TextReading{
				Exact:    joined,
				HasExact: true,
				Set:      refinementsets.StringTuple(stringOf(joined)),
				Grade:    grade,
			}, true
		}
		if len(sets) == 0 {
			// an empty array joins to the empty string
			return exactText("", grade), true
		}
		// the pieces concatenate right-nested, commas between them —
		// the same shape a template literal's spans build
		joinedSet := sets[len(sets)-1]
		for i := len(sets) - 2; i >= 0; i-- {
			joinedSet = refinementsets.MakeRefinedSet(refinementsets.Concatenation(
				sets[i],
				refinementsets.MakeRefinedSet(refinementsets.Concatenation(commaSet, joinedSet)),
			))
		}
		return TextReading{Exact: nil, HasExact: false, Set: joinedSet, Grade: grade}, true
	case abstractdomain.KindPossiblyUndefined:
		inner, ok := TextOfKnown(decimal, *known.Inner)
		if !ok {
			return TextReading{}, false
		}
		// The wrapper's own absent side contributes its ToString word(s)
		// (sec-tostring steps 3-4) on top of Inner's own text — Inner
		// already carries "null" for the Inner=Null wrapper shape
		// (PossiblyAbsent's own normalization), so this arm only ever
		// adds the WRAPPER side's word. AbsentSide states which runtime
		// value the wrapper side is: NullOnly claims exactly "null",
		// UndefOnly exactly "undefined"; the zero value (conflated) is a
		// MAY-claim over both words, so a sound text set unions both —
		// narrowing it to one word would drop a runtime possibility.
		absentWord := unionOf(refinementsets.StringTuple("undefined"), refinementsets.StringTuple("null"))
		switch known.AbsentSide {
		case abstractdomain.AbsentFlavorNullOnly:
			absentWord = refinementsets.StringTuple("null")
		case abstractdomain.AbsentFlavorUndefOnly:
			absentWord = refinementsets.StringTuple("undefined")
		}
		return TextReading{
			Exact:    nil,
			HasExact: false,
			Set:      unionOf(inner.Set, absentWord),
			Grade:    abstractdomain.MinTrustLevel(inner.Grade, abstractdomain.TrustLevelOf(known)),
		}, true
	case abstractdomain.KindPossiblyNaN:
		inner, ok := TextOfKnown(decimal, *known.Inner)
		if !ok {
			return TextReading{}, false
		}
		return TextReading{
			Exact:    nil,
			HasExact: false,
			Set:      unionOf(inner.Set, refinementsets.StringTuple("NaN")),
			Grade:    abstractdomain.MinTrustLevel(inner.Grade, abstractdomain.TrustLevelOf(known)),
		}, true
	case abstractdomain.KindKindUnion:
		// a union's text is the SET of texts its arms spell, so an arm
		// whose own text is out of reach widens that arm to the widest
		// sound claim instead of voiding the union: every completing run
		// of ToString on it lands a STRING (sec-tostring). An arm that
		// can THROW instead of landing — a symbol (sec-tostring step 2)
		// or a bare-prototype object, which has no toString and no
		// valueOf to reach a primitive — has no text to widen to, and the
		// union stays out of reach.
		var set *refinementsets.RefinedSet
		grade := abstractdomain.TrustLevelOf(known)
		for _, arm := range known.Arms {
			inner, ok := TextOfKnown(decimal, arm)
			if !ok {
				if !textIsAlwaysAString(arm) {
					return TextReading{}, false
				}
				inner = TextReading{
					Exact:    nil,
					HasExact: false,
					Set:      refinementsets.Strings,
					Grade:    abstractdomain.MinTrustLevel(abstractdomain.TrustLevelOf(arm), abstractdomain.TrustSpec),
				}
			}
			grade = abstractdomain.MinTrustLevel(grade, inner.Grade)
			if set == nil {
				set = &inner.Set
			} else {
				combined := unionOf(*set, inner.Set)
				set = &combined
			}
		}
		if set == nil {
			return TextReading{}, false
		}
		return TextReading{Exact: nil, HasExact: false, Set: *set, Grade: grade}, true
	default:
		return TextReading{}, false
	}
}
