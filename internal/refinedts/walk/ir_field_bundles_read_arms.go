// split from ir_field_bundles.go — the arms that read out of the bundle

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
)

// visitDestructuringRead: `const { a, b } = this` — a destructuring
// READ of declared fields, wearing a pattern. Each plain element is the
// read of the field it names; a default, a rest, a computed key, or a
// nested pattern keeps the escape (the pattern reads shapes no slot
// spells).
func (s *fieldCensusScan) visitDestructuringRead(node *ast.Node) bool {
	if !ast.IsVariableDeclaration(node) {
		return false
	}
	d := node.AsVariableDeclaration()
	if d.Initializer == nil || !s.isReceiver(Unwrapped(d.Initializer)) ||
		d.Name() == nil || !ast.IsObjectBindingPattern(d.Name()) {
		return false
	}
	for _, element := range d.Name().AsBindingPattern().Elements.Nodes {
		binding := element.AsBindingElement()
		if binding.DotDotDotToken != nil || binding.Initializer != nil {
			s.census.Escapes = true
			return true
		}
		if !ast.IsIdentifier(binding.Name()) {
			s.census.Escapes = true
			return true
		}
		read := binding.Name().Text()
		if binding.PropertyName != nil {
			if !ast.IsIdentifier(binding.PropertyName) {
				s.census.Escapes = true
				return true
			}
			read = binding.PropertyName.Text()
		}
		if !s.noteRead(read) {
			// the pattern reads a member the field set never declared —
			// the spelling a GET ACCESSOR is read by (`const { age } =
			// this` runs the getter). The same deferral the dotted read
			// makes: reported, and Believable refuses it for every
			// consumer without the accessor fold.
			s.noteAccessorRead(read)
		}
	}
	s.consumed[Unwrapped(d.Initializer)] = struct{}{}
	s.consumed[d.Initializer] = struct{}{}
	return true
}

// visitComputedMemberRead: a member read on the receiver spelled with
// brackets. A STABLE SYMBOL const names one field, so it is the read of
// that field; every other key names none, and the access is computed.
func (s *fieldCensusScan) visitComputedMemberRead(node *ast.Node) bool {
	if !ast.IsElementAccessExpression(node) {
		return false
	}
	access := node.AsElementAccessExpression()
	if !s.isReceiver(Unwrapped(access.Expression)) {
		return false
	}
	if name, isField := s.fieldAccessOf(node); isField {
		if !s.noteRead(name) {
			// the class stores under this symbol without declaring a
			// member for it — the key still names ONE field, so the
			// bounded computed reading is what the access is worth
			s.census.Computed = true
		}
	} else {
		s.census.Computed = true
	}
	consumeReceiver(s.consumed, node)
	// the index expression still walks — it may mention the receiver
	// itself (`this[this.key]`), which is its own occurrence
	s.visit(access.ArgumentExpression)
	return true
}

// visitFieldRead: a plain `<receiver>.<field>` READ.
func (s *fieldCensusScan) visitFieldRead(node *ast.Node) bool {
	name, isField := s.fieldAccessOf(node)
	if !isField {
		return false
	}
	if !s.noteRead(name) {
		// the field set never declared this member — the spelling a GET
		// ACCESSOR is read by. Reported, not ruled on: the consumer that
		// resolves it to a getter declaration folds that body's census in,
		// and every other consumer refuses through Believable, exactly as
		// the escape refused before.
		s.noteAccessorRead(name)
	}
	consumeReceiver(s.consumed, node)
	return true
}
