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
//     SKIPPED — Go has no JS-isolate analog here and the task scopes
//     this port to single-process.
//   - programFacts' INCREMENTAL CACHE (annotations/incremental_file_cache.ts,
//     the SourceFile-keyed WeakMap + interface-hash invalidation) is
//     NOT ported: it is its own genuinely blocked port task per that
//     file's header (needs a coordinator to wire CompileFileFacts
//     through the cache) and out of this unit's scope. Run below
//     COMPILES every reachable file's facts fresh on every check —
//     the same merge order and the same reporting-only-for-the-entry
//     rule as the TS source's programFacts, just without the
//     cross-check memoization. Functionally faithful for one check;
//     slower across repeated checks of an unchanged tree.
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
	"sort"

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

// shapeDiagnosticsIncluded is the TS source's shapeDiagnosticsIncluded
// mutable flag: whether run asks for the entry's own semantic
// diagnostics. On by default.
var shapeDiagnosticsIncluded = true

// SetShapeDiagnostics is setShapeDiagnostics in the TS source.
func SetShapeDiagnostics(included bool) {
	shapeDiagnosticsIncluded = included
}

// programFacts is programFacts in the TS source, WITHOUT the
// incremental cache (see file header): every reachable file's facts
// compile fresh, merged in reachableFiles' import order, with
// reporting (emptiness diagnostics) only for the entry.
type programFactsResult struct {
	registry         annotations.AnnotationRegistry
	objects          annotations.ObjectRegistry
	contracts        map[*ast.Symbol]*walk.FunctionContract
	entryDiagnostics []assignability.RefinementDiagnostic
}

func programFacts(p *program.CheckerProgram, kernel *kernelbridge.RefinedTSKernel) programFactsResult {
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
		currentHash[file.FileName()] = facts.InterfaceHash
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
	kernel := kernelbridge.KernelIfLoaded()
	if kernel == nil {
		if dylibPath := kernelbridge.ResolveDylibPath(); dylibPath != "" {
			loaded, err := kernelbridge.LoadKernel(dylibPath)
			if err == nil {
				kernel = loaded
			}
		}
	}
	// the operator transfers and the condition narrowings pose their
	// questions through this kernel
	walk.SetTransferKernel(kernel)
	narrowing.SetNarrowKernel(kernel)

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
	facts := programFacts(p, kernel)
	for _, d := range facts.entryDiagnostics {
		report(d)
	}

	// ── pass 1b: each object's graph is checked as a specification ──
	tracing.Span("pass1b.objectGraphs", func() any {
		checkObjectGraphs(p, facts.objects, kernel, report)
		return nil
	}, tracing.GrainStep)

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

	// each BODY walks once. Bodies walk OUTERMOST-FIRST: an enclosing
	// body's walk records the call-site snapshots its inner functions'
	// call-site joins consume, so the encloser must have walked before
	// the enclosed asks.
	tracing.Span("pass3.contractBodies", func() any {
		walkContractBodies(ctx, p, facts.contracts, kernel)
		return nil
	}, tracing.GrainStep)

	sort.SliceStable(refinements, func(i, j int) bool { return refinements[i].Start < refinements[j].Start })
	// newly earned kernel answers persist — theorems survive the
	// process (boundary/kernel.ts)
	tracing.Span("flushQuestionStore", func() any {
		kernelbridge.FlushQuestionStore()
		return nil
	}, tracing.GrainStep)
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

	position := map[*ast.Node]int{}
	for i, contract := range ordered {
		position[contract.Declaration] = i
	}

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
					callee, ok := byName[expr.Text()]
					if ok && callee != contract.Declaration && !edges[callee] {
						edges[callee] = true
						inDegree[callee] = inDegree[callee] + 1
					}
				}
			}
			node.ForEachChild(func(child *ast.Node) bool {
				scan(child)
				return false
			})
		}
		scan(contract.Declaration)
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
			env, ok := walk.CallSiteBindings(walk.CallSiteCtx{P: p, Registry: ctx.Registry, Objects: ctx.Objects, Contracts: contracts, Kernel: kernel}, declaration)
			if ok {
				initialStates = env
			}
		}
		walk.AnalyzeFunction(ctx, contract, initialStates)
	}
}
