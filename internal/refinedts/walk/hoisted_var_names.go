// Hoisted var names: every `var`-declared identifier that belongs to
// ONE function's scope, regardless of how deep inside its nested
// blocks the declaration sits. `var` hoists the BINDING to the
// enclosing function, but not the ASSIGNMENT — a read that runs before
// the declaration's own initializer statement executes sees the
// binding's initial value, `undefined`, exactly as a hoisted
// function-scope var reads in real JS.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
)

// HoistedVarNames collects every name a `var` declaration (patterns
// included) binds inside `body`, stopping at any nested function or
// class boundary — a nested function's own `var`s hoist to THAT
// function's scope, not this one's, so they must not seed here.
func HoistedVarNames(body *ast.Node) map[string]struct{} {
	names := map[string]struct{}{}
	var scan func(node *ast.Node)
	scan = func(node *ast.Node) {
		if node == nil {
			return
		}
		switch {
		// a nested function or class opens its OWN var scope — its
		// vars hoist there, not here
		case ast.IsFunctionDeclaration(node), ast.IsFunctionExpression(node),
			ast.IsArrowFunction(node), ast.IsMethodDeclaration(node),
			ast.IsGetAccessorDeclaration(node), ast.IsSetAccessorDeclaration(node),
			ast.IsConstructorDeclaration(node), ast.IsClassDeclaration(node),
			ast.IsClassExpression(node):
			return
		case ast.IsVariableStatement(node):
			declList := node.AsVariableStatement().DeclarationList
			flags := declList.Flags
			// no Let/Const flag means `var` — the same convention
			// blockScopedNames (block_statement.go) reads
			if (flags & (ast.NodeFlagsLet | ast.NodeFlagsConst)) != 0 {
				break
			}
			for _, d := range declList.AsVariableDeclarationList().Declarations.Nodes {
				hoistedBindingNames(d.AsVariableDeclaration().Name(), names)
			}
		}
		node.ForEachChild(func(child *ast.Node) bool {
			scan(child)
			return false
		})
	}
	scan(body)
	return names
}

// hoistedBindingNames is bindingNames' own reading (block_statement.go,
// syntactic_facts.go) — every leaf identifier a binding pattern names,
// recursing through array/object destructuring.
func hoistedBindingNames(name *ast.Node, into map[string]struct{}) {
	if name == nil {
		return
	}
	if ast.IsIdentifier(name) {
		into[name.Text()] = struct{}{}
		return
	}
	if ast.IsArrayBindingPattern(name) || ast.IsObjectBindingPattern(name) {
		for _, element := range name.AsBindingPattern().Elements.Nodes {
			if ast.IsOmittedExpression(element) || !ast.IsBindingElement(element) {
				continue
			}
			hoistedBindingNames(element.AsBindingElement().Name(), into)
		}
	}
}
