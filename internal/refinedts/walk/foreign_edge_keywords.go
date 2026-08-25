// The child_process callee test, the options-object keywords
// (input/encoding) execFileSync/spawnSync/execSync's second argument
// carries, and the small string-literal/JSON.stringify readers those
// keyword checks share.

package walk

import (
	"path/filepath"

	"github.com/microsoft/typescript-go/internal/ast"
)

// resolvesToChildProcessMember is the callee test, widened past
// execFileSync alone: the name is memberName, and its symbol declares
// in a DECLARATION FILE whose path names child_process. That is the
// same shape resolvesToDefaultLib tests for the built-ins (a symbol's
// declaring file), widened to the one ambient module this edge
// consumes — `child_process` is not in the default lib, it arrives
// with @types/node, so the default-lib test answers false for it and
// cannot be reused unchanged.
//
// Both spellings resolve: the named import `import { <memberName> } from
// "node:child_process"` (an identifier callee, followed through the
// import alias by symbolAt) and the namespace form
// `childProcess.<memberName>(...)` (a property access, whose NAME node
// carries the same symbol). spawnSync and execSync are the same
// child_process export shape as execFileSync, so one test serves all
// three names.
func resolvesToChildProcessMember(ctx *FlowContext, callee *ast.Node, memberName string) bool {
	if ctx == nil || ctx.P == nil || callee == nil {
		return false
	}
	var name *ast.Node
	if ast.IsIdentifier(callee) {
		name = callee
	} else if ast.IsPropertyAccessExpression(callee) {
		name = callee.AsPropertyAccessExpression().Name()
	} else {
		return false
	}
	if name.Text() != memberName {
		return false
	}
	symbol := symbolAt(ctx.P.Checker, name)
	if symbol == nil {
		return false
	}
	for _, declaration := range symbol.Declarations {
		declaredFile := ast.GetSourceFileOfNode(declaration)
		if declaredFile == nil || !declaredFile.IsDeclarationFile {
			continue
		}
		if isChildProcessDeclarationPath(declaredFile.FileName()) {
			return true
		}
	}
	return false
}

// isChildProcessDeclarationPath is whether a declaration file is
// child_process's: `.../child_process.d.ts` under any @types root. The
// test is on the file's BASE NAME, so a vendored or pnpm-nested copy
// reads the same.
func isChildProcessDeclarationPath(fileName string) bool {
	base := filepath.Base(fileName)
	return base == "child_process.d.ts" || base == "child_process.d.mts" ||
		base == "child_process.d.cts"
}

// execFileSyncOptionsOf reads the options object shared by execFileSync,
// spawnSync, and execSync's own second argument: the `input` property
// whose value is `JSON.stringify(<payload>)`, and the `encoding`
// property whose word makes the result a string. execSync's own
// command string carries no `input` — it reads only for the encoding
// half there, and its payload comes from `child.stdin.write` instead
// where a caller writes one.
//
// Answers the payload expression (nil where input is absent or is not a
// stringify), whether the encoding admits a string result, and a
// sentence for the one case that is neither: an options argument the
// reader cannot see into at all.
func execFileSyncOptionsOf(argument *ast.Node) (*ast.Node, bool, string) {
	options := Unwrapped(argument)
	if options == nil || !ast.IsObjectLiteralExpression(options) {
		return nil, false, "this call's options are not written out as an object " +
			"literal, so the checker cannot see what crosses on stdin or whether the result " +
			"is text — no edge is modeled here"
	}
	var payload *ast.Node
	encodingOk := false
	for _, property := range options.AsObjectLiteralExpression().Properties.Nodes {
		if !ast.IsPropertyAssignment(property) {
			continue
		}
		assignment := property.AsPropertyAssignment()
		key := assignment.Name()
		if key == nil || !ast.IsIdentifier(key) {
			continue
		}
		switch key.Text() {
		case "input":
			if inner, ok := jsonStringifyArgumentOf(assignment.Initializer); ok {
				payload = inner
			}
		case "encoding":
			if word, ok := stringLiteralText(assignment.Initializer); ok && stringEncodings[word] {
				encodingOk = true
			}
		}
	}
	return payload, encodingOk, ""
}

// jsonStringifyArgumentOf reads `JSON.stringify(<expr>)` and answers
// the single argument. The receiver test is by name only here: the
// resolvesToDefaultLib check that would ground it belongs to the
// evaluation of that call, which the payload's own evaluation performs.
func jsonStringifyArgumentOf(expression *ast.Node) (*ast.Node, bool) {
	call := Unwrapped(expression)
	if call == nil || !ast.IsCallExpression(call) {
		return nil, false
	}
	callee := call.AsCallExpression().Expression
	if callee == nil || !ast.IsPropertyAccessExpression(callee) {
		return nil, false
	}
	access := callee.AsPropertyAccessExpression()
	if !ast.IsIdentifier(access.Expression) || access.Expression.Text() != "JSON" ||
		access.Name().Text() != "stringify" {
		return nil, false
	}
	arguments := call.AsCallExpression().Arguments
	if arguments == nil || len(arguments.Nodes) != 1 {
		return nil, false
	}
	return arguments.Nodes[0], true
}

// stringLiteralText is a written string literal's own text — a template
// with no substitution reads the same way, since both spell one fixed
// word.
func stringLiteralText(expression *ast.Node) (string, bool) {
	node := Unwrapped(expression)
	if node == nil {
		return "", false
	}
	if ast.IsStringLiteral(node) || node.Kind == ast.KindNoSubstitutionTemplateLiteral {
		return node.Text(), true
	}
	return "", false
}
