// split from ir_map_slots_test.go — the slot reads
//
// The two reads a flattened collection answers: `m.get(k)` as the value
// slot or absent, and `m.size` as the ordinary number slot a guard and
// an assignment both land on.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

func TestMapSlots_AGetReadsTheValueSlotOrAbsent(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := mapLoweringContext(kernel,
		[]string{"m.size", "m.vals", "m.keys"},
		[]BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber})
	statements := loweringParse(t, `m.get(7);`)
	read := statements[0].AsExpressionStatement().Expression
	effect, ok := MapGetReadEffect(context, read)
	if !ok {
		t.Fatalf("MapGetReadEffect ok = false, want true")
	}
	if effect.Kind != kernelbridge.LoopEffectOrAbsent {
		t.Fatalf("effect kind = %q, want %q — a missing key answers undefined",
			effect.Kind, kernelbridge.LoopEffectOrAbsent)
	}
	if effect.A == nil || effect.A.Kind != kernelbridge.LoopEffectVar || effect.A.Index != 1 {
		t.Errorf("or-absent operand = %+v, want a var read of slot 1 (m.vals)", effect.A)
	}
}

func TestMapSlots_AGetOnASetDeclines(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := mapLoweringContext(kernel,
		[]string{"s.size", "s.vals"},
		[]BindingKind{BindingKindNumber, BindingKindNumber})
	statements := loweringParse(t, `s.get(7);`)
	read := statements[0].AsExpressionStatement().Expression
	if _, ok := MapGetReadEffect(context, read); ok {
		t.Errorf("s.get(7) read on a Set — get is a Map operation")
	}
}

func TestMapSlots_ASizeReadResolvesThroughIndexOf(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := mapLoweringContext(kernel,
		[]string{"m.size", "m.vals", "m.keys", "n"},
		[]BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber, BindingKindNumber})
	statements := loweringParse(t, `n = m.size;`)
	assignment, ok := AssignmentOf(context, statements[0])
	if !ok {
		t.Fatalf("AssignmentOf(n = m.size) ok = false, want true")
	}
	if assignment.Effect.Kind != kernelbridge.LoopEffectVar || assignment.Effect.Index != 0 {
		t.Errorf("effect = %+v, want a var read of slot 0 (m.size)", assignment.Effect)
	}
}

func TestMapSlots_ASizeGuardIsTheOrdinaryTwoSlotComparison(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := mapLoweringContext(kernel,
		[]string{"m.size", "m.vals", "m.keys", "i", "x"},
		[]BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber, BindingKindNumber, BindingKindNumber})
	stmts, ok := LowerStatements(context, loweringParse(t, `if (i < m.size) { x = 1; }`))
	if !ok {
		t.Fatalf("LowerStatements(i < m.size) ok = false, want true")
	}
	if stmts[0].Kind != kernelbridge.IrStatementBranch {
		t.Fatalf("stmts[0].Kind = %q, want %q", stmts[0].Kind, kernelbridge.IrStatementBranch)
	}
	if stmts[0].On != 3 || stmts[0].OnB != 0 {
		t.Errorf("branch slots = (%d, %d), want (3 = i, 0 = m.size)", stmts[0].On, stmts[0].OnB)
	}
}
