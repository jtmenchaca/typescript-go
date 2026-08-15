// split from ir_map_slots_test.go — the for-of lowerings
//
// Which slot each pass hands the element binding over a Set, a values
// view, a keys view, and an entry pattern — and the shapes that decline.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

func TestMapSlots_AForOfOverASetIsTheLoopWhoseElementEffectIsTheValueSlot(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := mapLoweringContext(kernel,
		[]string{"s.size", "s.vals", "v", "total"},
		[]BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber, BindingKindNumber})
	statements := loweringParse(t, `for (const v of s) { total = total + v; }`)
	loop, ok := MapForOfLowering(context, statements[0])
	if !ok {
		t.Fatalf("MapForOfLowering(for-of over a Set) ok = false, want true")
	}
	if loop.Kind != kernelbridge.IrStatementLoop {
		t.Fatalf("kind = %q, want %q", loop.Kind, kernelbridge.IrStatementLoop)
	}
	if loop.Body[2].Kind != kernelbridge.LoopEffectVar || loop.Body[2].Index != 1 {
		t.Errorf("element binding effect = %+v, want a var read of slot 1 (s.vals)", loop.Body[2])
	}
	if !loop.Written[2] || !loop.Written[3] {
		t.Errorf("written = %v, want the element binding and the accumulator both written", loop.Written)
	}
	for index, set := range loop.Cond {
		if set != nil {
			t.Errorf("cond[%d] is set, want nil — a for-of has no numeric head", index)
		}
	}
}

func TestMapSlots_AForOfOverTheValuesViewReadsTheValueSlot(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := mapLoweringContext(kernel,
		[]string{"m.size", "m.vals", "m.keys", "v", "total"},
		[]BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber, BindingKindNumber, BindingKindNumber})
	statements := loweringParse(t, `for (const v of m.values()) { total = total + v; }`)
	loop, ok := MapForOfLowering(context, statements[0])
	if !ok {
		t.Fatalf("MapForOfLowering(for-of over m.values()) ok = false, want true")
	}
	if loop.Body[3].Kind != kernelbridge.LoopEffectVar || loop.Body[3].Index != 1 {
		t.Errorf("element binding effect = %+v, want a var read of slot 1 (m.vals)", loop.Body[3])
	}
}

func TestMapSlots_AForOfOverTheKeysViewReadsTheKeySlot(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := mapLoweringContext(kernel,
		[]string{"m.size", "m.vals", "m.keys", "k", "total"},
		[]BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber, BindingKindNumber, BindingKindNumber})
	statements := loweringParse(t, `for (const k of m.keys()) { total = total + k; }`)
	loop, ok := MapForOfLowering(context, statements[0])
	if !ok {
		t.Fatalf("MapForOfLowering(for-of over m.keys()) ok = false, want true")
	}
	if loop.Body[3].Kind != kernelbridge.LoopEffectVar || loop.Body[3].Index != 2 {
		t.Errorf("element binding effect = %+v, want a var read of slot 2 (m.keys)", loop.Body[3])
	}
}

func TestMapSlots_AnEntryPatternTakesTheKeyFromKeysAndTheValueFromVals(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := mapLoweringContext(kernel,
		[]string{"m.size", "m.vals", "m.keys", "k", "v", "total"},
		[]BindingKind{
			BindingKindNumber, BindingKindNumber, BindingKindNumber,
			BindingKindNumber, BindingKindNumber, BindingKindNumber,
		})
	statements := loweringParse(t, `for (const [k, v] of m) { total = total + v; }`)
	loop, ok := MapForOfLowering(context, statements[0])
	if !ok {
		t.Fatalf("MapForOfLowering(for-of over a Map's entries) ok = false, want true")
	}
	if loop.Body[3].Kind != kernelbridge.LoopEffectVar || loop.Body[3].Index != 2 {
		t.Errorf("k's effect = %+v, want a var read of slot 2 (m.keys)", loop.Body[3])
	}
	if loop.Body[4].Kind != kernelbridge.LoopEffectVar || loop.Body[4].Index != 1 {
		t.Errorf("v's effect = %+v, want a var read of slot 1 (m.vals)", loop.Body[4])
	}
}

func TestMapSlots_AnEntriesViewIsTheSamePairIteration(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := mapLoweringContext(kernel,
		[]string{"m.size", "m.vals", "m.keys", "k", "v", "total"},
		[]BindingKind{
			BindingKindNumber, BindingKindNumber, BindingKindNumber,
			BindingKindNumber, BindingKindNumber, BindingKindNumber,
		})
	statements := loweringParse(t, `for (const [k, v] of m.entries()) { total = total + v; }`)
	loop, ok := MapForOfLowering(context, statements[0])
	if !ok {
		t.Fatalf("MapForOfLowering(for-of over m.entries()) ok = false, want true")
	}
	if loop.Body[3].Index != 2 || loop.Body[4].Index != 1 {
		t.Errorf("pattern effects = %+v, %+v, want slots 2 (m.keys) and 1 (m.vals)",
			loop.Body[3], loop.Body[4])
	}
}

func TestMapSlots_ABareMapWithASingleNameBindingDeclines(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := mapLoweringContext(kernel,
		[]string{"m.size", "m.vals", "m.keys", "e", "total"},
		[]BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber, BindingKindNumber, BindingKindNumber})
	statements := loweringParse(t, `for (const e of m) { total = total + 1; }`)
	if _, ok := MapForOfLowering(context, statements[0]); ok {
		t.Errorf("a bare Map bound to one name lowered — each pass hands a PAIR, which one slot cannot hold")
	}
}

func TestMapSlots_AThreeNamePatternDeclines(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := mapLoweringContext(kernel,
		[]string{"m.size", "m.vals", "m.keys", "k", "v", "w"},
		[]BindingKind{
			BindingKindNumber, BindingKindNumber, BindingKindNumber,
			BindingKindNumber, BindingKindNumber, BindingKindNumber,
		})
	statements := loweringParse(t, `for (const [k, v, w] of m) { k = k; }`)
	if _, ok := MapForOfLowering(context, statements[0]); ok {
		t.Errorf("a three-name pattern lowered — an entry is exactly two values")
	}
}

func TestMapSlots_AForAwaitOverACollectionDeclines(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := mapLoweringContext(kernel,
		[]string{"s.size", "s.vals", "v", "total"},
		[]BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber, BindingKindNumber})
	statements := loweringParse(t, `async function g() { for await (const v of s) { total = total + v; } }`)
	inner := statements[0].Body().AsBlock().Statements.Nodes[0]
	if _, ok := MapForOfLowering(context, inner); ok {
		t.Errorf("a for-await lowered — the binding takes the AWAITED value, which the slot does not hold")
	}
}
