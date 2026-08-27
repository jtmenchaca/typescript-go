// Unchecked-channel gate: an any initializer, an ambient declare, or
// one named copy of either. The seed must not paper over these
// (boundary_exhibit shape A; PV2-012).

package typereading

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/tracing"
)

// ArrivedUnchecked is arrivedUnchecked in the TS source.
func ArrivedUnchecked(c *checker.Checker, at *ast.Node) bool {
	for node := at.Parent; node != nil; node = node.Parent {
		if ast.IsVariableDeclaration(node) {
			return UncheckedDeclaration(c, node, 0)
		}
		if !ast.IsBindingElement(node) && !ast.IsObjectBindingPattern(node) && !ast.IsArrayBindingPattern(node) {
			return false
		}
	}
	return false
}

// UncheckedDeclaration is uncheckedDeclaration in the TS source.
func UncheckedDeclaration(c *checker.Checker, declaration *ast.Node, depth int) bool {
	holder := declaration
	for ast.IsBindingElement(holder) || ast.IsObjectBindingPattern(holder) || ast.IsArrayBindingPattern(holder) {
		holder = holder.Parent
	}
	if !ast.IsVariableDeclaration(holder) {
		return false
	}
	variableDeclaration := holder.AsVariableDeclaration()
	if (ast.GetCombinedModifierFlags(holder) & ast.ModifierFlagsAmbient) != 0 {
		return true
	}
	if variableDeclaration.Initializer == nil {
		return false
	}
	if (TypeAtLocation(c, variableDeclaration.Initializer).Flags() & checker.TypeFlagsAny) != 0 {
		return true
	}
	if depth >= 4 || !ast.IsIdentifier(variableDeclaration.Initializer) {
		return false
	}
	tracing.CountBy("host.symbolAtLocation", 1)
	source := c.GetSymbolAtLocation(variableDeclaration.Initializer)
	if source == nil || source.ValueDeclaration == nil {
		return false
	}
	return UncheckedDeclaration(c, source.ValueDeclaration, depth+1)
}
