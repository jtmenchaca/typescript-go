// The call statement: a site whose callee has a compiled summary
// lowers to IrStatementCall rather than inlining the callee's body, and
// the table the body carries names each callee once.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/parser"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

func TestSummaryTableBuilder_ACalleeIsTabledOnceAndReusedAfterwards(t *testing.T) {
	builder := &SummaryTableBuilder{}
	first := &ast.Node{}
	second := &ast.Node{}
	if index := builder.CalleeIndex(first, kernelbridge.SummaryBlob("one")); index != 0 {
		t.Errorf("first callee index = %d, want 0", index)
	}
	if index := builder.CalleeIndex(second, kernelbridge.SummaryBlob("two")); index != 1 {
		t.Errorf("second callee index = %d, want 1", index)
	}
	if index := builder.CalleeIndex(first, kernelbridge.SummaryBlob("one")); index != 0 {
		t.Errorf("the first callee's second ask = %d, want 0 — a callee is tabled once", index)
	}
	if len(builder.Blobs) != 2 {
		t.Errorf("len(table) = %d, want 2", len(builder.Blobs))
	}
	if builder.Blobs[0] != "one" || builder.Blobs[1] != "two" {
		t.Errorf("table = %v, want [one two]", builder.Blobs)
	}
}

// summaryCallParse parses a source whose statements the lowering reads
// directly — no checker involved.
func summaryCallParse(t *testing.T, source string) []*ast.Node {
	t.Helper()
	opts := ast.SourceFileParseOptions{FileName: "/call.ts", Path: "/call.ts"}
	file := parser.ParseSourceFile(opts, source, core.ScriptKindTS)
	return file.Statements.Nodes
}

func TestSummaryCallStatement_WithoutAFlowContextTheCallStatementDeclines(t *testing.T) {
	// no registry to ask, so the site keeps whatever other route it has
	context := &LoweringContext{
		Bindings:     []string{"x"},
		Sorts:        []BindingKind{BindingKindNumber},
		Typeofs:      []TypeofTag{TypeofTagNumber},
		SummaryTable: &SummaryTableBuilder{},
	}
	statements := summaryCallParse(t, `x = g(1);`)
	if _, ok := SummaryCallStatementOf(context, statements[0]); ok {
		t.Errorf("a call lowered without a flow context — there is no registry to answer for the callee")
	}
}

func TestSummaryCallStatement_WithoutATableTheCallStatementDeclines(t *testing.T) {
	// the table is where a callee's blob is indexed from; with none, the
	// call has no Callee field to name
	context := &LoweringContext{
		Bindings: []string{"x"},
		Sorts:    []BindingKind{BindingKindNumber},
		Typeofs:  []TypeofTag{TypeofTagNumber},
		Flow:     &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}},
	}
	statements := summaryCallParse(t, `x = g(1);`)
	if _, ok := SummaryCallStatementOf(context, statements[0]); ok {
		t.Errorf("a call lowered without a table — its Callee field would index nothing")
	}
}

/* ── record arguments ────────────────────────────────────────────── */

// recordParameterOf parses a one-parameter declaration and answers the
// members its annotation expands to.
func recordParameterOf(t *testing.T, source string) []recordParamMember {
	t.Helper()
	opts := ast.SourceFileParseOptions{FileName: "/p.ts", Path: "/p.ts"}
	file := parser.ParseSourceFile(opts, source, core.ScriptKindTS)
	declaration := file.Statements.Nodes[0]
	members, ok := recordParamMembersOf(declaration.Parameters()[0])
	if !ok {
		t.Fatalf("the parameter of %q did not expand", source)
	}
	return members
}

// firstExpressionOf is the expression a one-statement source spells.
func firstExpressionOf(t *testing.T, source string) *ast.Node {
	t.Helper()
	statements := summaryCallParse(t, source)
	return Unwrapped(statements[0].AsExpressionStatement().Expression)
}

func TestRecordArgumentEffects_AnObjectLiteralArgumentLowersEachMemberByName(t *testing.T) {
	members := recordParameterOf(t, "function g(p: { lo: number, hi: number }) { return p.lo; }")
	context := &LoweringContext{
		Bindings: []string{"n"},
		Sorts:    []BindingKind{BindingKindNumber},
		Typeofs:  []TypeofTag{TypeofTagNumber},
	}
	// the keys are spelled in the OTHER order — the effects must still
	// come out in the parameter's member order
	effects, ok := recordArgumentEffects(context, members, firstExpressionOf(t, `({ hi: n, lo: 4 });`))
	if !ok {
		t.Fatalf("an object literal of exactly the members declined")
	}
	if len(effects) != 2 {
		t.Fatalf("len(effects) = %d, want 2", len(effects))
	}
	// effect 0 fills "p.lo" — the literal 4
	if effects[0].Kind != kernelbridge.LoopEffectConst {
		t.Errorf("effect 0 kind = %v, want the constant 4 that lo was given", effects[0].Kind)
	}
	// effect 1 fills "p.hi" — a var of n's slot
	if effects[1].Kind != kernelbridge.LoopEffectVar || effects[1].Index != 0 {
		t.Errorf("effect 1 = %+v, want a var of slot 0 (n)", effects[1])
	}
}

func TestRecordArgumentEffects_AFlattenedRecordLocalArgumentLowersEachLeafSlotsVar(t *testing.T) {
	members := recordParameterOf(t, "function g(p: { lo: number, hi: number }) { return p.lo; }")
	context := &LoweringContext{
		Bindings: []string{"q.hi", "q.lo"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber},
		Typeofs:  []TypeofTag{TypeofTagNumber, TypeofTagNumber},
	}
	effects, ok := recordArgumentEffects(context, members, firstExpressionOf(t, `(q);`))
	if !ok {
		t.Fatalf("a flattened record local of exactly these leaves declined")
	}
	// member order, not slot order: lo first, though its slot is 1
	if len(effects) != 2 {
		t.Fatalf("len(effects) = %d, want 2", len(effects))
	}
	if effects[0].Kind != kernelbridge.LoopEffectVar || effects[0].Index != 1 {
		t.Errorf("effect 0 = %+v, want a var of q.lo's slot 1", effects[0])
	}
	if effects[1].Kind != kernelbridge.LoopEffectVar || effects[1].Index != 0 {
		t.Errorf("effect 1 = %+v, want a var of q.hi's slot 0", effects[1])
	}
}

func TestRecordArgumentEffects_TheWrongShapedArgumentsDecline(t *testing.T) {
	members := recordParameterOf(t, "function g(p: { lo: number, hi: number }) { return p.lo; }")
	context := &LoweringContext{
		Bindings: []string{"n", "r.lo"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber},
		Typeofs:  []TypeofTag{TypeofTagNumber, TypeofTagNumber},
	}
	sources := map[string]string{
		"a literal missing a member": `({ lo: 1 });`,
		"a literal with an extra":    `({ lo: 1, hi: 2, mid: 3 });`,
		"a literal with a spread":    `({ ...n, lo: 1, hi: 2 });`,
		"a shorthand row":            `({ lo, hi });`,
		"a computed key":             `({ [n]: 1, hi: 2 });`,
		"a scalar local":             `(n);`,
		"a record of other leaves":   `(r);`,
		"a call result":              `(h(1));`,
	}
	for name, source := range sources {
		if _, ok := recordArgumentEffects(context, members, firstExpressionOf(t, source)); ok {
			t.Errorf("%s filled the leaf entries — only an exact literal or an exact flattened local may", name)
		}
	}
}

func TestSummaryCallStatement_ANonCallStatementDeclines(t *testing.T) {
	context := &LoweringContext{
		Bindings:     []string{"x", "y"},
		Sorts:        []BindingKind{BindingKindNumber, BindingKindNumber},
		Typeofs:      []TypeofTag{TypeofTagNumber, TypeofTagNumber},
		Flow:         &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}},
		SummaryTable: &SummaryTableBuilder{},
	}
	statements := summaryCallParse(t, `x = y + 1;`)
	if _, ok := SummaryCallStatementOf(context, statements[0]); ok {
		t.Errorf("an ordinary assignment took the call route")
	}
}
