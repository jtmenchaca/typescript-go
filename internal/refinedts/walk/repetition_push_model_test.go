// Pins the E2.library.ts row: `xs.push(200)` on a receiver DECLARED
// `Age[]` is refused AT THE PUSH — not two lines later at a read of the
// widened `Age ∪ 200` — and THE REFUSED-WRITE LAW holds the tracked
// element at Age's own window rather than the value that just got
// refused, so the following read fires no SECOND diagnostic for the
// same defect (repetition_push_model.go's own doc, and assignments.go's
// WriteBinding for the plain-binding half of the same law).
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// repetitionPushAgeWindow is Age's own window (0..150, integer) as the
// repetition element compound_assign_family_test.go's group 3 already
// builds for an element-compound write — the same shape a declared
// `xs: Age[]` parameter states.
func repetitionPushAgeWindow() refinementsets.RefinedSet {
	return refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.AtMost(150), refinementsets.Integer)
}

// TestRepetitionPush_OutsideTheDeclaredElementSetFiresAtThePushNotTheRead
// is the E2.library.ts pushOutsideSet row: `xs.push(200)` where xs is
// declared `Age[]` fires exactly once, at the push — never at the
// following `return xs[xs.length - 1]` — because the tracked element
// keeps Age's own window rather than widening to `Age ∪ 200`.
func TestRepetitionPush_OutsideTheDeclaredElementSetFiresAtThePushNotTheRead(t *testing.T) {
	kernel := compoundAssignLoadKernel(t)
	p := entryEnvTestProgram(t, "function f(): number {\n"+
		"  xs.push(200);\n"+
		"  return xs[xs.length - 1];\n"+
		"}\n"+
		"declare const xs: number[];\n")
	var diagnostics []assignability.RefinementDiagnostic
	element := repetitionPushAgeWindow()
	windows := refinementsets.Repetition(element, 0, nil)
	ctx := compoundAssignContext(p, kernel, &diagnostics, map[string]*annotations.DeclaredRefinement{
		"xs": {Kind: annotations.DeclaredSet, Set: &windows},
	})
	env := NewEnv()
	env.Set("xs", abstractdomain.KnownSet(windows, nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone))
	statements := compoundAssignFunctionStatements(t, p, "f")
	AnalyzeStatements(ctx, env, statements, nil)
	if len(diagnostics) != 1 {
		t.Fatalf("`xs.push(200)` on a declared Age[] raised %d diagnostics, want exactly 1 (one fire per defect, at the push): %+v", len(diagnostics), diagnostics)
	}
	if diagnostics[0].Code != 7001 {
		t.Errorf("Code = %d, want 7001 — 200 is outside Age's declared [0, 150]", diagnostics[0].Code)
	}
	held, ok := env.Get("xs")
	if !ok {
		t.Fatalf("xs is untracked after `xs.push(200)`")
	}
	rep, repOk := refinementsets.AsRepetition(held.Set)
	if held.Kind != abstractdomain.KindSet || !repOk {
		t.Fatalf("xs after the refused push is not a tracked repetition: %+v", held)
	}
	wantElement, wantOk := abstractdomain.FormatAbstractValue(abstractdomain.KnownSet(refinementsets.CanonicalScalarForms(element), nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone))
	gotElement, gotOk := abstractdomain.FormatAbstractValue(abstractdomain.KnownSet(refinementsets.CanonicalScalarForms(rep.Element), nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone))
	if !wantOk || !gotOk || gotElement != wantElement {
		t.Errorf("xs's tracked element after the refused push = %q, want %q (Age's own window unchanged) — a refused write must not widen the tracked element to Age ∪ 200", gotElement, wantElement)
	}
}

// TestRepetitionPush_InsideTheDeclaredElementSetStaysSilent is the sibling
// admitted case: `xs.push(10)` on the same declared `Age[]` raises
// nothing, and the tracked element (and the raised length window) reads
// back sound — the refused-write law only ever narrows, never widens
// beyond what an admitted push already proves.
func TestRepetitionPush_InsideTheDeclaredElementSetStaysSilent(t *testing.T) {
	kernel := compoundAssignLoadKernel(t)
	p := entryEnvTestProgram(t, "function f(): number {\n"+
		"  xs.push(10);\n"+
		"  return xs[xs.length - 1];\n"+
		"}\n"+
		"declare const xs: number[];\n")
	var diagnostics []assignability.RefinementDiagnostic
	element := repetitionPushAgeWindow()
	windows := refinementsets.Repetition(element, 0, nil)
	ctx := compoundAssignContext(p, kernel, &diagnostics, map[string]*annotations.DeclaredRefinement{
		"xs": {Kind: annotations.DeclaredSet, Set: &windows},
	})
	env := NewEnv()
	env.Set("xs", abstractdomain.KnownSet(windows, nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone))
	statements := compoundAssignFunctionStatements(t, p, "f")
	AnalyzeStatements(ctx, env, statements, nil)
	if len(diagnostics) != 0 {
		t.Errorf("`xs.push(10)` on a declared Age[] (10 is in-set) raised %d diagnostics, want 0: %+v", len(diagnostics), diagnostics)
	}
}
