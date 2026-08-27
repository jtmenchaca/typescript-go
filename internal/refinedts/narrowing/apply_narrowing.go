// Applying narrowing effects to abstract values: narrowAt walks
// the tested path, shape effects meet without displacing sharper
// facts, truthiness filters finite word lists, and a refutation
// never narrows an unknown binding (the NaN smuggle). Split from
// condition_analysis.ts per the v2 tree.

package narrowing

import (
	"math"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// ApplyNarrowed is applyNarrowed in the TS source: apply one narrowing
// to what is known — refusing the NaN smuggle: a refutation narrows
// nothing about an unknown binding. Where the narrowing names a key, it
// rebuilds the object around that key.
func ApplyNarrowed(known abstractdomain.AbstractValue, n Narrowed) abstractdomain.AbstractValue {
	return narrowAt(known, n.Path, n)
}

// ShiftedNarrowed moves a narrowing along the line by a constant: the
// claim proved of a place becomes the claim of `place + shift`, which
// is what a `const off = place - k` binding holds. Answers
// (Narrowed{}, false) where the displacement cannot be stated exactly —
// and then the caller states nothing, which is always sound.
//
// EXACTNESS is the whole gate. A window carried across a shift is only
// the window it claims to be when each moved endpoint is the exact real
// sum; where the double addition rounds, the moved endpoint names a
// different number than the runtime subtraction produces, and a claim
// built on it could exclude a value the run actually takes. So each
// endpoint is moved and then CHECKED by moving it back: only a
// round-trip that returns the original number is exact, and any endpoint
// failing it refuses the whole narrowing rather than weakening one form.
//
// Only the ORDER forms and exact tuples move. `integer` is preserved
// only for an integral shift (an integer plus an integer is an integer;
// a fractional shift makes it false). Everything else — multiples,
// sequence and pattern forms, shape and word claims — states nothing
// under displacement here and refuses.
func ShiftedNarrowed(n Narrowed, shift float64) (Narrowed, bool) {
	if shift == 0 {
		return n, true
	}
	if math.IsNaN(shift) || math.IsInf(shift, 0) {
		return Narrowed{}, false
	}
	// only a pure SET claim displaces: definedness, truthiness, shapes,
	// words and kinds all say nothing about a shifted number
	if n.Definedness != "" || n.Truthiness != "" || n.ExcludesKind != "" ||
		n.HasShape || n.HasWordSet || n.HasWordSetExcluded ||
		n.HasExcludesBooleanWord || n.KeepAbsent || n.SequenceBrand || n.RefutedBrand != "" {
		return Narrowed{}, false
	}
	if n.ExactSort != "" && n.ExactSort != abstractdomain.PrimitiveNumber {
		return Narrowed{}, false
	}
	// moved is the exact displacement of x, or not-exact
	moved := func(x float64) (float64, bool) {
		if math.IsInf(x, 0) {
			// an infinite endpoint states no bound at all; it is its own
			// displacement and no rounding can happen to it
			return x, true
		}
		y := x + shift
		if math.IsNaN(y) || math.IsInf(y, 0) {
			return 0, false
		}
		// the round trip: exact addition is invertible, a rounded one is not
		if y-shift != x {
			return 0, false
		}
		return y, true
	}
	out := n
	out.Forms = nil
	for _, form := range n.Forms {
		switch form.Form {
		case refinementsets.FormAtLeast, refinementsets.FormAbove,
			refinementsets.FormAtMost, refinementsets.FormBelow:
			a, ok := moved(form.A)
			if !ok {
				return Narrowed{}, false
			}
			shiftedForm := form
			shiftedForm.A = a
			out.Forms = append(out.Forms, shiftedForm)
		case refinementsets.FormInteger:
			// an integer displaced by an integer is an integer; by anything
			// else the claim is simply false and cannot be carried
			if shift != math.Trunc(shift) {
				return Narrowed{}, false
			}
			out.Forms = append(out.Forms, form)
		case refinementsets.FormOneOf:
			words := make([]float64, 0, len(form.W))
			for _, w := range form.W {
				y, ok := moved(w)
				if !ok {
					return Narrowed{}, false
				}
				words = append(words, y)
			}
			shiftedForm := form
			shiftedForm.W = words
			out.Forms = append(out.Forms, shiftedForm)
		default:
			return Narrowed{}, false
		}
	}
	if n.Exact != nil {
		exact := make([]float64, 0, len(n.Exact))
		for _, v := range n.Exact {
			y, ok := moved(v)
			if !ok {
				return Narrowed{}, false
			}
			exact = append(exact, y)
		}
		out.Exact = exact
	}
	return out, true
}

// keepTruthy keeps only the truthy words of a finite list; NaN leaves
// through the wrapper (ToBoolean of NaN is false, so held truth
// refutes it). Shapes without a finite word list pass untouched.
func keepTruthy(known abstractdomain.AbstractValue) abstractdomain.AbstractValue {
	if known.Kind == abstractdomain.KindPossiblyNaN {
		return keepTruthy(*known.Inner)
	}
	if known.Kind == abstractdomain.KindValues &&
		(known.KindTag == abstractdomain.PrimitiveNumber || known.KindTag == abstractdomain.PrimitiveBoolean) {
		var kept []float64
		for _, v := range known.Values {
			if v != 0 {
				kept = append(kept, v)
			}
		}
		return abstractdomain.KnownValues(kept, known.KindTag, abstractdomain.TrustLevelOf(known))
	}
	return known
}

// dropBooleanWord sheds one word from a BOOLEAN-sorted value: `b !==
// false` on a `b: boolean` leaves exactly {true}.
//
// Only a KindValues tagged PrimitiveBoolean is touched. Strict
// inequality with a boolean literal holds for every non-boolean value
// too, so on any other shape the refutation states nothing and the
// value passes whole — no claim, no discard. An emptied list is left
// alone rather than claimed empty: proving the branch dead is the
// caller's machinery, not this filter's.
func dropBooleanWord(known abstractdomain.AbstractValue, word float64) abstractdomain.AbstractValue {
	if known.Kind == abstractdomain.KindPossiblyUndefined {
		// falsity proves no presence, so the maybe wrapper stays — the
		// same AbsentSide-preserving rebuild excludeKind keeps.
		return abstractdomain.PossiblyAbsent(dropBooleanWord(*known.Inner, word), known.AbsentSide, "", false, known.ProvedAbsent)
	}
	if known.Kind != abstractdomain.KindValues || known.KindTag != abstractdomain.PrimitiveBoolean {
		return known
	}
	var kept []float64
	for _, v := range known.Values {
		if v != word {
			kept = append(kept, v)
		}
	}
	if len(kept) == 0 || len(kept) == len(known.Values) {
		return known
	}
	return abstractdomain.KnownValues(kept, known.KindTag, abstractdomain.TrustLevelOf(known))
}

// keepFalsy keeps only the falsy words; absence and NaN both stay —
// each is false under ToBoolean.
func keepFalsy(known abstractdomain.AbstractValue) abstractdomain.AbstractValue {
	if known.Kind == abstractdomain.KindValues &&
		(known.KindTag == abstractdomain.PrimitiveNumber || known.KindTag == abstractdomain.PrimitiveBoolean) {
		var kept []float64
		for _, v := range known.Values {
			if v == 0 {
				kept = append(kept, v)
			}
		}
		return abstractdomain.KnownValues(kept, known.KindTag, abstractdomain.TrustLevelOf(known))
	}
	return known
}

// variantConsistent is whether one variant's held knowledge can coexist
// with the test that HELD — a variant it contradicts is not the runtime
// shape. Unreadable combinations answer true: no claim, no discard.
func variantConsistent(variant abstractdomain.AbstractValue, path []string, n Narrowed) bool {
	if variant.Kind != abstractdomain.KindObject {
		return true
	}
	key, rest := path[0], path[1:]
	held, foundHeld := objectKeyValue(variant, key)
	if !foundHeld {
		// a complete variant without the key holds it absent
		return !variant.Complete || consistentAtLeaf(abstractdomain.Undef, n)
	}
	if len(rest) > 0 {
		return variantConsistent(held, rest, n)
	}
	return consistentAtLeaf(held, n)
}

// objectKeyValue reads an object AbstractValue's key by name — the
// ordered-slice equivalent of the TS record's `keys[key]`.
func objectKeyValue(obj abstractdomain.AbstractValue, key string) (abstractdomain.AbstractValue, bool) {
	for _, k := range obj.Keys {
		if k.Name == key {
			return k.Value, true
		}
	}
	return abstractdomain.AbstractValue{}, false
}

func consistentAtLeaf(held abstractdomain.AbstractValue, n Narrowed) bool {
	present := held
	if held.Kind == abstractdomain.KindPossiblyUndefined {
		present = *held.Inner
	}
	var words []float64
	hasWords := present.Kind == abstractdomain.KindValues &&
		(present.KindTag == abstractdomain.PrimitiveNumber || present.KindTag == abstractdomain.PrimitiveBoolean)
	if hasWords {
		words = present.Values
	}
	if n.Definedness == "defined" || n.Truthiness == "truthy" {
		if held.Kind == abstractdomain.KindUndef {
			return false
		}
		if n.Truthiness == "truthy" && hasWords {
			for _, v := range words {
				if v != 0 {
					return true
				}
			}
			return false
		}
		return true
	}
	if n.Definedness == "undefined" {
		return held.Kind == abstractdomain.KindUndef || held.Kind == abstractdomain.KindPossiblyUndefined ||
			held.Kind == abstractdomain.KindUnknown
	}
	if n.Truthiness == "falsy" {
		if held.Kind == abstractdomain.KindUndef || held.Kind == abstractdomain.KindPossiblyUndefined {
			return true
		}
		if hasWords {
			for _, v := range words {
				if v == 0 {
					return true
				}
			}
			return false
		}
		if present.Kind == abstractdomain.KindObject || present.Kind == abstractdomain.KindList ||
			present.Kind == abstractdomain.KindCollection {
			return false // objects are always truthy
		}
		return true
	}
	// a refuted boolean equality: a variant whose key is EXACTLY the
	// excluded word cannot be the runtime shape. Every other holding —
	// a two-word boolean, a non-boolean sort — stays consistent.
	if n.HasExcludesBooleanWord {
		if present.Kind == abstractdomain.KindValues && present.KindTag == abstractdomain.PrimitiveBoolean {
			for _, v := range present.Values {
				if v != n.ExcludesBooleanWord {
					return true
				}
			}
			return false
		}
		return true
	}
	exactSort := n.ExactSort
	if exactSort == "" {
		exactSort = abstractdomain.PrimitiveNumber
	}
	if n.Exact != nil && hasWords && exactSort != abstractdomain.PrimitiveString {
		for _, v := range words {
			if floatsInclude(n.Exact, v) {
				return true
			}
		}
		return false
	}
	return true
}

func floatsInclude(xs []float64, v float64) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// meetShape meets what is held with the shape a type test proved. An
// unknown takes the shape whole; an object gains the keys it lacked;
// every sharper fact stands, because the shape is the weakest thing the
// test proves. A COMPLETE object is left alone — its key set is already
// a theorem, and `in` walks the prototype chain.
func meetShape(held, shape abstractdomain.AbstractValue) abstractdomain.AbstractValue {
	// holding proves presence: typeof, instanceof, and `in` all answer
	// false (or "undefined") for the absent value
	present := held
	if held.Kind == abstractdomain.KindPossiblyUndefined {
		present = *held.Inner
	}
	// a SORT UNION keeps the arms the proved shape's sort names — a
	// held `typeof v === "string"` discards the number arm outright
	// (number and boolean stay bucketed together: their words share
	// the ground)
	if present.Kind == abstractdomain.KindKindUnion {
		wanted := abstractdomain.KindOfClaim(shape)
		if wanted == abstractdomain.ClaimSortNone {
			return present
		}
		same := func(x abstractdomain.ClaimSort) bool {
			if x == wanted {
				return true
			}
			return x != abstractdomain.ClaimSortNone &&
				(x == abstractdomain.ClaimSortNumber || x == abstractdomain.ClaimSortBoolean) &&
				(wanted == abstractdomain.ClaimSortNumber || wanted == abstractdomain.ClaimSortBoolean)
		}
		var kept []abstractdomain.AbstractValue
		for _, arm := range present.Arms {
			s := abstractdomain.KindOfClaim(arm)
			// a SEQUENCE-SHAPED SET denotes a string OR an array — the
			// same tuple encoding — so a "string"-classified set arm
			// survives an object want: shedding it dropped real arrays
			// through `typeof v === 'object'` guards (prisma's
			// collectSelectRefs froze its isArray branch dead)
			if arm.Kind == abstractdomain.KindSet && s == abstractdomain.ClaimSortString && wanted == abstractdomain.ClaimSortObject {
				kept = append(kept, arm)
				continue
			}
			if s == abstractdomain.ClaimSortNone || same(s) {
				kept = append(kept, arm)
			}
		}
		if len(kept) > 0 {
			return abstractdomain.KindUnionOf(kept)
		}
		return shape
	}
	// an OPAQUE value carries no shape of its own — it is the "this file
	// determines nothing sharper" placeholder, not a competing claim —
	// so a type test's proof (a REAL runtime check the guard just ran)
	// is strictly sharper than the placeholder and replaces it exactly
	// as a bare unknown would. Discarding the shape here starved a
	// `typeof x === "number"` guard on an opaque JSON.parse element read
	// of its own proof: the guard is evidence the opaque marker never
	// had, not a weaker guess a real external determination should
	// out-rank.
	if present.Kind == abstractdomain.KindUnknown {
		return shape
	}
	// a POSSIBLY-NaN receiver met with a NUMBER-sorted shape: `typeof
	// v === "number"` holding proves the run is NOT the NaN arm as
	// surely as a comparison guard does (NaN answers "number" too —
	// sec-typeof-operator — but the held wrapper's own NaN-or-Inner
	// split is exactly what MeetKnown's KindPossiblyNaN arm already
	// narrows on a real set), so the same meet applies here: the
	// wrapper's inner claim meets the proved shape, dropping the NaN
	// arm the shape itself does not carry (GroundOfTypeofWord's
	// "number" answer IS admittedly PossiblyNaN, so this only fires
	// for a non-number shape reaching a possibly-NaN receiver, or an
	// inner claim MeetKnown can still sharpen). Falling through here
	// left a `typeof` guard's own proof discarded in favor of an
	// unposeable held wrapper — the same class of bug the OpDiv arm
	// above was already fixed for.
	if present.Kind == abstractdomain.KindPossiblyNaN {
		// MeetKnown's own KindPossiblyNaN arm only fires when the OTHER
		// side is a bare KindSet/KindValues — GroundOfTypeofWord's
		// "number" answer is itself wrapped PossiblyNaN (typeof NaN is
		// "number" too — sec-typeof-operator), so both sides arrive
		// wrapped and MeetKnown's guard misses them, falling through to
		// its unchanged bottom return. Unwrapping the proved shape here
		// (when it is itself a NaN-admitting number ground) hands
		// MeetKnown the bare set it already knows how to meet against a
		// possibly-NaN receiver, and re-wraps the result the same way —
		// the receiver's own NaN-or-not standing decides whether NaN
		// survives, since the shape proves nothing about whether THIS
		// particular NaN case is ruled out beyond what typeof already
		// admits.
		if shape.Kind == abstractdomain.KindPossiblyNaN && shape.Inner != nil {
			return abstractdomain.PossiblyNaN(abstractdomain.MeetKnown(present, *shape.Inner))
		}
		return abstractdomain.MeetKnown(present, shape)
	}
	if present.Kind == abstractdomain.KindObject && shape.Kind == abstractdomain.KindObject && !present.Complete {
		keys := append([]abstractdomain.ObjectKey{}, present.Keys...)
		for _, sk := range shape.Keys {
			if _, found := objectKeyValue(present, sk.Name); !found {
				keys = append(keys, sk)
			}
		}
		merged := abstractdomain.KnownObject(keys, nil, present.Complete, abstractdomain.TrustLevelOf(present), present.BareProto)
		// what was held is the sharper side: only ITS brand ambiguity
		// survives the meet (the shape's ground proves nothing sharper)
		if present.MaybeArray && merged.Kind == abstractdomain.KindObject {
			merged.MaybeArray = true
		}
		return merged
	}
	return present
}

// excludeKind drops the arms a refuted type test rules out. The value
// may still be absent (falsity proves no presence), so a maybe wrapper
// stays; every arm whose claim speaks EXACTLY the refuted kind goes. An
// emptied union claims nothing (the branch is dead — kindUnion of
// nothing is the honest unknown).
func excludeKind(known abstractdomain.AbstractValue, kind string) abstractdomain.AbstractValue {
	if known.Kind == abstractdomain.KindPossiblyUndefined {
		// PossiblyAbsent, not PossiblyUndefined: the rebuild must keep
		// the wrapper's own AbsentSide (NullOnly/UndefOnly/conflated) —
		// PossiblyUndefined always builds conflated, which would silently
		// widen a flavored wrapper's absent side back to "either" on
		// every excludeKind call.
		return abstractdomain.PossiblyAbsent(excludeKind(*known.Inner, kind), known.AbsentSide, "", false, known.ProvedAbsent)
	}
	if known.Kind != abstractdomain.KindKindUnion {
		return known
	}
	var kept []abstractdomain.AbstractValue
	changed := false
	for _, arm := range known.Arms {
		// the sequence ambiguity again: a set-shaped arm classified
		// "string" may be an array, so neither a refuted string test
		// nor a refuted object test proves it gone
		if arm.Kind == abstractdomain.KindSet && abstractdomain.KindOfClaim(arm) == abstractdomain.ClaimSortString &&
			(kind == "string" || kind == "object") {
			kept = append(kept, arm)
			continue
		}
		if string(abstractdomain.KindOfClaim(arm)) != kind {
			kept = append(kept, arm)
		} else {
			changed = true
		}
	}
	if !changed {
		return known
	}
	return abstractdomain.KindUnionOf(kept)
}

// keepSequenceArms is a held `Array.isArray(x)`: the value IS an Array
// exotic object, so a kind union keeps only the arms that could be one
// (Narrowed.SequenceBrand's own doc states which, and why a plain
// string arm is among them). A value that is not a union at all passes
// whole: the brand adds nothing to a single claim the walk already
// holds, and refusing it here would discard a fact.
//
// Dropping every arm would leave nothing to answer with, which no
// runtime value justifies — the union's own reading was then not one
// this rule can classify, so the held value stands unchanged.
func keepSequenceArms(known abstractdomain.AbstractValue) abstractdomain.AbstractValue {
	if known.Kind == abstractdomain.KindPossiblyUndefined {
		// isArray answers false for the absent value (step 1: not an
		// Object), so holding proves presence — the wrapper comes off
		// rather than being rebuilt around the narrowed inner
		return keepSequenceArms(*known.Inner)
	}
	if known.Kind != abstractdomain.KindKindUnion {
		return known
	}
	var kept []abstractdomain.AbstractValue
	changed := false
	for _, arm := range known.Arms {
		if couldBeArray(arm) {
			kept = append(kept, arm)
		} else {
			changed = true
		}
	}
	if !changed || len(kept) == 0 {
		return known
	}
	return abstractdomain.KindUnionOf(kept)
}

// dropRefutedBrandArms is a REFUTED `x instanceof C` for a
// default-library C: the value's prototype chain does not carry
// %C.prototype% (sec-instanceofoperator step 5 →
// sec-ordinaryhasinstance's Repeat over [[GetPrototypeOf]], which
// returns false exactly when the prototype never appears), so a kind
// union drops the arms whose kind IS that brand and keeps every arm the
// brand cannot decide.
//
// This is the symmetric half of keepSequenceArms, and it takes the same
// two escapes: a value that is not a union passes whole (a refutation
// removes no fact from a single claim the walk already holds), and a
// reading that would drop EVERY arm leaves the value untouched — no
// runtime value justifies an empty answer, so the union's reading was
// then not one this rule can classify.
//
// Absence is deliberately NOT stripped here. `undefined instanceof Map`
// answers false (sec-ordinaryhasinstance step 3: not an Object), so the
// false arm ADMITS the absent value — the opposite of the held test,
// where holding proves presence. A maybe-wrapped value keeps its
// wrapper and its inner union is narrowed inside it.
func dropRefutedBrandArms(known abstractdomain.AbstractValue, brand string) abstractdomain.AbstractValue {
	if known.Kind == abstractdomain.KindPossiblyUndefined {
		inner := dropRefutedBrandArms(*known.Inner, brand)
		if abstractdomain.SameKnown(inner, *known.Inner) {
			return known
		}
		return abstractdomain.PossiblyUndefined(inner, "", false, false)
	}
	if known.Kind != abstractdomain.KindKindUnion {
		return known
	}
	var kept []abstractdomain.AbstractValue
	changed := false
	for _, arm := range known.Arms {
		if wearsBrand(arm, brand) {
			changed = true
			continue
		}
		kept = append(kept, arm)
	}
	if !changed || len(kept) == 0 {
		return known
	}
	return abstractdomain.KindUnionOf(kept)
}

// wearsBrand reports whether one union arm's kind IS the refuted
// brand's — the only reading that lets the arm go. A Map-flavoured
// collection is what `instanceof Map` decides; a Set-flavoured one is
// what `instanceof Set` decides; the list kinds an Array value wears
// answer `instanceof Array`; a date and a regex answer their own.
// Every other arm — a set-shaped claim, a plain object, a scalar, an
// unclassified reading — says nothing about its prototype chain here
// and keeps its place, which is the answer that discards no runtime
// value.
func wearsBrand(arm abstractdomain.AbstractValue, brand string) bool {
	switch brand {
	case "Map":
		return arm.Kind == abstractdomain.KindCollection && arm.CollectionFlavor == abstractdomain.FlavorMap
	case "Set":
		return arm.Kind == abstractdomain.KindCollection && arm.CollectionFlavor == abstractdomain.FlavorSet
	case "Array":
		// the graph kinds an Array value wears. A set-shaped SEQUENCE arm
		// is NOT among them: strings and arrays share the tuple layer, so
		// such an arm may be a string, which `instanceof Array` refutes
		// nothing about — couldBeArray's own reading, read the other way.
		return arm.Kind == abstractdomain.KindList || arm.Kind == abstractdomain.KindObjectStar ||
			arm.Kind == abstractdomain.KindArrayHoles
	case "Date":
		return arm.Kind == abstractdomain.KindDate
	case "RegExp":
		return arm.Kind == abstractdomain.KindRegex
	}
	return false
}

// couldBeArray reports whether one union arm's claim admits an Array
// exotic object. The graph kinds an array value wears say yes outright;
// a set-shaped arm says yes exactly when its forms speak the SEQUENCE
// layer, which strings and arrays share; every scalar-sorted arm says
// no, since a scalar is not an Object (sec-isarray step 1). An arm the
// reading cannot classify says yes — the answer that discards nothing.
func couldBeArray(arm abstractdomain.AbstractValue) bool {
	switch arm.Kind {
	case abstractdomain.KindList, abstractdomain.KindObjectStar, abstractdomain.KindArrayHoles:
		return true
	case abstractdomain.KindValues:
		return arm.KindTag == abstractdomain.PrimitiveArray || arm.KindTag == abstractdomain.PrimitiveString
	case abstractdomain.KindSet:
		if arm.SetKindTag != abstractdomain.SetKindTagNone {
			return false
		}
		return abstractdomain.KindOfClaim(arm) != abstractdomain.ClaimSortNumber &&
			abstractdomain.KindOfClaim(arm) != abstractdomain.ClaimSortBoolean
	case abstractdomain.KindNaN, abstractdomain.KindBigints, abstractdomain.KindSymbol,
		abstractdomain.KindUndef, abstractdomain.KindNull, abstractdomain.KindHostFunction,
		abstractdomain.KindCollection, abstractdomain.KindPromise, abstractdomain.KindDate,
		abstractdomain.KindRegex:
		return false
	case abstractdomain.KindObject:
		// A keyed RECORD is never an array: the domain spells arrays as
		// KindList/KindObjectStar/KindArrayHoles/KindValues(PrimitiveArray),
		// and sec-isarray answers true only for an Array exotic object.
		// Without this arm a JSON union's plain-object arm rode through
		// `Array.isArray(x)`'s true side and the guarded return carried
		// an object into an array-stated position.
		return false
	case abstractdomain.KindPossiblyNaN:
		// a NUMBER arm beside its NaN possibility — the shape a plain
		// `number` position wears (declared_value.go's IsNumberGround
		// path wraps the ground set in PossiblyNaN). Both halves are
		// numbers, and no number is an Object, so isArray answers false
		// for the whole arm. Without this the wrapper fell to the
		// default below and a declared `T[] | number` kept its number
		// arm right through the guard, leaving the element read with an
		// arm that states no position.
		if arm.Inner == nil {
			return true
		}
		return couldBeArray(*arm.Inner)
	default:
		return true
	}
}

// unionOfWords is the union set spelling exactly these words.
func unionOfWords(words [][]float64) refinementsets.RefinedSet {
	set := refinementsets.StringTuple(runesOf(words[0]))
	for _, w := range words[1:] {
		set = refinementsets.MakeRefinedSet(refinementsets.Union(set, refinementsets.StringTuple(runesOf(w))))
	}
	return set
}

// runesOf is String.fromCodePoint(...words) — the TS source builds the
// word's string back from its codepoint tuple before re-spelling it as
// a StringTuple; this does the same via Go runes.
func runesOf(points []float64) string {
	runes := make([]rune, len(points))
	for i, p := range points {
		runes[i] = rune(int32(p))
	}
	return string(runes)
}

// sameWord reports whether two word tuples spell the same codepoints.
func sameWord(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// keepWordSet keeps only the WORDS a held literal disjunction admits:
// exact word-listable claims intersect, a kind union drops arms sharing
// no word, and anything the word reading cannot spell keeps its own
// facts. Holding the test proves presence.
func keepWordSet(known abstractdomain.AbstractValue, wordSet [][]float64) abstractdomain.AbstractValue {
	if known.Kind == abstractdomain.KindPossiblyUndefined {
		return keepWordSet(*known.Inner, wordSet)
	}
	if known.Kind == abstractdomain.KindSet && known.SetKindTag == abstractdomain.SetKindTagNone {
		words, ok := refinementsets.WordTuplesOf(known.Set)
		if !ok {
			// the prior claim spells no word list (a plain string, a
			// pattern ground) — the held disjunction alone pins the value:
			// whatever it was before, it is now one of the admitted words
			return abstractdomain.KnownSet(unionOfWords(wordSet), nil, abstractdomain.TrustLevelOf(known), abstractdomain.SetKindTagNone)
		}
		var kept [][]float64
		for _, w := range words {
			for _, s := range wordSet {
				if sameWord(s, w) {
					kept = append(kept, w)
					break
				}
			}
		}
		if len(kept) == 0 || len(kept) == len(words) {
			return known
		}
		return abstractdomain.KnownSet(unionOfWords(kept), nil, abstractdomain.TrustLevelOf(known), abstractdomain.SetKindTagNone)
	}
	if known.Kind == abstractdomain.KindValues && known.KindTag == abstractdomain.PrimitiveString {
		// one exact word: in the admitted set it stays; outside, the
		// branch cannot run with it — but proving THAT is the caller's
		// dead-branch machinery, so the word passes untouched
		return known
	}
	if known.Kind == abstractdomain.KindKindUnion {
		armWords := func(arm abstractdomain.AbstractValue) ([][]float64, bool) {
			return armWordsOf(arm)
		}
		var kept []abstractdomain.AbstractValue
		changed := false
		for _, arm := range known.Arms {
			words, ok := armWords(arm)
			if !ok {
				kept = append(kept, arm)
				continue
			}
			admitted := false
			for _, w := range words {
				for _, s := range wordSet {
					if sameWord(s, w) {
						admitted = true
						break
					}
				}
				if admitted {
					break
				}
			}
			if admitted {
				kept = append(kept, arm)
			} else {
				changed = true
			}
		}
		if !changed || len(kept) == 0 {
			mapped := make([]abstractdomain.AbstractValue, len(known.Arms))
			for i, arm := range known.Arms {
				mapped[i] = keepWordSet(arm, wordSet)
			}
			return abstractdomain.KindUnionOf(mapped)
		}
		mapped := make([]abstractdomain.AbstractValue, len(kept))
		for i, arm := range kept {
			mapped[i] = keepWordSet(arm, wordSet)
		}
		return abstractdomain.KindUnionOf(mapped)
	}
	return known
}

// armWordsOf reads one kind-union arm's words: a NESTED union's words
// are its arms' words — the two-alias seeding builds
// kindUnion(kindUnion(words), kindUnion(words)).
func armWordsOf(arm abstractdomain.AbstractValue) ([][]float64, bool) {
	if arm.Kind == abstractdomain.KindValues && arm.KindTag == abstractdomain.PrimitiveString {
		return [][]float64{arm.Values}, true
	}
	if arm.Kind == abstractdomain.KindSet && arm.SetKindTag == abstractdomain.SetKindTagNone {
		return refinementsets.WordTuplesOf(arm.Set)
	}
	if arm.Kind == abstractdomain.KindKindUnion {
		var collected [][]float64
		for _, inner := range arm.Arms {
			words, ok := armWordsOf(inner)
			if !ok {
				return nil, false
			}
			collected = append(collected, words...)
		}
		return collected, true
	}
	return nil, false
}

// dropWordSet sheds the WORDS a refuted literal disjunction excludes —
// the mirror of keepWordSet. Falsity proves no presence, so a maybe
// wrapper stays.
func dropWordSet(known abstractdomain.AbstractValue, excluded [][]float64) abstractdomain.AbstractValue {
	if known.Kind == abstractdomain.KindPossiblyUndefined {
		// Same AbsentSide-preserving rebuild excludeKind uses above.
		return abstractdomain.PossiblyAbsent(dropWordSet(*known.Inner, excluded), known.AbsentSide, "", false, known.ProvedAbsent)
	}
	if known.Kind == abstractdomain.KindSet && known.SetKindTag == abstractdomain.SetKindTagNone {
		words, ok := refinementsets.WordTuplesOf(known.Set)
		if !ok {
			return known
		}
		var kept [][]float64
		for _, w := range words {
			excludedHere := false
			for _, s := range excluded {
				if sameWord(s, w) {
					excludedHere = true
					break
				}
			}
			if !excludedHere {
				kept = append(kept, w)
			}
		}
		if len(kept) == 0 || len(kept) == len(words) {
			return known
		}
		return abstractdomain.KnownSet(unionOfWords(kept), nil, abstractdomain.TrustLevelOf(known), abstractdomain.SetKindTagNone)
	}
	if known.Kind == abstractdomain.KindKindUnion {
		var kept []abstractdomain.AbstractValue
		for _, arm := range known.Arms {
			words, ok := armWordsOf(arm)
			if !ok {
				kept = append(kept, arm)
				continue
			}
			survives := false
			for _, w := range words {
				excludedHere := false
				for _, s := range excluded {
					if sameWord(s, w) {
						excludedHere = true
						break
					}
				}
				if !excludedHere {
					survives = true
					break
				}
			}
			if survives {
				kept = append(kept, arm)
			}
		}
		if len(kept) == 0 {
			return known
		}
		mapped := make([]abstractdomain.AbstractValue, len(kept))
		for i, arm := range kept {
			mapped[i] = dropWordSet(arm, excluded)
		}
		return abstractdomain.KindUnionOf(mapped)
	}
	return known
}

func narrowAt(known abstractdomain.AbstractValue, path []string, n Narrowed) abstractdomain.AbstractValue {
	if len(path) == 0 {
		if n.HasWordSet {
			return keepWordSet(known, n.WordSet)
		}
		if n.HasWordSetExcluded {
			return dropWordSet(known, n.WordSetExcluded)
		}
		if n.HasExcludesBooleanWord {
			return dropBooleanWord(known, n.ExcludesBooleanWord)
		}
		if n.ExcludesKind != "" {
			return excludeKind(known, n.ExcludesKind)
		}
		if n.SequenceBrand {
			return keepSequenceArms(known)
		}
		if n.RefutedBrand != "" {
			return dropRefutedBrandArms(known, n.RefutedBrand)
		}
		if n.HasShape {
			return meetShape(known, n.Shape)
		}
		if n.Definedness == "defined" {
			present := known
			if known.Kind == abstractdomain.KindPossiblyUndefined {
				present = *known.Inner
			}
			if n.Truthiness == "truthy" {
				return keepTruthy(present)
			}
			return present
		}
		if n.Definedness == "undefined" {
			if known.Kind == abstractdomain.KindPossiblyUndefined || known.Kind == abstractdomain.KindUndef {
				return abstractdomain.Undef
			}
			return known
		}
		if n.Exact != nil {
			exactSort := n.ExactSort
			if exactSort == "" {
				exactSort = abstractdomain.PrimitiveNumber
			}
			// the held test PINS the value to these words — MET with what
			// was already known, never replacing it: a prior `!== 1.5`
			// difference and a later `[0.5, 1.5].includes(x)` pin
			// intersect to {0.5} in either application order.
			if exactSort == abstractdomain.PrimitiveNumber {
				if known.Kind == abstractdomain.KindValues && known.KindTag == exactSort {
					var kept []float64
					for _, v := range n.Exact {
						if floatsInclude(known.Values, v) {
							kept = append(kept, v)
						}
					}
					if len(kept) < len(n.Exact) {
						return abstractdomain.KnownValues(kept, exactSort, abstractdomain.TrustProved)
					}
				}
				if known.Kind == abstractdomain.KindSet && known.SetKindTag == abstractdomain.SetKindTagNone &&
					NarrowKernel() != nil {
					var kept []float64
					changed := false
					for _, v := range n.Exact {
						member, refused := memberRefusable(known.Set, v)
						if refused || member {
							kept = append(kept, v)
						} else {
							changed = true
						}
					}
					if changed {
						return abstractdomain.KnownValues(kept, exactSort, abstractdomain.TrustProved)
					}
				}
			}
			return abstractdomain.KnownValues(append([]float64{}, n.Exact...), exactSort, abstractdomain.TrustProved)
		}
		// A BOOLEAN-SORTED value answers truthiness by its own two words,
		// never by the real-line forms riding alongside. The numeric
		// truthiness channel poses "not (x === 0 or isNaN x)" over the
		// whole line, and meeting that with {0, 1} spells the open
		// interval (0, 1) — a real-line answer to a two-point question,
		// which then failed a `true` position that {1} satisfies. The word
		// filter is exact here: ToBoolean of the boolean sort is the
		// identity on its two values.
		if known.Kind == abstractdomain.KindValues && known.KindTag == abstractdomain.PrimitiveBoolean {
			if n.Truthiness == "truthy" {
				return keepTruthy(known)
			}
			if n.Truthiness == "falsy" {
				return keepFalsy(known)
			}
		}
		if n.Truthiness == "falsy" {
			return keepFalsy(known)
		}
		if n.KeepAbsent {
			// the `P === undefined || <numeric>` whenTrue: absence stays
			// admitted whole, and the present part narrows to the
			// numeric side's own claim
			if known.Kind == abstractdomain.KindUndef {
				return known
			}
			if known.Kind == abstractdomain.KindPossiblyUndefined {
				leafOnly := n
				leafOnly.KeepAbsent = false
				narrowed := ApplyNarrowed(*known.Inner, leafOnly)
				rewrapped := known
				rewrapped.Inner = &narrowed
				return rewrapped
			}
		}
		if n.Refuting && known.Kind == abstractdomain.KindPossiblyNaN {
			// NaN fails every comparison, so a REFUTED comparison keeps
			// it: the real part narrows, NaN rides on
			return abstractdomain.PossiblyNaN(ApplyNarrowed(*known.Inner, n))
		}
		if n.Refuting && (known.Kind == abstractdomain.KindUnknown || known.Kind == abstractdomain.KindPossiblyUndefined || known.Kind == abstractdomain.KindUndef) {
			return known
		}
		// a refuted WORD equality arrives as difference(C*, word): the
		// literal-union narrowing again, single-word this time — a
		// word-listable prior claim sheds exactly the removed words and
		// the survivors are spelled plainly. The stacked difference would
		// blind the pattern prover downstream (it reads one shape, not an
		// intersection), and the placement ask below cannot help: C*
		// minus a word sits inside no finite union.
		if len(n.Forms) > 0 && allDifferenceOfStringGround(n.Forms) {
			var removed [][]float64
			readable := true
			for _, f := range n.Forms {
				if f.Form != refinementsets.FormDifference {
					continue
				}
				words, ok := refinementsets.WordTuplesOf(*f.B)
				if !ok {
					readable = false
					break
				}
				removed = append(removed, words...)
			}
			if readable && len(removed) > 0 {
				dropped := dropWordSet(known, removed)
				if !abstractdomain.SameKnown(dropped, known) {
					return dropped
				}
			}
		}
		// a PATTERN-form narrowing over held sequence knowledge: stacked
		// forms have no reading in the kernel's pattern prover, but where
		// the PROVED subset says the narrowing's set already sits inside
		// what is held, the narrowing IS the intersection — spelled
		// plainly, so the pattern questions downstream stay askable.
		// (`strings ∩ startsWith("/")` is startsWith("/"); the kernel
		// decides the containment, this code only asks.)
		if known.Kind == abstractdomain.KindSet && known.SetKindTag == abstractdomain.SetKindTagNone &&
			NarrowKernel() != nil && len(n.Forms) > 0 && everySequenceForm(n.Forms) {
			// C* is the whole string ground: as a CONJUNCT beside other
			// sequence forms it adds nothing, and dropping a conjunct only
			// weakens a claim (value ∈ A ∩ C* implies value ∈ A) — while
			// keeping it stacked blinds the pattern prover, which reads one
			// shape, not an intersection
			candidate := refinementsets.MakeRefinedSet(refinementsets.WithoutStringGround(n.Forms)...)
			if refinementsets.IsStringGround(known.Set) {
				return abstractdomain.KnownSet(candidate, nil, abstractdomain.TrustLevelOf(known), abstractdomain.SetKindTagNone)
			}
			if subset, refused := seqSubsetRefusable(candidate, known.Set); !refused && subset {
				return abstractdomain.KnownSet(candidate, nil, abstractdomain.TrustLevelOf(known), abstractdomain.SetKindTagNone)
			}
		}
		// a FINITE NUMERIC SCATTER narrows by MEMBERSHIP: each member
		// either satisfies the guard's forms or is discarded — the
		// {0.5, 1.5} scatter under a held `!== 1.5` is exactly {0.5}.
		// NarrowKnown's KindValues arm cannot decide this (it has no
		// kernel); the narrow kernel answers per member here, and a
		// refused question keeps the member — no claim, no discard.
		if known.Kind == abstractdomain.KindValues && known.KindTag == abstractdomain.PrimitiveNumber &&
			NarrowKernel() != nil && len(n.Forms) > 0 {
			formSet := refinementsets.MakeRefinedSet(n.Forms...)
			var kept []float64
			changed := false
			for _, v := range known.Values {
				member, refused := memberRefusable(formSet, v)
				if refused || member {
					kept = append(kept, v)
				} else {
					changed = true
				}
			}
			if changed {
				return abstractdomain.KnownValues(kept, known.KindTag, abstractdomain.TrustLevelOf(known))
			}
			return known
		}
		return shedRemovedEndpoints(abstractdomain.NarrowKnown(known, n.Forms))
	}
	// a PATH narrowing that proves its leaf present or truthy proves
	// the ROOT present too — `o?.n` truthy required o to be there —
	// so the maybe strips and the walk continues inside
	if known.Kind == abstractdomain.KindPossiblyUndefined && (n.Definedness == "defined" || n.Truthiness == "truthy") {
		return narrowAt(*known.Inner, path, n)
	}
	// a keyed EXACT test on a SORT UNION of objects selects arms: an
	// arm whose key at the path provably differs from the pinned word
	// sheds, and the survivors narrow on — the discriminated-union
	// split for plain object-type unions.
	//
	// The step below reads a segment as an OBJECT KEY. An index segment
	// finds no such key, so the step answers not-reached and the arm is
	// KEPT — a list arm under an index shape sheds nothing, which is the
	// answer that discards no runtime value.
	if known.Kind == abstractdomain.KindKindUnion && n.Exact != nil {
		pinned := n.Exact
		var kept []abstractdomain.AbstractValue
		for _, arm := range known.Arms {
			held := &arm
			for _, step := range path {
				if held != nil && held.Kind == abstractdomain.KindObject {
					v, found := objectKeyValue(*held, step)
					if found {
						held = &v
					} else {
						held = nil
					}
				} else {
					held = nil
				}
			}
			if held == nil || held.Kind != abstractdomain.KindValues {
				kept = append(kept, arm)
				continue
			}
			exactSort := n.ExactSort
			if exactSort == "" {
				exactSort = abstractdomain.PrimitiveNumber
			}
			if exactSort != held.KindTag {
				kept = append(kept, arm)
				continue
			}
			if len(held.Values) == len(pinned) && sameWord(held.Values, pinned) {
				kept = append(kept, arm)
			}
		}
		if len(kept) > 0 && len(kept) != len(known.Arms) {
			// the union's own Grade is the declaration-backed floor every
			// arm was read at (entry_env.go's AtTrustLevel stamps the
			// PARAMETER's outer value, never each arm) — carrying it onto
			// the surviving arm here is what lets memberValueGraded's own
			// receiver.Grade gate see it at the member read past this
			// point. Dropping it here left a discriminant-narrowed arm's
			// members indistinguishable from an unexamined AfterReaders
			// seed (nan_wrapper.go's CheckPossiblyNaN ungraded skip), which
			// silenced a real escape past the sink instead of reporting it.
			survivor := abstractdomain.KindUnionOf(kept)
			if known.Grade != "" {
				survivor = abstractdomain.AtTrustLevel(survivor, known.Grade)
			}
			return narrowAt(survivor, path, n)
		}
		return known
	}
	// a LIST root under an INDEX segment: the narrowing speaks about one
	// slot, and the list carries its items positionally, so the item at
	// that slot narrows in place and the rest stay as they were. A slot
	// past the items says nothing — the list holds no value there for the
	// narrowing to sharpen.
	//
	// The in-range slot needs no absence wrapper: a KindList is hole-free
	// by construction (walk/element_access.go states the argument — every
	// builder writes each slot from its own walked source, an elision
	// writes Undef, and an index write retires the whole receiver rather
	// than growing it). Narrowing an Undef slot is a no-op, which is the
	// right answer for a hole.
	if known.Kind == abstractdomain.KindList {
		slot, isIndex := dataflowfacts.IndexSegmentOf(path[0])
		if !isIndex || slot >= len(known.Items) {
			return known
		}
		items := make([]abstractdomain.AbstractValue, len(known.Items))
		copy(items, known.Items)
		items[slot] = narrowAt(items[slot], path[1:], n)
		return abstractdomain.KnownList(items, abstractdomain.TrustLevelOf(known))
	}
	if known.Kind != abstractdomain.KindObject {
		return known
	}
	// an INDEX segment names a list slot, and an object carries no such
	// slot — an object under an index segment stays as it was rather than
	// having the bracket text read as one of its keys
	if _, isIndex := dataflowfacts.IndexSegmentOf(path[0]); isIndex {
		return known
	}
	// a discriminated object narrows through its variants first: the
	// test that held discards every variant it contradicts, and a
	// lone survivor IS the runtime shape
	if known.Variants != nil {
		var survivors []abstractdomain.AbstractValue
		for _, v := range known.Variants {
			if variantConsistent(v, path, n) {
				survivors = append(survivors, v)
			}
		}
		if len(survivors) == 1 {
			return narrowAt(survivors[0], path, n)
		}
		if len(survivors) > 1 && len(survivors) < len(known.Variants) {
			joint := narrowKeyed(known, path, n)
			if joint.Kind == abstractdomain.KindObject {
				joint.Variants = survivors
			}
			return joint
		}
	}
	return narrowKeyed(known, path, n)
}

// allDifferenceOfStringGround reports whether every form is a
// difference whose A side is the string ground — `n.forms.every((f) =>
// f.form === "difference" && isStringGround(f.A))` in the TS source.
func allDifferenceOfStringGround(forms []refinementsets.Refinement) bool {
	for _, f := range forms {
		if f.Form != refinementsets.FormDifference || !refinementsets.IsStringGround(*f.A_) {
			return false
		}
	}
	return true
}

// everySequenceForm reports whether every form is one of the sequence
// forms the pattern channel reads.
func everySequenceForm(forms []refinementsets.Refinement) bool {
	for _, f := range forms {
		switch f.Form {
		case refinementsets.FormConcatenation, refinementsets.FormStar, refinementsets.FormRepeat,
			refinementsets.FormDifference, refinementsets.FormUnion, refinementsets.FormEmptyTuple,
			refinementsets.FormWord:
			// on the reading
		default:
			return false
		}
	}
	return true
}

// memberRefusable asks the kernel's Member question for one scalar,
// turning a refusal into an (answer, refused) pair — the same
// try/catch shape seqSubsetRefusable keeps for its own question.
func memberRefusable(set refinementsets.RefinedSet, v float64) (member bool, refused bool) {
	defer func() {
		if recover() != nil {
			member, refused = false, true
		}
	}()
	return NarrowKernel().Member(set, []float64{v}), false
}

// seqSubsetRefusable asks the kernel's SeqSubset question, turning a
// refusal into an (answer, refused) pair — the TS source's try/catch
// around `narrowKernel.seqSubset(...)`.
func seqSubsetRefusable(a, b refinementsets.RefinedSet) (subset bool, refused bool) {
	defer func() {
		if recover() != nil {
			subset, refused = false, true
		}
	}()
	return NarrowKernel().SeqSubset(a, b), false
}

// shedRemovedEndpoints is the port of shedRemovedEndpoints in the TS
// source: a stacked difference that removes exactly a CLOSED INTEGER
// ENDPOINT of its own set is the bumped bound — {integer, ≥ −1, ≤ 2}
// minus {−1} IS {integer, ≥ 0, ≤ 2} — and the grid has nothing between.
// Said plainly here because the enclosure the transfers read keeps only
// closed bounds, so the stacked spelling would keep a refuted sentinel
// (`i === -1 ? … : len - i`) alive downstream. Repeats to a fixpoint:
// removing −1 can expose a removed 0.
func shedRemovedEndpoints(known abstractdomain.AbstractValue) abstractdomain.AbstractValue {
	if known.Kind != abstractdomain.KindSet || known.SetKindTag != abstractdomain.SetKindTagNone {
		return known
	}
	original := known.Set.Forms
	hasInteger := false
	for _, f := range original {
		if f.Form == refinementsets.FormInteger {
			hasInteger = true
			break
		}
	}
	if !hasInteger {
		return known
	}
	var lo, hi *float64
	hasLo, hasHi := false, false
	for _, f := range original {
		if f.Form == refinementsets.FormAtLeast && !hasLo {
			v := f.A
			lo = &v
			hasLo = true
		}
		if f.Form == refinementsets.FormAtMost && !hasHi {
			v := f.A
			hi = &v
			hasHi = true
		}
	}
	// every removed point riding a plain difference-of-oneOf; the
	// bounds walk in from removed endpoints to a fixpoint
	removed := map[float64]struct{}{}
	for _, f := range original {
		if f.Form != refinementsets.FormDifference {
			continue
		}
		if len(f.B.Forms) != 1 || f.B.Forms[0].Form != refinementsets.FormOneOf {
			continue
		}
		for _, w := range f.B.Forms[0].W {
			removed[w] = struct{}{}
		}
	}
	if len(removed) == 0 {
		return known
	}
	consumed := map[float64]struct{}{}
	for round := 0; round < len(removed)+1; round++ {
		moved := false
		if hasLo {
			if _, isRemoved := removed[*lo]; isRemoved {
				if _, isConsumed := consumed[*lo]; !isConsumed {
					consumed[*lo] = struct{}{}
					v := *lo + 1
					lo = &v
					moved = true
				}
			}
		}
		if hasHi {
			if _, isRemoved := removed[*hi]; isRemoved {
				if _, isConsumed := consumed[*hi]; !isConsumed {
					consumed[*hi] = struct{}{}
					v := *hi - 1
					hi = &v
					moved = true
				}
			}
		}
		if !moved {
			break
		}
	}
	if len(consumed) == 0 {
		return known
	}
	var forms []refinementsets.Refinement
	for _, f := range original {
		if f.Form == refinementsets.FormAtLeast && hasLo {
			forms = append(forms, refinementsets.AtLeast(*lo))
			continue
		}
		if f.Form == refinementsets.FormAtMost && hasHi {
			forms = append(forms, refinementsets.AtMost(*hi))
			continue
		}
		if f.Form != refinementsets.FormDifference {
			forms = append(forms, f)
			continue
		}
		if len(f.B.Forms) != 1 || f.B.Forms[0].Form != refinementsets.FormOneOf {
			forms = append(forms, f)
			continue
		}
		only := f.B.Forms[0]
		var kept []float64
		for _, w := range only.W {
			if _, isConsumed := consumed[w]; !isConsumed {
				kept = append(kept, w)
			}
		}
		if len(kept) == len(only.W) {
			forms = append(forms, f)
			continue
		}
		if len(kept) > 0 {
			rebuilt := f
			b := refinementsets.MakeRefinedSet(refinementsets.OneOf(kept))
			rebuilt.B = &b
			forms = append(forms, rebuilt)
			continue
		}
		if len(f.A_.Forms) != 0 {
			forms = append(forms, f)
		}
	}
	return abstractdomain.KnownSet(refinementsets.MakeRefinedSet(forms...), nil, abstractdomain.TrustLevelOf(known), abstractdomain.SetKindTagNone)
}

func narrowKeyed(known abstractdomain.AbstractValue, path []string, n Narrowed) abstractdomain.AbstractValue {
	key, rest := path[0], path[1:]
	held, found := objectKeyValue(known, key)
	if !found {
		return known
	}
	narrowed := narrowAt(held, rest, n)
	// a narrowed object is no longer exactly the stated annotation;
	// the rebuild keeps the object's grade ceiling — and its bare
	// prototype, or a later missing-key read would falsely claim the
	// inherited function
	keys := make([]abstractdomain.ObjectKey, len(known.Keys))
	copy(keys, known.Keys)
	replaced := false
	for i, k := range keys {
		if k.Name == key {
			keys[i] = abstractdomain.ObjectKey{Name: key, Value: narrowed}
			replaced = true
			break
		}
	}
	if !replaced {
		keys = append(keys, abstractdomain.ObjectKey{Name: key, Value: narrowed})
	}
	return abstractdomain.KnownObject(keys, nil, known.Complete, abstractdomain.TrustLevelOf(known), known.BareProto)
}
