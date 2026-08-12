// The program's import graph: edges, closures, and the one global
// dependency order that keeps interface hashes stable across entries.
//
// Ported 1:1 from annotations/program_graph.ts. userSourceFiles is
// inlined from service/program_resolution.ts (out of scope per
// PORT.md's adapter-layer rule, but a pure filter over
// p.Program.GetSourceFiles() -- the same inlining precedent as
// typereading/type_node.go's symbolAt).

package annotations

import (
	"sort"
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/compiler"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
)

// userSourceFiles is userSourceFiles in the TS source
// (service/program_resolution.ts): every file the program holds that
// a user wrote -- the entry, its imports, and theirs; declaration
// files (the default library) are not user statements.
func userSourceFiles(p *program.CheckerProgram) []*ast.SourceFile {
	var out []*ast.SourceFile
	for _, f := range p.Program.GetSourceFiles() {
		if !f.IsDeclarationFile {
			out = append(out, f)
		}
	}
	return out
}

// ProgramGraph is ProgramGraph in the TS source.
//
// Under the parallel sweep, ONE *compiler.Program is shared by many
// *program.CheckerProgram views, each running on its own goroutine --
// GraphOf hands every one of them the SAME *ProgramGraph pointer, so
// Edges/Closures/Order are read and filled CONCURRENTLY. mu guards
// those three maps; Scripts is written once before the graph is
// published (graphs[p.Program] = held, under graphsMu) and read-only
// after, so it needs no lock of its own.
type ProgramGraph struct {
	mu sync.Mutex
	// edges are import edges, resolved once per file per program.
	edges map[*ast.SourceFile][]*ast.SourceFile
	// closures are each entry's reachable set, in dependency order.
	closures map[*ast.SourceFile][]*ast.SourceFile
	// Scripts are the non-module scripts, which every closure
	// includes.
	Scripts []*ast.SourceFile
	// order is ONE dependency order for the whole program,
	// entry-independent. nil until first computed.
	order map[*ast.SourceFile]int
}

// edgesOf reads a cached edge list, reporting whether it was found.
// Locked -- concurrent goroutines sharing this graph must not tear
// the map.
func (g *ProgramGraph) edgesOf(file *ast.SourceFile) ([]*ast.SourceFile, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	edges, ok := g.edges[file]
	return edges, ok
}

// setEdges fills the cached edge list for file. Racing goroutines may
// compute the same edges twice; the last write wins and both answers
// agree, so duplicate work is fine -- only a torn map is not.
func (g *ProgramGraph) setEdges(file *ast.SourceFile, edges []*ast.SourceFile) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.edges[file] = edges
}

// closureOf reads a cached entry closure, reporting whether it was
// found. Locked for the same reason as edgesOf.
func (g *ProgramGraph) closureOf(entry *ast.SourceFile) ([]*ast.SourceFile, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	held, ok := g.closures[entry]
	return held, ok
}

// setClosure fills the cached closure for entry.
func (g *ProgramGraph) setClosure(entry *ast.SourceFile, members []*ast.SourceFile) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.closures[entry] = members
}

// orderSnapshot reads the built dependency order, reporting whether
// it exists yet.
func (g *ProgramGraph) orderSnapshot() (map[*ast.SourceFile]int, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.order, g.order != nil
}

// setOrder fills the dependency order once. A racing goroutine that
// also built one loses harmlessly -- both orders agree, since both
// are the same deterministic DFS over the same program.
func (g *ProgramGraph) setOrder(order map[*ast.SourceFile]int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.order == nil {
		g.order = order
	}
}

// graphsMu/graphs is the port's substitute for the TS source's
// `WeakMap<ts.Program, ProgramGraph>` -- a *compiler.Program pointer
// is stable per program (PORT.md's WeakMap convention), guarded by a
// mutex since the checker walk is not guaranteed single-threaded.
var (
	graphsMu sync.Mutex
	graphs   = map[*compiler.Program]*ProgramGraph{}
)

// GraphOf is graphOf in the TS source.
func GraphOf(p *program.CheckerProgram) *ProgramGraph {
	graphsMu.Lock()
	defer graphsMu.Unlock()
	held, ok := graphs[p.Program]
	if !ok {
		// the one pass over every user file, paid once per program
		var scripts []*ast.SourceFile
		for _, f := range userSourceFiles(p) {
			if !ast.IsExternalModule(f) {
				scripts = append(scripts, f)
			}
		}
		held = &ProgramGraph{
			edges:    map[*ast.SourceFile][]*ast.SourceFile{},
			closures: map[*ast.SourceFile][]*ast.SourceFile{},
			Scripts:  scripts,
		}
		graphs[p.Program] = held
	}
	return held
}

// OrderOf is orderOf in the TS source: the program's one dependency
// order: post-order DFS over every user module, roots sorted by path
// so the walk is deterministic. Built once per program, on first use.
func OrderOf(p *program.CheckerProgram, graph *ProgramGraph, edgesOf func(file *ast.SourceFile) []*ast.SourceFile) map[*ast.SourceFile]int {
	if order, ok := graph.orderSnapshot(); ok {
		return order
	}
	order := map[*ast.SourceFile]int{}
	seen := map[*ast.SourceFile]bool{}
	var visit func(file *ast.SourceFile)
	visit = func(file *ast.SourceFile) {
		if seen[file] {
			return
		}
		seen[file] = true
		edges, ok := graph.edgesOf(file)
		if !ok {
			edges = edgesOf(file)
			graph.setEdges(file, edges)
		}
		for _, imported := range edges {
			visit(imported)
		}
		order[file] = len(order)
	}
	var roots []*ast.SourceFile
	for _, f := range userSourceFiles(p) {
		if ast.IsExternalModule(f) {
			roots = append(roots, f)
		}
	}
	sort.Slice(roots, func(i, j int) bool { return roots[i].FileName() < roots[j].FileName() })
	for _, root := range roots {
		visit(root)
	}
	graph.setOrder(order)
	return graph.mustOrder()
}

// mustOrder reads back whatever order won the race in setOrder -- the
// caller's own `order` local may have lost to a concurrent builder,
// and the two are equal by construction, but the stored one is the
// single answer every later caller also sees.
func (g *ProgramGraph) mustOrder() map[*ast.SourceFile]int {
	order, _ := g.orderSnapshot()
	return order
}
