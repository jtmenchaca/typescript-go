// split from ir_map_slots_test.go — the declaration lowering
//
// What a `new Map()` / `new Map([[k, v], …])` declaration writes into
// the three slots: the row count, and the joined seed values and keys.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
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
		if effect.Kind != kernelbridge.LoopEffectConstState || !effect.Absent {
			t.Errorf("assignments[%d].Effect = %+v, want the absent-carrying constState", index, effect)
		}
	}
	stmts := assignsOf(assignments)
	exit := kernel.Walk([]kernelbridge.KnownStateWire{{Top: true}, {Top: true}, {Top: true}}, stmts)
	if !kernel.Member(loweringSetOf(t, exit[0]), []float64{0}) {
		t.Errorf("member(m.size, [0]) = false, want true")
	}
	if !exit[1].Absent {
		t.Errorf("m.vals.Absent = false, want true — an empty Map has no value to read")
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
