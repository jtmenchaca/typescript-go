// SpellForMemoKey must spell every value json.Marshal alone cannot:
// ±Inf set bounds (the bare z.number() set is atLeast(-Inf)) and
// non-finite exact values. A refusal here silently unkeys the inline
// memo — every such inline re-walks its callee body.

package abstractdomain

import (
	"math"
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

func TestSpellForMemoKeyBareNumberSet(t *testing.T) {
	known := KnownSet(refinementsets.Numbers, nil, TrustProved, SetKindTagNone)
	if _, ok := SpellForMemoKey(known); !ok {
		t.Errorf("the bare z.number() set (atLeast(-Inf)) must spell")
	}
}

func TestSpellForMemoKeyInfiniteValues(t *testing.T) {
	posInf := KnownValues([]float64{math.Inf(1)}, PrimitiveNumber, TrustProved)
	negInf := KnownValues([]float64{math.Inf(-1)}, PrimitiveNumber, TrustProved)
	posKey, ok := SpellForMemoKey(posInf)
	if !ok {
		t.Fatalf("+Inf value must spell")
	}
	negKey, ok := SpellForMemoKey(negInf)
	if !ok {
		t.Fatalf("-Inf value must spell")
	}
	if posKey == negKey {
		t.Errorf("+Inf and -Inf must spell distinctly")
	}
}

func TestSpellForMemoKeyNaNValueDistinct(t *testing.T) {
	nan := AbstractValue{Kind: KindValues, Values: []float64{math.NaN()}, KindTag: PrimitiveNumber}
	inf := AbstractValue{Kind: KindValues, Values: []float64{math.Inf(1)}, KindTag: PrimitiveNumber}
	nanKey, ok := SpellForMemoKey(nan)
	if !ok {
		t.Fatalf("a NaN value must spell")
	}
	infKey, ok := SpellForMemoKey(inf)
	if !ok {
		t.Fatalf("+Inf value must spell")
	}
	if nanKey == infKey {
		t.Errorf("NaN and +Inf must spell distinctly — JSON.stringify collapsed both to null")
	}
}

func TestSpellForMemoKeyVariableInfiniteBound(t *testing.T) {
	known := AbstractValue{Kind: KindVariable, Bound: refinementsets.Numbers}
	if _, ok := SpellForMemoKey(known); !ok {
		t.Errorf("a variable bound by the bare number set must spell")
	}
}

func TestSpellForMemoKeyDeterministic(t *testing.T) {
	known := KnownSet(refinementsets.MakeRefinedSet(
		refinementsets.AtLeast(0),
		refinementsets.Union(
			refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{1, 2})),
			refinementsets.MakeRefinedSet(refinementsets.Above(10)),
		),
	), nil, TrustProved, SetKindTagNone)
	first, ok := SpellForMemoKey(known)
	if !ok {
		t.Fatalf("a nested set must spell")
	}
	second, _ := SpellForMemoKey(known)
	if first != second {
		t.Errorf("the same knowledge must key identically")
	}
}
