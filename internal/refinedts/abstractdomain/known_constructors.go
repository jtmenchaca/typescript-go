// Object and list constructors for AbstractValue — the rooted-keys
// object build (prototype-free record, grade floor over keys) and
// the exact-length list build (grade floor over items), plus the
// variant attachment that only ever adds precision to an object.

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
