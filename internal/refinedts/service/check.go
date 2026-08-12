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
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/narrowing"
	"github.com/microsoft/typescript-go/internal/refinedts/objectgraphs"
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
// construction is paid once per project and each entry still gets
// exactly its own diagnostics. Two things ride tsgo's own machinery
// rather than the per-entry path:
//
//   - shape diagnostics for the WHOLE group come from one
//     GetSemanticDiagnostics(ctx, nil) call — the same grouped
//     checker-pool parallel path the tsgo CLI's own --noEmit check
//     takes — bucketed per file afterward;
//   - the refinement walk holds ONE checker lease across every entry
//     (types from different checkers must never mix — the pool's own
//     rule), with per-file facts compiled once per sweep through
//     programFactsCached's shared store.
//
// The result map is keyed by the caller's own entryPaths spellings; a
// path whose file did not parse into its group's program has no row.
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
	for _, key := range groupOrder {
		group := groups[key]
		resolved := make([]string, len(group))
		for i, row := range group {
			resolved[i] = row.resolved
		}
		p := ProgramFromDiskMany(resolved)
		shapeByFile := map[*ast.SourceFile][]*ast.Diagnostic{}
		if shapeDiagnosticsIncluded {
			for _, d := range p.GetSemanticDiagnostics(context.Background(), nil) {
				if d.File() != nil {
					shapeByFile[d.File()] = append(shapeByFile[d.File()], d)
				}
			}
		}
		kernel := setupKernel()
		factsStore := &sweepFactsStore{held: map[*ast.SourceFile]*walk.FileFacts{}}
		// one goroutine per entry, each walking on the checker the
		// POOL assigned to its file (the same checker that just
		// shape-checked it — warm caches), held exclusively for the
		// walk's duration. Types from different checkers never mix
		// within one walk; parallel width is the pool's checker count
		// (BuiltProgram sizes it to the machine).
		var wg sync.WaitGroup
		var resultsMu sync.Mutex
		for _, row := range group {
			entry := p.GetSourceFile(row.resolved)
			if entry == nil {
				continue
			}
			entryFile := entry.AsSourceFile()
			wg.Add(1)
			go func(given string, entryFile *ast.SourceFile) {
				defer wg.Done()
				c, release := p.GetTypeCheckerForFileExclusive(context.Background(), entryFile)
				defer release()
				view := &program.CheckerProgram{
					Program:      p,
					Checker:      c,
					Entry:        entryFile,
					SurfacePaths: map[string]bool{surfacePath: true},
					// the lease is released by this goroutine's defer;
					// the view never owns it
					Done: nil,
				}
				result := tracing.TraceFile(given, func() CheckResult {
					return runRefinements(view, shapeByFile[entryFile], kernel, factsStore)
				})
				resultsMu.Lock()
				results[given] = result
				resultsMu.Unlock()
			}(row.given, entryFile)
		}
		wg.Wait()
	}
	return results
}

// shapeDiagnosticsIncluded is the TS source's shapeDiagnosticsIncluded
// mutable flag: whether run asks for the entry's own semantic
// diagnostics. On by default.
var shapeDiagnosticsIncluded = true

// SetShapeDiagnostics is setShapeDiagnostics in the TS source.
func SetShapeDiagnostics(included bool) {
	shapeDiagnosticsIncluded = included
}

// programFactsCached is programFacts in the TS source: every
// reachable file's facts, merged in reachableFiles' import order,
// with reporting (emptiness diagnostics) only for the entry.
type programFactsResult struct {
	registry         annotations.AnnotationRegistry
	objects          annotations.ObjectRegistry
	contracts        map[*ast.Symbol]*walk.FunctionContract
	entryDiagnostics []assignability.RefinementDiagnostic
}

// The `cache` parameter is incremental_file_cache.ts's factsCache
// made caller-held: the sweep's lifetime stands in for the WeakMap's
// GC lifetime (the caller drops the map when the sweep ends), and nil
// is the uncached single-check regime. The TS validity rule carries
// over whole: a hit must hold diagnostics when the file IS the entry,
// and every imported interface must still MEAN the same thing; an
// entry recompile serves diagnostics, not a new interface — the cache
// keeps the first compile's facts and hash, the fresh diagnostics
// ride along (the anti-cascade rule the TS source traced to its
// root).
func programFactsCached(p *program.CheckerProgram, kernel *kernelbridge.RefinedTSKernel, cache *sweepFactsStore) programFactsResult {
	merged := walk.FileFactsMerged{
		Registry:  annotations.AnnotationRegistry{},
		Objects:   annotations.ObjectRegistry{},
		Contracts: map[*ast.Symbol]*walk.FunctionContract{},
	}
	currentHash := map[string]string{}
	var entryDiagnostics []assignability.RefinementDiagnostic

	files := annotations.ReachableFiles(p)
	for _, file := range files {
		reporting := file == p.Entry
		var held *walk.FileFacts
		if cache != nil {
			held = cache.get(file)
		}
		valid := held != nil && (!reporting || held.HasDiagnostics)
		if valid {
			for name, hash := range held.ImportHashes {
				if currentHash[name] != hash {
					valid = false
					break
				}
			}
		}
		if valid {
			for symbol, a := range held.Annotations {
				merged.Registry[symbol] = a
			}
			for symbol, o := range held.Objects {
				merged.Objects[symbol] = o
			}
			for symbol, c := range held.Contracts {
				merged.Contracts[symbol] = c
			}
			currentHash[file.FileName()] = held.InterfaceHash
			if reporting {
				entryDiagnostics = held.Diagnostics
			}
			continue
		}
		importHashes := map[string]string{}
		for _, imported := range annotations.ImportedUserFiles(p, file) {
			if hash, ok := currentHash[imported.FileName()]; ok {
				importHashes[imported.FileName()] = hash
			}
		}
		facts := walk.CompileFileFacts(p, file, merged, kernel, reporting, importHashes)
		for symbol, a := range facts.Annotations {
			merged.Registry[symbol] = a
		}
		for symbol, o := range facts.Objects {
			merged.Objects[symbol] = o
		}
		for symbol, c := range facts.Contracts {
			merged.Contracts[symbol] = c
		}
		if cache != nil {
			// the TS miss-classification precedence: an already-cached
			// file recompiled AS the entry keeps its first hash and
			// facts (even over a hash mismatch — TS classifies
			// entryDiagnostics before hashMismatch); everything else
			// caches the fresh compile whole
			if held != nil && reporting && !held.HasDiagnostics {
				updated := *held
				updated.Diagnostics = facts.Diagnostics
				updated.HasDiagnostics = facts.HasDiagnostics
				cache.put(file, &updated)
				currentHash[file.FileName()] = held.InterfaceHash
			} else {
				fresh := facts
				cache.put(file, &fresh)
				currentHash[file.FileName()] = facts.InterfaceHash
			}
		} else {
			currentHash[file.FileName()] = facts.InterfaceHash
		}
		if reporting {
			entryDiagnostics = facts.Diagnostics
		}
	}

	return programFactsResult{
		registry:         merged.Registry,
		objects:          merged.Objects,
		contracts:        merged.Contracts,
		entryDiagnostics: entryDiagnostics,
	}
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

// setupKernel loads the kernel and points the operator transfers and
// the condition narrowings at it. The two hooks are package-level
// writes, so this runs ONCE per run or per sweep — never inside a
// concurrent per-entry path.
func setupKernel() *kernelbridge.RefinedTSKernel {
	kernel := loadedKernel()
	walk.SetTransferKernel(kernel)
	narrowing.SetNarrowKernel(kernel)
	return kernel
}

// sweepFactsStore is incremental_file_cache.ts's factsCache made
// sweep-shared: CheckFiles' entries compile and merge facts from many
// goroutines at once, so lookups and inserts lock. Compiles happen
// OUTSIDE the lock — two entries may compile the same file
// concurrently; the duplicate work is benign (facts are
// deterministic) and the last insert wins.
type sweepFactsStore struct {
	mu   sync.Mutex
	held map[*ast.SourceFile]*walk.FileFacts
}

func (s *sweepFactsStore) get(file *ast.SourceFile) *walk.FileFacts {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.held[file]
}

func (s *sweepFactsStore) put(file *ast.SourceFile, facts *walk.FileFacts) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.held[file] = facts
}

// runRefinements is run's refinement half: passes 1–3 over an entry
// whose shape diagnostics and kernel the caller already holds.
// `factsCache` is nil for a single check; the batch runner hands one
// store across every entry of a sweep (programFactsCached).
func runRefinements(p *program.CheckerProgram, shape []*ast.Diagnostic, kernel *kernelbridge.RefinedTSKernel, factsCache *sweepFactsStore) CheckResult {
	detail := tracing.BeginFileDetail(p.Entry.FileName())
	tracing.BindFileDetail(detail)
	defer tracing.BindFileDetail(nil)
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
		P:         p,
		Kernel:    kernel,
		Registry:  facts.registry,
		Objects:   facts.objects,
		Contracts: facts.contracts,
		Report:    report,
		Aliases:   dataflowfacts.NewAliasClasses(),
		Declared:  map[string]*annotations.DeclaredRefinement{},
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
		walk.AnalyzeStatements(&topLevelCtx, walk.Env{}, statements, nil)
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

	return CheckResult{Shape: shape, Refinements: refinements}
}

// reportKey is the TS source's `${d.start}:${d.length}:${d.code}:${d.messageText}`
// dedupe key.
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

// checkObjectGraphs is check.ts's pass-1b loop: the keys' cardinality
// paths are exactly what the kernel's judgment reads. A fired fault
// refutes the graph outright; those are theorems, so they are
// reported where the object is stated.
func checkObjectGraphs(
	p *program.CheckerProgram,
	objects annotations.ObjectRegistry,
	kernel *kernelbridge.RefinedTSKernel,
	report func(d assignability.RefinementDiagnostic),
) {
	for symbol, object := range objects {
		var declaration *ast.Node
		if len(symbol.Declarations) > 0 {
			declaration = symbol.Declarations[0]
		}
		var site *ast.Node
		if declaration != nil && ast.IsVariableDeclaration(declaration) && declaration.AsVariableDeclaration().Initializer != nil {
			site = declaration.AsVariableDeclaration().Initializer
		} else {
			site = declaration
		}
		if site == nil {
			continue
		}
		// an imported object's graph is checked where it is STATED
		if ast.GetSourceFileOfNode(site) != p.Entry {
			continue
		}
		assembled := annotations.SpecificationOf(object, objects)
		if assembled.Unresolved != nil {
			report(assignability.At(assembled.Unresolved, 7004, "z.ref names a schema the checker did not read as an object"))
			continue
		}
		// a decline is an outcome, not an incident: a graph judgment
		// over the kernel's budget alerts at the statement, never
		// crashes — the kernel closure PANICS on a refused question
		// (PORT.md's kernel-panic convention), recovered here the way
		// the TS source's try/catch does.
		verdict, declined := checkAssignabilityRecovered(kernel, assembled.Spec)
		if declined != "" {
			report(assignability.At(site, 7002, "this object's graph judgment was declined — "+declined))
			continue
		}
		if !verdict.Structural {
			report(assignability.At(site, 7004, "this object's graph is not well formed"))
			continue
		}
		for _, fault := range verdict.Faults {
			key := keyNameOfPath(assembled.Spec, int(fault.Path))
			if key == "" {
				report(assignability.At(site, 7003, fault.MessageText))
			} else {
				report(assignability.At(site, 7003, "the key '"+key+"': "+fault.MessageText))
			}
		}
	}
}

// checkAssignabilityRecovered wraps objectgraphs.CheckAssignability's
// panic-on-decline (RefinedTSKernel's question methods panic on a
// refused question, mirroring the TS source's `throw`) into a
// (verdict, declineMessage) pair — declineMessage is "" on success.
func checkAssignabilityRecovered(kernel *kernelbridge.RefinedTSKernel, spec objectgraphs.Specification) (verdict kernelbridge.JudgeAnswer, declineMessage string) {
	defer func() {
		if r := recover(); r != nil {
			if err, ok := r.(error); ok {
				declineMessage = err.Error()
			} else {
				declineMessage = "declined"
			}
		}
	}()
	verdict = objectgraphs.CheckAssignability(kernel, spec, nil)
	return verdict, ""
}

// keyNameOfPath is keyNameOfPath in the TS source: which key a
// fault's path belongs to — the object markings carry the name, so a
// graph fault reads as a fault about a key.
func keyNameOfPath(spec objectgraphs.Specification, pathIndex int) string {
	for _, marking := range spec.Objects {
		for _, key := range marking.Keys {
			if key.Path == pathIndex {
				return key.Name
			}
		}
	}
	return ""
}

// walkContractBodies is check.ts's pass-3 second half: schedule the
// entry's contract bodies CALLERS-FIRST (Kahn's order over the
// entry-file call graph; a cycle keeps registration order among its
// members), then walk each with AnalyzeFunction.
func walkContractBodies(
	ctx *walk.FlowContext,
	p *program.CheckerProgram,
	contracts map[*ast.Symbol]*walk.FunctionContract,
	kernel *kernelbridge.RefinedTSKernel,
	detail *tracing.FileDetail,
) {
	walked := map[*ast.Node]bool{}
	var ordered []*walk.FunctionContract
	for _, contract := range contracts {
		if ast.GetSourceFileOfNode(contract.Declaration) != p.Entry {
			continue
		}
		if walked[contract.Declaration] {
			continue
		}
		walked[contract.Declaration] = true
		ordered = append(ordered, contract)
	}
	// the contracts map ranges in Go's randomized order; the TS
	// source's Map iterates in insertion (source) order. Sorting by
	// declaration position keeps the schedule deterministic run to
	// run — and a join a schedule never warms is demand-filled at the
	// ask (call_site_snapshot_fill.go), so the order is a warmth
	// optimization, never a correctness lever.
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].Declaration.Pos() < ordered[j].Declaration.Pos()
	})

	// callee edges by SPELLED NAME — a name that names exactly one
	// entry-file function declaration is that function; an ambiguous
	// or unresolvable name contributes no edge and the registration
	// order stands for it. No type-checker question is asked: the
	// order is a walk-scheduling heuristic, never a semantic claim.
	byName := map[string]*ast.Node{}
	ambiguous := map[string]bool{}
	for _, contract := range ordered {
		declaration := contract.Declaration
		if ast.IsFunctionDeclaration(declaration) && declaration.Name() != nil {
			name := declaration.Name().Text()
			if ambiguous[name] {
				continue
			}
			if _, seen := byName[name]; seen {
				delete(byName, name)
				ambiguous[name] = true
				continue
			}
			byName[name] = declaration
		}
	}

	position := map[*ast.Node]int{}
	for i, contract := range ordered {
		position[contract.Declaration] = i
	}

	noteCallee := func(edges map[*ast.Node]bool, from *ast.Node, name string) {
		callee, ok := byName[name]
		if !ok || callee == from || edges[callee] {
			return
		}
		edges[callee] = true
	}

	calleeEdges := map[*ast.Node]map[*ast.Node]bool{}
	inDegree := map[*ast.Node]int{}
	for _, contract := range ordered {
		inDegree[contract.Declaration] = 0
	}
	for _, contract := range ordered {
		edges := map[*ast.Node]bool{}
		var scan func(node *ast.Node)
		scan = func(node *ast.Node) {
			if ast.IsCallExpression(node) {
				expr := node.AsCallExpression().Expression
				if ast.IsIdentifier(expr) {
					noteCallee(edges, contract.Declaration, expr.Text())
				}
			}
			node.ForEachChild(func(child *ast.Node) bool {
				scan(child)
				return false
			})
		}
		scan(contract.Declaration)
		for callee := range edges {
			inDegree[callee] = inDegree[callee] + 1
		}
		calleeEdges[contract.Declaration] = edges
	}

	var ready []*ast.Node
	for _, contract := range ordered {
		if inDegree[contract.Declaration] == 0 {
			ready = append(ready, contract.Declaration)
		}
	}
	var sorted []*walk.FunctionContract
	placed := map[*ast.Node]bool{}
	byDeclaration := map[*ast.Node]*walk.FunctionContract{}
	for _, contract := range ordered {
		byDeclaration[contract.Declaration] = contract
	}
	for len(ready) > 0 {
		// among the ready, keep registration order — deterministic
		sort.SliceStable(ready, func(i, j int) bool { return position[ready[i]] < position[ready[j]] })
		next := ready[0]
		ready = ready[1:]
		if placed[next] {
			continue
		}
		placed[next] = true
		if held, ok := byDeclaration[next]; ok {
			sorted = append(sorted, held)
		}
		for callee := range calleeEdges[next] {
			inDegree[callee]--
			if inDegree[callee] == 0 {
				ready = append(ready, callee)
			}
		}
	}
	// cycle members never reach zero — they follow in registration order
	for _, contract := range ordered {
		if !placed[contract.Declaration] {
			sorted = append(sorted, contract)
		}
	}
	ordered = sorted

	for _, contract := range ordered {
		// an unstated parameter of a non-exported function wears the
		// join of what its call sites pass — every caller is in view,
		// so the join is exactly what the parameter can hold
		declaration := contract.Declaration
		needsCallSiteJoin := ast.IsFunctionDeclaration(declaration)
		if needsCallSiteJoin {
			needsCallSiteJoin = false
			for i := range declaration.Parameters() {
				var stated *annotations.DeclaredRefinement
				if i < len(contract.Params) {
					stated = contract.Params[i]
				}
				if stated == nil {
					needsCallSiteJoin = true
					break
				}
			}
		}
		var initialStates map[string]abstractdomain.AbstractValue
		if needsCallSiteJoin {
			tJoin := time.Now()
			env, ok := walk.CallSiteBindings(walk.CallSiteCtx{P: p, Registry: ctx.Registry, Objects: ctx.Objects, Contracts: contracts, Kernel: kernel}, declaration)
			detail.NotePhase("callSiteJoin", tJoin)
			if ok {
				initialStates = env
			}
		}
		tFn := time.Now()
		walk.AnalyzeFunction(ctx, contract, initialStates)
		detail.NoteContract(contractLabel(contract), tFn)
	}
}

// contractLabel names a contract for the slow-contract table: the
// spelled function/method name when present, otherwise kind@pos.
func contractLabel(contract *walk.FunctionContract) string {
	declaration := contract.Declaration
	if declaration == nil {
		return "<nil>"
	}
	if name := declaration.Name(); name != nil && ast.IsIdentifier(name) {
		return name.Text()
	}
	return fmt.Sprintf("%s@%d", declaration.Kind.String(), declaration.Pos())
}
