// Pins the A3.xfer.matchall corpus row (matchAllInside) in process: a
// `for (const m of "AA BB".matchAll(/[A-Z]{2}/g))` binds m[0] to the
// pattern's OWN grammar, anchored — not the substring-anywhere padding
// FormatGrammar applies to a bare pattern by default. Before the
// anchoring fix, IterationElementOf's matchAll reader compiled the raw,
// unanchored source, so m[0] wore
// `Σ* · (>= 65 && <= 90 && integer) × exactly 2 · Σ*` instead of the
// exact two-letter grammar, and a Code-declared return refused a value
// that is genuinely in Code's set.
package walk

import (
	"reflect"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// iterationElementsCodeWindow is Code's own window —
// z.string().regex(/^[A-Z]{2}$/) read as a plain refinement set, the
// same stand-in convention compoundAssignAgeWindow keeps for Age.
func iterationElementsCodeWindow() *annotations.DeclaredRefinement {
	compiled := refinementsets.FormatGrammar("^[A-Z]{2}$", "")
	if !compiled.Ok {
		panic("Code's own grammar failed to compile: " + compiled.Unsupported)
	}
	set := compiled.Set
	return &annotations.DeclaredRefinement{Kind: annotations.DeclaredSet, Set: &set}
}

// TestIterationElements_MatchAllGroupZeroIsAssignableToCode reproduces
// the corpus row end to end: a Code-declared function returning
// `m[0]` from inside a `for (const m of "AA BB".matchAll(/[A-Z]{2}/g))`
// raises no diagnostic — m[0] is genuinely in Code's set.
func TestIterationElements_MatchAllGroupZeroIsAssignableToCode(t *testing.T) {
	kernel := compoundAssignLoadKernel(t)
	p := entryEnvTestProgram(t, "function f(): string {\n"+
		"  for (const m of \"AA BB\".matchAll(/[A-Z]{2}/g)) {\n"+
		"    return m[0];\n"+
		"  }\n"+
		"  return \"AA\";\n"+
		"}\n")
	var diagnostics []assignability.RefinementDiagnostic
	ctx := compoundAssignContext(p, kernel, &diagnostics, map[string]*annotations.DeclaredRefinement{})
	env := NewEnv()
	statements := compoundAssignFunctionStatements(t, p, "f")
	AnalyzeStatements(ctx, env, statements, iterationElementsCodeWindow())
	for _, d := range diagnostics {
		t.Errorf("m[0] from an anchored two-letter matchAll pattern raised a diagnostic, want silence: %s", d.MessageText)
	}
}

// TestIterationElements_MatchAllGroupZeroSetIsAnchoredNotPadded reads
// IterationElementOf directly and checks the compiled group-0 set
// equals the ANCHORED compile (`^(?:[A-Z]{2})$`) and differs from the
// unanchored, C*-padded compile a substring-anywhere reading would
// produce — the exact defect the fix closes.
func TestIterationElements_MatchAllGroupZeroSetIsAnchoredNotPadded(t *testing.T) {
	kernel := compoundAssignLoadKernel(t)
	p := entryEnvTestProgram(t, "function f(): void {\n"+
		"  for (const m of \"AA BB\".matchAll(/[A-Z]{2}/g)) {\n"+
		"    void m;\n"+
		"  }\n"+
		"}\n")
	var diagnostics []assignability.RefinementDiagnostic
	ctx := compoundAssignContext(p, kernel, &diagnostics, map[string]*annotations.DeclaredRefinement{})
	fn := entryEnvFunctionNamed(t, p, "f")
	body := fn.AsFunctionDeclaration().Body
	forOf := body.AsBlock().Statements.Nodes[0]
	if !ast.IsForInOrOfStatement(forOf) {
		t.Fatalf("statement 0 is not a for-of")
	}
	iterable := forOf.AsForInOrOfStatement().Expression
	env := NewEnv()
	element := IterationElementOf(ctx, env, iterable)
	if element == nil {
		t.Fatalf("IterationElementOf answered nil for the matchAll iterable")
	}
	if element.Kind != abstractdomain.KindList || len(element.Items) == 0 {
		t.Fatalf("expected a non-empty KnownList wrapping group 0, got Kind=%s", element.Kind)
	}
	group0 := element.Items[0]
	if group0.Kind != abstractdomain.KindSet {
		t.Fatalf("expected group 0 to be a KindSet, got %s", group0.Kind)
	}
	anchored := refinementsets.FormatGrammar("^(?:[A-Z]{2})$", "g")
	if !anchored.Ok {
		t.Fatalf("expected the anchored pattern to compile: %s", anchored.Unsupported)
	}
	if !reflect.DeepEqual(group0.Set, anchored.Set) {
		t.Errorf("group 0's compiled set should equal the anchored compile exactly\ngot:  %#v\nwant: %#v",
			group0.Set, anchored.Set)
	}
	padded := refinementsets.FormatGrammar("[A-Z]{2}", "g")
	if !padded.Ok {
		t.Fatalf("expected the unanchored pattern to compile: %s", padded.Unsupported)
	}
	if reflect.DeepEqual(group0.Set, padded.Set) {
		t.Errorf("group 0's compiled set should NOT equal the unanchored, C*-padded compile " +
			"(that padded reading is the defect this test guards against)")
	}
}
