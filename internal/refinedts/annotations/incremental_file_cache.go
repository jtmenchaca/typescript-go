// Per-FILE facts, cached by SourceFile identity, invalidated by
// INTERFACE — the graph's break points made operational.
//
// BLOCKED: programFacts, compileFileFacts's caching wrapper, and
// clearFactsCache need FunctionContract (evaluation/flow_context.ts,
// not ported -- evaluation/ is out of scope per PORT.md's port
// order) and compileFileFacts/compileContractFileFacts (annotations'
// own contract_file_facts.ts / file_facts_compiler.ts, themselves
// blocked on the same FunctionContract). ImportedUserFiles and
// ReachableFiles -- the "what can this check reach" half, which needs
// only ProgramGraph (ported, program_graph.go) and symbol resolution
// -- are ported below; they are the two pieces every later caller
// (interprocedural, control_flow) will actually need first.
//
// Ported 1:1 from annotations/incremental_file_cache.ts's
// importedUserFiles and reachableFiles.

package annotations

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/tracing"
)

// ImportedUserFiles is importedUserFiles in the TS source: the user
// files this file imports, resolved through the checker -- symbols,
// never path guessing.
func ImportedUserFiles(p *program.CheckerProgram, file *ast.SourceFile) []*ast.SourceFile {
	var out []*ast.SourceFile
	for _, statement := range file.Statements.Nodes {
		var specifier *ast.Node
		if ast.IsImportDeclaration(statement) {
			specifier = statement.AsImportDeclaration().ModuleSpecifier
		} else if ast.IsExportDeclaration(statement) {
			specifier = statement.AsExportDeclaration().ModuleSpecifier
		} else {
			continue
		}
		if specifier == nil || !ast.IsStringLiteral(specifier) {
			continue
		}
		symbol := p.Checker.GetSymbolAtLocation(specifier)
		if symbol == nil {
			continue
		}
		declaration := symbol.ValueDeclaration
		if declaration == nil && len(symbol.Declarations) > 0 {
			declaration = symbol.Declarations[0]
		}
		if declaration == nil || !ast.IsSourceFile(declaration) {
			continue
		}
		sourceFile := declaration.AsSourceFile()
		if sourceFile.IsDeclarationFile {
			continue
		}
		out = append(out, sourceFile)
	}
	return out
}

/* -- what a check can reach -------------------------------------- */

// A check resolves symbols from nodes in its entry file, and from
// the bodies of contracts it inlines -- and a contract is only found
// because its callee resolved from the entry. So every symbol the
// walk looks up is declared in the entry's transitive import
// closure. Files outside it can only contribute registry entries
// nothing will ever ask for.
//
// Sweeping the whole program was therefore work proportional to the
// repository for an answer proportional to the closure -- and the
// closure does not grow with the repository.
//
// Two things the closure must carry beyond the import edges:
//
//   - NON-MODULE SCRIPTS share global scope. Their statements are
//     visible from every file without an import, so they are
//     reachable from everywhere and belong in every closure.
//   - the order is IMPORTS FIRST. The interface-hash chain reads a
//     dependency's hash while compiling its dependents, so a
//     post-order walk is not an optimization here, it is the
//     contract.

// ReachableFiles is reachableFiles in the TS source: the files this
// check can reach, imports first.
func ReachableFiles(p *program.CheckerProgram) []*ast.SourceFile {
	graph := GraphOf(p)
	if held, ok := graph.Closures[p.Entry]; ok {
		tracing.Count("facts.closureHit", 0)
		return held
	}
	var out []*ast.SourceFile
	seen := map[*ast.SourceFile]bool{}
	for _, script := range graph.Scripts {
		if seen[script] {
			continue
		}
		seen[script] = true
		out = append(out, script)
	}
	// membership by DFS (marked before descending, so a cycle
	// terminates) -- but the ORDER comes from the program's one
	// global dependency order, so a file's position is the same in
	// every closure that contains it and its interface hash cannot
	// flap on where this entry's walk happened to enter a cycle
	var members []*ast.SourceFile
	var visit func(file *ast.SourceFile)
	visit = func(file *ast.SourceFile) {
		if seen[file] {
			return
		}
		seen[file] = true
		edges, ok := graph.Edges[file]
		if !ok {
			edges = ImportedUserFiles(p, file)
			graph.Edges[file] = edges
		}
		for _, imported := range edges {
			visit(imported)
		}
		members = append(members, file)
	}
	visit(p.Entry)
	order := OrderOf(p, graph, func(file *ast.SourceFile) []*ast.SourceFile { return ImportedUserFiles(p, file) })
	sortSourceFilesByOrder(members, order)
	out = append(out, members...)
	graph.Closures[p.Entry] = out
	tracing.CountBy("facts.closureFiles", int64(len(out)))
	return out
}

// sortSourceFilesByOrder is a stable insertion sort over the order
// map -- the TS source's `members.sort((a, b) => (order.get(a) ?? 0)
// - (order.get(b) ?? 0))`.
func sortSourceFilesByOrder(members []*ast.SourceFile, order map[*ast.SourceFile]int) {
	for i := 1; i < len(members); i++ {
		for j := i; j > 0 && order[members[j-1]] > order[members[j]]; j-- {
			members[j-1], members[j] = members[j], members[j-1]
		}
	}
}
