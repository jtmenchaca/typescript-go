// split from ir_opaque_havoc_test.go — the whole body, through the kernel

package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

/* ── the whole body, through the kernel ──────────────────────────── */

func TestOpaqueHavoc_ReadableKnowledgeSurvivesAroundAnOpaqueCallInAWholeBody(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	// x is written readably, then an unresolvable call havocs ONLY what
	// it was handed, then y is written readably again. The body keeps its
	// route, and both readable writes reach the exit intact.
	context := &LoweringContext{
		Bindings: []string{"x", "y", "z"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber},
		Typeofs:  []TypeofTag{TypeofTagNumber, TypeofTagNumber, TypeofTagNumber},
		Narrow:   kernel.Narrow,
	}
	stmts, ok := LowerStatements(context, loweringParse(t, `
		x = 5;
		z = mysteryCall(x);
		y = x + 1;
	`))
	if !ok {
		t.Fatalf("the body declined — an opaque call should have havocked, not declined")
	}
	if context.FirstHavoc != "call mysteryCall" {
		t.Errorf("FirstHavoc = %q, want %q", context.FirstHavoc, "call mysteryCall")
	}
	exit := kernel.Walk(
		[]kernelbridge.KnownStateWire{{Top: true}, {Top: true}, {Top: true}},
		stmts,
	)
	x := exit[0]
	if x.Top {
		t.Fatalf("x.Top = true, want the readable write to have survived the havoc")
	}
	xSet := loweringSetOf(t, x)
	if !kernel.Member(xSet, []float64{5}) {
		t.Errorf("member(x, [5]) = false, want true — x was written readably and never havocked")
	}
	if kernel.Member(xSet, []float64{6}) {
		t.Errorf("member(x, [6]) = true, want false — the havoc must not have widened x")
	}
	y := exit[1]
	if y.Top {
		t.Fatalf("y.Top = true, want the arithmetic after the havoc to have been read")
	}
	ySet := loweringSetOf(t, y)
	if !kernel.Member(ySet, []float64{6}) {
		t.Errorf("member(y, [6]) = false, want true — y = x + 1 with x still pinned at 5")
	}
	if kernel.Member(ySet, []float64{7}) {
		t.Errorf("member(y, [7]) = true, want false")
	}
	// z took the call's value, which claims nothing
	if !exit[2].Top {
		t.Errorf("z.Top = false, want true — the havocked slot claims nothing")
	}
}

func TestOpaqueHavoc_AHavockedFlattenedLocalLosesItsLeavesAndOnlyThose(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	context := &LoweringContext{
		Bindings: []string{"p.lo", "p.hi", "keep"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber},
		Typeofs:  []TypeofTag{TypeofTagNumber, TypeofTagNumber, TypeofTagNumber},
		Narrow:   kernel.Narrow,
	}
	stmts, ok := LowerStatements(context, loweringParse(t, `
		keep = 3;
		handOut(p);
	`))
	if !ok {
		t.Fatalf("the body declined")
	}
	exit := kernel.Walk(
		[]kernelbridge.KnownStateWire{
			{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{1}))},
			{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{2}))},
			{Top: true},
		},
		stmts,
	)
	if !exit[0].Top {
		t.Errorf("p.lo.Top = false, want true — the leaf was handed to unseen code")
	}
	if !exit[1].Top {
		t.Errorf("p.hi.Top = false, want true — the leaf was handed to unseen code")
	}
	keep := exit[2]
	if keep.Top {
		t.Fatalf("keep.Top = true, want the unrelated slot's knowledge to have survived")
	}
	if !kernel.Member(loweringSetOf(t, keep), []float64{3}) {
		t.Errorf("member(keep, [3]) = false, want true")
	}
}
