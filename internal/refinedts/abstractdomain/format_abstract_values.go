// How KNOWLEDGE reads — the checker's half of the display decisions.
// `refined_sets/display.ts` decides how a SET reads and is reachable
// from the runtime surface; `AbstractValue` is the checker's own type, so its
// rendering lives here and defers there for the sets inside it.
//
// One rule shapes all of it: where the TYPE LANGUAGE can say it, the
// rendering IS the type. An exact number renders as `7`, a string as
// `"Foo"`, an exact sequence as `[1, 2, 3]` — the plugin puts that
// after the colon, so the hover reads `const total: 7`, exactly like
// tsc's own narrowing display. Braces are kept for what the type
// language cannot say: a set, an object's known values, NaN, absence.
// "" (the TS source's null) means the rendering adds nothing the host
// type does not already carry, and silence is the right answer there.
//
// TWO POSITIONS, not two audiences. `FormatAbstractValue` renders at the
// top of a hover; `FormatAbstractValueInline` renders nested inside a
// larger one — an object's key, a sequence's element — where braces
// around every part would drown the shape it is trying to show.
//
// Every position speaks the hover vocabulary: a set nested inside an
// object renders with the same words as a set at the top, braces
// dropped because the shape around it supplies them. A key the
// checker knows nothing about says "unknown" — never a bare `?`.
//
// formatObjectAnnotation and formatKeyValue (the TS source) are NOT
// ported: both read annotations.ObjectAnnotation
// (annotations/declared_refinement.ts), which is out of this
// directory's import set (annotations is "pending (wave 3)" per
// go-port-tracker.md, and depends on abstract_domain — importing it here
// would invert PORT.md's dependency order). Every function below that
// operates on AbstractValue alone (which never carries ObjectAnnotation
// data directly — Stated is an opaque identity handle, never read here)
// is ported in full.

package abstractdomain

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// FormatWordedSet is formatWordedSet in the TS source: a stated set
// spoken by its surface WORD where one rides the annotation — `{uuid}`,
// `{sha256 hash}`, `{url, runtime-checked}` — and by the set's own words
// anywhere else. The word covers a leading prefix of the forms; forms
// stacked after it display beside it, and an unread statement says so in
// the same breath.
//
// hasWord takes the place of TS's optional `word` field being absent.
func FormatWordedSet(set refinementsets.RefinedSet, wordText string, wordCovers int, hasWord bool, unread bool) (string, bool) {
	if !hasWord {
		return refinementsets.FormatForHover(set)
	}
	extra := set.Forms[min(wordCovers, len(set.Forms)):]
	parts := []string{wordText}
	if len(extra) > 0 {
		shown, ok := refinementsets.FormatForHover(refinementsets.MakeRefinedSet(extra...))
		if !ok {
			return refinementsets.FormatForHover(set)
		}
		parts = append(parts, bareOf(shown))
	}
	if unread {
		parts = append(parts, "runtime-checked")
	}
	return "{" + strings.Join(parts, ", ") + "}", true
}

// bareOf strips a leading "{" and trailing "}" if both are present —
// the TS source's repeated `shown.startsWith("{") ? shown.slice(1, -1) :
// shown` idiom, pulled into one helper (still 1:1 in effect at each call
// site).
func bareOf(shown string) string {
	if strings.HasPrefix(shown, "{") && strings.HasSuffix(shown, "}") {
		return shown[1 : len(shown)-1]
	}
	return shown
}

func stringOf(codepoints []float64) string {
	var b strings.Builder
	for _, c := range codepoints {
		b.WriteRune(rune(int32(c)))
	}
	return b.String()
}

// formatKindTaggedSet is formatKindTaggedSet in the TS source: the words
// of a set wearing a non-double sort, or "" (the TS source's null) for
// an ordinary set. A symbol statement's whole claim is its sort; a
// bigint statement says its sort and then its windows — integrality is
// the sort's own fact.
func formatKindTaggedSet(known AbstractValue) (string, bool) {
	if known.SetKindTag == SetKindTagNone {
		return "", false
	}
	if known.SetKindTag == SetKindTagSymbol {
		return "symbol", true
	}
	var bare []refinementsets.Refinement
	for _, f := range known.Set.Forms {
		if f.Form != refinementsets.FormInteger {
			bare = append(bare, f)
		}
	}
	if len(bare) == 0 {
		return "bigint", true
	}
	shown, ok := refinementsets.FormatForHover(refinementsets.MakeRefinedSet(bare...))
	if !ok {
		return "bigint", true
	}
	return "bigint, " + bareOf(shown), true
}

// FormatAbstractValue is formatAbstractValue in the TS source: a
// knowledge state rendered at the top of a hover.
func FormatAbstractValue(known AbstractValue) (string, bool) {
	return formatAbstractValueAt(known, true)
}

// FormatAbstractValueInline is formatAbstractValueInline in the TS
// source: the compact rendering used inside a larger one — an object's
// key, a sequence's element. No braces of its own: the shape around it
// supplies them.
func FormatAbstractValueInline(known AbstractValue) (string, bool) {
	return formatAbstractValueAt(known, false)
}

// inlineOrUnknown is the TS source's repeated
// `formatAbstractValueAt(x, "inline") ?? "unknown"` idiom.
func inlineOrUnknown(known AbstractValue) string {
	s, ok := formatAbstractValueAt(known, false)
	if !ok {
		return "unknown"
	}
	return s
}

// formatAbstractValueAt is the TS source's formatAbstractValueAt: ONE
// switch for both positions. The self-delimited shapes (an object, a
// sequence, a Map, a Date) render identically everywhere; the brace-
// carried shapes (a set, a tuple word, absence, NaN, the wrappers) wear
// braces at the top and go bare inline, where the surrounding shape
// supplies its own.
func formatAbstractValueAt(known AbstractValue, top bool) (string, bool) {
	switch known.Kind {
	case KindValues:
		if known.KindTag == PrimitiveString {
			return jsonQuoteString(stringOf(known.Values)), true
		}
		if known.KindTag == PrimitiveArray {
			return "[" + joinFloats(known.Values, ", ") + "]", true
		}
		if known.KindTag == PrimitiveBoolean {
			if len(known.Values) == 0 {
				return "", false
			}
			words := make([]string, len(known.Values))
			for i, v := range known.Values {
				words[i] = strconv.FormatBool(v != 0)
			}
			joined := strings.Join(words, " | ")
			if top && len(known.Values) > 1 {
				return "{" + joined + "}", true
			}
			return joined, true
		}
		// a number: the literal type; a multi-value word is a tuple
		if len(known.Values) == 1 {
			return formatJSNumberLiteral(known.Values[0]), true
		}
		tuple := "(" + joinFloats(known.Values, ", ") + ")"
		if top {
			return "{" + tuple + "}", true
		}
		return tuple, true

	case KindSet:
		worn, wornOK := formatKindTaggedSet(known)
		if wornOK {
			if top {
				return "{" + worn + "}", true
			}
			if strings.Contains(worn, ", ") {
				return "(" + worn + ")", true
			}
			return worn, true
		}
		shown, shownOK := refinementsets.FormatForHover(known.Set)
		if top {
			// elements that may include NaN say so — the set alone would
			// overclaim
			if known.NaNElements && shownOK {
				if strings.HasPrefix(shown, "{") {
					return "{" + bareOf(shown) + ", or NaN elements}", true
				}
				return shown + ", or NaN elements", true
			}
			return shown, shownOK
		}
		// beside a host type line, the set of all strings adds nothing;
		// nested inside a list or object there is no such line, so the
		// sort word beats "unknown"
		if !shownOK {
			if refinementsets.IsStrings(known.Set) {
				return "string", true
			}
			return "", false
		}
		bare := bareOf(shown)
		said := bare
		if known.NaNElements {
			said = bare + ", or NaN elements"
		}
		if strings.Contains(said, ", ") {
			return "(" + said + ")", true
		}
		return said, true

	case KindObject:
		var keys []string
		for _, key := range known.Keys {
			keys = append(keys, key.Name+": "+inlineOrUnknown(key.Value))
		}
		if len(keys) == 0 {
			return "", false
		}
		return "{" + strings.Join(keys, ", ") + "}", true

	case KindList:
		var items []string
		for _, item := range known.Items {
			items = append(items, inlineOrUnknown(item))
		}
		return "[" + strings.Join(items, ", ") + "]", true

	case KindObjectStar:
		// the element, then `[]` — the type language's own way of saying
		// "a sequence of these", and it says exactly what the form claims:
		// the element at each position, no count. Self-delimited by the
		// brackets, so it reads the same at both positions.
		if known.Inner == nil {
			return "", false
		}
		element, ok := formatAbstractValueAt(*known.Inner, false)
		if !ok {
			return "", false
		}
		return element + "[]", true

	case KindArrayHoles:
		// the exact length wrapped in Inner — the same {n} scalar
		// .length itself reads — spelled the way a hole array's own
		// claim reads: every slot absent
		length, ok := LengthOfArrayHoles(known)
		if !ok {
			return "", false
		}
		return "Array(" + strconv.Itoa(length) + ")", true

	case KindCollection:
		var entries []string
		for _, e := range known.Entries {
			if known.CollectionFlavor == FlavorMap {
				entries = append(entries, inlineOrUnknown(e.Key)+" → "+inlineOrUnknown(e.Value))
			} else {
				entries = append(entries, inlineOrUnknown(e.Key))
			}
		}
		name := "Set"
		if known.CollectionFlavor == FlavorMap {
			name = "Map"
		}
		trailer := ""
		if !known.Complete {
			trailer = ", …"
		}
		return name + " {" + strings.Join(entries, ", ") + trailer + "}", true

	case KindPromise:
		inner, ok := formatAbstractValueAt(*known.Inner, false)
		if !ok {
			return "", false
		}
		return "Promise {" + inner + "}", true

	case KindDate:
		// an exact time value reads as its ISO spelling — the host
		// computes the spec-exact rendering of a known millis
		if known.Millis.Kind == KindValues && len(known.Millis.Values) == 1 {
			millis := known.Millis.Values[0]
			if iso, ok := isoStringOfMillis(millis); ok {
				return "Date {" + iso + "}", true
			}
			return "", false
		}
		inner, ok := formatAbstractValueAt(*known.Millis, false)
		if !ok {
			return "", false
		}
		return "Date {time value " + inner + "}", true

	case KindSymbol:
		if known.HasSymbolKey {
			return "Symbol.for(" + jsonQuoteString(known.SymbolKey) + ")", true
		}
		if known.HasDescription {
			return "Symbol(" + jsonQuoteString(known.Description) + ")", true
		}
		return "Symbol()", true

	case KindBigints:
		// exact integers, spelled the way the program spells them
		if len(known.BigintValues) == 0 {
			return "", false
		}
		words := make([]string, len(known.BigintValues))
		for i, v := range known.BigintValues {
			words[i] = strconv.FormatInt(v, 10) + "n"
		}
		joined := strings.Join(words, " | ")
		if top {
			return "{" + joined + "}", true
		}
		return joined, true

	case KindRegex:
		return "/" + known.Source + "/" + known.Flags, true

	case KindHostFunction:
		if top {
			return "{a function}", true
		}
		return "a function", true

	case KindUndef:
		if top {
			return "{absent}", true
		}
		return "absent", true

	case KindNaN:
		if top {
			return "{NaN}", true
		}
		return "NaN", true

	case KindPossiblyUndefined:
		inner, ok := formatAbstractValueAt(*known.Inner, top)
		if !ok {
			return "", false
		}
		if !top {
			return inner + ", or absent", true
		}
		return "{" + bareOf(inner) + ", or absent}", true

	case KindPossiblyNaN:
		inner, ok := formatAbstractValueAt(*known.Inner, top)
		if !ok {
			return "", false
		}
		if !top {
			return inner + ", or NaN", true
		}
		return "{" + bareOf(inner) + ", or NaN}", true

	case KindKindUnion:
		// the arms, side by side — the runtime value is one of them
		arms := make([]string, len(known.Arms))
		for i, arm := range known.Arms {
			s, ok := formatAbstractValueAt(arm, false)
			if !ok {
				return "", false
			}
			arms[i] = s
		}
		if top {
			return "{" + strings.Join(arms, " | ") + "}", true
		}
		return "(" + strings.Join(arms, " | ") + ")", true

	case KindVariable, KindUnknown:
		return "", false

	default:
		return "", false
	}
}

// joinFloats renders each value the way TS's `Array.prototype.join`
// renders numbers — no trailing ".0", no scientific-notation surprises
// for the ordinary integers this domain carries as exact tuple/array
// elements.
func joinFloats(values []float64, sep string) string {
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = formatJSNumberLiteral(v)
	}
	return strings.Join(parts, sep)
}

// formatJSNumberLiteral renders a float64 the way a JS numeric literal
// spells it (no trailing ".0" for whole numbers) — the plain
// `${known.values[0]}` / `${v}` template-literal coercions in the TS
// source.
func formatJSNumberLiteral(v float64) string {
	if v == float64(int64(v)) {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// jsonQuoteString is JSON.stringify(s) restricted to strings — the TS
// source's `JSON.stringify` calls on plain strings.
func jsonQuoteString(s string) string {
	out, err := json.Marshal(s)
	if err != nil {
		// json.Marshal on a Go string only fails for invalid UTF-8, which
		// cannot arise from codepoints already validated as ℝ̄ elements
		// or from a symbol's description string built by the checker
		// itself
		panic(err)
	}
	return string(out)
}

// isoStringOfMillis is `new Date(millis).toISOString()` restricted to
// the finite-and-in-range case the TS source's try/catch guards
// (`toISOString` throws RangeError outside ±8,640,000,000,000,000ms from
// the epoch, or on a NaN time value).
func isoStringOfMillis(millis float64) (string, bool) {
	const maxMillis = 8640000000000000
	if millis != millis || millis > maxMillis || millis < -maxMillis {
		return "", false
	}
	sec := int64(millis) / 1000
	nsec := (int64(millis) % 1000) * int64(time.Millisecond)
	if nsec < 0 {
		nsec += int64(time.Second)
		sec--
	}
	t := time.Unix(sec, nsec).UTC()
	return t.Format("2006-01-02T15:04:05.000Z"), true
}
