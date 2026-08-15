// split from effect_expression.go — the closure write set and the escape rule

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
)

// closureAssignedNames collects the names every function or class
// literal INSIDE a subtree can write — the write set of the closures the
// subtree hands to whoever receives its value.
//
// This is the other half of inertValue's boundary. That predicate stops
// at a function literal because building one runs nothing; this one
// walks the bodies it stopped at, because a closure that leaves this
// body may be called at a time the lowering cannot place, and every name
// it assigns is a name nothing here may believe afterwards.
//
// The reading is the syntactic one every write model in this package
// takes: an assignment target, a compound target, a ++/-- operand, and
// each of those through a destructuring pattern's leaves. Both spellings
// of a step are recorded — the plain identifier `total` and the one-step
// path `this.count` — because the slot vector holds each under its own
// name (SpelledNameOf's two forms). Over-collection is the safe
// direction: a name with no slot answers nothing, and a name whose
// closure never runs only costs the caller a decline.
func closureAssignedNames(node *ast.Node, into map[string]struct{}) {
	if node == nil {
		return
	}
	var noteTarget func(target *ast.Node)
	var inClosure func(child *ast.Node) bool
	noteTarget = func(target *ast.Node) {
		target = Unwrapped(target)
		if target == nil {
			return
		}
		// a DESTRUCTURING target is a pattern of store positions
		switch {
		case ast.IsObjectLiteralExpression(target):
			for _, property := range target.AsObjectLiteralExpression().Properties.Nodes {
				switch {
				case ast.IsPropertyAssignment(property):
					noteTarget(property.AsPropertyAssignment().Initializer)
				case ast.IsShorthandPropertyAssignment(property):
					noteTarget(property.AsShorthandPropertyAssignment().Name())
				case ast.IsSpreadAssignment(property):
					noteTarget(property.AsSpreadAssignment().Expression)
				}
			}
			return
		case ast.IsArrayLiteralExpression(target):
			for _, element := range target.AsArrayLiteralExpression().Elements.Nodes {
				if ast.IsSpreadElement(element) {
					noteTarget(element.AsSpreadElement().Expression)
					continue
				}
				noteTarget(element)
			}
			return
		}
		if spelled, ok := SpelledNameOf(target); ok {
			into[spelled] = struct{}{}
		}
	}
	// inClosure walks a closure's BODY: every write form inside it, and
	// on through the closures nested inside that one.
	inClosure = func(child *ast.Node) bool {
		if child == nil {
			return false
		}
		if ast.IsBinaryExpression(child) {
			binary := child.AsBinaryExpression()
			operator := binary.OperatorToken.Kind
			if operator >= ast.KindFirstAssignment && operator <= ast.KindLastAssignment {
				noteTarget(binary.Left)
			}
		}
		if ast.IsPrefixUnaryExpression(child) {
			unary := child.AsPrefixUnaryExpression()
			if unary.Operator == ast.KindPlusPlusToken || unary.Operator == ast.KindMinusMinusToken {
				noteTarget(unary.Operand)
			}
		}
		if ast.IsPostfixUnaryExpression(child) {
			unary := child.AsPostfixUnaryExpression()
			if unary.Operator == ast.KindPlusPlusToken || unary.Operator == ast.KindMinusMinusToken {
				noteTarget(unary.Operand)
			}
		}
		if ast.IsDeleteExpression(child) {
			noteTarget(child.AsDeleteExpression().Expression)
		}
		child.ForEachChild(inClosure)
		return false
	}
	// the OUTER walk finds the closures; their bodies go to inClosure
	var visit func(child *ast.Node) bool
	visit = func(child *ast.Node) bool {
		if child == nil {
			return false
		}
		if ast.IsFunctionLike(child) || ast.IsClassLike(child) {
			child.ForEachChild(inClosure)
			return false
		}
		child.ForEachChild(visit)
		return false
	}
	if ast.IsFunctionLike(node) || ast.IsClassLike(node) {
		node.ForEachChild(inClosure)
		return
	}
	node.ForEachChild(visit)
}

// closureWritesTracked answers whether a subtree hands over a closure
// that writes a name this lowering holds a slot for. A caller admitting
// a value whose closures escape asks this and keeps its decline when the
// answer is yes: the closure runs at a time no statement here places, so
// the slot it writes cannot be believed for the rest of the body and
// there is no statement position at which to say so.
//
// The membership test is the reader's HoldsPlace — slot membership with
// no sort filter, because a slot of ANY sort holds state a later closure
// run would falsify.
//
// A reader that supplies NO HoldsPlace has not said which names it
// holds, so this answers yes for any closure that writes at all. That is
// the conservative direction and it is the one the readers without a
// slot vector want: the loop solver reads names through a state map this
// seam does not see, and its own write guard (unknownIfPure) used to
// catch a closure-bearing literal only because the literal fell through
// to Opaque. Answering yes here keeps that decline exactly where it was.
// A closure that writes NOTHING is inert for every reader alike.
func closureWritesTracked(node *ast.Node, reader EffectReader) bool {
	written := map[string]struct{}{}
	closureAssignedNames(node, written)
	if len(written) == 0 {
		return false
	}
	if reader.HoldsPlace == nil {
		return true
	}
	for name := range written {
		if reader.HoldsPlace(name) {
			return true
		}
	}
	return false
}

// ClosureEscapesTrackedWrite is THE SHARED BOUNDARY RULE between the
// local census and the havoc floor, asked of a LoweringContext directly
// rather than through an effect reader.
//
// The two predicates draw the function boundary in opposite places, on
// purpose:
//
//   - collectSummaryLocals (ir_summary_body.go) STEPS OVER a nested
//     function, because its declarations are the inner function's and
//     laying out slots for them would give this body names it cannot
//     read;
//   - havocSlotsOfStatement (ir_opaque_havoc.go) walks INTO one, because
//     a closure closes over THIS body's names and calling it writes them.
//
// Those two answer the same question — which names of this body may a
// nested function move — only while every route that admits a statement
// holding a closure either falls to the floor or asks this predicate.
// A route that believes a slot value while handing over an arrow that
// writes that same slot is the disagreement, and it is unsound: the
// closure runs at a time no statement here places, so the belief cannot
// be retracted at any position.
//
// The reading is closureWritesTracked's, with slot membership answered
// from the context's own vector (slotIndexOfName), which is the same
// membership the census laid out and the floor havocs. A context with no
// slots answers false: there is no belief to falsify.
func ClosureEscapesTrackedWrite(context *LoweringContext, node *ast.Node) bool {
	if context == nil || node == nil {
		return false
	}
	return closureWritesTracked(node, EffectReader{
		HoldsPlace: func(spelled string) bool {
			_, found := slotIndexOfName(context, spelled)
			return found
		},
	})
}
