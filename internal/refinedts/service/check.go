// from service/check.ts
//
// The entry: one source in, tsc's own shape diagnostics plus the
// refinement judgments out. Three passes over the entry file —
// annotation statements compile (and are checked for emptiness),
// function signatures become stated contracts, then facts flow
// through the bodies with the kernel answering every checked
// position. Plain TypeScript with no annotation references anywhere
// is untouched: no judgment, no alert.
//
// Ported to a single-process, single-run shape only:
//
//   - checkFilesParallel/batch_worker.ts (worker-isolate batching) is
//     SKIPPED — Go's answer to parallel checking is tsgo's own
//     checker pool, which CheckFiles' one bulk GetSemanticDiagnostics
//     call already rides; the walk's own parallelization is
//     parallel-sweep-audit.md's queue (walk-scoped package state must
//     move into context first).
//   - programFacts' INCREMENTAL CACHE (annotations/incremental_file_cache.ts)
//     ports as programFactsCached's caller-held store: CheckFiles
//     holds one map for a whole sweep, so each file's facts compile
//     once per sweep; a single CheckFile passes nil and compiles
//     fresh, functionally identical either way.
//   - the tsgo-oracle span/tracing wiring (flushSpanLedger,
//     span/spanAsync's tsgo-specific ledger) has no Go twin — this
//     tree's checker is always in-process (PORT.md's adapter rule);
//     tracing.Span/Count stand in for span()/count() at the call
//     sites that matter.
//   - shape diagnostics: Program.GetSemanticDiagnostics(ctx, entry),
//     direct, per the task's instruction — no parse-only oracle
//     branch.

package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/compiler"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/diagnose"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/narrowing"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/tracing"
	"github.com/microsoft/typescript-go/internal/refinedts/walk"
)

// CheckResult is the TS CheckResult interface.
type CheckResult struct {
	// Shape is tsc's own diagnostics for the entry file — always
	// first.
	Shape []*ast.Diagnostic
	// Refinements are the refinement judgments, in source order.
	Refinements []assignability.RefinementDiagnostic
	// WallMs is this entry's own refinement-walk wall — the honest
	// (untraced) per-file number the CLI's -wall decomposition prints.
	WallMs float64
	// ConsumedForeignTargets: the cross-language target paths (.py
	// files reached through a recognized execFileSync edge) this
	// check's walk consumed — the coordinator's consumer-index
	// prerequisite (docs/one-checker/lsp-coordinator.md build plan
	// item 3), read by a caller that must know which foreign saves
	// should invalidate and re-pull THIS file's diagnostics.
	//
	// ALWAYS EMPTY TODAY: the plumbing (walk.FlowContext.ConsumedForeignSink,
	// wired through runRefinements below) is in place, but nothing pushes
	// into it — ForeignEdgeOutcome (walk/foreign_edge.go) carries no
	// TargetPath field, so the one call site that could populate it
	// (walk/analyze_statement.go's ForeignEdgeAt call) has nothing to
	// record. See ConsumedForeignSink's own doc comment for the exact
	// one-line hook that closes this.
	ConsumedForeignTargets []string
}

// SweepPhases holds the last CheckFiles run's phase walls, summed
// across project groups — the CLI's -wall decomposition reads it. An
// estimate for any fix is its share of the CRITICAL PATH these rows
// spell (program + shape + the busiest checker slot's file chain),
// never its share of process CPU.
var SweepPhases struct {
	ProgramMs float64
	ShapeMs   float64
}

// Check is the TS `check` function: build an in-memory program from a
// source string (surfaceDir names the refined-ts-typescript/surface
// directory to read the surface module from) and run it.
func Check(source string, surfaceDir string) (CheckResult, error) {
	p, err := ProgramFromSource(source, surfaceDir)
	if err != nil {
		return CheckResult{}, err
	}
	// a string-mode program is built fresh per call and never cached
	// (unlike ProgramFromDisk's remembered programs) — its checker
	// lease is released as soon as this run is done
	defer p.Done()
	return run(p), nil
}

// CheckFile is checkFile in the TS source: program mode — check a
// real file on disk. Module resolution runs over the real filesystem;
// the surface is recognized at its own path.
func CheckFile(entryFilePath string, surfacePath string) (CheckResult, error) {
	p, err := ProgramFromDisk(entryFilePath, surfacePath)
	if err != nil {
		return CheckResult{}, err
	}
	return run(p), nil
}

// CheckFiles is checkFiles in the TS source: batch mode — many
// entries over ONE program per covering tsconfig, so program
// construction (parsing, module resolution) is paid once per project
// and each entry still gets exactly its own diagnostics. Two things
// ride tsgo's own machinery rather than the per-entry path:
//
//   - shape diagnostics for the WHOLE group come from one
//     GetSemanticDiagnostics(ctx, nil) call — the same grouped
//     checker-pool parallel path the tsgo CLI's own --noEmit check
//     takes — bucketed per file afterward;
//   - EVERY entry's refinement walk gets its OWN freshly built
//     checker.Checker (checker.NewChecker(p, nil), same constructor
//     the program's internal pool uses) — never a checker another
//     entry has already questioned. A file's verdict is a pure
//     function of the file, its imports, and the kernel; it must never
//     depend on which other files happened to share a checker's
//     accumulated caches first (issue #35). Per-file facts still
//     compile once per sweep through programFactsCached's shared
//     store, keyed by (checker, file) — with a fresh checker per
//     entry, a shared support file's facts now recompile once per
//     entry that reaches it, which is intended: correctness over
//     cross-entry reuse.
//
// The result map is keyed by the caller's own entryPaths spellings; a
// path whose file did not parse into its group's program has no row.
// factsCacheDisabled is the -no-facts-cache diagnostic switch: with it
// set, CheckFiles hands runRefinements no shared facts store, so no
// entry ever consumes facts another worker compiled.
var factsCacheDisabled bool

// SetFactsCacheDisabled is called once by the CLI before any check.
func SetFactsCacheDisabled(disabled bool) { factsCacheDisabled = disabled }

func CheckFiles(entryPaths []string, surfacePath string) map[string]CheckResult {
	results := make(map[string]CheckResult, len(entryPaths))
	if len(entryPaths) == 0 {
		return results
	}
	// one program PER PROJECT: entries grouped by their covering
	// tsconfig — one program with the first entry's options resolving
	// every other package's imports lands on built node_modules types
	// and loses the imported function bodies (the TS source measured
	// the vanished refutations on nest).
	type entryRow struct {
		given    string // the caller's spelling — the result key
		resolved string // absolute — the program's file name
	}
	groups := map[string][]entryRow{}
	var groupOrder []string
	for _, given := range entryPaths {
		resolved, err := filepath.Abs(given)
		if err != nil {
			resolved = given
		}
		key := CoveringProjectCached(resolved).ConfigPath
		if _, seen := groups[key]; !seen {
			groupOrder = append(groupOrder, key)
		}
		groups[key] = append(groups[key], entryRow{given: given, resolved: resolved})
	}
	SweepPhases.ProgramMs = 0
	SweepPhases.ShapeMs = 0
	for _, key := range groupOrder {
		group := groups[key]
		resolved := make([]string, len(group))
		for i, row := range group {
			resolved[i] = row.resolved
		}
		diagnose.Log("check.program.start", "group", key, "entries", len(resolved))
		tProgram := time.Now()
		p := ProgramFromDiskMany(resolved)
		SweepPhases.ProgramMs += float64(time.Since(tProgram)) / float64(time.Millisecond)
		diagnose.Log("check.program.end", "group", key, "entries", len(resolved),
			"ms", float64(time.Since(tProgram))/float64(time.Millisecond))
		shapeByFile := map[*ast.SourceFile][]*ast.Diagnostic{}
		tShape := time.Now()
		if shapeDiagnosticsIncluded {
			for _, d := range p.GetSemanticDiagnostics(context.Background(), nil) {
				if d.File() != nil {
					shapeByFile[d.File()] = append(shapeByFile[d.File()], d)
				}
			}
		}
		SweepPhases.ShapeMs += float64(time.Since(tShape)) / float64(time.Millisecond)
		kernel := setupKernel()
		factsStore := &sweepFactsStore{held: map[sweepFactsKey]*walk.FileFacts{}}
		if factsCacheDisabled {
			// -no-facts-cache: every entry compiles its own reachable
			// files instead of consuming another worker's first compile
			// — the determinism instrument that separates cache-carried
			// variance from checker-history variance.
			factsStore = nil
		}
		// walk scheduling: heaviest entries first off ONE shared list,
		// each goroutine claiming the next row and building its OWN
		// fresh checker for it — longest-processing-time packing. The
		// old file→checker affinity left ~800 ms of measured imbalance
		// on the recharts corpus (entries queued behind their assigned
		// checker while other checkers sat idle); this scheme keeps that
		// packing while giving every entry a checker no other entry ever
		// touches (see the fresh-checker note below).
		type walkRow struct {
			given     string
			entryFile *ast.SourceFile
		}
		var rows []walkRow
		for _, row := range group {
			entry := p.GetSourceFile(row.resolved)
			if entry == nil {
				continue
			}
			rows = append(rows, walkRow{given: row.given, entryFile: entry.AsSourceFile()})
		}
		sort.SliceStable(rows, func(i, j int) bool {
			return len(rows[i].entryFile.Text()) > len(rows[j].entryFile.Text())
		})
		if diagnose.EventOn("check.row.order") {
			// the sorted row order, once: entry file name + text length,
			// in the exact order workers will claim them — a rerun that
			// claims rows in a different order is the first place two
			// runs can diverge.
			for i, row := range rows {
				diagnose.Log("check.row.order", "group", key, "index", i,
					"file", row.entryFile.FileName(), "textLen", len(row.entryFile.Text()))
			}
		}
		// a file's verdict is a pure function of the file, its imports,
		// and the kernel — never of which OTHER files a checker happened
		// to answer questions about first. compiler.Program's own
		// checker pool cannot give that: its checkers are built ONCE
		// (checkerpool.go's createCheckersOnce) and statically striped
		// across every file by size, so ForEachCheckerParallel's workers
		// each held one checker across MANY entries — every cache the
		// checker accumulates (cachedTypes, narrowedTypes,
		// subtypeReductionCache, and the rest of checker.NewChecker's
		// per-instance maps) carried from one file's walk into the
		// next's (issue #35: a three-entry batch missed a designated
		// error the same entry reported alone). checker.NewChecker(p,
		// nil) is the same constructor the pool calls internally
		// (checkerpool.go:109) and is safe to call any number of times
		// against one already-built *compiler.Program — it only reads
		// the program's parse trees and module resolution (already paid
		// for by ProgramFromDiskMany above) and mints entirely new maps
		// and symbols on the returned *checker.Checker. Calling it here,
		// once per entry, is what makes each entry's checker actually
		// fresh rather than a lease on a recycled one.
		//
		// Parallelism stays bounded at the same width the old pool used
		// (GOMAXPROCS capped at 8, program_disk_host.go's BuiltProgram
		// comment): a fixed number of goroutines, each pulling the next
		// row off nextRow in longest-first order until the list drains —
		// the same packing ForEachCheckerParallel's workers gave, minus
		// the shared checker.
		checkerWidth := min(runtime.GOMAXPROCS(0), 8, len(rows))
		if checkerWidth < 1 {
			checkerWidth = 1
		}
		var nextRow atomic.Int64
		var resultsMu sync.Mutex
		var wg sync.WaitGroup
		for range checkerWidth {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					i := int(nextRow.Add(1)) - 1
					if i >= len(rows) {
						return
					}
					row := rows[i]
					diagnose.Log("check.row.claim", "group", key, "index", i, "file", row.given)
					// this entry's OWN checker: built fresh, questioned
					// only by this goroutine, and never handed to
					// another entry — no lease, no release, nothing to
					// return to a pool.
					c, _ := checker.NewChecker(p, nil)
					diagnose.Log("check.entry.checker", "file", row.given, "checker", fmt.Sprintf("%p", c))
					view := &program.CheckerProgram{
						Program:      p,
						Checker:      c,
						Entry:        row.entryFile,
						SurfacePaths: map[string]bool{surfacePath: true},
						Done:         nil,
					}
					entryStarted := time.Now()
					diagnose.Log("check.entry.walk.start", "file", row.given)
					result := tracing.TraceFile(row.given, func() CheckResult {
						return runRefinements(view, shapeByFile[row.entryFile], kernel, factsStore)
					})
					result.WallMs = float64(time.Since(entryStarted)) / float64(time.Millisecond)
					diagnose.Log("check.entry.walk.end", "file", row.given,
						"ms", result.WallMs, "refinements", len(result.Refinements))
					resultsMu.Lock()
					results[row.given] = result
					resultsMu.Unlock()
				}
			}()
		}
		wg.Wait()
		reportFactsDivergence(factsStore)
	}
	return results
}

// reportFactsDivergence is the determinism self-check: every checker
// that compiled the SAME file must have produced the same interface —
// the A1 sweep corruption (2026-08-24) was exactly two entries reading
// one support file with different compiled meanings, and every layer
// degraded without a report. Runs after every worker has joined, so it
// perturbs no scheduling inside the walks; costs one string compare
// per (file, checker) pair; prints nothing while the invariant holds.
// On a divergence it names the file, every checker's hash, and the
// exact interface lines the sides disagree on — the first corrupted
// run reports its own mechanism instead of waiting for a traced rerun
// to catch one.
func reportFactsDivergence(store *sweepFactsStore) {
	if store == nil {
		return
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	byFile := map[*ast.SourceFile]map[*checker.Checker]*walk.FileFacts{}
	for key, facts := range store.held {
		if byFile[key.file] == nil {
			byFile[key.file] = map[*checker.Checker]*walk.FileFacts{}
		}
		byFile[key.file][key.checker] = facts
	}
	for file, sides := range byFile {
		if len(sides) < 2 {
			continue
		}
		var firstChecker *checker.Checker
		var first *walk.FileFacts
		diverged := false
		for c, facts := range sides {
			if first == nil {
				firstChecker, first = c, facts
				continue
			}
			if facts.InterfaceHash != first.InterfaceHash {
				diverged = true
			}
		}
		if !diverged {
			continue
		}
		fmt.Fprintf(os.Stderr, "refinedts-facts-divergence file=%s checkers=%d\n", file.FileName(), len(sides))
		fmt.Fprintf(os.Stderr, "  checker %p hash=%s (the baseline below)\n", firstChecker, first.InterfaceHash)
		firstParts := map[string]bool{}
		for _, part := range walk.InterfaceParts(first.Annotations, first.Objects, first.Contracts, first.ImportHashes) {
			firstParts[part] = true
		}
		for c, facts := range sides {
			if facts == first {
				continue
			}
			fmt.Fprintf(os.Stderr, "  checker %p hash=%s\n", c, facts.InterfaceHash)
			parts := map[string]bool{}
			for _, part := range walk.InterfaceParts(facts.Annotations, facts.Objects, facts.Contracts, facts.ImportHashes) {
				parts[part] = true
				if !firstParts[part] {
					fmt.Fprintf(os.Stderr, "    only here:     %s\n", part)
				}
			}
			for part := range firstParts {
				if !parts[part] {
					fmt.Fprintf(os.Stderr, "    only baseline: %s\n", part)
				}
			}
		}
	}
}

// liveCheckMu serializes the whole live-program refinement seam.
// GO-LSP-EDITOR-PATH.md §16.3 item 1: document-diagnostic handlers run
// on a goroutine per request, and two overlapping walks would (a) race
// setupKernel's package-level hook writes ("never inside a concurrent
// per-entry path" — setupKernel's own contract) and (b) run
// kernelbridge.FlushQuestionStore concurrently at the end of both
// walks, which the store is not designed for. One editor buffer's walk
// is milliseconds-to-tens-of-milliseconds; serializing the seam is the
// listed resolution ("serialize refinement checks"), chosen over a
// per-request question store.
var liveCheckMu sync.Mutex

// CheckWithProgram is checkWithProgram in the TS source (check.ts):
// the language-service seam — judge ONE file of a LIVE program the
// caller already holds (the LSP session's own), so unsaved buffer
// contents are what is checked and no Program is rebuilt per call.
//
// This seam answers the EDITOR'S view: a fire covered by a
// @refinedts-expect-error marker is suppressed, and a stale marker is
// its own 7005 diagnostic (EditorView) — the CLI's raw comparator
// keeps its own presentation over the same reader.
//
// Shape policy (locked, GO-LSP-EDITOR-PATH.md §15.3): shape
// diagnostics are never collected here — the LS's own
// getAllDiagnostics already gathers them, and the plugin appends only
// refinements. runRefinements is called directly with a nil shape
// slice (which the walk never reads), so no process-global flag is
// touched and the CLI's own shape behavior is unaffected.
//
// A panic inside the walk is recovered into an error: an editor pull
// must degrade to shape-only, never take the request down
// (plugin/index.cjs logs and keeps prior on the same failure).
func CheckWithProgram(
	ctx context.Context,
	prog *compiler.Program,
	entryPath string,
	surfacePaths []string,
) (result CheckResult, err error) {
	p, buildErr := ProgramFromExisting(ctx, prog, entryPath, surfacePaths)
	if buildErr != nil {
		return CheckResult{}, buildErr
	}
	// Pattern 1 (locked §15.2): the lease opened inside
	// ProgramFromExisting is a REAL release on the project checker
	// pool — never decorative — and this seam owns it.
	if p.Done != nil {
		defer p.Done()
	}
	liveCheckMu.Lock()
	defer liveCheckMu.Unlock()
	defer func() {
		if r := recover(); r != nil {
			if recoveredErr, ok := r.(error); ok {
				err = recoveredErr
			} else {
				err = fmt.Errorf("refinement walk failed: %v", r)
			}
			result = CheckResult{}
		}
	}()
	result = runRefinements(p, nil, setupKernel(), nil)
	result.Refinements = EditorView(p.Entry.Text(), result.Refinements)
	return result, nil
}

// shapeDiagnosticsIncluded is the TS source's shapeDiagnosticsIncluded
// mutable flag: whether run asks for the entry's own semantic
// diagnostics. On by default.
var shapeDiagnosticsIncluded = true

// SetShapeDiagnostics is setShapeDiagnostics in the TS source.
func SetShapeDiagnostics(included bool) {
	shapeDiagnosticsIncluded = included
}

// run is the raw runner both Check and CheckFile share.
func run(p *program.CheckerProgram) CheckResult {
	var shape []*ast.Diagnostic
	if shapeDiagnosticsIncluded {
		shape = ShapeDiagnostics(p)
	}
	return runRefinements(p, shape, setupKernel(), nil)
}

// loadedKernel is run's kernel-acquisition step, shared with the batch
// runner: the already-loaded kernel, or one loaded from the resolved
// dylib path, or nil.
func loadedKernel() *kernelbridge.RefinedTSKernel {
	kernel := kernelbridge.KernelIfLoaded()
	if kernel == nil {
		if dylibPath := kernelbridge.ResolveDylibPath(); dylibPath != "" {
			loaded, err := kernelbridge.LoadKernel(dylibPath)
			if err == nil {
				kernel = loaded
			}
		}
	}
	return kernel
}

// setupKernel loads the kernel and points the operator transfers, the
// condition narrowings, AND the walk's engine at it. The hooks are
// package-level writes, so this runs ONCE per run or per sweep —
// never inside a concurrent per-entry path.
//
// The engine hook is the one the whole summary route gates on
// (EngineKernelHeld): without it, applySummary and every lowering
// that compiles a blob decline on their first line, and the kernel
// serves nothing but per-operation transfers. It was wired in every
// TEST and never here — the measurement that found it read
// kernel.ask=0 on every hot file while the engine sat built and dark.
func setupKernel() *kernelbridge.RefinedTSKernel {
	kernel := loadedKernel()
	walk.SetTransferKernel(kernel)
	walk.SetEngineKernel(kernel)
	narrowing.SetNarrowKernel(kernel)
	abstractdomain.SetLatticeKernel(kernel)
	return kernel
}
