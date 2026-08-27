// An element read off a KIND UNION receiver — the shape a JSON parse
// of unread text answers (coercion_models_json.go's anyJSONValue: one
// arm per JSON shape).
//
// The runtime value is on exactly one arm, and the read lands on that
// arm. So what `u[i]` can answer is the JOIN over the arms' own element
// readings: every value the read can produce is a value some arm
// produces, which is precisely what JoinKnown states.
//
// A SCALAR arm — a number, a boolean, null — has no element position at
// all. sec-ordinaryget finds no own numeric property on it and walks the
// prototype chain, which for Number.prototype / Boolean.prototype
// carries no numeric slot, so the read is exactly undefined. (A null
// arm does not even get that far: an element read on null throws. The
// join treats it as undefined too — a throwing run completes nothing,
// so a summary over the runs that RETURN claims nothing false by leaving
// it out, the same stance the reduce lowering takes toward its own
// throwing case.)
//
// A STRING arm is the one scalar-looking arm that does read a position:
// `"abc"[0]` is "a". It is handled by the ordinary set reading below
// rather than being called absent.

package walk

import (
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// unionElementOf reads one position off every arm of a kind union and
// joins the answers. nil where any arm's own reading declines — the
// join would otherwise claim the union answers something sharper than
// one of its arms can support.
func unionElementOf(
	receiver abstractdomain.AbstractValue,
	index abstractdomain.AbstractValue,
) *abstractdomain.AbstractValue {
	if len(receiver.Arms) == 0 {
		return nil
	}
	var joined abstractdomain.AbstractValue
	hasJoined := false
	for _, arm := range receiver.Arms {
		read := armElementOf(arm, index)
		if read == nil {
			return nil
		}
		if !hasJoined {
			joined, hasJoined = *read, true
			continue
		}
		joined = abstractdomain.JoinKnown(joined, *read)
	}
	if !hasJoined {
		return nil
	}
	out := abstractdomain.AtTrustLevel(joined,
		abstractdomain.MinTrustLevel(abstractdomain.TrustLevelOf(joined), abstractdomain.TrustLevelOf(receiver)))
	return &out
}

// armElementOf is one arm's own element reading. nil where the arm
// states a sequence whose position this reader cannot pin.
func armElementOf(
	arm abstractdomain.AbstractValue,
	index abstractdomain.AbstractValue,
) *abstractdomain.AbstractValue {
	switch arm.Kind {
	case abstractdomain.KindValues:
		// a scalar tuple reads its own position; a boolean or plain
		// number word holds none
		if arm.KindTag != abstractdomain.PrimitiveArray && arm.KindTag != abstractdomain.PrimitiveString {
			out := abstractdomain.Undef
			return &out
		}
		if index.Kind == abstractdomain.KindValues && len(index.Values) == 1 && isInteger(index.Values[0]) {
			at := index.Values[0]
			if at >= 0 && int(at) < len(arm.Values) {
				kindTag := abstractdomain.PrimitiveNumber
				if arm.KindTag == abstractdomain.PrimitiveString {
					kindTag = abstractdomain.PrimitiveString
				}
				out := abstractdomain.KnownValues([]float64{arm.Values[int(at)]}, kindTag, abstractdomain.TrustLevelOf(arm))
				return &out
			}
			out := abstractdomain.Undef
			return &out
		}
		return nil

	case abstractdomain.KindList:
		if index.Kind == abstractdomain.KindValues && len(index.Values) == 1 && isInteger(index.Values[0]) {
			at := index.Values[0]
			if at >= 0 && int(at) < len(arm.Items) {
				out := arm.Items[int(at)]
				return &out
			}
			out := abstractdomain.Undef
			return &out
		}
		return nil

	case abstractdomain.KindSet:
		if arm.SetKindTag != abstractdomain.SetKindTagNone {
			return nil
		}
		// a SEQUENCE-shaped set states an element at every position; a
		// scalar-shaped one states no position at all
		if rep, ok := refinementsets.AsRepetition(arm.Set); ok {
			read := abstractdomain.KnownSet(rep.Element, nil, abstractdomain.TrustLevelOf(arm), abstractdomain.SetKindTagNone)
			// sec-ordinaryget: an unvouched index may sit out of range,
			// where the get answers exactly undefined
			out := abstractdomain.PossiblyAbsent(read, abstractdomain.AbsentFlavorUndefOnly, abstractdomain.TrustSpec, true, true)
			return &out
		}
		if abstractdomain.KindOfClaim(arm) == abstractdomain.ClaimSortNumber ||
			abstractdomain.KindOfClaim(arm) == abstractdomain.ClaimSortBoolean {
			out := abstractdomain.Undef
			return &out
		}
		return nil

	case abstractdomain.KindObject:
		// an object arm reads its own key set, and this is the numeric-
		// index spelling of that read — the whole-object reading belongs
		// to the KindObject arm in element_access.go, which this reader
		// deliberately does not duplicate. An object arm therefore stops
		// the join rather than being guessed at.
		return nil

	case abstractdomain.KindNull, abstractdomain.KindUndef:
		// an element read on null or undefined THROWS
		// (sec-getvalue's RequireObjectCoercible). A throwing run
		// completes nothing, so a claim about the runs that return says
		// nothing false by leaving this arm out — the same stance the
		// reduce lowering takes toward its own throwing case. Undef is
		// the join-neutral answer that carries no set.
		out := abstractdomain.Undef
		return &out

	case abstractdomain.KindNaN, abstractdomain.KindBigints, abstractdomain.KindSymbol:
		out := abstractdomain.Undef
		return &out

	default:
		return nil
	}
}
