// The covered-ness instrument, probed without a program: the store
// keys on declaration IDENTITY alone, so a distinct node pointer per
// body is the whole fixture — no parse, no checker, no kernel.
//
// The report's own formatters are tested from here too (the tracing
// package carries no test file of its own): the per-entry line and the
// global section both have to render sanely with zero records, which
// is the state the tree is in until the lowering wires its call sites.
package walk

import (
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/tracing"
)

// outcomeFixture clears both stores and answers a fresh declaration
// node maker — each call a distinct body.
func outcomeFixture(t *testing.T) func() *ast.Node {
	t.Helper()
	ClearSummaryOutcomes()
	tracing.ClearSummaryOutcomes()
	t.Cleanup(func() {
		ClearSummaryOutcomes()
		tracing.ClearSummaryOutcomes()
	})
	return func() *ast.Node { return &ast.Node{} }
}

func TestSummaryOutcomeRecordsOnce(t *testing.T) {
	body := outcomeFixture(t)
	declaration := body()

	RecordSummaryOutcome(nil, declaration, "load", SummaryPorous, "this.httpAdapter")
	outcome, construct, had := SummaryOutcomeOf(nil, declaration)
	if !had {
		t.Fatalf("SummaryOutcomeOf answered no record")
	}
	if outcome != SummaryPorous {
		t.Errorf("outcome = %q, want %q", outcome, SummaryPorous)
	}
	if construct != "this.httpAdapter" {
		t.Errorf("construct = %q, want %q", construct, "this.httpAdapter")
	}

	complete, porous, declined, _ := SummaryOutcomeTallies()
	if complete != 0 || porous != 1 || declined != 0 {
		t.Errorf("tallies = (%d, %d, %d), want (0, 1, 0)", complete, porous, declined)
	}
}

func TestSummaryOutcomeUpgradesNeverDowngrade(t *testing.T) {
	body := outcomeFixture(t)

	// declined → porous → complete: each record says more than the one
	// before, so each one lands
	upward := body()
	RecordSummaryOutcome(nil, upward, "resolve", SummaryDeclined, "for await")
	RecordSummaryOutcome(nil, upward, "resolve", SummaryPorous, "obj[key]")
	if outcome, construct, _ := SummaryOutcomeOf(nil, upward); outcome != SummaryPorous || construct != "obj[key]" {
		t.Errorf("after the porous record: (%q, %q), want (porous, obj[key])", outcome, construct)
	}
	RecordSummaryOutcome(nil, upward, "resolve", SummaryComplete, "")
	if outcome, construct, _ := SummaryOutcomeOf(nil, upward); outcome != SummaryComplete || construct != "" {
		t.Errorf("after the complete record: (%q, %q), want (complete, \"\")", outcome, construct)
	}

	// complete → porous → declined: each record says LESS, so the
	// settled answer stands
	downward := body()
	RecordSummaryOutcome(nil, downward, "create", SummaryComplete, "")
	RecordSummaryOutcome(nil, downward, "create", SummaryPorous, "opaque call")
	RecordSummaryOutcome(nil, downward, "create", SummaryDeclined, "eval")
	if outcome, construct, _ := SummaryOutcomeOf(nil, downward); outcome != SummaryComplete || construct != "" {
		t.Errorf("after the downgrade attempts: (%q, %q), want (complete, \"\")", outcome, construct)
	}

	// a record equal to the one held is not a change either
	same := body()
	RecordSummaryOutcome(nil, same, "scan", SummaryPorous, "first")
	RecordSummaryOutcome(nil, same, "scan", SummaryPorous, "second")
	if _, construct, _ := SummaryOutcomeOf(nil, same); construct != "first" {
		t.Errorf("construct = %q, want the FIRST one recorded", construct)
	}
}

func TestSummaryOutcomeTalliesCountBodies(t *testing.T) {
	body := outcomeFixture(t)

	first, second, third := body(), body(), body()
	RecordSummaryOutcome(nil, first, "a", SummaryComplete, "")
	RecordSummaryOutcome(nil, second, "b", SummaryPorous, "this.container")
	RecordSummaryOutcome(nil, third, "c", SummaryPorous, "this.container")
	RecordSummaryOutcome(nil, body(), "d", SummaryDeclined, "for await")

	// an upgrade moves a body rather than adding one
	RecordSummaryOutcome(nil, second, "b", SummaryComplete, "")

	complete, porous, declined, constructs := SummaryOutcomeTallies()
	if complete != 2 || porous != 1 || declined != 1 {
		t.Errorf("tallies = (%d, %d, %d), want (2, 1, 1)", complete, porous, declined)
	}
	if constructs["this.container"] != 1 {
		t.Errorf("constructs[this.container] = %d, want 1", constructs["this.container"])
	}
	if constructs["for await"] != 1 {
		t.Errorf("constructs[for await] = %d, want 1", constructs["for await"])
	}

	// the trace's own tally is the same read — the report prints this one
	traced := tracing.SummaryOutcomeTally()
	if traced.Complete != 2 || traced.Porous != 1 || traced.Declined != 1 {
		t.Errorf("traced tally = (%d, %d, %d), want (2, 1, 1)",
			traced.Complete, traced.Porous, traced.Declined)
	}
	if traced.Total() != 4 {
		t.Errorf("traced total = %d, want 4", traced.Total())
	}
	// the upgraded body left "this.container" behind with it
	if traced.Constructs["this.container"] != 1 {
		t.Errorf("traced constructs[this.container] = %d, want 1",
			traced.Constructs["this.container"])
	}
}

func TestSummaryOutcomeHistogramRanks(t *testing.T) {
	body := outcomeFixture(t)
	for range 3 {
		RecordSummaryOutcome(nil, body(), "x", SummaryPorous, "this.container")
	}
	for range 2 {
		RecordSummaryOutcome(nil, body(), "y", SummaryPorous, "obj[key]")
	}
	RecordSummaryOutcome(nil, body(), "z", SummaryDeclined, "eval")

	tally := tracing.SummaryOutcomeTally()
	named := tally.TopConstructs(2)
	if len(named) != 2 {
		t.Fatalf("len(TopConstructs(2)) = %d, want 2", len(named))
	}
	if named[0].Name != "this.container" || named[0].Count != 3 {
		t.Errorf("top construct = (%q, %d), want (this.container, 3)", named[0].Name, named[0].Count)
	}
	if named[1].Name != "obj[key]" || named[1].Count != 2 {
		t.Errorf("second construct = (%q, %d), want (obj[key], 2)", named[1].Name, named[1].Count)
	}
	if all := tally.TopConstructs(0); len(all) != 3 {
		t.Errorf("len(TopConstructs(0)) = %d, want every name (3)", len(all))
	}
}

func TestSummaryOutcomeIgnoresNothingToRecord(t *testing.T) {
	body := outcomeFixture(t)

	RecordSummaryOutcome(nil, nil, "nowhere", SummaryComplete, "")
	RecordSummaryOutcome(nil, body(), "unnamed", SummaryOutcome("guessed"), "")

	complete, porous, declined, constructs := SummaryOutcomeTallies()
	if complete+porous+declined != 0 {
		t.Errorf("tallies = (%d, %d, %d), want every one zero", complete, porous, declined)
	}
	if len(constructs) != 0 {
		t.Errorf("len(constructs) = %d, want 0", len(constructs))
	}
	traced := tracing.SummaryOutcomeTally()
	if traced.Total() != 0 {
		t.Errorf("traced total = %d, want 0", traced.Total())
	}
}

func TestSummaryOutcomeReportRendersWithNoRecords(t *testing.T) {
	outcomeFixture(t)

	// the per-entry row omits itself when the entry reported nothing —
	// the same way the join row does
	if line, ok := tracing.SummaryOutcomeLineFor(&tracing.SummaryOutcomeCounts{}); ok {
		t.Errorf("the per-entry line rendered with no records: %q", line)
	}
	// and the global section prints nothing at all
	empty := tracing.SummaryOutcomeTally()
	if text := tracing.SummaryOutcomeSectionText(empty); text != "" {
		t.Errorf("the global section rendered with no records:\n%s", text)
	}
}

func TestSummaryOutcomeReportNamesConstructs(t *testing.T) {
	body := outcomeFixture(t)
	for range 15 {
		RecordSummaryOutcome(nil, body(), "whole", SummaryComplete, "")
	}
	RecordSummaryOutcome(nil, body(), "porous1", SummaryPorous, "this.httpAdapter")
	RecordSummaryOutcome(nil, body(), "porous2", SummaryPorous, "wrapper.instance")

	tally := tracing.SummaryOutcomeTally()
	line, ok := tracing.SummaryOutcomeLineFor(&tally)
	if !ok {
		t.Fatalf("the per-entry line omitted itself with 17 records")
	}
	for _, want := range []string{"summaries", "complete=15", "porous=2", "declined=0", "this.httpAdapter"} {
		if !strings.Contains(line, want) {
			t.Errorf("the per-entry line %q does not carry %q", line, want)
		}
	}

	text := tracing.SummaryOutcomeSectionText(tally)
	for _, want := range []string{"summary coverage", "complete", "porous", "declined", "this.httpAdapter", "wrapper.instance"} {
		if !strings.Contains(text, want) {
			t.Errorf("the global section does not carry %q:\n%s", want, text)
		}
	}
}
