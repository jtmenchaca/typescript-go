// Ports control_flow/walk_probe.test.ts. The flow walk's first
// probe: a hand-lowered body — an assignment through the transfers, a
// branch splitting on equality, arm-local assignments, and the join
// at the merge — walked by the kernel in one question, with the exit
// states read back by membership.
//
//	y = x + 1
//	if (x === 0) { y = 100 } else { y = y * 2 }
//
// with x known in [0, 10]: at exit x stays in [0, 10] with 0 still
// admitted, and y is 100 or [2, 22]. Skipped (never a faked pass)
// when the native kernel dylib is absent, the same gate
// kernelbridge's own round-trip tests use.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

func TestWalkProbe_TheKernelWalksALoweredBodyWholeAssignBranchJoin(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	entry := []kernelbridge.KnownStateWire{
		{Set: refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.AtMost(10))},
		{Top: true},
	}
	body := []kernelbridge.IrStatement{
		{
			Kind:   kernelbridge.IrStatementAssign,
			Target: 1,
			Effect: kernelbridge.LoopEffect{
				Kind: kernelbridge.LoopEffectBinary,
				Op:   kernelbridge.LoopOpAdd,
				A:    &kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectVar, Index: 0},
				B:    &kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectConst, Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{1}))},
			},
		},
		{
			Kind: kernelbridge.IrStatementBranch,
			On:   0,
			Test: kernelbridge.IrTestEq,
			W:    floatPtr(0),
			Then: []kernelbridge.IrStatement{
				{
					Kind:   kernelbridge.IrStatementAssign,
					Target: 1,
					Effect: kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectConst, Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{100}))},
				},
			},
			Else: []kernelbridge.IrStatement{
				{
					Kind:   kernelbridge.IrStatementAssign,
					Target: 1,
					Effect: kernelbridge.LoopEffect{
						Kind: kernelbridge.LoopEffectBinary,
						Op:   kernelbridge.LoopOpMul,
						A:    &kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectVar, Index: 1},
						B:    &kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectConst, Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{2}))},
					},
				},
			},
		},
	}
	exit := kernel.Walk(entry, body)
	if len(exit) != 2 {
		t.Fatalf("len(exit) = %d, want 2", len(exit))
	}

	x := exit[0]
	if x.Top {
		t.Fatalf("x.Top = true, want false")
	}
	if x.Absent {
		t.Errorf("x.Absent = true, want false")
	}
	if x.Nan {
		t.Errorf("x.Nan = true, want false")
	}
	if !kernel.Member(x.Set, []float64{0}) {
		t.Errorf("member(x, [0]) = false, want true")
	}
	if !kernel.Member(x.Set, []float64{10}) {
		t.Errorf("member(x, [10]) = false, want true")
	}
	if kernel.Member(x.Set, []float64{11}) {
		t.Errorf("member(x, [11]) = true, want false")
	}

	y := exit[1]
	if y.Top {
		t.Fatalf("y.Top = true, want false")
	}
	if y.Absent {
		t.Errorf("y.Absent = true, want false")
	}
	if y.Nan {
		t.Errorf("y.Nan = true, want false")
	}
	if !kernel.Member(y.Set, []float64{100}) {
		t.Errorf("member(y, [100]) = false, want true")
	}
	if !kernel.Member(y.Set, []float64{2}) {
		t.Errorf("member(y, [2]) = false, want true")
	}
	if !kernel.Member(y.Set, []float64{22}) {
		t.Errorf("member(y, [22]) = false, want true")
	}
	if kernel.Member(y.Set, []float64{1}) {
		t.Errorf("member(y, [1]) = true, want false")
	}
	if kernel.Member(y.Set, []float64{23}) {
		t.Errorf("member(y, [23]) = true, want false")
	}
}

func TestWalkProbe_ReadingAFlaggedBindingPoisonsTheAssignmentWithNaN(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	entry := []kernelbridge.KnownStateWire{
		{Top: true},
		{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{5})), Absent: true},
	}
	exit := kernel.Walk(entry, []kernelbridge.IrStatement{
		{
			Kind:   kernelbridge.IrStatementAssign,
			Target: 0,
			Effect: kernelbridge.LoopEffect{
				Kind: kernelbridge.LoopEffectBinary,
				Op:   kernelbridge.LoopOpAdd,
				A:    &kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectVar, Index: 1},
				B:    &kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectConst, Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{1}))},
			},
		},
	})
	x := exit[0]
	if x.Top {
		t.Fatalf("x.Top = true, want false")
	}
	if x.Absent {
		t.Errorf("x.Absent = true, want false")
	}
	if !x.Nan {
		t.Errorf("x.Nan = false, want true")
	}
	if !kernel.Member(x.Set, []float64{6}) {
		t.Errorf("member(x, [6]) = false, want true")
	}
	if kernel.Member(x.Set, []float64{7}) {
		t.Errorf("member(x, [7]) = true, want false")
	}
}

func TestWalkProbe_ALoopInsideTheWalkedBodyCertifiesThroughTheSolverAndAnUntouchedBindingKeepsItsStateAndFlags(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	// let i = 0; while (i < 10) { i = i + 1 }  — with x beside it,
	// possibly absent, never written by the loop
	entry := []kernelbridge.KnownStateWire{
		{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{0}))},
		{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{7})), Absent: true},
	}
	belowTen := refinementsets.MakeRefinedSet(refinementsets.Below(10))
	atLeastTen := refinementsets.MakeRefinedSet(refinementsets.AtLeast(10))
	exit := kernel.Walk(entry, []kernelbridge.IrStatement{
		{
			Kind:    kernelbridge.IrStatementLoop,
			Written: []bool{true, false},
			Cond:    []*refinementsets.RefinedSet{&belowTen, nil},
			After:   []*refinementsets.RefinedSet{&atLeastTen, nil},
			Body: []kernelbridge.LoopEffect{
				{
					Kind: kernelbridge.LoopEffectBinary,
					Op:   kernelbridge.LoopOpAdd,
					A:    &kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectVar, Index: 0},
					B:    &kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectConst, Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{1}))},
				},
				{Kind: kernelbridge.LoopEffectVar, Index: 1},
			},
		},
	})

	// the certified invariant is [0, ∞) integer (the solver widens
	// the moving ceiling away); the exit intersects the condition's
	// falsity, so i reads ≥ 10 — the true exit {10} sits inside, 9
	// and below are refuted, and the looseness above 10 is the
	// widening's honest price
	i := exit[0]
	if i.Top {
		t.Fatalf("i.Top = true, want false")
	}
	if i.Absent {
		t.Errorf("i.Absent = true, want false")
	}
	if i.Nan {
		t.Errorf("i.Nan = true, want false")
	}
	if !kernel.Member(i.Set, []float64{10}) {
		t.Errorf("member(i, [10]) = false, want true")
	}
	if kernel.Member(i.Set, []float64{9}) {
		t.Errorf("member(i, [9]) = true, want false")
	}
	if kernel.Member(i.Set, []float64{5}) {
		t.Errorf("member(i, [5]) = true, want false")
	}
	if kernel.Member(i.Set, []float64{10.5}) {
		t.Errorf("member(i, [10.5]) = true, want false")
	}

	x := exit[1]
	if x.Top {
		t.Fatalf("x.Top = true, want false")
	}
	if !x.Absent {
		t.Errorf("x.Absent = false, want true")
	}
	if x.Nan {
		t.Errorf("x.Nan = true, want false")
	}
	if !kernel.Member(x.Set, []float64{7}) {
		t.Errorf("member(x, [7]) = false, want true")
	}
	if kernel.Member(x.Set, []float64{8}) {
		t.Errorf("member(x, [8]) = true, want false")
	}
}

// The opaque branch through the kernel walk: the same body shape as the
// first probe, but the branch tests NOTHING —
//
//	y = x + 1
//	if (<unreadable>) { y = 100 } else { y = y * 2 }
//
// A concrete run may take either arm, so the walk must admit both: y
// reads 100 or [2, 22] exactly as the tested branch's join did, while
// x — which neither arm writes and no test narrowed — stays [0, 10]
// with both ends still admitted.
func TestWalkProbe_TheOpaqueBranchWalksBothArmsAndJoinsThemWithoutReadingAnyTest(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	entry := []kernelbridge.KnownStateWire{
		{Set: refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.AtMost(10))},
		{Top: true},
	}
	exit := kernel.Walk(entry, []kernelbridge.IrStatement{
		{
			Kind:   kernelbridge.IrStatementAssign,
			Target: 1,
			Effect: kernelbridge.LoopEffect{
				Kind: kernelbridge.LoopEffectBinary,
				Op:   kernelbridge.LoopOpAdd,
				A:    &kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectVar, Index: 0},
				B:    &kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectConst, Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{1}))},
			},
		},
		{
			Kind: kernelbridge.IrStatementBranchBoth,
			Then: []kernelbridge.IrStatement{
				{
					Kind:   kernelbridge.IrStatementAssign,
					Target: 1,
					Effect: kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectConst, Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{100}))},
				},
			},
			Else: []kernelbridge.IrStatement{
				{
					Kind:   kernelbridge.IrStatementAssign,
					Target: 1,
					Effect: kernelbridge.LoopEffect{
						Kind: kernelbridge.LoopEffectBinary,
						Op:   kernelbridge.LoopOpMul,
						A:    &kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectVar, Index: 1},
						B:    &kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectConst, Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{2}))},
					},
				},
			},
		},
	})
	if len(exit) != 2 {
		t.Fatalf("len(exit) = %d, want 2", len(exit))
	}

	x := exit[0]
	if x.Top {
		t.Fatalf("x.Top = true, want false")
	}
	// no test read x, so neither arm narrowed it
	for _, reading := range []float64{0, 10} {
		if !kernel.Member(x.Set, []float64{reading}) {
			t.Errorf("member(x, [%v]) = false, want true", reading)
		}
	}
	if kernel.Member(x.Set, []float64{11}) {
		t.Errorf("member(x, [11]) = true, want false")
	}

	y := exit[1]
	if y.Top {
		t.Fatalf("y.Top = true, want false")
	}
	if y.Absent {
		t.Errorf("y.Absent = true, want false")
	}
	if y.Nan {
		t.Errorf("y.Nan = true, want false")
	}
	for _, reading := range []float64{100, 2, 22} {
		if !kernel.Member(y.Set, []float64{reading}) {
			t.Errorf("member(y, [%v]) = false, want true — both arms ride", reading)
		}
	}
	if kernel.Member(y.Set, []float64{23}) {
		t.Errorf("member(y, [23]) = true, want false")
	}
}

func floatPtr(v float64) *float64 {
	return &v
}
