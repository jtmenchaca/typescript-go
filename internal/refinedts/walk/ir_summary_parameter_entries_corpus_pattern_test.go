// Pins for the corpus binding-pattern parameter shapes
// (tmp/recharts-trace.txt, "a binding-pattern parameter"):
//
//   - dataSelectors.ts's combine arrows — a pattern annotated with the
//     CROSS-FILE alias ChartDataState whose members include union
//     annotations (`ChartData | undefined`, `unknown | undefined`).
//     This shape already expands; the pins keep it expanding.
//   - PolarUtils.ts's getAngleOfPoint/formatAngleOfSector — a pattern
//     annotated with `PolarViewBoxRequired`, an ALIAS of
//     `Required<PolarViewBox>`. The syntax route's alias arm refuses an
//     applied reference behind a name, so recordParamMembersIn falls
//     back to the checker's own instantiation
//     (instantiatedReferenceMembersOf) for a no-arguments reference.
//   - the whole-param-defaulted record (`({ a, b } = {})`): every bound
//     name takes an unknown-sorted TOP-filled entry (bodySlot.TopEntry)
//     instead of the parameter refusing as "a defaulted parameter".
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
)

// corpusChartDataHelperSource is chartDataSlice.ts's shape: the generic
// ChartData alias (with a default) and the ChartDataState literal alias
// whose members carry union annotations.
const corpusChartDataHelperSource = `
export type ChartData<DataPointType = unknown> = ReadonlyArray<DataPointType>;
export type ChartDataState = {
  chartData: ChartData | undefined;
  computedData: unknown | undefined;
  dataStartIndex: number;
  dataEndIndex: number;
};
`

// TestSummaryParameterEntries_CrossFileUnionMemberPatternExpands pins
// the parameter expansion alone: the pattern binds chartData
// (unknown-sorted — its member annotation is a union), dataStartIndex
// and dataEndIndex (number-sorted), across the import edge.
func TestSummaryParameterEntries_CrossFileUnionMemberPatternExpands(t *testing.T) {
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	mainSource := `
import { ChartData, ChartDataState } from "./helper.ts";
const combine = ({ chartData, dataStartIndex, dataEndIndex }: ChartDataState): ChartData =>
  chartData != null ? chartData.slice(dataStartIndex, dataEndIndex + 1) : [];
`
	p := crossFileConstructedProgram(t, corpusChartDataHelperSource, mainSource)
	arrow := arrowConstNamed(t, p.Entry.Statements.Nodes, "combine")
	ctx := &FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}
	entries, ok := SummaryParameterEntriesIn(ctx, arrow.Parameters()[0])
	if !ok {
		t.Fatalf("the annotated pattern declined to expand — want three bound entries")
	}
	if len(entries) != 3 {
		t.Fatalf("len(entries) = %d, want 3 (chartData, dataStartIndex, dataEndIndex): %+v", len(entries), entries)
	}
	if entries[0].Name != "chartData" || entries[0].Sort != BindingKindUnknown {
		t.Errorf("entries[0] = %+v, want chartData unknown-sorted (its member annotation is a union)", entries[0])
	}
	if entries[1].Name != "dataStartIndex" || entries[1].Sort != BindingKindNumber {
		t.Errorf("entries[1] = %+v, want dataStartIndex number-sorted", entries[1])
	}
	if entries[2].Name != "dataEndIndex" || entries[2].Sort != BindingKindNumber {
		t.Errorf("entries[2] = %+v, want dataEndIndex number-sorted", entries[2])
	}
}

// TestSummaryParameterEntries_CrossFileUnionMemberPatternBodyIsNotRefusedAtTheParameter
// pins the body-level fate of the same arrow: whatever the body's own
// statements do, the recorded construct must no longer be the
// parameter's own refusal ("a binding-pattern parameter").
func TestSummaryParameterEntries_CrossFileUnionMemberPatternBodyIsNotRefusedAtTheParameter(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	mainSource := `
import { ChartData, ChartDataState } from "./helper.ts";
const combine = ({ chartData, dataStartIndex, dataEndIndex }: ChartDataState): ChartData =>
  chartData != null ? chartData.slice(dataStartIndex, dataEndIndex + 1) : [];
`
	p := crossFileConstructedProgram(t, corpusChartDataHelperSource, mainSource)
	arrow := arrowConstNamed(t, p.Entry.Statements.Nodes, "combine")
	ctx := &FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}
	RelowerSummaryBody(ctx, arrow)
	outcome, construct, recorded := SummaryOutcomeOf(p.Checker, arrow)
	if !recorded {
		t.Fatalf("no outcome recorded — the lowering ran and must report a fate")
	}
	t.Logf("corpus combine shape: outcome=%q construct=%q", outcome, construct)
	if construct == "a binding-pattern parameter" {
		t.Errorf("construct = %q — the annotated pattern must bind its entries rather than refuse", construct)
	}
}

// corpusPolarHelperSource is util/types.ts's shape for the PolarUtils
// bodies: Coordinate (a plain interface) and PolarViewBoxRequired — an
// ALIAS of Required<PolarViewBox>, the applied-utility-type-behind-a-
// name shape the census's three selectors.ts "a binding-pattern
// parameter" rows decline on.
const corpusPolarHelperSource = `
export interface Coordinate {
  x: number;
  y: number;
}
export interface PolarViewBox {
  cx?: number;
  cy?: number;
  innerRadius?: number;
  outerRadius?: number;
  startAngle?: number;
  endAngle?: number;
  clockWise?: boolean;
}
export type PolarViewBoxRequired = Required<PolarViewBox>;
`

// TestSummaryParameterEntries_RequiredAliasPatternExpands pins the
// PolarUtils formatAngleOfSector shape: a pattern annotated with an
// alias of Required<PolarViewBox> binds its elements number-sorted —
// Required strips each depth-1 member's optionality, and the checker's
// instantiation is what reads the alias's applied reference.
func TestSummaryParameterEntries_RequiredAliasPatternExpands(t *testing.T) {
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	mainSource := `
import { PolarViewBoxRequired } from "./helper.ts";
export const formatAngleOfSector = ({ startAngle, endAngle }: PolarViewBoxRequired): number => {
  const startCnt = Math.floor(startAngle / 360);
  return endAngle - startCnt * 360;
};
`
	p := crossFileConstructedProgram(t, corpusPolarHelperSource, mainSource)
	arrow := arrowConstNamed(t, p.Entry.Statements.Nodes, "formatAngleOfSector")
	ctx := &FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}
	entries, ok := SummaryParameterEntriesIn(ctx, arrow.Parameters()[0])
	if !ok {
		t.Fatalf("the Required-alias pattern declined to expand — want two bound entries")
	}
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2 (startAngle, endAngle): %+v", len(entries), entries)
	}
	if entries[0].Name != "startAngle" || entries[0].Sort != BindingKindNumber {
		t.Errorf("entries[0] = %+v, want startAngle number-sorted — Required strips the optionality", entries[0])
	}
	if entries[1].Name != "endAngle" || entries[1].Sort != BindingKindNumber {
		t.Errorf("entries[1] = %+v, want endAngle number-sorted", entries[1])
	}
}

// TestSummaryParameterEntries_RequiredAliasSecondPatternBodyIsNotRefusedAtTheParameter
// pins the getAngleOfPoint shape body-level: TWO pattern parameters,
// the second annotated with the Required alias — whatever the body's
// own statements do, the recorded construct must no longer be the
// parameter's refusal.
func TestSummaryParameterEntries_RequiredAliasSecondPatternBodyIsNotRefusedAtTheParameter(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	mainSource := `
import { Coordinate, PolarViewBoxRequired } from "./helper.ts";
export const getAngleOfPoint = ({ x, y }: Coordinate, { cx, cy }: PolarViewBoxRequired): number => {
  return (x - cx) * (x - cx) + (y - cy) * (y - cy);
};
`
	p := crossFileConstructedProgram(t, corpusPolarHelperSource, mainSource)
	arrow := arrowConstNamed(t, p.Entry.Statements.Nodes, "getAngleOfPoint")
	ctx := &FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}
	RelowerSummaryBody(ctx, arrow)
	outcome, construct, recorded := SummaryOutcomeOf(p.Checker, arrow)
	if !recorded {
		t.Fatalf("no outcome recorded — the lowering ran and must report a fate")
	}
	t.Logf("getAngleOfPoint shape: outcome=%q construct=%q", outcome, construct)
	if construct == "a binding-pattern parameter" {
		t.Errorf("construct = %q — both patterns must bind their entries rather than refuse", construct)
	}
}

// TestSummaryParameterEntries_NestedMemberElementBindsTopEntry pins
// ChartUtils.ts's getBaseValueOfBar shape (axisSelectors.ts's remaining
// census rows): a pattern element whose annotation member expands to
// NESTED leaves (`{ numericAxis }: { numericAxis: BaseAxisWithScale }`)
// has no depth-1 row to bind from — the flat entry vector cannot carry
// its leaf paths — so the element binds an unknown-sorted TOP entry
// instead of refusing the whole pattern.
func TestSummaryParameterEntries_NestedMemberElementBindsTopEntry(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
		type RechartsScale = { bandwidth: number; rangeMin: number };
		type BaseAxisWithScale = { scale: RechartsScale; axisType: string };
		const getBaseValueOfBar = ({ numericAxis }: { numericAxis: BaseAxisWithScale }): number => {
			return numericAxis == null ? 0 : 1;
		};
	`)
	arrow := arrowConstNamed(t, p.Entry.Statements.Nodes, "getBaseValueOfBar")
	ctx := &FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}
	entries, ok := SummaryParameterEntriesIn(ctx, arrow.Parameters()[0])
	if !ok {
		t.Fatalf("the nested-member pattern declined to expand — want one TOP-filled entry")
	}
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1 (numericAxis): %+v", len(entries), entries)
	}
	if entries[0].Name != "numericAxis" || !entries[0].TopEntry || entries[0].Sort != BindingKindUnknown {
		t.Errorf("entries[0] = %+v, want numericAxis as an unknown-sorted TOP entry", entries[0])
	}
	RelowerSummaryBody(ctx, arrow)
	outcome, construct, recorded := SummaryOutcomeOf(p.Checker, arrow)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	t.Logf("nested-member element: outcome=%q construct=%q", outcome, construct)
	if construct == "a binding-pattern parameter" {
		t.Errorf("construct = %q — the nested-member element binds a TOP entry rather than refusing", construct)
	}
}

// TestSummaryParameterEntries_WholeParamDefaultedRecordBindsTopEntries
// pins the whole-param-defaulted record: `({ a, b } = {})` binds each
// name as an unknown-sorted TOP-filled entry — the bound value is
// argument-member-or-default-object-member, which no member state
// spells, and TOP claims nothing — instead of the parameter refusing
// as "a defaulted parameter".
func TestSummaryParameterEntries_WholeParamDefaultedRecordBindsTopEntries(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
		function f({ a, b }: { a?: number; b?: number } = {}): number {
			if (a != null) {
				return 1;
			}
			return 0;
		}
	`)
	declaration := entryEnvFunctionNamed(t, p, "f")
	ctx := &FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}
	entries, ok := SummaryParameterEntriesIn(ctx, declaration.Parameters()[0])
	if !ok {
		t.Fatalf("the defaulted whole pattern declined to expand — want two TOP-filled entries")
	}
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2 (a, b): %+v", len(entries), entries)
	}
	for at, entry := range entries {
		if !entry.TopEntry || entry.Sort != BindingKindUnknown {
			t.Errorf("entries[%d] = %+v, want an unknown-sorted TOP entry — no member state spells member-or-default", at, entry)
		}
	}
	RelowerSummaryBody(ctx, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(p.Checker, declaration)
	if !recorded {
		t.Fatalf("no outcome recorded — the lowering ran and must report a fate")
	}
	t.Logf("defaulted whole pattern: outcome=%q construct=%q", outcome, construct)
	if construct == "a defaulted parameter" {
		t.Errorf("construct = %q — the defaulted pattern binds TOP entries rather than refusing", construct)
	}
}
