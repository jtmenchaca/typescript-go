// from service/token_at.ts
//
// The deepest node covering a position — the hover dispatcher's own
// token walk, ported per the locked decision (GO-LSP-EDITOR-PATH.md
// §15.8): half-open [getStart, end), descending to the deepest child.
// astnav's GetTouchingPropertyName (sticky at End) and
// GetTokenAtPosition (lands on trivia) are NOT this walk and are
// forbidden stand-ins for it.

package service

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/scanner"
)

// TokenAt is tokenAt in the TS source: the deepest node whose
// [start, end) covers the position, or nil when the file does not.
// getStart() (leading trivia skipped) is scanner.GetTokenPosOfNode.
func TokenAt(file *ast.SourceFile, position int) *ast.Node {
	root := file.AsNode()
	if position < scanner.GetTokenPosOfNode(root, file, false) || position >= root.End() {
		return nil
	}
	found := root
	for {
		var child *ast.Node
		found.ForEachChild(func(c *ast.Node) bool {
			if position >= scanner.GetTokenPosOfNode(c, file, false) && position < c.End() {
				child = c
				return true // first covering child wins, as in TS
			}
			return false
		})
		if child == nil {
			return found
		}
		found = child
	}
}
