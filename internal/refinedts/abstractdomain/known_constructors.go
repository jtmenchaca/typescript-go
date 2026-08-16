// Object and list constructors for AbstractValue — the rooted-keys
// object build (prototype-free record, grade floor over keys), the
// exact-length list build (grade floor over items), and the
// object-star build (one element, no length), plus the variant
// attachment that only ever adds precision to an object.

package abstractdomain

import "github.com/microsoft/typescript-go/internal/refinedts/refinementsets"

// emptyElementSet is the empty set, OneOf(nil): the "present element"
// claim a hole array wears — no scalar is a member, because there is
// no present element to be one. The same shape kernel_delegation.go's
// emptySet builds (package-private there); this is abstractdomain's
// own copy so KnownArrayHoles does not reach into walk.
var emptyElementSet = refinementsets.MakeRefinedSet(refinementsets.OneOf(nil))

// KnownObject is knownObject in the TS source.
//
// bareProto takes the place of TS's optional `bareProto?: true`
// parameter.
//
// The TS source roots `keys` on `Object.create(null)` before reading it,
// because the record is read with program-controlled names and a
// prototype-bearing record would answer the inherited
// Object.prototype.toString for keys["toString"] — the prototype-
// collision defect the zod survey catalogued. `ObjectKey` here is
// already an ordered slice of (name, value) pairs, not a Go map with a
// prototype, so there is no equivalent collision to guard against; the
// rooting step has no Go twin.
func KnownObject(
	keys []ObjectKey,
	stated ObjectAnnotationRef,
	complete bool,
	grade TrustLevel,
	bareProto bool,
) AbstractValue {
	// the object's ceiling includes its keys' — composition by minimum
	floor := grade
	for _, held := range keys {
		floor = MinTrustLevel(floor, TrustLevelOf(held.Value))
	}
	base := AbstractValue{Kind: KindObject, Keys: keys, Stated: stated, Complete: complete, BareProto: bareProto}
	if floor != TrustProved {
		base.Grade = floor
	}
	return base
}

// KnownWithVariants is knownWithVariants in the TS source: attach
// discriminated variants to an object knowledge state.
func KnownWithVariants(joint AbstractValue, variants []AbstractValue) AbstractValue {
	if joint.Kind != KindObject {
		return joint
	}
	out := joint
	out.Variants = variants
	return out
}

// KnownObjectStar builds the object-star: a sequence whose every
// position holds the given element, at a length the value does not
// state. It is the form for `Array.from(map.values())` over class
// instances and for a declared `Foo[]` — the element lives in the
// object graph, so there is no set to star and KindList would have to
// fabricate a count.
//
// What it claims is exactly one thing: each position, if it exists,
// holds the element. It claims NOTHING about how many positions there
// are — not even that there is one — so no read of a slot answers
// without absence, and `.length` answers only the sort.
//
// The element must be graph-shaped knowledge (an object, or a maybe
// over one). A SET element belongs in the star of the tuple layer,
// which the kernel decides; routing it here instead would hide it from
// every set decider. An unknown element states nothing to put at a
// position, so there is no sequence claim to build. Both answer
// (zero, false) and the caller keeps the answer it already had.
func KnownObjectStar(element AbstractValue, grade TrustLevel) (AbstractValue, bool) {
	if !objectShaped(element) {
		return AbstractValue{}, false
	}
	// the sequence's ceiling includes its element's — composition by
	// minimum, the same floor KnownList takes over its items
	floor := MinTrustLevel(grade, TrustLevelOf(element))
	elementCopy := element
	out := AbstractValue{Kind: KindObjectStar, Inner: &elementCopy}
	if floor != TrustProved {
		out.Grade = floor
	}
	return out, true
}

// objectShaped reports whether a value is knowledge the object graph
// holds — a record or class instance, or one of those beside absence.
// A maybe element is admitted because a sequence of maybe-absent
// records is an ordinary reading (`(Foo | undefined)[]`), and the
// element read below already re-wraps absence anyway.
func objectShaped(element AbstractValue) bool {
	if element.Kind == KindObject {
		return true
	}
	if element.Kind == KindPossiblyUndefined && element.Inner != nil {
		return objectShaped(*element.Inner)
	}
	return false
}

// ElementOfObjectStar is the element an object-star holds at one
// position. Callers that already tested the kind read it here rather
// than dereferencing Inner themselves.
func ElementOfObjectStar(k AbstractValue) (AbstractValue, bool) {
	if k.Kind != KindObjectStar || k.Inner == nil {
		return AbstractValue{}, false
	}
	return *k.Inner, true
}

// KnownList is knownList in the TS source.
func KnownList(items []AbstractValue, grade TrustLevel) AbstractValue {
	floor := grade
	for _, item := range items {
		floor = MinTrustLevel(floor, TrustLevelOf(item))
	}
	if floor == TrustProved {
		return AbstractValue{Kind: KindList, Items: items}
	}
	return AbstractValue{Kind: KindList, Items: items, Grade: floor}
}

// KnownArrayHoles builds the array-holes form from the TWO claims that
// together say everything `new Array(n)` past KnownList's
// materialization ceiling (arrayConstructionHoleLimit,
// walk/array_construction.go) is known to be: the length is EXACTLY
// length (wrapped as the ordinary KindValues scalar {n} — the same
// shape every other array kind's .length answers in), and the set of
// PRESENT elements is EMPTY (wrapped as the kernel's own ∅,
// OneOf(nil)) — a hole array has no present element to admit.
//
// dense is the sparse/dense bit (AbstractValue.Dense's doc): pass
// false for `new Array(n)` (sparse — sec-array never calls
// CreateDataPropertyOrThrow), true for `Array.from({length: n})`'s
// past-ceiling arm (dense — sec-array.from's array-like branch calls
// CreateDataPropertyOrThrow at every index). Both call sites know
// their density affirmatively, so KnownArrayHoles always sets
// DenseKnown true; a joined value that cannot establish either shape
// builds the struct literal directly with DenseKnown false instead of
// calling this constructor (JoinKnown's own array-holes/KindList arm).
// Every other claim this constructor builds (Length, ElementSet) is
// identical either way.
//
// The two claims stay separate rather than folding into one
// RepeatOf(∅, n, n) kernel-set question: that question denotes ∅
// itself for n > 0 (no tuple can fill n positions from an empty
// alphabet), which would be sound as "the present elements admit
// nothing" but UNSOUND as "the array is a member of nothing" — an
// assignability check reading the receiver's own Set would then see
// ∅ ⊆ every target and accept `new Array(n)` against any annotation.
// Keeping Length as its own scalar and ElementSet as its own (unequated)
// set sidesteps that: nothing ever asks the kernel whether the array
// is a member of ElementSet, only whether the EMPTY set has members
// worth handing back at an element read (it does not) — the walk's
// own read paths (evaluate_property_access.go, element_access.go)
// decide length and element reads directly off the two fields, the
// same way KindList's reads index Items rather than asking the kernel
// a question about the whole list.
//
// Go-native: no TS counterpart exists yet for this shape
// (array_construction.go itself has none either — the Array
// constructor's model is Go-first in this tree), so there is no TS
// source to port from or keep in step.
//
// length is a non-negative integer below 2^32 — the same ToUint32
// exactness sec-array's algorithm already proved before this is
// reached (ReadArrayConstruction never builds one for a length its own
// contract row would have thrown on).
func KnownArrayHoles(length int, grade TrustLevel, dense bool) AbstractValue {
	lengthClaim := KnownValues([]float64{float64(length)}, PrimitiveNumber, grade)
	if grade == TrustProved {
		return AbstractValue{Kind: KindArrayHoles, Inner: &lengthClaim, ElementSet: emptyElementSet, Dense: dense, DenseKnown: true}
	}
	return AbstractValue{Kind: KindArrayHoles, Inner: &lengthClaim, ElementSet: emptyElementSet, Grade: grade, Dense: dense, DenseKnown: true}
}

// LengthOfArrayHoles reads the exact length an array-holes value
// wears — the KnownValues {n} wrapped in Inner. Callers that already
// tested the kind read it here rather than re-deriving the KindValues
// shape themselves.
func LengthOfArrayHoles(k AbstractValue) (int, bool) {
	if k.Kind != KindArrayHoles || k.Inner == nil {
		return 0, false
	}
	inner := *k.Inner
	if inner.Kind != KindValues || len(inner.Values) != 1 {
		return 0, false
	}
	return int(inner.Values[0]), true
}
