// The string-valued sequence rows: charAt riding the sliceBmp row,
// template substitutions widened to number-sorted slots, and the
// trimLeft/trimRight aliases. Skipped (never a faked pass) when the
// native kernel dylib is absent, the same gate every other walk test
// here uses.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// sequenceLowered lowers `out = <expr>;` over string-sorted bindings and
// hands back the statements.
func sequenceLowered(t *testing.T, kernel *kernelbridge.RefinedTSKernel, bindings []string, sorts []BindingKind, source string) []kernelbridge.IrStatement {
	t.Helper()
	stmts, ok := LowerStatements(&LoweringContext{
		Bindings: bindings,
		Sorts:    sorts,
		Narrow:   kernel.Narrow,
	}, loweringParse(t, source))
	if !ok {
		t.Fatalf("LowerStatements(%q) ok = false, want true", source)
	}
	return stmts
}

func TestEffectCharAt_UnderTheBmpGateRidesTheSliceBmpRow(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	stmts := sequenceLowered(t, kernel, []string{"s", "out"},
		[]BindingKind{BindingKindString, BindingKindString}, `out = s.charAt(0);`)
	if stmts[0].Effect.Kind != kernelbridge.LoopEffectSeqUnary {
		t.Fatalf("effect kind = %q, want %q", stmts[0].Effect.Kind, kernelbridge.LoopEffectSeqUnary)
	}
	if stmts[0].Effect.Op != kernelbridge.LoopOpSliceBmp {
		t.Fatalf("effect op = %q, want %q — charAt rides the same gated row as slice", stmts[0].Effect.Op, kernelbridge.LoopOpSliceBmp)
	}
	// a receiver drawn from {a} with a ceiling of 3, BMP: the drawn-from
	// claim admits any all-"a" word up to that ceiling, including
	// results charAt can never actually produce ("aaa") — sound but not
	// the tight [0,1] claim, exactly as documented. The alphabet must be
	// a single stated scalar (OneOf) rather than a union: the kernel's
	// BMP gate (scalarCapOf, set_functions/walk.lean) reads AtMost/Below/
	// OneOf at the top of the alphabet's forms and refuses a Union there
	// even though every branch is itself BMP — the same shape
	// effect_replace_union_test.go's receiver uses.
	ceiling := 3
	exit := kernel.Walk(
		[]kernelbridge.KnownStateWire{
			{Set: refinementsets.MakeRefinedSet(refinementsets.RepeatOf(
				refinementsets.MakeRefinedSet(refinementsets.OneOf(loweringCodePoints("a"))),
				0, &ceiling))},
			{Top: true},
		},
		stmts,
	)
	out := exit[1]
	if out.Top {
		t.Fatalf("out.Top = true, want false")
	}
	set := loweringSetOf(t, out)
	if !kernel.Member(set, loweringCodePoints("a")) {
		t.Errorf(`member(out, "a") = false, want true — a real charAt(0) result`)
	}
	if !kernel.Member(set, loweringCodePoints("")) {
		t.Errorf(`member(out, "") = false, want true — the empty string is drawn from, floor 0`)
	}
	if kernel.Member(set, loweringCodePoints("b")) {
		t.Errorf(`member(out, "b") = true, want false — "b" is outside the receiver's alphabet`)
	}
}

func TestEffectCharAt_AComputingArgumentDeclines(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	stmts, ok := LowerStatements(&LoweringContext{
		Bindings: []string{"s", "out"},
		Sorts:    []BindingKind{BindingKindString, BindingKindString},
		Narrow:   kernel.Narrow,
	}, loweringParse(t, `out = s.charAt(g());`))
	if !ok {
		t.Fatalf("LowerStatements ok = false, want true (the havoc floor still lowers)")
	}
	sawTarget := false
	for _, stmt := range stmts {
		if stmt.Kind == kernelbridge.IrStatementAssign && stmt.Target == 1 &&
			stmt.Effect.Kind == kernelbridge.LoopEffectUnknown {
			sawTarget = true
		}
	}
	if !sawTarget {
		t.Errorf("out (slot 1) was not havocked by the computing-argument decline")
	}
}

func TestEffectTemplateSubstitution_ANumberSortedSpanContributesTheStringRoot(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	stmts := sequenceLowered(t, kernel, []string{"n", "out"},
		[]BindingKind{BindingKindNumber, BindingKindString}, "out = `a${n}b`;")
	if stmts[0].Effect.Kind != kernelbridge.LoopEffectConcat {
		t.Fatalf("effect kind = %q, want %q — a number-sorted span must widen, not decline", stmts[0].Effect.Kind, kernelbridge.LoopEffectConcat)
	}
	exit := kernel.Walk(
		[]kernelbridge.KnownStateWire{
			{Set: refinementsets.MakeRefinedSet(refinementsets.Integer, refinementsets.AtLeast(0), refinementsets.AtMost(9))},
			{Top: true},
		},
		stmts,
	)
	out := exit[1]
	if out.Top {
		t.Fatalf("out.Top = true, want false — the sort-only widening is a claim, not a refusal")
	}
	set := loweringSetOf(t, out)
	if !kernel.Member(set, loweringCodePoints("a5b")) {
		t.Errorf(`member(out, "a5b") = false, want true`)
	}
	if !kernel.Member(set, loweringCodePoints("a123456789b")) {
		t.Errorf(`member(out, "a123456789b") = false, want true — the middle piece is the unconstrained string root`)
	}
	if kernel.Member(set, loweringCodePoints("xab")) {
		t.Errorf(`member(out, "xab") = true, want false — the literal chunks still bound the ends`)
	}
}

func TestEffectTemplateSubstitution_MixedStringAndNumberSpansBothWiden(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	stmts := sequenceLowered(t, kernel, []string{"s", "n", "out"},
		[]BindingKind{BindingKindString, BindingKindNumber, BindingKindString},
		"out = `${s}-${n}`;")
	if stmts[0].Effect.Kind != kernelbridge.LoopEffectConcat {
		t.Fatalf("effect kind = %q, want %q", stmts[0].Effect.Kind, kernelbridge.LoopEffectConcat)
	}
	exit := kernel.Walk(
		[]kernelbridge.KnownStateWire{
			{Set: refinementsets.StringTuple("id")},
			{Set: refinementsets.MakeRefinedSet(refinementsets.Integer, refinementsets.AtLeast(0), refinementsets.AtMost(9))},
			{Top: true},
		},
		stmts,
	)
	out := exit[2]
	if out.Top {
		t.Fatalf("out.Top = true, want false")
	}
	set := loweringSetOf(t, out)
	if !kernel.Member(set, loweringCodePoints("id-7")) {
		t.Errorf(`member(out, "id-7") = false, want true`)
	}
}

func TestEffectTemplateSubstitution_AnUnreadableSpanStillDeclinesTheWholeTemplate(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	// an assignment expression inside the substitution is a WRITE, which
	// neither the sequence route (no reading for it at all) nor the
	// numeric route's object/array/template literal arm (gated on
	// inertValue, false for an assignment) admits — the template must
	// still decline exactly as it did before this widening existed
	stmts, ok := LowerStatements(&LoweringContext{
		Bindings: []string{"n", "out"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindString},
		Narrow:   kernel.Narrow,
	}, loweringParse(t, "out = `a${(n = 1)}b`;"))
	if !ok {
		t.Fatalf("LowerStatements ok = false, want true (the havoc floor still lowers)")
	}
	sawTarget := false
	for _, stmt := range stmts {
		if stmt.Kind == kernelbridge.IrStatementAssign && stmt.Target == 1 &&
			stmt.Effect.Kind == kernelbridge.LoopEffectUnknown {
			sawTarget = true
		}
	}
	if !sawTarget {
		t.Errorf("out (slot 1) was not havocked by the unreadable-span decline")
	}
}

func TestEffectRepeatElem_ARidesTheDrawnFromRowWithNoCeiling(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	stmts := sequenceLowered(t, kernel, []string{"s", "out"},
		[]BindingKind{BindingKindString, BindingKindString}, `out = s.repeat(3);`)
	if stmts[0].Effect.Kind != kernelbridge.LoopEffectSeqUnary {
		t.Fatalf("effect kind = %q, want %q", stmts[0].Effect.Kind, kernelbridge.LoopEffectSeqUnary)
	}
	if stmts[0].Effect.Op != kernelbridge.LoopOpRepeatElem {
		t.Fatalf("effect op = %q, want %q", stmts[0].Effect.Op, kernelbridge.LoopOpRepeatElem)
	}
	ceiling := 2
	exit := kernel.Walk(
		[]kernelbridge.KnownStateWire{
			{Set: refinementsets.MakeRefinedSet(refinementsets.RepeatOf(
				refinementsets.MakeRefinedSet(refinementsets.OneOf(loweringCodePoints("a"))),
				0, &ceiling))},
			{Top: true},
		},
		stmts,
	)
	out := exit[1]
	if out.Top {
		t.Fatalf("out.Top = true, want false")
	}
	set := loweringSetOf(t, out)
	if !kernel.Member(set, loweringCodePoints("")) {
		t.Errorf(`member(out, "") = false, want true — the drawn-from claim keeps the floor at 0`)
	}
	// the ceiling is DROPPED entirely (LoopOpRepeatElem's own doc): a
	// word far longer than 3 copies of a 2-scalar-ceiling receiver is
	// still admitted, since no length bound rides this row at all
	if !kernel.Member(set, loweringCodePoints("aaaaaaaaaa")) {
		t.Errorf(`member(out, "aaaaaaaaaa") = false, want true — no ceiling is stated`)
	}
	if kernel.Member(set, loweringCodePoints("b")) {
		t.Errorf(`member(out, "b") = true, want false — "b" is outside the receiver's alphabet`)
	}
}

func TestEffectRepeatElem_ANegativeCountLiteralDeclines(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	stmts, ok := LowerStatements(&LoweringContext{
		Bindings: []string{"s", "out"},
		Sorts:    []BindingKind{BindingKindString, BindingKindString},
		Narrow:   kernel.Narrow,
	}, loweringParse(t, `out = s.repeat(-1);`))
	if !ok {
		t.Fatalf("LowerStatements ok = false, want true (the havoc floor still lowers)")
	}
	sawTarget := false
	for _, stmt := range stmts {
		if stmt.Kind == kernelbridge.IrStatementAssign && stmt.Target == 1 &&
			stmt.Effect.Kind == kernelbridge.LoopEffectUnknown {
			sawTarget = true
		}
	}
	if !sawTarget {
		t.Errorf("out (slot 1) was not havocked by the negative-count decline — RangeError makes the drawn-from claim unsound there")
	}
}

func TestEffectPadUnion_OverARepetitionReceiverRidesTheUnionAlphabetRow(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	stmts := sequenceLowered(t, kernel, []string{"s", "out"},
		[]BindingKind{BindingKindString, BindingKindString}, `out = s.padStart(4, "0");`)
	if stmts[0].Effect.Kind != kernelbridge.LoopEffectSeqUnary {
		t.Fatalf("effect kind = %q, want %q", stmts[0].Effect.Kind, kernelbridge.LoopEffectSeqUnary)
	}
	if stmts[0].Effect.Op != kernelbridge.LoopOpPadUnion {
		t.Fatalf("effect op = %q, want %q", stmts[0].Effect.Op, kernelbridge.LoopOpPadUnion)
	}
	three := 3
	exit := kernel.Walk(
		[]kernelbridge.KnownStateWire{
			{Set: refinementsets.Repetition(refinementsets.Digits, 1, &three)},
			{Top: true},
		},
		stmts,
	)
	out := exit[1]
	if out.Top {
		t.Fatalf("out.Top = true, want false")
	}
	set := loweringSetOf(t, out)
	// the union alphabet admits the fill scalar "0" beside the receiver's
	// own Digits — a result built entirely from "0" is admitted, which
	// the receiver's OWN alphabet already covers (Digits includes "0"),
	// so this also confirms the floor survived unchanged
	if !kernel.Member(set, loweringCodePoints("0")) {
		t.Errorf(`member(out, "0") = false, want true — the receiver's floor of 1 survives`)
	}
	if kernel.Member(set, loweringCodePoints("")) {
		t.Errorf(`member(out, "") = true, want false — a pad never shortens below the receiver's own floor`)
	}
	// NO ceiling rides this row — a word far longer than any pad to 4
	// could produce is still admitted, since padUnionForm drops hi
	// entirely
	if !kernel.Member(set, loweringCodePoints("0000000000")) {
		t.Errorf(`member(out, "0000000000") = false, want true — no ceiling is stated`)
	}
	if kernel.Member(set, loweringCodePoints("x")) {
		t.Errorf(`member(out, "x") = true, want false — "x" is outside both the receiver's and the fill's alphabet`)
	}
}

func TestEffectPadUnion_ANonExactFillArgumentDeclines(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	// the fill text must be exactly known — the kernel needs its
	// scalar SET as an operand, and a tracked (non-literal) fill has
	// no set this reader can read syntactically
	stmts, ok := LowerStatements(&LoweringContext{
		Bindings: []string{"s", "pad", "out"},
		Sorts:    []BindingKind{BindingKindString, BindingKindString, BindingKindString},
		Narrow:   kernel.Narrow,
	}, loweringParse(t, `out = s.padStart(4, pad);`))
	if !ok {
		t.Fatalf("LowerStatements ok = false, want true (the havoc floor still lowers)")
	}
	sawTarget := false
	for _, stmt := range stmts {
		if stmt.Kind == kernelbridge.IrStatementAssign && stmt.Target == 2 &&
			stmt.Effect.Kind == kernelbridge.LoopEffectUnknown {
			sawTarget = true
		}
	}
	if !sawTarget {
		t.Errorf("out (slot 2) was not havocked by the non-exact-fill decline")
	}
}

func TestEffectTrimAliases_TrimLeftRidesTheTrimStartRow(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	left := sequenceLowered(t, kernel, []string{"s", "out"},
		[]BindingKind{BindingKindString, BindingKindString}, `out = s.trimLeft();`)
	start := sequenceLowered(t, kernel, []string{"s", "out"},
		[]BindingKind{BindingKindString, BindingKindString}, `out = s.trimStart();`)
	if left[0].Effect.Kind != kernelbridge.LoopEffectSeqUnary || left[0].Effect.Op != kernelbridge.LoopOpTrimStart {
		t.Fatalf("trimLeft effect = %+v, want seqUn/trimStart", left[0].Effect)
	}
	if kernelbridge.EffectWire(left[0].Effect) != kernelbridge.EffectWire(start[0].Effect) {
		t.Errorf("trimLeft wire %q != trimStart wire %q — they must lower identically", kernelbridge.EffectWire(left[0].Effect), kernelbridge.EffectWire(start[0].Effect))
	}
}

func TestEffectTrimAliases_TrimRightRidesTheTrimEndRow(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	right := sequenceLowered(t, kernel, []string{"s", "out"},
		[]BindingKind{BindingKindString, BindingKindString}, `out = s.trimRight();`)
	end := sequenceLowered(t, kernel, []string{"s", "out"},
		[]BindingKind{BindingKindString, BindingKindString}, `out = s.trimEnd();`)
	if right[0].Effect.Kind != kernelbridge.LoopEffectSeqUnary || right[0].Effect.Op != kernelbridge.LoopOpTrimEnd {
		t.Fatalf("trimRight effect = %+v, want seqUn/trimEnd", right[0].Effect)
	}
	if kernelbridge.EffectWire(right[0].Effect) != kernelbridge.EffectWire(end[0].Effect) {
		t.Errorf("trimRight wire %q != trimEnd wire %q — they must lower identically", kernelbridge.EffectWire(right[0].Effect), kernelbridge.EffectWire(end[0].Effect))
	}
}
