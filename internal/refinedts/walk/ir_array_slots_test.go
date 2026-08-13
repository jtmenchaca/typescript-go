// Array locals flattened into two slots — the length and the join of
// the elements — lowered from real source and walked by the kernel.
// Skipped (never a faked pass) when the native kernel dylib is absent,
// the same gate every other walk test here uses.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// arrayLoweringContext is the two-slot layout every test below shares:
// the array's len and elem slots, plus whatever scalars the case needs.
func arrayLoweringContext(kernel *kernelbridge.RefinedTSKernel, bindings []string, sorts []BindingKind) *LoweringContext {
	return &LoweringContext{
		Bindings: bindings,
		Sorts:    sorts,
		Typeofs:  make([]TypeofTag, len(bindings)),
		Narrow:   kernel.Narrow,
	}
}

func TestArraySlots_AnArrayLiteralWritesTheCountIntoTheLenSlotAndTheJoinIntoTheElemSlot(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := arrayLoweringContext(kernel,
		[]string{"a.len", "a.elem"},
		[]BindingKind{BindingKindNumber, BindingKindNumber})
	stmts, ok := LowerStatements(context, loweringParse(t, `const a = [1, 2, 3];`))
	if !ok {
		t.Fatalf("LowerStatements(array literal) ok = false, want true")
	}
	if len(stmts) != 2 {
		t.Fatalf("len(stmts) = %d, want 2 (len then elem)", len(stmts))
	}
	if stmts[0].Target != 0 || stmts[1].Target != 1 {
		t.Fatalf("targets = %d, %d, want 0 (a.len), 1 (a.elem)", stmts[0].Target, stmts[1].Target)
	}
	exit := kernel.Walk([]kernelbridge.KnownStateWire{{Top: true}, {Top: true}}, stmts)
	length := exit[0]
	if length.Top {
		t.Fatalf("a.len.Top = true, want false")
	}
	if !kernel.Member(loweringSetOf(t, length), []float64{3}) {
		t.Errorf("member(a.len, [3]) = false, want true")
	}
	if kernel.Member(loweringSetOf(t, length), []float64{2}) {
		t.Errorf("member(a.len, [2]) = true, want false")
	}
	element := exit[1]
	if element.Top {
		t.Fatalf("a.elem.Top = true, want false")
	}
	elementSet := loweringSetOf(t, element)
	for _, value := range []float64{1, 2, 3} {
		if !kernel.Member(elementSet, []float64{value}) {
			t.Errorf("member(a.elem, [%v]) = false, want true", value)
		}
	}
	if kernel.Member(elementSet, []float64{4}) {
		t.Errorf("member(a.elem, [4]) = true, want false")
	}
}

func TestArraySlots_AnEmptyArrayLiteralStartsTheElemSlotAtTheAbsentCarryingConstant(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := arrayLoweringContext(kernel,
		[]string{"a.len", "a.elem"},
		[]BindingKind{BindingKindNumber, BindingKindNumber})
	stmts, ok := LowerStatements(context, loweringParse(t, `const a = [];`))
	if !ok {
		t.Fatalf("LowerStatements(empty array) ok = false, want true")
	}
	if len(stmts) != 2 {
		t.Fatalf("len(stmts) = %d, want 2", len(stmts))
	}
	if stmts[1].Effect.Kind != kernelbridge.LoopEffectConstState || !stmts[1].Effect.Absent {
		t.Errorf("elem effect = %+v, want the absent-carrying constState", stmts[1].Effect)
	}
	exit := kernel.Walk([]kernelbridge.KnownStateWire{{Top: true}, {Top: true}}, stmts)
	if !kernel.Member(loweringSetOf(t, exit[0]), []float64{0}) {
		t.Errorf("member(a.len, [0]) = false, want true")
	}
	if !exit[1].Absent {
		t.Errorf("a.elem.Absent = false, want true — an empty array has no element to read")
	}
}

func TestArraySlots_ALengthReadResolvesToTheLenSlot(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := arrayLoweringContext(kernel,
		[]string{"a.len", "a.elem", "n"},
		[]BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber})
	stmts, ok := LowerStatements(context, loweringParse(t, `n = a.length;`))
	if !ok {
		t.Fatalf("LowerStatements(a.length) ok = false, want true")
	}
	if stmts[0].Effect.Kind != kernelbridge.LoopEffectVar || stmts[0].Effect.Index != 0 {
		t.Errorf("effect = %+v, want a var read of slot 0 (a.len)", stmts[0].Effect)
	}
}

func TestArraySlots_APushStepsTheLengthAndJoinsThePushedValueIntoTheElemSlot(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := arrayLoweringContext(kernel,
		[]string{"a.len", "a.elem"},
		[]BindingKind{BindingKindNumber, BindingKindNumber})
	stmts, ok := LowerStatements(context, loweringParse(t, `a.push(9);`))
	if !ok {
		t.Fatalf("LowerStatements(a.push) ok = false, want true")
	}
	if len(stmts) != 2 {
		t.Fatalf("len(stmts) = %d, want 2 (len step then elem join)", len(stmts))
	}
	if stmts[0].Effect.Kind != kernelbridge.LoopEffectBinary || stmts[0].Effect.Op != kernelbridge.LoopOpAdd {
		t.Errorf("len effect = %+v, want an add", stmts[0].Effect)
	}
	if stmts[1].Effect.Kind != kernelbridge.LoopEffectJoin {
		t.Errorf("elem effect kind = %q, want %q", stmts[1].Effect.Kind, kernelbridge.LoopEffectJoin)
	}
	exit := kernel.Walk([]kernelbridge.KnownStateWire{
		{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{2}))},
		{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{1, 2}))},
	}, stmts)
	if !kernel.Member(loweringSetOf(t, exit[0]), []float64{3}) {
		t.Errorf("member(a.len, [3]) = false, want true")
	}
	elementSet := loweringSetOf(t, exit[1])
	for _, value := range []float64{1, 2, 9} {
		if !kernel.Member(elementSet, []float64{value}) {
			t.Errorf("member(a.elem, [%v]) = false, want true", value)
		}
	}
}

func TestArraySlots_AnIndexWriteJoinsIntoTheElemSlotAndLeavesTheLengthAlone(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := arrayLoweringContext(kernel,
		[]string{"a.len", "a.elem", "i"},
		[]BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber})
	stmts, ok := LowerStatements(context, loweringParse(t, `a[i] = 7;`))
	if !ok {
		t.Fatalf("LowerStatements(a[i] = 7) ok = false, want true")
	}
	if len(stmts) != 1 {
		t.Fatalf("len(stmts) = %d, want 1 — the length is untouched", len(stmts))
	}
	if stmts[0].Target != 1 {
		t.Errorf("target = %d, want 1 (a.elem)", stmts[0].Target)
	}
	if stmts[0].Effect.Kind != kernelbridge.LoopEffectJoin {
		t.Errorf("effect kind = %q, want %q — a weak update", stmts[0].Effect.Kind, kernelbridge.LoopEffectJoin)
	}
}

func TestArraySlots_AnUnguardedIndexReadCarriesTheOrAbsentWrapping(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := arrayLoweringContext(kernel,
		[]string{"a.len", "a.elem", "i", "x"},
		[]BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber, BindingKindNumber})
	stmts, ok := LowerStatements(context, loweringParse(t, `x = a[i];`))
	if !ok {
		t.Fatalf("LowerStatements(x = a[i]) ok = false, want true")
	}
	if stmts[0].Effect.Kind != kernelbridge.LoopEffectOrAbsent {
		t.Fatalf("effect kind = %q, want %q — nothing bounds i", stmts[0].Effect.Kind, kernelbridge.LoopEffectOrAbsent)
	}
	if stmts[0].Effect.A == nil || stmts[0].Effect.A.Kind != kernelbridge.LoopEffectVar || stmts[0].Effect.A.Index != 1 {
		t.Errorf("or-absent operand = %+v, want a var read of slot 1 (a.elem)", stmts[0].Effect.A)
	}
}

func TestArraySlots_AnIndexReadUnderADominatingLengthGuardIsThePlainElemRead(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := arrayLoweringContext(kernel,
		[]string{"a.len", "a.elem", "i", "x"},
		[]BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber, BindingKindNumber})
	stmts, ok := LowerStatements(context, loweringParse(t, `if (i < a.length) { x = a[i]; }`))
	if !ok {
		t.Fatalf("LowerStatements(guarded index read) ok = false, want true")
	}
	if stmts[0].Kind != kernelbridge.IrStatementBranch {
		t.Fatalf("stmts[0].Kind = %q, want %q", stmts[0].Kind, kernelbridge.IrStatementBranch)
	}
	// the guard is the two-slot comparison against the len slot
	if stmts[0].Test != kernelbridge.IrTestLtSlot {
		t.Errorf("test = %q, want %q", stmts[0].Test, kernelbridge.IrTestLtSlot)
	}
	if stmts[0].On != 2 || stmts[0].OnB != 0 {
		t.Errorf("branch slots = (%d, %d), want (2 = i, 0 = a.len)", stmts[0].On, stmts[0].OnB)
	}
	if len(stmts[0].Then) != 1 {
		t.Fatalf("len(then) = %d, want 1", len(stmts[0].Then))
	}
	read := stmts[0].Then[0].Effect
	if read.Kind != kernelbridge.LoopEffectVar || read.Index != 1 {
		t.Errorf("guarded read = %+v, want a plain var read of slot 1 (a.elem)", read)
	}
}

func TestArraySlots_TheBoundDoesNotEscapeTheArmThatProvedIt(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := arrayLoweringContext(kernel,
		[]string{"a.len", "a.elem", "i", "x"},
		[]BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber, BindingKindNumber})
	stmts, ok := LowerStatements(context, loweringParse(t, `
		if (i < a.length) { x = a[i]; }
		x = a[i];
	`))
	if !ok {
		t.Fatalf("LowerStatements ok = false, want true")
	}
	if len(stmts) != 2 {
		t.Fatalf("len(stmts) = %d, want 2", len(stmts))
	}
	after := stmts[1].Effect
	if after.Kind != kernelbridge.LoopEffectOrAbsent {
		t.Errorf("read after the arm = %q, want %q — the bound was proved inside the arm only", after.Kind, kernelbridge.LoopEffectOrAbsent)
	}
}

func TestArraySlots_AForOfLowersAsTheLoopWhoseElementEffectIsTheElemSlot(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := arrayLoweringContext(kernel,
		[]string{"a.len", "a.elem", "x", "total"},
		[]BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber, BindingKindNumber})
	stmts, ok := LowerStatements(context, loweringParse(t, `for (const x of a) { total = total + x; }`))
	if !ok {
		t.Fatalf("LowerStatements(for-of) ok = false, want true")
	}
	if len(stmts) != 1 || stmts[0].Kind != kernelbridge.IrStatementLoop {
		t.Fatalf("lowered to %d statement(s) of kind %q, want one loop", len(stmts), stmts[0].Kind)
	}
	loop := stmts[0]
	// the element binding takes the elem slot's value each pass
	if loop.Body[2].Kind != kernelbridge.LoopEffectVar || loop.Body[2].Index != 1 {
		t.Errorf("element binding effect = %+v, want a var read of slot 1 (a.elem)", loop.Body[2])
	}
	if !loop.Written[2] || !loop.Written[3] {
		t.Errorf("written = %v, want the element binding and the accumulator both written", loop.Written)
	}
	// no numeric head bounds the trip count
	for index, set := range loop.Cond {
		if set != nil {
			t.Errorf("cond[%d] is set, want nil — a for-of has no numeric head", index)
		}
	}
}

func TestArraySlots_AnAliasedArrayDeclines(t *testing.T) {
	declaration := summaryDeclarationOf(t,
		"function f(n: number) { const a = [1, 2]; const b = a; return b.length; }")
	body := declaration.Body()
	locals, ok := CollectLocals(body)
	if !ok {
		t.Fatalf("CollectLocals ok = false, want the array local collected")
	}
	if flattened := ArrayLocalsOf(body, locals.Locals, nil); len(flattened) != 0 {
		t.Errorf("an aliased array flattened — the alias reads a value the two slots do not build")
	}
}

func TestArraySlots_AnUnlistedMethodDeclinesTheArray(t *testing.T) {
	declaration := summaryDeclarationOf(t,
		"function f(n: number) { const a = [1, 2]; a.pop(); return a.length; }")
	body := declaration.Body()
	locals, ok := CollectLocals(body)
	if !ok {
		t.Fatalf("CollectLocals ok = false, want the array local collected")
	}
	if flattened := ArrayLocalsOf(body, locals.Locals, nil); len(flattened) != 0 {
		t.Errorf("an array with a pop() flattened — the two slots do not carry a shrink")
	}
}

func TestArraySlots_TheRecognizerAdmitsTheListedFormsAndNamesBothSlots(t *testing.T) {
	declaration := summaryDeclarationOf(t,
		"function f(i: number) { const a = [1, 2]; a.push(3); a[i] = 4; return a.length + a[i]; }")
	body := declaration.Body()
	locals, ok := CollectLocals(body)
	if !ok {
		t.Fatalf("CollectLocals ok = false, want the array local collected")
	}
	flattened := ArrayLocalsOf(body, locals.Locals, nil)
	if len(flattened) != 1 {
		t.Fatalf("ArrayLocalsOf found %d flattened arrays, want 1", len(flattened))
	}
	for _, local := range flattened {
		if local.LenSlotName != "a.len" || local.ElemSlotName != "a.elem" {
			t.Errorf("slot names = %q, %q, want a.len, a.elem", local.LenSlotName, local.ElemSlotName)
		}
		if got := ArrayElementSort(local); got != BindingKindNumber {
			t.Errorf("element sort = %q, want number", got)
		}
		if got := ArrayElementTypeof(local); got != TypeofTagNumber {
			t.Errorf("element typeof = %q, want number", got)
		}
	}
}

func TestArraySlots_ASpreadInTheArrayLiteralDeclines(t *testing.T) {
	declaration := summaryDeclarationOf(t,
		"function f(n: number) { const a = [1, ...[2, 3]]; return a.length; }")
	body := declaration.Body()
	locals, ok := CollectLocals(body)
	if !ok {
		t.Fatalf("CollectLocals ok = false, want the array local collected")
	}
	if flattened := ArrayLocalsOf(body, locals.Locals, nil); len(flattened) != 0 {
		t.Errorf("a spread array literal flattened — its count is not the literal's own rows")
	}
}

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
	// the has() takes the collection down, so there are no source slots
	// for the bridge to read
	collections, arrays := bridgeLocalsOfSource(t,
		"function f(k: number) { const m = new Map(); m.set(k, 1); if (m.has(k)) { return 0; } const a = [...m.values()]; return a.length; }")
	if len(collections) != 0 {
		t.Fatalf("the has() left the collection flattened, want it declined")
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
	if stmts[0].Target != 3 || stmts[0].Effect.Kind != kernelbridge.LoopEffectVar || stmts[0].Effect.Index != 0 {
		t.Errorf("len write = target %d, effect %+v, want target 3 (a.len) from a var of slot 0 (m.size)",
			stmts[0].Target, stmts[0].Effect)
	}
	if stmts[1].Target != 4 || stmts[1].Effect.Kind != kernelbridge.LoopEffectVar || stmts[1].Effect.Index != 1 {
		t.Errorf("elem write = target %d, effect %+v, want target 4 (a.elem) from a var of slot 1 (m.vals)",
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
	// n = a.length — the len slot's var
	if stmts[2].Effect.Kind != kernelbridge.LoopEffectVar || stmts[2].Effect.Index != 3 {
		t.Errorf("length read = %+v, want a var read of slot 3 (a.len)", stmts[2].Effect)
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
	if len(guard.Then) != 1 || guard.Then[0].Effect.Kind != kernelbridge.LoopEffectVar ||
		guard.Then[0].Effect.Index != 4 {
		t.Errorf("guarded read = %+v, want a plain var read of slot 4 (a.elem)", guard.Then)
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
