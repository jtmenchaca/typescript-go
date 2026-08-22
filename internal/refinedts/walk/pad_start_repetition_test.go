// The `.padStart(n, p)` row over a repetition-SHAPED receiver
// (repeat(S, lo, hi), not an exact value) — the digit-precision
// derivation's own live case: `String(year).padStart(4, "0")` where
// year's window forces the receiver to repeat(Digits, 4, 4) must stay
// EXACTLY repeat(Digits, 4, 4), not fall back to the sort-level
// Strings answer stringOutMethods used to hand back.
//
// StringPad only pads when maxLength exceeds the receiver's length
// (sec-stringpad step 2), so a window whose OWN lower bound already
// meets targetLength never pads at any length it admits — the
// receiver rides back unchanged. A window whose lower bound falls
// short gets an over-approximated pad prefix, repeat(codepoints-of-p,
// 0, n-lo), concatenated ahead of the receiver's own shape.
package walk

import (
	"reflect"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// padStartCallSite builds a MethodCallSite for `<receiver>.padStart(n,
// pad)` off a real parsed CallExpression node (readStringMethods
// dereferences site.E unconditionally on this branch, same
// requirement seq_prefix_slice_test.go's sites carry) — no kernel
// needed, since this row never asks one.
func padStartCallSite(t *testing.T, receiver abstractdomain.AbstractValue) MethodCallSite {
	t.Helper()
	p := entryEnvTestProgram(t, "function f(s: string): string {\n"+
		"  return s.padStart(4, \"0\");\n"+
		"}\n")
	ctx := superArrayContracts(t, p)
	fn := entryEnvFunctionNamed(t, p, "f")
	returned := superArrayFirstNode(t, fn.Body(), "return statement", ast.IsReturnStatement)
	call := returned.AsReturnStatement().Expression
	if !ast.IsCallExpression(call) {
		t.Fatalf("the return expression is not a call: %+v", call)
	}
	return MethodCallSite{
		Ctx:      ctx,
		Env:      NewEnv(),
		E:        call,
		Receiver: receiver,
		Method:   "padStart",
	}
}

func TestPadStartRepetition_AFourDigitFloorStaysExactlyTheDigitsWindow(t *testing.T) {
	// repeat(Digits, 4, 4) is what numericSetText derives for a
	// [1970,9999] year window through String(): every admitted length
	// already equals targetLength 4, so StringPad step 2 ("If
	// maxLength <= stringLength, return string") fires on every member
	// — the receiver must ride back UNCHANGED.
	four := 4
	receiverSet := refinementsets.Repetition(refinementsets.Digits, 4, &four)
	receiver := abstractdomain.KnownSet(receiverSet, nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone)
	site := padStartCallSite(t, receiver)
	argKnowns := []abstractdomain.AbstractValue{
		abstractdomain.KnownValues([]float64{4}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved),
		abstractdomain.KnownValues(refinementsets.CodepointsOf("0"), abstractdomain.PrimitiveString, abstractdomain.TrustProved),
	}
	got := readStringMethods(site, argKnowns, true, abstractdomain.TrustSpec)
	if got == nil {
		t.Fatalf(`readStringMethods(repeat(Digits,4,4).padStart(4,"0")) = nil, want the unchanged receiver`)
	}
	if got.Kind != abstractdomain.KindSet || got.SetKindTag != abstractdomain.SetKindTagNone {
		spelled, _ := abstractdomain.FormatAbstractValue(*got)
		t.Fatalf("got = %+v (%q), want a KindSet/SetKindTagNone value", got, spelled)
	}
	if !reflect.DeepEqual(got.Set, receiverSet) {
		spelled, _ := abstractdomain.FormatAbstractValue(*got)
		t.Errorf(`padStart(4,"0") over repeat(Digits,4,4) = %q, want the receiver's own set unchanged`, spelled)
	}
}

func TestPadStartRepetition_ABelowFloorWindowGetsAFourLengthPaddedShape(t *testing.T) {
	// a [5,120]-window value's decimal spelling is repeat(Digits, 1,
	// 3) (1..3 digits) — the lower bound (1) falls short of
	// targetLength 4, so the result over-approximates as
	// concatenation(repeat(pad-alphabet, 0, 3), repeat(Digits, 1, 3)):
	// a 4-length shape whose head admits the pad codepoint "0".
	three := 3
	receiverSet := refinementsets.Repetition(refinementsets.Digits, 1, &three)
	receiver := abstractdomain.KnownSet(receiverSet, nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone)
	site := padStartCallSite(t, receiver)
	argKnowns := []abstractdomain.AbstractValue{
		abstractdomain.KnownValues([]float64{4}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved),
		abstractdomain.KnownValues(refinementsets.CodepointsOf("0"), abstractdomain.PrimitiveString, abstractdomain.TrustProved),
	}
	got := readStringMethods(site, argKnowns, true, abstractdomain.TrustSpec)
	if got == nil {
		t.Fatalf(`readStringMethods(repeat(Digits,1,3).padStart(4,"0")) = nil, want the padded shape`)
	}
	if got.Kind != abstractdomain.KindSet || got.SetKindTag != abstractdomain.SetKindTagNone {
		spelled, _ := abstractdomain.FormatAbstractValue(*got)
		t.Fatalf("got = %+v (%q), want a KindSet/SetKindTagNone value", got, spelled)
	}
	if reflect.DeepEqual(got.Set, receiverSet) {
		t.Errorf("padStart over a below-floor window answered the receiver unchanged — the pad prefix must widen the shape")
	}
	// the exact shape this row builds: repeat(0, n-lo) over the pad
	// codepoint("0"), concatenated ahead of the receiver's own
	// repeat(Digits,1,3) — a 4-length-ceiling shape whose head admits
	// "0"
	padWindowHi := 3 // n(4) - lo(1)
	padAlphabet := refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{'0'}))
	wantPrefix := refinementsets.Repetition(padAlphabet, 0, &padWindowHi)
	want := refinementsets.MakeRefinedSet(refinementsets.Concatenation(wantPrefix, receiverSet))
	if !reflect.DeepEqual(got.Set, want) {
		spelled, _ := abstractdomain.FormatAbstractValue(*got)
		t.Errorf(`padStart(4,"0") over repeat(Digits,1,3) = %q (%+v), want concatenation(repeat("0",0,3), repeat(Digits,1,3))`, spelled, got.Set)
	}
}
