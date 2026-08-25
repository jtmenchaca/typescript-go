// from service/check.ts
//
// walkContractBodies: pass-3's second half — schedule the entry's
// contract bodies CALLERS-FIRST (Kahn's order over the entry-file
// call graph; a cycle keeps registration order among its members),
// then walk each with AnalyzeFunction.

package service

import (
	"fmt"
	"sort"
	"time"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/tracing"
	"github.com/microsoft/typescript-go/internal/refinedts/walk"
)

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
		// a non-exported function's parameters wear the join of what
		// its call sites pass — every caller is in view, so the join is
		// exactly what each parameter can hold. This is asked for EVERY
		// parameter, stated or not: a STATED parameter still wants the
		// join, because entry_env.go's BindEntryEnv takes the MEET of a
		// call-site-derived exact value against the declared ceiling
		// (entryStateMeet), which only ever narrows, never widens.
		// Gating on "some parameter is unstated" (as this used to)
		// was sound only while a bare keyword (`number`, `string`,
		// `boolean`) compiled to no DeclaredRefinement at all; now that
		// annotations/type_node_sets.go grounds those keywords, a
		// function whose every parameter carries a written bare-keyword
		// type had EVERY stated slot non-nil, so the old inner loop
		// never found a nil entry and the join never ran — silently
		// dropping every call-site-derived exact value for exactly the
		// functions the grounding change touches (walk/
		// call_site_snapshot_fill.go's demand-fill path carried the
		// identical bug, fixed the same way).
		declaration := contract.Declaration
		needsCallSiteJoin := ast.IsFunctionDeclaration(declaration)
		var initialStates map[string]abstractdomain.AbstractValue
		if needsCallSiteJoin {
			tJoin := time.Now()
			env, ok := walk.CallSiteBindings(walk.CallSiteCtx{P: p, Registry: ctx.Registry, Objects: ctx.Objects, Contracts: contracts, Kernel: kernel}, declaration)
			detail.NotePhase("callSiteJoin", tJoin)
			if ok {
				// AnalyzeFunction takes the call-site join as a plain map;
				// the Env is read once here, at the boundary.
				initialStates = env.AsMap()
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
