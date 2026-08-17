// split from ir_array_slots.go — the arrays that stand on an
// already-flattened SIBLING array: the callback result `const b =
// a.map(cb)` and the copy `const b = [...a]`

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
)

// mappedSourceOf reads a declaration's initializer as a CALLBACK RESULT
// that is itself an array: `const b = a.map(cb)` and `const b =
// a.filter(cb)`, over a bare-name receiver. Answers the receiver's
// spelled name.
//
// Only the two length-and-element-shaped methods are read here. `map`
// answers an array of cb's images, one per source element; `filter`
// answers an array of source elements. Both have a len and an elem the
// two slots can hold, which is what makes the result an array local.
//
// `find` answers ONE element or undefined, `reduce` answers the
// accumulator, and `flatMap` answers a concatenation the two-slot
// flattening has no spelling for — all three are scalars to this route,
// and scalarTargetSlotOf in ir_callback_summary.go is where their
// results land instead.
func mappedSourceOf(declaration *ast.Node) (string, bool) {
	if !ast.IsVariableDeclaration(declaration) {
		return "", false
	}
	initializer := declaration.AsVariableDeclaration().Initializer
	if initializer == nil {
		return "", false
	}
	head := Unwrapped(initializer)
	if !ast.IsCallExpression(head) {
		return "", false
	}
	call := head.AsCallExpression()
	if call.QuestionDotToken != nil {
		return "", false
	}
	access := Unwrapped(call.Expression)
	if !ast.IsPropertyAccessExpression(access) {
		return "", false
	}
	property := access.AsPropertyAccessExpression()
	if property.QuestionDotToken != nil {
		return "", false
	}
	if !ast.IsIdentifier(property.Expression) || !ast.IsIdentifier(property.Name()) {
		return "", false
	}
	switch property.Name().Text() {
	case "map", "filter":
	default:
		return "", false
	}
	if call.Arguments == nil || len(call.Arguments.Nodes) != 1 {
		return "", false
	}
	if ast.IsSpreadElement(call.Arguments.Nodes[0]) {
		return "", false
	}
	return property.Expression.Text(), true
}

// mappedArrayLocalOf is the CALLBACK-RESULT recognizer: `const b =
// a.map(cb)` / `a.filter(cb)` over an array the recognizer already
// admitted. The result is an ArrayLocal in every respect, so every
// downstream reader treats it as it treats a literal-built array — and
// laying it out as a pair is what lets the map lowering write it, since
// a whole-name scalar slot is a target that route refuses.
//
// The ELEMENT expressions are NOT inherited. A filter's elements are the
// source's, but a map's are cb's images, which no syntax here reads —
// so the element slot's sort comes from the lowering rather than from
// seed expressions, and leaving Elements empty is what says that. An
// empty Elements reads as the number sort through ArrayElementSort,
// which is the sort the map lowering's own unknown-sorted write and the
// index reads already speak.
//
// Gated the same three ways the copy is: the source must be flattened,
// the result may not name the source (its slots would be read before
// they are written), and the result's own uses must all be recognized.
func mappedArrayLocalOf(body *ast.Node, declaration *ast.Node, sources flattenedSources) (ArrayLocal, bool) {
	if !ast.IsIdentifier(declaration.AsVariableDeclaration().Name()) {
		return ArrayLocal{}, false
	}
	source, isMapped := mappedSourceOf(declaration)
	if !isMapped {
		return ArrayLocal{}, false
	}
	if _, flattened := sources.arrayOf(source); !flattened {
		return ArrayLocal{}, false
	}
	name := declaration.AsVariableDeclaration().Name().Text()
	if source == name {
		return ArrayLocal{}, false
	}
	if !usesAreAllArrayForms(body, declaration, name) {
		return ArrayLocal{}, false
	}
	return ArrayLocal{
		Declaration:  declaration,
		Name:         name,
		LenSlotName:  name + arrayLenSuffix,
		ElemSlotName: name + arrayElemSuffix,
	}, true
}

// arrayCopySourceOf reads a declaration's initializer as the ARRAY COPY:
// `const b = [...a]`, the array literal holding exactly one spread of a
// bare name, or `const b = Array.from(a)`, the same source spelled as a
// call. Answers the spelled name the copy reads.
//
// Whether that name is an already-flattened ARRAY is the caller's
// question — this reads the syntax only, exactly as copySourceOf does
// for a collection.
//
// A spread standing beside ANYTHING else — `[...a, x]`, `[...a, ...b]` —
// is not the copy: the count would be the source's length plus those,
// which the one-var length effect does not spell. A spread of a VIEW
// (`[...a.values()]`) is not read here either; it is the collection
// bridge's shape, and an array's own `values()` is not a recognized use.
func arrayCopySourceOf(declaration *ast.Node) (string, bool) {
	if !ast.IsVariableDeclaration(declaration) {
		return "", false
	}
	initializer := declaration.AsVariableDeclaration().Initializer
	if initializer == nil {
		return "", false
	}
	head := Unwrapped(initializer)
	var spread *ast.Node
	switch {
	case ast.IsArrayLiteralExpression(head):
		elements := head.AsArrayLiteralExpression().Elements.Nodes
		if len(elements) != 1 || !ast.IsSpreadElement(elements[0]) {
			return "", false
		}
		spread = elements[0].AsSpreadElement().Expression
	case ast.IsCallExpression(head):
		spread = arrayFromReceiverOf(head.AsCallExpression())
		if spread == nil {
			return "", false
		}
	default:
		return "", false
	}
	name := Unwrapped(spread)
	if !ast.IsIdentifier(name) {
		return "", false
	}
	return name.Text(), true
}

// copiedArrayLocalOf is the COPY recognizer: `const b = [...a]` over an
// array the recognizer already admitted. The result is an ArrayLocal in
// every respect — the same two slot spellings, the same use scan, the
// same sort and typeof answers — so every downstream reader treats it
// exactly as it treats a literal-built array. What differs is only where
// the two slots' VALUES come from: the sibling's own len and elem slots.
//
// The twin of copiedMapLocalOf, gated the same three ways: the sibling
// must be flattened (without its slots there is nothing to read), the
// copy may not name ITSELF (at the point the spread runs, its own slots
// have not been written yet), and the copy's own uses must all be
// recognized forms.
//
// A source that is a flattened COLLECTION rather than an array is left
// alone here — `const a = [...s]` over a Set is the bridge, and
// bridgedArrayLocalOf reads it.
func copiedArrayLocalOf(body *ast.Node, declaration *ast.Node, sources flattenedSources) (ArrayLocal, bool) {
	if !ast.IsIdentifier(declaration.AsVariableDeclaration().Name()) {
		return ArrayLocal{}, false
	}
	source, isCopy := arrayCopySourceOf(declaration)
	if !isCopy {
		return ArrayLocal{}, false
	}
	sibling, flattened := sources.arrayOf(source)
	if !flattened {
		return ArrayLocal{}, false
	}
	name := declaration.AsVariableDeclaration().Name().Text()
	// an array cannot be copied from itself — at the point the spread
	// runs, its own slots have not been written yet
	if source == name {
		return ArrayLocal{}, false
	}
	if !usesAreAllArrayForms(body, declaration, name) {
		return ArrayLocal{}, false
	}
	return ArrayLocal{
		Declaration:  declaration,
		Name:         name,
		LenSlotName:  name + arrayLenSuffix,
		ElemSlotName: name + arrayElemSuffix,
		// the sibling's element expressions ride in the ordinary Elements
		// field, so ArrayElementSort and ArrayElementTypeof answer for this
		// local exactly as they answer for the one it copied
		Elements:   sibling.Elements,
		CopiedFrom: source,
	}, true
}

// copiedArrayDeclarationAssignmentsOf is the array COPY's lowering:
// `const b = [...a]` writes `b.len := a verbatim copy of a.len` and
// `b.elem := a verbatim copy of a.elem`. Two whole-state copies — the
// copy holds exactly what the source held, so every later `b.length`,
// `b[i]`, `b.push(v)` and for-of over `b` reads the same shapes it
// would over a literal-built array.
//
// The syntax alone decides here, as everywhere in the lowering: the
// recognizer's admission is already recorded in the slot vector, so the
// gate is that both arrays' slot pairs resolve. A source that resolves
// to a COLLECTION's family rather than an array's has no ".len"/".elem"
// pair, so it falls through to the bridge below.
func copiedArrayDeclarationAssignmentsOf(context *LoweringContext, declaration *ast.Node) ([]AssignmentTarget, bool) {
	source, isCopy := arrayCopySourceOf(declaration)
	if !isCopy {
		return nil, false
	}
	if !ast.IsIdentifier(declaration.AsVariableDeclaration().Name()) {
		return nil, false
	}
	name := declaration.AsVariableDeclaration().Name().Text()
	if source == name {
		return nil, false
	}
	lenSlot, elemSlot, ok := arraySlotsOf(context, name)
	if !ok {
		return nil, false
	}
	sourceLen, sourceElem, sourceOk := arraySlotsOf(context, source)
	if !sourceOk {
		return nil, false
	}
	return []AssignmentTarget{
		{Target: lenSlot, Effect: varStateEffect(sourceLen)},
		{Target: elemSlot, Effect: varStateEffect(sourceElem)},
	}, true
}
