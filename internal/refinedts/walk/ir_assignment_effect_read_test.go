// The numeric/boolean string-method rows the numeric reader's Opaque
// hooks: lastIndexOf riding indexOf's wire op, the boolean pair for
// includes/startsWith/endsWith, and .length's exact and sort-only rows.
// Skipped (never a faked pass) when the native kernel dylib is absent,
// the same gate every other walk test here uses.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// numericStringMethodLowered lowers `out = s.<call>;` over a
// string-sorted s and a number-sorted out and hands back the
// statements.
func numericStringMethodLowered(t *testing.T, kernel *kernelbridge.RefinedTSKernel, source string) []kernelbridge.IrStatement {
	t.Helper()
	stmts, ok := LowerStatements(&LoweringContext{
		Bindings: []string{"s", "out"},
		Sorts:    []BindingKind{BindingKindString, BindingKindNumber},
		Narrow:   kernel.Narrow,
	}, loweringParse(t, source))
	if !ok {
		t.Fatalf("LowerStatements(%q) ok = false, want true", source)
	}
	return stmts
}

func TestEffectLastIndexOf_RidesTheSameSeqNumRowAsIndexOf(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	stmts := numericStringMethodLowered(t, kernel, `out = s.lastIndexOf("a");`)
	if stmts[0].Effect.Kind != kernelbridge.LoopEffectSeqNum {
		t.Fatalf("effect kind = %q, want %q", stmts[0].Effect.Kind, kernelbridge.LoopEffectSeqNum)
	}
	if stmts[0].Effect.Op != kernelbridge.LoopOpIndexOf {
		t.Fatalf("effect op = %q, want %q — lastIndexOf must reuse indexOf's wire spelling", stmts[0].Effect.Op, kernelbridge.LoopOpIndexOf)
	}
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
	// scalar-count ceiling 3 -> code-unit ceiling 2*3 = 6, so the window
	// is {-1} u [0, 5]
	if !kernel.Member(set, []float64{-1}) {
		t.Errorf("member(out, [-1]) = false, want true — the not-found floor")
	}
	if !kernel.Member(set, []float64{5}) {
		t.Errorf("member(out, [5]) = false, want true — 2*hi-1")
	}
	if kernel.Member(set, []float64{6}) {
		t.Errorf("member(out, [6]) = true, want false — the ceiling excludes 2*hi")
	}
	if kernel.Member(set, []float64{-2}) {
		t.Errorf("member(out, [-2]) = true, want false — nothing below the -1 floor")
	}
}

func TestEffectLastIndexOf_ATwoArgumentCallDeclines(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	// a position argument shifts the search; this reader has not read
	// that shape, so it declines to the havoc floor rather than guess
	stmts := numericStringMethodLowered(t, kernel, `out = s.lastIndexOf("a", 2);`)
	if stmts[0].Effect.Kind != kernelbridge.LoopEffectUnknown {
		t.Fatalf("effect kind = %q, want %q — a 2-argument lastIndexOf must not serve the window", stmts[0].Effect.Kind, kernelbridge.LoopEffectUnknown)
	}
}

// booleanSearchServed asserts the lowering emitted the two-value set and
// walks it under an arbitrary receiver: the exit admits both truth
// values and nothing else.
func booleanSearchServed(t *testing.T, kernel *kernelbridge.RefinedTSKernel, source string) {
	t.Helper()
	stmts := numericStringMethodLowered(t, kernel, source)
	if stmts[0].Effect.Kind != kernelbridge.LoopEffectConst {
		t.Fatalf("effect kind = %q, want %q", stmts[0].Effect.Kind, kernelbridge.LoopEffectConst)
	}
	exit := kernel.Walk(
		[]kernelbridge.KnownStateWire{
			{Set: refinementsets.StringTuple("abc")},
			{Top: true},
		},
		stmts,
	)
	out := exit[1]
	if out.Top {
		t.Fatalf("out.Top = true, want false")
	}
	set := loweringSetOf(t, out)
	if !kernel.Member(set, []float64{0}) || !kernel.Member(set, []float64{1}) {
		t.Errorf("the two-value set excludes a truth value: %+v", set)
	}
	if kernel.Member(set, []float64{2}) {
		t.Errorf("the two-value set admits 2: %+v", set)
	}
}

func TestEffectStringBooleanSearch_IncludesServesTheTwoValueSet(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	booleanSearchServed(t, kernel, `out = s.includes("b");`)
}

func TestEffectStringBooleanSearch_StartsWithOneArgument(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	booleanSearchServed(t, kernel, `out = s.startsWith("a");`)
}

func TestEffectStringBooleanSearch_EndsWithOneArgument(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	booleanSearchServed(t, kernel, `out = s.endsWith("c");`)
}

func TestEffectStringBooleanSearch_StartsWithTwoArgumentFormAlsoServes(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	// the position argument changes no bound of the boolean claim, so the
	// 2-argument form is admitted too — write/call-free is all that gates it
	booleanSearchServed(t, kernel, `out = s.startsWith("a", 0);`)
}

func TestEffectStringBooleanSearch_ACallingArgumentDeclines(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	// the needle here calls g(), which moves state the boolean claim
	// reads nothing about — the whole call must not serve the pair
	stmts, ok := LowerStatements(&LoweringContext{
		Bindings: []string{"s", "out"},
		Sorts:    []BindingKind{BindingKindString, BindingKindNumber},
		Narrow:   kernel.Narrow,
	}, loweringParse(t, `out = s.includes(g());`))
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
		t.Errorf("out (slot 1) was not havocked by the calling-argument decline")
	}
}

func TestEffectStringLength_OnAStringLiteralIsExact(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	stmts := numericStringMethodLowered(t, kernel, `out = "hello".length;`)
	if stmts[0].Effect.Kind != kernelbridge.LoopEffectConst {
		t.Fatalf("effect kind = %q, want %q", stmts[0].Effect.Kind, kernelbridge.LoopEffectConst)
	}
	exit := kernel.Walk(
		[]kernelbridge.KnownStateWire{
			{Set: refinementsets.StringTuple("z")},
			{Top: true},
		},
		stmts,
	)
	out := exit[1]
	if out.Top {
		t.Fatalf("out.Top = true, want false")
	}
	set := loweringSetOf(t, out)
	if !kernel.Member(set, []float64{5}) {
		t.Errorf(`member(out, [5]) = false, want true — "hello" has 5 code units`)
	}
	if kernel.Member(set, []float64{4}) {
		t.Errorf("member(out, [4]) = true, want false — the exact row admits only 5")
	}
}

func TestEffectStringLength_OnATrackedReceiverIsTheSortOnlyRow(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	// a tracked slot's own repetition bounds are not readable Go-side (only
	// the kernel sees a slot's entry set, and no wire op asks it for a
	// length), so a bare `s.length` serves what the String type itself
	// claims: a non-negative integer at most 2^53-1
	stmts := numericStringMethodLowered(t, kernel, `out = s.length;`)
	if stmts[0].Effect.Kind != kernelbridge.LoopEffectConst {
		t.Fatalf("effect kind = %q, want %q", stmts[0].Effect.Kind, kernelbridge.LoopEffectConst)
	}
	exit := kernel.Walk(
		[]kernelbridge.KnownStateWire{
			{Set: refinementsets.StringTuple("hello world, this receiver's own bound is unread here")},
			{Top: true},
		},
		stmts,
	)
	out := exit[1]
	if out.Top {
		t.Fatalf("out.Top = true, want false — the sort-only row is a claim, not a refusal")
	}
	set := loweringSetOf(t, out)
	if !kernel.Member(set, []float64{9007199254740991}) {
		t.Errorf("member(out, [2^53-1]) = false, want true — the sort-only row bounds no value below the spec ceiling")
	}
	if kernel.Member(set, []float64{-1}) {
		t.Errorf("member(out, [-1]) = true, want false — a length is never negative")
	}
	if kernel.Member(set, []float64{1.5}) {
		t.Errorf("member(out, [1.5]) = true, want false — a length is always an integer")
	}
}

func TestEffectStringLength_OnAConcatenationIsAlsoTheSortOnlyRow(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	stmts, ok := LowerStatements(&LoweringContext{
		Bindings: []string{"a", "b", "out"},
		Sorts:    []BindingKind{BindingKindString, BindingKindString, BindingKindNumber},
		Narrow:   kernel.Narrow,
	}, loweringParse(t, `out = (a + b).length;`))
	if !ok {
		t.Fatalf("LowerStatements ok = false, want true")
	}
	if stmts[0].Effect.Kind != kernelbridge.LoopEffectConst {
		t.Fatalf("effect kind = %q, want %q — the concatenation's exact text is unknown here, so only the sort-only row applies", stmts[0].Effect.Kind, kernelbridge.LoopEffectConst)
	}
}
