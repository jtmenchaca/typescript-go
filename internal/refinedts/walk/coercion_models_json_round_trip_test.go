// Ports the coverage the A2–A5.edge.json, B7.keep.offset,
// B7.keep.trans, and B7.use.sink e2e rows exercise:
// JSON.parse(JSON.stringify(x)) threading x's own (possibly windowed,
// non-exact) set through the round trip instead of routing through an
// intermediate exact string only an already-exact x could produce,
// JSON.parse of genuinely unknown text answering a DETERMINED "any
// JSON value" claim instead of residue, and JSON's null (not
// undefined) reading for a decoded Go nil.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// jsonToKnown(nil, …): a decoded JSON null must read as KindNull, not
// KindUndef — the two are DIFFERENT runtime values at a
// possiblyUndefined target (RefutePossiblyAbsent's own
// AbsentFlavorNullOnly branch), and a record member holding JS
// undefined never reaches this decode at all (sec-
// serializejsonproperty step 8 drops it before serialization).
func TestJsonToKnown_DecodedNilReadsAsNullNotUndef(t *testing.T) {
	got := jsonToKnown(nil, abstractdomain.TrustProved)
	if got.Kind != abstractdomain.KindNull {
		t.Fatalf("jsonToKnown(nil, TrustProved).Kind = %v, want KindNull", got.Kind)
	}
}

// jsonRoundTripOf on a windowed (non-exact) integer KindSet — the
// shape a guarded parameter carries (B7.keep.offset's `v` after
// `Number.isInteger(v) && v >= 0 && v <= 9`) — answers the SAME set
// back, at TrustSpec floor, rather than declining because
// jsonStringifyOf cannot spell one exact string for a whole window.
func TestJsonRoundTripOf_WindowedIntegerSetSurvivesUnchanged(t *testing.T) {
	window := abstractdomain.KnownSet(
		refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.AtMost(9), refinementsets.Integer),
		nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone,
	)
	got, ok := jsonRoundTripOf(window)
	if !ok {
		t.Fatalf("jsonRoundTripOf(windowed [0,9] integer) = ok=false, want a threaded-through set")
	}
	if got.Kind != abstractdomain.KindSet {
		t.Fatalf("jsonRoundTripOf(windowed [0,9] integer).Kind = %v, want KindSet", got.Kind)
	}
	gotRange := RangeOfSet(got.Set)
	if gotRange == nil || gotRange.Lo != 0 || gotRange.Hi != 9 {
		t.Errorf("jsonRoundTripOf(windowed [0,9] integer).Set = %+v, want the same [0,9] window", got.Set)
	}
	if abstractdomain.TrustLevelOf(got) != abstractdomain.TrustSpec {
		t.Errorf("jsonRoundTripOf(windowed [0,9] integer) grade = %v, want TrustSpec (the round trip's own floor)", abstractdomain.TrustLevelOf(got))
	}
}

// An UNBOUNDED integer set (B7.keep.offset's second function,
// v with no declared window, and B7.keep.trans's unboundedProducer)
// still threads through unchanged — jsonRoundTripOf never adds a
// bound the source value never had; the ASSIGNABILITY refusal against
// Age is a separate, already-sound question this function does not
// answer. RangeOfSet's own contract (number_range.go) makes nil mean
// "cannot enclose this set numerically at all" (a sequence form, the
// bare root) — never "no finite bound." A scalar integer set with no
// declared window legitimately encloses at ±Inf (Int:true still
// carries real information a nil return would lose, exactly as
// evaluate_property_access_length_meet_test.go's own unbounded-length
// case expects r.Hi == +Inf, not nil) — every real caller of
// RangeOfSet (math_unary_transfer.go, element_access.go,
// string_method_models.go, element_in_bounds.go,
// ranged_list_element.go) treats nil as "give up entirely," so
// jsonRoundTripOf threading the SAME ±Inf window the input already
// carried is the correct, unchanged answer.
func TestJsonRoundTripOf_UnboundedIntegerSetSurvivesUnbounded(t *testing.T) {
	unbounded := abstractdomain.KnownSet(
		refinementsets.MakeRefinedSet(refinementsets.Integer),
		nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone,
	)
	got, ok := jsonRoundTripOf(unbounded)
	if !ok {
		t.Fatalf("jsonRoundTripOf(unbounded integer) = ok=false, want a threaded-through set")
	}
	gotRange := RangeOfSet(got.Set)
	wantRange := RangeOfSet(unbounded.Set)
	if gotRange == nil || wantRange == nil || *gotRange != *wantRange {
		t.Errorf("jsonRoundTripOf(unbounded integer).Set range = %+v, want the SAME window the input carried (%+v) — the round trip changes no bound", gotRange, wantRange)
	}
}

// jsonRoundTripOf on a record whose one key holds a windowed number:
// the recursion preserves the key's own window (not just its sort),
// and an undefined-valued key is DROPPED, never carried through as a
// key holding undefined (sec-serializejsonproperty step 8).
func TestJsonRoundTripOf_ObjectRecursesPerKeyAndDropsUndefined(t *testing.T) {
	windowed := abstractdomain.KnownSet(
		refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.AtMost(150), refinementsets.Integer),
		nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone,
	)
	record := abstractdomain.KnownObject([]abstractdomain.ObjectKey{
		{Name: "age", Value: windowed},
		{Name: "note", Value: abstractdomain.Undef},
	}, nil, true, abstractdomain.TrustProved, false)

	got, ok := jsonRoundTripOf(record)
	if !ok {
		t.Fatalf("jsonRoundTripOf(record{age: windowed, note: undefined}) = ok=false, want a threaded-through object")
	}
	if got.Kind != abstractdomain.KindObject {
		t.Fatalf("jsonRoundTripOf(record).Kind = %v, want KindObject", got.Kind)
	}
	var ageValue *abstractdomain.AbstractValue
	for i := range got.Keys {
		if got.Keys[i].Name == "note" {
			t.Errorf("jsonRoundTripOf(record) kept the undefined-valued key %q — sec-serializejsonproperty step 8 drops it", got.Keys[i].Name)
		}
		if got.Keys[i].Name == "age" {
			ageValue = &got.Keys[i].Value
		}
	}
	if ageValue == nil {
		t.Fatalf("jsonRoundTripOf(record) dropped the age key — want it to survive with its own window")
	}
	ageRange := RangeOfSet(ageValue.Set)
	if ageRange == nil || ageRange.Lo != 0 || ageRange.Hi != 150 {
		t.Errorf("jsonRoundTripOf(record).age = %+v, want the [0,150] window preserved", ageValue.Set)
	}
}

// A NaN-carrying value (B7.use.sink's `JSON.stringify(NaN)`) is not a
// KindSet or KindObject — jsonRoundTripOf's own doc says the existing
// exact-text path (jsonStringifyOf answers "null" for KindNaN,
// json.Unmarshal decodes it, jsonToKnown now answers Null) already
// round-trips it losslessly, so this function correctly defers
// (ok=false) rather than duplicating that path.
func TestJsonRoundTripOf_NaNDefersToTheExactTextPath(t *testing.T) {
	_, ok := jsonRoundTripOf(abstractdomain.NaNValue)
	if ok {
		t.Errorf("jsonRoundTripOf(NaN) = ok=true, want ok=false (NaN already round-trips exactly through jsonStringifyOf+jsonToKnown)")
	}
}

// jsonStringifyCallArgument recognizes JSON.parse's own argument
// shape syntactically: JSON.stringify(x) with exactly one argument
// answers x's own expression node; anything else (a bare identifier,
// a different callee, a wrong argument count) answers ok=false so the
// caller falls through to the exact-text/unknown-text arms unchanged.
func TestJsonStringifyCallArgument_RecognizesTheOneArgumentShape(t *testing.T) {
	p := entryEnvTestProgram(t,
		"function f(v: number): string {\n"+
			"  const nested = JSON.stringify(v);\n"+
			"  const bare = v;\n"+
			"  void bare;\n"+
			"  return nested;\n"+
			"}\n")
	fn := entryEnvFunctionNamed(t, p, "f")

	stringifyCall := superArrayFirstNode(t, fn.Body(), "the JSON.stringify(v) call", func(node *ast.Node) bool {
		if !ast.IsCallExpression(node) {
			return false
		}
		call := node.AsCallExpression()
		if !ast.IsPropertyAccessExpression(call.Expression) {
			return false
		}
		pa := call.Expression.AsPropertyAccessExpression()
		return ast.IsIdentifier(pa.Expression) && pa.Expression.Text() == "JSON" && pa.Name().Text() == "stringify"
	})

	argument, ok := jsonStringifyCallArgument(stringifyCall)
	if !ok {
		t.Fatalf("jsonStringifyCallArgument(JSON.stringify(v)) = ok=false, want true")
	}
	if !ast.IsIdentifier(argument) || argument.Text() != "v" {
		t.Errorf("jsonStringifyCallArgument(JSON.stringify(v)) = %v, want the identifier v", argument)
	}

	bareIdentifier := superArrayFirstNode(t, fn.Body(), "the bare `v` initializer", func(node *ast.Node) bool {
		return ast.IsIdentifier(node) && node.Text() == "v" && node != argument
	})
	if _, ok := jsonStringifyCallArgument(bareIdentifier); ok {
		t.Errorf("jsonStringifyCallArgument(a bare identifier) = ok=true, want false")
	}
}

// jsonStringifyCallArgument does NOT resolve through a const
// indirection (`const encoded = JSON.stringify(v); …
// JSON.parse(encoded)`, B7.keep.join's own shape), even though a
// const identifier can never be REASSIGNED — because the referenced
// object can still be MUTATED IN PLACE between the two statements
// (`v.a = v.a + 1`, B7.keep.write's own shape: a currently-firing
// false positive this exact indirection would make WORSE, not better,
// by claiming the round trip of v's POST-mutation value as what the
// earlier stringify actually serialized). See coercion_models.go's
// own doc on jsonStringifyCallArgument and AGENT-BRIEF.md for the
// sound fix this still needs (a statement-range write-set check that
// does not exist yet).
func TestJsonStringifyCallArgument_DoesNotFollowAConstBoundIndirection(t *testing.T) {
	p := entryEnvTestProgram(t,
		"function f(v: number): number {\n"+
			"  const encoded = JSON.stringify(v);\n"+
			"  const decoded = JSON.parse(encoded);\n"+
			"  return decoded;\n"+
			"}\n")
	fn := entryEnvFunctionNamed(t, p, "f")

	parseArgument := superArrayFirstNode(t, fn.Body(), "the `encoded` identifier read by JSON.parse", func(node *ast.Node) bool {
		return ast.IsIdentifier(node) && node.Text() == "encoded" && node.Parent != nil && ast.IsCallExpression(node.Parent)
	})

	if _, ok := jsonStringifyCallArgument(parseArgument); ok {
		t.Errorf("jsonStringifyCallArgument(encoded, a const bound to JSON.stringify(v)) = ok=true, want false — "+
			"resolving this without a between-the-points write check would be unsound for a mutated receiver")
	}
}

// anyJSONValue: every arm sec-json.parse's own grammar admits is
// DETERMINED (never KindUnknown) — the fix this pins directly, since
// KindUnionOf collapses the whole union to Unknown the moment any one
// arm is Unknown (abstract_value.go's own rule), which is exactly the
// residue this function replaces.
func TestAnyJSONValue_EveryArmIsDetermined(t *testing.T) {
	got := anyJSONValue(abstractdomain.TrustSpec)
	if got.Kind == abstractdomain.KindUnknown {
		t.Fatalf("anyJSONValue(TrustSpec) = Unknown, want a determined KindKindUnion")
	}
	if got.Kind != abstractdomain.KindKindUnion {
		t.Fatalf("anyJSONValue(TrustSpec).Kind = %v, want KindKindUnion", got.Kind)
	}
	for i, arm := range got.Arms {
		if arm.Kind == abstractdomain.KindUnknown {
			t.Errorf("anyJSONValue(TrustSpec).Arms[%d] = Unknown, want every arm determined", i)
		}
	}
}

// anyJSONValue's object arm is what lets CheckObjectKnown refute a
// scalar-only target (Unit, Code, a literal-true type) outright — the
// arm must actually be KindObject, incomplete (unstated keys), for
// that dispatch to fire.
func TestAnyJSONValue_CarriesAnObjectArm(t *testing.T) {
	got := anyJSONValue(abstractdomain.TrustSpec)
	sawObject := false
	for _, arm := range got.Arms {
		if arm.Kind == abstractdomain.KindObject {
			sawObject = true
			if arm.Complete {
				t.Errorf("anyJSONValue(TrustSpec)'s object arm is Complete — want incomplete (unstated keys), the same claim jsxElementValue/Object.fromEntries already build")
			}
		}
	}
	if !sawObject {
		t.Errorf("anyJSONValue(TrustSpec).Arms = %+v, want one KindObject arm (JSON's object/array grammar)", got.Arms)
	}
}

// TestA5_edge_json_StringifyDropsAnUndefinedValuedKey pins
// A5.edge.json's own claim: `JSON.stringify({ a: undefined })` is
// exactly "{}".
//
// sec-serializejsonproperty step 8 — a property whose value serializes
// to undefined is DROPPED from the object text, never written as
// "null" (that reading belongs to an ARRAY item,
// sec-serializejsonarray step 8, which the list arm already spells).
//
// The bug this pins: KindUndef's own arm answers ("", false), since a
// bare undefined names no ONE exact text. Recursing into the key's
// value BEFORE testing for it therefore threw the whole object away —
// the drop check below could never be reached, and the row went
// undetermined. The test would pass trivially against a checker that
// simply declined, so the ok flag is asserted too.
func TestA5_edge_json_StringifyDropsAnUndefinedValuedKey(t *testing.T) {
	object := abstractdomain.KnownObject(
		[]abstractdomain.ObjectKey{{Name: "a", Value: abstractdomain.Undef}},
		nil, true, abstractdomain.TrustProved, false,
	)
	got, ok := jsonStringifyOf(object, nil)
	if !ok {
		t.Fatalf("jsonStringifyOf({a: undefined}) declined, want an exact text")
	}
	if got != "{}" {
		t.Errorf("jsonStringifyOf({a: undefined}) = %q, want \"{}\" (sec-serializejsonproperty step 8 drops the key)", got)
	}
}

// A present key beside a dropped one still writes, with no stray comma
// — the drop must not disturb the separator bookkeeping.
func TestA5_edge_json_StringifyKeepsPresentKeysBesideADroppedOne(t *testing.T) {
	one := abstractdomain.KnownValues([]float64{1}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
	object := abstractdomain.KnownObject(
		[]abstractdomain.ObjectKey{
			{Name: "a", Value: abstractdomain.Undef},
			{Name: "b", Value: one},
		},
		nil, true, abstractdomain.TrustProved, false,
	)
	got, ok := jsonStringifyOf(object, nil)
	if !ok {
		t.Fatalf("jsonStringifyOf({a: undefined, b: 1}) declined, want an exact text")
	}
	if got != `{"b":1}` {
		t.Errorf("jsonStringifyOf({a: undefined, b: 1}) = %q, want `{\"b\":1}`", got)
	}
}

// The ARRAY reading is the opposite one and must not have moved: an
// undefined ITEM serializes as "null" (sec-serializejsonarray step 8),
// never dropped — the position is meaningful in an array.
func TestA5_edge_json_StringifyWritesAnUndefinedArrayItemAsNull(t *testing.T) {
	list := abstractdomain.KnownList(
		[]abstractdomain.AbstractValue{abstractdomain.Undef}, abstractdomain.TrustProved,
	)
	got, ok := jsonStringifyOf(list, nil)
	if !ok {
		t.Fatalf("jsonStringifyOf([undefined]) declined, want an exact text")
	}
	if got != "[null]" {
		t.Errorf("jsonStringifyOf([undefined]) = %q, want \"[null]\" (sec-serializejsonarray step 8)", got)
	}
}
