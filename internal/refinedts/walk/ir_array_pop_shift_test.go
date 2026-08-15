// Tests for ir_array_pop_shift.go: `a.pop()` / `a.shift()` — the
// shrinking pair's slot writes and the value they answer.
//
// ArrayShrinkAssignmentsOf is not yet wired into
// lowerFlatteningRoutes (that hook lives in
// lowering_to_kernel_ir_flattening.go, outside this package's array
// files), so these tests call the dispatcher directly on a parsed
// statement — the same shape the hook will hand it once landed.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

func TestArrayShrink_APopAsABareStatementStepsTheLenSlotDownAndLeavesElemAlone(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := arrayLoweringContext(kernel,
		[]string{"a.len", "a.elem"},
		[]BindingKind{BindingKindNumber, BindingKindNumber})
	statements := loweringParse(t, `a.pop();`)
	assignments, ok := ArrayShrinkAssignmentsOf(context, statements[0])
	if !ok {
		t.Fatalf("ArrayShrinkAssignmentsOf(a.pop()) ok = false, want true")
	}
	if len(assignments) != 1 {
		t.Fatalf("len(assignments) = %d, want 1 — only the len slot writes for a discarded pop", len(assignments))
	}
	if assignments[0].Target != 0 {
		t.Errorf("target = %d, want 0 (a.len)", assignments[0].Target)
	}
	exit := kernel.Walk([]kernelbridge.KnownStateWire{
		{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{3}))},
		{Top: true},
	}, []kernelbridge.IrStatement{{Kind: kernelbridge.IrStatementAssign, Target: 0, Effect: assignments[0].Effect}})
	if !kernel.Member(loweringSetOf(t, exit[0]), []float64{2}) {
		t.Errorf("member(a.len, [2]) = false, want true — one pop off a len-3 array")
	}
}

func TestArrayShrink_APopOverAnEmptyArrayClampsTheLenSlotAtZero(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := arrayLoweringContext(kernel,
		[]string{"a.len", "a.elem"},
		[]BindingKind{BindingKindNumber, BindingKindNumber})
	statements := loweringParse(t, `a.pop();`)
	assignments, ok := ArrayShrinkAssignmentsOf(context, statements[0])
	if !ok {
		t.Fatalf("ArrayShrinkAssignmentsOf(a.pop()) ok = false, want true")
	}
	exit := kernel.Walk([]kernelbridge.KnownStateWire{
		{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{0}))},
		{Top: true},
	}, []kernelbridge.IrStatement{{Kind: kernelbridge.IrStatementAssign, Target: 0, Effect: assignments[0].Effect}})
	result := loweringSetOf(t, exit[0])
	if !kernel.Member(result, []float64{0}) {
		t.Errorf("member(a.len, [0]) = false, want true — max(0, -1) clamps at zero")
	}
	if kernel.Member(result, []float64{-1}) {
		t.Errorf("member(a.len, [-1]) = true, want false — the len slot never goes negative")
	}
}

func TestArrayShrink_APopReadIntoAConstDeclarationAnswersTheElemSlotOrAbsent(t *testing.T) {
	context := &LoweringContext{
		Bindings: []string{"a.len", "a.elem", "n"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber},
		Typeofs:  make([]TypeofTag, 3),
	}
	statements := loweringParse(t, `const n = a.pop();`)
	assignments, ok := ArrayShrinkAssignmentsOf(context, statements[0])
	if !ok {
		t.Fatalf("ArrayShrinkAssignmentsOf(const n = a.pop()) ok = false, want true")
	}
	if len(assignments) != 2 {
		t.Fatalf("len(assignments) = %d, want 2 (len step, then n := elem or-absent)", len(assignments))
	}
	if assignments[0].Target != 0 {
		t.Errorf("assignments[0].Target = %d, want 0 (a.len)", assignments[0].Target)
	}
	if assignments[1].Target != 2 {
		t.Errorf("assignments[1].Target = %d, want 2 (n)", assignments[1].Target)
	}
	valueEffect := assignments[1].Effect
	if valueEffect.Kind != kernelbridge.LoopEffectOrAbsent {
		t.Fatalf("value effect.Kind = %q, want %q", valueEffect.Kind, kernelbridge.LoopEffectOrAbsent)
	}
	if valueEffect.A == nil || valueEffect.A.Kind != kernelbridge.LoopEffectVar || valueEffect.A.Index != 1 {
		t.Errorf("or-absent operand = %+v, want a var read of slot 1 (a.elem)", valueEffect.A)
	}
}

func TestArrayShrink_AShiftAssignedIntoAnExistingNameReadsTheElemSlotOrAbsent(t *testing.T) {
	context := &LoweringContext{
		Bindings: []string{"a.len", "a.elem", "n"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber},
		Typeofs:  make([]TypeofTag, 3),
	}
	statements := loweringParse(t, `n = a.shift();`)
	assignments, ok := ArrayShrinkAssignmentsOf(context, statements[0])
	if !ok {
		t.Fatalf("ArrayShrinkAssignmentsOf(n = a.shift()) ok = false, want true")
	}
	if len(assignments) != 2 {
		t.Fatalf("len(assignments) = %d, want 2", len(assignments))
	}
	if assignments[1].Effect.Kind != kernelbridge.LoopEffectOrAbsent {
		t.Errorf("assignments[1].Effect.Kind = %q, want %q", assignments[1].Effect.Kind, kernelbridge.LoopEffectOrAbsent)
	}
}

func TestArrayShrink_ATargetSortDisagreementDeclines(t *testing.T) {
	context := &LoweringContext{
		Bindings: []string{"a.len", "a.elem", "s"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber, BindingKindString},
		Typeofs:  make([]TypeofTag, 3),
	}
	statements := loweringParse(t, `const s = a.pop();`)
	if _, ok := ArrayShrinkAssignmentsOf(context, statements[0]); ok {
		t.Errorf("ArrayShrinkAssignmentsOf ok = true, want false — a string target cannot hold a number-sorted element")
	}
}

func TestArrayShrink_AnUnflattenedReceiverDeclines(t *testing.T) {
	context := &LoweringContext{
		Bindings: []string{"b"},
		Sorts:    []BindingKind{BindingKindNumber},
		Typeofs:  make([]TypeofTag, 1),
	}
	statements := loweringParse(t, `a.pop();`)
	if _, ok := ArrayShrinkAssignmentsOf(context, statements[0]); ok {
		t.Errorf("ArrayShrinkAssignmentsOf over an unflattened receiver ok = true, want false")
	}
}

func TestArrayShrink_APopWithAnArgumentDeclines(t *testing.T) {
	context := &LoweringContext{
		Bindings: []string{"a.len", "a.elem"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber},
		Typeofs:  make([]TypeofTag, 2),
	}
	statements := loweringParse(t, `a.pop(1);`)
	if _, ok := ArrayShrinkAssignmentsOf(context, statements[0]); ok {
		t.Errorf("ArrayShrinkAssignmentsOf(a.pop(1)) ok = true, want false — pop takes no argument")
	}
}
