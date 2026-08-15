// The widened record shapes lowered from real source: nested leaves,
// whole-record reassignment, destructuring, and the switch chain.
// Skipped (never a faked pass) when the native kernel dylib is absent,
// the same gate every other walk test here uses.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

func recordLoweringContext(kernel *kernelbridge.RefinedTSKernel, bindings []string, sorts []BindingKind) *LoweringContext {
	return &LoweringContext{
		Bindings: bindings,
		Sorts:    sorts,
		Typeofs:  make([]TypeofTag, len(bindings)),
		Narrow:   kernel.Narrow,
	}
}

func TestIrRecordLowering_ANestedLiteralWritesOneAssignmentPerLeafPath(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := recordLoweringContext(kernel,
		[]string{"p.lo", "p.inner.deep", "n"},
		[]BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber})
	stmts, ok := LowerStatements(context, loweringParse(t, `const p = { lo: 0, inner: { deep: n } };`))
	if !ok {
		t.Fatalf("LowerStatements(nested literal) ok = false, want true")
	}
	if len(stmts) != 2 {
		t.Fatalf("len(stmts) = %d, want 2 — one per leaf", len(stmts))
	}
	if stmts[0].Target != 0 || stmts[1].Target != 1 {
		t.Errorf("targets = %d, %d, want 0 (p.lo), 1 (p.inner.deep)", stmts[0].Target, stmts[1].Target)
	}
	if stmts[1].Effect.Kind != kernelbridge.LoopEffectVar || stmts[1].Effect.Index != 2 {
		t.Errorf("nested leaf effect = %+v, want a var read of n", stmts[1].Effect)
	}
}

func TestIrRecordLowering_ANestedLeafReadsAndWritesThroughItsOwnSlot(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := recordLoweringContext(kernel,
		[]string{"p.inner.deep", "x"},
		[]BindingKind{BindingKindNumber, BindingKindNumber})
	stmts, ok := LowerStatements(context, loweringParse(t, `
		p.inner.deep = 5;
		x = p.inner.deep + 1;
	`))
	if !ok {
		t.Fatalf("LowerStatements(nested leaf read/write) ok = false, want true")
	}
	if stmts[0].Target != 0 {
		t.Errorf("write target = %d, want 0 (p.inner.deep)", stmts[0].Target)
	}
	exit := kernel.Walk([]kernelbridge.KnownStateWire{{Top: true}, {Top: true}}, stmts)
	if !kernel.Member(loweringSetOf(t, exit[1]), []float64{6}) {
		t.Errorf("member(x, [6]) = false, want true")
	}
}

func TestIrRecordLowering_ARecordCopiesLeafForLeaf(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := recordLoweringContext(kernel,
		[]string{"p.hi", "p.lo", "q.hi", "q.lo"},
		[]BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber, BindingKindNumber})
	stmts, ok := LowerStatements(context, loweringParse(t, `p = q;`))
	if !ok {
		t.Fatalf("LowerStatements(p = q) ok = false, want true")
	}
	if len(stmts) != 2 {
		t.Fatalf("len(stmts) = %d, want 2 — one per leaf", len(stmts))
	}
	// the leaves pair by PATH, not by slot order: p.hi takes q.hi
	if stmts[0].Target != 0 || stmts[0].Effect.Index != 2 {
		t.Errorf("first assign = slot %d from slot %d, want 0 (p.hi) from 2 (q.hi)", stmts[0].Target, stmts[0].Effect.Index)
	}
	if stmts[1].Target != 1 || stmts[1].Effect.Index != 3 {
		t.Errorf("second assign = slot %d from slot %d, want 1 (p.lo) from 3 (q.lo)", stmts[1].Target, stmts[1].Effect.Index)
	}
	exit := kernel.Walk([]kernelbridge.KnownStateWire{
		{Top: true},
		{Top: true},
		{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{9}))},
		{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{4}))},
	}, stmts)
	if !kernel.Member(loweringSetOf(t, exit[0]), []float64{9}) {
		t.Errorf("member(p.hi, [9]) = false, want true")
	}
	if !kernel.Member(loweringSetOf(t, exit[1]), []float64{4}) {
		t.Errorf("member(p.lo, [4]) = false, want true")
	}
}

func TestIrRecordLowering_ARecordTakesAMatchingLiteralLeafForLeaf(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := recordLoweringContext(kernel,
		[]string{"p.hi", "p.lo", "n"},
		[]BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber})
	stmts, ok := LowerStatements(context, loweringParse(t, `p = { hi: n, lo: 0 };`))
	if !ok {
		t.Fatalf("LowerStatements(p = { … }) ok = false, want true")
	}
	if len(stmts) != 2 {
		t.Fatalf("len(stmts) = %d, want 2", len(stmts))
	}
	if stmts[0].Target != 0 || stmts[1].Target != 1 {
		t.Errorf("targets = %d, %d, want 0 (p.hi), 1 (p.lo)", stmts[0].Target, stmts[1].Target)
	}
}

func TestIrRecordLowering_ALiteralOfADifferentShapeTakesTheHavocFloor(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := recordLoweringContext(kernel,
		[]string{"p.hi", "p.lo"},
		[]BindingKind{BindingKindNumber, BindingKindNumber})
	// the leaf-for-leaf route refuses a shape mismatch, and the
	// TOTAL-LOWERING floor havocs every leaf p carries — the write
	// moved the record somewhere the slots cannot follow, and unknown
	// is exactly that claim
	stmts, ok := LowerStatements(context, loweringParse(t, `p = { hi: 1, other: 2 };`))
	if !ok {
		t.Fatalf("a different-shaped literal declined outright, want the havoc floor")
	}
	havocked := map[int]struct{}{}
	for _, s := range stmts {
		if s.Kind != kernelbridge.IrStatementAssign || s.Effect.Kind != kernelbridge.LoopEffectUnknown {
			t.Fatalf("stmts = %+v, want only unknown assigns", stmts)
		}
		havocked[s.Target] = struct{}{}
	}
	for slot := 0; slot < 2; slot++ {
		if _, hit := havocked[slot]; !hit {
			t.Errorf("leaf slot %d not havocked — its knowledge would survive a write that moved it", slot)
		}
	}
}

func TestIrRecordLowering_DestructuringReadsEachLeafSlot(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := recordLoweringContext(kernel,
		[]string{"p.lo", "p.hi", "x", "y"},
		[]BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber, BindingKindNumber})
	stmts, ok := LowerStatements(context, loweringParse(t, `const { lo: x, hi: y } = p;`))
	if !ok {
		t.Fatalf("LowerStatements(destructuring) ok = false, want true")
	}
	if len(stmts) != 2 {
		t.Fatalf("len(stmts) = %d, want 2 — one per bound name", len(stmts))
	}
	if stmts[0].Target != 2 || stmts[0].Effect.Index != 0 {
		t.Errorf("first read = slot %d from slot %d, want 2 (x) from 0 (p.lo)", stmts[0].Target, stmts[0].Effect.Index)
	}
	if stmts[1].Target != 3 || stmts[1].Effect.Index != 1 {
		t.Errorf("second read = slot %d from slot %d, want 3 (y) from 1 (p.hi)", stmts[1].Target, stmts[1].Effect.Index)
	}
}

func TestIrRecordLowering_AShorthandDestructuringReadsTheSameNamedLeaf(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := recordLoweringContext(kernel,
		[]string{"p.lo", "lo"},
		[]BindingKind{BindingKindNumber, BindingKindNumber})
	stmts, ok := LowerStatements(context, loweringParse(t, `const { lo } = p;`))
	if !ok {
		t.Fatalf("LowerStatements(shorthand destructuring) ok = false, want true")
	}
	if len(stmts) != 1 || stmts[0].Target != 1 || stmts[0].Effect.Index != 0 {
		t.Errorf("lowered to %+v, want one read of slot 0 (p.lo) into slot 1 (lo)", stmts)
	}
}

func TestIrRecordLowering_ADestructuringDefaultLowersExactly(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := recordLoweringContext(kernel,
		[]string{"p.lo", "lo"},
		[]BindingKind{BindingKindNumber, BindingKindNumber})
	// `const { lo = 3 } = p` from a flattened holder: the leaf read into
	// the bound name, then the definedness branch — only an undefined
	// leaf takes the default, exactly the runtime's rule
	stmts, ok := LowerStatements(context, loweringParse(t, `const { lo = 3 } = p;`))
	if !ok {
		t.Fatalf("a destructuring default declined outright")
	}
	if len(stmts) != 2 {
		t.Fatalf("stmts = %+v, want the leaf read then the definedness branch", stmts)
	}
	if stmts[0].Kind != kernelbridge.IrStatementAssign || stmts[0].Target != 1 ||
		stmts[0].Effect.Kind != kernelbridge.LoopEffectVar || stmts[0].Effect.Index != 0 {
		t.Errorf("stmts[0] = %+v, want lo := p.lo", stmts[0])
	}
	branch := stmts[1]
	if branch.Kind != kernelbridge.IrStatementBranch || branch.On != 1 ||
		branch.Test != kernelbridge.IrTestDefined {
		t.Fatalf("stmts[1] = %+v, want a definedness branch on lo", branch)
	}
	if len(branch.Else) != 1 || branch.Else[0].Target != 1 ||
		branch.Else[0].Effect.Kind != kernelbridge.LoopEffectConst {
		t.Errorf("the else arm = %+v, want lo := {3}", branch.Else)
	}
}

func TestIrSwitchLowering_ASwitchOnAStringSlotLowersAsAChainOfEqualityBranches(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := recordLoweringContext(kernel,
		[]string{"s", "n"},
		[]BindingKind{BindingKindString, BindingKindNumber})
	stmts, ok := LowerStatements(context, loweringParse(t, `
		switch (s) {
			case "a": n = 1; break;
			case "b": n = 2; break;
			default: n = 3;
		}
	`))
	if !ok {
		t.Fatalf("LowerStatements(switch) ok = false, want true")
	}
	if len(stmts) != 1 || stmts[0].Kind != kernelbridge.IrStatementBranch {
		t.Fatalf("lowered to %d statement(s), want one branch heading the chain", len(stmts))
	}
	if stmts[0].Test != kernelbridge.IrTestEqSeq {
		t.Errorf("first test = %q, want %q — a word compared exactly", stmts[0].Test, kernelbridge.IrTestEqSeq)
	}
	// the else arm is the next case's branch, and its else the default
	if len(stmts[0].Else) != 1 || stmts[0].Else[0].Kind != kernelbridge.IrStatementBranch {
		t.Fatalf("the first case's else is not the next case's branch: %+v", stmts[0].Else)
	}
	exit := kernel.Walk([]kernelbridge.KnownStateWire{
		{Set: refinementsets.MakeRefinedSet(refinementsets.Union(
			refinementsets.StringTuple("a"), refinementsets.StringTuple("z"))),
		},
		{Top: true},
	}, stmts)
	n := exit[1]
	if n.Top {
		t.Fatalf("n.Top = true, want false")
	}
	set := loweringSetOf(t, n)
	if !kernel.Member(set, []float64{1}) {
		t.Errorf("member(n, [1]) = false, want true — \"a\" reaches the first case")
	}
	if !kernel.Member(set, []float64{3}) {
		t.Errorf("member(n, [3]) = false, want true — \"z\" reaches the default")
	}
	if kernel.Member(set, []float64{4}) {
		t.Errorf("member(n, [4]) = true, want false")
	}
}

func TestIrSwitchLowering_GroupedLabelsShareOneArm(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := recordLoweringContext(kernel,
		[]string{"x", "n"},
		[]BindingKind{BindingKindNumber, BindingKindNumber})
	stmts, ok := LowerStatements(context, loweringParse(t, `
		switch (x) {
			case 1:
			case 2: n = 10; break;
			default: n = 20;
		}
	`))
	if !ok {
		t.Fatalf("LowerStatements(grouped labels) ok = false, want true")
	}
	exit := kernel.Walk([]kernelbridge.KnownStateWire{
		{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{1, 2, 5}))},
		{Top: true},
	}, stmts)
	set := loweringSetOf(t, exit[1])
	if !kernel.Member(set, []float64{10}) {
		t.Errorf("member(n, [10]) = false, want true — 1 and 2 both reach the shared arm")
	}
	if !kernel.Member(set, []float64{20}) {
		t.Errorf("member(n, [20]) = false, want true — 5 reaches the default")
	}
}

func TestIrSwitchLowering_AFallingThroughCaseConcatenatesItsRun(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := recordLoweringContext(kernel,
		[]string{"x", "n"},
		[]BindingKind{BindingKindNumber, BindingKindNumber})
	// a falling-through case's run is its own statements followed by
	// the next clause's, up to the first ending — so case 1 runs
	// `n = 1; n = 2; break` and case 2 runs `n = 2; break`. Every path
	// through case 1 overwrites the intermediate 1, which is what the
	// exit must show: n leaves as 2 on a matched run and untouched on
	// an unmatched one, and 1 is unobservable.
	stmts, ok := LowerStatements(context, loweringParse(t, `
		switch (x) {
			case 1: n = 1;
			case 2: n = 2; break;
		}
	`))
	if !ok {
		t.Fatalf("a falling-through switch declined, want the concatenated equality chain")
	}
	exit := kernel.Walk([]kernelbridge.KnownStateWire{
		{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{1, 2, 5}))},
		{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{0}))},
	}, stmts)
	set := loweringSetOf(t, exit[1])
	if !kernel.Member(set, []float64{2}) {
		t.Errorf("member(n, [2]) = false, want true — a matched run lands on 2")
	}
	if !kernel.Member(set, []float64{0}) {
		t.Errorf("member(n, [0]) = false, want true — an unmatched x leaves n alone")
	}
	if kernel.Member(set, []float64{1}) {
		t.Errorf("member(n, [1]) = true, want false — every path through case 1 overwrites the 1")
	}
}

func TestIrSwitchLowering_ASwitchWithNoDefaultLeavesTheChainsFinalElseEmpty(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := recordLoweringContext(kernel,
		[]string{"x", "n"},
		[]BindingKind{BindingKindNumber, BindingKindNumber})
	stmts, ok := LowerStatements(context, loweringParse(t, `
		switch (x) {
			case 1: n = 1; break;
		}
	`))
	if !ok {
		t.Fatalf("LowerStatements(switch without default) ok = false, want true")
	}
	if len(stmts) != 1 || stmts[0].Kind != kernelbridge.IrStatementBranch {
		t.Fatalf("lowered to %+v, want one branch", stmts)
	}
	if len(stmts[0].Else) != 0 {
		t.Errorf("else arm = %+v, want empty — no default means nothing runs", stmts[0].Else)
	}
	exit := kernel.Walk([]kernelbridge.KnownStateWire{
		{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{1, 7}))},
		{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{0}))},
	}, stmts)
	set := loweringSetOf(t, exit[1])
	if !kernel.Member(set, []float64{1}) {
		t.Errorf("member(n, [1]) = false, want true")
	}
	if !kernel.Member(set, []float64{0}) {
		t.Errorf("member(n, [0]) = false, want true — the unmatched path leaves n alone")
	}
}

// TestIrSwitchLowering_ABooleanDiscriminantLowersItsCaseTrueChainAsABranch
// pins the boolean case-label arm (lowering_to_kernel_ir_switch_labels.go):
// over a boolean-sorted (number-sorted, typeof-boolean) slot, `case
// true:` reads as the same IrTestEq the number sort already tests
// under, true's own word 1.
func TestIrSwitchLowering_ABooleanDiscriminantLowersItsCaseTrueChainAsABranch(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := recordLoweringContext(kernel,
		[]string{"flag", "x"},
		[]BindingKind{BindingKindNumber, BindingKindNumber})
	context.Typeofs[0] = TypeofTagBoolean
	stmts, ok := LowerStatements(context, loweringParse(t, `
		switch (flag) {
			case true: x = 1; break;
			default: x = 2;
		}
	`))
	if !ok {
		t.Fatalf("LowerStatements(boolean switch) ok = false, want true")
	}
	if len(stmts) != 1 || stmts[0].Kind != kernelbridge.IrStatementBranch {
		t.Fatalf("lowered to %+v, want one branch heading the chain", stmts)
	}
	if stmts[0].Test != kernelbridge.IrTestEq || stmts[0].W == nil || *stmts[0].W != 1 {
		t.Errorf("first test = %+v, want IrTestEq against the word 1 (true)", stmts[0])
	}
	exit := kernel.Walk([]kernelbridge.KnownStateWire{
		{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{0, 1}))},
		{Top: true},
	}, stmts)
	set := loweringSetOf(t, exit[1])
	if !kernel.Member(set, []float64{1}) {
		t.Errorf("member(x, [1]) = false, want true — true reaches the case true arm")
	}
	if !kernel.Member(set, []float64{2}) {
		t.Errorf("member(x, [2]) = false, want true — false reaches the default")
	}
}

// TestIrSwitchLowering_ABooleanCaseLabelOverAnUntrackedDiscriminantTakesTheHavocFloor
// pins the soundness side of the same arm: a boolean label over a
// discriminant with no tracked slot at all still declines the chain
// and falls to the havoc floor, exactly like the plain-number
// untracked-discriminant case above.
func TestIrSwitchLowering_ABooleanCaseLabelOverAnUntrackedDiscriminantTakesTheHavocFloor(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := recordLoweringContext(kernel,
		[]string{"x"},
		[]BindingKind{BindingKindNumber})
	stmts, ok := LowerStatements(context, loweringParse(t, `
		switch (free) {
			case true: x = 1; break;
		}
	`))
	if !ok {
		t.Fatalf("an untracked-discriminant boolean switch declined outright, want the havoc floor")
	}
	sawX := false
	for _, s := range stmts {
		if s.Kind != kernelbridge.IrStatementAssign || s.Effect.Kind != kernelbridge.LoopEffectUnknown {
			t.Fatalf("stmts = %+v, want only unknown assigns", stmts)
		}
		if s.Target == 0 {
			sawX = true
		}
	}
	if !sawX {
		t.Errorf("x (slot 0) was not havocked — the arm's write would be skipped")
	}
}

func TestIrSwitchLowering_ASwitchOnAnUntrackedDiscriminantTakesTheHavocFloor(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := recordLoweringContext(kernel,
		[]string{"n"},
		[]BindingKind{BindingKindNumber})
	// no slot carries the discriminant's value, so the chain refuses —
	// and the floor havocs what the arms could write, which claims
	// nothing about which arm ran
	stmts, ok := LowerStatements(context, loweringParse(t, `
		switch (free) {
			case 1: n = 1; break;
		}
	`))
	if !ok {
		t.Fatalf("an untracked-discriminant switch declined outright, want the havoc floor")
	}
	sawN := false
	for _, s := range stmts {
		if s.Kind != kernelbridge.IrStatementAssign || s.Effect.Kind != kernelbridge.LoopEffectUnknown {
			t.Fatalf("stmts = %+v, want only unknown assigns", stmts)
		}
		if s.Target == 0 {
			sawN = true
		}
	}
	if !sawN {
		t.Errorf("n (slot 0) was not havocked — the arm's write would be skipped")
	}
}
