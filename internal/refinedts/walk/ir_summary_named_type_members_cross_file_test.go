// Pins the cross-file named-type premise AGENT-BRIEF's TASK 1 names:
// Sankey.tsx's `const centerY = (node: SankeyNode) => node.y + node.dy
// / 2;` where SankeyNode is an interface IMPORTED from another file
// (tmp/recharts-src/src/util/types.ts). The same-file twin
// (ir_array_parameters_test.go's TestCenterYShapedRecordParameter_
// PremiseCheck) already pins SummaryComplete — this file is the
// cross-file variant, built on crossFileConstructedProgram's two-file
// vfstest recipe (cross_file_constructed_test.go) since
// entryEnvTestProgram only ever takes one source.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
)

// TestCenterYShapedRecordParameter_CrossFilePremiseCheck is the
// cross-file twin of TestCenterYShapedRecordParameter_PremiseCheck
// (ir_array_parameters_test.go): SankeyNode declared in /helper.ts,
// centerY declared in /main.ts and importing it. Before this pin, the
// premise was that namedTypeMembersOf/declaredTypeMembersOf's
// symbolAt-based resolution would fail across the import edge the
// same way EvaluateNewExpression once did (cross_file_constructed_
// test.go's own header) — symbolAt already follows
// ast.SymbolFlagsAlias through GetAliasedSymbol (AGENT-BRIEF), so the
// question this pin actually answers is whether declaredTypeMembersOf
// inherits that alias-following for free or needs its own hop.
func TestCenterYShapedRecordParameter_CrossFilePremiseCheck(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	helperSource := `
export interface SankeyNode {
  dx: number;
  dy: number;
  name: string;
  value: any;
  x: number;
  y: number;
  depth: number;
  targetNodes: number[];
  targetLinks: number[];
  sourceNodes: number[];
  sourceLinks: number[];
}
`
	mainSource := `
import { SankeyNode } from "./helper.ts";
function centerY(node: SankeyNode) { return node.y + node.dy / 2; }
`
	p := crossFileConstructedProgram(t, helperSource, mainSource)
	declaration := entryEnvFunctionNamed(t, p, "centerY")
	ctx := &FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}
	RelowerSummaryBody(ctx, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(declaration)
	if !recorded {
		t.Fatalf("no outcome recorded — the lowering ran and must report a fate")
	}
	t.Logf("cross-file centerY-shaped body: outcome=%q construct=%q", outcome, construct)
	if outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q), want complete — SankeyNode is a plain interface, imported from another file", outcome, construct)
	}
}

// TestNamedTypeMembersOf_CrossFileHeritageExpandsTheParentToo widens
// the premise check past the plain-interface shape: a parameter typed
// by an interface that EXTENDS a parent declared IN THE SAME OTHER
// FILE (declaredTypeMembersOf's heritage recursion, both links
// resolved through symbolAt across the same import edge). If the
// heritage walk needed its own alias hop beyond what plain
// declaredTypeMembersOf already exercises, this is where it would
// show up as a decline rather than the parent's members merging in.
func TestNamedTypeMembersOf_CrossFileHeritageExpandsTheParentToo(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	helperSource := `
export interface Base { lo: number; }
export interface Bounds extends Base { hi: string; }
`
	mainSource := `
import { Bounds } from "./helper.ts";
function f(p: Bounds) { return p.lo; }
`
	p := crossFileConstructedProgram(t, helperSource, mainSource)
	declaration := entryEnvFunctionNamed(t, p, "f")
	ctx := &FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}
	entries, ok := SummaryParameterEntriesIn(ctx, declaration.Parameters()[0])
	if !ok {
		t.Fatalf("cross-file heritage declined the expansion — members: %+v", entries)
	}
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2 (lo from the cross-file parent, hi from the child): %+v", len(entries), entries)
	}
}
