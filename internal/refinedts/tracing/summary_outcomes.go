// The covered-ness record: one outcome per contracted body, counted
// per entry file and globally.
//
// The summary machinery (walk/summary_outcome.go) decides the outcome
// — complete, porous, or declined — and one record lands here for each
// body. This file only counts: it never decides, and it holds no AST.
// The per-entry half rides the same goroutine-bound FileDetail channel
// Count uses, so a parallel sweep keeps each entry's tally exact; the
// global half is the aggregate the report prints beside the counters.

package tracing

import (
	"sort"
	"sync"
)

// SummaryOutcomeCounts is one scope's tally: how many bodies landed in
// each outcome, and how often each named construct was the FIRST thing
// that made a body porous or declined.
type SummaryOutcomeCounts struct {
	Complete int64
	Porous   int64
	Declined int64

	// Constructs counts the named construct per outcome — the havocked
	// construct for a porous body, the decline reason for a declined
	// one. Complete bodies name nothing and add nothing here.
	Constructs map[string]int64
}

// Total is how many bodies reported an outcome.
func (c *SummaryOutcomeCounts) Total() int64 {
	if c == nil {
		return 0
	}
	return c.Complete + c.Porous + c.Declined
}

// NamedConstruct is one construct name and how many bodies named it.
type NamedConstruct struct {
	Name  string
	Count int64
}

// TopConstructs ranks the named constructs, most-named first, ties
// broken by name so the report is stable across runs.
func (c *SummaryOutcomeCounts) TopConstructs(limit int) []NamedConstruct {
	if c == nil || len(c.Constructs) == 0 {
		return nil
	}
	out := make([]NamedConstruct, 0, len(c.Constructs))
	for name, count := range c.Constructs {
		out = append(out, NamedConstruct{Name: name, Count: count})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Name < out[j].Name
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// note moves one body's count by delta — +1 when an outcome is
// recorded, -1 when an upgrade takes the body out of the outcome it
// held before, so the tally counts BODIES rather than records.
func (c *SummaryOutcomeCounts) note(outcome string, construct string, delta int64) {
	switch outcome {
	case "complete":
		c.Complete += delta
	case "porous":
		c.Porous += delta
	case "declined":
		c.Declined += delta
	default:
		return
	}
	if construct == "" {
		return
	}
	if c.Constructs == nil {
		if delta < 0 {
			return
		}
		c.Constructs = map[string]int64{}
	}
	held := c.Constructs[construct] + delta
	if held <= 0 {
		delete(c.Constructs, construct)
		return
	}
	c.Constructs[construct] = held
}

// summaryOutcomesMu guards the global tally. The per-entry tallies are
// owned by the goroutine walking that entry (the FileDetail idiom), so
// only this aggregate is shared.
var (
	summaryOutcomesMu sync.Mutex
	summaryOutcomes   SummaryOutcomeCounts
)

// NoteSummaryOutcome records one body's outcome. `outcome` is
// "complete", "porous" or "declined"; `construct` names the first
// havocked construct or the first decline reason, and is empty for a
// complete body. Anything else is ignored rather than counted under a
// name the report cannot explain.
//
// Unlike Count, this records whether or not tracing is on: covered-ness
// is a property of the check, not of the timing run, and the summary
// machinery reports it once per body — a per-declaration cost, not a
// hot-path one. The report reads it only when there is something to
// read.
func NoteSummaryOutcome(outcome string, construct string) {
	moveSummaryOutcome("", "", outcome, construct)
}

// MoveSummaryOutcome replaces a body's earlier outcome with a later,
// better-informed one: the body leaves `from` and joins `to`, so the
// tally still counts it once. An empty `from` is a first record.
func MoveSummaryOutcome(from string, fromConstruct string, to string, toConstruct string) {
	moveSummaryOutcome(from, fromConstruct, to, toConstruct)
}

func moveSummaryOutcome(from string, fromConstruct string, to string, toConstruct string) {
	summaryOutcomesMu.Lock()
	if from != "" {
		summaryOutcomes.note(from, fromConstruct, -1)
	}
	summaryOutcomes.note(to, toConstruct, 1)
	summaryOutcomesMu.Unlock()
	if d := ActiveFileDetail(); d != nil {
		if from != "" {
			d.Summaries.note(from, fromConstruct, -1)
		}
		d.Summaries.note(to, toConstruct, 1)
	}
}

// SummaryOutcomeTally copies the global tally for the report.
func SummaryOutcomeTally() SummaryOutcomeCounts {
	summaryOutcomesMu.Lock()
	defer summaryOutcomesMu.Unlock()
	held := SummaryOutcomeCounts{
		Complete: summaryOutcomes.Complete,
		Porous:   summaryOutcomes.Porous,
		Declined: summaryOutcomes.Declined,
	}
	if len(summaryOutcomes.Constructs) > 0 {
		held.Constructs = make(map[string]int64, len(summaryOutcomes.Constructs))
		for name, count := range summaryOutcomes.Constructs {
			held.Constructs[name] = count
		}
	}
	return held
}

// ClearSummaryOutcomes drops the global tally. Called by ResetRecords
// with the rest of the records.
func ClearSummaryOutcomes() {
	summaryOutcomesMu.Lock()
	summaryOutcomes = SummaryOutcomeCounts{}
	summaryOutcomesMu.Unlock()
}

// NoteSummaryOutcome on a FileDetail is the per-entry half, for a
// caller that holds an entry directly rather than through the
// goroutine binding.
func (d *FileDetail) NoteSummaryOutcome(outcome string, construct string) {
	if d == nil {
		return
	}
	d.Summaries.note(outcome, construct, 1)
}
