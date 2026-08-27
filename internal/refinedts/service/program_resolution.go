// from service/program_resolution.ts
//
// Symbol and path resolution questions the analyzer asks of a
// CheckerProgram: default-library membership, surface and library-
// adapter recognition, alias-followed symbols, and user source files.
//
// The TsgoSymbol branch (a tsc symbol vs. a wrapped tsgo symbol,
// distinguished because the TS source runs against EITHER checker) has
// no Go twin: this whole tree's checker IS tsgo's, in-process, so
// every symbol answers the tsc-shaped branch directly (per PORT.md's
// CheckerHost/oracle-adapter rule). resolvesToDefaultLib and
// symbolInDefaultLib/symbolEntirelyInDefaultLib read straight off
// *checker.Checker's own exported wrappers (SymbolInDefaultLib,
// SymbolEntirelyInDefaultLib), which already answer this question
// directly — the TS source's "is this symbol a TsgoSymbol" branch and
// its declarations-walk fallback both collapse into that one call.
//
// symbolAt/resolvesToSurface/libraryAdapterOfNode/resolvesToAnnotationRoot
// already ported (unexported) alongside annotations/chain_roots.go's
// call sites, per that file's own header note — not duplicated here.
// userSourceFiles is the one function with no landed Go caller yet;
// it is ported below against *compiler.Program directly.

package service

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/tracing"
)

// SymbolAt is symbolAt in the TS source: the symbol behind a node,
// followed THROUGH import aliases — an imported name resolves to its
// declaration in the exporting file, so registries keyed by
// declaration symbols answer for imported names too.
func SymbolAt(p *program.CheckerProgram, node *ast.Node) *ast.Symbol {
	tracing.CountBy("host.symbolAtLocation", 1)
	symbol := p.Checker.GetSymbolAtLocation(node)
	if symbol != nil && (symbol.Flags&ast.SymbolFlagsAlias) != 0 {
		tracing.CountBy("host.aliasedSymbol", 1)
		symbol = p.Checker.GetAliasedSymbol(symbol)
	}
	return symbol
}

// UserSourceFiles is userSourceFiles in the TS source: every file the
// program holds that a user wrote — the entry, its imports, and
// theirs; declaration files (the default library) are not user
// statements.
func UserSourceFiles(p *program.CheckerProgram) []*ast.SourceFile {
	var out []*ast.SourceFile
	for _, file := range p.Program.SourceFiles() {
		if !file.IsDeclarationFile {
			out = append(out, file)
		}
	}
	return out
}

// ResolvesToDefaultLib is resolvesToDefaultLib in the TS source: does
// this identifier resolve to a declaration in a DEFAULT library file
// (the global Number, Math, …)? Symbols, never names.
func ResolvesToDefaultLib(p *program.CheckerProgram, node *ast.Node) bool {
	tracing.CountBy("host.symbolAtLocation", 1)
	symbol := p.Checker.GetSymbolAtLocation(node)
	tracing.CountBy("host.symbolInDefaultLib", 1)
	return p.Checker.SymbolInDefaultLib(symbol)
}

// SymbolEntirelyInDefaultLib is symbolEntirelyInDefaultLib in the TS
// source: whether EVERY declaration of a symbol lives in the default
// library (and there is at least one) — the stricter gate a user
// augmentation of a global must defeat.
func SymbolEntirelyInDefaultLib(p *program.CheckerProgram, symbol *ast.Symbol) bool {
	if symbol == nil {
		return false
	}
	tracing.CountBy("host.symbolInDefaultLib", 1)
	return p.Checker.SymbolEntirelyInDefaultLib(symbol)
}
