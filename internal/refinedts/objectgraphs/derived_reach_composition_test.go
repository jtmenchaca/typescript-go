package objectgraphs

import (
	"math"
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// count composition is the kernel's exact ℕ product now — skipped
// (t.Skip, never a faked pass) when the artifacts are absent

func loadDerivedReachKernel(t *testing.T) *kernelbridge.RefinedTSKernel {
	t.Helper()
	if !kernelbridge.KernelArtifactsPresent(kernelbridge.DylibPath) {
		t.Skip("native kernel dylib absent — build it first")
	}
	kernel, err := kernelbridge.LoadKernel(kernelbridge.DylibPath)
	if err != nil {
		t.Fatalf("LoadKernel: %v", err)
	}
	return kernel
}

func hasForm(forms []refinementsets.Refinement, form refinementsets.Form, a float64) bool {
	for _, f := range forms {
		if f.Form == form && f.A == a {
			return true
		}
	}
	return false
}

func TestTwoStepReachesComposeOrderItemRegion(t *testing.T) {
	kernel := loadDerivedReachKernel(t)
	// Order →{1,2}→ Item, Item →{1}→ Region
	spec := SpecificationOf(SpecificationParts{
		Nodes: []refinementsets.RefinedSet{
			refinementsets.MakeRefinedSet(),
			refinementsets.MakeRefinedSet(),
			refinementsets.MakeRefinedSet(),
		},
		Paths: []CardinalityPath{
			{Tail: 0, Count: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{1, 2})), Head: 1},
			{Tail: 1, Count: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{1})), Head: 2},
		},
	})
	reaches := DerivedReaches(spec, kernel, DerivedReachesBounds{})
	if len(reaches) != 1 {
		t.Fatalf("len(reaches) = %d, want 1", len(reaches))
	}
	if reaches[0].Tail != 0 || reaches[0].Head != 2 {
		t.Errorf("reaches[0] tail/head = %d/%d, want 0/2", reaches[0].Tail, reaches[0].Head)
	}
	if len(reaches[0].Through) != 2 || reaches[0].Through[0] != 0 || reaches[0].Through[1] != 1 {
		t.Errorf("reaches[0].Through = %v, want [0 1]", reaches[0].Through)
	}
	// each Order reaches 1–2 Regions (through 1–2 Items × 1 Region each)
	if !hasForm(reaches[0].Count.Forms, refinementsets.FormAtLeast, 1) {
		t.Errorf("reaches[0].Count.Forms missing atLeast 1: %+v", reaches[0].Count.Forms)
	}
	if !hasForm(reaches[0].Count.Forms, refinementsets.FormAtMost, 2) {
		t.Errorf("reaches[0].Count.Forms missing atMost 2: %+v", reaches[0].Count.Forms)
	}
}

func TestChainsExtendPastTwoHopsBoundsMultiplying(t *testing.T) {
	kernel := loadDerivedReachKernel(t)
	// A →{2}→ B →{3}→ C →{1,2}→ D
	spec := SpecificationOf(SpecificationParts{
		Nodes: []refinementsets.RefinedSet{
			refinementsets.MakeRefinedSet(),
			refinementsets.MakeRefinedSet(),
			refinementsets.MakeRefinedSet(),
			refinementsets.MakeRefinedSet(),
		},
		Paths: []CardinalityPath{
			{Tail: 0, Count: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{2})), Head: 1},
			{Tail: 1, Count: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{3})), Head: 2},
			{Tail: 2, Count: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{1, 2})), Head: 3},
		},
	})
	reaches := DerivedReaches(spec, kernel, DerivedReachesBounds{})
	// 0→2 (2 hops), 1→3 (2 hops), 0→3 (3 hops)
	if len(reaches) != 3 {
		t.Fatalf("len(reaches) = %d, want 3", len(reaches))
	}
	var full *DerivedReach
	for i := range reaches {
		if len(reaches[i].Through) == 3 {
			full = &reaches[i]
		}
	}
	if full == nil {
		t.Fatalf("no 3-hop reach found")
	}
	if full.Tail != 0 || full.Head != 3 {
		t.Errorf("full tail/head = %d/%d, want 0/3", full.Tail, full.Head)
	}
	// 2·3·[1,2] = [6, 12] D-slots per A
	if !hasForm(full.Count.Forms, refinementsets.FormAtLeast, 6) {
		t.Errorf("full.Count.Forms missing atLeast 6: %+v", full.Count.Forms)
	}
	if !hasForm(full.Count.Forms, refinementsets.FormAtMost, 12) {
		t.Errorf("full.Count.Forms missing atMost 12: %+v", full.Count.Forms)
	}
}

func TestAnUnboundedHopLeavesTheCeilingOpenKeepsTheFloor(t *testing.T) {
	kernel := loadDerivedReachKernel(t)
	spec := SpecificationOf(SpecificationParts{
		Nodes: []refinementsets.RefinedSet{
			refinementsets.MakeRefinedSet(),
			refinementsets.MakeRefinedSet(),
			refinementsets.MakeRefinedSet(),
		},
		Paths: []CardinalityPath{
			{Tail: 0, Count: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{2, 3})), Head: 1},
			{Tail: 1, Count: refinementsets.MakeRefinedSet(refinementsets.Integer, refinementsets.AtLeast(1)), Head: 2},
		},
	})
	reaches := DerivedReaches(spec, kernel, DerivedReachesBounds{})
	if len(reaches) != 1 {
		t.Fatalf("len(reaches) = %d, want 1", len(reaches))
	}
	if !hasForm(reaches[0].Count.Forms, refinementsets.FormAtLeast, 2) {
		t.Errorf("reaches[0].Count.Forms missing atLeast 2: %+v", reaches[0].Count.Forms)
	}
	// no finite ceiling (the wire spells the open one as atMost +∞)
	for _, f := range reaches[0].Count.Forms {
		if f.Form == refinementsets.FormAtMost && !math.IsInf(f.A, 1) {
			t.Errorf("reaches[0].Count.Forms has a finite atMost %v, want none", f.A)
		}
	}
}

func TestTheHopBoundCapsChainLengthPathsNeverRepeat(t *testing.T) {
	kernel := loadDerivedReachKernel(t)
	// a 2-cycle: A →{1}→ B →{1}→ A — chains stop when every path is used
	spec := SpecificationOf(SpecificationParts{
		Nodes: []refinementsets.RefinedSet{
			refinementsets.MakeRefinedSet(),
			refinementsets.MakeRefinedSet(),
		},
		Paths: []CardinalityPath{
			{Tail: 0, Count: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{1})), Head: 1},
			{Tail: 1, Count: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{1})), Head: 0},
		},
	})
	reaches := DerivedReaches(spec, kernel, DerivedReachesBounds{MaxHops: 5})
	// 0→0 through [0,1] and 1→1 through [1,0]; no longer chains exist
	if len(reaches) != 2 {
		t.Fatalf("len(reaches) = %d, want 2", len(reaches))
	}
	for _, r := range reaches {
		if len(r.Through) != 2 {
			t.Errorf("r.Through = %v, want length 2", r.Through)
		}
	}
}
