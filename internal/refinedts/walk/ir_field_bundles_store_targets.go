// split from ir_field_bundles.go — the store positions and what they move

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
)

// writeFormTargetOf: the `<receiver>.<field>` a write form writes
// through, if this node is one of the write forms.
func writeFormTargetOf(node *ast.Node) (target *ast.Node, alsoReads bool, ok bool) {
	if ast.IsBinaryExpression(node) {
		binary := node.AsBinaryExpression()
		operator := binary.OperatorToken.Kind
		if operator == ast.KindEqualsToken {
			return Unwrapped(binary.Left), false, true
		}
		if operator >= ast.KindFirstCompoundAssignment && operator <= ast.KindLastCompoundAssignment {
			// a compound write reads the old value before storing the new
			return Unwrapped(binary.Left), true, true
		}
		return nil, false, false
	}
	if ast.IsPrefixUnaryExpression(node) {
		unary := node.AsPrefixUnaryExpression()
		if unary.Operator == ast.KindPlusPlusToken || unary.Operator == ast.KindMinusMinusToken {
			return Unwrapped(unary.Operand), true, true
		}
		return nil, false, false
	}
	if ast.IsPostfixUnaryExpression(node) {
		unary := node.AsPostfixUnaryExpression()
		if unary.Operator == ast.KindPlusPlusToken || unary.Operator == ast.KindMinusMinusToken {
			return Unwrapped(unary.Operand), true, true
		}
		return nil, false, false
	}
	if ast.IsDeleteExpression(node) {
		return Unwrapped(node.AsDeleteExpression().Expression), false, true
	}
	return nil, false, false
}

// noteStoreTarget records ONE store position. A store into
// `<receiver>.<field>` is a write; a store into `<receiver>[e]` moves a
// field nothing names; a store through any other shape that MENTIONS
// the receiver puts it somewhere the census cannot follow.
//
// It answers whether the target was accounted for, so the caller knows
// not to walk it again as an ordinary expression — a store position
// visited as an expression reads as a plain READ, which is the exact
// wrong answer: the slot would keep its entry value past the store.
func (s *fieldCensusScan) noteStoreTarget(target *ast.Node, alsoReads bool) bool {
	target = Unwrapped(target)
	if target == nil {
		return false
	}
	if name, isField := s.fieldAccessOf(target); isField {
		switch {
		case s.noteWrite(name):
			if alsoReads {
				s.noteRead(name)
			}
		case ast.IsElementAccessExpression(target):
			// a SYMBOL-KEYED store whose field the set never declared —
			// the class stores under a symbol it declares no member for.
			// The key names ONE field, so nothing outside this receiver
			// moves, and the declaration still bounds the slots: the
			// bounded havoc ComputedWrite already stands for is the honest
			// report, not the whole-body escape an unnamed member gets.
			s.census.Computed = true
			s.census.ComputedWrite = true
		default:
			// a member the field set never declared: the spelling a SET
			// ACCESSOR is written by. The name is reported, not ruled on —
			// the consumer that resolves it to a setter declaration folds
			// that body's own census in, and every other consumer refuses
			// through Believable, exactly as the escape refused before. A
			// compound or update form also runs the GETTER first.
			s.noteAccessorStore(name)
			if alsoReads {
				s.noteAccessorRead(name)
			}
		}
		s.consumed[target] = struct{}{}
		consumeReceiver(s.consumed, target)
		return true
	}
	if ast.IsElementAccessExpression(target) &&
		s.isReceiver(Unwrapped(target.AsElementAccessExpression().Expression)) {
		// `this[k] = v`: the declaration bounds which slots could move,
		// but nothing names which one did — so no slot of this receiver
		// can be believed past this point
		s.census.Computed = true
		s.census.ComputedWrite = true
		consumeReceiver(s.consumed, target)
		return true
	}
	return false
}

// storePattern walks a DESTRUCTURING target — the `{ x: this.count }`
// of `({ x: this.count } = source)`, the `[this.count]` of
// `[this.count] = pair`, and the same shapes nested inside each other.
//
// Every leaf of such a pattern is a STORE position, not a read. The
// walk descends through the pattern's own structure and hands each
// leaf to noteStoreTarget; a leaf that is not a receiver access at all
// (a plain local, another object's field) is nobody's business here,
// and its own subexpressions — a computed key, a default's right side
// — are ordinary expressions and walk normally.
//
// It and visit call each other: a default value inside a pattern is an
// ordinary expression.
func (s *fieldCensusScan) storePattern(target *ast.Node) {
	target = Unwrapped(target)
	if target == nil {
		return
	}
	switch {
	case ast.IsObjectLiteralExpression(target):
		for _, property := range target.AsObjectLiteralExpression().Properties.Nodes {
			switch {
			case ast.IsPropertyAssignment(property):
				assignment := property.AsPropertyAssignment()
				// a computed key is an ordinary expression evaluated in place
				if name := assignment.Name(); name != nil && ast.IsComputedPropertyName(name) {
					s.visit(name.AsComputedPropertyName().Expression)
				}
				s.storePattern(assignment.Initializer)
			case ast.IsShorthandPropertyAssignment(property):
				// `({ count } = source)` stores into a LOCAL named count, never
				// into the receiver — but its default value is an expression
				if initializer := property.AsShorthandPropertyAssignment().ObjectAssignmentInitializer; initializer != nil {
					s.visit(initializer)
				}
			case ast.IsSpreadAssignment(property):
				s.storePattern(property.AsSpreadAssignment().Expression)
			default:
				s.visit(property)
			}
		}
	case ast.IsArrayLiteralExpression(target):
		for _, element := range target.AsArrayLiteralExpression().Elements.Nodes {
			if ast.IsSpreadElement(element) {
				s.storePattern(element.AsSpreadElement().Expression)
				continue
			}
			s.storePattern(element)
		}
	case ast.IsBinaryExpression(target) &&
		target.AsBinaryExpression().OperatorToken.Kind == ast.KindEqualsToken:
		// a DEFAULTED element: `{ x: this.count = 1 }` stores into
		// this.count when the source has no x, and evaluates 1 as an
		// ordinary expression
		binary := target.AsBinaryExpression()
		s.storePattern(binary.Left)
		s.visit(binary.Right)
	default:
		if s.noteStoreTarget(target, false) {
			return
		}
		// not a receiver access: a plain local, another object's field.
		// It still walks as an expression so a receiver mentioned inside
		// it (`other[this.key] = v`) is counted.
		s.visit(target)
	}
}
