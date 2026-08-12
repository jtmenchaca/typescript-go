// Symbol and path resolution questions the annotation readers ask.
// service/program_resolution.ts is out of scope for this directory's
// port (the CheckerHost/tsgo-oracle adapter layer, per PORT.md); the
// PURE SEMANTIC READS it offers -- symbol resolution through import
// aliases, default-library membership, surface/library-adapter
// recognition -- are inlined here directly against *checker.Checker
// and program.CheckerProgram, mirroring typereading/type_node.go's
// symbolAt precedent. libraryAdapterOfNode, resolvesToAnnotationRoot,
// and resolvesToSurface stay UNPORTED here: they read
// program.CheckerProgram.SurfacePaths (present) and the library
// adapter registry (also present, library_adapter.go), so they are
// ported alongside chain_roots.go where every call site lives.

package annotations

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
)

// symbolAt is symbolAt in the TS source (service/program_resolution.ts):
// the symbol behind a node, followed THROUGH import aliases -- an
// imported name resolves to its declaration in the exporting file, so
// registries keyed by declaration symbols answer for imported names
// too.
func symbolAt(c *checker.Checker, node *ast.Node) *ast.Symbol {
	symbol := c.GetSymbolAtLocation(node)
	if symbol != nil && (symbol.Flags&ast.SymbolFlagsAlias) != 0 {
		symbol = c.GetAliasedSymbol(symbol)
	}
	return symbol
}

// resolvesToDefaultLib is resolvesToDefaultLib in the TS source: does
// this identifier resolve to a declaration in a DEFAULT library file
// (the global Number, Math, ...)? Symbols, never names.
func resolvesToDefaultLib(c *checker.Checker, node *ast.Node) bool {
	return c.SymbolInDefaultLib(c.GetSymbolAtLocation(node))
}
