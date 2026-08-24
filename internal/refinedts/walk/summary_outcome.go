// The per-body outcome record — the covered-ness instrument.
//
// Every contracted body reports exactly one outcome: `complete` (the
// lowering read every statement, the blob compiled, the kernel serves
// it), `porous` (it lowered, but some construct havocked — the FIRST
// one is named), or `declined` (it did not lower — the first reason is
// named). The trace's per-file detail prints the tally beside
// kernel.ask and the inline count, so "covered" is a read-off rather
// than a claim.
//
// This file only RECORDS. The lowering and the registry decide; they
// call RecordSummaryOutcome at the point the outcome becomes known.

package walk

import (
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/tracing"
)

// SummaryOutcome is what happened to one body's lowering.
type SummaryOutcome string

const (
	// SummaryComplete: the body lowered whole and the kernel serves it.
	SummaryComplete SummaryOutcome = "complete"
	// SummaryPorous: the body lowered, but at least one construct
	// havocked. The named construct is the first one that did.
	SummaryPorous SummaryOutcome = "porous"
	// SummaryDeclined: the body did not lower. The named construct is
	// the first decline reason.
	SummaryDeclined SummaryOutcome = "declined"
)

// summaryOutcomeRank orders the outcomes by how much they say. A later
// record for the same declaration replaces an earlier one only when it
// ranks HIGHER: a body that has already been seen to lower whole is
// never talked back down to declined by a second, less informed pass
// (the fixpoint re-lowers a recursive body, and a re-lowering that
// hits the cycle guard knows less than the settled build did).
func summaryOutcomeRank(outcome SummaryOutcome) int {
	switch outcome {
	case SummaryDeclined:
		return 1
	case SummaryPorous:
		return 2
	case SummaryComplete:
		return 3
	}
	return 0
}

// summaryOutcomeRecord is one body's settled outcome.
type summaryOutcomeRecord struct {
	// Name is the body's own name, as the report spells contracts.
	Name string
	// Outcome is the highest-ranking outcome recorded so far.
	Outcome SummaryOutcome
	// Construct is the first havocked construct (porous) or the first
	// decline reason (declined); empty for complete.
	Construct string
}

// summaryOutcomesMu guards the record store. The outcome is what ONE
// checker's lowering found — the first havocked construct or decline
// reason it read — so the store keys by (checker, declaration): two
// parallel workers' checkers can each be mid-lowering the same
// declaration, and a bare-node key would let whichever one finishes
// first hand its outcome to the other's later reads, on a checker that
// never produced it (the same reasoning summary_registry.go's
// summaryKey carries). One body has one outcome PER CHECKER, however
// many times its lowering is entered for it.
var (
	summaryOutcomesMu sync.Mutex
	summaryOutcomes   = map[summaryKey]summaryOutcomeRecord{}
)

// RecordSummaryOutcome reports what happened to one body's lowering.
// `construct` names the FIRST havocked construct for a porous body or
// the first decline reason for a declined one, and is empty for a
// complete one.
//
// One outcome per declaration: a later record for the same declaration
// overwrites only when it carries MORE information
// (declined < porous < complete), so a re-lowering that knows less
// never downgrades a settled answer. The trace's tally sees each
// declaration once — the record that lands, and every later upgrade,
// bumps the tally through the outcome it moved TO and un-counts the
// one it moved from.
func RecordSummaryOutcome(c *checker.Checker, declaration *ast.Node, name string, outcome SummaryOutcome, construct string) {
	if declaration == nil || summaryOutcomeRank(outcome) == 0 {
		return
	}
	if outcome == SummaryComplete {
		// a complete body names nothing — the field exists for the two
		// outcomes that have something to name
		construct = ""
	}
	key := summaryKey{checker: c, declaration: declaration}
	summaryOutcomesMu.Lock()
	held, had := summaryOutcomes[key]
	if had && summaryOutcomeRank(outcome) <= summaryOutcomeRank(held.Outcome) {
		summaryOutcomesMu.Unlock()
		return
	}
	if name == "" && had {
		name = held.Name
	}
	summaryOutcomes[key] = summaryOutcomeRecord{
		Name:      name,
		Outcome:   outcome,
		Construct: construct,
	}
	summaryOutcomesMu.Unlock()

	// the trace's tally counts BODIES, so an upgrade moves the body out
	// of the outcome it held rather than counting it twice
	if had {
		tracing.MoveSummaryOutcome(string(held.Outcome), held.Construct, string(outcome), construct)
		return
	}
	tracing.NoteSummaryOutcome(string(outcome), construct)
}

// SummaryOutcomeOf answers a declaration's settled outcome, and
// whether one was ever recorded. The serving rule reads this: a
// COMPLETE blob serves unconditionally, a POROUS one keeps the TOP-ret
// decline.
func SummaryOutcomeOf(c *checker.Checker, declaration *ast.Node) (SummaryOutcome, string, bool) {
	if declaration == nil {
		return "", "", false
	}
	summaryOutcomesMu.Lock()
	defer summaryOutcomesMu.Unlock()
	held, had := summaryOutcomes[summaryKey{checker: c, declaration: declaration}]
	if !had {
		return "", "", false
	}
	return held.Outcome, held.Construct, true
}

// SummaryOutcomeTallies is the whole store as counts: how many bodies
// landed in each outcome, and how often each named construct was the
// one that made the difference. Reads the store rather than the trace,
// so it answers the same whether or not tracing is on.
func SummaryOutcomeTallies() (complete int64, porous int64, declined int64, constructs map[string]int64) {
	constructs = map[string]int64{}
	summaryOutcomesMu.Lock()
	defer summaryOutcomesMu.Unlock()
	for _, held := range summaryOutcomes {
		switch held.Outcome {
		case SummaryComplete:
			complete++
		case SummaryPorous:
			porous++
		case SummaryDeclined:
			declined++
		}
		if held.Construct != "" {
			constructs[held.Construct]++
		}
	}
	return complete, porous, declined, constructs
}

// ClearSummaryOutcomes drops every recorded outcome. The store is keyed
// on (checker, declaration) from one program, so a caller that builds a
// new program clears it; the tests clear it between cases.
func ClearSummaryOutcomes() {
	summaryOutcomesMu.Lock()
	summaryOutcomes = map[summaryKey]summaryOutcomeRecord{}
	summaryOutcomesMu.Unlock()
}
