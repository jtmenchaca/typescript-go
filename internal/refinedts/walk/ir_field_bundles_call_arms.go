// split from ir_field_bundles.go — the arms that call through the receiver

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
)

// visitNestedFunction: a nested function's body runs at a time this
// scan cannot place — a receiver mentioned inside carries the bundle
// out of sight, UNLESS every mention is a plain READ of a declared
// field: a read moves nothing, so it may interleave at any later time
// without invalidating any belief the body holds. Those reads are
// recorded as the body's own; anything else inside — a write, a method
// call (whose body may write), a bare mention, a computed access —
// keeps the escape.
//
// `this` is the one spelling that does not always cross: only an ARROW
// keeps the enclosing `this`, while a function expression, a function
// declaration, a method, and a class body all rebind it, so a `this`
// inside one of those denotes some other object entirely and is none of
// this bundle's business. A NAMED receiver crosses into every nested
// form — a closure over `wrapper` is still that wrapper.
func (s *fieldCensusScan) visitNestedFunction(node *ast.Node) bool {
	if !ast.IsFunctionLike(node) && !ast.IsClassLike(node) {
		return false
	}
	rebindsThis := !ast.IsArrowFunction(node)
	if s.receiverName == "this" && rebindsThis {
		return true
	}
	if !mentionsReceiver(node, s.receiverName) {
		return true
	}
	if reads, called, admissible := captureMentions(s.c, node, s.isReceiver, s.byName); admissible {
		for _, name := range reads {
			s.noteRead(name)
		}
		for _, method := range called {
			s.noteCapturedMethodCall(method)
		}
		return true
	}
	s.census.Escapes = true
	return true
}

// visitDeferredMethodBind: `this.onData.bind(this)` — a DEFERRED method
// call: the bound function may run at any later time, exactly like a
// closure calling the method, and is collected the same way. The shape
// is exact: callee `<receiver>.<m>.bind`, first argument the receiver
// itself; anything looser falls through to the rules below it.
func (s *fieldCensusScan) visitDeferredMethodBind(node *ast.Node) bool {
	method, isBind := receiverMethodBindOf(node, s.isReceiver)
	if !isBind {
		return false
	}
	s.noteCapturedMethodCall(method)
	consumeBindMentions(s.consumed, node)
	// partial-application arguments past the bound receiver are
	// ordinary expressions and may mention the receiver themselves
	for _, argument := range node.AsCallExpression().Arguments.Nodes[1:] {
		s.visit(argument)
	}
	return true
}

// visitReceiverMethodCall: a CALL whose callee is `<receiver>.<name>`:
// the name is a METHOD, which resolves through ContractBySymbol
// carrying its own summary, not a slot. So an undeclared name in callee
// position is not a missing slot and does not escape — but a DECLARED
// field called as a function (`this.handler()`) is a genuine read of
// that field. Only the callee step is accounted for here; the arguments
// walk on.
func (s *fieldCensusScan) visitReceiverMethodCall(node *ast.Node) bool {
	if !ast.IsCallExpression(node) {
		return false
	}
	call := node.AsCallExpression()
	if call.QuestionDotToken != nil {
		return false
	}
	callee := Unwrapped(call.Expression)
	name, isField := s.fieldAccessOf(callee)
	if !isField {
		return false
	}
	// the callee names a FIELD or a METHOD, and BOTH join the call
	// list. A method resolves through its declaration; a declared field
	// called as a function is a read of that field AND a call through
	// whatever the class stored in it, whose body may write fields of
	// its own — the write-set closure resolves either from the class's
	// own text (fieldValuedFunctionBodies).
	//
	// A `#sym:` spelling from the element-access arm lands here on the
	// same terms a dotted name does: the stable key names one member,
	// and nothing about the bracket makes it less placeable than a dot.
	s.noteRead(name)
	s.noteDirectMethodCall(name)
	consumeReceiver(s.consumed, Unwrapped(call.Expression))
	s.consumed[call.Expression] = struct{}{}
	for _, argument := range call.Arguments.Nodes {
		s.visit(argument)
	}
	return true
}

// visitReturnsSelf: `return this` — the fluent-builder tail. Nothing
// moves during the body; the serving seams carry the caller-alias
// requirement (ReturnsSelf's doc). This-receivers only: a named
// receiver returned would need the parameter-bundle seams taught the
// same forgetting, which they are not.
func (s *fieldCensusScan) visitReturnsSelf(node *ast.Node) bool {
	if !ast.IsReturnStatement(node) {
		return false
	}
	returned := node.AsReturnStatement().Expression
	if returned == nil || Unwrapped(returned).Kind != ast.KindThisKeyword || s.receiverName != "this" {
		return false
	}
	s.census.ReturnsSelf = true
	s.consumed[returned] = struct{}{}
	s.consumed[Unwrapped(returned)] = struct{}{}
	return true
}

// visitBareMention: a bare mention of the receiver in any other
// position — an argument, a return value, an alias, an optional chain,
// the object of an undeclared step.
//
// A property NAME spelled like the receiver is not a mention: the
// `wrapper` in `holder.wrapper` is a step, not the object.
func (s *fieldCensusScan) visitBareMention(node *ast.Node) bool {
	if !s.isReceiver(node) || isPropertyStepName(node) {
		return false
	}
	s.census.Escapes = true
	return true
}
