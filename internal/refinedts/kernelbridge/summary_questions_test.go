// The summary questions' wires: what summarize and applySummary send,
// and the pin that keeps a walk question carrying no table spelled
// exactly as it was before summaries existed — every cached walk
// answer is keyed by that wire, so a stray byte would throw the whole
// store away.
package kernelbridge

import (
	"fmt"
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// a stand-in for whatever the kernel's compiled summary looks like: the
// wire holds it verbatim, so the test only needs it to be recognizable.
const aSummary SummaryBlob = `{"arity":1,"steps":[{"eval":{"var":0}}],"out":[1]}`

func TestTheSummarizeWireCarriesArityStatementsAndTheTable(t *testing.T) {
	got := SummarizeWire(2, []IrStatement{
		{
			Kind:   IrStatementAssign,
			Target: 0,
			Effect: LoopEffect{Kind: LoopEffectVar, Index: 1},
		},
	}, []SummaryBlob{aSummary})
	want := fmt.Sprintf(
		`{"arity":2,"stmts":[{"assign":{"target":0,"e":{"var":1}}}],"table":[%s]}`,
		string(aSummary),
	)
	if got != want {
		t.Errorf("SummarizeWire = %q, want %q", got, want)
	}
}

func TestAnEmptyTableStillWiresAsAnEmptyListForSummarize(t *testing.T) {
	got := SummarizeWire(0, nil, nil)
	want := `{"arity":0,"stmts":[],"table":[]}`
	if got != want {
		t.Errorf("SummarizeWire(empty) = %q, want %q", got, want)
	}
}

func TestTheTableSplicesEachSummaryInRaw(t *testing.T) {
	got := TableWire([]SummaryBlob{aSummary, `{"arity":0,"steps":[],"out":[]}`})
	want := fmt.Sprintf(`[%s,{"arity":0,"steps":[],"out":[]}]`, string(aSummary))
	if got != want {
		t.Errorf("TableWire = %q, want %q", got, want)
	}
}

func TestTheApplySummaryWireSplicesTheBlobBesideTheEntryStates(t *testing.T) {
	got := ApplySummaryWire(aSummary, []KnownStateWire{
		{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{7}))},
		{Top: true},
	})
	want := fmt.Sprintf(
		`{"summary":%s,"entries":[{"set":{"forms":[{"form":"oneOf","w":[{"num":7,"exp":0}]}]},"undef":false,"null":false,"nan":false},{"top":true}]}`,
		string(aSummary),
	)
	if got != want {
		t.Errorf("ApplySummaryWire = %q, want %q", got, want)
	}
}

func TestAWalkQuestionWithNoTableWritesNoTableFieldAtAll(t *testing.T) {
	if got := TableField(nil); got != "" {
		t.Errorf("TableField(nil) = %q, want the empty string", got)
	}
	if got := TableField([]SummaryBlob{}); got != "" {
		t.Errorf("TableField(empty) = %q, want the empty string", got)
	}
}

func TestAWalkQuestionWithATableAppendsItAfterTheStatements(t *testing.T) {
	got := TableField([]SummaryBlob{aSummary})
	want := fmt.Sprintf(`,"table":[%s]`, string(aSummary))
	if got != want {
		t.Errorf("TableField = %q, want %q", got, want)
	}
}

// The walk question's own wire, rebuilt from the shared encoders — the
// spelling kernel_asks.go sends, held here so the zero-table form is
// pinned without a kernel to ask.
func walkWire(states []KnownStateWire, stmts []IrStatement, table []SummaryBlob) string {
	return fmt.Sprintf(
		`{"states":[%s],"stmts":[%s]%s}`,
		joinComma(StateWires(states)), joinComma(StmtWires(stmts)), TableField(table),
	)
}

func TestTheZeroTableWalkWireIsUnchangedFromBeforeSummariesExisted(t *testing.T) {
	states := []KnownStateWire{{Top: true}}
	stmts := []IrStatement{
		{Kind: IrStatementAssign, Target: 0, Effect: LoopEffect{Kind: LoopEffectVar, Index: 0}},
	}
	got := walkWire(states, stmts, nil)
	want := `{"states":[{"top":true}],"stmts":[{"assign":{"target":0,"e":{"var":0}}}]}`
	if got != want {
		t.Errorf("walk wire (no table) = %q, want %q", got, want)
	}
}

func TestTheWalkWireGainsTheTableWhenSummariesRideAlong(t *testing.T) {
	got := walkWire([]KnownStateWire{{Top: true}}, nil, []SummaryBlob{aSummary})
	want := fmt.Sprintf(`{"states":[{"top":true}],"stmts":[],"table":[%s]}`, string(aSummary))
	if got != want {
		t.Errorf("walk wire (with table) = %q, want %q", got, want)
	}
}

func TestASummaryAnswerIsCapturedWholeWithoutBeingRead(t *testing.T) {
	raw := `{"arity":1,"steps":[{"cutT":{"test":"defined","src":0}}],"out":[1]}`
	if got := DecodeSummaryBlob(raw); string(got) != raw {
		t.Errorf("DecodeSummaryBlob = %q, want the answer verbatim %q", string(got), raw)
	}
}

func TestApplySummaryDecodesTheSameStateListTheWalkAnswers(t *testing.T) {
	parsed := Answered(`{"states":[{"top":true},{"set":{"forms":[{"form":"integer"}]},"absent":true,"nan":false}]}`)
	states := DecodeWalkStates(parsed, "applySummary")
	if len(states) != 2 {
		t.Fatalf("len(states) = %d, want 2", len(states))
	}
	if !states[0].Top {
		t.Errorf("states[0].Top = false, want true")
	}
	if states[1].Top || !states[1].Undef || !states[1].Null {
		t.Errorf("states[1] = %+v, want a set with the absent flag up", states[1])
	}
}
