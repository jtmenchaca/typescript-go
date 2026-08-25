// from service/check.ts
//
// runRefinements: the raw per-entry driver — passes 1–3 over an entry
// whose shape diagnostics and kernel the caller already holds. Both
// Check/CheckFile's single-entry path and CheckFiles' batch path call
// this directly.

package service

import (
	"sort"
	"time"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/tracing"
	"github.com/microsoft/typescript-go/internal/refinedts/walk"
)

// runRefinements is run's refinement half: passes 1–3 over an entry
// whose shape diagnostics and kernel the caller already holds.
// `factsCache` is nil for a single check; the batch runner hands one
// store across every entry of a sweep (programFactsCached).
func runRefinements(p *program.CheckerProgram, shape []*ast.Diagnostic, kernel *kernelbridge.RefinedTSKernel, factsCache *sweepFactsStore) CheckResult {
	detail := tracing.BeginFileDetail(p.Entry.FileName())
	tracing.BindFileDetail(detail)
	defer tracing.BindFileDetail(nil)
	// this entry's spans nest against THIS goroutine's own stack: the
	// sweep walks entries on several goroutines at once, and one shared
	// span stack credited a worker's elapsed as child time to whatever
	// frame another worker had open (tracing/span_scope.go)
	closeSpanScope := tracing.BeginSpanScope()
	defer closeSpanScope()
	entryStarted := time.Now()
	defer func() {
		tracing.EndFileDetail(detail, float64(time.Since(entryStarted))/float64(time.Millisecond))
	}()

	var refinements []assignability.RefinementDiagnostic
	// one finding per (span, code, message): a correlation pass walks
	// a statement list twice, and a judgment failing in both passes is
	// one finding, not two
	reported := map[string]bool{}
	report := func(d assignability.RefinementDiagnostic) {
		key := reportKey(d)
		if reported[key] {
			return
		}
		reported[key] = true
		refinements = append(refinements, d)
	}

	// the consumer-index prerequisite: every foreign target this
	// check's walk consumes lands here, shared across every ctx copy
	// (topLevelCtx, walkContractBodies' per-body ctx) the same way
	// ReturnSink/ThrowSink already share their pointee across value
	// copies. See CheckResult.ConsumedForeignTargets' own doc comment
	// for why this stays empty until foreign_edge.go grows TargetPath.
	var consumedForeign []string

	// ── passes 1 and 2: per-FILE facts ────────────────────────────
	tFacts := time.Now()
	facts := programFactsCached(p, kernel, factsCache)
	detail.NotePhase("facts", tFacts)
	for _, d := range facts.entryDiagnostics {
		report(d)
	}

	// ── pass 1b: each object's graph is checked as a specification ──
	tObj := time.Now()
	tracing.Span("pass1b.objectGraphs", func() any {
		checkObjectGraphs(p, facts.objects, kernel, report)
		return nil
	}, tracing.GrainStep)
	detail.NotePhase("objectGraphs", tObj)

	// ── pass 3: facts flow; the kernel judges ────────────────────────
	ctx := &walk.FlowContext{
		P:                   p,
		Kernel:              kernel,
		Registry:            facts.registry,
		Objects:             facts.objects,
		Contracts:           facts.contracts,
		Report:              report,
		Aliases:             dataflowfacts.NewAliasClasses(),
		Declared:            map[string]*annotations.DeclaredRefinement{},
		ConsumedForeignSink: &consumedForeign,
	}
	tTop := time.Now()
	tracing.Span("pass3.topLevel", func() any {
		topLevelCtx := *ctx
		// top-level call sites record their environments too — the
		// entry file stands as their owner
		topLevelCtx.SnapshotOwner = p.Entry.AsNode()
		var statements []*ast.Node
		for _, s := range p.Entry.Statements.Nodes {
			if !ast.IsFunctionDeclaration(s) {
				statements = append(statements, s)
			}
		}
		walk.AnalyzeStatements(&topLevelCtx, walk.NewEnv(), statements, nil)
		return nil
	}, tracing.GrainStep)
	detail.NotePhase("topLevel", tTop)

	// each BODY walks once. Bodies walk OUTERMOST-FIRST: an enclosing
	// body's walk records the call-site snapshots its inner functions'
	// call-site joins consume, so the encloser must have walked before
	// the enclosed asks.
	tBodies := time.Now()
	tracing.Span("pass3.contractBodies", func() any {
		walkContractBodies(ctx, p, facts.contracts, kernel, detail)
		return nil
	}, tracing.GrainStep)
	detail.NotePhase("bodies", tBodies)

	sort.SliceStable(refinements, func(i, j int) bool { return refinements[i].Start < refinements[j].Start })
	// newly earned kernel answers persist — theorems survive the
	// process (boundary/kernel.ts)
	tFlush := time.Now()
	tracing.Span("flushQuestionStore", func() any {
		kernelbridge.FlushQuestionStore()
		return nil
	}, tracing.GrainStep)
	detail.NotePhase("flush", tFlush)
	// the TS source's flushSpanLedger (tsgo span asks) has no Go
	// twin — this tree's checker is always in-process, so there is no
	// out-of-process span ledger to flush.

	return CheckResult{Shape: shape, Refinements: refinements, ConsumedForeignTargets: consumedForeign}
}

// reportKey is the TS source's `${d.start}:${d.length}:${d.code}:${d.messageText}`
// dedupe key. Related steps are deliberately NOT part of it: the
// duplicate this collapses is the same judgment reached twice by a
// correlation pass, which computes the same steps both times, so
// keying on them would only turn one finding back into two.
func reportKey(d assignability.RefinementDiagnostic) string {
	return itoa(d.Start) + ":" + itoa(d.Length) + ":" + itoa(d.Code) + ":" + d.MessageText
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
