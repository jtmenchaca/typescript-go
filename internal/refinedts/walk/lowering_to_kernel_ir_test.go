// Ports control_flow/lowering_to_kernel_ir.test.ts. The adapter's
// half of the engine, demonstrated on real source text: parse
// TypeScript, lower the statements to the IR, and let the kernel walk
// the whole body — the same proved answers the hand-built probes
// pinned, now reached from syntax. Skipped (never a faked pass) when
// the native kernel dylib is absent, the same gate kernelbridge's own
// round-trip tests use.
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
			{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{7})), Absent: true},
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
	if !x.Absent {
		t.Errorf("x.Absent = false, want true")
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
				Set:    refinementsets.MakeRefinedSet(refinementsets.Union(refinementsets.StringTuple("ok"), refinementsets.StringTuple("no"))),
				Absent: true,
			},
		},
		stmts,
	)
	s := exit[0]
	if s.Top {
		t.Fatalf("s.Top = true, want false")
	}
	if !s.Absent {
		t.Errorf("s.Absent = false, want true")
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

func TestLoweringToKernelIR_UnreadableStatementsDeclineTheWholeLowering(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	_, ok := LowerStatements(&LoweringContext{
		Bindings: []string{"x"},
		Sorts:    []BindingKind{BindingKindNumber},
		Narrow:   kernel.Narrow,
	}, loweringParse(t, `x = fetch("nope");`))
	if ok {
		t.Errorf("LowerStatements(fetch call) ok = true, want false")
	}
	// a value test on an unknown-sorted binding has no reading — the
	// OPAQUE BRANCH now serves it: both arms walk, nothing is claimed
	// about the condition
	stmts, ok := LowerStatements(&LoweringContext{
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
	// a condition that WRITES is not opaque-lowerable — still a decline
	_, ok = LowerStatements(&LoweringContext{
		Bindings: []string{"x"},
		Sorts:    []BindingKind{BindingKindUnknown},
		Narrow:   kernel.Narrow,
	}, loweringParse(t, `if ((x = 1)) { x = 2; }`))
	if ok {
		t.Errorf("LowerStatements(writing test) ok = true, want false")
	}
}

func TestLoweringToKernelIR_TheSharedGrammarSpeaksMinMaxAndTheTernaryJoinInTheIR(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	// shapes the IR lowering used to decline: Math.min, a ternary
	stmts, ok := LowerStatements(&LoweringContext{
		Bindings: []string{"x", "y"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber},
		Narrow:   kernel.Narrow,
	}, loweringParse(t, `
		y = Math.min(x, 10);
		x = x > 5 ? 1 : 2;
	`))
	if !ok {
		t.Fatalf("LowerStatements ok = false, want true")
	}
	exit := kernel.Walk(
		[]kernelbridge.KnownStateWire{
			{Set: refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.AtMost(20))},
			{Top: true},
		},
		stmts,
	)
	y := exit[1]
	if y.Top {
		t.Fatalf("y.Top = true, want false")
	}
	// min caps at 10
	if kernel.Member(loweringSetOf(t, y), []float64{21}) {
		t.Errorf("member(y, [21]) = true, want false")
	}
	x := exit[0]
	if x.Top {
		t.Fatalf("x.Top = true, want false")
	}
	xSet := loweringSetOf(t, x)
	// the ternary joins its arms
	if !kernel.Member(xSet, []float64{1}) {
		t.Errorf("member(x, [1]) = false, want true")
	}
	if !kernel.Member(xSet, []float64{2}) {
		t.Errorf("member(x, [2]) = false, want true")
	}
	if kernel.Member(xSet, []float64{3}) {
		t.Errorf("member(x, [3]) = true, want false")
	}
}
