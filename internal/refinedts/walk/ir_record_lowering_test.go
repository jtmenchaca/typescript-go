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

func TestIrRecordLowering_ADestructuringDefaultTakesTheHavocFloor(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := recordLoweringContext(kernel,
		[]string{"p.lo", "lo"},
		[]BindingKind{BindingKindNumber, BindingKindNumber})
	// the leaf-read route refuses a default (the leaf's absence would
	// take it, which a plain read does not spell), and the floor havocs
	// the bound name — unknown covers both the leaf and the default
	stmts, ok := LowerStatements(context, loweringParse(t, `const { lo = 3 } = p;`))
	if !ok {
		t.Fatalf("a destructuring default declined outright, want the havoc floor")
	}
	sawBoundName := false
	for _, s := range stmts {
		if s.Kind != kernelbridge.IrStatementAssign || s.Effect.Kind != kernelbridge.LoopEffectUnknown {
			t.Fatalf("stmts = %+v, want only unknown assigns", stmts)
		}
		if s.Target == 1 {
			sawBoundName = true
		}
	}
	if !sawBoundName {
		t.Errorf("the bound name (slot 1) was not havocked — its old knowledge would survive the binding")
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

func TestIrSwitchLowering_AFallingThroughCaseDeclinesTheWholeSwitch(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := recordLoweringContext(kernel,
		[]string{"x", "n"},
		[]BindingKind{BindingKindNumber, BindingKindNumber})
	if _, ok := LowerStatements(context, loweringParse(t, `
		switch (x) {
			case 1: n = 1;
			case 2: n = 2; break;
		}
	`)); ok {
		t.Errorf("a falling-through case lowered — its statements run on into the next clause, which the chain has no arm for")
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

func TestIrSwitchLowering_ASwitchOnAnUntrackedDiscriminantDeclines(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := recordLoweringContext(kernel,
		[]string{"n"},
		[]BindingKind{BindingKindNumber})
	if _, ok := LowerStatements(context, loweringParse(t, `
		switch (free) {
			case 1: n = 1; break;
		}
	`)); ok {
		t.Errorf("a switch on an untracked name lowered — no slot carries its value")
	}
}
