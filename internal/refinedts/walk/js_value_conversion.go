// from evaluation/js_value_conversion.ts
//
// ONE AbstractValue <-> concrete-JS-value converter (finding 22):
// every consumer — the host-computed builtin rows, JSON.stringify,
// and the zod parse evaluator — converts through the same core, so
// two conversions of the same knowledge can never disagree. The
// enumeration is EXHAUSTIVE and every unlisted richness declines —
// a fallthrough here once turned a Date into the exact value
// `undefined`.

package walk

import (
	"math"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// absentMarker is ABSENT in the TS source: the absent value (null and
// undefined, conflated the way the model's marker conflates them).
// TS's `unique symbol` has no direct Go twin; a private sentinel type
// (compared by identity, like the TS symbol) is the substitute.
type absentMarkerType struct{}

var absentMarker = &absentMarkerType{}

// notExactMarker is NOT_EXACT in the TS source: the marker for "this
// knowledge is not an exact JS value".
type notExactMarkerType struct{}

var notExactMarker = &notExactMarkerType{}

// JsValue is the TS source's JsValue union: number | string | boolean
// | typeof ABSENT | readonly JsValue[] | { [key: string]: JsValue }.
// Go has no closed union type; this is carried as `any` holding one
// of: float64, string, bool, *absentMarkerType, []JsValue, or
// map[string]JsValue -- the same shapes the TS union names, checked
// by the same type switches the TS source's own consumers use.
type JsValue = any

// JsValueExact is jsValueExact in the TS source: THE core: the exact
// JS value a knowledge state pins, or nil where any part is looser
// than a single value. Absence comes out as the absentMarker;
// noteGrade threads the trust floor of every part the walk visits
// (nil when the caller does not want it, matching the TS optional
// parameter).
func JsValueExact(known abstractdomain.AbstractValue, noteGrade func(grade abstractdomain.TrustLevel)) JsValue {
	if noteGrade != nil {
		noteGrade(abstractdomain.TrustLevelOf(known))
	}
	switch known.Kind {
	case abstractdomain.KindValues:
		if known.KindTag == abstractdomain.PrimitiveString {
			return stringOf(known.Values)
		}
		if known.KindTag == abstractdomain.PrimitiveArray {
			out := make([]JsValue, len(known.Values))
			for i, v := range known.Values {
				out[i] = v
			}
			return out
		}
		if known.KindTag == abstractdomain.PrimitiveBoolean {
			if len(known.Values) == 1 {
				return known.Values[0] != 0
			}
			return nil
		}
		if len(known.Values) == 1 {
			return known.Values[0]
		}
		return nil
	case abstractdomain.KindUndef:
		return absentMarker
	case abstractdomain.KindNaN:
		return math.NaN()
	case abstractdomain.KindObject:
		// only a COMPLETE object names every key it has, and one
		// non-exact key voids the whole map. Both consumers READ THE MAP
		// AS TOTAL and would turn a partial one into a wrong answer, so
		// the all-or-nothing rule is the sound one here:
		//   - JSON.stringify writes every key it is handed and nothing
		//     else, so a partial map prints a shorter object as if it
		//     were the whole one.
		//   - the zod parse evaluator reads a key's ABSENCE from the map
		//     as the schema throwing (parse_evaluator's object arm), and
		//     reads the unnamed keys to decide stripping versus
		//     strictObject's throw — a partial map fabricates a throw.
		// A caller that only needs ONE key does not come through here at
		// all: object_key_access reads a stated key straight off
		// known.Keys, incomplete object or not.
		if !known.Complete {
			return nil
		}
		out := map[string]JsValue{}
		for _, key := range known.Keys {
			value := JsValueExact(key.Value, noteGrade)
			if value == nil {
				return nil
			}
			out[key.Name] = value
		}
		return out
	case abstractdomain.KindList:
		// one non-exact item voids the whole list, for the same reason
		// the object case gives: both consumers index the list by
		// position and read it as total.
		out := make([]JsValue, 0, len(known.Items))
		for _, item := range known.Items {
			value := JsValueExact(item, noteGrade)
			if value == nil {
				return nil
			}
			out = append(out, value)
		}
		return out
	default:
		// set, variable, possiblyUndefined, possiblyNaN, kindUnion,
		// objectStar, collection, promise, date, symbol, hostFunction,
		// bigints, regex, unknown: none of them pin one exact value. The
		// object-star states no LENGTH, so it names no list of positions
		// to hand a consumer that reads its result as total.
		return nil
	}
}

// resolveAbsent is resolveAbsent in the TS source: ABSENT resolved
// for a host-level consumer: mapped to nil (the TS source's
// `undefined`) where the reading admits absence, or the whole value
// declined (notExactMarker) where it does not (the undef marker
// conflates undefined with null, which serialize differently).
func resolveAbsent(value JsValue, admitAbsent bool) any {
	if value == absentMarker {
		if admitAbsent {
			return nil
		}
		return notExactMarker
	}
	if list, ok := value.([]JsValue); ok {
		out := make([]any, 0, len(list))
		for _, item := range list {
			resolved := resolveAbsent(item, admitAbsent)
			if resolved == notExactMarker {
				return notExactMarker
			}
			out = append(out, resolved)
		}
		return out
	}
	if m, ok := value.(map[string]JsValue); ok {
		out := map[string]any{}
		for key, held := range m {
			resolved := resolveAbsent(held, admitAbsent)
			if resolved == notExactMarker {
				return notExactMarker
			}
			out[key] = resolved
		}
		return out
	}
	return value
}

// JsValueOfReading is the TS source's inline `reading` parameter
// object of jsValueOf: `{ admitAbsent: boolean; noteGrade?: ... }`.
type JsValueOfReading struct {
	AdmitAbsent bool
	NoteGrade   func(grade abstractdomain.TrustLevel)
}

// JsValueOf is jsValueOf in the TS source: the exact JS value for the
// host-computed rows — the core with absence resolved per the
// reading, and the trust floor threaded through NoteGrade. Passing
// the TS default `{ admitAbsent: true }` is the caller's job here
// (Go has no parameter defaults); JsValueOfDefault gives it.
func JsValueOf(known abstractdomain.AbstractValue, reading JsValueOfReading) any {
	exact := JsValueExact(known, reading.NoteGrade)
	if exact == nil {
		return notExactMarker
	}
	return resolveAbsent(exact, reading.AdmitAbsent)
}

// JsValueOfDefault calls JsValueOf with the TS source's default
// reading, `{ admitAbsent: true }`.
func JsValueOfDefault(known abstractdomain.AbstractValue) any {
	return JsValueOf(known, JsValueOfReading{AdmitAbsent: true})
}

// IsNotExact reports whether a JsValueOf/JsValueOfDefault result is
// the NOT_EXACT marker.
func IsNotExact(v any) bool {
	return v == notExactMarker
}

// AbstractValueOfJs is abstractValueOfJs in the TS source: the
// knowledge state an exact JS value pins — the reverse direction, at
// the stated trust level. A flat number array reads as the exact
// tuple (the richer claim); anything nested stays a list.
func AbstractValueOfJs(value JsValue, grade abstractdomain.TrustLevel) abstractdomain.AbstractValue {
	switch v := value.(type) {
	case float64:
		if math.IsNaN(v) {
			return abstractdomain.NaNValue
		}
		return abstractdomain.KnownValues([]float64{v}, abstractdomain.PrimitiveNumber, grade)
	case string:
		return abstractdomain.KnownValues(refinementsets.CodepointsOf(v), abstractdomain.PrimitiveString, grade)
	case bool:
		val := float64(0)
		if v {
			val = 1
		}
		return abstractdomain.KnownValues([]float64{val}, abstractdomain.PrimitiveBoolean, grade)
	case *absentMarkerType:
		return abstractdomain.Undef
	case []JsValue:
		flat := true
		numbers := make([]float64, len(v))
		for i, item := range v {
			n, ok := item.(float64)
			if !ok || math.IsNaN(n) {
				flat = false
				break
			}
			numbers[i] = n
		}
		if flat {
			return abstractdomain.KnownValues(numbers, abstractdomain.PrimitiveArray, grade)
		}
		items := make([]abstractdomain.AbstractValue, len(v))
		for i, item := range v {
			items[i] = AbstractValueOfJs(item, grade)
		}
		return abstractdomain.KnownList(items, grade)
	case map[string]JsValue:
		var keys []abstractdomain.ObjectKey
		for key, held := range v {
			keys = append(keys, abstractdomain.ObjectKey{Name: key, Value: AbstractValueOfJs(held, grade)})
		}
		return abstractdomain.KnownObject(keys, nil, true, grade, false)
	default:
		return abstractdomain.Undef
	}
}
