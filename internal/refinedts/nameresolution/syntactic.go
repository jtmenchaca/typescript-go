// The syntax-only road from a written type name to the declaration it
// names: a top-level declaration in the same file, or one named
// import followed to the exporting file's own top-level declaration.
//
// The checker's resolver is NOT load-bearing for a written name
// (packages/refinedts/CHECKER-SEAMS.md, standing rule 1): under
// concurrent per-entry checkers over one shared program,
// GetSymbolAtLocation was measured intermittently dropping an
// imported alias, and the annotation readers' silent fallback then
// read a parameter written `Wide` as plain number (the A1 sweep
// corruption, 2026-08-24). Syntax cannot drift: the same bytes answer
// the same declaration on every run, whatever else shares the
// process.
package nameresolution

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
)

// DeclarationSymbolOf resolves an identifier to the BINDER's symbol
// for the declaration it names, using the binder's own scope tables:
// ascend the enclosing locals containers innermost-first — the first
// table holding the name is the binding scope, the binder's own rule,
// so shadowing resolves correctly — and follow one import edge
// syntactically when that binding is an import. The tables are
// written once at bind time (binder.BindSourceFile's BindOnce) and
// read-only after, so this answer cannot drift. Nil where the tables
// cannot answer (a global from lib.d.ts, an export-star re-export, a
// namespace member) — the caller falls back to the checker.
func DeclarationSymbolOf(program checker.Program, name *ast.Node) *ast.Symbol {
	if name == nil || !ast.IsIdentifier(name) {
		return nil
	}
	text := name.Text()
	for scope := name.Parent; scope != nil; scope = scope.Parent {
		locals := scope.Locals()
		if locals == nil {
			continue
		}
		symbol, held := locals[text]
		if !held || symbol == nil {
			continue
		}
		if (symbol.Flags & ast.SymbolFlagsAlias) != 0 {
			return importTargetSymbolOf(program, symbol)
		}
		return symbol
	}
	return nil
}

// importTargetSymbolOf follows an import binding's one edge to the
// exporting file's own symbol for the name: a namespace import
// answers the module file's symbol; a named import answers the
// target file's top-level binding under the export's name. Nil for
// every shape one syntactic edge cannot settle (a re-export chain, a
// default import, an export-star) — the caller falls back to the
// checker.
func importTargetSymbolOf(program checker.Program, alias *ast.Symbol) *ast.Symbol {
	if program == nil || alias == nil || len(alias.Declarations) == 0 {
		return nil
	}
	declaration := alias.Declarations[0]
	importDecl := ast.FindAncestorKind(declaration, ast.KindImportDeclaration)
	if importDecl == nil {
		return nil
	}
	specifier := importDecl.AsImportDeclaration().ModuleSpecifier
	if specifier == nil || !ast.IsStringLiteral(specifier) {
		return nil
	}
	file := ast.GetSourceFileOfNode(declaration)
	if file == nil {
		return nil
	}
	mode := program.GetEmitSyntaxForUsageLocation(file, specifier)
	resolved := program.GetResolvedModule(file, specifier.Text(), mode)
	if resolved == nil || !resolved.IsResolved() {
		return nil
	}
	target := program.GetSourceFileForResolvedModule(resolved.ResolvedFileName)
	if target == nil {
		return nil
	}
	if ast.IsNamespaceImport(declaration) {
		return target.AsNode().Symbol()
	}
	if !ast.IsImportSpecifier(declaration) {
		return nil
	}
	importSpecifier := declaration.AsImportSpecifier()
	exported := importSpecifier.Name().Text()
	if importSpecifier.PropertyName != nil {
		exported = importSpecifier.PropertyName.Text()
	}
	locals := target.AsNode().Locals()
	if locals == nil {
		return nil
	}
	symbol := locals[exported]
	if symbol != nil && (symbol.Flags&ast.SymbolFlagsAlias) != 0 {
		// a re-export chain: one syntactic edge is this resolver's
		// whole claim — the checker settles longer roads
		return nil
	}
	if symbol != nil && symbol.ExportSymbol != nil {
		// an exported member's declaration carries the EXPORTS-table
		// symbol (binder.declareModuleMember); the locals twin only
		// points to it — registries keyed by declaration symbols hold
		// the export symbol, so answer that one
		symbol = symbol.ExportSymbol
	}
	return symbol
}

// TypeDeclarationOf resolves an identifier written as a type name to
// the type declaration it names, by syntax alone: the file's own
// top-level declarations first, then one named-import edge into the
// exporting file's top-level declarations. Nil where syntax alone
// cannot see the declaration (a global, a namespace member, a
// re-export chain) — the caller falls back to the checker as before.
func TypeDeclarationOf(program checker.Program, name *ast.Node) *ast.Node {
	if program == nil || name == nil || !ast.IsIdentifier(name) {
		return nil
	}
	file := ast.GetSourceFileOfNode(name)
	if file == nil {
		return nil
	}
	text := name.Text()
	if local := topLevelTypeDeclarationIn(file, text); local != nil {
		return local
	}
	exportedName, specifier := namedImportOf(file, text)
	if specifier == nil {
		return nil
	}
	mode := program.GetEmitSyntaxForUsageLocation(file, specifier)
	resolved := program.GetResolvedModule(file, specifier.Text(), mode)
	if resolved == nil || !resolved.IsResolved() {
		return nil
	}
	target := program.GetSourceFileForResolvedModule(resolved.ResolvedFileName)
	if target == nil {
		return nil
	}
	return topLevelTypeDeclarationIn(target, exportedName)
}

// ValueDeclarationOf resolves an identifier written as a VALUE name
// (`typeof X`'s X) to the top-level declaration it names, by syntax
// alone: the file's own top-level variable and function declarations
// first, then one named-import edge into the exporting file's. Nil
// where syntax alone cannot see it — the caller falls back to the
// checker as before.
func ValueDeclarationOf(program checker.Program, name *ast.Node) *ast.Node {
	if program == nil || name == nil || !ast.IsIdentifier(name) {
		return nil
	}
	file := ast.GetSourceFileOfNode(name)
	if file == nil {
		return nil
	}
	text := name.Text()
	if local := topLevelValueDeclarationIn(file, text); local != nil {
		return local
	}
	exportedName, specifier := namedImportOf(file, text)
	if specifier == nil {
		return nil
	}
	mode := program.GetEmitSyntaxForUsageLocation(file, specifier)
	resolved := program.GetResolvedModule(file, specifier.Text(), mode)
	if resolved == nil || !resolved.IsResolved() {
		return nil
	}
	target := program.GetSourceFileForResolvedModule(resolved.ResolvedFileName)
	if target == nil {
		return nil
	}
	return topLevelValueDeclarationIn(target, exportedName)
}

// topLevelValueDeclarationIn finds the file's top-level variable
// declaration or function declaration named text.
func topLevelValueDeclarationIn(file *ast.SourceFile, text string) *ast.Node {
	for _, statement := range file.Statements.Nodes {
		if ast.IsFunctionDeclaration(statement) {
			if n := statement.Name(); n != nil && n.Text() == text {
				return statement
			}
			continue
		}
		if !ast.IsVariableStatement(statement) {
			continue
		}
		declarations := statement.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes
		for _, declaration := range declarations {
			n := declaration.Name()
			if n != nil && ast.IsIdentifier(n) && n.Text() == text {
				return declaration
			}
		}
	}
	return nil
}

// topLevelTypeDeclarationIn finds the file's top-level type alias,
// interface, or enum declaration named text.
func topLevelTypeDeclarationIn(file *ast.SourceFile, text string) *ast.Node {
	for _, statement := range file.Statements.Nodes {
		if !ast.IsTypeAliasDeclaration(statement) &&
			!ast.IsInterfaceDeclaration(statement) &&
			!ast.IsEnumDeclaration(statement) {
			continue
		}
		if n := statement.Name(); n != nil && n.Text() == text {
			return statement
		}
	}
	return nil
}

// namedImportOf finds the named-import binding for text and answers
// the exported name it aliases (the propertyName when the import
// renames) together with that import's module specifier.
func namedImportOf(file *ast.SourceFile, text string) (string, *ast.Node) {
	for _, statement := range file.Statements.Nodes {
		if !ast.IsImportDeclaration(statement) {
			continue
		}
		declaration := statement.AsImportDeclaration()
		specifier := declaration.ModuleSpecifier
		if specifier == nil || !ast.IsStringLiteral(specifier) || declaration.ImportClause == nil {
			continue
		}
		named := declaration.ImportClause.AsImportClause().NamedBindings
		if named == nil || !ast.IsNamedImports(named) {
			continue
		}
		for _, element := range named.AsNamedImports().Elements.Nodes {
			importSpecifier := element.AsImportSpecifier()
			if importSpecifier.Name() == nil || importSpecifier.Name().Text() != text {
				continue
			}
			exported := text
			if importSpecifier.PropertyName != nil {
				exported = importSpecifier.PropertyName.Text()
			}
			return exported, specifier
		}
	}
	return "", nil
}
