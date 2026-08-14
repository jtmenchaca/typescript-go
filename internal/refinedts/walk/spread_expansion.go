// Reading a spread argument whose source is an EXACT sequence.
//
// A spread hands each element of its source to the call as its own
// argument (tmp/ecma262/spec.html sec-argument-lists: ArgumentList :
// ... AssignmentExpression appends every iterated value in order). So
// a call site can count and place its arguments exactly when the
// spread's source holds a known length and known items, and cannot
// when it does not.
//
// Two held shapes are exact, the same two the array literal and the
// call-argument expansion already read: a "values" tuple worn as an
// array, whose items are its numbers one apiece, and a LIST, whose
// items are its slots. Everything else — a set with a length window,
// an unknown, a collection — answers false, and each caller keeps the
// sentence it already spoke for a spread it could not place.
//
// EffectiveArgumentsOf below is the PLACEMENT seam built on that
// reading: it needs the arguments' held values, so it answers the
// positions a call's arguments really occupy. ArgumentNodesOf
// (inliner.go) stays the SYNTACTIC seam — it holds no values, so it
// answers one slot per written argument and cannot see past a spread.
// A reader wanting only syntax (which expressions did the programmer
// write here) asks ArgumentNodesOf; a reader placing a PARAMETER
// against an argument asks for the effective arguments.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// ExactSpreadItems reads the items a spread's already-evaluated source
// hands over, one per argument position. The caller evaluates the
// source expression itself and passes the held value, so an effectful
// expression is never walked twice.
func ExactSpreadItems(spread abstractdomain.AbstractValue) ([]abstractdomain.AbstractValue, bool) {
	// a tuple worn as an array: each number is one item, carrying the
	// tuple's own trust
	if spread.Kind == abstractdomain.KindValues && spread.KindTag == abstractdomain.PrimitiveArray {
		items := make([]abstractdomain.AbstractValue, 0, len(spread.Values))
		for _, v := range spread.Values {
			items = append(items, abstractdomain.KnownValues(
				[]float64{v},
				abstractdomain.PrimitiveNumber,
				abstractdomain.TrustLevelOf(spread),
			))
		}
		return items, true
	}
	// a list states its length in its slots
	if spread.Kind == abstractdomain.KindList {
		return spread.Items, true
	}
	return nil, false
}

// ExpandedArgumentItems reads one argument expression as the items it
// contributes to the call's argument list: a spread of an exact source
// contributes its elements, and any other expression contributes
// itself as one item. The second answer is false when the expression
// is a spread whose source is not exact — the count past it is not
// placeable, and the caller declines.
//
// evaluate is the caller's own evaluation of an expression, so each
// call site keeps its evaluation discipline and nothing here walks an
// expression the caller did not ask for.
func ExpandedArgumentItems(
	argument *ast.Node,
	evaluate func(*ast.Node) abstractdomain.AbstractValue,
) ([]abstractdomain.AbstractValue, bool) {
	if !ast.IsSpreadElement(argument) {
		return []abstractdomain.AbstractValue{evaluate(argument)}, true
	}
	return ExactSpreadItems(evaluate(argument.AsSpreadElement().Expression))
}

// ExactArgumentCount counts the argument positions a call's argument
// list occupies, expanding every exact spread. It answers false as
// soon as one spread's source is not exact, because every position
// after that one has moved by an unknown amount.
func ExactArgumentCount(
	arguments []*ast.Node,
	evaluate func(*ast.Node) abstractdomain.AbstractValue,
) (int, bool) {
	count := 0
	for _, argument := range arguments {
		if !ast.IsSpreadElement(argument) {
			count++
			continue
		}
		items, exact := ExactSpreadItems(evaluate(argument.AsSpreadElement().Expression))
		if !exact {
			return 0, false
		}
		count += len(items)
	}
	return count, true
}

// EffectiveArguments is the argument list a call actually hands its
// callee, one entry per PARAMETER POSITION rather than per written
// argument expression: a spread of an exact source occupies as many
// positions as it has items, and every other argument occupies one.
//
// Nodes and Knowns are built together, one append apiece, so entry i
// of one is always entry i of the other — a reader that maps a
// parameter index to an argument NODE and a reader that maps it to a
// VALUE land on the same argument. Nothing outside EffectiveArgumentsOf
// and the tagged-template construction appends to either slice, so the
// two lengths cannot drift.
//
// A Nodes entry is nil where the position has no caller expression
// behind it: an item expanded out of a spread's source, and a tagged
// template's synthesized template object. Every reader already treats a
// nil slot as "no caller state behind this position" and writes nothing
// back to it (ArgumentNodesOf's comment carries the same rule).
//
// Exact is false when some spread's source did not expand: that
// spread kept a single position holding residue, and every position
// after it has moved by an unknown amount, so no position can be
// placed and no rest list can state its length.
type EffectiveArguments struct {
	Nodes  []*ast.Node
	Knowns []abstractdomain.AbstractValue
	Exact  bool
}

// EffectiveArgumentsOf reads a call's written arguments as the
// positions they occupy. evaluate is the caller's own evaluation of an
// expression, so each call site keeps its evaluation discipline and
// every argument expression is walked exactly once, in source order.
//
// A non-spread argument contributes its own node and its own value. A
// spread whose held source expands (ExactSpreadItems) contributes one
// position per item, each with a NIL node: the item is an element of
// the source's value, not an expression the caller wrote, so there is
// no expression to write a parameter's final state back through. That
// loses nothing the walk had — a write-back through the SpreadElement
// node reached no arm of ForgetThrough either, so a spread argument's
// source never forgot at an inline and still does not. A spread that
// does not expand keeps its own single position holding residue and
// marks the whole list inexact.
func EffectiveArgumentsOf(
	arguments []*ast.Node,
	evaluate func(*ast.Node) abstractdomain.AbstractValue,
) EffectiveArguments {
	effective := EffectiveArguments{Exact: true}
	for _, argument := range arguments {
		if !ast.IsSpreadElement(argument) {
			effective.Nodes = append(effective.Nodes, argument)
			effective.Knowns = append(effective.Knowns, evaluate(argument))
			continue
		}
		items, exact := ExactSpreadItems(evaluate(argument.AsSpreadElement().Expression))
		if !exact {
			// an inexpansible spread loses its slots: it holds the one
			// position it was written as, and the count past it is unread
			effective.Nodes = append(effective.Nodes, argument)
			effective.Knowns = append(effective.Knowns, silence.Residue())
			effective.Exact = false
			continue
		}
		for _, item := range items {
			effective.Nodes = append(effective.Nodes, nil)
			effective.Knowns = append(effective.Knowns, item)
		}
	}
	return effective
}

// SyntacticSpreadLength is the element count of a spread's source read
// from SYNTAX alone, for the readers that hold a checker but no
// environment. It answers only for an array literal — written in
// place, or named by a const chain that ends in one — because a const
// bound to an array literal holds that array's LENGTH at every
// reachable point (the binding cannot be reassigned; a push would
// change the items, so no item is read here, only how many positions
// the spread occupies).
//
// A hole or a nested spread inside the literal answers false: the
// hole still occupies a position but the nested spread's own length is
// unread here, so the count past it would be a guess.
func SyntacticSpreadLength(c *checker.Checker, source *ast.Node) (int, bool) {
	literal := Unwrapped(source)
	if !ast.IsArrayLiteralExpression(literal) && c != nil && ast.IsIdentifier(literal) {
		initializer, ok := dataflowfacts.ConstInitializerOf(c, literal)
		if !ok {
			return 0, false
		}
		literal = Unwrapped(initializer)
	}
	if !ast.IsArrayLiteralExpression(literal) {
		return 0, false
	}
	elements := literal.AsArrayLiteralExpression().Elements.Nodes
	for _, element := range elements {
		if ast.IsSpreadElement(element) {
			return 0, false
		}
	}
	return len(elements), true
}

// ExactSyntacticArgumentCount counts a call's argument positions the
// syntactic way, for the readers with no environment: every plain
// argument is one position, and every spread contributes its source
// array's length. False as soon as one spread's length is unread.
func ExactSyntacticArgumentCount(c *checker.Checker, arguments []*ast.Node) (int, bool) {
	count := 0
	for _, argument := range arguments {
		if !ast.IsSpreadElement(argument) {
			count++
			continue
		}
		length, exact := SyntacticSpreadLength(c, argument.AsSpreadElement().Expression)
		if !exact {
			return 0, false
		}
		count += length
	}
	return count, true
}
