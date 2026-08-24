package kernelbridge

import (
	"math"
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

func TestAScalarSetEncodesToTheKernelsWire(t *testing.T) {
	got := EncodeSet(refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.Integer))
	want := `{"forms":[{"form":"atLeast","a":{"num":0,"exp":0}},{"form":"integer"}]}`
	if got != want {
		t.Errorf("EncodeSet = %q, want %q", got, want)
	}
}

func TestEndpointsAndMembersCarryExactPairsInfinitiesCarryStrings(t *testing.T) {
	got := EncodeSet(refinementsets.MakeRefinedSet(refinementsets.AtLeast(math.Inf(-1))))
	want := `{"forms":[{"form":"atLeast","a":"-inf"}]}`
	if got != want {
		t.Errorf("EncodeSet(atLeast(-Inf)) = %q, want %q", got, want)
	}

	got = EncodeSet(refinementsets.MakeRefinedSet(refinementsets.MultipleOf(0.25)))
	want = `{"forms":[{"form":"multipleOf","d":{"num":1,"exp":-2}}]}`
	if got != want {
		t.Errorf("EncodeSet(multipleOf(0.25)) = %q, want %q", got, want)
	}

	got = EncodeSet(refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{0.5, math.Inf(1)})))
	want = `{"forms":[{"form":"oneOf","w":[{"num":1,"exp":-1},"+inf"]}]}`
	if got != want {
		t.Errorf("EncodeSet(oneOf([0.5, +Inf])) = %q, want %q", got, want)
	}
}

func TestTuplesAreListsOfNumbers(t *testing.T) {
	got := EncodeTuple([]float64{3, 0.5})
	want := `[{"num":3,"exp":0},{"num":1,"exp":-1}]`
	if got != want {
		t.Errorf("EncodeTuple([3, 0.5]) = %q, want %q", got, want)
	}

	got = EncodeTuple([]float64{})
	want = "[]"
	if got != want {
		t.Errorf("EncodeTuple([]) = %q, want %q", got, want)
	}
}

func TestRecursiveFormsNestSets(t *testing.T) {
	got := EncodeSet(refinementsets.MakeRefinedSet(refinementsets.Star(refinementsets.Numbers)))
	want := `{"forms":[{"form":"star","A":{"forms":[{"form":"atLeast","a":"-inf"}]}}]}`
	if got != want {
		t.Errorf("EncodeSet(star(numbers)) = %q, want %q", got, want)
	}
}

// TestWordEncodesToTheFixedWireContract pins the wire contract a Word
// leaf crosses at: `{"form":"word","w":[<numbers>]}`, the "w" array
// carrying each codepoint through the identical number encoding oneOf
// already uses (marshalWireValue over WireNumberOf) -- the Lean side's
// own decoder reads this exact shape.
func TestWordEncodesToTheFixedWireContract(t *testing.T) {
	got := EncodeSet(refinementsets.MakeRefinedSet(refinementsets.Word([]float64{104, 105})))
	want := `{"forms":[{"form":"word","w":[{"num":104,"exp":0},{"num":105,"exp":0}]}]}`
	if got != want {
		t.Errorf("EncodeSet(word([104,105])) = %q, want %q", got, want)
	}

	// an empty Word (never built by StringTuple, which spells the empty
	// string as emptyTuple, but a direct construction still crosses
	// correctly) spells an empty "w" array
	got = EncodeSet(refinementsets.MakeRefinedSet(refinementsets.Word([]float64{})))
	want = `{"forms":[{"form":"word","w":[]}]}`
	if got != want {
		t.Errorf("EncodeSet(word([])) = %q, want %q", got, want)
	}
}

// NOT PORTED: "a specification encodes nodes, paths, and objects" —
// encodeSpecification takes a Specification (object_graphs, unported).
// See wire_format.go's file comment; reported as blocked.
