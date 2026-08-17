// split from ir_map_slots_test.go — the mutation lowerings
//
// What a set, an add, a delete, a clear, and a getOrInsert write into
// the slots: the joined size readings, the weak value and key updates,
// the non-negative integer ray a delete leaves behind, the fresh-empty
// state a clear resets every slot to, and the joined stored-or-inserted
// value getOrInsert hands back.
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

func TestMapSlots_AClearResetsSizeToZeroAndValsAndKeysToAbsent(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := mapLoweringContext(kernel,
		[]string{"m.size", "m.vals", "m.keys"},
		[]BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber})
	statements := loweringParse(t, `m.clear();`)
	assignments, ok := MapClearAssignmentsOf(context, statements[0])
	if !ok {
		t.Fatalf("MapClearAssignmentsOf ok = false, want true")
	}
	if len(assignments) != 3 {
		t.Fatalf("len(assignments) = %d, want 3 (size, vals, keys)", len(assignments))
	}
	if assignments[0].Effect.Kind != kernelbridge.LoopEffectConst {
		t.Errorf("size effect kind = %q, want %q — clear replaces rather than joins",
			assignments[0].Effect.Kind, kernelbridge.LoopEffectConst)
	}
	for _, index := range []int{1, 2} {
		effect := assignments[index].Effect
		if effect.Kind != kernelbridge.LoopEffectConstState || !effect.Undef || effect.Null {
			t.Errorf("assignments[%d].Effect = %+v, want the undefined-carrying constState (Undef alone)", index, effect)
		}
	}
	stmts := assignsOf(assignments)
	// walk from a NON-EMPTY entry state — clear must overwrite it, not join
	exit := kernel.Walk([]kernelbridge.KnownStateWire{
		{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{3}))},
		{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{9}))},
		{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{5}))},
	}, stmts)
	size := loweringSetOf(t, exit[0])
	if !kernel.Member(size, []float64{0}) {
		t.Errorf("member(m.size, [0]) = false, want true — clear empties the collection")
	}
	if kernel.Member(size, []float64{3}) {
		t.Errorf("member(m.size, [3]) = true, want false — the old count does not ride")
	}
	if !exit[1].Undef || exit[1].Null {
		t.Errorf("m.vals admissions (undef=%v null=%v), want undefined alone — a missing read answers undefined, never null", exit[1].Undef, exit[1].Null)
	}
	if !exit[2].Undef || exit[2].Null {
		t.Errorf("m.keys admissions (undef=%v null=%v), want undefined alone", exit[2].Undef, exit[2].Null)
	}
}

func TestMapSlots_AClearOnASetWritesOnlySizeAndVals(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := mapLoweringContext(kernel,
		[]string{"s.size", "s.vals"},
		[]BindingKind{BindingKindNumber, BindingKindNumber})
	statements := loweringParse(t, `s.clear();`)
	assignments, ok := MapClearAssignmentsOf(context, statements[0])
	if !ok {
		t.Fatalf("MapClearAssignmentsOf(Set) ok = false, want true")
	}
	if len(assignments) != 2 {
		t.Fatalf("len(assignments) = %d, want 2 — a Set has no key slot", len(assignments))
	}
}

func TestMapSlots_AClearWithAnArgumentDoesNotLower(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := mapLoweringContext(kernel,
		[]string{"m.size", "m.vals", "m.keys"},
		[]BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber})
	if _, ok := MapClearAssignmentsOf(context, loweringParse(t, `m.clear(1);`)[0]); ok {
		t.Errorf("m.clear(1) lowered — clear takes no arguments")
	}
}

func TestMapSlots_AGetOrInsertAsAStatementWritesSizeValsAndKeys(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := mapLoweringContext(kernel,
		[]string{"m.size", "m.vals", "m.keys"},
		[]BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber})
	statements := loweringParse(t, `m.getOrInsert(7, 9);`)
	assignments, ok := MapGetOrInsertAssignmentsOf(context, statements[0])
	if !ok {
		t.Fatalf("MapGetOrInsertAssignmentsOf ok = false, want true")
	}
	if len(assignments) != 3 {
		t.Fatalf("len(assignments) = %d, want 3 (size, vals, keys) — the return value is discarded", len(assignments))
	}
	if assignments[0].Effect.Kind != kernelbridge.LoopEffectJoin {
		t.Errorf("size effect kind = %q, want %q — k may already be present, so both readings ride",
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
			t.Errorf("member(m.size, [%v]) = false, want true — present keeps 2, absent gives 3", reading)
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

func TestMapSlots_AGetOrInsertBoundToADeclarationReadsTheJoinedValsSlot(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := mapLoweringContext(kernel,
		[]string{"m.size", "m.vals", "m.keys", "r"},
		[]BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber, BindingKindNumber})
	statements := loweringParse(t, `const r = m.getOrInsert(7, 9);`)
	assignments, ok := MapGetOrInsertAssignmentsOf(context, statements[0])
	if !ok {
		t.Fatalf("MapGetOrInsertAssignmentsOf(declaration) ok = false, want true")
	}
	if len(assignments) != 4 {
		t.Fatalf("len(assignments) = %d, want 4 (size, vals, keys, r)", len(assignments))
	}
	if assignments[3].Target != 3 {
		t.Errorf("target = %d, want 3 (r)", assignments[3].Target)
	}
	if assignments[3].Effect.Kind != kernelbridge.LoopEffectVarState || assignments[3].Effect.Index != 1 {
		t.Errorf("r's effect = %+v, want a verbatim copy of slot 1 (m.vals), taken AFTER the weak update", assignments[3].Effect)
	}
	stmts := assignsOf(assignments)
	// k=7 is ALREADY present holding {3}: the stored value must ride in
	// r's reading beside the inserted default
	exit := kernel.Walk([]kernelbridge.KnownStateWire{
		{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{1}))},
		{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{3}))},
		{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{7}))},
		{Top: true},
	}, stmts)
	r := loweringSetOf(t, exit[3])
	for _, reading := range []float64{3, 9} {
		if !kernel.Member(r, []float64{reading}) {
			t.Errorf("member(r, [%v]) = false, want true — r joins the stored reading with v", reading)
		}
	}
}

func TestMapSlots_AGetOrInsertAssignedToATrackedNameReadsTheJoinedValsSlot(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := mapLoweringContext(kernel,
		[]string{"m.size", "m.vals", "m.keys", "r"},
		[]BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber, BindingKindNumber})
	statements := loweringParse(t, `r = m.getOrInsert(7, 9);`)
	assignments, ok := MapGetOrInsertAssignmentsOf(context, statements[0])
	if !ok {
		t.Fatalf("MapGetOrInsertAssignmentsOf(assignment) ok = false, want true")
	}
	if len(assignments) != 4 {
		t.Fatalf("len(assignments) = %d, want 4 (size, vals, keys, r)", len(assignments))
	}
}

func TestMapSlots_AGetOrInsertOnASetDoesNotLower(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := mapLoweringContext(kernel,
		[]string{"s.size", "s.vals"},
		[]BindingKind{BindingKindNumber, BindingKindNumber})
	if _, ok := MapGetOrInsertAssignmentsOf(context, loweringParse(t, `s.getOrInsert(1, 2);`)[0]); ok {
		t.Errorf("s.getOrInsert(1, 2) lowered on a Set — getOrInsert is a Map operation")
	}
}

func TestMapSlots_AGetOrInsertComputedDoesNotLower(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := mapLoweringContext(kernel,
		[]string{"m.size", "m.vals", "m.keys"},
		[]BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber})
	if _, ok := MapGetOrInsertAssignmentsOf(context, loweringParse(t, `m.getOrInsertComputed(7, cb);`)[0]); ok {
		t.Errorf("m.getOrInsertComputed(7, cb) lowered — the callback form needs callback-summary machinery not built here")
	}
}
