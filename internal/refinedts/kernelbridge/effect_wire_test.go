// The effect wire's newer forms — the state constant that carries the
// absent flag, and the sequence concatenation — beside the pins that
// keep the OLDER forms byte-identical, so a kernel built before them
// decodes an unchanged wire.
package kernelbridge

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

func TestAPlainConstEffectStillWiresWithoutFlags(t *testing.T) {
	got := EffectWire(LoopEffect{
		Kind: LoopEffectConst,
		Set:  refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{1})),
	})
	want := `{"set":{"forms":[{"form":"oneOf","w":[{"num":1,"exp":0}]}]}}`
	if got != want {
		t.Errorf("EffectWire(const) = %q, want %q", got, want)
	}
}

func TestAStateConstantCarriesTheAbsentAndNanFlagsBesideItsSet(t *testing.T) {
	got := EffectWire(AbsentConst())
	want := `{"set":{"forms":[{"form":"oneOf","w":[]}]},"absent":true,"nan":false}`
	if got != want {
		t.Errorf("EffectWire(AbsentConst) = %q, want %q", got, want)
	}
}

func TestAConcatenationEffectWiresItsTwoOperands(t *testing.T) {
	a := LoopEffect{Kind: LoopEffectVar, Index: 0}
	b := LoopEffect{Kind: LoopEffectVar, Index: 1}
	got := EffectWire(LoopEffect{Kind: LoopEffectConcat, A: &a, B: &b})
	want := `{"concat":[{"var":0},{"var":1}]}`
	if got != want {
		t.Errorf("EffectWire(concat) = %q, want %q", got, want)
	}
}

func TestTheSequenceEqualityGuardWiresWithOnBLikeTheScalarTwoSlotTests(t *testing.T) {
	if !IsTwoSlotTest(IrTestEqSeqSlot) {
		t.Fatalf("IsTwoSlotTest(eqSeqSlot) = false, want true")
	}
	got := StmtWire(IrStatement{
		Kind: IrStatementBranch,
		On:   0,
		Test: IrTestEqSeqSlot,
		OnB:  1,
	})
	want := `{"branch":{"on":0,"test":"eqSeqSlot","onB":1,"then":[],"else":[]}}`
	if got != want {
		t.Errorf("StmtWire(eqSeqSlot) = %q, want %q", got, want)
	}
}
