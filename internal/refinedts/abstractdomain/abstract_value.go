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
	KindVariable          Kind = "variable"
	KindList              Kind = "list"
	KindCollection        Kind = "collection"
	KindPromise           Kind = "promise"
	KindDate              Kind = "date"
	KindSymbol            Kind = "symbol"
	KindHostFunction      Kind = "hostFunction"
	KindBigints           Kind = "bigints"
	KindRegex             Kind = "regex"
	KindUndef             Kind = "undef"
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

	// "collection": a built Map or Set.
	CollectionFlavor Flavor
	Entries          []CollectionEntry

	// "promise" / "possiblyUndefined" / "possiblyNaN": the wrapped value.
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
func PossiblyUndefined(inner AbstractValue, wrapperGrade TrustLevel, hasWrapperGrade bool, provedAbsent bool) AbstractValue {
	if inner.Kind == KindUndef {
		return Undef
	}
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
	out := AbstractValue{Kind: KindPossiblyUndefined, Inner: &innerCopy, ProvedAbsent: provedAbsent}
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
