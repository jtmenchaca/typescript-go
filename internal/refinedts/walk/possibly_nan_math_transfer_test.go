// Pins the fix for A2.xfer.trig / A2.xfer.sign / A2.edge.process: a
// bare `number` operand arrives at Math.* already wrapped
// abstractdomain.PossiblyNaN(KnownSet(refinementsets.Numbers, …))
// (declared_value.go's AbstractValueOfDeclared,
// InitialStateOfPlainParameter). mathImage (math_transfer.go) used to
// read args through NumericOperand/SetOfKnownForTransfer with no
// KindPossiblyNaN case at all, so this exact shape declined every
// unary trig/sign question outright (silence.Residue()) rather than
// answering the real half's kernel window unioned with the NaN the
// spec's own NaN-in/NaN-out rows pin. Fixed by peeling the wrapper
// once (the same idiom TransferBinary/TransferBitwise already use)
// and re-wrapping the answer.

package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/narrowing"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// bareNumberOperand is the exact AbstractValue shape a bare `number`
// parameter/local wears — abstractdomain.PossiblyNaN wrapping the
// whole number ground (refinementsets.Numbers), TrustSpec-graded, the
// same shape AbstractValueOfDeclared's IsNumberGround arm and
// InitialStateOfPlainParameter both build.
func bareNumberOperand() abstractdomain.AbstractValue {
	inner := abstractdomain.KnownSet(refinementsets.Numbers, nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone)
	return abstractdomain.PossiblyNaN(inner)
}

// TestMathImage_SinOfBareNumberAnswersPossiblyNaNWindowNotResidue pins
// mechanism (1) for the trig row: Math.sin of a bare `number` operand
// must answer KindPossiblyNaN wrapping the kernel's own global [−1, 1]
// window (jsSin's window arm, sin.lean) — never silence.Residue().
func TestMathImage_SinOfBareNumberAnswersPossiblyNaNWindowNotResidue(t *testing.T) {
	loadTrigTestKernel(t)
	got, ok := TransferMathCall("sin", []abstractdomain.AbstractValue{bareNumberOperand()})
	if !ok {
		t.Fatalf("Math.sin(bare number): TransferMathCall answered not-transferred")
	}
	if got.Kind == abstractdomain.KindUnknown {
		t.Fatalf("Math.sin(bare number) = KindUnknown (residue), want KindPossiblyNaN wrapping [-1, 1]")
	}
	if got.Kind != abstractdomain.KindPossiblyNaN {
		t.Fatalf("Math.sin(bare number).Kind = %v, want KindPossiblyNaN — the operand's own NaN possibility must ride the answer", got.Kind)
	}
	bounds, boundsOk := narrowing.BoundsOfKnown(*got.Inner)
	if !boundsOk {
		t.Fatalf("Math.sin(bare number)'s real half poses no window: %+v", *got.Inner)
	}
	if bounds.Lo != -1 || bounds.Hi != 1 {
		t.Errorf("Math.sin(bare number)'s real half = [%v, %v], want [-1, 1]", bounds.Lo, bounds.Hi)
	}
}

// TestMathImage_CosOfBareNumberAnswersPossiblyNaNWindowNotResidue is
// sin's cos twin — sec-math.cos step 2 ("not finite" → NaN) folds NaN
// and ±∞ into one clause, unlike sin's separate steps 2/3, so this
// pins the same unwrap fix reaches cos too.
func TestMathImage_CosOfBareNumberAnswersPossiblyNaNWindowNotResidue(t *testing.T) {
	loadTrigTestKernel(t)
	got, ok := TransferMathCall("cos", []abstractdomain.AbstractValue{bareNumberOperand()})
	if !ok {
		t.Fatalf("Math.cos(bare number): TransferMathCall answered not-transferred")
	}
	if got.Kind != abstractdomain.KindPossiblyNaN {
		t.Fatalf("Math.cos(bare number).Kind = %v, want KindPossiblyNaN", got.Kind)
	}
	bounds, boundsOk := narrowing.BoundsOfKnown(*got.Inner)
	if !boundsOk {
		t.Fatalf("Math.cos(bare number)'s real half poses no window: %+v", *got.Inner)
	}
	if bounds.Lo != -1 || bounds.Hi != 1 {
		t.Errorf("Math.cos(bare number)'s real half = [%v, %v], want [-1, 1]", bounds.Lo, bounds.Hi)
	}
}

// TestMathImage_SignOfBareNumberAnswersPossiblyNaNThreePieceSet pins
// mechanism (1) for the sign row: sec-math.sign step 3/4 sends a real
// ±∞ operand to a REAL corner (±1), unlike sin/cos — so sign's answer
// over the whole number ground is {−1, 0, 1}, wrapped possibly-NaN,
// never a residue.
func TestMathImage_SignOfBareNumberAnswersPossiblyNaNThreePieceSet(t *testing.T) {
	loadTrigTestKernel(t)
	got, ok := TransferMathCall("sign", []abstractdomain.AbstractValue{bareNumberOperand()})
	if !ok {
		t.Fatalf("Math.sign(bare number): TransferMathCall answered not-transferred")
	}
	if got.Kind != abstractdomain.KindPossiblyNaN {
		t.Fatalf("Math.sign(bare number).Kind = %v, want KindPossiblyNaN", got.Kind)
	}
	inner := *got.Inner
	kernel := currentTransferKernel()
	if kernel == nil {
		t.Fatalf("no transfer kernel seated")
	}
	set, setOk := SetOfKnownForTransfer(inner)
	if !setOk {
		t.Fatalf("Math.sign(bare number)'s real half poses no set: %+v", inner)
	}
	for _, want := range []float64{-1, 0, 1} {
		if !kernel.Member(set, []float64{want}) {
			t.Errorf("Math.sign(bare number)'s real half excludes %v: %+v", want, set)
		}
	}
}
