// THE type-reading interface: what a type states as an AbstractValue.
// Two adapters (syntax, resolved) join here. A caller that uses one
// adapter and returns is the next false unknown.

package typereading

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/diagnose"
)

// ReadDeclaredType is readDeclaredType in the TS source: syntax, then
// host fills every hole syntax left -- a null result, or an object
// missing keys the host names.
func ReadDeclaredType(c *checker.Checker, node *ast.Node, at *ast.Node) (abstractdomain.AbstractValue, bool) {
	fromSyntax, syntaxOk := ReadTypeNode(c, node, at, 0)
	fromHost, hostOk := ReadHostType(c, TypeAtLocation(c, at), at, 0)
	joined, joinedOk := CompleteWithHost(fromSyntax, syntaxOk, fromHost, hostOk)
	if diagnose.EventOn("typeread.declared") {
		diagnose.Log("typeread.declared",
			"node", nodeText(node),
			"fromSyntaxOk", syntaxOk,
			"fromSyntax", inlineSpellingOrEmpty(fromSyntax, syntaxOk),
			"fromHostOk", hostOk,
			"fromHost", inlineSpellingOrEmpty(fromHost, hostOk),
			"joinedOk", joinedOk,
			"joined", inlineSpellingOrEmpty(joined, joinedOk),
		)
	}
	return joined, joinedOk
}

// nodeText is a short label for the declared type node: its syntax
// kind, plus the literal/identifier text where the node carries one
// (a type reference's name, a keyword) — enough to tell "Wide" from
// "number" in a log line without a full pretty-printer.
func nodeText(node *ast.Node) string {
	if node == nil {
		return "<nil>"
	}
	label := node.Kind.String()
	if ast.IsTypeReferenceNode(node) {
		if name := node.AsTypeReferenceNode().TypeName; name != nil {
			return label + ":" + name.Text()
		}
	}
	// Node.Text panics on the kinds it does not spell (a type
	// reference among them), so the log reads it through the safe
	// helper — a diagnostic never crashes the checker
	if text := diagnose.NodeText(node); text != "" && text != "<"+label+">" {
		return label + ":" + text
	}
	return label
}

// inlineSpellingOrEmpty spells an AbstractValue the hover way when the
// read succeeded; a failed read has nothing to spell.
func inlineSpellingOrEmpty(known abstractdomain.AbstractValue, ok bool) string {
	if !ok {
		return "<none>"
	}
	spelling, spelledOk := abstractdomain.FormatAbstractValueInline(known)
	if !spelledOk {
		return "unspellable"
	}
	return spelling
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
