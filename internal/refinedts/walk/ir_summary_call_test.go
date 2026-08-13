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
