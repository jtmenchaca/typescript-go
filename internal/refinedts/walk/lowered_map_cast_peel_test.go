// the cast-peel the KERNEL-LOWERED map vocabulary now shares with the
// evaluation walk's collection models (collection_models.go): the
// receiver of a `set`/`getOrInsert` call may stand behind
// `as unknown as {…}` — erased at runtime, so the lowering still reads
// and writes the tracked collection under the cast exactly as the bare
// name would.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

func TestMapSlots_ASetCallBehindAnUnknownCastStillLowers(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := mapLoweringContext(kernel,
		[]string{"m.size", "m.vals", "m.keys"},
		[]BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber})
	statements := loweringParse(t, `(m as unknown as { set(k: number, v: number): void }).set(7, 9);`)
	assignments, ok := MapSetAssignmentsOf(context, statements[0])
	if !ok {
		t.Fatalf("MapSetAssignmentsOf(cast receiver) ok = false, want true — the cast erases at runtime")
	}
	if len(assignments) != 3 {
		t.Fatalf("len(assignments) = %d, want 3 (size, vals, keys)", len(assignments))
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
}

func TestMapSlots_AGetOrInsertCallBehindAnUnknownCastStillLowers(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := mapLoweringContext(kernel,
		[]string{"m.size", "m.vals", "m.keys"},
		[]BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber})
	statements := loweringParse(t, `(m as unknown as { getOrInsert(k: number, v: number): number }).getOrInsert(7, 9);`)
	assignments, ok := MapGetOrInsertAssignmentsOf(context, statements[0])
	if !ok {
		t.Fatalf("MapGetOrInsertAssignmentsOf(cast receiver) ok = false, want true — the cast erases at runtime")
	}
	if len(assignments) != 3 {
		t.Fatalf("len(assignments) = %d, want 3 (size, vals, keys) — the return value is discarded", len(assignments))
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
}

func TestMapSlots_AGetOrInsertCallBehindACastBoundToADeclarationReadsTheJoinedValsSlot(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := mapLoweringContext(kernel,
		[]string{"m.size", "m.vals", "m.keys", "r"},
		[]BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber, BindingKindNumber})
	statements := loweringParse(t, `const r = (m as unknown as { getOrInsert(k: number, v: number): number }).getOrInsert(7, 9);`)
	assignments, ok := MapGetOrInsertAssignmentsOf(context, statements[0])
	if !ok {
		t.Fatalf("MapGetOrInsertAssignmentsOf(cast receiver, declaration) ok = false, want true")
	}
	if len(assignments) != 4 {
		t.Fatalf("len(assignments) = %d, want 4 (size, vals, keys, r)", len(assignments))
	}
	if assignments[3].Effect.Kind != kernelbridge.LoopEffectVarState || assignments[3].Effect.Index != 1 {
		t.Errorf("r's effect = %+v, want a verbatim copy of slot 1 (m.vals), taken AFTER the weak update", assignments[3].Effect)
	}
}
