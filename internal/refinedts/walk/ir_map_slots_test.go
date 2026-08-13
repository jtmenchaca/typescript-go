// Map and Set locals flattened into scalar slots — the size, the joined
// values, and (for a Map) the joined keys — recognized from real source
// and lowered. The kernel-walking cases are skipped (never a faked pass)
// when the native kernel dylib is absent, the same gate every other walk
// test here uses.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// mapLoweringContext is the slot layout the lowering tests below share:
// whatever slot names the case needs, under the sorts it names.
func mapLoweringContext(kernel *kernelbridge.RefinedTSKernel, bindings []string, sorts []BindingKind) *LoweringContext {
	return &LoweringContext{
		Bindings: bindings,
		Sorts:    sorts,
		Typeofs:  make([]TypeofTag, len(bindings)),
		Narrow:   kernel.Narrow,
	}
}

// mapLocalsOfSource parses a function body and answers the flattened
// collections its locals contribute.
func mapLocalsOfSource(t *testing.T, source string) map[*ast.Node]MapLocal {
	t.Helper()
	declaration := summaryDeclarationOf(t, source)
	body := declaration.Body()
	locals, ok := CollectLocals(body)
	if !ok {
		t.Fatalf("CollectLocals ok = false, want the collection local collected")
	}
	return MapLocalsOf(body, locals.Locals)
}

func TestMapSlots_AnEmptyMapNamesThreeSlots(t *testing.T) {
	flattened := mapLocalsOfSource(t,
		"function f(k: number) { const m = new Map(); m.set(k, 1); return m.size; }")
	if len(flattened) != 1 {
		t.Fatalf("MapLocalsOf found %d flattened collections, want 1", len(flattened))
	}
	for _, local := range flattened {
		if !local.IsMap {
			t.Errorf("IsMap = false, want true")
		}
		if local.SizeSlotName != "m.size" || local.ValsSlotName != "m.vals" || local.KeysSlotName != "m.keys" {
			t.Errorf("slot names = %q, %q, %q, want m.size, m.vals, m.keys",
				local.SizeSlotName, local.ValsSlotName, local.KeysSlotName)
		}
		if got := len(MapLocalSlots(local)); got != 3 {
			t.Errorf("len(MapLocalSlots) = %d, want 3", got)
		}
	}
}

func TestMapSlots_AnEmptySetNamesTwoSlotsAndNoKeys(t *testing.T) {
	flattened := mapLocalsOfSource(t,
		"function f(v: number) { const s = new Set(); s.add(v); return s.size; }")
	if len(flattened) != 1 {
		t.Fatalf("MapLocalsOf found %d flattened collections, want 1", len(flattened))
	}
	for _, local := range flattened {
		if local.IsMap {
			t.Errorf("IsMap = true, want false")
		}
		if local.KeysSlotName != "" {
			t.Errorf("KeysSlotName = %q, want empty — a Set tracks no keys", local.KeysSlotName)
		}
		slots := MapLocalSlots(local)
		if len(slots) != 2 {
			t.Fatalf("len(MapLocalSlots) = %d, want 2", len(slots))
		}
		if slots[0].Name != "s.size" || slots[1].Name != "s.vals" {
			t.Errorf("slot names = %q, %q, want s.size, s.vals", slots[0].Name, slots[1].Name)
		}
	}
}

func TestMapSlots_ASeededMapReadsItsValueSortAndTypeofFromTheRows(t *testing.T) {
	flattened := mapLocalsOfSource(t,
		`function f(k: number) { const m = new Map([["a", 1], ["b", 2]]); m.set(k, 3); return m.size; }`)
	if len(flattened) != 1 {
		t.Fatalf("MapLocalsOf found %d flattened collections, want 1", len(flattened))
	}
	for _, local := range flattened {
		if got := MapValueSort(local); got != BindingKindNumber {
			t.Errorf("value sort = %q, want number", got)
		}
		if got := MapValueTypeof(local); got != TypeofTagNumber {
			t.Errorf("value typeof = %q, want number", got)
		}
		if got := MapKeySort(local); got != BindingKindString {
			t.Errorf("key sort = %q, want string — both seeded keys are word literals", got)
		}
		if got := MapKeyTypeof(local); got != TypeofTagString {
			t.Errorf("key typeof = %q, want string", got)
		}
	}
}

func TestMapSlots_AHasCallDeclinesTheCollection(t *testing.T) {
	flattened := mapLocalsOfSource(t,
		"function f(k: number) { const m = new Map(); m.set(k, 1); if (m.has(k)) { return 1; } return 0; }")
	if len(flattened) != 0 {
		t.Errorf("a collection with a has() flattened — the slots carry no per-key knowledge, so no test on the result reads")
	}
}

func TestMapSlots_AClearDeclinesTheCollection(t *testing.T) {
	flattened := mapLocalsOfSource(t,
		"function f(k: number) { const m = new Map(); m.set(k, 1); m.clear(); return m.size; }")
	if len(flattened) != 0 {
		t.Errorf("a collection with a clear() flattened — the value slot would keep values the collection no longer holds")
	}
}

func TestMapSlots_AForEachDeclinesTheCollection(t *testing.T) {
	// the callback is a NAME, not an arrow — CollectLocals declines a body
	// with a nested function outright, and what is under test here is the
	// forEach itself
	flattened := mapLocalsOfSource(t,
		"function f(k: number) { const m = new Map(); m.set(k, 1); m.forEach(cb); return m.size; }")
	if len(flattened) != 0 {
		t.Errorf("a collection with a forEach() flattened — the callback is not this file's territory")
	}
}

func TestMapSlots_AnAliasedCollectionDeclines(t *testing.T) {
	flattened := mapLocalsOfSource(t,
		"function f(k: number) { const m = new Map(); m.set(k, 1); const other = m; return other.size; }")
	if len(flattened) != 0 {
		t.Errorf("an aliased collection flattened — the alias reads a value the slots do not build")
	}
}

func TestMapSlots_PassingTheCollectionToACalleeDeclines(t *testing.T) {
	flattened := mapLocalsOfSource(t,
		"function f(k: number) { const m = new Map(); m.set(k, 1); g(m); return m.size; }")
	if len(flattened) != 0 {
		t.Errorf("a collection passed as an argument flattened — the callee may do anything to it")
	}
}

func TestMapSlots_AnElementAccessDeclinesTheCollection(t *testing.T) {
	flattened := mapLocalsOfSource(t,
		"function f(k: number) { const m = new Map(); m.set(k, 1); return m[k]; }")
	if len(flattened) != 0 {
		t.Errorf("a collection read by element access flattened — an index read is not a get")
	}
}

func TestMapSlots_ASpreadSeedDeclines(t *testing.T) {
	flattened := mapLocalsOfSource(t,
		"function f(k: number) { const s = new Set([1, ...[2, 3]]); s.add(k); return s.size; }")
	if len(flattened) != 0 {
		t.Errorf("a spread seed flattened — its count is not the literal's own rows")
	}
}

func TestMapSlots_ASeedFromAVariableDeclines(t *testing.T) {
	flattened := mapLocalsOfSource(t,
		"function f(xs: number[]) { const s = new Set(xs); return s.size; }")
	if len(flattened) != 0 {
		t.Errorf("a collection seeded from a variable flattened — its rows name no expressions to join")
	}
}

func TestMapSlots_AWeakMapDeclines(t *testing.T) {
	flattened := mapLocalsOfSource(t,
		"function f(k: number) { const m = new WeakMap(); return 1; }")
	if len(flattened) != 0 {
		t.Errorf("a WeakMap flattened — only Map and Set are recognized")
	}
}

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

func TestMapSlots_ASetCallJoinsBothSizeReadingsAndTheKeyAndValue(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := mapLoweringContext(kernel,
		[]string{"m.size", "m.vals", "m.keys"},
		[]BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber})
	statements := loweringParse(t, `m.set(7, 9);`)
	assignments, ok := MapSetAssignmentsOf(context, statements[0])
	if !ok {
		t.Fatalf("MapSetAssignmentsOf ok = false, want true")
	}
	if len(assignments) != 3 {
		t.Fatalf("len(assignments) = %d, want 3 (size, vals, keys)", len(assignments))
	}
	if assignments[0].Effect.Kind != kernelbridge.LoopEffectJoin {
		t.Errorf("size effect kind = %q, want %q — a set may overwrite, so both readings ride",
			assignments[0].Effect.Kind, kernelbridge.LoopEffectJoin)
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
	values := loweringSetOf(t, exit[1])
	for _, value := range []float64{1, 9} {
		if !kernel.Member(values, []float64{value}) {
			t.Errorf("member(m.vals, [%v]) = false, want true", value)
		}
	}
	keys := loweringSetOf(t, exit[2])
	for _, key := range []float64{5, 7} {
		if !kernel.Member(keys, []float64{key}) {
			t.Errorf("member(m.keys, [%v]) = false, want true", key)
		}
	}
}

func TestMapSlots_AnAddCallWritesTheSizeAndTheValueAndNoKey(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := mapLoweringContext(kernel,
		[]string{"s.size", "s.vals"},
		[]BindingKind{BindingKindNumber, BindingKindNumber})
	statements := loweringParse(t, `s.add(4);`)
	assignments, ok := MapSetAssignmentsOf(context, statements[0])
	if !ok {
		t.Fatalf("MapSetAssignmentsOf(add) ok = false, want true")
	}
	if len(assignments) != 2 {
		t.Fatalf("len(assignments) = %d, want 2 — a Set has no key slot", len(assignments))
	}
	if assignments[1].Effect.Kind != kernelbridge.LoopEffectJoin {
		t.Errorf("value effect kind = %q, want %q — a weak update",
			assignments[1].Effect.Kind, kernelbridge.LoopEffectJoin)
	}
}

func TestMapSlots_AnAddOnAMapAndASetOnASetBothDecline(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	mapContext := mapLoweringContext(kernel,
		[]string{"m.size", "m.vals", "m.keys"},
		[]BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber})
	if _, ok := MapSetAssignmentsOf(mapContext, loweringParse(t, `m.add(1);`)[0]); ok {
		t.Errorf("m.add(1) lowered on a Map — add is a Set operation")
	}
	setContext := mapLoweringContext(kernel,
		[]string{"s.size", "s.vals"},
		[]BindingKind{BindingKindNumber, BindingKindNumber})
	if _, ok := MapSetAssignmentsOf(setContext, loweringParse(t, `s.set(1, 2);`)[0]); ok {
		t.Errorf("s.set(1, 2) lowered on a Set — set is a Map operation")
	}
}

func TestMapSlots_ADeleteLeavesTheNonNegativeIntegersJoinedWithTheOldSize(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := mapLoweringContext(kernel,
		[]string{"m.size", "m.vals", "m.keys"},
		[]BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber})
	statements := loweringParse(t, `m.delete(7);`)
	assignments, ok := MapDeleteAssignmentsOf(context, statements[0])
	if !ok {
		t.Fatalf("MapDeleteAssignmentsOf ok = false, want true")
	}
	if len(assignments) != 1 {
		t.Fatalf("len(assignments) = %d, want 1 — keys and values are untouched", len(assignments))
	}
	if assignments[0].Target != 0 {
		t.Errorf("target = %d, want 0 (m.size)", assignments[0].Target)
	}
	if assignments[0].Effect.Kind != kernelbridge.LoopEffectJoin {
		t.Fatalf("size effect kind = %q, want %q", assignments[0].Effect.Kind, kernelbridge.LoopEffectJoin)
	}
	stmts := assignsOf(assignments)
	exit := kernel.Walk([]kernelbridge.KnownStateWire{
		{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{3}))},
		{Top: true},
		{Top: true},
	}, stmts)
	size := loweringSetOf(t, exit[0])
	// the old reading stays admitted (a delete may miss) and every
	// non-negative integer rides beside it (the decrement is not claimable)
	for _, reading := range []float64{0, 2, 3} {
		if !kernel.Member(size, []float64{reading}) {
			t.Errorf("member(m.size, [%v]) = false, want true", reading)
		}
	}
	if kernel.Member(size, []float64{-1}) {
		t.Errorf("member(m.size, [-1]) = true, want false — a count is never negative")
	}
}

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
