// A native (compiled) module import — the TypeScript-side rung 1 of
// the compiled-extension recognition ladder (ext.1,
// packages/cpp/findings/python-c-extension-boundary.md's own three-
// rung shape, restated here for Node's own native-addon loading
// idiom). Mirrors refinedpy's unmodeled_module_call_name/
// unmodeled_module_call exactly: a call into a module this checker
// carries no model for names ITSELF at the blocked position, rather
// than falling to UnmodeledCallResult's generic "has no body to
// read" wording.
//
// Node's own native-addon surfaces (no ECMA-262 vocabulary covers any
// of these — they are Node's own module-loading conventions):
//   - a static ESM/CJS import whose module specifier names a `.node`
//     file directly: `import addon from "./build/Release/addon.node"`.
//   - the two established native-loader packages, called immediately
//     on import: `require("bindings")("addon")`,
//     `require("node-gyp-build")(__dirname)` — both resolve and
//     require a `.node` file at runtime, opaque to this checker
//     either way.
package walk

import (
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
)

// nativeAddonLoaderModules names the two established native-loader
// package names — a require() of either, called immediately, resolves
// a .node file this checker cannot see inside.
var nativeAddonLoaderModules = map[string]bool{
	"bindings":       true,
	"node-gyp-build": true,
}

// isNativeAddonSpecifier is whether a module specifier string names a
// native addon: a literal `.node` file path, or one of the two
// established loader package names.
func isNativeAddonSpecifier(specifier string) bool {
	if strings.HasSuffix(specifier, ".node") {
		return true
	}
	return nativeAddonLoaderModules[specifier]
}

// importDeclarationModuleSpecifierText answers the string literal a
// declaration's own enclosing ImportDeclaration/ImportEqualsDeclaration
// states as its module specifier — "" when node has no such ancestor,
// or the ancestor's specifier is not a plain string literal (a
// computed specifier import assertion shape this reader does not
// resolve).
func importDeclarationModuleSpecifierText(node *ast.Node) (string, bool) {
	importDecl := ast.FindAncestorKind(node, ast.KindImportDeclaration)
	if importDecl != nil {
		specifier := importDecl.AsImportDeclaration().ModuleSpecifier
		if specifier != nil && ast.IsStringLiteral(specifier) {
			return specifier.Text(), true
		}
		return "", false
	}
	return "", false
}

// nativeModuleImportName is whether callee's own root identifier
// resolves to an ALIAS symbol (an import binding) whose declaration
// sits inside an ImportDeclaration naming a native addon specifier —
// answers the specifier text (the module name a decline names, mirroring
// diagnostic_sentences.rs's unmodeled_module_call("torch")'s own single-
// argument shape) and true, or ("", false) for every other callee: a
// bare local function, an import from an ordinary (non-native) module,
// or a callee with no resolvable symbol at all.
func nativeModuleImportName(ctx *FlowContext, callee *ast.Node) (string, bool) {
	root := callee
	for {
		if ast.IsPropertyAccessExpression(root) {
			root = root.AsPropertyAccessExpression().Expression
			continue
		}
		if ast.IsElementAccessExpression(root) {
			root = root.AsElementAccessExpression().Expression
			continue
		}
		break
	}
	if !ast.IsIdentifier(root) {
		return "", false
	}
	symbol := ctx.P.Checker.GetSymbolAtLocation(root)
	if symbol == nil || (symbol.Flags&ast.SymbolFlagsAlias) == 0 {
		return "", false
	}
	for _, declaration := range symbol.Declarations {
		specifier, ok := importDeclarationModuleSpecifierText(declaration)
		if !ok {
			continue
		}
		if isNativeAddonSpecifier(specifier) {
			return specifier, true
		}
	}
	return "", false
}

// NativeAddonRequireCallName is whether e is a call expression shaped
// `require("bindings")(...)` or `require("node-gyp-build")(...)` —
// Node's own CommonJS native-loader idiom, recognized on the CALL's
// own callee (a require(...) call expression) rather than through an
// import symbol, since a bare `require(...)` call carries no import
// binding at all. Answers the loader package name and true, or
// ("", false) for every other call shape.
func NativeAddonRequireCallName(e *ast.Node) (string, bool) {
	call := e.AsCallExpression()
	if call.Expression == nil || !ast.IsCallExpression(call.Expression) {
		return "", false
	}
	inner := call.Expression.AsCallExpression()
	if inner.Expression == nil || !ast.IsIdentifier(inner.Expression) || inner.Expression.Text() != "require" {
		return "", false
	}
	if inner.Arguments == nil || len(inner.Arguments.Nodes) != 1 {
		return "", false
	}
	arg := inner.Arguments.Nodes[0]
	if !ast.IsStringLiteral(arg) {
		return "", false
	}
	name := arg.Text()
	if nativeAddonLoaderModules[name] {
		return name, true
	}
	return "", false
}

// NativeModuleCallName is the one recognition this file exports: a
// call at e whose callee resolves to a native-addon import (a `.node`
// specifier, or one of the two loader packages named directly), OR
// whose whole shape is `require("bindings")(...)`/
// `require("node-gyp-build")(...)` — answers the module/loader name
// UnmodeledCallResult's reason note states, or ("", false) for every
// call this file does not recognize as native.
func NativeModuleCallName(ctx *FlowContext, e *ast.Node) (string, bool) {
	if name, ok := NativeAddonRequireCallName(e); ok {
		return name, true
	}
	call := e.AsCallExpression()
	if call.Expression == nil {
		return "", false
	}
	return nativeModuleImportName(ctx, call.Expression)
}
