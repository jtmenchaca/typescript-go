// split from ir_map_slots_test.go — the declaration lowering
//
// What a `new Map()` / `new Map([[k, v], …])` declaration writes into
// the three slots: the row count, and the joined seed values and keys.
// Also the two-sibling PRODUCER's own writes — `const u = a.union(b)`
// and its three algebra siblings — unknown size and the joined vals.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

func TestMapSlots_AnEmptyMapDeclarationWritesZeroAndTheAbsentCarryingConstant(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := mapLoweringContext(kernel,
		[]string{"m.size", "m.vals", "m.keys"},
		[]BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber})
	statements := loweringParse(t, `const m = new Map();`)
	assignments, ok := MapDeclarationAssignmentsOf(context, statements[0])
	if !ok {
		t.Fatalf("MapDeclarationAssignmentsOf ok = false, want true")
	}
	if len(assignments) != 3 {
		t.Fatalf("len(assignments) = %d, want 3 (size, vals, keys)", len(assignments))
	}
	if assignments[0].Target != 0 || assignments[1].Target != 1 || assignments[2].Target != 2 {
		t.Fatalf("targets = %d, %d, %d, want 0, 1, 2",
			assignments[0].Target, assignments[1].Target, assignments[2].Target)
	}
	for _, index := range []int{1, 2} {
		effect := assignments[index].Effect
		if effect.Kind != kernelbridge.LoopEffectConstState || !effect.Undef || effect.Null {
			t.Errorf("assignments[%d].Effect = %+v, want the undefined-carrying constState (Undef alone)", index, effect)
		}
	}
	stmts := assignsOf(assignments)
	exit := kernel.Walk([]kernelbridge.KnownStateWire{{Top: true}, {Top: true}, {Top: true}}, stmts)
	if !kernel.Member(loweringSetOf(t, exit[0]), []float64{0}) {
		t.Errorf("member(m.size, [0]) = false, want true")
	}
	if !exit[1].Undef || exit[1].Null {
		t.Errorf("m.vals admissions (undef=%v null=%v), want undefined alone — a missing read answers undefined, never null", exit[1].Undef, exit[1].Null)
	}
}

func TestMapSlots_ASeededMapDeclarationWritesTheCountAndTheJoins(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := mapLoweringContext(kernel,
		[]string{"m.size", "m.vals", "m.keys"},
		[]BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber})
	statements := loweringParse(t, `const m = new Map([[10, 1], [20, 2]]);`)
	assignments, ok := MapDeclarationAssignmentsOf(context, statements[0])
	if !ok {
		t.Fatalf("MapDeclarationAssignmentsOf ok = false, want true")
	}
	stmts := assignsOf(assignments)
	exit := kernel.Walk([]kernelbridge.KnownStateWire{{Top: true}, {Top: true}, {Top: true}}, stmts)
	if !kernel.Member(loweringSetOf(t, exit[0]), []float64{2}) {
		t.Errorf("member(m.size, [2]) = false, want true")
	}
	values := loweringSetOf(t, exit[1])
	for _, value := range []float64{1, 2} {
		if !kernel.Member(values, []float64{value}) {
			t.Errorf("member(m.vals, [%v]) = false, want true", value)
		}
	}
	if kernel.Member(values, []float64{10}) {
		t.Errorf("member(m.vals, [10]) = true, want false — 10 is a key, not a value")
	}
	keys := loweringSetOf(t, exit[2])
	for _, key := range []float64{10, 20} {
		if !kernel.Member(keys, []float64{key}) {
			t.Errorf("member(m.keys, [%v]) = false, want true", key)
		}
	}
}

func TestMapSlots_AUnionDeclarationWritesUnknownSizeAndJoinedVals(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := mapLoweringContext(kernel,
		[]string{"a.size", "a.vals", "b.size", "b.vals", "u.size", "u.vals"},
		[]BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber, BindingKindNumber, BindingKindNumber, BindingKindNumber})
	statements := loweringParse(t, `const u = a.union(b);`)
	assignments, ok := MapDeclarationAssignmentsOf(context, statements[0])
	if !ok {
		t.Fatalf("MapDeclarationAssignmentsOf(union) ok = false, want true")
	}
	if len(assignments) != 2 {
		t.Fatalf("len(assignments) = %d, want 2 (size, vals) — a producer is a Set, no keys slot", len(assignments))
	}
	if assignments[0].Target != 4 || assignments[0].Effect.Kind != kernelbridge.LoopEffectUnknown {
		t.Errorf("assignments[0] = %+v, want u.size (slot 4) := unknown", assignments[0])
	}
	if assignments[1].Target != 5 || assignments[1].Effect.Kind != kernelbridge.LoopEffectJoin {
		t.Errorf("assignments[1] = %+v, want u.vals (slot 5) := join(...)", assignments[1])
	}
	stmts := assignsOf(assignments)
	exit := kernel.Walk([]kernelbridge.KnownStateWire{
		{Top: true},
		{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{1}))},
		{Top: true},
		{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{2}))},
		{Top: true},
		{Top: true},
	}, stmts)
	vals := loweringSetOf(t, exit[5])
	for _, value := range []float64{1, 2} {
		if !kernel.Member(vals, []float64{value}) {
			t.Errorf("member(u.vals, [%v]) = false, want true — union draws from both operands' members", value)
		}
	}
}

func TestMapSlots_AnIntersectionDeclinesToLowerWithoutBothSlotsResolved(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	// only a's slots are in this context — b's are missing, so the
	// producer lowering cannot read a source to join from
	context := mapLoweringContext(kernel,
		[]string{"a.size", "a.vals", "u.size", "u.vals"},
		[]BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber, BindingKindNumber})
	if _, ok := MapDeclarationAssignmentsOf(context, loweringParse(t, `const u = a.intersection(b);`)[0]); ok {
		t.Errorf("a.intersection(b) lowered without b's slots resolved — there is nothing to join from")
	}
}
