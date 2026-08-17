// Ports control_flow/lowering_to_kernel_ir.test.ts. The adapter's
// half of the engine, demonstrated on real source text: parse
// TypeScript, lower the statements to the IR, and let the kernel walk
// the whole body — the same proved answers the hand-built probes
// pinned, now reached from syntax. Skipped (never a faked pass) when
// the native kernel dylib is absent, the same gate kernelbridge's own
// round-trip tests use.
//
// The sections that were here are beside this file now:
// lowering_to_kernel_ir_return_test.go (the opaque return, and the
// loweringResultContext helper the throw tests read),
// _throw_test.go (the escaping throw), _floor_test.go (contained
// control and the havoc floor), _hoist_test.go (the hoist stream and
// the shared grammar).
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/parser"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// loweringParse mirrors the TS test's parse: every top-level
// statement of a throwaway source file, no checker involved —
// LowerStatements reads only syntax plus the caller's context.
func loweringParse(t *testing.T, source string) []*ast.Node {
	t.Helper()
	opts := ast.SourceFileParseOptions{FileName: "/body.ts", Path: "/body.ts"}
	file := parser.ParseSourceFile(opts, source, core.ScriptKindTS)
	return file.Statements.Nodes
}

// loweringSetOf mirrors the TS test's setOf: the concrete set of a
// non-top state, or a test failure — expected only where the caller
// already knows the state is concrete.
func loweringSetOf(t *testing.T, s kernelbridge.KnownStateWire) refinementsets.RefinedSet {
	t.Helper()
	if s.Top {
		t.Fatalf("expected a concrete state, got top")
	}
	return s.Set
}

// loweringCodePoints mirrors the TS test's points: a string's Unicode
// code points, as float64 — the codepoint tuple the kernel's string
// sets are built from.
func loweringCodePoints(s string) []float64 {
	out := make([]float64, 0, len(s))
	for _, r := range s {
		out = append(out, float64(r))
	}
	return out
}

func TestLoweringToKernelIR_ParsedSourceLowersToTheIRAndTheKernelWalksItAssignmentBranchAndJoin(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	stmts, ok := LowerStatements(&LoweringContext{
		Bindings: []string{"x", "y"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber},
		Narrow:   kernel.Narrow,
	}, loweringParse(t, `
		y = x + 1;
		if (x === 0) { y = 100; } else { y = y * 2; }
	`))
	if !ok {
		t.Fatalf("LowerStatements ok = false, want true")
	}
	exit := kernel.Walk(
		[]kernelbridge.KnownStateWire{
			{Set: refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.AtMost(10))},
			{Top: true},
		},
		stmts,
	)
	y := exit[1]
	if y.Top {
		t.Fatalf("y.Top = true, want false")
	}
	set := loweringSetOf(t, y)
	if !kernel.Member(set, []float64{100}) {
		t.Errorf("member(y, [100]) = false, want true")
	}
	if !kernel.Member(set, []float64{2}) {
		t.Errorf("member(y, [2]) = false, want true")
	}
	if !kernel.Member(set, []float64{22}) {
		t.Errorf("member(y, [22]) = false, want true")
	}
	if kernel.Member(set, []float64{1}) {
		t.Errorf("member(y, [1]) = true, want false")
	}
	if kernel.Member(set, []float64{23}) {
		t.Errorf("member(y, [23]) = true, want false")
	}
}

func TestLoweringToKernelIR_AParsedWhileLoopLowersThroughTheKernelsNarrowingAndCertifiesThroughTheSolver(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	stmts, ok := LowerStatements(&LoweringContext{
		Bindings: []string{"i", "x"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber},
		Narrow:   kernel.Narrow,
	}, loweringParse(t, `
		while (i < 10) { i = i + 1; }
	`))
	if !ok {
		t.Fatalf("LowerStatements ok = false, want true")
	}
	exit := kernel.Walk(
		[]kernelbridge.KnownStateWire{
			{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{0}))},
			{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{7})), Undef: true, Null: true},
		},
		stmts,
	)
	i := exit[0]
	if i.Top {
		t.Fatalf("i.Top = true, want false")
	}
	iSet := loweringSetOf(t, i)
	if !kernel.Member(iSet, []float64{10}) {
		t.Errorf("member(i, [10]) = false, want true")
	}
	if kernel.Member(iSet, []float64{9}) {
		t.Errorf("member(i, [9]) = true, want false")
	}
	if kernel.Member(iSet, []float64{5}) {
		t.Errorf("member(i, [5]) = true, want false")
	}
	x := exit[1]
	if x.Top {
		t.Fatalf("x.Top = true, want false")
	}
	if !x.Undef || !x.Null {
		t.Errorf("x admissions (undef=%v null=%v), want both", x.Undef, x.Null)
	}
	if !kernel.Member(loweringSetOf(t, x), []float64{7}) {
		t.Errorf("member(x, [7]) = false, want true")
	}
}

func TestLoweringToKernelIR_ASequentialBranchingLoopBodyFoldsIntoTheSolversParallelFormAndCertifies(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	stmts, ok := LowerStatements(&LoweringContext{
		Bindings: []string{"i", "x"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber},
		Narrow:   kernel.Narrow,
	}, loweringParse(t, `
		while (i < 10) {
			if (x === 0) { i = i + 1; } else { i = i + 1; i = i + 1; }
		}
	`))
	if !ok {
		t.Fatalf("LowerStatements ok = false, want true")
	}
	exit := kernel.Walk(
		[]kernelbridge.KnownStateWire{
			{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{0}))},
			{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{0, 1}))},
		},
		stmts,
	)
	i := exit[0]
	if i.Top {
		t.Fatalf("i.Top = true, want false")
	}
	iSet := loweringSetOf(t, i)
	if !kernel.Member(iSet, []float64{10}) {
		t.Errorf("member(i, [10]) = false, want true")
	}
	if kernel.Member(iSet, []float64{9}) {
		t.Errorf("member(i, [9]) = true, want false")
	}
	if kernel.Member(iSet, []float64{0}) {
		t.Errorf("member(i, [0]) = true, want false")
	}
}

func TestLoweringToKernelIR_AComparisonHeadedBranchLowersTruthTakesTheRayFalsityTheDifference(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	stmts, ok := LowerStatements(&LoweringContext{
		Bindings: []string{"x"},
		Sorts:    []BindingKind{BindingKindNumber},
		Narrow:   kernel.Narrow,
	}, loweringParse(t, `if (x < 5) { x = 100; }`))
	if !ok {
		t.Fatalf("LowerStatements ok = false, want true")
	}
	exit := kernel.Walk(
		[]kernelbridge.KnownStateWire{
			{Set: refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.AtMost(10))},
		},
		stmts,
	)
	x := exit[0]
	if x.Top {
		t.Fatalf("x.Top = true, want false")
	}
	set := loweringSetOf(t, x)
	if !kernel.Member(set, []float64{100}) {
		t.Errorf("member(x, [100]) = false, want true")
	}
	if !kernel.Member(set, []float64{7}) {
		t.Errorf("member(x, [7]) = false, want true")
	}
	if !kernel.Member(set, []float64{5}) {
		t.Errorf("member(x, [5]) = false, want true")
	}
	if kernel.Member(set, []float64{3}) {
		t.Errorf("member(x, [3]) = true, want false")
	}
}

func TestLoweringToKernelIR_AStringBindingBranchesOnItsOwnKindTagEqualityPinsTheExactWordTheElseKeepsTheDifferenceAndTheFlags(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	stmts, ok := LowerStatements(&LoweringContext{
		Bindings: []string{"s"},
		Sorts:    []BindingKind{BindingKindString},
		Narrow:   kernel.Narrow,
	}, loweringParse(t, `if (s === "ok") { s = "yes"; }`))
	if !ok {
		t.Fatalf("LowerStatements ok = false, want true")
	}
	// entry: the word "ok" or "no" (their union), possibly absent
	exit := kernel.Walk(
		[]kernelbridge.KnownStateWire{
			{
				Set:   refinementsets.MakeRefinedSet(refinementsets.Union(refinementsets.StringTuple("ok"), refinementsets.StringTuple("no"))),
				Undef: true,
				Null:  true,
			},
		},
		stmts,
	)
	s := exit[0]
	if s.Top {
		t.Fatalf("s.Top = true, want false")
	}
	if !s.Undef || !s.Null {
		t.Errorf("s admissions (undef=%v null=%v), want both", s.Undef, s.Null)
	}
	set := loweringSetOf(t, s)
	if !kernel.Member(set, loweringCodePoints("yes")) {
		t.Errorf(`member(s, "yes") = false, want true`)
	}
	if !kernel.Member(set, loweringCodePoints("no")) {
		t.Errorf(`member(s, "no") = false, want true`)
	}
	if kernel.Member(set, loweringCodePoints("ok")) {
		t.Errorf(`member(s, "ok") = true, want false`)
	}
}

func TestLoweringToKernelIR_AForLoopHeadLowersInitComparisonAndStepCertifyThroughTheSolver(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	stmts, ok := LowerStatements(&LoweringContext{
		Bindings: []string{"i"},
		Sorts:    []BindingKind{BindingKindNumber},
		Narrow:   kernel.Narrow,
	}, loweringParse(t, `for (i = 0; i < 8; i++) { }`))
	if !ok {
		t.Fatalf("LowerStatements ok = false, want true")
	}
	if len(stmts) != 2 {
		t.Fatalf("len(stmts) = %d, want 2", len(stmts))
	}
	exit := kernel.Walk([]kernelbridge.KnownStateWire{{Top: true}}, stmts)
	i := exit[0]
	if i.Top {
		t.Fatalf("i.Top = true, want false")
	}
	set := loweringSetOf(t, i)
	if !kernel.Member(set, []float64{8}) {
		t.Errorf("member(i, [8]) = false, want true")
	}
	if kernel.Member(set, []float64{7}) {
		t.Errorf("member(i, [7]) = true, want false")
	}
	if kernel.Member(set, []float64{0}) {
		t.Errorf("member(i, [0]) = true, want false")
	}
}

// TestLoweringToKernelIR_AngleBracketAndAsAssertionsUnwrapIdentically
// pins that `<T>e` and `e as T` lower the same statements — both erase
// at runtime exactly like a plain read, so a read through either
// spelling reaches the same assignment IR the unwrapped read does
// (Unwrapped, tracked_bindings.go).
func TestLoweringToKernelIR_AngleBracketAndAsAssertionsUnwrapIdentically(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	angleContext := &LoweringContext{
		Bindings: []string{"x", "y"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber},
		Narrow:   kernel.Narrow,
	}
	angleStmts, angleOk := LowerStatements(angleContext, loweringParse(t, `y = <number>x;`))
	if !angleOk {
		t.Fatalf("LowerStatements(<number>x) ok = false, want true")
	}
	asContext := &LoweringContext{
		Bindings: []string{"x", "y"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber},
		Narrow:   kernel.Narrow,
	}
	asStmts, asOk := LowerStatements(asContext, loweringParse(t, `y = x as number;`))
	if !asOk {
		t.Fatalf("LowerStatements(x as number) ok = false, want true")
	}
	if len(angleStmts) != len(asStmts) {
		t.Fatalf("len(angleStmts) = %d, len(asStmts) = %d, want equal", len(angleStmts), len(asStmts))
	}
	for i := range angleStmts {
		if angleStmts[i].Kind != asStmts[i].Kind || angleStmts[i].Target != asStmts[i].Target ||
			angleStmts[i].Effect.Kind != asStmts[i].Effect.Kind || angleStmts[i].Effect.Index != asStmts[i].Effect.Index {
			t.Errorf("stmts[%d] = %+v, want %+v (identical to the `as` spelling)", i, angleStmts[i], asStmts[i])
		}
	}
	// walked, both spellings pass the read through unchanged
	exit := kernel.Walk([]kernelbridge.KnownStateWire{
		{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{5}))},
		{Top: true},
	}, angleStmts)
	y := exit[1]
	if y.Top {
		t.Fatalf("y.Top = true, want false")
	}
	if !kernel.Member(loweringSetOf(t, y), []float64{5}) {
		t.Errorf("member(y, [5]) = false, want true — <number>x reads x through")
	}
}

// TestLoweringToKernelIR_SatisfiesExpressionUnwrapsLikeAsAndAngleBracket
// pins `e satisfies T` beside item 1's two spellings: the same
// erasure argument, the same Unwrapped arm.
func TestLoweringToKernelIR_SatisfiesExpressionUnwrapsLikeAsAndAngleBracket(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	satisfiesContext := &LoweringContext{
		Bindings: []string{"x", "y"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber},
		Narrow:   kernel.Narrow,
	}
	satisfiesStmts, satisfiesOk := LowerStatements(satisfiesContext, loweringParse(t, `y = x satisfies number;`))
	if !satisfiesOk {
		t.Fatalf("LowerStatements(x satisfies number) ok = false, want true")
	}
	asContext := &LoweringContext{
		Bindings: []string{"x", "y"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber},
		Narrow:   kernel.Narrow,
	}
	asStmts, asOk := LowerStatements(asContext, loweringParse(t, `y = x as number;`))
	if !asOk {
		t.Fatalf("LowerStatements(x as number) ok = false, want true")
	}
	if len(satisfiesStmts) != len(asStmts) {
		t.Fatalf("len(satisfiesStmts) = %d, len(asStmts) = %d, want equal", len(satisfiesStmts), len(asStmts))
	}
	for i := range satisfiesStmts {
		if satisfiesStmts[i].Kind != asStmts[i].Kind || satisfiesStmts[i].Target != asStmts[i].Target ||
			satisfiesStmts[i].Effect.Kind != asStmts[i].Effect.Kind || satisfiesStmts[i].Effect.Index != asStmts[i].Effect.Index {
			t.Errorf("stmts[%d] = %+v, want %+v (identical to the `as` spelling)", i, satisfiesStmts[i], asStmts[i])
		}
	}
	exit := kernel.Walk([]kernelbridge.KnownStateWire{
		{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{9}))},
		{Top: true},
	}, satisfiesStmts)
	y := exit[1]
	if y.Top {
		t.Fatalf("y.Top = true, want false")
	}
	if !kernel.Member(loweringSetOf(t, y), []float64{9}) {
		t.Errorf("member(y, [9]) = false, want true — x satisfies number reads x through")
	}
}

func TestLoweringToKernelIR_UnreadableStatementsHavocWhatTheyCouldHaveWrittenAndDeclineOnlyWhereTheSlotSetIsUnknowable(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	// an unresolvable call no longer declines the body: it HAVOCS the
	// slots it could have written — here the assignment's target alone,
	// since the argument is a literal — and the body keeps its route
	context := &LoweringContext{
		Bindings: []string{"x"},
		Sorts:    []BindingKind{BindingKindNumber},
		Narrow:   kernel.Narrow,
	}
	stmts, ok := LowerStatements(context, loweringParse(t, `x = fetch("nope");`))
	if !ok {
		t.Fatalf("LowerStatements(fetch call) ok = false, want the havoc floor")
	}
	if len(stmts) != 1 || stmts[0].Kind != kernelbridge.IrStatementAssign ||
		stmts[0].Target != 0 || stmts[0].Effect.Kind != kernelbridge.LoopEffectUnknown {
		t.Errorf("stmts = %+v, want one `assign 0 unknown`", stmts)
	}
	if context.FirstHavoc != "call fetch" {
		t.Errorf("FirstHavoc = %q, want %q", context.FirstHavoc, "call fetch")
	}
	// a THROW with no result slot pair still declines: there is nothing
	// for it to write or raise. (A function body's throw outside any try
	// lowers as `#ret := absent` then the raise — its own test above.)
	throwContext := &LoweringContext{
		Bindings: []string{"x"},
		Sorts:    []BindingKind{BindingKindNumber},
		Narrow:   kernel.Narrow,
	}
	if _, ok := LowerStatements(throwContext, loweringParse(t, `throw new Error("nope");`)); ok {
		t.Errorf("LowerStatements(throw, no result slots) ok = true, want false")
	}
	if named := DeclinedConstructOf(throwContext); named != "throw with no result slot" {
		t.Errorf("the decline named %q, want %q", named, "throw with no result slot")
	}
	// a value test on an unknown-sorted binding has no reading — the
	// OPAQUE BRANCH now serves it: both arms walk, nothing is claimed
	// about the condition
	stmts, ok = LowerStatements(&LoweringContext{
		Bindings: []string{"x"},
		Sorts:    []BindingKind{BindingKindUnknown},
		Narrow:   kernel.Narrow,
	}, loweringParse(t, `if (x === 5) { x = 1; }`))
	if !ok {
		t.Fatalf("LowerStatements(unknown-sort test) ok = false, want the opaque branch")
	}
	if len(stmts) != 1 || stmts[0].Kind != kernelbridge.IrStatementBranchBoth {
		t.Errorf("stmts = %+v, want one branchBoth", stmts)
	}
	// a condition that WRITES is still not opaque-BRANCH-lowerable — the
	// branch that tests nothing would skip the write. It now falls to the
	// havoc floor instead of declining: the write's target is nameable,
	// so x takes `unknown` and the body keeps its route. What the floor
	// gives up on is the branch structure, which is precision; what it
	// keeps is that no write is silently skipped.
	writingContext := &LoweringContext{
		Bindings: []string{"x"},
		Sorts:    []BindingKind{BindingKindUnknown},
		Narrow:   kernel.Narrow,
	}
	stmts, ok = LowerStatements(writingContext, loweringParse(t, `if ((x = 1)) { x = 2; }`))
	if !ok {
		t.Fatalf("LowerStatements(writing test) ok = false, want the havoc floor")
	}
	if len(stmts) != 1 || stmts[0].Kind != kernelbridge.IrStatementAssign ||
		stmts[0].Target != 0 || stmts[0].Effect.Kind != kernelbridge.LoopEffectUnknown {
		t.Errorf("stmts = %+v, want one `assign 0 unknown` — the write's own target", stmts)
	}
	if writingContext.FirstHavoc != "if" {
		t.Errorf("FirstHavoc = %q, want %q", writingContext.FirstHavoc, "if")
	}
}
