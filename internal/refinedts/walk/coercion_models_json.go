// from evaluation/coercion_models.ts
//
// JSON.parse/JSON.stringify as coercion models: the method dispatcher,
// the exact-text serializer, the decoded-value reader, the determined
// grammar claim for unpinned parse text, the JSON.parse(JSON.stringify(x))
// round-trip recognizer, and its identity computation. Split from
// coercion_models.go per file-length discipline.

package walk

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// readJsonMethods is readJsonMethods in the TS source: JSON.parse and
// JSON.stringify as method calls — parse runs BEFORE the schema-parse
// model in the dispatcher's chain, which would otherwise swallow the
// `parse` name.
func readJsonMethods(site MethodCallSite) *abstractdomain.AbstractValue {
	ctx, env, e, receiverExpression, method := site.Ctx, site.Env, site.E, site.ReceiverExpression, site.Method
	call := e.AsCallExpression()
	var arguments []*ast.Node
	if call.Arguments != nil {
		arguments = call.Arguments.Nodes
	}
	isJSONReceiver := ast.IsIdentifier(receiverExpression) && receiverExpression.Text() == "JSON" && resolvesToDefaultLib(ctx, receiverExpression)
	if isJSONReceiver && method == "parse" && len(arguments) == 1 {
		// JSON.parse(JSON.stringify(x)): the round trip through the SAME
		// process never leaves the JS value domain — parse re-seeds
		// whatever set x itself carried (jsonRoundTripOf's own doc),
		// rather than routing through an intermediate exact string that
		// only an ALREADY-exact x could produce. Checked on the argument
		// EXPRESSION (the nested call's own shape), not on text's
		// evaluated kind, so a windowed (non-exact) x is recognized here
		// before falling to the exact-text or unknown-text arms below.
		if stringifyArgument, isStringifyCall := jsonStringifyCallArgument(arguments[0]); isStringifyCall {
			inner := evaluateExpression(ctx, env, stringifyArgument)
			if roundTripped, ok := jsonRoundTripOf(inner); ok {
				return &roundTripped
			}
		}
		text := evaluateExpression(ctx, env, arguments[0])
		if text.Kind == abstractdomain.KindValues && text.KindTag == abstractdomain.PrimitiveString {
			var parsed any
			err := json.Unmarshal([]byte(stringOf(text.Values)), &parsed)
			if err != nil {
				out := silence.Residue()
				return &out
			}
			grade := abstractdomain.MinTrustLevel(abstractdomain.TrustLevelOf(text), abstractdomain.TrustSpec)
			out := jsonToKnown(parsed, grade)
			return &out
		}
		// unknown text: the result is whatever JSON value the text
		// spells — sec-json.parse's own grammar is the answer (a
		// number, a string, a boolean, null, or an object — JSONNumber /
		// JSONString / JSONBooleanLiteral / JSONNullLiteral /
		// JSONObject in sec-json.parse's production, with a JSONArray
		// folding under the same object-shaped claim CheckObjectKnown
		// already reads for "a string or an array"). This is DETERMINED
		// knowledge — every arm names an admitted JSON shape — so a
		// narrower declared position (Unit's [0, 1], Code's
		// /^[A-Z]{2}$/, a literal-true type) refutes the mismatched
		// arms outright (CheckKindUnion), rather than sitting undetermined:
		// an undetermined can never be designated (TESTING-TENETS.md), and
		// "any JSON value narrower than the target" is exactly the sound
		// refusal sec-json.parse's own contract already earns.
		const jsonParseOfUnknownTextSaid = "JSON.parse of unknown text yields whatever JSON " +
			"value the text spells — the type is everything this " +
			"file determines"
		if assignability.CollectingReasons() {
			assignability.NoteReason(assignability.ReasonNote{
				Site:        "expression",
				Node:        e,
				Said:        jsonParseOfUnknownTextSaid,
				Unsupported: false,
			})
		}
		out := anyJSONValue(abstractdomain.TrustSpec)
		// the union is DETERMINED (every arm is a real claim, never
		// KindUnknown), so the ordinary ResidueReason carrier — read only
		// off KindUnknown elsewhere — does not apply to it directly; but
		// CheckKindUnion (maybe_and_union.go) now reads a KindKindUnion's
		// own ResidueReason too (abstract_value.go's doc: its second
		// carrier), on BOTH the refutation path (an arm demonstrably
		// fails, e.g. the object arm against a scalar target) and the
		// generic-alert fallback (every arm judged, none refuted) — so
		// this sentence surfaces at the diagnostic either way, naming
		// JSON.parse rather than the arm's own internal shape or the
		// bare AlertText.
		out.ResidueReason = jsonParseOfUnknownTextSaid
		return &out
	}
	// JSON.stringify on exactly known structure: the serialization is
	// exactly specified (sec-json.stringify — default replacer and
	// space, own keys in insertion order), so the host computes the
	// exact text. The undef marker conflates undefined with null — the
	// two serialize differently, so it declines.
	if isJSONReceiver && method == "stringify" && len(arguments) == 1 {
		argKnown := evaluateExpression(ctx, env, arguments[0])
		floor := abstractdomain.TrustSpec
		text, ok := jsonStringifyOf(argKnown, func(grade abstractdomain.TrustLevel) { floor = abstractdomain.MinTrustLevel(floor, grade) })
		if ok {
			out := abstractdomain.KnownValues(refinementsets.CodepointsOf(text), abstractdomain.PrimitiveString, floor)
			return &out
		}
		// sec-json.stringify: the result is a String OR UNDEFINED — an
		// undefined, function, or symbol root serializes to nothing.
		// tsc's lib types it plain `string`, and prisma's defensive
		// `typeof wire !== 'string'` guard folded dead off that lie
		// until this arm told the spec's truth.
		inner := abstractdomain.KnownSet(refinementsets.Strings, nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone)
		out := abstractdomain.PossiblyUndefined(inner, "", false, false)
		return &out
	}
	return nil
}

// jsonStringifyOf builds JSON.stringify's exact text BY HAND, in the
// object's own key order — encoding/json.Marshal on a map SORTS keys
// alphabetically, which cannot reproduce a TS object literal's field
// order (PORT.md). This walks the SAME shapes JsValueExact admits
// (float64, string, bool, absence, list, complete object), reading
// AbstractValue.Keys directly instead of routing through the
// order-losing map form. noteGrade threads the trust floor the same
// way JsValueOf's reading parameter does. Returns ("", false) where
// any part is looser than a single value, mirroring JsValueOf's
// NOT_EXACT.
func jsonStringifyOf(known abstractdomain.AbstractValue, noteGrade func(grade abstractdomain.TrustLevel)) (string, bool) {
	if noteGrade != nil {
		noteGrade(abstractdomain.TrustLevelOf(known))
	}
	switch known.Kind {
	case abstractdomain.KindValues:
		if known.KindTag == abstractdomain.PrimitiveString {
			encoded, err := json.Marshal(stringOf(known.Values))
			if err != nil {
				return "", false
			}
			return string(encoded), true
		}
		if known.KindTag == abstractdomain.PrimitiveArray {
			var b strings.Builder
			b.WriteByte('[')
			for i, v := range known.Values {
				if i > 0 {
					b.WriteByte(',')
				}
				b.WriteString(jsonNumberString(v))
			}
			b.WriteByte(']')
			return b.String(), true
		}
		if known.KindTag == abstractdomain.PrimitiveBoolean {
			if len(known.Values) == 1 {
				if known.Values[0] != 0 {
					return "true", true
				}
				return "false", true
			}
			return "", false
		}
		if len(known.Values) == 1 {
			return jsonNumberString(known.Values[0]), true
		}
		return "", false
	case abstractdomain.KindUndef:
		return "", false // the marker conflates undefined (omitted) with null ("null") — not one exact text
	case abstractdomain.KindNull:
		return "null", true // sec-serializejsonproperty step 2: Type(null) is Object, and Quote/serialization writes the literal "null"
	case abstractdomain.KindNaN:
		return "null", true // sec-json.stringify: NaN and ±Infinity serialize as "null"
	case abstractdomain.KindObject:
		if !known.Complete {
			return "", false
		}
		var b strings.Builder
		b.WriteByte('{')
		wroteAny := false
		for _, key := range known.Keys {
			// a SYMBOL slot (#sym:…, keyed_slot_reads.go) is not a
			// String-valued key — SerializeJSONObject never writes it
			if symbolSlotKey(key.Name) {
				continue
			}
			// undefined VALUES are OMITTED from an object's serialization
			// (sec-serializejsonproperty step 8), not written as "null".
			// This test comes BEFORE the recursive serialize: KindUndef's
			// own arm answers ("", false) — it declines to name ONE exact
			// text for a value that could serialize as either nothing or
			// "null" depending on position — so recursing first threw the
			// whole object away instead of dropping the one key.
			if key.Value.Kind == abstractdomain.KindUndef {
				continue
			}
			value, ok := jsonStringifyOf(key.Value, noteGrade)
			if !ok {
				return "", false
			}
			if wroteAny {
				b.WriteByte(',')
			}
			nameEncoded, err := json.Marshal(key.Name)
			if err != nil {
				return "", false
			}
			b.Write(nameEncoded)
			b.WriteByte(':')
			b.WriteString(value)
			wroteAny = true
		}
		b.WriteByte('}')
		return b.String(), true
	case abstractdomain.KindList:
		var b strings.Builder
		b.WriteByte('[')
		for i, item := range known.Items {
			if i > 0 {
				b.WriteByte(',')
			}
			// undefined ITEMS in an array serialize as "null"
			// (sec-serializejsonarray step 8), never omitted
			if item.Kind == abstractdomain.KindUndef {
				b.WriteString("null")
				continue
			}
			value, ok := jsonStringifyOf(item, noteGrade)
			if !ok {
				return "", false
			}
			b.WriteString(value)
		}
		b.WriteByte(']')
		return b.String(), true
	case abstractdomain.KindCollection:
		// a Map or Set serializes as exactly "{}", whatever it holds. Its
		// entries live in [[MapData]]/[[SetData]], never as own
		// properties, so SerializeJSONObject's key list —
		// EnumerableOwnProperties(value, ~key~), sec-serializejsonobject
		// step 8 — is empty, and step 12 writes "{}" for an empty
		// partial. SerializeJSONProperty routes it there: a collection is
		// an Object, not callable and not an Array, carries no
		// [[NumberData]]/[[StringData]]/[[BooleanData]]/[[BigIntData]]
		// slot, and neither Map.prototype nor Set.prototype declares
		// toJSON (sec-serializejsonproperty steps 2 and 20). The
		// COMPLETENESS of the record does not enter: an entry is not an
		// own property either way, so an incomplete collection writes the
		// same exact text a complete one does.
		return "{}", true
	default:
		// set, variable, possiblyUndefined, possiblyNaN, kindUnion,
		// objectStar, promise, date, symbol, hostFunction, bigints,
		// regex, unknown: none of them pin one exact value. The
		// object-star in particular states no LENGTH, so there is not
		// even a position count to write brackets around.
		return "", false
	}
}

// jsonNumberString mirrors JS's JSON.stringify of a number: the
// spec-exact decimal form (Number::toString radix 10), with ±Infinity
// and NaN written "null" (sec-serializejsonproperty step 4 reads
// through Quote/ToString, and Number::toString has no representation
// for either — sec-json.stringify's own note says so explicitly).
func jsonNumberString(v float64) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return "null"
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// jsonToKnown is the TS source's inline toKnown: a decoded
// encoding/json value (float64 | string | bool | nil | []any |
// map[string]any) as AbstractValue knowledge, at the given grade.
func jsonToKnown(v any, grade abstractdomain.TrustLevel) abstractdomain.AbstractValue {
	switch value := v.(type) {
	case float64:
		return abstractdomain.KnownValues([]float64{value}, abstractdomain.PrimitiveNumber, grade)
	case string:
		return abstractdomain.KnownValues(refinementsets.CodepointsOf(value), abstractdomain.PrimitiveString, grade)
	case bool:
		n := float64(0)
		if value {
			n = 1
		}
		return abstractdomain.KnownValues([]float64{n}, abstractdomain.PrimitiveBoolean, grade)
	case nil:
		// JSON's null decodes as Go's untyped nil (encoding/json's own
		// mapping) — the parsed value is JS null (sec-json.parse's
		// JSONNullLiteral), never undefined; a record member holding
		// undefined is DROPPED before serialization (sec-
		// serializejsonproperty step 8) and so never reaches this
		// decode at all. Undef and Null read differently at a
		// possiblyUndefined target (RefutePossiblyAbsent's own
		// AbsentFlavorNullOnly branch), so the two are not
		// interchangeable here.
		return abstractdomain.AtTrustLevel(abstractdomain.Null, grade)
	case []any:
		items := make([]abstractdomain.AbstractValue, len(value))
		for i, item := range value {
			items[i] = jsonToKnown(item, grade)
		}
		return abstractdomain.KnownList(items, grade)
	case map[string]any:
		var keys []abstractdomain.ObjectKey
		for name, held := range value {
			keys = append(keys, abstractdomain.ObjectKey{Name: name, Value: jsonToKnown(held, grade)})
		}
		return abstractdomain.AtTrustLevel(abstractdomain.KnownObject(keys, nil, true, abstractdomain.TrustProved, false), grade)
	default:
		out := silence.Residue()
		return out
	}
}

// anyJSONValue is the determined claim sec-json.parse's own grammar
// makes about ANY value a successful parse can answer: a number, a
// string, a boolean, null, or an object (an array reads as an object
// too — sec-typeof-operator, and CheckObjectKnown's "a string or an
// array" wording already reads a bare KindObject arm that way against
// a sequence-shaped target). Every arm is DETERMINED (never Unknown),
// so KindUnionOf never collapses this to the residue it replaces —
// each arm instead judges on its own against the checked position
// (CheckKindUnion), and a target the JSON grammar cannot fit (Unit's
// [0, 1], a literal-true type, Code's pattern) is refuted through the
// arm that demonstrably fails, never left undetermined.
func anyJSONValue(grade abstractdomain.TrustLevel) abstractdomain.AbstractValue {
	numberGround := abstractdomain.KnownSet(refinementsets.MakeRefinedSet(), nil, grade, abstractdomain.SetKindTagNone)
	stringGround := abstractdomain.KnownSet(refinementsets.Strings, nil, grade, abstractdomain.SetKindTagNone)
	booleanGround := abstractdomain.KnownValues([]float64{0, 1}, abstractdomain.PrimitiveBoolean, grade)
	objectGround := abstractdomain.AtTrustLevel(abstractdomain.KnownObject(nil, nil, false, abstractdomain.TrustProved, false), grade)
	return abstractdomain.KindUnionOf([]abstractdomain.AbstractValue{
		numberGround,
		stringGround,
		booleanGround,
		abstractdomain.AtTrustLevel(abstractdomain.Null, grade),
		objectGround,
	})
}

// jsonStringifyCallArgument reports whether e is a call to
// JSON.stringify with exactly one argument, answering that argument's
// own expression node. Used to recognize JSON.parse(JSON.stringify(x))
// syntactically, at the argument-expression level — jsonRoundTripOf
// then reads x's OWN abstract value rather than the intermediate
// string jsonStringifyOf would have to reconstruct exactly.
//
// DELIBERATELY SYNTACTIC-NESTED-CALL-ONLY: a bare identifier bound
// through a CONST to `JSON.stringify(x)` earlier
// (`const encoded = JSON.stringify(v); … JSON.parse(encoded)`,
// B7.keep.join's own shape) is NOT resolved here, even though a const
// can never be REASSIGNED — because the object x itself can still be
// MUTATED IN PLACE between the stringify call and the later
// JSON.parse read (`v.a = v.a + 1`, B7.keep.write's own shape), and
// evaluating x's identifier at the LATER read site would silently
// read its post-mutation value while claiming it as the round trip of
// what was actually serialized earlier — a wrong answer, not merely
// an imprecision. A sound version of this widening needs a "no write
// to x's root name in any statement between the two points, across
// nested blocks" check (B7.keep.join's own const is declared OUTSIDE
// the if/else that later reads it, so a same-block-only or
// immediately-preceding-statement check is not enough either); no
// such statement-RANGE write-set utility exists yet in dataflowfacts
// or walk (AssignedNames/AssignedNameSet scan a whole subtree, not a
// range between two arbitrary points across block boundaries) — see
// AGENT-BRIEF.md.
func jsonStringifyCallArgument(e *ast.Node) (*ast.Node, bool) {
	if !ast.IsCallExpression(e) {
		return nil, false
	}
	call := e.AsCallExpression()
	if !ast.IsPropertyAccessExpression(call.Expression) {
		return nil, false
	}
	pa := call.Expression.AsPropertyAccessExpression()
	if !ast.IsIdentifier(pa.Expression) || pa.Expression.Text() != "JSON" || pa.Name().Text() != "stringify" {
		return nil, false
	}
	if call.Arguments == nil || len(call.Arguments.Nodes) != 1 {
		return nil, false
	}
	return call.Arguments.Nodes[0], true
}

// jsonRoundTripOf answers JSON.parse(JSON.stringify(value)) read AT
// value's own set, without ever materializing the intermediate text —
// sec-json.stringify then sec-json.parse compose to the identity on
// every value already exactly known (jsonStringifyOf/jsonToKnown
// already round-trip those losslessly; ok=false there defers to that
// existing exact-text path). What this adds is the WINDOWED case
// jsonStringifyOf cannot spell as one exact string: a scalar number
// set survives the trip unchanged (Number::toString then ToNumber is
// the identity on every finite double — sec-numeric-types-number-
// tostring, sec-json.parse's JSONNumber production), and an object's
// keys recurse the same way, with an undefined-valued key DROPPED
// (sec-serializejsonproperty step 8) rather than carried through.
// grade floors at TrustSpec, the same boundary the exact-text round
// trip already stamps (json.Unmarshal's own text crossing).
func jsonRoundTripOf(value abstractdomain.AbstractValue) (abstractdomain.AbstractValue, bool) {
	grade := abstractdomain.MinTrustLevel(abstractdomain.TrustLevelOf(value), abstractdomain.TrustSpec)
	switch value.Kind {
	case abstractdomain.KindSet:
		if value.SetKindTag != abstractdomain.SetKindTagNone {
			return abstractdomain.AbstractValue{}, false
		}
		// a string ground or pattern set round-trips through JSON the
		// same identity way a number window does (Quote then the
		// matching string literal production reads back the same
		// codepoints, sec-quotejsonstring / sec-json.parse's
		// JSONString) — KindOfClaim reads the SORT the set is grounded
		// on the identical way typeof discrimination does (a KindSet
		// never claims boolean — setSortOfForms answers only string,
		// number, or none), so only a numeric or string ground takes
		// this identity arm; anything else (a symbol-tagged set, or a
		// set this reader cannot sort) keeps the caller's existing
		// exact-text path.
		switch abstractdomain.KindOfClaim(value) {
		case abstractdomain.ClaimSortNumber, abstractdomain.ClaimSortString:
			return abstractdomain.KnownSet(value.Set, value.Temporal, grade, value.SetKindTag), true
		}
		return abstractdomain.AbstractValue{}, false
	case abstractdomain.KindObject:
		if !value.Complete {
			return abstractdomain.AbstractValue{}, false
		}
		var keys []abstractdomain.ObjectKey
		for _, key := range value.Keys {
			if symbolSlotKey(key.Name) {
				continue // never a String-valued key SerializeJSONObject writes
			}
			if key.Value.Kind == abstractdomain.KindUndef {
				continue // dropped, not written (sec-serializejsonproperty step 8)
			}
			held, ok := jsonRoundTripOf(key.Value)
			if !ok {
				if text, exact := jsonStringifyOf(key.Value, nil); exact {
					var parsed any
					if err := json.Unmarshal([]byte(text), &parsed); err == nil {
						held = jsonToKnown(parsed, grade)
						ok = true
					}
				}
			}
			if !ok {
				return abstractdomain.AbstractValue{}, false
			}
			keys = append(keys, abstractdomain.ObjectKey{Name: key.Name, Value: held})
		}
		return abstractdomain.AtTrustLevel(abstractdomain.KnownObject(keys, nil, true, abstractdomain.TrustProved, false), grade), true
	default:
		return abstractdomain.AbstractValue{}, false
	}
}
