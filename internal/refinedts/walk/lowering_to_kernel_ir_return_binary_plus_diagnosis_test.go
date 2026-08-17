// Diagnosis-only pin for the corpus census's "return (binary +)" row —
// getSumWithWeightedSource's own accumulator arm (Sankey.tsx:66):
// `return result + centerY(sourceNode) * getValue(links[id]);` inside a
// `reduce` callback. `result` is the callback's own tracked accumulator
// parameter; `centerY`/`getValue` are free module-level arrow functions.
//
// TWO PROBES. The first (below) is the SHAPE-ONLY harness
// (loweringResultContext), which has no context.ResolveCallee at all —
// every call inside the arithmetic necessarily declines there, so its
// "return (binary +)" havoc name says nothing about whether the real
// corpus body serves; it only confirms EffectOf/HoistCallEffect has no
// OTHER reading for an unresolvable call inside arithmetic (expected —
// that is interprocedural contract resolution's job, not this file's).
//
// The second probe goes through RelowerSummaryBody with the free arrow
// functions declared in the same source, which is what actually
// resolves callees for a corpus-shaped body.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
)

func TestLoweringToKernelIR_SankeyWeightedSourceBinaryPlusTaskDiagnosis(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	context := loweringResultContext([]string{"result", "sourceNode"}, []BindingKind{BindingKindNumber, BindingKindUnknown})
	context.Narrow = kernel.Narrow
	stmts, ok := LowerStatements(context, loweringParse(t,
		`return result + centerY(sourceNode) * getValue(sourceNode);`))
	t.Logf("shape-only probe (no ResolveCallee): ok=%v stmts=%+v firstHavoc=%q", ok, stmts, context.FirstHavoc)
	if !ok {
		t.Fatalf("declined outright")
	}
}

// TestLoweringToKernelIR_SankeyWeightedSourceBinaryPlusWithResolvableCalleesDiagnosis
// is the second probe: the free arrow functions ARE declared in the same
// source, mirroring Sankey.tsx's own module shape (centerY/getValue are
// top-level `const` arrows, exactly as declared at Sankey.tsx:43/46),
// and the accumulator arm sits inside a `reduce` callback exactly as
// getSumWithWeightedSource itself is shaped. Reports whether the
// callback-summary route (SummaryCallbackReturnOf, owned by a sibling
// agent per AGENT-BRIEF.md's territory split — ir_callback_recognition.go
// / ir_callback_convert.go / ir_summary_returned_shape.go) resolves the
// two free-function calls, which is the actual question this census row
// turns on — not a construct inside this agent's own return-lowering
// files.
func TestLoweringToKernelIR_SankeyWeightedSourceBinaryPlusWithResolvableCalleesDiagnosis(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
		type SankeyNode = { y: number; dy: number; source?: number };
		type LinkDataItemDy = { value?: number; source: number };
		const centerY = (node: SankeyNode) => node.y + node.dy / 2;
		const getValue = (entry: LinkDataItemDy | undefined): number => (entry && entry.value) || 0;
		function getSumWithWeightedSource(
			tree: ReadonlyArray<SankeyNode>,
			links: ReadonlyArray<LinkDataItemDy>,
			ids: number[],
		) {
			return ids.reduce((result: number, id: number) => {
				const link = links[id];
				if (link == null) {
					return result;
				}
				const sourceNode = tree[link.source];
				if (sourceNode == null) {
					return result;
				}
				return result + centerY(sourceNode) * getValue(link);
			}, 0);
		}
	`)
	declaration := entryEnvFunctionNamed(t, p, "getSumWithWeightedSource")
	_, ok := RelowerSummaryBody(&FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	t.Logf("resolvable-callee probe lowered ok=%v, outcome=%q, construct=%q", ok, outcome, construct)
}
