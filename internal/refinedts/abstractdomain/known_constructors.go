// Object and list constructors for AbstractValue — the rooted-keys
// object build (prototype-free record, grade floor over keys), the
// exact-length list build (grade floor over items), and the
// object-star build (one element, no length), plus the variant
// attachment that only ever adds precision to an object.

package abstractdomain

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
