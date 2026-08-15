// Array locals flattened into two slots — the length and the join of
// the elements — lowered from real source and walked by the kernel.
// Skipped (never a faked pass) when the native kernel dylib is absent,
// the same gate every other walk test here uses.
package walk

import (
	"testing"

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
		"function f(n: number) { const a = [1, 2]; a.sort(); return a.length; }")
	body := declaration.Body()
	locals, ok := CollectLocals(body)
	if !ok {
		t.Fatalf("CollectLocals ok = false, want the array local collected")
	}
	if flattened := ArrayLocalsOf(body, locals.Locals, nil); len(flattened) != 0 {
		t.Errorf("an array with a sort() flattened — the two slots do not carry a reorder")
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
