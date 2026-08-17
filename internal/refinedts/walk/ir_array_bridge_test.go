// split from ir_array_slots_test.go — the BRIDGE's tests

package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// The BRIDGE: `const a = [...m.values()]` and `Array.from(…)` flatten as
// an ARRAY local whose two slots are written from the collection's size
// and value slots. The point of every test below is that the resulting
// local is INDISTINGUISHABLE from a literal-initialized one — the same
// slot spellings, the same recognizer answers, the same reads.

// bridgeLocalsOfSource parses a function body and answers both tables:
// the flattened collections and the flattened arrays built over them.
func bridgeLocalsOfSource(t *testing.T, source string) (map[*ast.Node]MapLocal, map[*ast.Node]ArrayLocal) {
	t.Helper()
	declaration := summaryDeclarationOf(t, source)
	body := declaration.Body()
	locals, ok := CollectLocals(body)
	if !ok {
		t.Fatalf("CollectLocals ok = false, want the locals collected")
	}
	collections := MapLocalsOf(body, locals.Locals)
	return collections, ArrayLocalsOf(body, locals.Locals, collections)
}

func TestArraySlots_ASpreadOfAMapsValuesBridgesToAnArrayLocal(t *testing.T) {
	collections, arrays := bridgeLocalsOfSource(t,
		"function f(k: number) { const m = new Map(); m.set(k, 1); const a = [...m.values()]; return a.length; }")
	if len(collections) != 1 {
		t.Fatalf("MapLocalsOf found %d collections, want 1", len(collections))
	}
	if len(arrays) != 1 {
		t.Fatalf("ArrayLocalsOf found %d arrays, want 1 — the bridge is an array local", len(arrays))
	}
	for _, local := range arrays {
		// the slot spelling a literal-built array wears, exactly
		if local.LenSlotName != "a.len" || local.ElemSlotName != "a.elem" {
			t.Errorf("slot names = %q, %q, want a.len, a.elem", local.LenSlotName, local.ElemSlotName)
		}
		if local.BridgedFrom != "m" {
			t.Errorf("BridgedFrom = %q, want m", local.BridgedFrom)
		}
		// the recognizer answers a downstream reader consults
		if got := ArrayElementSort(local); got != BindingKindNumber {
			t.Errorf("element sort = %q, want number", got)
		}
	}
}

func TestArraySlots_ArrayFromOverASetBridgesTheSameWay(t *testing.T) {
	_, arrays := bridgeLocalsOfSource(t,
		"function f(v: number) { const s = new Set(); s.add(v); const a = Array.from(s); return a.length; }")
	if len(arrays) != 1 {
		t.Fatalf("ArrayLocalsOf found %d arrays, want 1", len(arrays))
	}
	for _, local := range arrays {
		if local.BridgedFrom != "s" {
			t.Errorf("BridgedFrom = %q, want s", local.BridgedFrom)
		}
	}
}

func TestArraySlots_ASpreadOfASetBridgesToAnArrayLocal(t *testing.T) {
	_, arrays := bridgeLocalsOfSource(t,
		"function f(v: number) { const s = new Set(); s.add(v); const a = [...s]; return a.length; }")
	if len(arrays) != 1 {
		t.Fatalf("ArrayLocalsOf found %d arrays, want 1 — a bare Set spreads its members", len(arrays))
	}
}

func TestArraySlots_ABareSpreadOfAMapDeclinesBothTheBridgeAndTheCollection(t *testing.T) {
	collections, arrays := bridgeLocalsOfSource(t,
		"function f(k: number) { const m = new Map(); m.set(k, 1); const a = [...m]; return a.length; }")
	if len(arrays) != 0 {
		t.Errorf("a bare Map spread bridged — each entry is a PAIR, which one element slot cannot hold")
	}
	if len(collections) != 0 {
		t.Errorf("the collection flattened past a bare spread — the recognizer refuses the use, not just the bridge")
	}
}

func TestArraySlots_ABridgeOverAnUnflattenedCollectionDeclines(t *testing.T) {
	// a has() OUT of test position takes the collection down (in test
	// position the opaque branch serves it now), so there are no source
	// slots for the bridge to read
	collections, arrays := bridgeLocalsOfSource(t,
		"function f(k: number) { const m = new Map(); m.set(k, 1); const b = m.has(k); const a = [...m.values()]; return a.length; }")
	if len(collections) != 0 {
		t.Fatalf("the out-of-test has() left the collection flattened, want it declined")
	}
	if len(arrays) != 0 {
		t.Errorf("a bridge over an unflattened collection flattened — there is nothing to copy from")
	}
}

func TestArraySlots_ASpreadBesideAnotherElementDeclinesTheBridge(t *testing.T) {
	_, arrays := bridgeLocalsOfSource(t,
		"function f(v: number) { const s = new Set(); s.add(v); const a = [0, ...s]; return a.length; }")
	if len(arrays) != 0 {
		t.Errorf("a spread beside another element bridged — the count is not the collection's size")
	}
}

func TestArraySlots_TheBridgeWritesTheLenFromTheSizeAndTheElemFromTheValues(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := arrayLoweringContext(kernel,
		[]string{"m.size", "m.vals", "m.keys", "a.len", "a.elem"},
		[]BindingKind{
			BindingKindNumber, BindingKindNumber, BindingKindNumber,
			BindingKindNumber, BindingKindNumber,
		})
	stmts, ok := LowerStatements(context, loweringParse(t, `const a = [...m.values()];`))
	if !ok {
		t.Fatalf("LowerStatements(bridge) ok = false, want true")
	}
	if len(stmts) != 2 {
		t.Fatalf("len(stmts) = %d, want 2 (len then elem)", len(stmts))
	}
	if stmts[0].Target != 3 || stmts[0].Effect.Kind != kernelbridge.LoopEffectVarState || stmts[0].Effect.Index != 0 {
		t.Errorf("len write = target %d, effect %+v, want target 3 (a.len) from a verbatim copy of slot 0 (m.size)",
			stmts[0].Target, stmts[0].Effect)
	}
	if stmts[1].Target != 4 || stmts[1].Effect.Kind != kernelbridge.LoopEffectVarState || stmts[1].Effect.Index != 1 {
		t.Errorf("elem write = target %d, effect %+v, want target 4 (a.elem) from a verbatim copy of slot 1 (m.vals)",
			stmts[1].Target, stmts[1].Effect)
	}
	exit := kernel.Walk([]kernelbridge.KnownStateWire{
		{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{2}))},
		{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{7, 8}))},
		{Top: true},
		{Top: true},
		{Top: true},
	}, stmts)
	if !kernel.Member(loweringSetOf(t, exit[3]), []float64{2}) {
		t.Errorf("member(a.len, [2]) = false, want true — the bridged length IS the collection's size")
	}
	elements := loweringSetOf(t, exit[4])
	for _, value := range []float64{7, 8} {
		if !kernel.Member(elements, []float64{value}) {
			t.Errorf("member(a.elem, [%v]) = false, want true", value)
		}
	}
}

func TestArraySlots_AKeysBridgeWritesTheElemFromTheKeySlot(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := arrayLoweringContext(kernel,
		[]string{"m.size", "m.vals", "m.keys", "a.len", "a.elem"},
		[]BindingKind{
			BindingKindNumber, BindingKindNumber, BindingKindNumber,
			BindingKindNumber, BindingKindNumber,
		})
	stmts, ok := LowerStatements(context, loweringParse(t, `const a = Array.from(m.keys());`))
	if !ok {
		t.Fatalf("LowerStatements(keys bridge) ok = false, want true")
	}
	if stmts[1].Effect.Index != 2 {
		t.Errorf("elem effect = %+v, want a var read of slot 2 (m.keys)", stmts[1].Effect)
	}
}

func TestArraySlots_ABridgedArrayReadsExactlyLikeALiteralOne(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := arrayLoweringContext(kernel,
		[]string{"m.size", "m.vals", "m.keys", "a.len", "a.elem", "i", "x", "n"},
		[]BindingKind{
			BindingKindNumber, BindingKindNumber, BindingKindNumber,
			BindingKindNumber, BindingKindNumber,
			BindingKindNumber, BindingKindNumber, BindingKindNumber,
		})
	// every downstream reader in one body: the length read, the unguarded
	// index read, the guarded one, and a push
	stmts, ok := LowerStatements(context, loweringParse(t, `
		const a = [...m.values()];
		n = a.length;
		x = a[i];
		a.push(5);
		if (i < a.length) { x = a[i]; }
	`))
	if !ok {
		t.Fatalf("LowerStatements over a bridged array ok = false, want true")
	}
	// n = a.length — a plain assignment copying the len slot verbatim
	if stmts[2].Effect.Kind != kernelbridge.LoopEffectVarState || stmts[2].Effect.Index != 3 {
		t.Errorf("length read = %+v, want a verbatim copy of slot 3 (a.len)", stmts[2].Effect)
	}
	// x = a[i] — the or-absent wrapping, nothing bounding i
	if stmts[3].Effect.Kind != kernelbridge.LoopEffectOrAbsent {
		t.Errorf("unguarded index read = %q, want %q", stmts[3].Effect.Kind, kernelbridge.LoopEffectOrAbsent)
	}
	// a.push(5) — the length steps and the element slot joins
	if stmts[4].Effect.Kind != kernelbridge.LoopEffectBinary || stmts[4].Effect.Op != kernelbridge.LoopOpAdd {
		t.Errorf("push length effect = %+v, want an add", stmts[4].Effect)
	}
	if stmts[5].Effect.Kind != kernelbridge.LoopEffectJoin {
		t.Errorf("push element effect kind = %q, want %q", stmts[5].Effect.Kind, kernelbridge.LoopEffectJoin)
	}
	// the guarded read — the two-slot comparison against the bridged len
	guard := stmts[6]
	if guard.Kind != kernelbridge.IrStatementBranch || guard.Test != kernelbridge.IrTestLtSlot {
		t.Fatalf("guard = kind %q, test %q, want a branch on %q", guard.Kind, guard.Test, kernelbridge.IrTestLtSlot)
	}
	if guard.On != 5 || guard.OnB != 3 {
		t.Errorf("branch slots = (%d, %d), want (5 = i, 3 = a.len)", guard.On, guard.OnB)
	}
	if len(guard.Then) != 1 || guard.Then[0].Effect.Kind != kernelbridge.LoopEffectVarState ||
		guard.Then[0].Effect.Index != 4 {
		t.Errorf("guarded read = %+v, want a verbatim copy of slot 4 (a.elem)", guard.Then)
	}
}

func TestArraySlots_AForOfOverABridgedArrayIsTheOrdinaryArrayLoop(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := arrayLoweringContext(kernel,
		[]string{"m.size", "m.vals", "m.keys", "a.len", "a.elem", "x", "total"},
		[]BindingKind{
			BindingKindNumber, BindingKindNumber, BindingKindNumber,
			BindingKindNumber, BindingKindNumber,
			BindingKindNumber, BindingKindNumber,
		})
	stmts, ok := LowerStatements(context, loweringParse(t, `
		const a = [...m.values()];
		for (const x of a) { total = total + x; }
	`))
	if !ok {
		t.Fatalf("LowerStatements(bridged for-of) ok = false, want true")
	}
	loop := stmts[len(stmts)-1]
	if loop.Kind != kernelbridge.IrStatementLoop {
		t.Fatalf("last statement kind = %q, want %q", loop.Kind, kernelbridge.IrStatementLoop)
	}
	if loop.Body[5].Kind != kernelbridge.LoopEffectVar || loop.Body[5].Index != 4 {
		t.Errorf("element binding effect = %+v, want a var read of slot 4 (a.elem)", loop.Body[5])
	}
}
