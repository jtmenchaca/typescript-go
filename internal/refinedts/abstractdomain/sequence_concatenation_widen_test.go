package abstractdomain

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// deepConcatenationChain is a right-nested Concatenation chain of the
// given depth over single-codepoint OneOf leaves — the same shape
// StringTuple builds per character (codepoint_sets.go), used here to
// pin the widening bound at an exact depth without touching the
// kernel.
func deepConcatenationChain(depth int) refinementsets.RefinedSet {
	set := refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{97}))
	for i := 0; i < depth; i++ {
		set = refinementsets.MakeRefinedSet(refinementsets.Concatenation(
			refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{97})), set))
	}
	return set
}

// TestSequenceNestingDepthCountsAConcatenationChain pins the measure
// itself: a chain built by wrapping N Concatenation layers around a
// leaf reads back as nesting depth N.
func TestSequenceNestingDepthCountsAConcatenationChain(t *testing.T) {
	for _, depth := range []int{0, 1, 17, 64, 65, 100} {
		set := deepConcatenationChain(depth)
		if got := refinementsets.SequenceNestingDepth(set); got != depth {
			t.Errorf("SequenceNestingDepth(chain of depth %d) = %d, want %d", depth, got, depth)
		}
	}
}

// TestJoinKnownWidensPastTheBoundInsteadOfGrowing is the fix's own
// pin: a join whose operand already sits past
// sequenceConcatenationWidenBound answers the string ground (Strings)
// rather than building an even deeper Concatenation/Union — the
// widening ReduceCSSCalc.ts's hang required (loop_candidate.go's
// settle loop, and any other repeated join, would otherwise hand the
// kernel's seqSubset ask an ever-deeper term every round).
func TestJoinKnownWidensPastTheBoundInsteadOfGrowing(t *testing.T) {
	tooDeep := KnownSet(deepConcatenationChain(sequenceConcatenationWidenBound+1), nil, TrustProved, SetKindTagNone)
	other := KnownValues([]float64{98, 99}, PrimitiveString, TrustProved) // "bc"
	joined := JoinKnown(tooDeep, other)
	if joined.Kind != KindSet {
		t.Fatalf("JoinKnown(tooDeep, \"bc\").Kind = %v, want KindSet (widened to Strings)", joined.Kind)
	}
	if !refinementsets.IsStringGround(joined.Set) {
		t.Errorf("JoinKnown(tooDeep, \"bc\").Set = %+v, want the string ground (Strings)", joined.Set)
	}
	// the same join, arguments swapped — the widening must not depend on
	// which side carries the too-deep chain
	joinedReversed := JoinKnown(other, tooDeep)
	if joinedReversed.Kind != KindSet || !refinementsets.IsStringGround(joinedReversed.Set) {
		t.Errorf("JoinKnown(\"bc\", tooDeep).Set = %+v, want the string ground, argument order reversed", joinedReversed.Set)
	}
}

// TestJoinKnownAtExactlyTheBoundStillJoinsNormally pins the boundary:
// a chain at EXACTLY sequenceConcatenationWidenBound layers deep is
// not touched by the widening (only strictly past the bound is), so
// this join still reaches the ordinary string-word union path rather
// than widening early.
func TestJoinKnownAtExactlyTheBoundStillJoinsNormally(t *testing.T) {
	atBound := KnownSet(deepConcatenationChain(sequenceConcatenationWidenBound), nil, TrustProved, SetKindTagNone)
	other := KnownValues([]float64{98, 99}, PrimitiveString, TrustProved) // "bc"
	joined := JoinKnown(atBound, other)
	if joined.Kind != KindSet {
		t.Fatalf("JoinKnown(atBound, \"bc\").Kind = %v, want KindSet", joined.Kind)
	}
	if refinementsets.IsStringGround(joined.Set) {
		t.Errorf("JoinKnown(atBound, \"bc\") widened to Strings at exactly the bound; want the ordinary union path (widening triggers only strictly past the bound)")
	}
}
