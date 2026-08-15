// split from effect_expression.go — capture name spellings and bound names

package walk

import (
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
)

// splitOneStep reads a slot spelling as a HOLDER and the path below it:
// "p.a" answers ("p", "a", true) and "p.a.b" answers ("p", "a.b", true),
// while a bare name answers false. The write set holds both the bare and
// the stepped spelling of every member write, and this is what tells
// them apart.
//
// The member half keeps whatever depth it was written with, because the
// leaf vocabulary keeps that depth too: a caller's nested literal lays
// out the slot "p.a.b", and leafSlotsUnder hands it back under the path
// "a.b". Cutting at the FIRST dot is what makes the holder the holder;
// nothing downstream needs the member to be a single step.
func splitOneStep(spelled string) (root string, member string, ok bool) {
	cut := strings.Index(spelled, ".")
	if cut < 0 {
		return "", "", false
	}
	root, member = spelled[:cut], spelled[cut+1:]
	if root == "" || member == "" {
		return "", "", false
	}
	return root, member, true
}

// capturedStepWrite answers whether a write TARGET is a step on a name
// the closure did not bind — `p.a = 1` or `xs[i] = v` on a capture. The
// capture's entry holds the name's own slot, and a leaf underneath it is
// a different slot the row never names, so the census refuses.
//
// A write to the BARE captured name (`settled = true`) is not this: that
// is exactly the row's own write-back, and it is what the whole layout
// exists to carry.
func capturedStepWrite(target *ast.Node, bound map[string]struct{}) bool {
	head := Unwrapped(target)
	if head == nil {
		return false
	}
	var root *ast.Node
	switch {
	case ast.IsPropertyAccessExpression(head):
		root = Unwrapped(head.AsPropertyAccessExpression().Expression)
	case ast.IsElementAccessExpression(head):
		root = Unwrapped(head.AsElementAccessExpression().Expression)
	default:
		return false
	}
	for root != nil && ast.IsPropertyAccessExpression(root) {
		root = Unwrapped(root.AsPropertyAccessExpression().Expression)
	}
	if root == nil || !ast.IsIdentifier(root) {
		// a `this`-rooted or call-rooted step: the census's own `this` and
		// call arms already refuse those, so nothing more is claimed here
		return false
	}
	_, isBound := bound[root.Text()]
	return !isBound
}

// closureBoundNames is every name a closure BINDS itself: its
// parameters (through binding patterns), its body's own declarations,
// the names of nested functions and classes it declares, and each
// `catch` binding. A name in this set is the closure's own, so a use of
// it is not a capture and a write to it moves nothing the caller holds.
//
// Over-collection here is the SAFE direction for the read half (a name
// wrongly called bound simply gets no row, and the body then reads a
// slot nothing filled — which the layout gives unknown) and the UNSAFE
// direction for the write half, which is why the write half subtracts
// this set rather than being built from it: closureAssignedNames reports
// every assigned spelling, and only the ones this set does not claim
// cross the boundary.
func closureBoundNames(closure *ast.Node) map[string]struct{} {
	bound := map[string]struct{}{}
	var noteName func(name *ast.Node)
	noteName = func(name *ast.Node) {
		if name == nil {
			return
		}
		if ast.IsIdentifier(name) {
			bound[name.Text()] = struct{}{}
			return
		}
		if ast.IsObjectBindingPattern(name) || ast.IsArrayBindingPattern(name) {
			for _, element := range name.AsBindingPattern().Elements.Nodes {
				if !ast.IsBindingElement(element) {
					continue
				}
				noteName(element.AsBindingElement().Name())
			}
		}
	}
	for _, parameter := range closure.Parameters() {
		noteName(parameter.AsParameterDeclaration().Name())
	}
	// the closure's own name, where it has one — `function step() { …
	// step() … }` refers to itself, not to any caller binding
	if selfName := closure.Name(); selfName != nil && ast.IsIdentifier(selfName) {
		bound[selfName.Text()] = struct{}{}
	}
	body := closure.Body()
	if body == nil {
		return bound
	}
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if node == nil {
			return false
		}
		switch {
		case ast.IsVariableDeclaration(node):
			noteName(node.Name())
		case ast.IsFunctionDeclaration(node) || ast.IsClassDeclaration(node):
			noteName(node.Name())
		case ast.IsCatchClause(node):
			if variable := node.AsCatchClause().VariableDeclaration; variable != nil {
				noteName(variable.Name())
			}
		}
		node.ForEachChild(visit)
		return false
	}
	visit(body)
	return bound
}
