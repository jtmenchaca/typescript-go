// Shared type→value recipes both adapters call. A form added here is
// what both readers seed; an adapter that inlines its own boolean or
// star is the next mirror drift.

package typereading

import (
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// BooleanCodes is booleanCodes in the TS source.
func BooleanCodes() abstractdomain.AbstractValue {
	return abstractdomain.KnownSet(
		refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{0, 1})),
		nil,
		abstractdomain.TrustProved,
		abstractdomain.SetKindTagNone,
	)
}

// StringGround is stringGround in the TS source.
func StringGround() abstractdomain.AbstractValue {
	return abstractdomain.KnownSet(refinementsets.Strings, nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone)
}

// NumberWithNaN is numberWithNaN in the TS source.
func NumberWithNaN() abstractdomain.AbstractValue {
	return abstractdomain.PossiblyNaN(
		abstractdomain.KnownSet(refinementsets.Numbers, nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone),
	)
}

// UnknownSymbol is unknownSymbol in the TS source.
func UnknownSymbol() abstractdomain.AbstractValue {
	return abstractdomain.AbstractValue{Kind: abstractdomain.KindSymbol}
}

// StarOfElement is starOfElement in the TS source: a sequence of this
// element -- NaN riding on the element rides on the star (`number[]`).
//
// Two layers, one recipe. An element the tuple layer holds stars into
// a refined SET the kernel decides. An element the OBJECT GRAPH holds
// -- a record, a class instance -- stars into the object-star instead:
// the same "these elements, length unstated" claim, carried where the
// graph can answer it. The zero value with ok=false is left for an
// element neither layer holds (an unknown), where no position claim
// exists to state.
func StarOfElement(element abstractdomain.AbstractValue) (abstractdomain.AbstractValue, bool) {
	nanRides := false
	inner := element
	if inner.Kind == abstractdomain.KindPossiblyNaN {
		nanRides = true
		inner = *inner.Inner
	}
	items, ok := abstractdomain.SetOfKnown(inner)
	if !ok {
		// an element the tuple layer cannot hold — a record, a class
		// instance — is a sequence claim all the same: every position
		// holds that element, at a length the type does not state. The
		// object-star carries exactly that, and nothing more.
		return abstractdomain.KnownObjectStar(inner, abstractdomain.TrustProved)
	}
	worn := abstractdomain.KnownSet(
		refinementsets.MakeRefinedSet(refinementsets.Star(items)),
		nil,
		abstractdomain.TrustProved,
		abstractdomain.SetKindTagNone,
	)
	if nanRides && worn.Kind == abstractdomain.KindSet {
		worn.NaNElements = true
	}
	return worn, true
}

// PresentUnion is presentUnion in the TS source: union arms after
// absent branches were set aside. An unknown arm dissolves the whole
// -- a partial union would claim less than the value can hold. The
// zero value with ok=false stands in for the TS source's null.
func PresentUnion(arms []abstractdomain.AbstractValue, sawAbsent bool) (abstractdomain.AbstractValue, bool) {
	if len(arms) == 0 {
		return abstractdomain.AbstractValue{}, false
	}
	present := arms[0]
	if len(arms) > 1 {
		present = abstractdomain.KindUnionOf(arms)
	}
	if present.Kind == abstractdomain.KindUnknown {
		return abstractdomain.AbstractValue{}, false
	}
	if sawAbsent {
		return abstractdomain.PossiblyUndefined(present, "", false, false), true
	}
	return present, true
}

// FillObjectKeys is fillObjectKeys in the TS source: host fills keys
// syntax skipped. Syntax wins on overlap (it read that member); host
// supplies the rest. Incomplete: a structural type admits keys it
// does not name.
func FillObjectKeys(syntax, host abstractdomain.AbstractValue) abstractdomain.AbstractValue {
	merged := make(map[string]abstractdomain.AbstractValue, len(host.Keys)+len(syntax.Keys))
	var order []string
	for _, k := range host.Keys {
		if _, seen := merged[k.Name]; !seen {
			order = append(order, k.Name)
		}
		merged[k.Name] = k.Value
	}
	for _, k := range syntax.Keys {
		if _, seen := merged[k.Name]; !seen {
			order = append(order, k.Name)
		}
		merged[k.Name] = k.Value
	}
	keys := make([]abstractdomain.ObjectKey, len(order))
	for i, name := range order {
		keys[i] = abstractdomain.ObjectKey{Name: name, Value: merged[name]}
	}
	stated := syntax.Stated
	if stated == nil {
		stated = host.Stated
	}
	bareProto := syntax.BareProto || host.BareProto
	return abstractdomain.KnownObject(keys, stated, false, abstractdomain.TrustProved, bareProto)
}
