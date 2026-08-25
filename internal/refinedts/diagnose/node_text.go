// A node's text for a LOG LINE, never for a decision.
//
// ast.Node.Text panics on any kind it does not handle (a
// TypeReferenceNode among them — ast.go's own trailing panic), and a
// diagnostic that crashes the checker is worse than no diagnostic at
// all. NodeText answers the kind name instead of panicking, so a log
// call is safe on every node the walk can reach.
package diagnose

import (
	"fmt"

	"github.com/microsoft/typescript-go/internal/ast"
)

// NodeText is the node's own text where the AST spells one, and
// "<kind>" where it does not. Never panics.
func NodeText(node *ast.Node) (text string) {
	if node == nil {
		return "<nil>"
	}
	defer func() {
		if recover() != nil {
			text = "<" + node.Kind.String() + ">"
		}
	}()
	return node.Text()
}

// NodeLabel is the kind and the text together — what a trace line
// needs to identify a node without ambiguity.
func NodeLabel(node *ast.Node) string {
	if node == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%s:%s", node.Kind.String(), NodeText(node))
}
