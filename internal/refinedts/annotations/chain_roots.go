// Whether an expression roots in a schema namespace — the surface
// or a registered library adapter — by symbol resolution, never
// name matching. Compilation re-resolves; this is the cheap
// pre-test coverage, hover, and the file cache ask.
//
// Ported 1:1 from annotations/chain_roots.ts. resolvesToSurface,
// libraryAdapterOfNode, and resolvesToAnnotationRoot are inlined here
// from service/program_resolution.ts (out of scope for this port per
// PORT.md's adapter-layer rule) rather than in program_resolution.go,
// because every call site of the library-adapter-specific reads lives
// in this file and its siblings — mirroring typereading/type_node.go's
// symbolAt precedent for the plain resolution helpers.

package annotations

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations/libraryadapters"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
)

// chainRoot is chainRoot in the TS source: the leftmost root
// identifier of a chain, or nil.
func chainRoot(expr *ast.Node) *ast.Node {
	e := expr
	for {
		if ast.IsCallExpression(e) {
			e = e.AsCallExpression().Expression
		} else if ast.IsPropertyAccessExpression(e) {
			e = e.AsPropertyAccessExpression().Expression
		} else if ast.IsParenthesizedExpression(e) {
			e = e.AsParenthesizedExpression().Expression
		} else {
			break
		}
	}
	if ast.IsIdentifier(e) {
		return e
	}
	return nil
}

// resolvesToSurface is resolvesToSurface in the TS source
// (service/program_resolution.ts): does this identifier (followed
// through import aliases) declare in the surface module? THE
// recognition question -- symbols, never names.
func resolvesToSurface(p *program.CheckerProgram, node *ast.Node) bool {
	symbol := p.Checker.GetSymbolAtLocation(node)
	if symbol == nil {
		return false
	}
	if (symbol.Flags & ast.SymbolFlagsAlias) != 0 {
		symbol = p.Checker.GetAliasedSymbol(symbol)
	}
	for _, d := range symbol.Declarations {
		if p.SurfacePaths[ast.GetSourceFileOfNode(d).FileName()] {
			return true
		}
	}
	return false
}

// libraryAdapterOfNode is libraryAdapterOfNode in the TS source: the
// library adapter this identifier resolves into -- a third-party
// schema package the checker reads as the annotation language it is
// -- or nil. What a library adapter compiles, compiles identically to
// the surface; what the checker cannot honor is not-an-annotation
// (plain TypeScript, untouched), never refused loudly the way the
// checker's own surface is.
func libraryAdapterOfNode(p *program.CheckerProgram, node *ast.Node) *libraryadapters.LibraryAdapter {
	symbol := p.Checker.GetSymbolAtLocation(node)
	if symbol == nil {
		return nil
	}
	if (symbol.Flags & ast.SymbolFlagsAlias) != 0 {
		symbol = p.Checker.GetAliasedSymbol(symbol)
	}
	for _, declaration := range symbol.Declarations {
		adapter := libraryadapters.LibraryAdapterOfFile(ast.GetSourceFileOfNode(declaration).FileName())
		if adapter != nil {
			return adapter
		}
	}
	return nil
}

// resolvesToAnnotationRoot is resolvesToAnnotationRoot in the TS
// source: the recognition question the annotation readers ask -- the
// checker's own surface, or a registered libraryAdapter.
func resolvesToAnnotationRoot(p *program.CheckerProgram, node *ast.Node) bool {
	return resolvesToSurface(p, node) || libraryAdapterOfNode(p, node) != nil
}

// RootsInSurface is rootsInSurface in the TS source: is this
// expression a chain whose leftmost root is an annotation namespace
// -- the surface, or the zod library adapter?
func RootsInSurface(p *program.CheckerProgram, expr *ast.Node) bool {
	root := chainRoot(expr)
	return root != nil && resolvesToAnnotationRoot(p, root)
}

// rootsInLibraryAdapter is rootsInLibraryAdapter in the TS source: is
// this chain a registered library adapter specifically? A
// library-adapter chain the checker cannot read is plain TypeScript,
// never a loud unsupported.
func rootsInLibraryAdapter(p *program.CheckerProgram, expr *ast.Node) bool {
	root := chainRoot(expr)
	return root != nil && libraryAdapterOfNode(p, root) != nil
}

// RootsInLibraryAdapter is the exported spelling of
// rootsInLibraryAdapter — the hover answer path (service) asks it to
// choose the schema-type-name prefix and the library grade.
func RootsInLibraryAdapter(p *program.CheckerProgram, expr *ast.Node) bool {
	return rootsInLibraryAdapter(p, expr)
}

// ResolvesToAnnotationRoot is the exported spelling of
// resolvesToAnnotationRoot — the hover answer path (service) asks it
// for unread-chain detection (a chain rooted in an annotation module
// that neither registry compiled).
func ResolvesToAnnotationRoot(p *program.CheckerProgram, node *ast.Node) bool {
	return resolvesToAnnotationRoot(p, node)
}

// libraryAdapterNameOfChain is libraryAdapterNameOfChain in the TS
// source: the library adapter a chain roots in, by name -- "" for the
// surface.
func libraryAdapterNameOfChain(p *program.CheckerProgram, expr *ast.Node) string {
	root := chainRoot(expr)
	if root == nil {
		return ""
	}
	adapter := libraryAdapterOfNode(p, root)
	if adapter == nil {
		return ""
	}
	return adapter.Name
}

// shapeOfResult is the {names, root} | null union shapeOf returns.
type shapeOfResult struct {
	Names []string
	Root  *ast.Node
	Ok    bool
}

// shapeOf is shapeOf in the TS source: the declared key names of an
// object-schema receiver -- a direct z.object/strictObject/
// looseObject call, or a const bound to one -- read syntactically:
// the same names zod's keyof() enumerates, plus the shape call's root
// for library-adapter resolution. Not ok where the shape is not in
// view or a key is not a plain name.
func shapeOf(p *program.CheckerProgram, receiver *ast.Node) shapeOfResult {
	e := receiver
	if ast.IsParenthesizedExpression(e) {
		e = e.AsParenthesizedExpression().Expression
	}
	if ast.IsIdentifier(e) {
		symbol := symbolAt(p.Checker, e)
		if symbol == nil {
			return shapeOfResult{}
		}
		declaration := symbol.ValueDeclaration
		if declaration == nil || !ast.IsVariableDeclaration(declaration) {
			return shapeOfResult{}
		}
		varDecl := declaration.AsVariableDeclaration()
		if varDecl.Initializer == nil || !ast.IsVariableDeclarationList(declaration.Parent) ||
			(declaration.Parent.Flags&ast.NodeFlagsConst) == 0 {
			return shapeOfResult{}
		}
		e = varDecl.Initializer
	}
	if !ast.IsCallExpression(e) || !ast.IsPropertyAccessExpression(e.AsCallExpression().Expression) {
		return shapeOfResult{}
	}
	access := e.AsCallExpression().Expression.AsPropertyAccessExpression()
	constructor := access.Name().Text()
	if constructor != "object" && constructor != "strictObject" && constructor != "looseObject" {
		return shapeOfResult{}
	}
	root := access.Expression
	if !ast.IsIdentifier(root) || !resolvesToAnnotationRoot(p, root) {
		return shapeOfResult{}
	}
	args := e.AsCallExpression().Arguments.Nodes
	if len(args) == 0 || !ast.IsObjectLiteralExpression(args[0]) {
		return shapeOfResult{}
	}
	var names []string
	for _, property := range args[0].AsObjectLiteralExpression().Properties.Nodes {
		if !ast.IsPropertyAssignment(property) {
			return shapeOfResult{} // a shape the reader cannot enumerate
		}
		name := property.AsPropertyAssignment().Name()
		if ast.IsIdentifier(name) {
			names = append(names, name.AsIdentifier().Text)
			continue
		}
		if ast.IsStringLiteral(name) {
			names = append(names, name.AsStringLiteral().Text)
			continue
		}
		return shapeOfResult{}
	}
	return shapeOfResult{Names: names, Root: root, Ok: true}
}

// RootsInObject is the exported spelling of rootsInObject — the hover
// answer path (service) asks it before compiling an inline object
// schema at a property assignment.
func RootsInObject(p *program.CheckerProgram, expr *ast.Node) bool {
	return rootsInObject(p, expr)
}

// rootsInObject is rootsInObject in the TS source: is this expression
// a `z.object({...})` statement -- possibly wrapped in `.refine(...)`
// calls, which state predicates over the same object?
func rootsInObject(p *program.CheckerProgram, expr *ast.Node) bool {
	cursor := expr
	for ast.IsCallExpression(cursor) &&
		ast.IsPropertyAccessExpression(cursor.AsCallExpression().Expression) &&
		cursor.AsCallExpression().Expression.AsPropertyAccessExpression().Name().Text() == "refine" {
		cursor = cursor.AsCallExpression().Expression.AsPropertyAccessExpression().Expression
	}
	if !ast.IsCallExpression(cursor) || !ast.IsPropertyAccessExpression(cursor.AsCallExpression().Expression) {
		return false
	}
	access := cursor.AsCallExpression().Expression.AsPropertyAccessExpression()
	name := access.Name().Text()
	return (name == "object" ||
		// zod's strict form differs only at runtime (extra keys throw
		// instead of stripping) -- the stated GRAPH is identical
		name == "strictObject") &&
		ast.IsIdentifier(access.Expression) &&
		resolvesToAnnotationRoot(p, access.Expression)
}

// readsAsDerivedAnnotation is readsAsDerivedAnnotation in the TS
// source: a chain DERIVED from a schema constant that still denotes
// an annotation -- today exactly `.keyof()` over a shape in view. The
// facts pass admits these where the root test misses them, and
// admits only what will compile, so library-adapter statements never
// gain a loud unsupported.
func readsAsDerivedAnnotation(p *program.CheckerProgram, e *ast.Node) bool {
	if !ast.IsCallExpression(e) || !ast.IsPropertyAccessExpression(e.AsCallExpression().Expression) {
		return false
	}
	access := e.AsCallExpression().Expression.AsPropertyAccessExpression()
	if access.Name().Text() != "keyof" || len(e.AsCallExpression().Arguments.Nodes) != 0 {
		return false
	}
	return shapeOf(p, access.Expression).Ok
}
