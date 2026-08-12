package refinementsets

import (
	"reflect"
	"testing"
)

func TestAStringIsItsCodepointTuple(t *testing.T) {
	if got := CodepointsOf("ab"); !reflect.DeepEqual(got, []float64{97, 98}) {
		t.Errorf("CodepointsOf(ab) = %v, want [97 98]", got)
	}
	if got := CodepointsOf(""); len(got) != 0 {
		t.Errorf("CodepointsOf(\"\") = %v, want []", got)
	}
	// a paired surrogate is ONE scalar value
	if got := CodepointsOf("🎉"); !reflect.DeepEqual(got, []float64{0x1F389}) {
		t.Errorf("CodepointsOf(party popper) = %v, want [0x1F389]", got)
	}
}

// TS: "a lone surrogate reads as itself — the set excludes it". A Go
// string cannot hold a lone UTF-16 surrogate (it is not valid UTF-8),
// so this case has no direct port -- see the substitution note on
// CodepointsOf in codepoint_sets.go. Not ported: no representable
// input.

func TestTheCodepointSetCarriesTheSurrogateGap(t *testing.T) {
	if !equalForm(Codepoints.Forms[0], Refinement{Form: FormInteger}) {
		t.Errorf("Codepoints.Forms[0] mismatch")
	}
	if Codepoints.Forms[1].Form != FormUnion {
		t.Errorf("Codepoints.Forms[1].Form = %v, want union", Codepoints.Forms[1].Form)
	}
}

func TestStringTupleAndThePatternSets(t *testing.T) {
	if StringTuple("").Forms[0].Form != FormEmptyTuple {
		t.Errorf("StringTuple(\"\").Forms[0].Form mismatch")
	}
	if !equalForm(StringTuple("a").Forms[0], Refinement{Form: FormOneOf, W: []float64{97}}) {
		t.Errorf("StringTuple(a).Forms[0] mismatch")
	}
	if StringTuple("ab").Forms[0].Form != FormConcatenation {
		t.Errorf("StringTuple(ab).Forms[0].Form mismatch")
	}
	if StartsWithSet("a").Forms[0].Form != FormConcatenation {
		t.Errorf("StartsWithSet(a).Forms[0].Form mismatch")
	}
}
