// Pins for a binding-pattern parameter carrying its OWN type
// annotation, mirroring tmp/recharts-src/src/state/selectors/dataSelectors.ts's
// combine-function shape:
//
//	({ chartData, dataStartIndex, dataEndIndex }: ChartDataState): ChartData =>
//	  chartData != null ? chartData.slice(dataStartIndex, dataEndIndex + 1) : [];
//
// The census (tmp/recharts-trace.txt) names this shape "a binding-pattern
// parameter" three times each in selectors.ts and axisSelectors.ts.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// arrowConstNamed finds a top-level `const <text> = <arrow>;`
// declaration's initializer arrow — the const-assigned-arrow twin of
// entryEnvFunctionNamed (entry_env_test.go), for the createSelector
// combine-function shape which is never a FunctionDeclaration.
func arrowConstNamed(t *testing.T, statements []*ast.Node, text string) *ast.Node {
	t.Helper()
	for _, statement := range statements {
		if !ast.IsVariableStatement(statement) {
			continue
		}
		for _, declaration := range statement.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes {
			vd := declaration.AsVariableDeclaration()
			name := vd.Name()
			if name == nil || !ast.IsIdentifier(name) || name.Text() != text {
				continue
			}
			if vd.Initializer != nil && ast.IsArrowFunction(vd.Initializer) {
				return vd.Initializer
			}
		}
	}
	t.Fatalf("no const arrow named %s", text)
	return nil
}

// TestSummaryParameterEntries_AnnotatedBindingPatternArrowArgumentCompletes
// pins dataSelectors.ts's combine-function shape: a destructured
// parameter that carries ITS OWN type annotation (`: ChartDataState`),
// used as a plain arrow expression (not a reduce/array callback, so
// parameterSorts is never built for it — lowerArrowSummary's whole
// per-position-site-sort route is specific to the collection-callback
// conversion machinery, which createSelector's combine argument never
// goes through). Before this pin: SummaryParameterEntriesIn's pattern
// arm reads pd.Type directly through recordParamMembersIn regardless of
// call shape, so this should already complete — the pin is here to keep
// it that way and to document the boundary against the truly-blocked
// unannotated-pattern shape (TestSummaryParameterEntries_
// UnannotatedBindingPatternArrowArgumentDeclines below).
func TestSummaryParameterEntries_AnnotatedBindingPatternArrowArgumentCompletes(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
		type ChartDataState = {
			chartData: number[];
			dataStartIndex: number;
			dataEndIndex: number;
		};
		const combine = ({ chartData, dataStartIndex, dataEndIndex }: ChartDataState): number => {
			if (chartData.length > 0) {
				return dataStartIndex;
			}
			return dataEndIndex;
		};
	`)
	arrow := arrowConstNamed(t, p.Entry.Statements.Nodes, "combine")
	_, ok := RelowerSummaryBody(&FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}, arrow)
	outcome, construct, recorded := SummaryOutcomeOf(p.Checker, arrow)
	if !ok {
		t.Fatalf("combine declined to lower (outcome %q, construct %q, recorded %v)", outcome, construct, recorded)
	}
	if !recorded {
		t.Fatalf("no outcome recorded for combine")
	}
	if outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q), want SummaryComplete — the pattern carries its own annotation", outcome, construct)
	}
}

// TestSummaryParameterEntries_DefaultedMemberInsideAnnotatedPatternCompletes
// pins axisSelectors.ts's combineAllAppliedValues shape: a binding
// pattern whose OWN annotation is present, but one bound member carries
// a DEFAULT (`{ chartData = [], dataStartIndex, dataEndIndex }:
// ChartDataState`). SummaryParameterEntriesIn's pattern arm now binds
// the defaulted element as an unknown-sorted TOP-filled entry
// (bodySlot.TopEntry) instead of refusing the whole pattern — the
// bound value is member-or-default, so a definite member state may not
// fill it, and TOP is what the entry quantifier already covers.
func TestSummaryParameterEntries_DefaultedMemberInsideAnnotatedPatternCompletes(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
		type ChartDataState = {
			chartData: number[];
			dataStartIndex: number;
			dataEndIndex: number;
		};
		const combine = ({ chartData = [], dataStartIndex, dataEndIndex }: ChartDataState): number => {
			if (chartData.length > 0) {
				return dataStartIndex;
			}
			return dataEndIndex;
		};
	`)
	arrow := arrowConstNamed(t, p.Entry.Statements.Nodes, "combine")
	_, ok := RelowerSummaryBody(&FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}, arrow)
	outcome, construct, recorded := SummaryOutcomeOf(p.Checker, arrow)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	if !ok || outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q, ok %v), want SummaryComplete — the defaulted element binds as an unknown-sorted TOP entry", outcome, construct, ok)
	}
}

// TestSummaryParameterEntries_ReduceElementPatternEndToEndCompletes pins
// Text.tsx's calculate shape end-to-end through RelowerSummaryBody:
// `words.reduce((result: Array<W>, { word, width }) => {...}, [])`
// returned directly. The LAYOUT wall this agent's family exists to
// close (summaryParameterEntries' arrow-argument gate, this file) is
// now open — TestCallbackConvert_ConvertReduceArrowElementPatternClosesTheLayoutWall
// proves convertReduceArrowElementPattern itself compiles — and the
// return-position routing that calls it (ir_callback_return.go, not
// owned here) is wired, so this body completes end-to-end.
func TestSummaryParameterEntries_ReduceElementPatternEndToEndCompletes(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearPatternLeafSorts()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
		type WordWithComputedWidth = { word: string; width: number };
		type WordsWithWidth = { words: string[]; width: number };
		const calculate = (words: ReadonlyArray<WordWithComputedWidth>): ReadonlyArray<WordsWithWidth> =>
			words.reduce((result: Array<WordsWithWidth>, { word, width }): Array<WordsWithWidth> => {
				result.push({ words: [word], width });
				return result;
			}, []);
	`)
	arrow := arrowConstNamed(t, p.Entry.Statements.Nodes, "calculate")
	_, ok := RelowerSummaryBody(&FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}, arrow)
	outcome, construct, recorded := SummaryOutcomeOf(p.Checker, arrow)
	if !ok {
		t.Fatalf("calculate declined to lower (outcome %q, construct %q, recorded %v)", outcome, construct, recorded)
	}
	if outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q), want SummaryComplete", outcome, construct)
	}
}

// TestCallbackConvert_ReduceElementPatternEntriesDegradesARestElementInsteadOfRefusingThePattern
// pins the REST-in-pattern sub-shape: `{ a, ...rest }` binds "a" (a
// real leaf resolving to "xs.elem.a") and "rest" (no leaf can — it
// stands for "everything else"). Before objectPatternElementBindings'
// widening: any rest element inside the pattern refused the WHOLE
// entry vector, so "a" — a leaf that DOES resolve — went unserved too.
// After: "rest" degrades to the absent entry / unknown sort, and "a"
// still resolves.
func TestCallbackConvert_ReduceElementPatternEntriesDegradesARestElementInsteadOfRefusingThePattern(t *testing.T) {
	context := memberElementCallbackReturnContext(
		"xs", map[string]BindingKind{"a": BindingKindNumber}, nil, nil)
	statements := loweringParse(t, `xs.reduce((acc: number, { a, ...rest }) => acc + a, 0);`)
	call := Unwrapped(statements[0].AsExpressionStatement().Expression)
	reduceSource, _, readOk := reduceCallOf(call)
	if !readOk {
		t.Fatalf("reduceCallOf declined the two-argument reduce")
	}
	accumulator := kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectConst, Set: nonNegativeIntegerSet()}
	entries, _, ok := reduceElementPatternEntries(
		context, reduceSource.Callback, "xs.elem",
		accumulator, BindingKindNumber, TypeofTagNumber,
	)
	if !ok {
		t.Fatalf("reduceElementPatternEntries declined — want the rest element to degrade rather than refuse the whole pattern")
	}
	if len(entries) != 3 {
		t.Fatalf("len(entries) = %d, want 3 — accumulator, a, rest", len(entries))
	}
	wantSlot, found := slotIndexOfName(context, "xs.elem.a")
	if !found {
		t.Fatalf("the harness's own xs.elem.a slot did not resolve")
	}
	if entries[1].Effect.Kind != kernelbridge.LoopEffectVarState || entries[1].Effect.Index != wantSlot {
		t.Errorf("entries[1] (a) = %+v, want a whole-state copy of slot %d", entries[1].Effect, wantSlot)
	}
	if entries[2].Effect.Kind != kernelbridge.LoopEffectConstState || entries[2].Sort != BindingKindUnknown {
		t.Errorf("entries[2] (rest) = %+v, want the absent unknown-sorted entry", entries[2])
	}
}

// TestCallbackConvert_ReduceElementPatternEntriesDegradesADefaultedMemberInsteadOfRefusingThePattern
// pins the DEFAULTED-member-inside-pattern sub-shape: `{ a, b = 0 }`
// binds "a" from a real leaf and "b" from a leaf that exists but whose
// default this leaf-for-leaf reader cannot apply. Before the widening:
// the default on "b" refused "a" too.
func TestCallbackConvert_ReduceElementPatternEntriesDegradesADefaultedMemberInsteadOfRefusingThePattern(t *testing.T) {
	context := memberElementCallbackReturnContext(
		"xs", map[string]BindingKind{"a": BindingKindNumber, "b": BindingKindNumber}, nil, nil)
	statements := loweringParse(t, `xs.reduce((acc: number, { a, b = 0 }) => acc + a, 0);`)
	call := Unwrapped(statements[0].AsExpressionStatement().Expression)
	reduceSource, _, readOk := reduceCallOf(call)
	if !readOk {
		t.Fatalf("reduceCallOf declined the two-argument reduce")
	}
	accumulator := kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectConst, Set: nonNegativeIntegerSet()}
	entries, _, ok := reduceElementPatternEntries(
		context, reduceSource.Callback, "xs.elem",
		accumulator, BindingKindNumber, TypeofTagNumber,
	)
	if !ok {
		t.Fatalf("reduceElementPatternEntries declined — want the defaulted member to degrade rather than refuse the whole pattern")
	}
	if len(entries) != 3 {
		t.Fatalf("len(entries) = %d, want 3 — accumulator, a, b", len(entries))
	}
	wantSlot, found := slotIndexOfName(context, "xs.elem.a")
	if !found {
		t.Fatalf("the harness's own xs.elem.a slot did not resolve")
	}
	if entries[1].Effect.Kind != kernelbridge.LoopEffectVarState || entries[1].Effect.Index != wantSlot {
		t.Errorf("entries[1] (a) = %+v, want a whole-state copy of slot %d", entries[1].Effect, wantSlot)
	}
	if entries[2].Effect.Kind != kernelbridge.LoopEffectConstState || entries[2].Sort != BindingKindUnknown {
		t.Errorf("entries[2] (b, defaulted) = %+v, want the absent unknown-sorted entry", entries[2])
	}
}
