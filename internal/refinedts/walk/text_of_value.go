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
		// the marker conflates undefined with null — two words, so the
		// text is the two-word set, never one exact spelling
		return TextReading{
			Exact:    nil,
			HasExact: false,
			Set:      unionOf(refinementsets.StringTuple("undefined"), refinementsets.StringTuple("null")),
			Grade:    abstractdomain.TrustLevelOf(known),
		}, true
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
				Set:      refinementsets.Strings,
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
		// empty string (sec-array.prototype.join step for undefined)
		pieces := make([][]float64, 0, len(known.Items))
		grade := abstractdomain.TrustLevelOf(known)
		for _, item := range known.Items {
			if item.Kind == abstractdomain.KindUndef {
				pieces = append(pieces, []float64{})
				continue
			}
			inner, ok := TextOfKnown(decimal, item)
			if !ok || !inner.HasExact {
				return TextReading{}, false
			}
			grade = abstractdomain.MinTrustLevel(grade, inner.Grade)
			pieces = append(pieces, inner.Exact)
		}
		comma := refinementsets.CodepointsOf(",")
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
	case abstractdomain.KindPossiblyUndefined:
		inner, ok := TextOfKnown(decimal, *known.Inner)
		if !ok {
			return TextReading{}, false
		}
		return TextReading{
			Exact:    nil,
			HasExact: false,
			Set:      unionOf(inner.Set, refinementsets.StringTuple("undefined")),
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
		var set *refinementsets.RefinedSet
		grade := abstractdomain.TrustLevelOf(known)
		for _, arm := range known.Arms {
			inner, ok := TextOfKnown(decimal, arm)
			if !ok {
				return TextReading{}, false
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
