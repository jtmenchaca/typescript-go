package objectgraphs

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// twoKeySpec is a one-object specification: a required integer key
// "age" ({1}) and an optional integer-sequence key "tag" ({0,1}).
func twoKeySpec() Specification {
	one := refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{1}))
	zeroOrOne := refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{0, 1}))
	return Specification{
		Nodes: []refinementsets.RefinedSet{
			refinementsets.MakeRefinedSet(), // the object node's own root set
			refinementsets.MakeRefinedSet(refinementsets.Integer),
			refinementsets.MakeRefinedSet(refinementsets.Star(
				refinementsets.MakeRefinedSet(refinementsets.Integer))),
		},
		Paths: []CardinalityPath{
			{Tail: 0, Count: one, Head: 1},
			{Tail: 0, Count: zeroOrOne, Head: 2},
		},
		Objects: []ObjectNode{{Node: 0, Keys: []ObjectKey{
			{Name: "age", Path: 0},
			{Name: "tag", Path: 1},
		}}},
	}
}

func TestValueWirePinsTwoKeyInstance(t *testing.T) {
	// "age" is a candidate WINDOW — the common case: what the checker
	// knows of a non-constant value crosses as its set, and the
	// kernel answers the subset question. "tag" is the exact word
	// "hi" (104, 105), crossing in EncodeTuple's compact {"w": …}
	// spelling.
	known := abstractdomain.AbstractValue{
		Kind: abstractdomain.KindObject,
		Keys: []abstractdomain.ObjectKey{
			{Name: "age", Value: abstractdomain.KnownSet(
				refinementsets.MakeRefinedSet(
					refinementsets.AtLeast(0), refinementsets.AtMost(120)),
				nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone)},
			{Name: "tag", Value: abstractdomain.KnownValues(
				[]float64{104, 105}, abstractdomain.PrimitiveString, abstractdomain.TrustProved)},
		},
	}
	values, reason := ValueAssignmentOf(twoKeySpec(), 0, known)
	if reason != "" {
		t.Fatalf("ValueAssignmentOf declined: %s", reason)
	}
	got := WireValueAssignment(values)
	// dyadics cross in canonical form: 120 = 15·2^3
	want := `{"root":0,"value":{"keys":[` +
		`{"name":"age","value":{"set":{"forms":[` +
		`{"form":"atLeast","a":{"num":0,"exp":0}},` +
		`{"form":"atMost","a":{"num":15,"exp":3}}]}}},` +
		`{"name":"tag","value":{"tuple":{"w":"hi"}}}]}}`
	if got != want {
		t.Errorf("value wire = %s, want %s", got, want)
	}
}

func TestValueEncodingDeclinesAWrappedKey(t *testing.T) {
	// a maybe-wrapped key value is a surviving boundary: the
	// wrapper's absent side meets the presence counts in its own
	// queued unit — the encoder declines loudly, never narrows
	inner := abstractdomain.KnownValues(
		[]float64{5}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
	known := abstractdomain.AbstractValue{
		Kind: abstractdomain.KindObject,
		Keys: []abstractdomain.ObjectKey{
			{Name: "age", Value: abstractdomain.PossiblyUndefined(
				inner, "", false, false)},
		},
	}
	_, reason := ValueAssignmentOf(twoKeySpec(), 0, known)
	want := "the key 'age' wears a maybe or NaN wrapper — the absent side meets the presence counts in its own unit"
	if reason != want {
		t.Errorf("decline reason = %q, want %q", reason, want)
	}
}

func TestValueEncodingScalarCandidatesCrossAsOneOf(t *testing.T) {
	// a scalar KindValues with several candidates is the finite
	// candidate set — judged by the same subset question
	known := abstractdomain.AbstractValue{
		Kind: abstractdomain.KindObject,
		Keys: []abstractdomain.ObjectKey{
			{Name: "age", Value: abstractdomain.KnownValues(
				[]float64{3, 5}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)},
		},
	}
	values, reason := ValueAssignmentOf(twoKeySpec(), 0, known)
	if reason != "" {
		t.Fatalf("ValueAssignmentOf declined: %s", reason)
	}
	got := WireValueAssignment(values)
	want := `{"root":0,"value":{"keys":[` +
		`{"name":"age","value":{"set":{"forms":[` +
		`{"form":"oneOf","w":[{"num":3,"exp":0},{"num":5,"exp":0}]}]}}}]}}`
	if got != want {
		t.Errorf("value wire = %s, want %s", got, want)
	}
}
