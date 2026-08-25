// The residue-reason sweep's inline_contract_body.go family (the
// interprocedural inline-call unit): SummaryCallReceiver's constructed-
// receiver-runs-twice decline, ClassMethodWalkCall's self-recursion
// guard, InlineContractBody's recursion-induction answer and its
// bodyless-declaration decline. See residue_reason_test.go's header
// for the sibling map.
package walk

import (
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// inlineContractBodyResidueContext is superArrayContracts' recipe (real
// compiled contracts, no kernel seated) with a diagnostic sink and a
// `Declared["age"]` window wired in on top — the same two additions
// compoundAssignContext makes over an empty-Contracts FlowContext,
// needed here because these rows call INTO another declared function's
// body, which only resolves through CompileContractFileFacts' real
// contract collection.
func inlineContractBodyResidueContext(t *testing.T, p *program.CheckerProgram, sink *[]assignability.RefinementDiagnostic) *FlowContext {
	t.Helper()
	ctx := superArrayContracts(t, p)
	ctx.Declared = map[string]*annotations.DeclaredRefinement{"age": residueReasonAgeWindow()}
	ctx.Report = func(d assignability.RefinementDiagnostic) {
		*sink = append(*sink, d)
	}
	return ctx
}

// inlineContractBodyExpectSentence runs source's function "f" (the
// caller) through AnalyzeStatements against a `Declared["age"]` window,
// with no engine kernel seated (requireNoEngineKernel), and asserts the
// RTS7002 diagnostic's MessageText carries wantSubstring rather than the
// bare AlertText.
func inlineContractBodyExpectSentence(t *testing.T, source string, wantSubstring string) {
	t.Helper()
	requireNoEngineKernel(t)
	p := entryEnvTestProgram(t, source)
	var diagnostics []assignability.RefinementDiagnostic
	ctx := inlineContractBodyResidueContext(t, p, &diagnostics)
	env := NewEnv()
	statements := compoundAssignFunctionStatements(t, p, "f")
	AnalyzeStatements(ctx, env, statements, nil)
	if len(diagnostics) == 0 {
		t.Fatalf("source raised no diagnostic, want RTS7002:\n%s", source)
	}
	got := diagnostics[0].MessageText
	if got == assignability.AlertText {
		t.Errorf("diagnostic MessageText = the bare AlertText, want the site's own sentence naming %q", wantSubstring)
	}
	if !strings.Contains(got, wantSubstring) {
		t.Errorf("diagnostic MessageText = %q, want it to contain %q", got, wantSubstring)
	}
}

// summaryCallReceiverConstructedTwiceCallSite finds the CallExpression
// node spelled `new Holder(...).read()` under root.
func summaryCallReceiverConstructedTwiceCallSite(t *testing.T, root *ast.Node) *ast.Node {
	t.Helper()
	return superArrayFirstNode(t, root, "call new Holder(...).read()", func(node *ast.Node) bool {
		if !ast.IsCallExpression(node) {
			return false
		}
		callee := node.AsCallExpression().Expression
		if !ast.IsPropertyAccessExpression(callee) {
			return false
		}
		access := callee.AsPropertyAccessExpression()
		if access.Name() == nil || !ast.IsIdentifier(access.Name()) || access.Name().Text() != "read" {
			return false
		}
		return ast.IsNewExpression(access.Expression)
	})
}

// TestSummaryCallReceiver_AConstructedReceiverThatRunsTwiceNamesItsOwnReader
// pins SummaryCallReceiver's fallthrough (inline_contract_body.go)
// DIRECTLY: a `new Holder(sideEffect()).read()` receiver is not
// ReadsWithoutEffect (a `new` expression), so SummaryCallReceiver tries
// constructedReceiverValue — which declines because the constructor
// argument `sideEffect()` is a call, not readable a second time without
// duplicating its effect (readsTwiceWithoutEffect). Called directly
// (not through the diagnostic pipeline): a completed trace this session
// showed wornReturnTypeIfUnknown or another residue site can intervene
// between this function's own answer and any diagnostic sink, so the
// only pin that observes THIS site's own sentence unambiguously is a
// direct call on SummaryCallReceiver's own return value — the same
// precedent TestInlineContractBody_ADeclarationWithNoBodyNamesItsOwnReader
// and TestJoinSinkSummarized_AMarkerJoinsAsUnknownRatherThanExcluded set
// in this file.
func TestSummaryCallReceiver_AConstructedReceiverThatRunsTwiceNamesItsOwnReader(t *testing.T) {
	p := entryEnvTestProgram(t, "class Holder {\n"+
		"  n: number;\n"+
		"  constructor(seed: number) { this.n = seed; }\n"+
		"  read(): number { return this.n; }\n"+
		"}\n"+
		"function sideEffect(): number { return 1; }\n"+
		"function f(): void {\n"+
		"  new Holder(sideEffect()).read();\n"+
		"}\n")
	ctx := superArrayContracts(t, p)
	call := summaryCallReceiverConstructedTwiceCallSite(t, p.Entry.AsNode())
	got := SummaryCallReceiver(ctx, NewEnv(), call)
	if got.Kind != abstractdomain.KindUnknown {
		t.Fatalf("SummaryCallReceiver on new Holder(sideEffect()).read() = %+v, want KindUnknown", got)
	}
	if got.ResidueReason == "" {
		t.Fatalf("SummaryCallReceiver's constructed-receiver-runs-twice unknown carries no ResidueReason")
	}
	if !strings.Contains(got.ResidueReason, "a receiver that runs beyond a gated") {
		t.Errorf("ResidueReason = %q, want it to name the gated-construction rule", got.ResidueReason)
	}
}

// classMethodWalkCallRecursiveCallSite finds the CallExpression node
// spelled `this.bump()` under root — the recursive call's own node,
// which ClassMethodWalkCall reads through CalleeExpressionOf.
func classMethodWalkCallRecursiveCallSite(t *testing.T, root *ast.Node) *ast.Node {
	t.Helper()
	return superArrayFirstNode(t, root, "call this.bump()", func(node *ast.Node) bool {
		if !ast.IsCallExpression(node) {
			return false
		}
		callee := node.AsCallExpression().Expression
		if !ast.IsPropertyAccessExpression(callee) {
			return false
		}
		access := callee.AsPropertyAccessExpression()
		return access.Expression.Kind == ast.KindThisKeyword &&
			access.Name() != nil && ast.IsIdentifier(access.Name()) && access.Name().Text() == "bump"
	})
}

// TestClassMethodWalkCall_ASelfRecursiveMethodCallNamesItsOwnReader pins
// ClassMethodWalkCall's recursion guard (inline_contract_body.go)
// DIRECTLY, for the same reason SummaryCallReceiver's twin above is
// called directly: a class method that calls itself back through `this`
// re-enters ClassMethodWalkCall with its own symbol already marked
// inlining — simulated here by marking ctx.Inlining with bump's own
// resolved symbol BEFORE the call, the same symbol
// ctx.P.Checker.GetSymbolAtLocation(calleeName) resolves inside the
// function — and the guard's residue must name the re-entry.
func TestClassMethodWalkCall_ASelfRecursiveMethodCallNamesItsOwnReader(t *testing.T) {
	p := entryEnvTestProgram(t, "class Counter {\n"+
		"  n: number;\n"+
		"  constructor(seed: number) { this.n = seed; }\n"+
		"  bump(): number {\n"+
		"    this.n = this.n + 1;\n"+
		"    return this.bump();\n"+
		"  }\n"+
		"}\n")
	ctx := superArrayContracts(t, p)
	declaration := superArrayClassMethod(t, p, "Counter", "bump")
	call := classMethodWalkCallRecursiveCallSite(t, p.Entry.AsNode())
	calleeName := declaration.Name()
	symbol := ctx.P.Checker.GetSymbolAtLocation(calleeName)
	if symbol == nil {
		t.Fatalf("no symbol resolved for Counter.bump's own name")
	}
	ctx.Inlining = map[*ast.Symbol]struct{}{symbol: {}}
	receiver := abstractdomain.KnownObject(
		[]abstractdomain.ObjectKey{{Name: "n", Value: abstractdomain.KnownValues([]float64{0}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)}},
		nil, true, abstractdomain.TrustProved, false)
	contract := &FunctionContract{Declaration: declaration}
	got, handled := ClassMethodWalkCall(ctx, NewEnv(), call, contract, EffectiveArguments{}, receiver)
	if !handled {
		t.Fatalf("ClassMethodWalkCall declined (handled=false) on a re-entrant this.bump() call, want handled=true")
	}
	if got.Kind != abstractdomain.KindUnknown {
		t.Fatalf("ClassMethodWalkCall on a re-entrant this.bump() call = %+v, want KindUnknown", got)
	}
	if got.ResidueReason == "" {
		t.Fatalf("ClassMethodWalkCall's recursion-guard unknown carries no ResidueReason")
	}
	if !strings.Contains(got.ResidueReason, "calls back into itself") {
		t.Errorf("ResidueReason = %q, want it to name the re-entry rule", got.ResidueReason)
	}
}

// inlineContractBodyLoopForeverCallSite finds the CallExpression node
// spelled `loopForever(n)` under root — the RECURSIVE call inside
// loopForever's own body (the first, and only, call by that name in the
// fixture), which InlineContractBody's own memo/kernel-summary/walk
// routes read while walking loopForever's body.
func inlineContractBodyLoopForeverCallSite(t *testing.T, root *ast.Node) *ast.Node {
	t.Helper()
	return superArrayFirstNode(t, root, "call loopForever(n)", func(node *ast.Node) bool {
		if !ast.IsCallExpression(node) {
			return false
		}
		callee := node.AsCallExpression().Expression
		return ast.IsIdentifier(callee) && callee.Text() == "loopForever"
	})
}

// TestInlineContractBody_ARecursionInductionAnswerNamesItsOwnReader pins
// InlineContractBody's own recursion-marker-escaping guard DIRECTLY, for
// the same reason the two tests above are: a plain (non-method) function
// whose only return is a call to itself joins, in JoinSinkSummarized, to
// the marker itself (no other branch to join against) — and that marker
// now WEARS loopForever's own declared return ground (`number`, read
// through RecursionMarker's DeclaredReturnTypeGround call), not a bare
// `{Kind: KindUnknown}` sentinel. IsMarkerOf still recognizes it as
// loopForever's own marker by IDENTITY — a direct markerBySymbol[symbol]
// read, unaffected by what shape the marker holds — so `returned` is
// still rebuilt with the leans-on-induction sentence rather than
// escaping silently as the (now-distinguishable-looking) ground.
// requireNoEngineKernel keeps the kernel-summary route (which the
// gate's own trace showed answering first with a DIFFERENT residue,
// "AlertText + The kernel declined the question") out of play, so the
// walk-route recursion guard this test means to exercise is the one
// that actually runs.
func TestInlineContractBody_ARecursionInductionAnswerNamesItsOwnReader(t *testing.T) {
	requireNoEngineKernel(t)
	p := entryEnvTestProgram(t, "function loopForever(n: number): number {\n"+
		"  return loopForever(n);\n"+
		"}\n")
	ctx := superArrayContracts(t, p)
	declaration := entryEnvFunctionNamed(t, p, "loopForever")
	call := inlineContractBodyLoopForeverCallSite(t, p.Entry.AsNode())
	contract := &FunctionContract{Declaration: declaration}
	effective := EffectiveArguments{
		Nodes:  []*ast.Node{call.AsCallExpression().Arguments.Nodes[0]},
		Knowns: []abstractdomain.AbstractValue{abstractdomain.KnownValues([]float64{1}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)},
		Exact:  true,
	}

	// the stronger claim first: loopForever's own marker (read the same
	// way InlineContractBody's recursion guard reads it — symbol off the
	// callee name, declaration off the contract) wears the declared
	// return ground, `number`, not a bare unknown — and IsMarkerOf still
	// recognizes it as loopForever's own, by identity.
	symbol := ctx.P.Checker.GetSymbolAtLocation(declaration.Name())
	if symbol == nil {
		t.Fatalf("no symbol resolved for loopForever's own name")
	}
	marker := RecursionMarker(ctx, symbol, declaration)
	wantGround := abstractdomain.PossiblyNaN(abstractdomain.AtTrustLevel(
		abstractdomain.KnownSet(refinementsets.Numbers, nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone),
		abstractdomain.TrustLibrary,
	))
	if !abstractdomain.SameKnown(marker, wantGround) {
		spelled, _ := abstractdomain.FormatAbstractValue(marker)
		t.Fatalf("RecursionMarker(loopForever) = %q, want loopForever's declared return ground (number)", spelled)
	}
	if !IsMarkerOf(marker, symbol) {
		t.Fatalf("IsMarkerOf(loopForever's own ground-carrying marker, loopForever's symbol) = false, want true — a ground-carrying marker is still a marker by identity")
	}

	// the end-to-end claim: the induction answer InlineContractBody
	// hands OUTWARD is still the never-memoized decline, ground
	// discarded — the marker's ground rides only as far as the
	// recognition test above; a leans-on-induction answer holds only
	// inside its own induction.
	got := InlineContractBody(ctx, NewEnv(), call, contract, effective)
	if got.Kind != abstractdomain.KindUnknown {
		t.Fatalf("InlineContractBody(loopForever(1)) = %+v, want KindUnknown", got)
	}
	if got.ResidueReason == "" {
		t.Fatalf("InlineContractBody's recursion-induction unknown carries no ResidueReason")
	}
	if !strings.Contains(got.ResidueReason, "an answer leaning on a recursion induction") {
		t.Errorf("ResidueReason = %q, want it to name the recursion-induction rule", got.ResidueReason)
	}
}

// inlineContractBodyBodylessCallSite finds the CallExpression node
// spelled `pinBodyless(...)` anywhere under root — the call this test
// hands to InlineContractBody directly (superArrayFirstNode's generic
// walk-and-find, reused for a callee name instead of a `new C().m()`
// shape).
func inlineContractBodyBodylessCallSite(t *testing.T, root *ast.Node) *ast.Node {
	t.Helper()
	return superArrayFirstNode(t, root, "call pinBodyless(...)", func(node *ast.Node) bool {
		if !ast.IsCallExpression(node) {
			return false
		}
		callee := node.AsCallExpression().Expression
		return ast.IsIdentifier(callee) && callee.Text() == "pinBodyless"
	})
}

// TestInlineContractBody_ADeclarationWithNoBodyNamesItsOwnReader pins
// InlineContractBody's own body-nil decline directly: every PRODUCTION
// caller (evaluate_call_expression.go, evaluate_tagged_template.go)
// already gates `contract.Declaration.Body() != nil` before calling
// InlineContractCall/InlineContractBody at all, so this branch is
// unreachable through the ordinary walk — it is called here directly,
// the same way TestJoinSinkSummarized_AMarkerJoinsAsUnknownRatherThan
// Excluded pins JoinSinkSummarized directly, one function-call layer in
// from the full walk.
func TestInlineContractBody_ADeclarationWithNoBodyNamesItsOwnReader(t *testing.T) {
	p := entryEnvTestProgram(t, "declare function pinBodyless(x: number): number;\n"+
		"function f(): void {\n"+
		"  pinBodyless(1);\n"+
		"}\n")
	declaration := entryEnvFunctionNamed(t, p, "pinBodyless")
	call := inlineContractBodyBodylessCallSite(t, p.Entry.AsNode())
	ctx := &FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}, Report: func(assignability.RefinementDiagnostic) {}}
	contract := &FunctionContract{Declaration: declaration}
	got := InlineContractBody(ctx, NewEnv(), call, contract, EffectiveArguments{})
	if got.Kind != abstractdomain.KindUnknown {
		t.Fatalf("InlineContractBody on a bodyless declaration = %+v, want KindUnknown", got)
	}
	if got.ResidueReason == "" {
		t.Fatalf("InlineContractBody's body-nil unknown carries no ResidueReason")
	}
	if !strings.Contains(got.ResidueReason, "no body to inline") {
		t.Errorf("ResidueReason = %q, want it to name the missing body", got.ResidueReason)
	}
}
