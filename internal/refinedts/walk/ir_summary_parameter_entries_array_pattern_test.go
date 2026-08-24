// Pins for the ARRAY-DESTRUCTURED parameter shapes
// (tmp/recharts-trace.txt, "a binding-pattern parameter" ×3 —
// util/scale/getNiceTickValues.ts's getValidInterval, getNiceTickValues,
// getTickValuesFixedDomain): a plain array pattern parameter
// (`([min, max]: [number, number])`) never reached the pattern arm of
// SummaryParameterEntriesIn before this pass — only ast.IsObjectBindingPattern
// was recognized, so an array pattern fell straight to the trailing
// "pd.Name() == nil || !ast.IsIdentifier" refusal.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
)

// TestSummaryParameterEntries_TupleAnnotatedArrayPatternBindsPositionally
// pins getValidInterval's own shape: a fixed two-element tuple annotation
// binds each element the tuple position's own sort, precisely (not TOP —
// a fixed tuple GUARANTEES the position is present).
func TestSummaryParameterEntries_TupleAnnotatedArrayPatternBindsPositionally(t *testing.T) {
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
		const getValidInterval = ([min, max]: [number, number]): [number, number] => {
			return [min, max];
		};
	`)
	arrow := arrowConstNamed(t, p.Entry.Statements.Nodes, "getValidInterval")
	ctx := &FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}
	entries, ok := SummaryParameterEntriesIn(ctx, arrow.Parameters()[0])
	if !ok {
		t.Fatalf("the tuple-annotated array pattern declined to expand — want two bound entries")
	}
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2 (min, max): %+v", len(entries), entries)
	}
	if entries[0].Name != "min" || entries[0].Sort != BindingKindNumber || entries[0].TopEntry {
		t.Errorf("entries[0] = %+v, want min number-sorted, not a TOP entry", entries[0])
	}
	if entries[1].Name != "max" || entries[1].Sort != BindingKindNumber || entries[1].TopEntry {
		t.Errorf("entries[1] = %+v, want max number-sorted, not a TOP entry", entries[1])
	}
}

// TestSummaryParameterEntries_TupleAnnotatedArrayPatternBodyIsNotRefusedAtTheParameter
// pins the body-level fate: the recorded construct must no longer be the
// parameter's own refusal ("a binding-pattern parameter").
func TestSummaryParameterEntries_TupleAnnotatedArrayPatternBodyIsNotRefusedAtTheParameter(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
		const getValidInterval = ([min, max]: [number, number]): [number, number] => {
			let validMin = min;
			let validMax = max;
			if (min > max) {
				validMin = max;
				validMax = min;
			}
			return [validMin, validMax];
		};
	`)
	arrow := arrowConstNamed(t, p.Entry.Statements.Nodes, "getValidInterval")
	ctx := &FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}
	RelowerSummaryBody(ctx, arrow)
	outcome, construct, recorded := SummaryOutcomeOf(p.Checker, arrow)
	if !recorded {
		t.Fatalf("no outcome recorded — the lowering ran and must report a fate")
	}
	t.Logf("getValidInterval shape: outcome=%q construct=%q", outcome, construct)
	if construct == "a binding-pattern parameter" {
		t.Errorf("construct = %q — the tuple-annotated array pattern must bind its entries rather than refuse", construct)
	}
}

// TestSummaryParameterEntries_ArrayAnnotatedPatternElementsBindTopEntries
// pins the array-annotated case (`number[]`, not a fixed tuple): the
// array may hold fewer elements than the pattern names, so no position
// past index 0 is a promise the annotation makes — bodySlot carries no
// absence flag to spell "present but maybe absent" precisely, so every
// element takes an unknown-sorted TOP-filled entry instead.
func TestSummaryParameterEntries_ArrayAnnotatedPatternElementsBindTopEntries(t *testing.T) {
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
		const firstTwo = ([a, b]: number[]): number => a + b;
	`)
	arrow := arrowConstNamed(t, p.Entry.Statements.Nodes, "firstTwo")
	ctx := &FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}
	entries, ok := SummaryParameterEntriesIn(ctx, arrow.Parameters()[0])
	if !ok {
		t.Fatalf("the array-annotated pattern declined to expand — want two TOP-filled entries")
	}
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2 (a, b): %+v", len(entries), entries)
	}
	for at, entry := range entries {
		if !entry.TopEntry || entry.Sort != BindingKindUnknown {
			t.Errorf("entries[%d] = %+v, want an unknown-sorted TOP entry — the array may be shorter than the pattern names", at, entry)
		}
	}
}

// TestSummaryParameterEntries_ElisionDoesNotShiftLaterTuplePositions pins
// the alignment hazard an elision introduces: the pattern's element INDEX
// (its syntactic position), not its ordinal among BOUND names, is what
// must map to the tuple position's own sort. `([a, , c]: [number, string,
// boolean])` elides position 1 — c binds position 2's sort (boolean,
// which rides the number sort with a boolean typeof tag), never
// position 1's (string), which a naive "next bound name gets the next
// sort" mapping would wrongly assign after skipping the elision.
func TestSummaryParameterEntries_ElisionDoesNotShiftLaterTuplePositions(t *testing.T) {
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
		const pickEnds = ([a, , c]: [number, string, boolean]): number => a;
	`)
	arrow := arrowConstNamed(t, p.Entry.Statements.Nodes, "pickEnds")
	ctx := &FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}
	entries, ok := SummaryParameterEntriesIn(ctx, arrow.Parameters()[0])
	if !ok {
		t.Fatalf("the elided tuple pattern declined to expand — want two bound entries (a, c)")
	}
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2 (a, c — the elision binds no entry): %+v", len(entries), entries)
	}
	if entries[0].Name != "a" || entries[0].Sort != BindingKindNumber || entries[0].TypeofTag != TypeofTagNumber {
		t.Errorf("entries[0] = %+v, want a number-sorted (position 0)", entries[0])
	}
	if entries[1].Name != "c" || entries[1].Sort != BindingKindNumber || entries[1].TypeofTag != TypeofTagBoolean {
		t.Errorf("entries[1] = %+v, want c wearing position 2's sort (boolean, which rides the number sort) — a shifted mapping would wrongly read position 1's string sort instead", entries[1])
	}
}
