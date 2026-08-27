// Ports the coverage the A15.seed.boundary e2e row exercises:
// `.length`/`.size` used to answer the RECEIVER's own (possibly
// unbounded) shape and return before the place-value memory
// (HeldPlaceEntry, which carries a guard's own narrowing on the
// dotted path) was ever consulted — so a guard's upper bound on
// `id.length` was silently discarded at a later read of the same
// path. readLengthOrSizeAccess's answer is now MET with
// HeldPlaceEntry uniformly (ReadPropertyAccess's own call), the same
// idiom every other reader in evaluate_property_access.go already
// uses.
package walk

import (
	"math"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// The exact A15.seed.boundary shape: an untracked string parameter's
// `.length` is guarded both directions (`>= 0 && <= 150`), then read
// AGAIN at the return — the second read must carry the guard's upper
// bound, not just the receiver's own unbounded [0, +inf) floor.
func TestReadPropertyAccess_LengthReadAfterGuardKeepsTheUpperBound(t *testing.T) {
	source := "function f(id: string): number {\n" +
		"  if (id.length >= 0 && id.length <= 150) {\n" +
		"    return id.length;\n" +
		"  }\n" +
		"  return 0;\n" +
		"}\n"
	kernel := parseVocabKernel(t)
	contract, ctx, diagnostics := yieldContractOf(t, source, "f")
	ctx.Kernel = kernel

	var sink []abstractdomain.AbstractValue
	ctx.ReturnSink = &sink
	AnalyzeFunction(ctx, contract, nil)
	if len(sink) == 0 {
		t.Fatalf("f's body recorded no return value")
	}
	returned := JoinSinkSummarized(sink)

	// the join folds both return arms (the guarded id.length and the
	// literal 0) — RangeOfSet reads whichever KindSet the join settles
	// on; a KindValues singleton (the literal-0-only arm winning) would
	// mean the guarded arm never contributed its own window at all,
	// which is itself the regression this test also catches.
	var upperBound *float64
	switch returned.Kind {
	case abstractdomain.KindSet:
		if r := RangeOfSet(returned.Set); r != nil {
			upperBound = &r.Hi
		}
	case abstractdomain.KindKindUnion:
		for _, arm := range returned.Arms {
			if arm.Kind != abstractdomain.KindSet {
				continue
			}
			if r := RangeOfSet(arm.Set); r != nil && (upperBound == nil || r.Hi > *upperBound) {
				upperBound = &r.Hi
			}
		}
	}
	if upperBound == nil {
		spelled, _ := abstractdomain.FormatAbstractValue(returned)
		t.Fatalf("f's return value = %+v (%q), want a KindSet/KindKindUnion carrying id.length's guarded window", returned, spelled)
	}
	if *upperBound != 150 {
		t.Errorf("f's return value's upper bound = %v, want 150 — the guard's id.length <= 150 must survive the second read", *upperBound)
	}
	if len(*diagnostics) != 0 {
		t.Errorf("f reported %d diagnostics, want 0: %+v", len(*diagnostics), *diagnostics)
	}
}

// Isolates the defect at the unit the fix touches: readLengthOrSizeAccess
// itself answers the receiver's own (unbounded) shape — by design, it
// no longer consults HeldPlaceEntry itself (that meet now happens once,
// in ReadPropertyAccess, uniformly) — so this pins that
// readLengthOrSizeAccess's own answer is the WIDE one, and the meet is
// what narrows it; a caller that skipped the meet would regress
// silently even though this unit stayed correct.
func TestReadLengthOrSizeAccess_AnswersTheReceiversOwnUnboundedShape(t *testing.T) {
	source := "function f(id: string): number {\n" +
		"  if (id.length >= 0 && id.length <= 150) {\n" +
		"    return id.length;\n" +
		"  }\n" +
		"  return 0;\n" +
		"}\n"
	p := entryEnvTestProgram(t, source)
	fn := entryEnvFunctionNamed(t, p, "f")
	lengthNode := superArrayFirstNode(t, fn.Body(), "the id.length read inside the guard", func(node *ast.Node) bool {
		if !ast.IsPropertyAccessExpression(node) {
			return false
		}
		pa := node.AsPropertyAccessExpression()
		return ast.IsIdentifier(pa.Expression) && pa.Expression.Text() == "id" && pa.Name().Text() == "length"
	})
	pa := lengthNode.AsPropertyAccessExpression()

	env := NewEnv()
	env.Set("id", abstractdomain.KnownSet(
		refinementsets.Strings, nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone,
	))
	ctx := &FlowContext{P: p}
	got := readLengthOrSizeAccess(ctx, env, lengthNode, pa)
	if got == nil {
		t.Fatalf("readLengthOrSizeAccess(id.length, id unbounded) = nil, want a determined answer")
	}
	if got.Kind != abstractdomain.KindSet {
		t.Fatalf("readLengthOrSizeAccess(id.length, id unbounded).Kind = %v, want KindSet", got.Kind)
	}
	r := RangeOfSet(got.Set)
	if r == nil || r.Hi != math.Inf(1) {
		t.Errorf("readLengthOrSizeAccess(id.length, id unbounded) = %+v, want the receiver's own UNBOUNDED floor (no guard consulted at this layer)", got.Set)
	}
}

// TestMeetHeldPlaceEntry_AnEmptyMeetFallsBackToTheReadersOwnAnswer is the
// unit isolation for item 1's fix: a place-value memory entry built
// from a local comparison alone (`xs.length > 5`, with no knowledge of
// `xs`'s own `.max(3)` bound) can meet against the receiver's natural
// window to a scalar conjunction no value satisfies — Above(5) ∧
// AtMost(3) ∧ Integer, the exact shape repetition_panic_repro_test.go's
// ternary produces on its true arm. Before this fix that empty-but-
// unlabeled conjunction rode onward as a live KindSet; after it,
// meetHeldPlaceEntry asks the kernel whether the met set is empty and,
// where it is, falls back to the reader's own natural answer — the
// shape the receiver itself proves, not the locally-derived narrowing
// that turned out not to apply.
func TestMeetHeldPlaceEntry_AnEmptyMeetFallsBackToTheReadersOwnAnswer(t *testing.T) {
	kernel := parseVocabKernel(t)
	source := "function f(xs: number[]): number {\n" +
		"  return xs.length;\n" +
		"}\n"
	p := entryEnvTestProgram(t, source)
	fn := entryEnvFunctionNamed(t, p, "f")
	lengthNode := superArrayFirstNode(t, fn.Body(), "the xs.length read", func(node *ast.Node) bool {
		if !ast.IsPropertyAccessExpression(node) {
			return false
		}
		pa := node.AsPropertyAccessExpression()
		return ast.IsIdentifier(pa.Expression) && pa.Expression.Text() == "xs" && pa.Name().Text() == "length"
	})

	env := NewEnv()
	// xs's own natural window: a repetition set bounded above by 3, the
	// same shape z.array(z.number()).max(3) compiles to
	hi := 3
	natural := refinementsets.Repetition(refinementsets.Numbers, 0, &hi)
	env.Set("xs", abstractdomain.KnownSet(natural, nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone))
	// the place-value memory entry a `xs.length > 5` guard would have
	// written: a bare lower ray, built with no knowledge of xs's own
	// ceiling — set directly here to isolate meetHeldPlaceEntry from the
	// narrowing machinery that produces this shape in the full pipeline
	env.Set("xs.length", abstractdomain.KnownSet(
		refinementsets.MakeRefinedSet(refinementsets.Above(5)), nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone,
	))
	ctx := &FlowContext{P: p, Kernel: kernel}

	got := ReadPropertyAccess(ctx, env, lengthNode)
	if got == nil {
		t.Fatalf("ReadPropertyAccess(xs.length) = nil, want the receiver's own [0,3] window")
	}
	if got.Kind != abstractdomain.KindSet {
		t.Fatalf("ReadPropertyAccess(xs.length).Kind = %v, want KindSet", got.Kind)
	}
	r := RangeOfSet(got.Set)
	if r == nil || r.Lo != 0 || r.Hi != 3 {
		t.Errorf("ReadPropertyAccess(xs.length) = %+v, want the receiver's own [0,3] window — the empty meet with Above(5) must fall back to it, not ride onward as a live-looking empty set", got.Set)
	}
}

// TestMeetHeldPlaceEntry_AGatedAnswerSurvivesMeetingABareUnknownEntry pins
// the A9.guard.member fix: `s.r` on the false arm of `!("r" in s)` — the
// narrowing's absence proof for `s.r` lands as a bare, reason-less
// place-value entry (object_key_access.go's own comment on this: a
// negative `in` guard's proof does not turn into a set, so the dotted
// entry is handed back unchanged). ReadObjectKeyAccess's union-receiver
// arm answers its own ResidueOf-tagged unknown, naming why: "the
// receiver's key set is not proven complete...". Before this fix,
// abstractdomain.MeetKnown's own unknown/unknown case always answered
// the SECOND argument (held, here the bare entry) with no regard for
// which side actually named a gate, so the reader's sentence was
// silently discarded and the position's decline fell back to the
// generic "the walk holds nothing that pins this value" — a real
// sentence lost for no reason, though the value stayed exactly as
// undetermined either way.
func TestMeetHeldPlaceEntry_AGatedAnswerSurvivesMeetingABareUnknownEntry(t *testing.T) {
	gated := abstractdomain.AbstractValue{
		Kind:          abstractdomain.KindUnknown,
		ResidueReason: "the receiver's key set is not proven complete, so a name outside its known keys is neither proven present nor proven absent",
	}
	bareEntry := abstractdomain.Unknown

	// the exact A9.guard.member shape: `s.r` on the false arm of
	// `!("r" in s)` — the place-value memory holds a bare, reason-less
	// unknown at "s.r" (the narrowing's own absence proof does not turn
	// into a set), and the reader's answer (gated, above) is what
	// meetHeldPlaceEntry must keep.
	source := "function g(s: { kind: \"circle\"; r: number } | { kind: \"square\"; side: number }): void {\n" +
		"  if (!(\"r\" in s)) {\n" +
		"    s.r;\n" +
		"  }\n" +
		"}\n"
	p := entryEnvTestProgram(t, source)
	g := entryEnvFunctionNamed(t, p, "g")
	accessNode := superArrayFirstNode(t, g.Body(), "the s.r read", func(node *ast.Node) bool {
		if !ast.IsPropertyAccessExpression(node) {
			return false
		}
		pa := node.AsPropertyAccessExpression()
		return ast.IsIdentifier(pa.Expression) && pa.Expression.Text() == "s" && pa.Name().Text() == "r"
	})
	env := NewEnv()
	env.Set("s.r", bareEntry)
	ctx := &FlowContext{P: p}

	got := meetHeldPlaceEntry(ctx, env, accessNode, &gated)
	if got == nil {
		t.Fatalf("meetHeldPlaceEntry = nil, want the gated unknown back")
	}
	if got.Kind != abstractdomain.KindUnknown {
		t.Fatalf("meetHeldPlaceEntry.Kind = %v, want KindUnknown — this fix changes no determination", got.Kind)
	}
	if got.ResidueReason != gated.ResidueReason {
		t.Errorf("meetHeldPlaceEntry.ResidueReason = %q, want the reader's own gate %q preserved over the bare place-value entry",
			got.ResidueReason, gated.ResidueReason)
	}
}
