// Pins the fix to CheckPossiblyNaN's real-half subset question: a
// value whose real half ADDS NOTHING beyond the number sort's own
// ground (refinementsets.Numbers, the AtLeast(-Infinity) ray — the
// exact shape typereading.NumberWithNaN seeds an undetermined
// expression with) must decline (7002), never refute (7001) — "any
// real, or NaN" is not a derived fact about the value, it is the
// un-narrowed sort itself. A GENUINELY narrower real half (a walk-
// proven range that still fails to fit) must keep refuting exactly as
// before — this file pins both sides so the fix cannot broaden past
// its own boundary.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/derivation"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// nanWrapperLoadKernel mirrors every other kernel-backed walk test's
// own recipe (compoundAssignLoadKernel, yieldContractKernel, …) — skips
// (never a faked pass) when the native kernel dylib is absent.
func nanWrapperLoadKernel(t *testing.T) *kernelbridge.RefinedTSKernel {
	t.Helper()
	if !kernelbridge.KernelArtifactsPresent(kernelbridge.DylibPath) {
		t.Skip("native kernel dylib absent — build it first")
	}
	kernel, err := kernelbridge.LoadKernel(kernelbridge.DylibPath)
	if err != nil {
		t.Fatalf("LoadKernel: %v", err)
	}
	return kernel
}

// nanWrapperTestNode is a real *ast.Node to check against — GuardFix's
// own ast.IsIdentifier gate (sequence_measures.go) needs a genuine
// node, not a nil stand-in, on the 7002 fallthrough every case here
// reaches.
func nanWrapperTestNode(t *testing.T) *ast.Node {
	t.Helper()
	p := entryEnvTestProgram(t, "function f(age: number) { age; }\n")
	fn := entryEnvFunctionNamed(t, p, "f")
	body := fn.AsFunctionDeclaration().Body
	if body == nil || !ast.IsBlock(body) {
		t.Fatalf("f has no block body")
	}
	statements := body.AsBlock().Statements.Nodes
	if len(statements) == 0 || !ast.IsExpressionStatement(statements[0]) {
		t.Fatalf("f's first statement is not the bare `age;` expression")
	}
	return statements[0].AsExpressionStatement().Expression
}

// ageTarget is the fixture's own Age shape: >= 0 && <= 120 && integer.
func ageTarget() annotations.DeclaredRefinement {
	set := refinementsets.MakeRefinedSet(
		refinementsets.AtLeast(0),
		refinementsets.AtMost(120),
		refinementsets.Integer,
	)
	return annotations.DeclaredRefinement{Kind: annotations.DeclaredSet, Set: &set}
}

func TestCheckPossiblyNaN_SortGroundRealHalfDeclinesInsteadOfRefuting(t *testing.T) {
	node := nanWrapperTestNode(t)
	var reported []assignability.RefinementDiagnostic
	ctx := &FlowContext{Report: func(d assignability.RefinementDiagnostic) { reported = append(reported, d) }}

	// PossiblyNaN(Numbers) — typereading.NumberWithNaN's exact shape,
	// AfterReaders' own fallback seed for an expression the walk
	// determined nothing about (silence/after_readers.go). This asks
	// no kernel question at all: AddsNothingSet(Numbers) short-
	// circuits CheckPossiblyNaN straight to the 7002 fallthrough.
	known := abstractdomain.PossiblyNaN(
		abstractdomain.KnownSet(refinementsets.Numbers, nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone),
	)
	CheckPossiblyNaN(ctx, known, ageTarget(), node, "an initialized value")

	if len(reported) != 1 {
		t.Fatalf("reported %d diagnostics, want exactly 1: %+v", len(reported), reported)
	}
	if reported[0].Code != 7002 {
		t.Errorf("Code = %d, want 7002 (undetermined) — the un-narrowed sort ground carries no more information than KindUnknown, so it must decline the same way, not refute at %+v", reported[0].Code, reported[0])
	}
}

// TestCheckPossiblyNaN_SortGroundFallthroughOpensADeclinedRootSpan pins
// the trace-coverage fix: the same sort-ground fallthrough as
// TestCheckPossiblyNaN_SortGroundRealHalfDeclinesInsteadOfRefuting
// above, but driven through CheckAssignability (never CheckPossiblyNaN
// directly) so the outer checkAssignability root span opens exactly as
// it does in the real walk. Before the fix, CheckPossiblyNaN's 7002
// fallthrough reported straight through ctx.Report with no call to the
// Decline helper, so the root span opened by CheckAssignability
// (check_assignability.go's root.Answer(spellDeclared(&target)), set
// unconditionally when the span opens) never got flipped: a position
// that reported RTS7002 traced as [answered] with no gate, the exact
// shape packages/tests/e2e/state/C5.guard/C5.guard.ts:14 and its six
// siblings hit. The fix threads the fallthrough through
// DeclineSentence, so the root span now closes Declined with a
// non-empty gate.
func TestCheckPossiblyNaN_SortGroundFallthroughOpensADeclinedRootSpan(t *testing.T) {
	node := nanWrapperTestNode(t)
	recorder, closer := derivation.BeginRecording("ts", derivation.PositionOf(node), 0)
	defer closer()

	var reported []assignability.RefinementDiagnostic
	ctx := &FlowContext{Report: func(d assignability.RefinementDiagnostic) { reported = append(reported, d) }}

	known := abstractdomain.PossiblyNaN(
		abstractdomain.KnownSet(refinementsets.Numbers, nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone),
	)
	CheckAssignability(ctx, known, ageTarget(), node, "an initialized value", nil)

	if len(reported) != 1 || reported[0].Code != 7002 {
		t.Fatalf("reported = %+v, want exactly one 7002", reported)
	}

	traces := recorder.Traces()
	if len(traces) != 1 {
		t.Fatalf("recorder.Traces() = %d traces, want exactly 1: %+v", len(traces), traces)
	}
	root := traces[0].Root
	if root.Status != derivation.Declined {
		t.Fatalf("root.Status = %q, want %q — a reported RTS7002 must never leave the root span answered", root.Status, derivation.Declined)
	}
	if root.Attributes[derivation.AttrGate] == "" {
		t.Errorf("root.Attributes[refinery.gate] is empty — a declined span must name the premise that failed")
	}
	if _, stillAnswered := root.Attributes[derivation.AttrAnswer]; stillAnswered {
		t.Errorf("root.Attributes[refinery.answer] = %q, want it cleared once the span declines", root.Attributes[derivation.AttrAnswer])
	}
}

func TestCheckPossiblyNaN_ProvenNarrowerRealHalfStillRefutes(t *testing.T) {
	node := nanWrapperTestNode(t)
	var reported []assignability.RefinementDiagnostic
	ctx := &FlowContext{Report: func(d assignability.RefinementDiagnostic) { reported = append(reported, d) }}

	// a walk-PROVEN range (200..300) is genuine derived knowledge, not
	// the sort ground — AddsNothingSet(200..300) is false, so this
	// still takes the kernel-asked subset path. A nil ctx.Kernel here
	// panics inside checkPossiblyNaNSubset's own ScalarSubset call,
	// recovered by its try/catch into "a refused question keeps the
	// caller's alert" — which is the SAME 7002 fallthrough, so this
	// case cannot tell a genuine kernel refutation apart from a
	// refused one without a real kernel. Skip rather than pass for the
	// wrong reason.
	ctx.Kernel = nanWrapperLoadKernel(t)

	provenRange := refinementsets.MakeRefinedSet(refinementsets.AtLeast(200), refinementsets.AtMost(300))
	known := abstractdomain.PossiblyNaN(
		abstractdomain.KnownSet(provenRange, nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone),
	)
	CheckPossiblyNaN(ctx, known, ageTarget(), node, "an initialized value")

	if len(reported) != 1 {
		t.Fatalf("reported %d diagnostics, want exactly 1: %+v", len(reported), reported)
	}
	if reported[0].Code != 7001 {
		t.Errorf("Code = %d, want 7001 (200..300 is proven outside 0..120, a genuine refutation the fix must not silence) at %+v", reported[0].Code, reported[0])
	}
}
