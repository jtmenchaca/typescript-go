// The harness a target FILE runs as, when exported for the cross-
// language edge (docs/one-checker/reverse-pair.md, Half A step 4).
//
// THE ONE RECOGNIZED SHAPE (a pinned product decision for v1 — every
// deviation declines, never guesses):
//
//	console.log(JSON.stringify(<fn>(JSON.parse(readFileSync(0, "utf8")))));
//
// as a BARE TOP-LEVEL statement in the source file. There is no main
// guard here the way the Python reader's harness_call requires one:
// unlike a Python module, which a test suite or a sibling script can
// import, the TypeScript target file named by an execFileSync argv
// entry IS the script — node runs it top to bottom and nothing else
// imports it — so the statement being top-level is the whole
// condition; a guard would be modeling an import surface this
// artifact's premises never claim to hold.
//
// `readFileSync` must resolve to node:fs's own export: a named import
// (`import { readFileSync } from "node:fs"` or `"fs"`) or a namespace
// / default import used as `<ns>.readFileSync(0, "utf8")`, with the
// import statement itself naming "node:fs" or "fs" — never a locally
// declared function of the same name, which this reader has no reason
// to trust reads stdin at all.
//
// <fn> must be a bare identifier naming a function declared in the
// same file — HarnessCallOf hands that name back for its caller to
// look up; it does not resolve the declaration itself.
//
// Anything else — a guard around the statement, a second matching
// statement, `process.stdout.write` in place of `console.log`, a
// shadowed `readFileSync` — answers ("", false). The absence of a
// harness fact is the consumer's signal; HarnessCallOf never guesses
// a default in its place.
package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
)

// HarnessCallOf scans sourceFile's own top-level statements for the
// one recognized harness shape and answers the named function's
// identifier text. Answers ("", false) where zero or more than one
// top-level statement matches — a file that runs the shape twice
// states no single harness fact any more than a file that never runs
// it does.
func HarnessCallOf(sourceFile *ast.SourceFile) (calls string, ok bool) {
	if sourceFile == nil {
		return "", false
	}
	readFileSyncNames := readFileSyncBindingsOf(sourceFile)
	if len(readFileSyncNames) == 0 {
		return "", false
	}
	found := ""
	matches := 0
	for _, statement := range sourceFile.Statements.Nodes {
		if !ast.IsExpressionStatement(statement) {
			continue
		}
		name, matched := harnessShapeCallOf(statement.AsExpressionStatement().Expression, readFileSyncNames)
		if !matched {
			continue
		}
		found = name
		matches++
	}
	if matches != 1 {
		return "", false
	}
	return found, true
}

// readFileSyncBindingsOf answers every top-level name this file's own
// imports bind to node:fs's readFileSync: the named import's local
// name (`readFileSync`, or an aliased local from `readFileSync as
// rfs`), read as a bare call; and any namespace/default import's local
// name, read as `<local>.readFileSync(...)`. A second, unrelated
// `import fs from "some-other-package"` never enters this set — only
// clauses whose module specifier spells "node:fs" or "fs" do.
//
// Answers a set of BARE names (the named-import spelling) and a set
// of NAMESPACE receiver names (the default/namespace spelling) folded
// into one map: bare names map to true under the empty receiver "",
// namespace receivers map to true under their own name.
func readFileSyncBindingsOf(sourceFile *ast.SourceFile) map[string]bool {
	bindings := map[string]bool{}
	for _, statement := range sourceFile.Statements.Nodes {
		if !ast.IsImportDeclaration(statement) {
			continue
		}
		declaration := statement.AsImportDeclaration()
		specifier := declaration.ModuleSpecifier
		if specifier == nil || !ast.IsStringLiteral(specifier) {
			continue
		}
		word := specifier.Text()
		if word != "node:fs" && word != "fs" {
			continue
		}
		clause := declaration.ImportClause
		if clause == nil {
			continue
		}
		importClause := clause.AsImportClause()
		// a default import used as a namespace receiver: `import fs from
		// "node:fs"` read the same way `import * as fs` is below
		if importClause.Name() != nil {
			bindings[importClause.Name().Text()+"."] = true
		}
		named := importClause.NamedBindings
		if named == nil {
			continue
		}
		if ast.IsNamespaceImport(named) {
			bindings[named.AsNamespaceImport().Name().Text()+"."] = true
			continue
		}
		if !ast.IsNamedImports(named) {
			continue
		}
		for _, element := range named.AsNamedImports().Elements.Nodes {
			specifier := element.AsImportSpecifier()
			propertyName := specifier.PropertyName
			importedWord := specifier.Name().Text()
			if propertyName != nil {
				importedWord = propertyName.Text()
			}
			if importedWord == "readFileSync" {
				bindings[specifier.Name().Text()] = true
			}
		}
	}
	return bindings
}

// harnessShapeCallOf reads one expression as
// `console.log(JSON.stringify(<fn>(JSON.parse(<stdin read>))))`,
// answering <fn>'s name. Every layer must match exactly; any deviation
// answers ("", false).
func harnessShapeCallOf(expr *ast.Node, readFileSyncNames map[string]bool) (string, bool) {
	logged, ok := singleArgumentOfNamedCall(expr, "console", "log")
	if !ok {
		return "", false
	}
	stringified, ok := singleArgumentOfNamedCall(logged, "JSON", "stringify")
	if !ok {
		return "", false
	}
	called := Unwrapped(stringified)
	if called == nil || !ast.IsCallExpression(called) {
		return "", false
	}
	callExpression := called.AsCallExpression()
	callee := Unwrapped(callExpression.Expression)
	if callee == nil || !ast.IsIdentifier(callee) {
		return "", false
	}
	arguments, ok := callArguments(called)
	if !ok || len(arguments) != 1 {
		return "", false
	}
	parsed, ok := singleArgumentOfNamedCall(arguments[0], "JSON", "parse")
	if !ok {
		return "", false
	}
	if !isReadFileSyncStdinRead(parsed, readFileSyncNames) {
		return "", false
	}
	return callee.Text(), true
}

// isReadFileSyncStdinRead is whether expr is `readFileSync(0, "utf8")`
// (or `<ns>.readFileSync(0, "utf8")`) under one of the bindings
// readFileSyncBindingsOf found — the fd argument is the literal 0
// (stdin's own descriptor) and the encoding is the literal "utf8".
func isReadFileSyncStdinRead(expr *ast.Node, readFileSyncNames map[string]bool) bool {
	call := Unwrapped(expr)
	if call == nil || !ast.IsCallExpression(call) {
		return false
	}
	callExpression := call.AsCallExpression()
	callee := Unwrapped(callExpression.Expression)
	if callee == nil {
		return false
	}
	switch {
	case ast.IsIdentifier(callee):
		if !readFileSyncNames[callee.Text()] {
			return false
		}
	case ast.IsPropertyAccessExpression(callee):
		access := callee.AsPropertyAccessExpression()
		if !ast.IsIdentifier(access.Expression) || access.Name().Text() != "readFileSync" {
			return false
		}
		if !readFileSyncNames[access.Expression.Text()+"."] {
			return false
		}
	default:
		return false
	}
	arguments, ok := callArguments(call)
	if !ok || len(arguments) != 2 {
		return false
	}
	fd, fdOk := NumberOf(Unwrapped(arguments[0]))
	if !fdOk || fd != 0 {
		return false
	}
	encoding, encodingOk := stringLiteralText(arguments[1])
	return encodingOk && encoding == "utf8"
}

// singleArgumentOfNamedCall reads expr as `<object>.<method>(<arg>)` —
// exactly one positional argument, no other shape — and answers that
// one argument. The shared "single positional argument of a named
// call" peeling idiom foreign_edge.go's jsonStringifyArgumentOf and
// resolvesToChildProcessExecFileSync's callee test both spell
// separately; this file's own calls (console.log, JSON.stringify,
// JSON.parse) fold to the same shape, so the one helper serves all
// three rather than repeating the peel.
func singleArgumentOfNamedCall(expr *ast.Node, object string, method string) (*ast.Node, bool) {
	call := Unwrapped(expr)
	if call == nil || !ast.IsCallExpression(call) {
		return nil, false
	}
	callee := call.AsCallExpression().Expression
	if callee == nil || !ast.IsPropertyAccessExpression(callee) {
		return nil, false
	}
	access := callee.AsPropertyAccessExpression()
	if !ast.IsIdentifier(access.Expression) || access.Expression.Text() != object ||
		access.Name().Text() != method {
		return nil, false
	}
	arguments, ok := callArguments(call)
	if !ok || len(arguments) != 1 {
		return nil, false
	}
	return arguments[0], true
}
