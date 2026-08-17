// RefutePossiblyAbsent's own 7001 wording: the absent side's flavor
// names which runtime value the refutation is about (a NullOnly
// wrapper proves only 'null' is admitted, an UndefOnly wrapper only
// 'undefined') — the pre-flavor conflated wording ("may be
// 'undefined'") stays for the zero-value flavor, since a conflated
// wrapper may in fact be either at runtime.
package walk

import (
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// refuteAbsentStatedNumberWindow is a stated result set admitting any
// number — RefutePossiblyAbsent's own target shape (DeclaredSet), the
// same stand-in compound_assign_family_test.go and
// with_scope_narrowing_gate_test.go already use for a zod window.
func refuteAbsentStatedNumberWindow() *annotations.DeclaredRefinement {
	set := refinementsets.MakeRefinedSet(refinementsets.AtLeast(0))
	return &annotations.DeclaredRefinement{Kind: annotations.DeclaredSet, Set: &set}
}

// refuteAbsentIdentifierNode returns the checked position's own
// identifier node — the first statement in "f"'s body, an expression
// statement reading the parameter — so ast.GetSourceFileOfNode
// (assignability.At) and GuardFix's parent walk both see a real,
// parsed node.
func refuteAbsentIdentifierNode(t *testing.T, p *program.CheckerProgram) *ast.Node {
	t.Helper()
	fn := entryEnvFunctionNamed(t, p, "f")
	body := fn.AsFunctionDeclaration().Body
	if body == nil || !ast.IsBlock(body) {
		t.Fatalf("f has no block body")
	}
	stmts := body.AsBlock().Statements.Nodes
	if len(stmts) == 0 || !ast.IsExpressionStatement(stmts[0]) {
		t.Fatalf("f's body first statement is not an expression statement")
	}
	expr := stmts[0].AsExpressionStatement().Expression
	if !ast.IsIdentifier(expr) {
		t.Fatalf("f's body first statement is not a bare identifier read")
	}
	return expr
}

func refuteAbsentContext(sink *[]assignability.RefinementDiagnostic) *FlowContext {
	return &FlowContext{
		Contracts: map[*ast.Symbol]*FunctionContract{},
		Aliases:   dataflowfacts.NewAliasClasses(),
		Declared:  map[string]*annotations.DeclaredRefinement{},
		Report: func(d assignability.RefinementDiagnostic) {
			*sink = append(*sink, d)
		},
	}
}

func TestRefutePossiblyAbsent_NullOnlyNamesNull(t *testing.T) {
	p := entryEnvTestProgram(t, "function f(x: number) { x; }\n")
	node := refuteAbsentIdentifierNode(t, p)
	var sink []assignability.RefinementDiagnostic
	ctx := refuteAbsentContext(&sink)
	five := abstractdomain.KnownValues([]float64{5}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
	known := abstractdomain.PossiblyAbsent(five, abstractdomain.AbsentFlavorNullOnly, "", false, false)
	RefutePossiblyAbsent(ctx, known, *refuteAbsentStatedNumberWindow(), node, "the value")
	if len(sink) != 1 {
		t.Fatalf("RefutePossiblyAbsent(NullOnly) reported %d diagnostics, want 1", len(sink))
	}
	if !strings.Contains(sink[0].MessageText, "may be 'null'") {
		t.Errorf("RefutePossiblyAbsent(NullOnly) message = %q, want it to name 'null'", sink[0].MessageText)
	}
	if strings.Contains(sink[0].MessageText, "'undefined'") {
		t.Errorf("RefutePossiblyAbsent(NullOnly) message = %q, must not name 'undefined'", sink[0].MessageText)
	}
}

func TestRefutePossiblyAbsent_UndefOnlyNamesUndefined(t *testing.T) {
	p := entryEnvTestProgram(t, "function f(x: number) { x; }\n")
	node := refuteAbsentIdentifierNode(t, p)
	var sink []assignability.RefinementDiagnostic
	ctx := refuteAbsentContext(&sink)
	five := abstractdomain.KnownValues([]float64{5}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
	known := abstractdomain.PossiblyAbsent(five, abstractdomain.AbsentFlavorUndefOnly, "", false, false)
	RefutePossiblyAbsent(ctx, known, *refuteAbsentStatedNumberWindow(), node, "the value")
	if len(sink) != 1 {
		t.Fatalf("RefutePossiblyAbsent(UndefOnly) reported %d diagnostics, want 1", len(sink))
	}
	if !strings.Contains(sink[0].MessageText, "may be 'undefined'") {
		t.Errorf("RefutePossiblyAbsent(UndefOnly) message = %q, want it to name 'undefined'", sink[0].MessageText)
	}
	if strings.Contains(sink[0].MessageText, "'null'") {
		t.Errorf("RefutePossiblyAbsent(UndefOnly) message = %q, must not name 'null'", sink[0].MessageText)
	}
}

// The conflated (zero-value) flavor keeps the pre-flavor wording: a
// conflated wrapper may be either at runtime, so narrowing the wording
// to one word would overclaim.
func TestRefutePossiblyAbsent_ConflatedKeepsTheExistingWording(t *testing.T) {
	p := entryEnvTestProgram(t, "function f(x: number) { x; }\n")
	node := refuteAbsentIdentifierNode(t, p)
	var sink []assignability.RefinementDiagnostic
	ctx := refuteAbsentContext(&sink)
	five := abstractdomain.KnownValues([]float64{5}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
	known := abstractdomain.PossiblyUndefined(five, "", false, false)
	RefutePossiblyAbsent(ctx, known, *refuteAbsentStatedNumberWindow(), node, "the value")
	if len(sink) != 1 {
		t.Fatalf("RefutePossiblyAbsent(conflated) reported %d diagnostics, want 1", len(sink))
	}
	if !strings.HasPrefix(sink[0].MessageText, "the value may be 'undefined', which is not assignable to ") {
		t.Errorf("RefutePossiblyAbsent(conflated) message = %q, want the pre-flavor wording unchanged", sink[0].MessageText)
	}
}
