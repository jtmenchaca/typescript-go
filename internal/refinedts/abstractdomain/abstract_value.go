// What the checker knows an expression to be, and what survives two
// paths. A concrete value is a maximally fine state of knowledge
// (TERMS.md term 6 — the singleton); a chain of narrowings is a
// refined set; anything else is unknown. The join is EXACT: two known
// sets join as their union form, which the kernel decides like any
// other set — nothing is loosened except against unknown.

package abstractdomain

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// PrimitiveKind is PrimitiveKind in the TS source: the host sort a
// tracked word was READ UNDER. The model has one root — "a", 97, [97],
// and true ↦ 1 are all tuples — and the sorts are the host-type layer
// that tells them apart. A word reread under another sort (TERMS-v2, the
// admitted-language rule) is not admitted knowledge: the checked
// positions and the transfers degrade it to unknown rather than answer
// through the reread.
type PrimitiveKind string

const (
	PrimitiveNumber  PrimitiveKind = "number"
	PrimitiveBoolean PrimitiveKind = "boolean"
	PrimitiveString  PrimitiveKind = "string"
	PrimitiveArray   PrimitiveKind = "array"
)

// Kind is the tag of an AbstractValue.
type Kind string

const (
	KindValues            Kind = "values"
	KindSet               Kind = "set"
	KindObject            Kind = "object"
	KindObjectStar        Kind = "objectStar"
	KindVariable          Kind = "variable"
	KindList              Kind = "list"
	KindArrayHoles        Kind = "arrayHoles"
	KindCollection        Kind = "collection"
	KindPromise           Kind = "promise"
	KindDate              Kind = "date"
	KindSymbol            Kind = "symbol"
	KindHostFunction      Kind = "hostFunction"
	KindBigints           Kind = "bigints"
	KindRegex             Kind = "regex"
	KindUndef             Kind = "undef"
	KindNull              Kind = "null"
	KindNaN               Kind = "nan"
	KindPossiblyUndefined Kind = "possiblyUndefined"
	KindPossiblyNaN       Kind = "possiblyNaN"
	KindKindUnion         Kind = "kindUnion"
	KindUnknown           Kind = "unknown"
)

// Flavor is the "map" | "set" tag of a collection AbstractValue.
type Flavor string

const (
	FlavorMap Flavor = "map"
	FlavorSet Flavor = "set"
)

// AbsentFlavor is which absent admission a "possiblyUndefined" wrapper's
// OWN absent side carries — never a claim about Inner (Inner already
// says everything about the present side, and a wrapper around Null
// already says "null or undefined" through Inner, not through this
// field). The zero value, AbsentFlavorConflated, is the "either
// admission" reading every existing wrapper carries today — so every
// call site and every reader that does not know about flavors keeps
// today's exact behavior with no edit.
type AbsentFlavor string

const (
	// AbsentFlavorConflated is the zero value: the wrapper's absent side
	// admits BOTH undefined and null, same as before this field existed.
	AbsentFlavorConflated AbsentFlavor = ""
	// AbsentFlavorUndefOnly: the wrapper's absent side is exactly
	// undefined — null is not admitted.
	AbsentFlavorUndefOnly AbsentFlavor = "undefOnly"
	// AbsentFlavorNullOnly: the wrapper's absent side is exactly null —
	// undefined is not admitted.
	AbsentFlavorNullOnly AbsentFlavor = "nullOnly"
)

// Measures is the sequence MEASURES the value provably wears — parse-
// checked facts BETWEEN elements (the exact reduce total, the
// non-decreasing order). A measure is a fact about the VALUE, so any
// sound narrowing keeps it; joins drop it (the joined values need not
// share it).
//
// Sum uses (float64, bool) rather than TS's optional number, per the
// port convention (T | undefined -> the (T, bool) pair when the zero
// value is ambiguous — 0 is a real sum).
type Measures struct {
	Sum    float64
	HasSum bool
	Sorted bool
}

// CollectionEntry is one (key, value) pair of a collection AbstractValue
// — the TS source's `readonly [AbstractValue, AbstractValue]` tuple.
type CollectionEntry struct {
	Key   AbstractValue
	Value AbstractValue
}

// ObjectKey is one key of an "object" AbstractValue's Keys — the TS
// source's `Readonly<Record<string, AbstractValue>>`, carried as an
// ordered slice of (name, value) so formatting and joins can walk keys
// in insertion order the way `Object.keys` does. (A Go map has no
// stable order; the TS record's iteration order is insertion order.)
type ObjectKey struct {
	Name  string
	Value AbstractValue
}

// AbstractValue is the TS source's discriminated union
//
//	{ kind: "values" } | { kind: "set" } | { kind: "object" } | ...
//
// collapsed to one struct with a Kind tag, per the port's convention for
// discriminated unions that carry pure data. Not every field is
// meaningful for every Kind; each constructor below sets only the fields
// its kind uses. Grade == "" means the TS source's `grade?: TrustLevel`
// was absent, which the TS reads as "proved" (TrustLevelOf).
type AbstractValue struct {
	Kind Kind

	// "values": exact host-sorted values.
	Values  []float64
	KindTag PrimitiveKind

	// "set": a refined set, its temporal chart, and its worn sort.
	Set         refinementsets.RefinedSet
	Temporal    *refinementsets.TemporalAnnotation
	SetKindTag  SetKindTag
	Measures    *Measures
	NaNElements bool

	// "set", REPETITION-SHAPED only (refinementsets.AsRepetition succeeds
	// on Set): SeqDense and SeqDenseKnown are KindArrayHoles's Dense/
	// DenseKnown pair, carried on the one other array shape that needed
	// it. A Repetition states MEMBERSHIP-IF-PRESENT over its counted
	// window ("every element lies in this set, count between lo and
	// hi") and nothing about which of those counted positions are OWN
	// PROPERTIES versus holes — the same gap KindArrayHoles's own Dense
	// field closes for the all-holes shape, needed here because a
	// Repetition arriving from an untraced source (a parameter, a
	// summary) carries no density proof, while one a constructor BUILT
	// by writing every counted index (MapOutcome's per-index
	// CreateDataPropertyOrThrow, sec-array.prototype.map) does.
	// InBoundsElementOf's proved-in-bounds arm reads SeqDenseKnown &&
	// SeqDense to skip the PossiblyAbsent wrap it otherwise always
	// applies at that arm — in bounds is a claim about the LENGTH,
	// density a claim about OWN PROPERTIES, and a plain Repetition
	// states only the former.
	//
	// SeqDenseKnown false (the zero value) means "density not
	// established" — every existing Repetition-building call site
	// leaves both fields at their zero value and keeps today's
	// PossiblyAbsent wrap exactly as before. SeqDenseKnown true,
	// SeqDense true is the proved-dense fact; SeqDenseKnown true,
	// SeqDense false would be proved-sparse (not produced by any
	// constructor yet, but a name a reader can check without assuming
	// the pair is boolean-only).
	SeqDense      bool
	SeqDenseKnown bool

	// "object": rooted-keys record, stated annotation (blocked — see
	// package doc), completeness, variants, bareProto, maybeArray.
	Keys       []ObjectKey
	Stated     ObjectAnnotationRef
	Complete   bool
	Variants   []AbstractValue
	BareProto  bool
	MaybeArray bool

	// "variable": a refinement-variable value.
	Symbol      *ast.Symbol
	Bound       refinementsets.RefinedSet
	BoundObject ObjectAnnotationRef
	StarDepth   int

	// "list": a nested exact sequence.
	Items []AbstractValue

	// "arrayHoles": an array whose LENGTH is the exact scalar Inner
	// (a KindValues {n}, the same shape .length already reads for
	// every other array kind) and whose PRESENT-ELEMENT set is
	// ElementSet — the empty set (refinementsets' OneOf(nil)), because
	// a hole array has no present elements at all. Neither claim is
	// asserted through RepeatOf/star: a repetition set denotes tuples
	// whose OWN members are drawn from ElementSet, and RepeatOf(∅, n,
	// n) for n > 0 denotes ∅ itself (no tuple can fill n positions
	// from an empty alphabet) — sound as "the present elements admit
	// nothing," unsound as "the array IS a member of nothing." The two
	// claims stay independent scalars/sets, exactly the way the walk's
	// own array-local frame (walk/ir_array_slots.go's ".len"/".elem"
	// pair) keeps a length slot and an element slot apart rather than
	// fusing them into one kernel question. A read of .length answers
	// Inner directly; an element read answers absent because
	// ElementSet, being empty, has nothing to hand back — the same
	// Undef machinery KindUndef already carries.
	//
	// `new Array(n)` past KnownList's materialization ceiling builds
	// this instead of a per-slot list; `new Array(40)` stays a
	// KindList (the existing pinned row) because a small list carries
	// strictly more precision (a consumer can iterate, spread, or
	// destructure its slots) and nothing here should narrow that.
	ElementSet refinementsets.RefinedSet

	// "arrayHoles": Dense and DenseKnown are the one bit every read
	// path EXCEPT Object.keys/values/entries does not need. `new
	// Array(n)` and `Array.from({length: n})` build the same
	// Length/ElementSet pair (sec-array vs. sec-array.from,
	// tmp/ecma262/spec.html) but differ in OWN PROPERTIES: `new
	// Array(n)` never calls CreateDataPropertyOrThrow, so no index
	// 0..n-1 is an own property (SPARSE — Object.keys answers []);
	// Array.from's array-like branch calls CreateDataPropertyOrThrow
	// at every index (DENSE — every index is an own property holding
	// undefined, so Object.keys answers the n index strings).
	// EnumerableOwnProperties (sec-enumerableownproperties, the walk
	// object_static_models.go's Object.keys/values/entries read
	// through) iterates OWN keys only, so this is the one place the
	// two shapes read apart. Every other read (.length, an element
	// read, Array.isArray, instanceof, truthiness, spread, for-of,
	// .join()) answers off Length/ElementSet alone and is provably
	// identical either way — Dense carries no weight there and those
	// call sites do not read it.
	//
	// DenseKnown separates "affirmatively sparse" from "density not
	// established" the same way ProvedAbsent separates "proved absent"
	// from "not proved present" on possiblyUndefined: a bare Dense bool
	// cannot carry both "definitely sparse" and "unproven" without one
	// of Object.keys' two arms overclaiming from a join that could not
	// actually establish either shape (a KindArrayHoles joined with a
	// plain KindList of undefined items proves neither — KnownList's
	// items never carry an own-property fact). DenseKnown false means
	// object_static_models.go's Object.keys/values/entries arms must
	// decline rather than read Dense at all; DenseKnown true means
	// Dense is the proved fact.
	Dense      bool
	DenseKnown bool

	// "collection": a built Map or Set.
	CollectionFlavor Flavor
	Entries          []CollectionEntry

	// "promise" / "possiblyUndefined" / "possiblyNaN" / "objectStar" /
	// "arrayHoles": the wrapped value. For "objectStar" it is the
	// ELEMENT — what one position of the sequence holds — and the
	// sequence states no length of its own. For "arrayHoles" it is the
	// LENGTH — the exact scalar {n}, the same AbstractValue a .length
	// read hands back whole; LengthOfArrayHoles unwraps it to a plain
	// int for callers that want the number rather than the claim.
	Inner *AbstractValue

	// "date": the time value, as its own AbstractValue (number knowledge).
	Millis *AbstractValue

	// "symbol": a Symbol.for(key) registry key, or a fresh Symbol()'s
	// description.
	SymbolKey      string
	HasSymbolKey   bool
	Description    string
	HasDescription bool

	// "bigints": exact bigint values, at any width. int64 per the port
	// convention (TS bigint -> int64 unless the source exceeds it; this
	// domain carries exact literal values, which fit int64 the way the
	// rest of the checker's number handling does).
	BigintValues []int64

	// "regex": a literal regex's source and flags.
	Source string
	Flags  string

	// "possiblyUndefined": true when a mechanism proved a run where the
	// value is absent, never mere not-proved-present.
	ProvedAbsent bool

	// "possiblyUndefined": which admission the wrapper's OWN absent side
	// carries — AbsentFlavorConflated (the zero value) means "either
	// undefined or null", matching every wrapper built before this field
	// existed. See AbsentFlavor's doc for the full rule.
	AbsentSide AbsentFlavor

	// "kindUnion": the sort-distinguished arms.
	Arms []AbstractValue

	// "unknown": true when the value enters from outside the file's
	// determination.
	Opaque bool

	// carried on every kind except the unclaimed ones (unknown, variable
	// carry none in the TS source either — variable has no `grade?`
	// field; unknown has no `grade?` field). "" means absent (proved).
	Grade TrustLevel
}

// SetKindTag is the "bigint" | "symbol" non-double sort a set
// AbstractValue's members wear.
type SetKindTag string

const (
	SetKindTagNone   SetKindTag = ""
	SetKindTagBigint SetKindTag = "bigint"
	SetKindTagSymbol SetKindTag = "symbol"
)

// ObjectAnnotationRef stands in for *annotations.ObjectAnnotation
// (annotations/declared_refinement.ts), which is not ported yet
// (go-port-tracker.md: annotations is "pending (wave 3)", and importing
// it from here would also invert the port order in PORT.md — annotations
// depends on abstract_domain, not the reverse). Comparisons in this
// package only ever check identity (`a.stated !== other.stated` in
// sameKnown, `a.stated === b.stated` in joinKnown) — never the object's
// shape — so an opaque pointer carries that identity exactly. When
// annotations ports, this becomes `*annotations.ObjectAnnotation` and
// every call site (a pointer either way) is unchanged.
type ObjectAnnotationRef = *struct{}

// Unknown is UNKNOWN in the TS source.
var Unknown = AbstractValue{Kind: KindUnknown}

// Opaque is OPAQUE in the TS source: the unknown that is DETERMINED to
// be undeterminable: the value arrives from outside the file (an
// external call's result, a network body), so the type is everything
// this file determines — and reads through it inherit that standing.
var Opaque = AbstractValue{Kind: KindUnknown, Opaque: true}

// PossiblyNaN is possiblyNaN in the TS source: wrap knowledge as
// possibly NaN; collapses degenerate wraps.
func PossiblyNaN(inner AbstractValue) AbstractValue {
	if inner.Kind == KindUnknown {
		return Unknown
	}
	if inner.Kind == KindNaN {
		return NaNValue
	}
	if inner.Kind == KindPossiblyNaN {
		return inner
	}
	grade := TrustLevelOf(inner)
	innerCopy := inner
	if grade == TrustProved {
		return AbstractValue{Kind: KindPossiblyNaN, Inner: &innerCopy}
	}
	return AbstractValue{Kind: KindPossiblyNaN, Inner: &innerCopy, Grade: grade}
}

// Undef is UNDEF in the TS source.
var Undef = AbstractValue{Kind: KindUndef}

// Null is the exactly-null admission the Lean kernel's AbsentMark split
// carries as its own flavor, distinct from KindUndef (exactly-undefined).
// Mirrors Undef's own construction — a bare, kind-only value with no
// carried fields.
var Null = AbstractValue{Kind: KindNull}

// NaNValue is NAN in the TS source (renamed: NAN collides with nothing
// in Go, but the exported identifier NAN would be ALL_CAPS-only, unusual
// among this package's names; NaNValue keeps the connection to the TS
// name in this comment).
var NaNValue = AbstractValue{Kind: KindNaN}

// HostFunction is HOST_FUNCTION in the TS source: a function of unknown
// body, at spec grade — the typeof-function ground and the inherited
// Object.prototype member.
var HostFunction = AbstractValue{Kind: KindHostFunction, Grade: TrustSpec}

// ObjectPrototypeFunctionKeys is OBJECT_PROTOTYPE_FUNCTION_KEYS in the TS
// source: the Object.prototype member names every plain object inherits
// — all FUNCTION-valued (sec-properties-of-the-object-prototype-object).
// A read of one of these on a complete plain object whose own keys lack
// it answers the inherited function, never undefined — the collision
// class the zod survey catalogued.
var ObjectPrototypeFunctionKeys = map[string]bool{
	"constructor":          true,
	"hasOwnProperty":       true,
	"isPrototypeOf":        true,
	"propertyIsEnumerable": true,
	"toLocaleString":       true,
	"toString":             true,
	"valueOf":              true,
}

// PossiblyUndefined is possiblyUndefined in the TS source: the maybe
// wrapper — THE one way a possibly-absent value is built (finding 5): no
// caller spells the record by hand, so the normalization (undef stays
// undef, a maybe never nests) and the grade floor hold everywhere.
// wrapperGrade states the trust of the ABSENCE claim itself where it is
// weaker than the inner value's — a spec row wrapping no knowledge at
// all.
//
// hasWrapperGrade takes the place of TS's `wrapperGrade?: TrustLevel`
// being omitted at the call site.
//
// Delegates to PossiblyAbsent with AbsentFlavorConflated — every
// existing caller (none of which knows about flavors) keeps building
// today's "either admission" wrapper exactly as before.
func PossiblyUndefined(inner AbstractValue, wrapperGrade TrustLevel, hasWrapperGrade bool, provedAbsent bool) AbstractValue {
	return PossiblyAbsent(inner, AbsentFlavorConflated, wrapperGrade, hasWrapperGrade, provedAbsent)
}

// PossiblyAbsent is the flavor-carrying twin of PossiblyUndefined: the
// same maybe wrapper, but flavor states which admission the wrapper's
// OWN absent side carries (AbsentFlavorConflated — the zero value —
// reproduces PossiblyUndefined exactly). A new constructor rather than
// a widened PossiblyUndefined signature, per the package's convention:
// every existing call site keeps compiling unchanged, and only a
// caller that has proved a single flavor reaches for this one.
//
// Normalization, in order:
//   - wrapping KindUndef always collapses to the bare Undef — the
//     flavor is a fact about the WRAPPER's absent contribution, and
//     Undef already IS the exact-undefined value with nothing left to
//     wrap.
//   - wrapping KindNull with a flavor that still admits undefined
//     (conflated or UndefOnly) is the existing Inner=Null wrapper,
//     which already states "null or undefined" through Inner alone —
//     the wrapper's OWN side collapses to conflated (there is nothing
//     left for a separate flavor tag to add: the undefined admission
//     already reads off Inner, not off this field).
//   - wrapping KindNull with AbsentFlavorNullOnly would claim "null,
//     or null" — exactly Null itself, so it collapses to the bare
//     Null value with no wrapper at all.
//   - wrapping an existing KindPossiblyUndefined never nests; the
//     inner wrapper's own flavor stands (a second flavor claim about
//     the same wrapper's absent side is not this constructor's call to
//     make — the caller narrowing an already-built wrapper reaches for
//     a narrowing operation, not a re-wrap).
func PossiblyAbsent(inner AbstractValue, flavor AbsentFlavor, wrapperGrade TrustLevel, hasWrapperGrade bool, provedAbsent bool) AbstractValue {
	if inner.Kind == KindUndef {
		return Undef
	}
	if inner.Kind == KindNull {
		if flavor == AbsentFlavorNullOnly {
			// "null, or null" is just null — no wrapper needed
			return Null
		}
		// conflated or UndefOnly: the undefined admission already reads
		// through Inner=Null (PossiblyUndefined(Null)'s own established
		// meaning), so the wrapper's own side carries nothing further
		flavor = AbsentFlavorConflated
	}
	// PossiblyUndefined(Null) admits BOTH null (the inner value) and
	// undefined (what the wrapper itself contributes) — and the wrapper
	// with Inner = Null states EXACTLY that, so it does NOT collapse:
	// collapsing to the bare Undef would let a flavored consumer (the
	// comparison tables) decide `=== undefined` true of a value that
	// may be null. The wrapper form keeps both admissions visible.
	if inner.Kind == KindPossiblyUndefined {
		if provedAbsent && !inner.ProvedAbsent {
			out := inner
			out.ProvedAbsent = true
			return out
		}
		return inner
	}
	var grade TrustLevel
	if !hasWrapperGrade {
		grade = TrustLevelOf(inner)
	} else {
		grade = MinTrustLevel(wrapperGrade, TrustLevelOf(inner))
	}
	innerCopy := inner
	out := AbstractValue{Kind: KindPossiblyUndefined, Inner: &innerCopy, ProvedAbsent: provedAbsent, AbsentSide: flavor}
	if grade != TrustProved {
		out.Grade = grade
	}
	return out
}

// KindUnion is kindUnion in the TS source: build the sort union,
// collapsing degenerates: no arms claims nothing, one arm IS that arm,
// and an unknown arm dissolves the whole (the union would admit
// anything).
func KindUnionOf(arms []AbstractValue) AbstractValue {
	hasUnknown := false
	for _, arm := range arms {
		if arm.Kind == KindUnknown {
			hasUnknown = true
			break
		}
	}
	if len(arms) == 0 || hasUnknown {
		return Unknown
	}
	if len(arms) == 1 {
		return arms[0]
	}
	floor := TrustProved
	for _, arm := range arms {
		floor = MinTrustLevel(floor, TrustLevelOf(arm))
	}
	if floor == TrustProved {
		return AbstractValue{Kind: KindKindUnion, Arms: arms}
	}
	return AbstractValue{Kind: KindKindUnion, Arms: arms, Grade: floor}
}

// KnownVariable is knownVariable in the TS source.
//
// boundObject takes the place of TS's optional `boundObject?:
// ObjectAnnotation` parameter — pass nil when absent.
func KnownVariable(symbol *ast.Symbol, bound refinementsets.RefinedSet, starDepth int, boundObject ObjectAnnotationRef) AbstractValue {
	return AbstractValue{Kind: KindVariable, Symbol: symbol, Bound: bound, StarDepth: starDepth, BoundObject: boundObject}
}

// KnownValues is knownValues in the TS source.
//
// Every constructor REQUIRES its trust level (finding 11): an omitted
// grade used to default to the strongest claim, so a call site could
// overclaim by silence. Now the compiler makes every construction state
// the boundary it crossed.
func KnownValues(values []float64, kindTag PrimitiveKind, grade TrustLevel) AbstractValue {
	if grade == TrustProved {
		return AbstractValue{Kind: KindValues, Values: values, KindTag: kindTag}
	}
	return AbstractValue{Kind: KindValues, Values: values, KindTag: kindTag, Grade: grade}
}

// IsNumericKind is isNumericKind in the TS source: whether a knowledge
// state can be read NUMERICALLY: the transfers and the set-building
// joins admit number- and boolean-sorted words (true ↦ 1 is the spec's
// own ToNumber); a string or array word read as numbers would be the
// cross-sort reread.
func IsNumericKind(k AbstractValue) bool {
	return k.Kind != KindValues || k.KindTag == PrimitiveNumber || k.KindTag == PrimitiveBoolean
}

// KnownSet is knownSet in the TS source.
//
// kindTag takes the place of TS's optional `kindTag?: "bigint" |
// "symbol"` parameter — pass SetKindTagNone when absent.
func KnownSet(set refinementsets.RefinedSet, temporal *refinementsets.TemporalAnnotation, grade TrustLevel, kindTag SetKindTag) AbstractValue {
	base := AbstractValue{Kind: KindSet, Set: set, Temporal: temporal, SetKindTag: kindTag}
	if grade == TrustProved {
		return base
	}
	base.Grade = grade
	return base
}

// KnownSetDense marks an ALREADY-BUILT KindSet value's repetition
// window as PROVED DENSE — every counted position is an own property,
// never a hole — the same fact KindArrayHoles's Dense/DenseKnown pair
// states for the all-holes shape. A new wrapper constructor rather
// than a widened KnownSet signature, per the package's convention
// (PossiblyAbsent beside PossiblyUndefined, KnownWithMeasures beside
// KnownSet): every existing KnownSet call site keeps compiling and
// keeps SeqDenseKnown=false unchanged, and only a caller that has
// actually proved density — a constructor whose own algorithm writes
// every counted index — reaches for this one.
//
// A no-op on anything that is not a set-kind repetition (kindTag !=
// none, or Set does not read back as a repetition/star at all): dense
// is a fact about the counted window, and there is no window to mark
// dense where AsRepetition refuses.
func KnownSetDense(known AbstractValue) AbstractValue {
	if known.Kind != KindSet || known.SetKindTag != SetKindTagNone {
		return known
	}
	if _, ok := refinementsets.AsRepetition(known.Set); !ok {
		return known
	}
	out := known
	out.SeqDense = true
	out.SeqDenseKnown = true
	return out
}

// KnownWithMeasures is knownWithMeasures in the TS source: the MEET of
// two claims that both hold of the same value: an exact word wins
// outright, plain sets intersect (their forms concatenate) with
// measures merged, and anything harder keeps the first claim. Sound
// because each input is already a true claim about the very same
// runtime value — the meet only ever says less than both together.
//
// hasMeasures takes the place of TS's `measures: {...} | undefined`
// parameter.
func KnownWithMeasures(known AbstractValue, measures *Measures) AbstractValue {
	if measures == nil || known.Kind != KindSet {
		return known
	}
	out := known
	out.Measures = measures
	return out
}
