// Companion tests for three small pre-specified gaps, each named by a
// prior unit and each fixed additively in the file it names:
//
//  1. MapDeleteAssignmentsOf / MapClearAssignmentsOf (ir_map_mutation_
//     assignments.go) each held their own bare ast.IsIdentifier(receiver)
//     pre-gate ahead of the already cast-peeled collectionMethodCallOf,
//     blocking a cast-wrapped receiver before that peel was ever reached.
//     lowered_map_cast_peel_test.go already pins `set` and `getOrInsert`
//     behind a cast (getOrInsert needed no fix — its own reader already
//     used Unwrapped); this file adds the two callers the fix actually
//     changes: delete and clear.
//  3. string_method_models.go's functional-replacer recognition block
//     only matched an INLINE arrow/function-expression replacer. A
//     replacer passed by NAME now falls through FunctionInReach the same
//     way promiseHandlerOf resolves a named settlement handler.
//
// (Gap 2 — admitting FormEmptyTuple in statesOnlyLongSequences — lives in
// package abstractdomain, where its own small_named_gaps_test.go sits
// beside lattice_operations.go.)
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// TestMapSlots_ADeleteCallBehindAnUnknownCastStillLowers is gap 1's
// delete half: `(m as unknown as {...}).delete(k)` must lower the same
// as the bare `m.delete(k)` — the cast erases at runtime.
func TestMapSlots_ADeleteCallBehindAnUnknownCastStillLowers(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := mapLoweringContext(kernel,
		[]string{"m.size", "m.vals", "m.keys"},
		[]BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber})
	statements := loweringParse(t, `(m as unknown as { delete(k: number): boolean }).delete(7);`)
	assignments, ok := MapDeleteAssignmentsOf(context, statements[0])
	if !ok {
		t.Fatalf("MapDeleteAssignmentsOf(cast receiver) ok = false, want true — the cast erases at runtime")
	}
	if len(assignments) != 1 {
		t.Fatalf("len(assignments) = %d, want 1 — keys and values are untouched", len(assignments))
	}
	stmts := assignsOf(assignments)
	exit := kernel.Walk([]kernelbridge.KnownStateWire{
		{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{3}))},
		{Top: true},
		{Top: true},
	}, stmts)
	size := loweringSetOf(t, exit[0])
	if !kernel.Member(size, []float64{3}) {
		t.Errorf("member(m.size, [3]) = false, want true — the old reading rides (a delete may miss)")
	}
	if kernel.Member(size, []float64{-1}) {
		t.Errorf("member(m.size, [-1]) = true, want false — a count is never negative")
	}
}

// TestMapSlots_AClearCallBehindAnUnknownCastStillLowers is gap 1's clear
// half: `(m as unknown as {...}).clear()` must reset size to zero and
// vals/keys to absent, the same as the bare `m.clear()`.
func TestMapSlots_AClearCallBehindAnUnknownCastStillLowers(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := mapLoweringContext(kernel,
		[]string{"m.size", "m.vals", "m.keys"},
		[]BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber})
	statements := loweringParse(t, `(m as unknown as { clear(): void }).clear();`)
	assignments, ok := MapClearAssignmentsOf(context, statements[0])
	if !ok {
		t.Fatalf("MapClearAssignmentsOf(cast receiver) ok = false, want true — the cast erases at runtime")
	}
	if len(assignments) != 3 {
		t.Fatalf("len(assignments) = %d, want 3 (size, vals, keys)", len(assignments))
	}
	stmts := assignsOf(assignments)
	exit := kernel.Walk([]kernelbridge.KnownStateWire{
		{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{3}))},
		{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{9}))},
		{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{5}))},
	}, stmts)
	size := loweringSetOf(t, exit[0])
	if !kernel.Member(size, []float64{0}) {
		t.Errorf("member(m.size, [0]) = false, want true — clear empties the collection")
	}
	if kernel.Member(size, []float64{3}) {
		t.Errorf("member(m.size, [3]) = true, want false — the old count does not ride")
	}
	if !exit[1].Absent {
		t.Errorf("m.vals.Absent = false, want true — a cleared Map has no value to read")
	}
	if !exit[2].Absent {
		t.Errorf("m.keys.Absent = false, want true — a cleared Map has no key to read")
	}
}

// TestStringMethodModels_ANamedFunctionReplacerAnswersTheExactResult is
// gap 3: a replacer passed as a NAME, with its declaration in reach,
// must compute the exact assembled result the inline-arrow row already
// gives — not decline to the sort-only row.
func TestStringMethodModels_ANamedFunctionReplacerAnswersTheExactResult(t *testing.T) {
	p := keyedSlotProgram(t, `
function swap(matched: string): string {
	return "Y";
}
function f() {
	const word = "abXcd";
	const out = word.replace("X", swap);
	return out;
}
`)
	env := keyedSlotBodyEnv(t, p, "f")
	if got := keyedSlotFormatted(t, env, "out"); got != `"abYcd"` {
		t.Errorf(`word.replace("X", swap) = %s, want "abYcd" — a named replacer must resolve through FunctionInReach`, got)
	}
}

// TestStringMethodModels_ANamedReplacerWithNoReachableDeclarationStillDeclines
// pins the boundary the fix must not erase: a replacer name with no
// function declaration in reach (an imported, untracked name) keeps
// falling to the sort-only row rather than fabricating an exact answer.
func TestStringMethodModels_ANamedReplacerWithNoReachableDeclarationStillDeclines(t *testing.T) {
	p := keyedSlotProgram(t, `
function f(swap: (matched: string) => string) {
	const word = "abXcd";
	const out = word.replace("X", swap);
	return out;
}
`)
	env := keyedSlotBodyEnv(t, p, "f")
	held, tracked := env.Get("out")
	if !tracked {
		t.Fatalf("no tracked value for out")
	}
	// the sort-level row is a SET (the strings ground) with no exact
	// spelling to format — the assertion reads the value directly so
	// the honest decline is not failed for being unformattable
	if held.Kind == abstractdomain.KindValues {
		t.Errorf("word.replace(\"X\", swap) answered the exact %v, want the sort-level row — a parameter has no reachable body", held.Values)
	}
}
