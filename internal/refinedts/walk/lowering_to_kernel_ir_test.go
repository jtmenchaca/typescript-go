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

/* ── the opaque return ───────────────────────────────────────────── */

// The three conversions below are the wave's coverage work: a return
// whose VALUE no reading lowered, a throw that leaves the body, and
// control that cannot leave the statement being stood in for. Each
// keeps the body's route where it used to cost the body its lowering.

// loweringResultContext is a FUNCTION BODY context: two slots for the
// body's own names and the (done, ret) pair the return routes write.
func loweringResultContext(names []string, sorts []BindingKind) *LoweringContext {
	bindings := append(append([]string{}, names...), "#done", "#ret")
	kinds := append(append([]BindingKind{}, sorts...), BindingKindNumber, BindingKindUnknown)
	tags := make([]TypeofTag, len(bindings))
	for index := range tags {
		tags[index] = TypeofTagNumber
	}
	tags[len(tags)-1] = TypeofTagNone
	return &LoweringContext{
		Bindings: bindings,
		Sorts:    kinds,
		Typeofs:  tags,
		Result:   &LoweringResult{Done: len(names), Ret: len(names) + 1},
	}
}

func TestLoweringToKernelIR_AReturnNoReadingLoweredKeepsItsControlAndLosesOnlyItsValue(t *testing.T) {
	// every route for the VALUE declined — no effect, no await form, no
	// inlinable callee, no guard shape. The control flow is still exact:
	// `#ret := unknown` then the done raise, the readable return's own
	// two statements with the value part standing in.
	// A write-and-call-free literal return is READ (no havoc); a literal
	// whose construction RUNS CODE still havocs.
	context := loweringResultContext([]string{"x"}, []BindingKind{BindingKindNumber})
	stmts, ok := LowerStatements(context, loweringParse(t, "return { lo: f() };"))
	if !ok {
		t.Fatalf("an opaque return declined — its control flow is exact")
	}
	if len(stmts) != 2 {
		t.Fatalf("len(stmts) = %d, want 2 — the ret write and the raise: %+v", len(stmts), stmts)
	}
	if stmts[0].Kind != kernelbridge.IrStatementAssign || stmts[0].Target != context.Result.Ret ||
		stmts[0].Effect.Kind != kernelbridge.LoopEffectUnknown {
		t.Errorf("stmts[0] = %+v, want `assign #ret unknown` — never absent, which would claim undefined", stmts[0])
	}
	if stmts[1].Kind != kernelbridge.IrStatementAssign || stmts[1].Target != context.Result.Done {
		t.Errorf("stmts[1] = %+v, want the done raise", stmts[1])
	}
	if !RaisesDone(stmts, context.Result.Done) {
		t.Errorf("RaisesDone = false, want true — the block ends at this return")
	}
	if context.FirstHavoc != "return (object literal)" {
		t.Errorf("FirstHavoc = %q, want %q", context.FirstHavoc, "return (object literal)")
	}
}

func TestLoweringToKernelIR_AnOpaqueReturnNamesTheReturnedExpressionsOwnSyntax(t *testing.T) {
	cases := []struct {
		source string
		want   string
	}{
		{"return { lo: f() };", "return (object literal)"},
		{"return this.x.y(q);", "return (call this.x.y)"},
		{"return [f()];", "return (array literal)"},
		{"return new Thing();", "return (new)"},
		{"return tag`raw`;", "return (tagged template)"},
	}
	for _, held := range cases {
		context := loweringResultContext([]string{"x"}, []BindingKind{BindingKindNumber})
		if _, ok := LowerStatements(context, loweringParse(t, held.source)); !ok {
			t.Errorf("%q declined", held.source)
			continue
		}
		if context.FirstHavoc != held.want {
			t.Errorf("%q named %q, want %q", held.source, context.FirstHavoc, held.want)
		}
	}
}

func TestLoweringToKernelIR_StatementsAfterAnOpaqueReturningArmStillGateOnTheDoneFlag(t *testing.T) {
	// the continuation discipline is the readable return's, unchanged: an
	// arm that may have returned puts the block's remainder under the
	// flag's falsity. The opaque return raises the same flag, so it reads
	// exactly the same way.
	context := loweringResultContext([]string{"x"}, []BindingKind{BindingKindNumber})
	stmts, ok := LowerStatements(context, loweringParse(t, `
		if (x === 0) { return { lo: 1 }; }
		x = 2;
	`))
	if !ok {
		t.Fatalf("the body declined — the opaque return keeps its route")
	}
	if len(stmts) != 2 {
		t.Fatalf("len(stmts) = %d, want 2 — the branch and the gated remainder: %+v", len(stmts), stmts)
	}
	if stmts[0].Kind != kernelbridge.IrStatementBranch {
		t.Errorf("stmts[0].Kind = %v, want a branch", stmts[0].Kind)
	}
	gate := stmts[1]
	if gate.Kind != kernelbridge.IrStatementBranch || gate.On != context.Result.Done ||
		gate.Test != kernelbridge.IrTestTruthyNum {
		t.Fatalf("stmts[1] = %+v, want the remainder gated on the done flag", gate)
	}
	if len(gate.Then) != 0 {
		t.Errorf("the flag-up arm runs %d statements, want none — the body returned", len(gate.Then))
	}
	if len(gate.Else) != 1 || gate.Else[0].Target != 0 {
		t.Errorf("the flag-down arm = %+v, want the one later assignment", gate.Else)
	}
}

/* ── the escaping throw ──────────────────────────────────────────── */

func TestLoweringToKernelIR_AThrowOutsideAnyTryReturnsNothingAndEndsTheBlock(t *testing.T) {
	// a run that threw returns NOTHING, so no claim about the returned
	// outcome can be wrong about it: `#ret := absent` then the raise.
	context := loweringResultContext([]string{"x"}, []BindingKind{BindingKindNumber})
	stmts, ok := LowerStatements(context, loweringParse(t, `throw new Error("nope");`))
	if !ok {
		t.Fatalf("a throw outside any try declined — it leaves the body outright")
	}
	// the thrown expression's mentions havoc first (none here), then the
	// absent ret and the raise — and the statement is READ whole: the
	// mention havoc covers the constructor's effects, so no porous mark
	if len(stmts) < 2 {
		t.Fatalf("len(stmts) = %d, want at least the absent ret and the raise: %+v", len(stmts), stmts)
	}
	retAssign := stmts[len(stmts)-2]
	raise := stmts[len(stmts)-1]
	if retAssign.Target != context.Result.Ret ||
		retAssign.Effect.Kind != kernelbridge.LoopEffectConstState || !retAssign.Effect.Absent {
		t.Errorf("stmts[-2] = %+v, want `assign #ret absent`", retAssign)
	}
	if raise.Target != context.Result.Done {
		t.Errorf("stmts[-1] = %+v, want the done raise", raise)
	}
	if context.FirstHavoc != "" {
		t.Errorf("FirstHavoc = %q, want none — the throw's mentions are enumerable, so the statement is read", context.FirstHavoc)
	}
}

func TestLoweringToKernelIR_AThrowInsideATryStillDeclinesAndNamesTheConstruct(t *testing.T) {
	// raising the done flag would make the catch's own writes invisible to
	// the walk — a WRONG claim, not a weak one. The decline stands, and
	// the report names the construct rather than a category.
	context := loweringResultContext([]string{"x"}, []BindingKind{BindingKindNumber})
	source := loweringParse(t, `try { throw e; } catch (e) { x = 1; }`)
	tryBody := source[0].AsTryStatement().TryBlock.AsBlock().Statements.Nodes
	if _, ok := LowerStatements(context, tryBody); ok {
		t.Errorf("a throw inside a try lowered — the catch's writes would go unseen")
	}
	if named := DeclinedConstructOf(context); named != "throw inside try" {
		t.Errorf("the decline named %q, want %q", named, "throw inside try")
	}
}

/* ── contained control no longer costs the body ──────────────────── */

func TestLoweringToKernelIR_ASwitchTheChainDeclinesHavocsInsteadOfDecliningTheBody(t *testing.T) {
	// the switch's discriminant is untracked, so the equality chain
	// declines. Its bare breaks cannot LEAVE the switch, so the floor
	// admits it: the whole switch havocs what it could have written and
	// the body keeps its route.
	context := &LoweringContext{
		Bindings: []string{"x", "keep"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber},
		Typeofs:  []TypeofTag{TypeofTagNumber, TypeofTagNumber},
	}
	stmts, ok := LowerStatements(context, loweringParse(t, `
		keep = 3;
		switch (free) { case 1: x = 1; break; default: x = 2; }
	`))
	if !ok {
		t.Fatalf("a switch with a contained break declined the body — the break cannot leave it")
	}
	if len(stmts) != 2 {
		t.Fatalf("len(stmts) = %d, want 2 — the readable write and the switch's one havoc: %+v", len(stmts), stmts)
	}
	if stmts[1].Kind != kernelbridge.IrStatementAssign || stmts[1].Target != 0 ||
		stmts[1].Effect.Kind != kernelbridge.LoopEffectUnknown {
		t.Errorf("stmts[1] = %+v, want `assign 0 unknown` — x alone; keep is untouched", stmts[1])
	}
	if context.FirstHavoc != "switch" {
		t.Errorf("FirstHavoc = %q, want %q", context.FirstHavoc, "switch")
	}
}

func TestLoweringToKernelIR_ALoopCarryingABareBreakHavocsInsteadOfDecliningTheBody(t *testing.T) {
	context := &LoweringContext{
		Bindings: []string{"x"},
		Sorts:    []BindingKind{BindingKindNumber},
		Typeofs:  []TypeofTag{TypeofTagNumber},
	}
	// a for-in has no loop reading at all; its body's break stays inside
	stmts, ok := LowerStatements(context, loweringParse(t, `
		for (const k in o) { x = 1; break; }
	`))
	if !ok {
		t.Fatalf("a for-in carrying a bare break declined — the break cannot leave it")
	}
	if len(stmts) != 1 || stmts[0].Target != 0 ||
		stmts[0].Effect.Kind != kernelbridge.LoopEffectUnknown {
		t.Errorf("stmts = %+v, want one `assign 0 unknown`", stmts)
	}
	if context.FirstHavoc != "for-in" {
		t.Errorf("FirstHavoc = %q, want %q", context.FirstHavoc, "for-in")
	}
}

func TestLoweringToKernelIR_ABreakThatCrossesOutOfTheHavockedStatementStillDeclines(t *testing.T) {
	context := &LoweringContext{
		Bindings: []string{"x"},
		Sorts:    []BindingKind{BindingKindNumber},
		Typeofs:  []TypeofTag{TypeofTagNumber},
	}
	// the labelled break leaves the WHILE the floor would stand in for:
	// the transfer goes to a label outside it, so the havoc's writes
	// would sit on a path control had already left
	source := loweringParse(t, `outer: while (c) { for (const k in o) { break outer; } }`)
	inner := source[0].AsLabeledStatement().Statement.
		AsWhileStatement().Statement.AsBlock().Statements.Nodes
	if _, ok := LowerStatements(context, inner); ok {
		t.Errorf("a labelled break crossing out lowered — the transfer leaves the statement")
	}
	if named := DeclinedConstructOf(context); named != "labeled break crossing out" {
		t.Errorf("the decline named %q, want %q", named, "labeled break crossing out")
	}
}

func TestLoweringToKernelIR_ABodyThatLoweredOwesNoDeclineNameEvenWhereAnArmDeclined(t *testing.T) {
	// the switch's arm carries a return with no result slot, so the arm's
	// own nested lowering declines — and the havoc floor then stands in
	// for the whole switch. The BODY lowered, so it owes no decline name;
	// only the outermost run's answer decides that.
	context := &LoweringContext{
		Bindings: []string{"x"},
		Sorts:    []BindingKind{BindingKindNumber},
		Typeofs:  []TypeofTag{TypeofTagNumber},
	}
	if _, ok := LowerStatements(context, loweringParse(t, `
		switch (free) { case 1: x = 1; break; }
	`)); !ok {
		t.Fatalf("the body declined — the switch's contained break havocs")
	}
	if named := DeclinedConstructOf(context); named != "" {
		t.Errorf("a body that lowered left the decline name %q, want none", named)
	}
}

func TestLoweringToKernelIR_TheDeclineNameIsReadOnceAndCleared(t *testing.T) {
	context := &LoweringContext{
		Bindings: []string{"x"},
		Sorts:    []BindingKind{BindingKindNumber},
		Typeofs:  []TypeofTag{TypeofTagNumber},
	}
	if _, ok := LowerStatements(context, loweringParse(t, `with (o) { x = 1; }`)); ok {
		t.Fatalf("a with statement lowered — its written-slot set is not a syntactic question")
	}
	if named := DeclinedConstructOf(context); named != "with statement" {
		t.Fatalf("the decline named %q, want %q", named, "with statement")
	}
	if named := DeclinedConstructOf(context); named != "" {
		t.Errorf("a second read answered %q, want empty — one run, one name", named)
	}
}

func TestLoweringToKernelIR_HoistingIsInertWhereNoCallSubexpressionAppears(t *testing.T) {
	// the hoist stream is threaded through every route of LowerStatements;
	// a body with no call subexpression must lower to exactly what it
	// lowered to before, statement for statement
	kernel := kernelDelegationLoadKernel(t)
	context := &LoweringContext{
		Bindings: []string{"x", "y"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber},
		Narrow:   kernel.Narrow,
	}
	stmts, ok := LowerStatements(context, loweringParse(t, `
		y = x + 1;
		if (x === 0) { y = 100; } else { y = y * 2; }
		while (x < 10) { x = x + 1; }
	`))
	if !ok {
		t.Fatalf("LowerStatements ok = false, want true")
	}
	if len(stmts) != 3 {
		t.Errorf("len(stmts) = %d, want 3 — one assign, one branch, one loop", len(stmts))
	}
	for _, statement := range stmts {
		if statement.Kind == kernelbridge.IrStatementCall {
			t.Errorf("a call statement appeared in a body with no call: %+v", stmts)
		}
	}
	if len(context.Hoisted) != 0 {
		t.Errorf("len(Hoisted) = %d after a call-free body, want 0", len(context.Hoisted))
	}
	// the flag and the statement pointer are restored, so the context is
	// handed back exactly as the caller gave it
	if context.CanHoist || context.HoistStatement != nil {
		t.Errorf("CanHoist = %v, HoistStatement = %v after the walk, want the caller's own values restored",
			context.CanHoist, context.HoistStatement)
	}
}

func TestLoweringToKernelIR_ACallSubexpressionWithNoBlobStillFallsToTheHavocFloor(t *testing.T) {
	// with no registry there is no blob to hoist, so `x = fetch("n") + 1`
	// reads exactly as it did before hoisting existed: the havoc floor,
	// naming x's own slot and nothing else
	kernel := kernelDelegationLoadKernel(t)
	context := &LoweringContext{
		Bindings: []string{"x"},
		Sorts:    []BindingKind{BindingKindNumber},
		Narrow:   kernel.Narrow,
	}
	stmts, ok := LowerStatements(context, loweringParse(t, `x = fetch("nope") + 1;`))
	if !ok {
		t.Fatalf("LowerStatements ok = false, want the havoc floor")
	}
	if len(stmts) != 1 || stmts[0].Kind != kernelbridge.IrStatementAssign ||
		stmts[0].Target != 0 || stmts[0].Effect.Kind != kernelbridge.LoopEffectUnknown {
		t.Errorf("stmts = %+v, want one `assign 0 unknown`", stmts)
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
