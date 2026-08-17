// Pins importedHookCallStatement: a call through a bare imported
// identifier lowers havoc-free wherever every argument is
// writeAndCallFree or a write-free function literal, and falls back to
// the ordinary havoc floor the moment an argument writes a tracked
// local. crossFileConstructedProgram (cross_file_constructed_test.go)
// is the two-file program recipe this file reuses — the callee here
// must resolve through an ACTUAL import, not a same-file binding, for
// resolvesOutsideThisFile to answer true.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
)

// importedHookLoweringContext is a LoweringContext wired to a real
// checker program, its own ResolveCallee mirroring the production
// wiring (ir_summary_body_lowering.go): ContractOf, so a same-file
// local always resolves ahead of this recognizer, exactly as the
// production door tries the blob tier first.
func importedHookLoweringContext(p *program.CheckerProgram, bindings []string, sorts []BindingKind, typeofs []TypeofTag) *LoweringContext {
	flow := &FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}
	return &LoweringContext{
		Bindings:     bindings,
		Sorts:        sorts,
		Typeofs:      typeofs,
		Flow:         flow,
		SummaryTable: &SummaryTableBuilder{},
		ResolveCallee: func(callee *ast.Node) *ast.Node {
			if called := ContractOf(flow, callee); called != nil {
				return called.Declaration
			}
			return nil
		},
	}
}

// importedHookFirstStatement is the first statement of the ENTRY
// file's first function/arrow body reachable from a top-level
// `export function main() { … }` — the shape every pin below declares
// its body under, so the call under test is always statement 0.
func importedHookFirstStatement(t *testing.T, p *program.CheckerProgram) *ast.Node {
	t.Helper()
	for _, statement := range p.Entry.Statements.Nodes {
		if ast.IsFunctionDeclaration(statement) {
			name := statement.AsFunctionDeclaration().Name()
			if name != nil && ast.IsIdentifier(name) && name.Text() == "main" {
				body := statement.Body()
				if body == nil || len(body.AsBlock().Statements.Nodes) == 0 {
					t.Fatalf("main has no statements")
				}
				return body.AsBlock().Statements.Nodes[0]
			}
		}
	}
	t.Fatalf("no function named main in the entry source")
	return nil
}

/* ── pin (a): a bare imported identifier's result is unknown, COMPLETE ── */

func TestImportedHookCallStatement_AnImportedHooksResultIsUnknownAndTheBodyCompletes(t *testing.T) {
	p := crossFileConstructedProgram(t,
		"export function useThing(): number { return 1; }\n",
		"import { useThing } from \"./helper.ts\";\n"+
			"export function main() {\n"+
			"  const v = useThing();\n"+
			"  return v ? 1 : 0;\n"+
			"}\n")
	context := importedHookLoweringContext(p,
		[]string{"v", "#done", "#ret"},
		[]BindingKind{BindingKindUnknown, BindingKindNumber, BindingKindUnknown},
		[]TypeofTag{TypeofTagNone, TypeofTagNumber, TypeofTagNone})
	statement := importedHookFirstStatement(t, p)
	lowered, ok := SummaryCallStatementOf(context, statement)
	if !ok {
		t.Fatalf("an imported hook call with no arguments declined whole")
	}
	if len(lowered) != 1 || lowered[0].Kind != kernelbridge.IrStatementAssign ||
		lowered[0].Target != 0 || lowered[0].Effect.Kind != kernelbridge.LoopEffectUnknown {
		t.Errorf("lowered = %+v, want one `v := unknown` and nothing else havocked", lowered)
	}
}

/* ── pin (b): an inline-arrow argument that WRITES a tracked local declines ── */

func TestImportedHookCallStatement_AWritingInlineArrowArgumentDeclinesAndTheHavocFloorStands(t *testing.T) {
	p := crossFileConstructedProgram(t,
		"export function useThing(cb: () => number): number { return cb(); }\n",
		"import { useThing } from \"./helper.ts\";\n"+
			"export function main() {\n"+
			"  let total = 0;\n"+
			"  const v = useThing(() => { total = total + 1; return total; });\n"+
			"  return v;\n"+
			"}\n")
	context := importedHookLoweringContext(p,
		[]string{"total", "v", "#done", "#ret"},
		[]BindingKind{BindingKindNumber, BindingKindUnknown, BindingKindNumber, BindingKindUnknown},
		[]TypeofTag{TypeofTagNumber, TypeofTagNone, TypeofTagNumber, TypeofTagNone})
	// the recognizer itself must decline directly — this is the negative
	// the soundness pin cares about, not merely "some route lowers it"
	var call *ast.Node
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if call != nil || node == nil {
			return true
		}
		if ast.IsCallExpression(node) && ast.IsIdentifier(Unwrapped(node.AsCallExpression().Expression)) &&
			Unwrapped(node.AsCallExpression().Expression).Text() == "useThing" {
			call = node
			return true
		}
		node.ForEachChild(visit)
		return false
	}
	visit(p.Entry.AsNode())
	if call == nil {
		t.Fatalf("no useThing(...) call found in the entry source")
	}
	if _, ok := importedHookCallStatement(context, call, 1); ok {
		t.Fatalf("importedHookCallStatement served a call whose only argument WRITES a tracked local (total) — unsound")
	}
	// and the old floor must still answer the site: SummaryCallOrHavoc
	// falls through to the opaque tier, which havocs `total` alongside
	// the unknown result
	lowered, ok := SummaryCallOrHavoc(context, call, 1)
	if !ok {
		t.Fatalf("the call site declined whole once the recognizer stepped aside")
	}
	havockedTotal := false
	resultUnknown := false
	for _, statement := range lowered {
		if statement.Kind != kernelbridge.IrStatementAssign || statement.Effect.Kind != kernelbridge.LoopEffectUnknown {
			continue
		}
		if statement.Target == 0 {
			havockedTotal = true
		}
		if statement.Target == 1 {
			resultUnknown = true
		}
	}
	if !havockedTotal {
		t.Errorf("lowered = %+v, want total's slot 0 havocked — the closure writes it", lowered)
	}
	if !resultUnknown {
		t.Errorf("lowered = %+v, want v's slot 1 havocked unknown", lowered)
	}
}

/* ── pin (c): bare-statement position is COMPLETE, with no statement needed ── */

func TestImportedHookCallStatement_ABareStatementCallLowersToNothing(t *testing.T) {
	p := crossFileConstructedProgram(t,
		"export function useThing(): number { return 1; }\n",
		"import { useThing } from \"./helper.ts\";\n"+
			"export function main() {\n"+
			"  useThing();\n"+
			"  return 0;\n"+
			"}\n")
	context := importedHookLoweringContext(p,
		[]string{"#done", "#ret"},
		[]BindingKind{BindingKindNumber, BindingKindUnknown},
		[]TypeofTag{TypeofTagNumber, TypeofTagNone})
	statement := importedHookFirstStatement(t, p)
	lowered, ok := SummaryCallStatementOf(context, statement)
	if !ok {
		t.Fatalf("a bare imported-hook call statement declined whole")
	}
	if len(lowered) != 0 {
		t.Errorf("lowered = %+v, want no statements at all — nothing here moves a tracked slot", lowered)
	}
}

/* ── the real corpus shape: useAppSelector(selectActiveLabel) ───────── */

func TestImportedHookCallStatement_ABareSelectorArgumentMirrorsTheRechartsShape(t *testing.T) {
	// tmp/recharts-src/src/state/hooks.ts's useActiveTooltipLabel:
	// `return useAppSelector(selectActiveLabel);` — a bare imported
	// function passed BY NAME as the sole argument, no arrow at all
	p := crossFileConstructedProgram(t,
		"export type State = { label: string };\n"+
			"export function useAppSelector<T>(selector: (state: State) => T): T | undefined {\n"+
			"  return selector({ label: \"\" });\n"+
			"}\n"+
			"export function selectActiveLabel(state: State): string {\n"+
			"  return state.label;\n"+
			"}\n",
		"import { useAppSelector, selectActiveLabel } from \"./helper.ts\";\n"+
			"export function main() {\n"+
			"  return useAppSelector(selectActiveLabel);\n"+
			"}\n")
	context := importedHookLoweringContext(p,
		[]string{"#done", "#ret"},
		[]BindingKind{BindingKindNumber, BindingKindUnknown},
		[]TypeofTag{TypeofTagNumber, TypeofTagNone})
	var call *ast.Node
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if call != nil || node == nil {
			return true
		}
		if ast.IsCallExpression(node) {
			call = node
			return true
		}
		node.ForEachChild(visit)
		return false
	}
	visit(p.Entry.AsNode())
	if call == nil {
		t.Fatalf("no call expression found in the entry source")
	}
	lowered, ok := importedHookCallStatement(context, call, 1)
	if !ok {
		t.Fatalf("useAppSelector(selectActiveLabel) declined — a bare imported function argument is write-and-call-free")
	}
	if len(lowered) != 1 || lowered[0].Kind != kernelbridge.IrStatementAssign ||
		lowered[0].Target != 1 || lowered[0].Effect.Kind != kernelbridge.LoopEffectUnknown {
		t.Errorf("lowered = %+v, want one `#ret := unknown` and nothing else havocked", lowered)
	}
}
