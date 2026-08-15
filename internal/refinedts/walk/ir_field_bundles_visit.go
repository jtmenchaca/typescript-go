// split from ir_field_bundles.go — the walk that dispatches the arms

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
)

// visit is the body walk. Each arm below answers whether it ACCOUNTED
// for this node; the first one that does ends the node, exactly as the
// chain of early returns did, and a node no arm claims walks on into
// its children.
//
// The order is the rule order and is load-bearing: the write forms are
// tried before the read rule, because an assignment target is a write
// and not a read of the slot it stores into; the deferred bind is tried
// before the plain receiver call, because `this.m.bind(this)` is a call
// whose callee is a receiver access; and the bare mention is last,
// because every earlier arm consumes the receiver spelling it
// recognized.
func (s *fieldCensusScan) visit(node *ast.Node) bool {
	if node == nil {
		return false
	}
	if _, already := s.consumed[node]; already {
		// the outer form accounted for this node's receiver spelling; its
		// remaining children (a computed step's index, an assignment's
		// right side) still walk
		node.ForEachChild(s.visit)
		return false
	}
	switch {
	case s.visitNestedFunction(node):
	case s.visitDestructuringRead(node):
	case s.visitLoopBindingStore(node):
	case s.visitWriteForm(node):
	case s.visitComputedMemberRead(node):
	case s.visitReturnsSelf(node):
	case s.visitObjectAssignStore(node):
	case s.visitDeferredMethodBind(node):
	case s.visitReceiverMethodCall(node):
	case s.visitFieldRead(node):
	case s.visitBareMention(node):
	default:
		node.ForEachChild(s.visit)
	}
	return false
}
