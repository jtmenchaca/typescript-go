// Ports control_flow/switch_statement.test.ts. Label-value pinning:
// what a switch clause admits, read from literals only — no
// FlowContext, no walk.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/parser"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/derivation"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// switchExprOf mirrors the TS test's ts.factory.create* literal
// builders: an expression parsed from a throwaway source file's
// single expression statement — the same node shapes the factory
// calls would build, reached through the parser instead (no
// ts.factory twin is needed in Go; the parser already produces
// exactly these node kinds).
func switchExprOf(t *testing.T, source string) *ast.Node {
	t.Helper()
	opts := ast.SourceFileParseOptions{FileName: "/e.ts", Path: "/e.ts"}
	file := parser.ParseSourceFile(opts, source+";", core.ScriptKindTS)
	if len(file.Statements.Nodes) == 0 {
		t.Fatalf("no statements parsed from %q", source)
	}
	stmt := file.Statements.Nodes[0]
	if !ast.IsExpressionStatement(stmt) {
		t.Fatalf("statement is not an expression statement: %q", source)
	}
	return stmt.AsExpressionStatement().Expression
}

func TestSwitchKeyOfLabel_NumericNegativeStringBoolean(t *testing.T) {
	key, ok := SwitchKeyOfLabel(switchExprOf(t, "7"))
	if !ok || key != "n:7" {
		t.Errorf("SwitchKeyOfLabel(7) = %q, %v, want %q, true", key, ok, "n:7")
	}
	key, ok = SwitchKeyOfLabel(switchExprOf(t, "-3"))
	if !ok || key != "n:-3" {
		t.Errorf("SwitchKeyOfLabel(-3) = %q, %v, want %q, true", key, ok, "n:-3")
	}
	key, ok = SwitchKeyOfLabel(switchExprOf(t, `"hi"`))
	if !ok || key != "s:hi" {
		t.Errorf(`SwitchKeyOfLabel("hi") = %q, %v, want %q, true`, key, ok, "s:hi")
	}
	key, ok = SwitchKeyOfLabel(switchExprOf(t, "true"))
	if !ok || key != "b:true" {
		t.Errorf("SwitchKeyOfLabel(true) = %q, %v, want %q, true", key, ok, "b:true")
	}
	key, ok = SwitchKeyOfLabel(switchExprOf(t, "false"))
	if !ok || key != "b:false" {
		t.Errorf("SwitchKeyOfLabel(false) = %q, %v, want %q, true", key, ok, "b:false")
	}
	key, ok = SwitchKeyOfLabel(switchExprOf(t, "(1)"))
	if !ok || key != "n:1" {
		t.Errorf("SwitchKeyOfLabel((1)) = %q, %v, want %q, true", key, ok, "n:1")
	}
	key, ok = SwitchKeyOfLabel(switchExprOf(t, "`x`"))
	if !ok || key != "s:x" {
		t.Errorf("SwitchKeyOfLabel(`x`) = %q, %v, want %q, true", key, ok, "s:x")
	}
	_, ok = SwitchKeyOfLabel(switchExprOf(t, "k"))
	if ok {
		t.Errorf("SwitchKeyOfLabel(k) ok = true, want false")
	}
}

func TestSwitchKeyOfKnown_SortTaggedExactValuesAndNaN(t *testing.T) {
	key, ok := SwitchKeyOfKnown(abstractdomain.KnownValues([]float64{7}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved))
	if !ok || key != "n:7" {
		t.Errorf("SwitchKeyOfKnown(7) = %q, %v, want %q, true", key, ok, "n:7")
	}
	key, ok = SwitchKeyOfKnown(abstractdomain.KnownValues([]float64{1}, abstractdomain.PrimitiveBoolean, abstractdomain.TrustProved))
	if !ok || key != "b:true" {
		t.Errorf("SwitchKeyOfKnown(true) = %q, %v, want %q, true", key, ok, "b:true")
	}
	key, ok = SwitchKeyOfKnown(abstractdomain.KnownValues([]float64{0}, abstractdomain.PrimitiveBoolean, abstractdomain.TrustProved))
	if !ok || key != "b:false" {
		t.Errorf("SwitchKeyOfKnown(false) = %q, %v, want %q, true", key, ok, "b:false")
	}
	key, ok = SwitchKeyOfKnown(abstractdomain.KnownValues(refinementsets.CodepointsOf("hi"), abstractdomain.PrimitiveString, abstractdomain.TrustProved))
	if !ok || key != "s:hi" {
		t.Errorf("SwitchKeyOfKnown(hi) = %q, %v, want %q, true", key, ok, "s:hi")
	}
	key, ok = SwitchKeyOfKnown(abstractdomain.NaNValue)
	if !ok || key != "!nan" {
		t.Errorf("SwitchKeyOfKnown(NaN) = %q, %v, want %q, true", key, ok, "!nan")
	}
	_, ok = SwitchKeyOfKnown(abstractdomain.KnownValues([]float64{1, 2}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved))
	if ok {
		t.Errorf("SwitchKeyOfKnown([1,2]) ok = true, want false")
	}
}

func TestSwitchLabelValues_NumbersOneStringSeveralStringsMixed(t *testing.T) {
	numbers, ok := SwitchLabelValues([]*ast.Node{
		switchExprOf(t, "1"), switchExprOf(t, "2"), switchExprOf(t, "-3"),
	})
	if !ok {
		t.Fatalf("SwitchLabelValues([1,2,-3]) ok = false, want true")
	}
	want := abstractdomain.KnownValues([]float64{1, 2, -3}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
	gotFormatted, _ := abstractdomain.FormatAbstractValue(numbers)
	wantFormatted, _ := abstractdomain.FormatAbstractValue(want)
	if gotFormatted != wantFormatted {
		t.Errorf("SwitchLabelValues([1,2,-3]) = %q, want %q", gotFormatted, wantFormatted)
	}

	oneString, ok := SwitchLabelValues([]*ast.Node{switchExprOf(t, `"hi"`)})
	if !ok {
		t.Fatalf(`SwitchLabelValues(["hi"]) ok = false, want true`)
	}
	wantString := abstractdomain.KnownValues(refinementsets.CodepointsOf("hi"), abstractdomain.PrimitiveString, abstractdomain.TrustProved)
	gotStringFormatted, _ := abstractdomain.FormatAbstractValue(oneString)
	wantStringFormatted, _ := abstractdomain.FormatAbstractValue(wantString)
	if gotStringFormatted != wantStringFormatted {
		t.Errorf(`SwitchLabelValues(["hi"]) = %q, want %q`, gotStringFormatted, wantStringFormatted)
	}

	several, ok := SwitchLabelValues([]*ast.Node{switchExprOf(t, `"a"`), switchExprOf(t, `"b"`)})
	if !ok {
		t.Fatalf(`SwitchLabelValues(["a","b"]) ok = false, want true`)
	}
	if several.Kind != abstractdomain.KindSet {
		t.Errorf("SwitchLabelValues([a,b]).Kind = %v, want KindSet", several.Kind)
	}

	_, ok = SwitchLabelValues([]*ast.Node{switchExprOf(t, "1"), switchExprOf(t, `"a"`)})
	if ok {
		t.Errorf("SwitchLabelValues([1,a]) ok = true, want false")
	}
	_, ok = SwitchLabelValues([]*ast.Node{switchExprOf(t, "k")})
	if ok {
		t.Errorf("SwitchLabelValues([k]) ok = true, want false")
	}
	_, ok = SwitchLabelValues(nil)
	if ok {
		t.Errorf("SwitchLabelValues([]) ok = true, want false")
	}
}

// TestAnalyzeSwitchStatement_TypeofDiscriminantCaseBodyRecordsAWrittenTouch
// pins A12.guard.arm's own gap: `switch (typeof v)` narrows a NAMED
// discriminant place (SwitchKeyOfKnown, the identifier branch this file
// already handles) but never the typeof-wrapped operand's own binding —
// GroundOfTypeofWord's ground is a SORT, not the strict-equality key
// this reader's exact-scrutinee narrowing keys on. `v` inside the
// "number" case body is therefore left exactly as unbound as the switch
// found it. AnalyzeSwitchStatement now records that as a WRITTEN touch
// on `v`, naming the case clause, so a later read of `v` inside the case
// body names the switch instead of falling to a bare residue with no
// mutation to point at.
func TestAnalyzeSwitchStatement_TypeofDiscriminantCaseBodyRecordsAWrittenTouch(t *testing.T) {
	_, closer := derivation.BeginRecording("ts", "f.ts:1", 0)
	defer closer()

	p := entryEnvTestProgram(t, "function f(v: unknown): void {\n"+
		"  switch (typeof v) {\n"+
		"    case \"number\":\n"+
		"      v;\n"+
		"      break;\n"+
		"    default:\n"+
		"      break;\n"+
		"  }\n"+
		"}\n")
	var diagnostics []assignability.RefinementDiagnostic
	ctx := compoundAssignContext(p, nil, &diagnostics, nil)
	env := NewEnv()
	env.Set("v", abstractdomain.Unknown)
	statements := compoundAssignFunctionStatements(t, p, "f")
	AnalyzeStatements(ctx, env, statements, nil)

	if touch := derivation.LastTouchOf("v"); touch == "" {
		t.Fatal("LastTouchOf(v) is empty, want the switch's case clause recorded as a written touch")
	} else if touch == "written" {
		t.Fatal(`LastTouchOf(v) = "written" with no site named — want the case clause's own construct/range`)
	}
}
