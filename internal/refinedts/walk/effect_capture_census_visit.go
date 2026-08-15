// split from effect_capture_census.go — the walk over one closure body

package walk

import (
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
)

// visit walks one node of the closure body, noting the captures it
// spells and ending the census on a node no capture row stands for.
// It reports true once the census has ended, which is what stops the
// ForEachChild walk.
func (census *captureCensus) visit(node *ast.Node) bool {
	if census.declined || node == nil {
		return true
	}
	// a nested function's captures are ITS rows, and `this` is no
	// scalar entry — both end the census
	if ast.IsFunctionLike(node) || ast.IsClassLike(node) {
		census.declined = true
		return true
	}
	if node.Kind == ast.KindThisKeyword {
		census.declined = true
		return true
	}
	// a WRITE through a step on a captured name: a one-step declared
	// member is the object capture's own leaf write, and everything
	// else moves a place no row spells
	if census.capturedWriteRefuses(node) {
		census.declined = true
		return true
	}
	// a CALL: the callee's own steps are consumed, and every argument
	// that hands a captured name WHOLE to code declines — the callee
	// may write through the reference and no row carries that back
	if ast.IsCallExpression(node) || ast.IsNewExpression(node) {
		return census.visitCall(node)
	}
	// a property access's NAME half is not a read of a binding; the
	// ROOT is, and a captured root read through a declared PATH
	// (`stream.writableEnded`, `p.a.b`) is a LEAF of the object
	// capture. The path is what the caller's flattening spells, and
	// matching it is closureCapturesOf's business — a path the caller
	// never laid out has no slot there and refuses the whole capture.
	//
	// An OPTIONAL or COMPUTED step anywhere in the path still ends the
	// census: neither names a leaf, and an absent receiver is what no
	// leaf carries. (A `this`-rooted path falls to the `this` arm
	// above, which has already ended the census.)
	if ast.IsPropertyAccessExpression(node) {
		access := node.AsPropertyAccessExpression()
		if pathRoot, path, pathOk := propertyPathOf(node); pathOk {
			if _, isBound := census.bound[pathRoot]; !isBound && pathRoot != "this" {
				census.noteMember(pathRoot, strings.Join(path, "."))
				return false
			}
		} else if root := Unwrapped(access.Expression); root != nil &&
			ast.IsIdentifier(root) {
			// propertyPathOf refused the spelling — an optional step or a
			// non-identifier name somewhere in it. On a captured root that
			// is a place no leaf spells.
			if _, isBound := census.bound[root.Text()]; !isBound {
				census.declined = true
				return true
			}
		}
		census.visit(access.Expression)
		return false
	}
	if ast.IsElementAccessExpression(node) {
		access := node.AsElementAccessExpression()
		root := Unwrapped(access.Expression)
		if root != nil && ast.IsIdentifier(root) {
			if _, isBound := census.bound[root.Text()]; !isBound {
				census.declined = true
				return true
			}
		}
		census.visit(access.Expression)
		census.visit(access.ArgumentExpression)
		return false
	}
	if ast.IsIdentifier(node) {
		census.note(node.Text())
		return false
	}
	node.ForEachChild(census.visit)
	return false
}
