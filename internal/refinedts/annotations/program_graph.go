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
type ProgramGraph struct {
	// Edges are import edges, resolved once per file per program.
	Edges map[*ast.SourceFile][]*ast.SourceFile
	// Closures are each entry's reachable set, in dependency order.
	Closures map[*ast.SourceFile][]*ast.SourceFile
	// Scripts are the non-module scripts, which every closure
	// includes.
	Scripts []*ast.SourceFile
	// Order is ONE dependency order for the whole program,
	// entry-independent. nil until first computed.
	Order map[*ast.SourceFile]int
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
			Edges:    map[*ast.SourceFile][]*ast.SourceFile{},
			Closures: map[*ast.SourceFile][]*ast.SourceFile{},
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
	if graph.Order != nil {
		return graph.Order
	}
	order := map[*ast.SourceFile]int{}
	seen := map[*ast.SourceFile]bool{}
	var visit func(file *ast.SourceFile)
	visit = func(file *ast.SourceFile) {
		if seen[file] {
			return
		}
		seen[file] = true
		edges, ok := graph.Edges[file]
		if !ok {
			edges = edgesOf(file)
			graph.Edges[file] = edges
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
	graph.Order = order
	return order
}
