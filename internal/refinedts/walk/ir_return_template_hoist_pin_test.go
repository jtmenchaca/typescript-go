// Pin for the census rows "return (template over call …)" —
// BarStack.tsx's `return \`url(#${getClipPathId(stackId, index)})\`;`.
//
// OBSERVED PRE-FIX (this pin failed before the two edits below): with
// getClipPathId's contract registered and its blob compiled COMPLETE,
// the consumer still settled porous at "return (template over call
// getClipPathId)". The blocker was never the hoist: the summary layout
// laid #ret out BindingKindUnknown unconditionally, so the return
// route's sort fallback read the returned template NUMERICALLY and the
// sequence grammar — the only route that hoists a template's call
// substitution — was never asked. Two edits close it:
//
//   - retDeclaredStringSort (ir_summary_body_lowering_slots.go): a
//     `: string`-annotated body's #ret wears the string sort, so the
//     returned template takes the sequence route;
//   - templateSpanOpaqueCallEffect (effect_sequence.go): a call
//     substitution the blob-backed hoist cannot serve still hoists
//     through HoistOpaqueCallTemp, under the sort/order gates its own
//     doc states.
package walk

import (
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
)

// templatePinArrowNamed finds `const <text> = <arrow>` at the top level
// and answers the const's name node and the arrow node.
func templatePinArrowNamed(t *testing.T, p *program.CheckerProgram, text string) (*ast.Node, *ast.Node) {
	t.Helper()
	for _, statement := range p.Entry.Statements.Nodes {
		if !ast.IsVariableStatement(statement) {
			continue
		}
		for _, d := range statement.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes {
			vd := d.AsVariableDeclaration()
			name := vd.Name()
			if name == nil || !ast.IsIdentifier(name) || name.Text() != text || vd.Initializer == nil {
				continue
			}
			initializer := Unwrapped(vd.Initializer)
			if ast.IsArrowFunction(initializer) || ast.IsFunctionExpression(initializer) {
				return name, initializer
			}
		}
	}
	t.Fatalf("no const arrow named %s", text)
	return nil, nil
}

func TestReturnTemplateOverServedCall_CompletesThroughTheSequenceHoist(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	// Go raw strings cannot embed a backtick; "B" swaps in for one.
	source := strings.ReplaceAll(`
		const getClipPathId = (stackId: string, index: number): string => {
			return Brecharts-bar-stack-clip-path-${stackId}-${index}B;
		};
		function urlFor(stackId: string, index: number): string {
			return Burl(#${getClipPathId(stackId, index)})B;
		}
	`, "B", "`")
	p := entryEnvTestProgram(t, source)
	ctx := &FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}
	nameNode, arrow := templatePinArrowNamed(t, p, "getClipPathId")
	symbol := p.Checker.GetSymbolAtLocation(nameNode)
	if symbol == nil {
		t.Fatalf("no symbol for getClipPathId")
	}
	ctx.Contracts[symbol] = &FunctionContract{Declaration: arrow}
	// the callee's own template body: string param spans read exactly,
	// the number span widens to the string root — COMPLETE either way
	if _, blobOk := LowerSummaryBody(ctx, arrow); !blobOk {
		t.Fatalf("getClipPathId's body did not compile a blob — the premise (a servable callee) is gone")
	}
	if outcome, construct, _ := SummaryOutcomeOf(p.Checker, arrow); outcome != SummaryComplete {
		t.Fatalf("getClipPathId: outcome=%q construct=%q, want complete", outcome, construct)
	}
	declaration := entryEnvFunctionNamed(t, p, "urlFor")
	if _, ok := RelowerSummaryBody(ctx, declaration); !ok {
		t.Fatalf("urlFor declined whole")
	}
	outcome, construct, recorded := SummaryOutcomeOf(p.Checker, declaration)
	if !recorded {
		t.Fatalf("no outcome recorded for urlFor")
	}
	if outcome != SummaryComplete {
		t.Errorf("urlFor: outcome=%q construct=%q, want complete — the returned template's call "+
			"substitution must hoist and the concatenation read its temp", outcome, construct)
	}
}
