// THE type-reading interface: what a type states as an AbstractValue.
// Two adapters (syntax, resolved) join here. A caller that uses one
// adapter and returns is the next false unknown.

package typereading

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
)

// ReadDeclaredType is readDeclaredType in the TS source: syntax, then
// host fills every hole syntax left -- a null result, or an object
// missing keys the host names.
func ReadDeclaredType(c *checker.Checker, node *ast.Node, at *ast.Node) (abstractdomain.AbstractValue, bool) {
	fromSyntax, syntaxOk := ReadTypeNode(c, node, at, 0)
	fromHost, hostOk := ReadHostType(c, TypeAtLocation(c, at), at, 0)
	return CompleteWithHost(fromSyntax, syntaxOk, fromHost, hostOk)
}

// CompleteWithHost is completeWithHost in the TS source.
func CompleteWithHost(
	fromSyntax abstractdomain.AbstractValue, syntaxOk bool,
	fromHost abstractdomain.AbstractValue, hostOk bool,
) (abstractdomain.AbstractValue, bool) {
	if !syntaxOk {
		return fromHost, hostOk
	}
	if !hostOk {
		return fromSyntax, true
	}
	if fromSyntax.Kind == abstractdomain.KindObject && fromHost.Kind == abstractdomain.KindObject {
		return FillObjectKeys(fromSyntax, fromHost), true
	}
	return fromSyntax, true
}
