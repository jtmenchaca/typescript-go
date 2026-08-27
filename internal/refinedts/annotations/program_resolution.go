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
	"fmt"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/diagnose"
	"github.com/microsoft/typescript-go/internal/refinedts/nameresolution"
	"github.com/microsoft/typescript-go/internal/refinedts/tracing"
)

// symbolAt is symbolAt in the TS source (service/program_resolution.ts):
// the symbol behind a node, followed THROUGH import aliases -- an
// imported name resolves to its declaration in the exporting file, so
// registries keyed by declaration symbols answer for imported names
// too.
//
// The BINDER's own scope tables answer first (nameresolution): they
// are written once at bind time and cannot drift, where the checker's
// resolver was measured intermittently dropping an imported alias
// under concurrent per-entry checkers. The checker settles only what
// one syntactic edge cannot (globals, re-export chains, namespace
// members).
func symbolAt(c *checker.Checker, node *ast.Node) *ast.Symbol {
	if s := nameresolution.DeclarationSymbolOf(c.BoundProgram(), node); s != nil {
		if diagnose.EventOn("annotations.symbolAt") {
			diagnose.Log("annotations.symbolAt",
				"text", diagnose.NodeText(node), "road", "binder", "symbol", fmt.Sprintf("%p", s))
		}
		return s
	}
	tracing.CountBy("host.symbolAtLocation", 1)
	symbol := c.GetSymbolAtLocation(node)
	if symbol != nil && (symbol.Flags&ast.SymbolFlagsAlias) != 0 {
		tracing.CountBy("host.aliasedSymbol", 1)
		symbol = c.GetAliasedSymbol(symbol)
	}
	if diagnose.EventOn("annotations.symbolAt") {
		diagnose.Log("annotations.symbolAt",
			"text", diagnose.NodeText(node), "road", "checker", "symbol", fmt.Sprintf("%p", symbol))
	}
	return symbol
}

// resolvesToDefaultLib is resolvesToDefaultLib in the TS source: does
// this identifier resolve to a declaration in a DEFAULT library file
// (the global Number, Math, ...)? Symbols, never names.
func resolvesToDefaultLib(c *checker.Checker, node *ast.Node) bool {
	tracing.CountBy("host.symbolAtLocation", 1)
	tracing.CountBy("host.symbolInDefaultLib", 1)
	return c.SymbolInDefaultLib(c.GetSymbolAtLocation(node))
}
