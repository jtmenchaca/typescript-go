// The effect wire's newer forms — the state constant that carries the
// absent flag, the sequence concatenation, the or-absent index read,
// and the call that applies a callee's summary — beside the pins that
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

func TestAStateConstantCarriesTheUndefNullAndNanFlagsBesideItsSet(t *testing.T) {
	got := EffectWire(AbsentConst())
	want := `{"set":{"forms":[{"form":"oneOf","w":[]}]},"undef":true,"null":false,"nan":false}`
	if got != want {
		t.Errorf("EffectWire(AbsentConst) = %q, want %q", got, want)
	}
}

func TestAVarStateEffectWiresTheVerbatimCopyByIndex(t *testing.T) {
	got := EffectWire(LoopEffect{Kind: LoopEffectVarState, Index: 3})
	want := `{"varState":3}`
	if got != want {
		t.Errorf("EffectWire(varState) = %q, want %q", got, want)
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

func TestAnOrAbsentEffectWiresItsOperandUnderTheOneField(t *testing.T) {
	elem := LoopEffect{Kind: LoopEffectVar, Index: 2}
	got := EffectWire(LoopEffect{Kind: LoopEffectOrAbsent, A: &elem})
	want := `{"orAbsent":{"var":2}}`
	if got != want {
		t.Errorf("EffectWire(orAbsent) = %q, want %q", got, want)
	}
}

func TestACallStatementWiresItsCalleeArgsAndRets(t *testing.T) {
	got := StmtWire(IrStatement{
		Kind:   IrStatementCall,
		Callee: 1,
		Args: []LoopEffect{
			{Kind: LoopEffectVar, Index: 0},
			{Kind: LoopEffectVar, Index: 3},
		},
		Rets: []int{2, -1},
	})
	want := `{"call":{"callee":1,"args":[{"var":0},{"var":3}],"rets":[2,null]}}`
	if got != want {
		t.Errorf("StmtWire(call) = %q, want %q", got, want)
	}
}

func TestACallWithNoArgsOrRetsWiresEmptyLists(t *testing.T) {
	got := StmtWire(IrStatement{Kind: IrStatementCall, Callee: 0})
	want := `{"call":{"callee":0,"args":[],"rets":[]}}`
	if got != want {
		t.Errorf("StmtWire(call, empty) = %q, want %q", got, want)
	}
}

func TestTheOpaqueBranchWiresBothArmsAndNoTest(t *testing.T) {
	got := StmtWire(IrStatement{
		Kind: IrStatementBranchBoth,
		Then: []IrStatement{{
			Kind:   IrStatementAssign,
			Target: 0,
			Effect: LoopEffect{
				Kind: LoopEffectConst,
				Set:  refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{1})),
			},
		}},
		Else: []IrStatement{{
			Kind:   IrStatementAssign,
			Target: 0,
			Effect: LoopEffect{
				Kind: LoopEffectConst,
				Set:  refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{2})),
			},
		}},
	})
	// 2 spells (num 1, exp 1) — the canonical dyadic form the encoder
	// normalizes to
	want := `{"branchBoth":{"thn":[{"assign":{"target":0,"e":{"set":{"forms":[{"form":"oneOf","w":[{"num":1,"exp":0}]}]}}}}],` +
		`"els":[{"assign":{"target":0,"e":{"set":{"forms":[{"form":"oneOf","w":[{"num":1,"exp":1}]}]}}}}]}}`
	if got != want {
		t.Errorf("StmtWire(branchBoth) = %q, want %q", got, want)
	}
}

func TestAnOpaqueBranchWithNoElseArmWiresAnEmptyElseList(t *testing.T) {
	got := StmtWire(IrStatement{Kind: IrStatementBranchBoth})
	want := `{"branchBoth":{"thn":[],"els":[]}}`
	if got != want {
		t.Errorf("StmtWire(branchBoth, empty) = %q, want %q", got, want)
	}
}

func TestTheEqUndefAndEqNullBranchTestsWireBareWithNoWOperand(t *testing.T) {
	gotUndef := StmtWire(IrStatement{
		Kind: IrStatementBranch,
		On:   0,
		Test: IrTestEqUndef,
	})
	wantUndef := `{"branch":{"on":0,"test":"eqUndef","then":[],"else":[]}}`
	if gotUndef != wantUndef {
		t.Errorf("StmtWire(eqUndef) = %q, want %q", gotUndef, wantUndef)
	}
	gotNull := StmtWire(IrStatement{
		Kind: IrStatementBranch,
		On:   1,
		Test: IrTestEqNull,
	})
	wantNull := `{"branch":{"on":1,"test":"eqNull","then":[],"else":[]}}`
	if gotNull != wantNull {
		t.Errorf("StmtWire(eqNull) = %q, want %q", gotNull, wantNull)
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
