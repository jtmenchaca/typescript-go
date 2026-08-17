// split from ir_array_slots.go — the BRIDGE: an array built from a
// flattened Map or Set, its syntax readers, its recognizer, and its
// declaration lowering

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
)

// arrayFromReceiverOf is the single argument of `Array.from(x)` — the
// call half of the bridge below. Answers nil for anything else,
// including a two-argument `Array.from(x, cb)`, whose mapping callback
// changes every element.
func arrayFromReceiverOf(call *ast.CallExpression) *ast.Node {
	if !ast.IsPropertyAccessExpression(call.Expression) {
		return nil
	}
	access := call.Expression.AsPropertyAccessExpression()
	if access.QuestionDotToken != nil {
		return nil
	}
	if !ast.IsIdentifier(access.Expression) || access.Expression.Text() != "Array" {
		return nil
	}
	if !ast.IsIdentifier(access.Name()) || access.Name().Text() != "from" {
		return nil
	}
	if call.Arguments == nil || len(call.Arguments.Nodes) != 1 {
		return nil
	}
	if ast.IsSpreadElement(call.Arguments.Nodes[0]) {
		return nil
	}
	return call.Arguments.Nodes[0]
}

// collectionSourceOf reads an expression as a COLLECTION the bridge can
// stand on: a bare Set `s`, or one of the Map views `m.values()` /
// `m.keys()`. Answers the collection's spelled name and which of its
// slots the resulting array's elements come from.
//
// `m.entries()` and a bare Map are NOT sources: their elements are
// pairs, and one element slot holds one scalar.
func collectionSourceOf(node *ast.Node) (name string, view string, ok bool) {
	head := Unwrapped(node)
	if ast.IsIdentifier(head) {
		// a bare name — a Set's members, or a Map, which the caller rules
		// out by asking whether it has a keys slot
		return head.Text(), "values", true
	}
	if !ast.IsCallExpression(head) {
		return "", "", false
	}
	property := head.AsCallExpression().Expression
	if !ast.IsPropertyAccessExpression(property) {
		return "", "", false
	}
	receiver := property.AsPropertyAccessExpression().Expression
	if !ast.IsIdentifier(receiver) {
		return "", "", false
	}
	called, isView := iteratorCallOf(head, receiver.Text())
	if !isView {
		return "", "", false
	}
	if called != "values" && called != "keys" {
		return "", "", false
	}
	return receiver.Text(), called, true
}

// bridgeSourceOfDeclaration reads a declaration's initializer as the
// BRIDGE: `const a = [...m.values()]`, `const a = Array.from(s)`, or
// `const a = [...s]`. Answers the collection's spelled name and which
// slot feeds the elements.
//
// A spread array literal with anything BESIDE the one spread — an extra
// element, a second spread — is not the bridge: the count would be the
// collection's size plus those, which the one-var length effect does not
// spell.
func bridgeSourceOfDeclaration(declaration *ast.Node) (name string, view string, ok bool) {
	if !ast.IsVariableDeclaration(declaration) {
		return "", "", false
	}
	initializer := declaration.AsVariableDeclaration().Initializer
	if initializer == nil {
		return "", "", false
	}
	head := Unwrapped(initializer)
	if ast.IsArrayLiteralExpression(head) {
		elements := head.AsArrayLiteralExpression().Elements.Nodes
		if len(elements) != 1 || !ast.IsSpreadElement(elements[0]) {
			return "", "", false
		}
		return collectionSourceOf(elements[0].AsSpreadElement().Expression)
	}
	if ast.IsCallExpression(head) {
		source := arrayFromReceiverOf(head.AsCallExpression())
		if source == nil {
			return "", "", false
		}
		return collectionSourceOf(source)
	}
	return "", "", false
}

// bridgedArrayLocalOf is the BRIDGE recognizer: `const a =
// [...m.values()]`, `const a = Array.from(m.values())`, `const a =
// [...s]` over a Map or Set the collection recognizer already admitted.
// The result is an ArrayLocal in every respect — the same two slot
// spellings, the same use scan, the same sort and typeof answers — so
// every downstream reader treats it exactly as it treats an array
// literal's local. What differs is only where the two slots' VALUES come
// from: the collection's size and value (or key) slots.
//
// A bridge over a collection that is NOT flattened declines: without the
// source slots there is nothing to copy from.
func bridgedArrayLocalOf(body *ast.Node, declaration *ast.Node, sources flattenedSources) (ArrayLocal, bool) {
	source, view, isBridge := bridgeSourceOfDeclaration(declaration)
	if !isBridge {
		return ArrayLocal{}, false
	}
	collection, flattened := sources.collectionOf(source)
	if !flattened {
		return ArrayLocal{}, false
	}
	// a bare name spreads a SET's members; a bare Map spreads its
	// ENTRIES, which are pairs and have no one element slot — a Map has
	// to be bridged through a named view
	if collection.IsMap && bareCollectionSpread(declaration.AsVariableDeclaration().Initializer) {
		return ArrayLocal{}, false
	}
	// `m.keys()` over a Set has no slot; the collection recognizer already
	// refused such a body, and this is the second gate
	if view == "keys" && !collection.IsMap {
		return ArrayLocal{}, false
	}
	name := declaration.AsVariableDeclaration().Name().Text()
	if !usesAreAllArrayForms(body, declaration, name) {
		return ArrayLocal{}, false
	}
	return ArrayLocal{
		Declaration:  declaration,
		Name:         name,
		LenSlotName:  name + arrayLenSuffix,
		ElemSlotName: name + arrayElemSuffix,
		BridgedFrom:  source,
		// the collection's seeded expressions ride in the ordinary
		// Elements field, so ArrayElementSort and ArrayElementTypeof answer
		// for this local exactly as they answer for a literal one
		Elements: bridgedElementsOf(collection, view),
	}, true
}

// bareCollectionSpread is whether an initializer spreads the collection
// BY NAME (`[...s]`, `Array.from(s)`) rather than through a view
// (`[...m.values()]`).
func bareCollectionSpread(initializer *ast.Node) bool {
	head := Unwrapped(initializer)
	if ast.IsArrayLiteralExpression(head) {
		elements := head.AsArrayLiteralExpression().Elements.Nodes
		if len(elements) != 1 || !ast.IsSpreadElement(elements[0]) {
			return false
		}
		return ast.IsIdentifier(Unwrapped(elements[0].AsSpreadElement().Expression))
	}
	if ast.IsCallExpression(head) {
		source := arrayFromReceiverOf(head.AsCallExpression())
		return source != nil && ast.IsIdentifier(Unwrapped(source))
	}
	return false
}

// bridgedElementsOf is the seed expressions a bridged array's ELEMENT
// slot inherits — the collection's seeded values, or its seeded keys for
// a `keys()` bridge. Carrying them in the ordinary Elements field is
// what makes ArrayElementSort and ArrayElementTypeof answer for a
// bridged array exactly as they answer for a literal one: the element
// slot's sort is the sort of the values it will hold.
//
// The COUNT of these expressions is never read as the array's length —
// a bridged array's len slot is written from the collection's size slot,
// not from a literal's row count (see ArrayDeclarationAssignmentsOf).
func bridgedElementsOf(collection MapLocal, view string) []*ast.Node {
	if view == "keys" {
		return collection.SeedKeys
	}
	return collection.SeedVals
}

// bridgedDeclarationAssignmentsOf is the BRIDGE's lowering: `const a =
// [...m.values()]` writes `a.len := a verbatim copy of m.size` and
// `a.elem := a verbatim copy of m.vals` (or of `m.keys` for a `keys()`
// bridge). Two whole-state copies — the array's slots hold exactly what
// the collection's held, so every later `a.length`, `a[i]`, `a.push(v)`
// and for-of over `a` reads the same shapes it would over a
// literal-built array.
//
// The syntax alone decides here, as everywhere in the lowering: the
// recognizer's admission is already recorded in the slot vector, so the
// gate is that both the array's slots and the collection's resolve.
func bridgedDeclarationAssignmentsOf(context *LoweringContext, declaration *ast.Node) ([]AssignmentTarget, bool) {
	source, view, isBridge := bridgeSourceOfDeclaration(declaration)
	if !isBridge {
		return nil, false
	}
	if !ast.IsIdentifier(declaration.AsVariableDeclaration().Name()) {
		return nil, false
	}
	lenSlot, elemSlot, ok := arraySlotsOf(context, declaration.AsVariableDeclaration().Name().Text())
	if !ok {
		return nil, false
	}
	sizeSlot, valsSlot, keysSlot, keysOk, found := mapSlotsOf(context, source)
	if !found {
		return nil, false
	}
	elementSource := valsSlot
	switch view {
	case "values":
		// a BARE spread of a Map spreads its entries, which are pairs; the
		// recognizer refused that, and this is the second gate
		if keysOk && bareCollectionSpread(declaration.AsVariableDeclaration().Initializer) {
			return nil, false
		}
	case "keys":
		if !keysOk {
			return nil, false
		}
		elementSource = keysSlot
	default:
		return nil, false
	}
	return []AssignmentTarget{
		{Target: lenSlot, Effect: varStateEffect(sizeSlot)},
		{Target: elemSlot, Effect: varStateEffect(elementSource)},
	}, true
}
