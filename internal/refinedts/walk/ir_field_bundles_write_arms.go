// split from ir_field_bundles.go — the arms that store into the bundle

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
)

// visitLoopBindingStore: a LOOP BINDING through an expression —
// `for (this.count of xs)` and `for (this.count in o)` store into their
// target once per iteration. The target is not an assignment
// expression, so the write forms never see it — without this the store
// reads as a plain access and the slot survives the loop unchanged.
//
// A for-of/for-in whose initializer is a DECLARATION binds fresh names
// and stores into nothing the bundle holds; only the expression form
// can name a field.
func (s *fieldCensusScan) visitLoopBindingStore(node *ast.Node) bool {
	if node.Kind != ast.KindForOfStatement && node.Kind != ast.KindForInStatement {
		return false
	}
	loop := node.AsForInOrOfStatement()
	if loop.Initializer != nil && !ast.IsVariableDeclarationList(loop.Initializer) {
		s.storePattern(loop.Initializer)
	} else {
		s.visit(loop.Initializer)
	}
	s.visit(loop.Expression)
	s.visit(loop.Statement)
	return true
}

// visitWriteForm: the WRITE forms, tried before the read rule — an
// assignment target is a write, not a read of the slot it stores into.
func (s *fieldCensusScan) visitWriteForm(node *ast.Node) bool {
	target, alsoReads, isWrite := writeFormTargetOf(node)
	if !isWrite || target == nil {
		return false
	}
	// a DESTRUCTURING target is a pattern of store positions, each of
	// which may name a field; a simple target is one store position
	if ast.IsObjectLiteralExpression(target) || ast.IsArrayLiteralExpression(target) {
		s.storePattern(target)
		// the source side is an ordinary expression
		if ast.IsBinaryExpression(node) {
			s.visit(node.AsBinaryExpression().Right)
		}
		return true
	}
	s.noteStoreTarget(target, alsoReads)
	node.ForEachChild(s.visit)
	return true
}

// visitObjectAssignStore: `Object.assign(this, source)` — a store into
// fields nothing names: the computed store's own shape. Every field may
// have moved, the declaration bounds the set, and ComputedWrite's
// all-field havoc bracketing stands for it — not an escape. The source
// arguments walk on as ordinary expressions.
func (s *fieldCensusScan) visitObjectAssignStore(node *ast.Node) bool {
	if !ast.IsCallExpression(node) {
		return false
	}
	assignCall := node.AsCallExpression()
	if assignCall.QuestionDotToken != nil || !ast.IsPropertyAccessExpression(assignCall.Expression) {
		return false
	}
	assignAccess := assignCall.Expression.AsPropertyAccessExpression()
	if assignAccess.QuestionDotToken != nil ||
		!ast.IsIdentifier(assignAccess.Expression) || assignAccess.Expression.Text() != "Object" ||
		!ast.IsIdentifier(assignAccess.Name()) || assignAccess.Name().Text() != "assign" ||
		assignCall.Arguments == nil || len(assignCall.Arguments.Nodes) == 0 ||
		!s.isReceiver(Unwrapped(assignCall.Arguments.Nodes[0])) {
		return false
	}
	s.census.Computed = true
	s.census.ComputedWrite = true
	s.consumed[assignCall.Arguments.Nodes[0]] = struct{}{}
	s.consumed[Unwrapped(assignCall.Arguments.Nodes[0])] = struct{}{}
	for _, argument := range assignCall.Arguments.Nodes[1:] {
		s.visit(argument)
	}
	return true
}
