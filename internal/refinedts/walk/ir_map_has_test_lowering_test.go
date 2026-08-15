// split from ir_map_slots_test.go — the has-in-test-position lowering
//
// A `has` standing as the whole test of an if lowers through the opaque
// branch: both arms present, nothing claimed about the condition, and a
// writing head falling back to the havoc floor.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

func TestMapSlots_AHasTestLowersAsTheOpaqueBranchWithBothArmsPresent(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := mapLoweringContext(kernel,
		[]string{"m.size", "m.vals", "m.keys", "k", "x"},
		[]BindingKind{
			BindingKindNumber, BindingKindNumber, BindingKindNumber,
			BindingKindNumber, BindingKindNumber,
		})
	stmts, ok := LowerStatements(context, loweringParse(t, `if (m.has(k)) { x = 1; } else { x = 2; }`))
	if !ok {
		t.Fatalf("LowerStatements(if (m.has(k))) ok = false, want true")
	}
	if len(stmts) != 1 {
		t.Fatalf("len(stmts) = %d, want 1", len(stmts))
	}
	if stmts[0].Kind != kernelbridge.IrStatementBranchBoth {
		t.Fatalf("stmts[0].Kind = %q, want %q — no leaf reads a has()",
			stmts[0].Kind, kernelbridge.IrStatementBranchBoth)
	}
	if len(stmts[0].Then) != 1 || len(stmts[0].Else) != 1 {
		t.Fatalf("arms = %d then, %d else, want 1 and 1", len(stmts[0].Then), len(stmts[0].Else))
	}
	// both arms walk from the state as it stood, so the exit admits each
	// arm's write and nothing else
	exit := kernel.Walk([]kernelbridge.KnownStateWire{
		{Top: true}, {Top: true}, {Top: true}, {Top: true}, {Top: true},
	}, stmts)
	x := loweringSetOf(t, exit[4])
	for _, written := range []float64{1, 2} {
		if !kernel.Member(x, []float64{written}) {
			t.Errorf("member(x, [%v]) = false, want true — the join admits both arms", written)
		}
	}
	if kernel.Member(x, []float64{3}) {
		t.Errorf("member(x, [3]) = true, want false — neither arm writes 3")
	}
}

func TestMapSlots_AHasTestWithNoElseArmLowersWithAnEmptyElseList(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := mapLoweringContext(kernel,
		[]string{"m.size", "m.vals", "m.keys", "k", "x"},
		[]BindingKind{
			BindingKindNumber, BindingKindNumber, BindingKindNumber,
			BindingKindNumber, BindingKindNumber,
		})
	stmts, ok := LowerStatements(context, loweringParse(t, `if (m.has(k)) { x = 1; }`))
	if !ok {
		t.Fatalf("LowerStatements(if (m.has(k)), no else) ok = false, want true")
	}
	if stmts[0].Kind != kernelbridge.IrStatementBranchBoth {
		t.Fatalf("stmts[0].Kind = %q, want %q", stmts[0].Kind, kernelbridge.IrStatementBranchBoth)
	}
	if len(stmts[0].Else) != 0 {
		t.Errorf("len(Else) = %d, want 0 — a missing else is the empty arm", len(stmts[0].Else))
	}
	exit := kernel.Walk([]kernelbridge.KnownStateWire{
		{Top: true}, {Top: true}, {Top: true}, {Top: true},
		{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{7}))},
	}, stmts)
	x := loweringSetOf(t, exit[4])
	for _, reading := range []float64{1, 7} {
		if !kernel.Member(x, []float64{reading}) {
			t.Errorf("member(x, [%v]) = false, want true — the empty arm leaves x where it stood", reading)
		}
	}
}

func TestMapSlots_AWriteInsideAnUnreadableTestStillDeclines(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := mapLoweringContext(kernel,
		[]string{"m.size", "m.vals", "m.keys", "k", "x"},
		[]BindingKind{
			BindingKindNumber, BindingKindNumber, BindingKindNumber,
			BindingKindNumber, BindingKindNumber,
		})
	// the branchBoth fallback refuses a writing test, and the statement
	// then takes the TOTAL-LOWERING floor: every slot the if could have
	// touched havocs — k (the step), x (both arms), and the collection's
	// leaves (mentioned) — which COVERS the step rather than skipping it
	stmts, ok := LowerStatements(context, loweringParse(t, `if (m.has(k++)) { x = 1; } else { x = 2; }`))
	if !ok {
		t.Fatalf("a writing test declined outright, want the havoc floor")
	}
	havocked := map[int]struct{}{}
	for _, s := range stmts {
		if s.Kind != kernelbridge.IrStatementAssign || s.Effect.Kind != kernelbridge.LoopEffectUnknown {
			t.Fatalf("stmts = %+v, want only unknown assigns", stmts)
		}
		havocked[s.Target] = struct{}{}
	}
	for _, slot := range []int{3, 4} { // k and x
		if _, hit := havocked[slot]; !hit {
			t.Errorf("slot %d not havocked — the step or the arms' writes would be skipped", slot)
		}
	}
	// the awaited head: parsed inside an async body so `await` is the
	// operator and not an identifier, then asked of the gate directly —
	// no route reads an await in a condition, so it declines rather than
	// riding as "no claim"
	async := loweringParse(t, `async function g() { if (await h()) { x = 1; } }`)[0]
	inner := async.Body().AsBlock().Statements.Nodes[0]
	if OpaqueTestableCondition(inner.AsIfStatement().Expression) {
		t.Errorf("an awaited test was admitted as opaque — an await in a head has no lowering anywhere, so it must decline")
	}
	// a plain call in the head IS admitted: it writes no tracked slot,
	// and its result is exactly what the branch declines to read
	plain := loweringParse(t, `if (h(k)) { x = 1; }`)[0]
	if !OpaqueTestableCondition(plain.AsIfStatement().Expression) {
		t.Errorf("a plain call head was refused as opaque — a callee cannot write this body's tracked slots")
	}
}
