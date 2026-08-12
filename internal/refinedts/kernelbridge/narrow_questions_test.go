package kernelbridge

import (
	"reflect"
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

func mustNotPanic(t *testing.T, name string, fn func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("%s: unexpected panic: %v", name, r)
		}
	}()
	fn()
}

func mustPanic(t *testing.T, name string, wantMessage string, fn func()) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			t.Errorf("%s: expected panic %q, got none", name, wantMessage)
			return
		}
		if got, ok := r.(string); !ok || got != wantMessage {
			t.Errorf("%s: panic = %v, want %q", name, r, wantMessage)
		}
	}()
	fn()
}

func TestStateWireDecodeWireStateRoundTrip(t *testing.T) {
	got := DecodeWireState(parseWire(t, StateWire(KnownStateWire{Top: true})))
	if !got.Top {
		t.Errorf("round trip top: got %+v", got)
	}

	known := KnownStateWire{
		Set:    refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.Integer),
		Absent: false,
		Nan:    true,
	}
	got = DecodeWireState(parseWire(t, StateWire(known)))
	if !reflect.DeepEqual(got, known) {
		t.Errorf("round trip known: got %+v, want %+v", got, known)
	}
}

func TestGateNarrowNaNEndpointsThrow(t *testing.T) {
	nan := math_NaN()
	mustPanic(t, "eq NaN", "a narrowing endpoint is NaN", func() {
		GateNarrow(NarrowTree{Kind: NarrowKindEq, K: nan})
	})
	mustPanic(t, "cmpSet NaN hi", "a narrowing endpoint is NaN", func() {
		GateNarrow(NarrowTree{Kind: NarrowKindCmpSet, Op: NarrowOpLt, Lo: 0, Hi: nan})
	})
	mustPanic(t, "not(cmp NaN)", "a narrowing endpoint is NaN", func() {
		inner := NarrowTree{Kind: NarrowKindCmp, Op: NarrowOpGe, K: nan}
		GateNarrow(NarrowTree{Kind: NarrowKindNot, A: &inner})
	})
	mustNotPanic(t, "isInt", func() {
		GateNarrow(NarrowTree{Kind: NarrowKindIsInt})
	})
	mustNotPanic(t, "eq 0", func() {
		GateNarrow(NarrowTree{Kind: NarrowKindEq, K: 0})
	})
}

func math_NaN() float64 {
	var zero float64
	return zero / zero
}

func TestDecodeNarrowAnswerBothClaimsOrNone(t *testing.T) {
	set := refinementsets.MakeRefinedSet(refinementsets.AtLeast(0))
	parsed := map[string]any{
		"whenTrue":  map[string]any{"set": parseWire(t, EncodeSet(set)), "strong": true},
		"whenFalse": map[string]any{"none": true},
	}
	got := DecodeNarrowAnswer(parsed)
	if got.WhenTrue == nil || !reflect.DeepEqual(got.WhenTrue.Set, set) || !got.WhenTrue.Strong {
		t.Errorf("WhenTrue = %+v", got.WhenTrue)
	}
	if got.WhenFalse != nil {
		t.Errorf("WhenFalse = %+v, want nil", got.WhenFalse)
	}
}

func TestLinearWireCoefficientsBoundStrictFlag(t *testing.T) {
	wire := LinearWire(LinearWireInput{
		Facts:  []LinearFact{{Coefs: []float64{1, -1}, Bound: 0, Strict: false}},
		Target: LinearFact{Coefs: []float64{1, 0}, Bound: 2, Strict: true},
	})
	parsed := parseWire(t, wire).(map[string]any)
	facts := parsed["facts"].([]any)
	fact0 := facts[0].(map[string]any)
	coefs0 := fact0["coefs"].([]any)
	if len(coefs0) != 2 || coefs0[0] != float64(1) || coefs0[1] != float64(-1) {
		t.Errorf("facts[0].coefs = %v, want [1, -1]", coefs0)
	}
	if _, hasStrict := fact0["strict"]; hasStrict {
		t.Errorf("facts[0].strict is present, want absent")
	}
	bound0 := fact0["bound"].(map[string]any)
	if bound0["num"].(float64)*pow2(bound0["exp"].(float64)) != 0 {
		t.Errorf("facts[0].bound = %v, want 0", bound0)
	}

	target := parsed["target"].(map[string]any)
	coefsT := target["coefs"].([]any)
	if len(coefsT) != 2 || coefsT[0] != float64(1) || coefsT[1] != float64(0) {
		t.Errorf("target.coefs = %v, want [1, 0]", coefsT)
	}
	if strict, _ := target["strict"].(bool); !strict {
		t.Errorf("target.strict = %v, want true", target["strict"])
	}
	boundT := target["bound"].(map[string]any)
	if boundT["num"].(float64)*pow2(boundT["exp"].(float64)) != 2 {
		t.Errorf("target.bound = %v, want 2", boundT)
	}
}
