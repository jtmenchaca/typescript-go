// The replace/replaceAll rows, walked end to end: the union row where
// its gates hold, the SORT row where they fail but the clause still
// guarantees a normal String completion, the FUNCTIONAL-REPLACER row
// (both sides of its write-set gate), and the refusals that stand ahead
// of every row (a regex `replaceAll` without `g`, whose clause throws,
// and a replacement this side cannot read at all). Skipped (never a
// faked pass) when the native kernel dylib is absent, the same gate
// every other walk test here uses.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// replaceLowered lowers `out = s.<call>;` over string-sorted s and out
// and hands back the statements.
func replaceLowered(t *testing.T, kernel *kernelbridge.RefinedTSKernel, source string) []kernelbridge.IrStatement {
	t.Helper()
	return replaceLoweredWithBindings(t, kernel, source,
		[]string{"s", "out"}, []BindingKind{BindingKindString, BindingKindString})
}

// replaceLoweredWithBindings is replaceLowered with a caller-chosen
// binding set — the functional-replacer tests need a THIRD tracked
// scalar for the replacer to write into, beside the receiver and the
// target every other test here uses.
func replaceLoweredWithBindings(t *testing.T, kernel *kernelbridge.RefinedTSKernel, source string,
	bindings []string, sorts []BindingKind) []kernelbridge.IrStatement {
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

// replaceSortRowServed asserts the lowering emitted the SORT row and
// walks it: the exit is a word — not top, not absent, not NaN, not a
// thrown exit — and its set is the root, admitting every word.
func replaceSortRowServed(t *testing.T, kernel *kernelbridge.RefinedTSKernel, source string) {
	t.Helper()
	stmts := replaceLowered(t, kernel, source)
	if len(stmts) != 1 {
		t.Fatalf("len(stmts) = %d, want 1", len(stmts))
	}
	if stmts[0].Effect.Kind != kernelbridge.LoopEffectSeqUnary {
		t.Fatalf("effect kind = %q, want %q", stmts[0].Effect.Kind, kernelbridge.LoopEffectSeqUnary)
	}
	if stmts[0].Effect.Op != kernelbridge.LoopOpReplaceSortSafe {
		t.Fatalf("effect op = %q, want %q", stmts[0].Effect.Op, kernelbridge.LoopOpReplaceSortSafe)
	}
	exit := kernel.Walk(
		[]kernelbridge.KnownStateWire{
			{Set: refinementsets.StringTuple("aaa")},
			{Top: true},
		},
		stmts,
	)
	out := exit[1]
	if out.Top {
		t.Fatalf("out.Top = true, want false — the sort row is a claim, not a refusal")
	}
	if out.Absent {
		t.Errorf("out.Absent = true, want false")
	}
	if out.Nan {
		t.Errorf("out.Nan = true, want false")
	}
	if out.Thrown {
		t.Errorf("out.Thrown = true, want false")
	}
	// the set half is the root: every word is admitted, none excluded
	set := loweringSetOf(t, out)
	if !kernel.Member(set, loweringCodePoints("zzzzzzzz")) {
		t.Errorf(`member(out, "zzzzzzzz") = false, want true — the sort row bounds no value`)
	}
}

// replaceRefused asserts the lowering fell to the havoc floor: only
// unknown assigns, the write target among them — the refusal answers
// top, which admits the thrown exit the shape can take.
func replaceRefused(t *testing.T, kernel *kernelbridge.RefinedTSKernel, source string) {
	t.Helper()
	stmts := replaceLowered(t, kernel, source)
	sawTarget := false
	for _, s := range stmts {
		if s.Kind != kernelbridge.IrStatementAssign || s.Effect.Kind != kernelbridge.LoopEffectUnknown {
			t.Fatalf("stmts = %+v, want only unknown assigns", stmts)
		}
		if s.Target == 1 {
			sawTarget = true
		}
	}
	if !sawTarget {
		t.Errorf("out (slot 1) was not havocked — its old knowledge would survive the write")
	}
}

func TestEffectReplace_AGlobalRegexTakesTheSortRow(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	// multi-match: no sound length ceiling exists, so the union row
	// declines; the result is still a String and the call still
	// completes (sec-regexp.prototype-%symbol.replace% steps 14-17)
	replaceSortRowServed(t, kernel, `out = s.replace(/a/gu, "b");`)
}

func TestEffectReplace_ReplaceAllAtAGlobalRegexTakesTheSortRow(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	// replaceAll admits only the `g` form (step 2.a.iii of
	// sec-string.prototype.replaceall throws otherwise); with the flag
	// proven in the literal, the call completes with a String
	replaceSortRowServed(t, kernel, `out = s.replaceAll(/a/gu, "b");`)
}

func TestEffectReplace_ADollarSubstitutionTakesTheSortRow(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	// "$&" expands to the match (sec-getsubstitution), so no finite
	// bump bounds the length; every branch still yields a String
	replaceSortRowServed(t, kernel, `out = s.replace("a", "$&x");`)
}

func TestEffectReplace_ANonUnicodeRegexTakesTheSortRow(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	// without `u`/`v` the matcher walks code units and can split an
	// astral pair, so the union alphabet fails; the result is still a
	// String
	replaceSortRowServed(t, kernel, `out = s.replace(/a/, "b");`)
}

func TestEffectReplace_AReplaceAllWhoseReplacementOutgrowsThePatternTakesTheSortRow(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	// every match may lengthen, so the receiver's ceiling cannot ride;
	// the sort row claims no ceiling at all
	replaceSortRowServed(t, kernel, `out = s.replaceAll("a", "bb");`)
}

func TestEffectReplace_TheUnionRowStillWinsOnAStringPattern(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	// strong beats weak: the union row's gates all hold here, so the
	// sort row must NOT serve
	stmts := replaceLowered(t, kernel, `out = s.replace("a", "b");`)
	if stmts[0].Effect.Op != kernelbridge.LoopOpReplaceUnionSafe {
		t.Fatalf("effect op = %q, want %q", stmts[0].Effect.Op, kernelbridge.LoopOpReplaceUnionSafe)
	}
	if stmts[0].Effect.Bump != 1 {
		t.Errorf("effect bump = %d, want 1", stmts[0].Effect.Bump)
	}
	// the union claim reads off the receiver's REPEAT form — an exact
	// word (StringTuple) states none, so the ceiling rides only where
	// the entry set states one: words of at most three scalars drawn
	// from {a}
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
	if !kernel.Member(set, loweringCodePoints("baa")) {
		t.Errorf(`member(out, "baa") = false, want true`)
	}
	// the union row still BOUNDS: five scalars exceed the receiver's
	// ceiling plus the bump, and `c` sits outside the union alphabet —
	// the sort row would have admitted both
	if kernel.Member(set, loweringCodePoints("bbbbb")) {
		t.Errorf(`member(out, "bbbbb") = true, want false — the union row's ceiling held`)
	}
	if kernel.Member(set, loweringCodePoints("c")) {
		t.Errorf(`member(out, "c") = true, want false — the union row's alphabet held`)
	}
}

func TestEffectReplace_TheUnionRowStillWinsOnASingleMatchUnicodeRegex(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	stmts := replaceLowered(t, kernel, `out = s.replace(/a/u, "b");`)
	if stmts[0].Effect.Op != kernelbridge.LoopOpReplaceUnionSafe {
		t.Fatalf("effect op = %q, want %q", stmts[0].Effect.Op, kernelbridge.LoopOpReplaceUnionSafe)
	}
}

// TestEffectReplace_AFunctionReplacementWithNoWritesTakesTheThrowRow
// covers the plain case: the replacer writes nothing tracked, so
// ClosureEscapesTrackedWrite answers false and the row serves with no
// havoc appended. The exit still carries the thrown flag UP, unlike the
// SORT row's exact-replacement claim.
func TestEffectReplace_AFunctionReplacementWithNoWritesTakesTheThrowRow(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	stmts := replaceLowered(t, kernel, `out = s.replace("a", (m) => m);`)
	if len(stmts) != 1 {
		t.Fatalf("len(stmts) = %d, want 1 — nothing tracked to havoc", len(stmts))
	}
	if stmts[0].Effect.Kind != kernelbridge.LoopEffectSeqUnary {
		t.Fatalf("effect kind = %q, want %q", stmts[0].Effect.Kind, kernelbridge.LoopEffectSeqUnary)
	}
	if stmts[0].Effect.Op != kernelbridge.LoopOpReplaceSortThrowSafe {
		t.Fatalf("effect op = %q, want %q", stmts[0].Effect.Op, kernelbridge.LoopOpReplaceSortThrowSafe)
	}
	exit := kernel.Walk(
		[]kernelbridge.KnownStateWire{
			{Set: refinementsets.StringTuple("aaa")},
			{Top: true},
		},
		stmts,
	)
	out := exit[1]
	if out.Top {
		t.Fatalf("out.Top = true, want false — the throw row is a claim, not a refusal")
	}
	if out.Absent {
		t.Errorf("out.Absent = true, want false")
	}
	if out.Nan {
		t.Errorf("out.Nan = true, want false")
	}
	if !out.Thrown {
		t.Errorf("out.Thrown = false, want true — a functional replacer may throw")
	}
	set := loweringSetOf(t, out)
	if !kernel.Member(set, loweringCodePoints("zzzzzzzz")) {
		t.Errorf(`member(out, "zzzzzzzz") = false, want true — the throw row bounds no value`)
	}
}

// TestEffectReplace_AFunctionReplacementWithACoverableWriteHavocsIt
// covers the gate's SERVED side: the replacer writes a tracked SCALAR
// name (`n`), so ClosureEscapesTrackedWrite answers true and its slot
// is havocked alongside the throw-capable row.
func TestEffectReplace_AFunctionReplacementWithACoverableWriteHavocsIt(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	stmts := replaceLoweredWithBindings(t, kernel,
		`out = s.replace("a", (m) => { n = 1; return m; });`,
		[]string{"s", "out", "n"},
		[]BindingKind{BindingKindString, BindingKindString, BindingKindNumber})
	// the hoisted havoc flushes AHEAD of the statement that reads it
	// (TakeHoisted, ir_call_hoist_flush.go), so `n`'s unknown assign
	// comes first and the row's own assign second
	if len(stmts) != 2 {
		t.Fatalf("len(stmts) = %d, want 2 (n havocked, plus the row): %+v", len(stmts), stmts)
	}
	sawNHavocked := false
	sawRow := false
	for _, s := range stmts {
		if s.Kind == kernelbridge.IrStatementAssign && s.Target == 2 && s.Effect.Kind == kernelbridge.LoopEffectUnknown {
			sawNHavocked = true
		}
		if s.Kind == kernelbridge.IrStatementAssign && s.Target == 1 &&
			s.Effect.Kind == kernelbridge.LoopEffectSeqUnary && s.Effect.Op == kernelbridge.LoopOpReplaceSortThrowSafe {
			sawRow = true
		}
	}
	if !sawNHavocked {
		t.Errorf("n (slot 2) was not havocked — its old knowledge would survive the replacer's write: %+v", stmts)
	}
	if !sawRow {
		t.Errorf("the throw row was not emitted: %+v", stmts)
	}
	exit := kernel.Walk(
		[]kernelbridge.KnownStateWire{
			{Set: refinementsets.StringTuple("aaa")},
			{Top: true},
			{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{0}))},
		},
		stmts,
	)
	if !exit[2].Top {
		t.Errorf("n's exit = %+v, want Top — the replacer's write invalidates its old value", exit[2])
	}
}

// TestEffectReplace_AFunctionReplacementWithAnUncoverableWriteStaysRefused
// covers the gate's REFUSED side: the replacer writes through a member
// access with no slot of its own (closureMutatesFlattenedCapture),
// which the scalar write census cannot spell — the same shape that
// leaves FunctionValuedDeclarationOf refusing a handed-over closure.
func TestEffectReplace_AFunctionReplacementWithAnUncoverableWriteStaysRefused(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	replaceRefused(t, kernel, `out = s.replace("a", (m) => { p.field = 1; return m; });`)
}

func TestEffectReplace_ReplaceAllAtARegexWithoutGStaysRefused(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	// step 2.a.iii of sec-string.prototype.replaceall throws a
	// TypeError when the flags lack "g", so "never a thrown exit" is
	// exactly false and no row holds
	replaceRefused(t, kernel, `out = s.replaceAll(/a/u, "b");`)
}
