// The scalar/string world's newly lowered shapes, walked end to end
// through the kernel: absent-carrying constants (`x = null`), string
// concatenation, template literals, the join-only two-slot string
// equality guard, and do-while. Skipped (never a faked pass) when the
// native kernel dylib is absent, the same gate every other walk test
// here uses.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

func TestIrSequenceLowering_AssigningNullWritesTheAbsentStateConstantUnderTheNumberSort(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	stmts, ok := LowerStatements(&LoweringContext{
		Bindings: []string{"x"},
		Sorts:    []BindingKind{BindingKindNumber},
		Narrow:   kernel.Narrow,
	}, loweringParse(t, `x = null;`))
	if !ok {
		t.Fatalf("LowerStatements(x = null) ok = false, want true")
	}
	if len(stmts) != 1 {
		t.Fatalf("len(stmts) = %d, want 1", len(stmts))
	}
	if stmts[0].Effect.Kind != kernelbridge.LoopEffectConstState {
		t.Fatalf("effect kind = %q, want %q", stmts[0].Effect.Kind, kernelbridge.LoopEffectConstState)
	}
	// the null LITERAL carries exactly the null admission — a later
	// `=== undefined` on x is decidably false
	if stmts[0].Effect.Null == false || stmts[0].Effect.Undef {
		t.Errorf("effect.Undef=%v effect.Null=%v, want the null-only constant", stmts[0].Effect.Undef, stmts[0].Effect.Null)
	}
	exit := kernel.Walk(
		[]kernelbridge.KnownStateWire{
			{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{5}))},
		},
		stmts,
	)
	x := exit[0]
	if x.Top {
		t.Fatalf("x.Top = true, want false")
	}
	if !x.Null || x.Undef {
		t.Errorf("x admissions (undef=%v null=%v), want null alone", x.Undef, x.Null)
	}
	if kernel.Member(loweringSetOf(t, x), []float64{5}) {
		t.Errorf("member(x, [5]) = true, want false — the write replaced the entry value")
	}
}

func TestIrSequenceLowering_AssigningUndefinedWritesTheSameAbsentConstantUnderTheStringSort(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	stmts, ok := LowerStatements(&LoweringContext{
		Bindings: []string{"s"},
		Sorts:    []BindingKind{BindingKindString},
		Narrow:   kernel.Narrow,
	}, loweringParse(t, `s = undefined;`))
	if !ok {
		t.Fatalf("LowerStatements(s = undefined) ok = false, want true")
	}
	exit := kernel.Walk(
		[]kernelbridge.KnownStateWire{
			{Set: refinementsets.StringTuple("hi")},
		},
		stmts,
	)
	s := exit[0]
	if s.Top {
		t.Fatalf("s.Top = true, want false")
	}
	if !s.Undef || s.Null {
		t.Errorf("s admissions (undef=%v null=%v), want undefined alone", s.Undef, s.Null)
	}
}

func TestIrSequenceLowering_AReturnOfNullLowersRatherThanDecliningTheWholeBody(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	_, ok := LowerStatements(&LoweringContext{
		Bindings: []string{"x", "#done", "#ret"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber},
		Narrow:   kernel.Narrow,
		Result:   &LoweringResult{Done: 1, Ret: 2},
	}, loweringParse(t, `return null;`))
	if !ok {
		t.Errorf("LowerStatements(return null) ok = false, want true")
	}
}

func TestIrSequenceLowering_ConcatenatingTwoStringSlotsBuildsTheConcatenationOfTheirSets(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	stmts, ok := LowerStatements(&LoweringContext{
		Bindings: []string{"a", "b", "c"},
		Sorts:    []BindingKind{BindingKindString, BindingKindString, BindingKindString},
		Narrow:   kernel.Narrow,
	}, loweringParse(t, `c = a + b;`))
	if !ok {
		t.Fatalf("LowerStatements(c = a + b) ok = false, want true")
	}
	if stmts[0].Effect.Kind != kernelbridge.LoopEffectConcat {
		t.Fatalf("effect kind = %q, want %q", stmts[0].Effect.Kind, kernelbridge.LoopEffectConcat)
	}
	exit := kernel.Walk(
		[]kernelbridge.KnownStateWire{
			{Set: refinementsets.StringTuple("ab")},
			{Set: refinementsets.StringTuple("cd")},
			{Top: true},
		},
		stmts,
	)
	c := exit[2]
	if c.Top {
		t.Fatalf("c.Top = true, want false")
	}
	set := loweringSetOf(t, c)
	if !kernel.Member(set, loweringCodePoints("abcd")) {
		t.Errorf(`member(c, "abcd") = false, want true`)
	}
	if kernel.Member(set, loweringCodePoints("ab")) {
		t.Errorf(`member(c, "ab") = true, want false`)
	}
	if kernel.Member(set, loweringCodePoints("cdab")) {
		t.Errorf(`member(c, "cdab") = true, want false`)
	}
}

func TestIrSequenceLowering_ConcatenationOnNumberSortedOperandsStaysArithmeticAndDoesNotConcatenate(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	stmts, ok := LowerStatements(&LoweringContext{
		Bindings: []string{"x", "y", "z"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber},
		Narrow:   kernel.Narrow,
	}, loweringParse(t, `z = x + y;`))
	if !ok {
		t.Fatalf("LowerStatements(z = x + y) ok = false, want true")
	}
	if stmts[0].Effect.Kind != kernelbridge.LoopEffectBinary {
		t.Errorf("effect kind = %q, want %q — numbers still add", stmts[0].Effect.Kind, kernelbridge.LoopEffectBinary)
	}
}

func TestIrSequenceLowering_ATemplateLiteralLowersAsAConcatChainOverItsLiteralChunksAndSlotReads(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	stmts, ok := LowerStatements(&LoweringContext{
		Bindings: []string{"x", "out"},
		Sorts:    []BindingKind{BindingKindString, BindingKindString},
		Narrow:   kernel.Narrow,
	}, loweringParse(t, "out = `a${x}b`;"))
	if !ok {
		t.Fatalf("LowerStatements(template) ok = false, want true")
	}
	if stmts[0].Effect.Kind != kernelbridge.LoopEffectConcat {
		t.Fatalf("effect kind = %q, want %q", stmts[0].Effect.Kind, kernelbridge.LoopEffectConcat)
	}
	exit := kernel.Walk(
		[]kernelbridge.KnownStateWire{
			{Set: refinementsets.StringTuple("Z")},
			{Top: true},
		},
		stmts,
	)
	out := exit[1]
	if out.Top {
		t.Fatalf("out.Top = true, want false")
	}
	set := loweringSetOf(t, out)
	if !kernel.Member(set, loweringCodePoints("aZb")) {
		t.Errorf(`member(out, "aZb") = false, want true`)
	}
	if kernel.Member(set, loweringCodePoints("ab")) {
		t.Errorf(`member(out, "ab") = true, want false`)
	}
}

func TestIrSequenceLowering_ATemplateWithANumberSortedSubstitutionLowersAsAConcatenation(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	// a number-sorted substitution contributes the sort-only string root
	// (ToString of a number is total and always a String), so the
	// template lowers as the concatenation instead of taking the havoc
	// floor — the target is a string built around the spans, not unknown
	stmts, ok := LowerStatements(&LoweringContext{
		Bindings: []string{"n", "out"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindString},
		Narrow:   kernel.Narrow,
	}, loweringParse(t, "out = `a${n}b`;"))
	if !ok {
		t.Fatalf("a number substitution declined outright, want the concatenation lowering")
	}
	if len(stmts) != 1 {
		t.Fatalf("len(stmts) = %d, want the one concatenation assign: %+v", len(stmts), stmts)
	}
	if stmts[0].Kind != kernelbridge.IrStatementAssign || stmts[0].Target != 1 ||
		stmts[0].Effect.Kind != kernelbridge.LoopEffectConcat {
		t.Errorf("stmts[0] = %+v, want `out := concat` — the spans ride, the number span widens to the string root", stmts[0])
	}
}

func TestIrSequenceLowering_TwoStringSlotsComparedForEqualityLowerAsTheJoinOnlyTwoSlotGuard(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	stmts, ok := LowerStatements(&LoweringContext{
		Bindings: []string{"s", "t", "r"},
		Sorts:    []BindingKind{BindingKindString, BindingKindString, BindingKindString},
		Narrow:   kernel.Narrow,
	}, loweringParse(t, `if (s === t) { r = "same"; } else { r = "diff"; }`))
	if !ok {
		t.Fatalf("LowerStatements(s === t) ok = false, want true")
	}
	if stmts[0].Test != kernelbridge.IrTestEqSeqSlot {
		t.Fatalf("test = %q, want %q", stmts[0].Test, kernelbridge.IrTestEqSeqSlot)
	}
	exit := kernel.Walk(
		[]kernelbridge.KnownStateWire{
			{Set: refinementsets.StringTuple("a")},
			{Set: refinementsets.StringTuple("b")},
			{Top: true},
		},
		stmts,
	)
	r := exit[2]
	if r.Top {
		t.Fatalf("r.Top = true, want false")
	}
	// both arms are walked and joined — the guard narrows neither side,
	// so both writes survive
	set := loweringSetOf(t, r)
	if !kernel.Member(set, loweringCodePoints("same")) {
		t.Errorf(`member(r, "same") = false, want true`)
	}
	if !kernel.Member(set, loweringCodePoints("diff")) {
		t.Errorf(`member(r, "diff") = false, want true`)
	}
	if kernel.Member(set, loweringCodePoints("other")) {
		t.Errorf(`member(r, "other") = true, want false`)
	}
}

func TestIrSequenceLowering_ADoWhileRunsItsBodyOnceThenTheOrdinaryLoop(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	stmts, ok := LowerStatements(&LoweringContext{
		Bindings: []string{"i"},
		Sorts:    []BindingKind{BindingKindNumber},
		Narrow:   kernel.Narrow,
	}, loweringParse(t, `do { i = i + 1; } while (i < 8);`))
	if !ok {
		t.Fatalf("LowerStatements(do-while) ok = false, want true")
	}
	// the once-through assignment, then the loop
	if len(stmts) != 2 {
		t.Fatalf("len(stmts) = %d, want 2", len(stmts))
	}
	if stmts[0].Kind != kernelbridge.IrStatementAssign {
		t.Errorf("stmts[0].Kind = %q, want %q", stmts[0].Kind, kernelbridge.IrStatementAssign)
	}
	if stmts[1].Kind != kernelbridge.IrStatementLoop {
		t.Errorf("stmts[1].Kind = %q, want %q", stmts[1].Kind, kernelbridge.IrStatementLoop)
	}
	exit := kernel.Walk(
		[]kernelbridge.KnownStateWire{
			{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{0}))},
		},
		stmts,
	)
	i := exit[0]
	if i.Top {
		t.Fatalf("i.Top = true, want false")
	}
	set := loweringSetOf(t, i)
	if !kernel.Member(set, []float64{8}) {
		t.Errorf("member(i, [8]) = false, want true")
	}
	if kernel.Member(set, []float64{0}) {
		t.Errorf("member(i, [0]) = true, want false — the body ran at least once")
	}
}
