// Memo-key spelling of an AbstractValue — what the inline replay
// cache keys on. The TS source keyed on JSON.stringify; this speller
// writes one flat string instead, for two reasons measured on the
// recharts corpus: encoding/json refuses NaN and ±Inf outright (the
// bare z.number() set is atLeast(-Inf), so refusal was the COMMON
// case, silently unkeying the memo), and the map-plus-Marshal DTO
// paid an allocation storm on every inline call — hits included
// (Sankey spelled ~6,300 environments per check). Field order is
// fixed, arbitrary strings are quoted, and non-finite floats spell
// as their words — deterministic, and distinct knowledge never
// collides. Symbols and stated annotations spell by pointer identity
// within a check, matching SameKnown's discrimination.

package abstractdomain

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// SpellForMemoKey is the deterministic key fragment for one AbstractValue.
// ("", false) where knowledge cannot be spelled safely — the caller
// leaves that inline unmemoized rather than risk a wrong hit.
func SpellForMemoKey(known AbstractValue) (string, bool) {
	var b strings.Builder
	if !spellInto(&b, known) {
		return "", false
	}
	return b.String(), true
}

// spellFloat writes a float64 including the non-finite values JSON
// cannot carry: NaN, +Inf, -Inf spell as those words.
func spellFloat(b *strings.Builder, x float64) {
	b.WriteString(strconv.FormatFloat(x, 'g', -1, 64))
}

func spellFloats(b *strings.Builder, xs []float64) {
	for i, x := range xs {
		if i > 0 {
			b.WriteByte(',')
		}
		spellFloat(b, x)
	}
}

// spellSet writes the RefinedSet grammar in fixed field order — the
// rays carry ±Inf bounds, which is why this never rides json.Marshal.
func spellSet(b *strings.Builder, set refinementsets.RefinedSet) {
	b.WriteByte('[')
	for i, form := range set.Forms {
		if i > 0 {
			b.WriteByte('|')
		}
		b.WriteString(string(form.Form))
		b.WriteByte(':')
		spellFloat(b, form.A)
		b.WriteByte(':')
		spellFloats(b, form.W)
		b.WriteByte(':')
		if form.A_ != nil {
			spellSet(b, *form.A_)
		}
		b.WriteByte(':')
		if form.B != nil {
			spellSet(b, *form.B)
		}
		b.WriteByte(':')
		b.WriteString(strconv.Itoa(form.Lo))
		b.WriteByte(':')
		if form.Hi != nil {
			b.WriteString(strconv.Itoa(*form.Hi))
		}
	}
	b.WriteByte(']')
}

// spellInto writes one AbstractValue. False where an unknown kind
// refuses a spelling rather than inventing one.
func spellInto(b *strings.Builder, known AbstractValue) bool {
	switch known.Kind {
	case KindUnknown:
		b.WriteString("u(")
		b.WriteString(strconv.FormatBool(known.Opaque))
		b.WriteByte(')')
	case KindUndef:
		b.WriteString("undef")
	case KindNull:
		b.WriteString("null")
	case KindNaN:
		b.WriteString("nan")
	case KindValues:
		b.WriteString("v(")
		spellFloats(b, known.Values)
		b.WriteByte(';')
		b.WriteString(string(known.KindTag))
		b.WriteByte(';')
		b.WriteString(string(known.Grade))
		b.WriteByte(')')
	case KindBigints:
		b.WriteString("big(")
		for i, x := range known.BigintValues {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(x.String())
		}
		b.WriteByte(';')
		b.WriteString(string(known.Grade))
		b.WriteByte(')')
	case KindSet:
		b.WriteString("s(")
		spellSet(b, known.Set)
		b.WriteByte(';')
		b.WriteString(string(known.SetKindTag))
		b.WriteByte(';')
		b.WriteString(strconv.FormatBool(known.NaNElements))
		b.WriteByte(';')
		b.WriteString(string(known.Grade))
		if known.Temporal != nil {
			b.WriteString(";t(")
			b.WriteString(string(known.Temporal.Chart))
			b.WriteByte(';')
			b.WriteString(strconv.FormatBool(known.Temporal.HasMin))
			b.WriteString(strconv.Quote(known.Temporal.Min))
			b.WriteByte(';')
			b.WriteString(strconv.FormatBool(known.Temporal.HasMax))
			b.WriteString(strconv.Quote(known.Temporal.Max))
			b.WriteByte(')')
		}
		if known.Measures != nil {
			b.WriteString(";m(")
			spellFloat(b, known.Measures.Sum)
			b.WriteByte(';')
			b.WriteString(strconv.FormatBool(known.Measures.HasSum))
			b.WriteByte(';')
			b.WriteString(strconv.FormatBool(known.Measures.Sorted))
			b.WriteByte(')')
		}
		b.WriteByte(')')
	case KindObject:
		b.WriteString("o(")
		for i, key := range known.Keys {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(strconv.Quote(key.Name))
			b.WriteByte('=')
			if !spellInto(b, key.Value) {
				return false
			}
		}
		b.WriteByte(';')
		b.WriteString(strconv.FormatBool(known.Complete))
		b.WriteByte(';')
		b.WriteString(strconv.FormatBool(known.BareProto))
		b.WriteByte(';')
		b.WriteString(strconv.FormatBool(known.MaybeArray))
		b.WriteByte(';')
		fmt.Fprintf(b, "%p", known.Stated)
		b.WriteByte(';')
		for i, variant := range known.Variants {
			if i > 0 {
				b.WriteByte(',')
			}
			if !spellInto(b, variant) {
				return false
			}
		}
		b.WriteByte(';')
		b.WriteString(string(known.Grade))
		b.WriteByte(')')
	case KindObjectStar:
		// the element is the whole claim, so the element's spelling plus
		// the grade is the whole key — there is no length to write
		b.WriteString("os(")
		if known.Inner == nil {
			return false
		}
		if !spellInto(b, *known.Inner) {
			return false
		}
		b.WriteByte(';')
		b.WriteString(string(known.Grade))
		b.WriteByte(')')
	case KindArrayHoles:
		// the length (wrapped in Inner) is the whole claim beside the
		// (always-∅) element set — there is no per-slot knowledge to
		// write, unlike KindList. Dense/DenseKnown must key the memo
		// too: a dense and a sparse array-holes of the same length
		// answer Object.keys differently (object_static_models.go), so
		// collapsing them to the same key would serve one call's cached
		// answer to the other.
		length, ok := LengthOfArrayHoles(known)
		if !ok {
			return false
		}
		b.WriteString("ah(")
		b.WriteString(strconv.Itoa(length))
		b.WriteByte(';')
		b.WriteString(string(known.Grade))
		b.WriteByte(';')
		b.WriteString(strconv.FormatBool(known.DenseKnown))
		b.WriteByte(';')
		b.WriteString(strconv.FormatBool(known.Dense))
		b.WriteByte(')')
	case KindVariable:
		b.WriteString("var(")
		fmt.Fprintf(b, "%p", known.Symbol)
		b.WriteByte(';')
		spellSet(b, known.Bound)
		b.WriteByte(';')
		b.WriteString(strconv.Itoa(known.StarDepth))
		b.WriteByte(';')
		fmt.Fprintf(b, "%p", known.BoundObject)
		b.WriteByte(')')
	case KindList:
		b.WriteString("l(")
		for i, item := range known.Items {
			if i > 0 {
				b.WriteByte(',')
			}
			if !spellInto(b, item) {
				return false
			}
		}
		// a list's own NON-INDEX properties (a match array's `groups`)
		// key the memo too: two lists with the same slots but different
		// named properties answer a property read differently, so
		// collapsing them to one key would serve one call's cached
		// answer to the other.
		b.WriteByte(';')
		for i, key := range known.Keys {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(strconv.Quote(key.Name))
			b.WriteByte('=')
			if !spellInto(b, key.Value) {
				return false
			}
		}
		b.WriteByte(';')
		b.WriteString(string(known.Grade))
		b.WriteByte(')')
	case KindCollection:
		b.WriteString("c(")
		b.WriteString(string(known.CollectionFlavor))
		b.WriteByte(';')
		b.WriteString(strconv.FormatBool(known.Complete))
		b.WriteByte(';')
		for i, entry := range known.Entries {
			if i > 0 {
				b.WriteByte(',')
			}
			if !spellInto(b, entry.Key) {
				return false
			}
			b.WriteByte('>')
			if !spellInto(b, entry.Value) {
				return false
			}
		}
		b.WriteByte(';')
		b.WriteString(string(known.Grade))
		b.WriteByte(')')
	case KindPromise, KindPossiblyUndefined, KindPossiblyNaN:
		if known.Inner == nil {
			return false
		}
		b.WriteString(string(known.Kind))
		b.WriteByte('(')
		if !spellInto(b, *known.Inner) {
			return false
		}
		if known.Kind == KindPossiblyUndefined {
			b.WriteByte(';')
			b.WriteString(strconv.FormatBool(known.ProvedAbsent))
			b.WriteByte(';')
			// the flavor keys the memo too: an UndefOnly and a NullOnly
			// wrapper around the same inner value are DIFFERENT knowledge
			// (a later `=== undefined` guard decides them differently) and
			// must not share a cache hit
			b.WriteString(string(known.AbsentSide))
		}
		b.WriteByte(';')
		b.WriteString(string(known.Grade))
		b.WriteByte(')')
	case KindDate:
		b.WriteString("d(")
		if known.Millis != nil {
			if !spellInto(b, *known.Millis) {
				return false
			}
		}
		b.WriteByte(';')
		b.WriteString(string(known.Grade))
		b.WriteByte(')')
	case KindSymbol:
		b.WriteString("sym(")
		b.WriteString(strconv.FormatBool(known.HasSymbolKey))
		b.WriteString(strconv.Quote(known.SymbolKey))
		b.WriteByte(';')
		b.WriteString(strconv.FormatBool(known.HasDescription))
		b.WriteString(strconv.Quote(known.Description))
		b.WriteByte(';')
		b.WriteString(string(known.Grade))
		b.WriteByte(')')
	case KindRegex:
		b.WriteString("re(")
		b.WriteString(strconv.Quote(known.Source))
		b.WriteByte(';')
		b.WriteString(strconv.Quote(known.Flags))
		b.WriteByte(';')
		b.WriteString(string(known.Grade))
		b.WriteByte(')')
	case KindHostFunction:
		b.WriteString("hf(")
		b.WriteString(string(known.Grade))
		b.WriteByte(')')
	case KindKindUnion:
		b.WriteString("ku(")
		for i, arm := range known.Arms {
			if i > 0 {
				b.WriteByte(',')
			}
			if !spellInto(b, arm) {
				return false
			}
		}
		b.WriteByte(';')
		b.WriteString(string(known.Grade))
		b.WriteByte(')')
	default:
		// refuse rather than invent a spelling for an unknown kind
		return false
	}
	return true
}
