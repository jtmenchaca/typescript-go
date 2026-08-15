// split from ir_map_slots_test.go — the mutation lowerings
//
// What a set, an add, and a delete write into the slots: the joined
// size readings, the weak value and key updates, and the non-negative
// integer ray a delete leaves behind.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

func TestMapSlots_ASetCallJoinsBothSizeReadingsAndTheKeyAndValue(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := mapLoweringContext(kernel,
		[]string{"m.size", "m.vals", "m.keys"},
		[]BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber})
	statements := loweringParse(t, `m.set(7, 9);`)
	assignments, ok := MapSetAssignmentsOf(context, statements[0])
	if !ok {
		t.Fatalf("MapSetAssignmentsOf ok = false, want true")
	}
	if len(assignments) != 3 {
		t.Fatalf("len(assignments) = %d, want 3 (size, vals, keys)", len(assignments))
	}
	if assignments[0].Effect.Kind != kernelbridge.LoopEffectJoin {
		t.Errorf("size effect kind = %q, want %q — a set may overwrite, so both readings ride",
			assignments[0].Effect.Kind, kernelbridge.LoopEffectJoin)
	}
	stmts := assignsOf(assignments)
	exit := kernel.Walk([]kernelbridge.KnownStateWire{
		{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{2}))},
		{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{1}))},
		{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{5}))},
	}, stmts)
	size := loweringSetOf(t, exit[0])
	for _, reading := range []float64{2, 3} {
		if !kernel.Member(size, []float64{reading}) {
			t.Errorf("member(m.size, [%v]) = false, want true — an overwrite keeps 2, a fresh key gives 3", reading)
		}
	}
	values := loweringSetOf(t, exit[1])
	for _, value := range []float64{1, 9} {
		if !kernel.Member(values, []float64{value}) {
			t.Errorf("member(m.vals, [%v]) = false, want true", value)
		}
	}
	keys := loweringSetOf(t, exit[2])
	for _, key := range []float64{5, 7} {
		if !kernel.Member(keys, []float64{key}) {
			t.Errorf("member(m.keys, [%v]) = false, want true", key)
		}
	}
}

func TestMapSlots_AnAddCallWritesTheSizeAndTheValueAndNoKey(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := mapLoweringContext(kernel,
		[]string{"s.size", "s.vals"},
		[]BindingKind{BindingKindNumber, BindingKindNumber})
	statements := loweringParse(t, `s.add(4);`)
	assignments, ok := MapSetAssignmentsOf(context, statements[0])
	if !ok {
		t.Fatalf("MapSetAssignmentsOf(add) ok = false, want true")
	}
	if len(assignments) != 2 {
		t.Fatalf("len(assignments) = %d, want 2 — a Set has no key slot", len(assignments))
	}
	if assignments[1].Effect.Kind != kernelbridge.LoopEffectJoin {
		t.Errorf("value effect kind = %q, want %q — a weak update",
			assignments[1].Effect.Kind, kernelbridge.LoopEffectJoin)
	}
}

func TestMapSlots_AnAddOnAMapAndASetOnASetBothDecline(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	mapContext := mapLoweringContext(kernel,
		[]string{"m.size", "m.vals", "m.keys"},
		[]BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber})
	if _, ok := MapSetAssignmentsOf(mapContext, loweringParse(t, `m.add(1);`)[0]); ok {
		t.Errorf("m.add(1) lowered on a Map — add is a Set operation")
	}
	setContext := mapLoweringContext(kernel,
		[]string{"s.size", "s.vals"},
		[]BindingKind{BindingKindNumber, BindingKindNumber})
	if _, ok := MapSetAssignmentsOf(setContext, loweringParse(t, `s.set(1, 2);`)[0]); ok {
		t.Errorf("s.set(1, 2) lowered on a Set — set is a Map operation")
	}
}

func TestMapSlots_ADeleteLeavesTheNonNegativeIntegersJoinedWithTheOldSize(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := mapLoweringContext(kernel,
		[]string{"m.size", "m.vals", "m.keys"},
		[]BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber})
	statements := loweringParse(t, `m.delete(7);`)
	assignments, ok := MapDeleteAssignmentsOf(context, statements[0])
	if !ok {
		t.Fatalf("MapDeleteAssignmentsOf ok = false, want true")
	}
	if len(assignments) != 1 {
		t.Fatalf("len(assignments) = %d, want 1 — keys and values are untouched", len(assignments))
	}
	if assignments[0].Target != 0 {
		t.Errorf("target = %d, want 0 (m.size)", assignments[0].Target)
	}
	if assignments[0].Effect.Kind != kernelbridge.LoopEffectJoin {
		t.Fatalf("size effect kind = %q, want %q", assignments[0].Effect.Kind, kernelbridge.LoopEffectJoin)
	}
	stmts := assignsOf(assignments)
	exit := kernel.Walk([]kernelbridge.KnownStateWire{
		{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{3}))},
		{Top: true},
		{Top: true},
	}, stmts)
	size := loweringSetOf(t, exit[0])
	// the old reading stays admitted (a delete may miss) and every
	// non-negative integer rides beside it (the decrement is not claimable)
	for _, reading := range []float64{0, 2, 3} {
		if !kernel.Member(size, []float64{reading}) {
			t.Errorf("member(m.size, [%v]) = false, want true", reading)
		}
	}
	if kernel.Member(size, []float64{-1}) {
		t.Errorf("member(m.size, [-1]) = true, want false — a count is never negative")
	}
}
