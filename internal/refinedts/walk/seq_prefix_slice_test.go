// The seq_prefix kernel ask, wired into the .slice model: a
// concatenation-SHAPED string set (not an exact value) sliced as
// `(expr).slice(0, n)` reads through kernel_seq_prefix instead of
// falling to the sort-level Strings answer.
//
// The shape seq_prefix serves includes the fixed-SCALAR-head one: one
// literal character concatenated onto an unbounded tail,
// `("a" + rest).slice(0, n)` — AND, since seqWindowOf's extension to
// seqOf (subset_seq_shape.lean), the open-left shape too: an unbounded
// string concatenated with a fixed literal tail, `(seed +
// "xxxxxxxx").slice(0, n)`. Both answer through prefix_read.lean's
// prefixReadOf: a Repeat over foldAlphabet, the union of every
// recognized position's own scalar alphabet — TestSeqPrefixSlice_
// AnOpenLeftReceiverKeepsTheSortLevelFallback's ORIGINAL premise (this
// shape declines and keeps the sort-level Strings fallback) is STALE
// as of that extension; the test below pins the current, measured
// answer instead.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// oneCharConcatReceiver builds the KindSet value readStringConcatenation
// answers for `"<head>" + rest` (head one character, rest an
// unbounded string) — the seqOf-recognized fixed-scalar-head/open-tail
// shape.
func oneCharConcatReceiver(head rune) abstractdomain.AbstractValue {
	return abstractdomain.KnownSet(
		refinementsets.MakeRefinedSet(refinementsets.Concatenation(
			refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{float64(head)})),
			refinementsets.Strings,
		)),
		nil,
		abstractdomain.TrustSpec,
		abstractdomain.SetKindTagNone,
	)
}

func TestSeqPrefixSlice_AFixedScalarHeadConcatenationReadsTheKernelsPrefixWindow(t *testing.T) {
	kernel := superArrayLoadKernel(t)
	p := entryEnvTestProgram(t, "function f(rest: string): string {\n"+
		"  return (\"a\" + rest).slice(0, 3);\n"+
		"}\n")
	ctx := superArrayContracts(t, p)
	ctx.Kernel = kernel
	fn := entryEnvFunctionNamed(t, p, "f")
	returned := superArrayFirstNode(t, fn.Body(), "return statement", ast.IsReturnStatement)
	call := returned.AsReturnStatement().Expression
	if !ast.IsCallExpression(call) {
		t.Fatalf("the return expression is not a call: %+v", call)
	}

	receiver := oneCharConcatReceiver('a')
	argKnowns := []abstractdomain.AbstractValue{
		abstractdomain.KnownValues([]float64{0}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved),
		abstractdomain.KnownValues([]float64{3}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved),
	}
	site := MethodCallSite{
		Ctx:      ctx,
		Env:      NewEnv(),
		E:        call,
		Receiver: receiver,
		Method:   "slice",
	}
	got := readStringMethods(site, argKnowns, true, abstractdomain.TrustSpec)
	if got == nil {
		t.Fatalf(`readStringMethods(("a"+rest).slice(0,3)) = nil, want the kernel's prefix window`)
	}
	if got.Kind != abstractdomain.KindSet || got.SetKindTag != abstractdomain.SetKindTagNone {
		spelled, _ := abstractdomain.FormatAbstractValue(*got)
		t.Fatalf("got = %+v (%q), want a KindSet/SetKindTagNone value", got, spelled)
	}
	spelled, _ := abstractdomain.FormatAbstractValue(*got)
	t.Logf("answered set spells: %q (%+v)", spelled, got.Set)
	// the kernel's answer folds the fixed head's alphabet and the
	// tail's alphabet into ONE set (foldAlphabet, prefix_read.lean) —
	// a length-and-alphabet window, not a positional "starts with a"
	// claim. take(3) of "a"+rest is length 1..3 over that fold: "abc"
	// (rest contributing "bc…") is a member, and a FOURTH character
	// never appears in a take-3 result
	if !kernel.Member(got.Set, refinementsets.CodepointsOf("abc")) {
		t.Errorf(`the answered set excludes "abc" — take(3) of "a"+"bc…" must be a member`)
	}
	if kernel.Member(got.Set, refinementsets.CodepointsOf("abcd")) {
		t.Errorf(`the answered set admits "abcd" — .slice(0,3) never returns more than 3 characters`)
	}
	if !kernel.Member(got.Set, refinementsets.CodepointsOf("a")) {
		t.Errorf(`the answered set excludes "a" — rest may be empty, leaving only the fixed head`)
	}
	// the window's floor is exactly the fixed-head count (1): a
	// zero-length result is never a member — take(3) always includes
	// at least the fixed head
	if kernel.Member(got.Set, refinementsets.CodepointsOf("")) {
		t.Errorf(`the answered set admits the empty word — the window's floor is 1 (the fixed head is always present)`)
	}
}

func TestSeqPrefixSlice_ANonZeroStartKeepsTheSortLevelFallback(t *testing.T) {
	kernel := superArrayLoadKernel(t)
	p := entryEnvTestProgram(t, "function f(rest: string): string {\n"+
		"  return (\"a\" + rest).slice(1, 3);\n"+
		"}\n")
	ctx := superArrayContracts(t, p)
	ctx.Kernel = kernel
	fn := entryEnvFunctionNamed(t, p, "f")
	returned := superArrayFirstNode(t, fn.Body(), "return statement", ast.IsReturnStatement)
	call := returned.AsReturnStatement().Expression

	receiver := oneCharConcatReceiver('a')
	argKnowns := []abstractdomain.AbstractValue{
		abstractdomain.KnownValues([]float64{1}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved),
		abstractdomain.KnownValues([]float64{3}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved),
	}
	site := MethodCallSite{
		Ctx:      ctx,
		Env:      NewEnv(),
		E:        call,
		Receiver: receiver,
		Method:   "slice",
	}
	got := readStringMethods(site, argKnowns, true, abstractdomain.TrustSpec)
	if got == nil {
		t.Fatalf("readStringMethods(.slice(1,3)) = nil, want the sort-level Strings fallback")
	}
	if !refinementsets.IsStringGround(got.Set) {
		spelled, _ := abstractdomain.FormatAbstractValue(*got)
		t.Errorf("a non-zero start must keep the plain Strings answer, got %q", spelled)
	}
}

// TestSeqPrefixSlice_AnOpenLeftReceiverReadsTheKernelsPrefixWindow pins
// §G's OWN text_label.ts shape: seed is an unbounded plain string on
// the LEFT of the concatenation, `(seed + "xxxxxxxx")`.
//
// OLD PREMISE (stale, this test's own original name and body): "seqOf
// requires the LEFT operand scalar (subset_seq_shape.lean's seqOf, the
// .Concatenation A B arm's `if A.scalarB then … else none`), and
// Strings (a Star) is never scalar, so this receiver correctly
// declines and keeps the existing sort-level answer" — asserted
// `refinementsets.IsStringGround(got.Set)` (the UNBOUNDED Strings
// ground alone).
//
// MEASURED, CURRENT: seqWindowOf's extension to seqOf now recognizes
// this open-left shape too, so kernel.SeqPrefix answers rather than
// declining. The answer is prefixReadOf's own Repeat over the folded
// alphabet (foldAlphabet unions the receiver's own scalar alphabet —
// Codepoints, from Strings' star — with each fixed literal character
// "xxxxxxxx" folds in) — measured here as the exact 3-character window
// `{𝑙𝑒𝑛 = 3}` (FormatAbstractValue), member of "abc", not a member of
// "abcd".
func TestSeqPrefixSlice_AnOpenLeftReceiverReadsTheKernelsPrefixWindow(t *testing.T) {
	kernel := superArrayLoadKernel(t)
	p := entryEnvTestProgram(t, "function f(seed: string): string {\n"+
		"  return (seed + \"xxxxxxxx\").slice(0, 3);\n"+
		"}\n")
	ctx := superArrayContracts(t, p)
	ctx.Kernel = kernel
	fn := entryEnvFunctionNamed(t, p, "f")
	returned := superArrayFirstNode(t, fn.Body(), "return statement", ast.IsReturnStatement)
	call := returned.AsReturnStatement().Expression

	receiver := abstractdomain.KnownSet(
		refinementsets.MakeRefinedSet(refinementsets.Concatenation(refinementsets.Strings, refinementsets.StringTuple("xxxxxxxx"))),
		nil,
		abstractdomain.TrustSpec,
		abstractdomain.SetKindTagNone,
	)
	argKnowns := []abstractdomain.AbstractValue{
		abstractdomain.KnownValues([]float64{0}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved),
		abstractdomain.KnownValues([]float64{3}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved),
	}
	site := MethodCallSite{
		Ctx:      ctx,
		Env:      NewEnv(),
		E:        call,
		Receiver: receiver,
		Method:   "slice",
	}
	got := readStringMethods(site, argKnowns, true, abstractdomain.TrustSpec)
	if got == nil {
		t.Fatalf(`readStringMethods((seed+"xxxxxxxx").slice(0,3)) = nil, want the kernel's prefix window`)
	}
	if got.Kind != abstractdomain.KindSet || got.SetKindTag != abstractdomain.SetKindTagNone {
		spelled, _ := abstractdomain.FormatAbstractValue(*got)
		t.Fatalf("got = %+v (%q), want a KindSet/SetKindTagNone value", got, spelled)
	}
	spelled, spelledOk := abstractdomain.FormatAbstractValue(*got)
	if !spelledOk || spelled != "{𝑙𝑒𝑛 = 3}" {
		t.Errorf(`FormatAbstractValue(got) = %q, ok=%v, want "{𝑙𝑒𝑛 = 3}" — a 3-character string window, `+
			`the container-hover discriminator (IsCodepointAlphabetFold) reading the folded alphabet as a `+
			`codepoint element rather than a numeric tuple`, spelled, spelledOk)
	}
	// the exact 3-character window: a 3-character member is admitted,
	// a 4-character one is not
	if !kernel.Member(got.Set, refinementsets.CodepointsOf("abc")) {
		t.Errorf(`the answered set excludes "abc" — take(3) of an unbounded receiver must be a member`)
	}
	if kernel.Member(got.Set, refinementsets.CodepointsOf("abcd")) {
		t.Errorf(`the answered set admits "abcd" — .slice(0,3) never returns more than 3 characters`)
	}
	if kernel == nil {
		t.Fatalf("kernel must be loaded for this test to mean anything")
	}
}
