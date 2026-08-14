// Array locals flattened into TWO scalar slots for the flow IR.
//
// A body that keeps an array in a local — `const a = [1, 2, 3]` — reads
// its length, pushes onto it, indexes it, and walks it. The kernel's
// walk is over a vector of scalar slots, so such a local is carried as
// TWO: a LENGTH slot spelled "a.len", holding the count as an ordinary
// number, and an ELEMENT slot spelled "a.elem", holding the JOIN of
// everything the array can hold. Every proved transfer applies
// unchanged — the kernel never learns two slots came from one array.
//
// The element slot is a weak summary on purpose: a write at any index
// joins into it rather than replacing it, so the slot always over-
// approximates what an index read can produce. That is the whole
// soundness story — an index read answers the join, never a narrower
// per-position claim.
//
// The recognized uses, total-or-decline over EVERY occurrence of the
// name:
//
//   - `a.length` → the len slot's var.
//   - `a.push(v, …)` → len := len + (the argument count); elem :=
//     join(elem, every argument). push ANSWERS the new length, so
//     `const n = a.push(v)` also writes n := the len slot's var, read
//     after the step.
//   - `a[i]` under a dominating `i < a.length` → the elem slot's var.
//   - `a[i]` with nothing bounding i → orAbsent(elem): the element or
//     undefined, which is exactly what an out-of-range read yields.
//   - `a[i] = v` → elem := join(elem, v) (weak update; len unchanged),
//     and the compound `a[i] += v` the same with the arithmetic in
//     front: elem := join(elem, elem + v).
//   - `for (const x of a)` and `for (x of a)` over a name declared
//     outside → the ordinary loop lowering with x's per-pass effect the
//     elem slot's var.
//   - `const b = [...a]` over an already-flattened sibling array of the
//     same body → a COPY: b's two slots take a's, read var for var. The
//     two hold the same values, so they wear the same sorts, and every
//     reader treats the copy exactly as it treats a literal-built array.
//   - `a.map(cb)`, `a.filter(cb)`, `a.forEach(cb)`, `a.find(cb)`,
//     `a.flatMap(cb)`, `a.reduce(cb, seed)` → the callback routes in
//     ir_callback_summary.go, which read a's two slots and convert the
//     callback. The array's own occurrence as the receiver is consumed
//     here; the callback and any seed still scan.
//
// Any other use of the name — an alias, an argument, a return, a method
// not listed — declines the array, and its slots then resolve to
// nothing so the whole body declines.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// ArrayLocal is one flattened array local: the declaration it came
// from, the name it was spelled under, the two slot names, and the
// literal's own element expressions in source order.
//
// A BRIDGED array — `const a = [...m.values()]` over a flattened Map or
// Set — has no literal elements of its own; it carries the collection it
// was built from instead, and its two slots are written from that
// collection's size and value slots. Every OTHER field, and every
// recognizer answer below, is identical to a literal-initialized
// array's: downstream readers (`a.length`, `a[i]`, `a.push(v)`, a
// for-of, and a concurrent agent's `.map(cb)`) cannot tell the two
// apart, which is the whole point of the bridge.
type ArrayLocal struct {
	Declaration  *ast.Node // VariableDeclaration
	Name         string
	LenSlotName  string // "a.len"
	ElemSlotName string // "a.elem"
	Elements     []*ast.Node
	// BridgedFrom: the spelled name of the Map or Set this array was
	// built from, or "" for an ordinary array literal. Its slots are
	// "<BridgedFrom>.size" and "<BridgedFrom>.vals".
	BridgedFrom string
	// CopiedFrom: the spelled name of the already-flattened ARRAY this one
	// was spread from — `const b = [...a]` — or "" for every other
	// initializer. Its slots are "<CopiedFrom>.len" and "<CopiedFrom>.elem".
	//
	// The twin of MapLocal.CopiedFrom, and it carries the same story: the
	// copy has no literal elements of its own, so it inherits the
	// sibling's in the ordinary Elements field and ArrayElementSort /
	// ArrayElementTypeof answer for it exactly as they answer for the
	// sibling.
	CopiedFrom string
}

// arrayLenSuffix and arrayElemSuffix are the two slot spellings a
// flattened array wears below its name.
const (
	arrayLenSuffix  = ".len"
	arrayElemSuffix = ".elem"
)

// arrayLiteralOfDeclaration is the array literal a declaration's
// initializer is, through parens and casts — or nil.
func arrayLiteralOfDeclaration(declaration *ast.Node) *ast.Node {
	if !ast.IsVariableDeclaration(declaration) {
		return nil
	}
	initializer := declaration.AsVariableDeclaration().Initializer
	if initializer == nil {
		return nil
	}
	literal := Unwrapped(initializer)
	if !ast.IsArrayLiteralExpression(literal) {
		return nil
	}
	return literal
}

// arrayElementsOf reads an array literal's elements, or declines: a
// spread names no one element, and a nested object or array value is
// not a scalar the element slot can hold.
func arrayElementsOf(literal *ast.Node) ([]*ast.Node, bool) {
	var out []*ast.Node
	for _, element := range literal.AsArrayLiteralExpression().Elements.Nodes {
		if ast.IsSpreadElement(element) || ast.IsOmittedExpression(element) {
			return nil, false
		}
		value := Unwrapped(element)
		if ast.IsObjectLiteralExpression(value) || ast.IsArrayLiteralExpression(value) {
			return nil, false
		}
		out = append(out, element)
	}
	return out, true
}

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

// pushCallOf is `a.push(v, …)` with one or more plain arguments — the
// growth shape the two slots can carry. Every argument is a value the
// element slot joins, and the count of them is the step the length
// takes, so a MULTI-argument push reads exactly as the one-argument
// case does, only wider: `a.push(v, w)` steps the length by two and
// joins both values.
//
// A push with NO argument moves nothing and is not admitted here — it
// names no value to join, and the length step would be zero, which the
// caller has no reading for. A SPREAD argument names no fixed count, so
// neither the step nor the join can be spelled.
func pushCallOf(node *ast.Node, name string) ([]*ast.Node, bool) {
	if !ast.IsCallExpression(node) {
		return nil, false
	}
	call := node.AsCallExpression()
	if !ast.IsPropertyAccessExpression(call.Expression) {
		return nil, false
	}
	access := call.Expression.AsPropertyAccessExpression()
	if access.QuestionDotToken != nil {
		return nil, false
	}
	if !ast.IsIdentifier(access.Expression) || access.Expression.Text() != name {
		return nil, false
	}
	if !ast.IsIdentifier(access.Name()) || access.Name().Text() != "push" {
		return nil, false
	}
	if call.Arguments == nil || len(call.Arguments.Nodes) == 0 {
		return nil, false
	}
	for _, argument := range call.Arguments.Nodes {
		if ast.IsSpreadElement(argument) {
			return nil, false
		}
	}
	return call.Arguments.Nodes, true
}

// callbackMethodCallOf is `a.<method>(…)` where the method is one the
// callback routes model — the ArrayCallbackMethods vocabulary
// callback_pins.go holds, which ir_callback_summary.go's lowering switch
// has a case for name for name. Answers the call's arguments, which
// still scan.
//
// The argument SHAPE is not judged here. Whether the callback actually
// converts is the lowering's question, and it answers it by declining
// the statement — a receiver admitted here whose callback then declines
// costs the body its lowering, exactly as an unadmitted use would, and
// no wrong value is ever claimed in between.
//
// A SPREAD argument declines: the callback would be at no fixed
// position, so no entry layout spells it.
func callbackMethodCallOf(node *ast.Node, name string) ([]*ast.Node, bool) {
	if !ast.IsCallExpression(node) {
		return nil, false
	}
	call := node.AsCallExpression()
	if call.QuestionDotToken != nil {
		return nil, false
	}
	if !ast.IsPropertyAccessExpression(call.Expression) {
		return nil, false
	}
	access := call.Expression.AsPropertyAccessExpression()
	if access.QuestionDotToken != nil {
		return nil, false
	}
	if !ast.IsIdentifier(access.Expression) || access.Expression.Text() != name {
		return nil, false
	}
	if !ast.IsIdentifier(access.Name()) {
		return nil, false
	}
	if _, modeled := ArrayCallbackMethods[access.Name().Text()]; !modeled {
		return nil, false
	}
	if call.Arguments == nil || len(call.Arguments.Nodes) == 0 {
		return nil, false
	}
	for _, argument := range call.Arguments.Nodes {
		if ast.IsSpreadElement(argument) {
			return nil, false
		}
	}
	return call.Arguments.Nodes, true
}

// loneSpreadNameOf is the name a LONE spread of an array literal
// spreads: `[...a]` answers "a", and `[...a, x]` answers nothing.
//
// A spread beside anything else makes the count the source's length plus
// those, which the one-var length effect does not spell, so the source
// array does not keep its flattening there. The literal is read from
// above rather than through a spread's Parent link, which is not
// populated on every node this scan walks.
func loneSpreadNameOf(literal *ast.Node) (string, bool) {
	if !ast.IsArrayLiteralExpression(literal) {
		return "", false
	}
	elements := literal.AsArrayLiteralExpression().Elements.Nodes
	if len(elements) != 1 || !ast.IsSpreadElement(elements[0]) {
		return "", false
	}
	spread := Unwrapped(elements[0].AsSpreadElement().Expression)
	if !ast.IsIdentifier(spread) {
		return "", false
	}
	return spread.Text(), true
}

// lengthReadOf is `a.length` — the one property read the len slot
// answers.
func lengthReadOf(node *ast.Node, name string) bool {
	if !ast.IsPropertyAccessExpression(node) {
		return false
	}
	access := node.AsPropertyAccessExpression()
	if access.QuestionDotToken != nil {
		return false
	}
	return ast.IsIdentifier(access.Expression) && access.Expression.Text() == name &&
		ast.IsIdentifier(access.Name()) && access.Name().Text() == "length"
}

// indexAccessOf is `a[e]` with a plain (non-optional) index — the read
// and write shape both.
func indexAccessOf(node *ast.Node, name string) (*ast.Node, bool) {
	if !ast.IsElementAccessExpression(node) {
		return nil, false
	}
	access := node.AsElementAccessExpression()
	if access.QuestionDotToken != nil {
		return nil, false
	}
	if !ast.IsIdentifier(access.Expression) || access.Expression.Text() != name {
		return nil, false
	}
	return access.ArgumentExpression, true
}

// usesAreAllArrayForms scans a body for every occurrence of the name
// and answers whether each one sits in a form the two slots can spell.
// The declaration's own name position and the literal's own elements
// are not uses.
func usesAreAllArrayForms(body *ast.Node, declaration *ast.Node, name string) bool {
	declarationName := declaration.AsVariableDeclaration().Name()
	ok := true
	var visit func(node *ast.Node) bool
	visitIfPresent := func(node *ast.Node) {
		if node != nil {
			visit(node)
		}
	}
	visit = func(node *ast.Node) bool {
		if !ok {
			return true
		}
		// `delete a[i]` — the array's LENGTH changes and a hole appears,
		// neither of which two slots carry. Checked FIRST: the operand
		// would otherwise pass the index rule below as an ordinary read.
		if ast.IsDeleteExpression(node) {
			operand := Unwrapped(node.AsDeleteExpression().Expression)
			if _, isIndex := indexAccessOf(operand, name); isIndex {
				ok = false
				return true
			}
		}
		// `a.length = k` — a length write TRUNCATES or grows the array,
		// dropping or inventing elements the element slot does not track.
		// Checked before the read rule below, which would otherwise admit
		// the target as an ordinary read.
		if ast.IsBinaryExpression(node) {
			bin := node.AsBinaryExpression()
			if bin.OperatorToken.Kind >= ast.KindFirstAssignment &&
				bin.OperatorToken.Kind <= ast.KindLastAssignment &&
				lengthReadOf(Unwrapped(bin.Left), name) {
				ok = false
				return true
			}
		}
		// `a.length++` / `--a.length` — the same truncation, spelled as a
		// step
		if ast.IsPrefixUnaryExpression(node) || ast.IsPostfixUnaryExpression(node) {
			var operator ast.Kind
			var operand *ast.Node
			if ast.IsPrefixUnaryExpression(node) {
				unary := node.AsPrefixUnaryExpression()
				operator, operand = unary.Operator, unary.Operand
			} else {
				unary := node.AsPostfixUnaryExpression()
				operator, operand = unary.Operator, unary.Operand
			}
			if (operator == ast.KindPlusPlusToken || operator == ast.KindMinusMinusToken) &&
				lengthReadOf(Unwrapped(operand), name) {
				ok = false
				return true
			}
		}
		// `a.push(v, …)` — the arguments still have to be scanned (any of
		// them could mention a), the callee half is consumed here
		if arguments, isPush := pushCallOf(node, name); isPush {
			for _, argument := range arguments {
				visitIfPresent(argument)
			}
			return false
		}
		// `a.map(cb)` / `.filter` / `.forEach` / `.find` / `.flatMap` /
		// `.reduce(cb, seed)` — the callback routes read the two slots and
		// convert the callback, so the receiver's own occurrence is
		// consumed. The arguments still scan: a callback that mentions the
		// array by name reads it as a whole value, and that occurrence gets
		// its own ruling below.
		if arguments, isCallback := callbackMethodCallOf(node, name); isCallback {
			for _, argument := range arguments {
				visitIfPresent(argument)
			}
			return false
		}
		// `[...a]` / `Array.from(a)` — this array COPIED into a new one.
		// The copy reads only the two slots, which hold everything the
		// array can hold, so the source keeps its flattening and its own
		// occurrence here is consumed whole. A spread standing beside
		// anything else is NOT this form and falls through to decline.
		if ast.IsArrayLiteralExpression(node) {
			if spread, isLone := loneSpreadNameOf(node); isLone && spread == name {
				return false
			}
		}
		if ast.IsCallExpression(node) {
			if source := arrayFromReceiverOf(node.AsCallExpression()); source != nil {
				if head := Unwrapped(source); ast.IsIdentifier(head) && head.Text() == name {
					return false
				}
			}
		}
		// `a.length` — consumed whole
		if lengthReadOf(node, name) {
			return false
		}
		// `a[e]` — consumed, the index expression still scanned
		if index, isIndex := indexAccessOf(node, name); isIndex {
			visitIfPresent(index)
			return false
		}
		// `for (const x of a)` — the array in the iterated position
		if ast.IsForInOrOfStatement(node) {
			forOf := node.AsForInOrOfStatement()
			if ast.IsForOfStatement(node) && forOf.AwaitModifier == nil {
				iterated := Unwrapped(forOf.Expression)
				if ast.IsIdentifier(iterated) && iterated.Text() == name {
					// the initializer and body still scan; the array's own
					// occurrence is admitted
					visitIfPresent(forOf.Initializer)
					visitIfPresent(forOf.Statement)
					return false
				}
			}
		}
		// Every other occurrence of the bare name — an alias, an argument,
		// a return, `a.map(…)`, `a.slice()` — is the WHOLE array in a
		// position two scalar slots cannot spell.
		if ast.IsIdentifier(node) && node.Text() == name && node != declarationName {
			ok = false
			return true
		}
		node.ForEachChild(visit)
		return false
	}
	visit(body)
	return ok
}

// flattenedSources is what the array recognizer can ask about a spelled
// name it did not declare — the two sibling kinds a lone-spread
// initializer may stand on.
//
// `Collection` answers whether the name is a flattened Map or Set, which
// the BRIDGE (`const a = [...m.values()]`) needs. `Array` answers
// whether it is a flattened ARRAY, which the COPY (`const b = [...a]`)
// needs. Neither is readable off the declaration itself, which is why
// both are threaded in rather than derived here.
//
// A nil field admits no form that depends on it; the zero value admits
// neither, which is the behaviour before either existed.
type flattenedSources struct {
	Collection func(name string) (MapLocal, bool)
	Array      func(name string) (ArrayLocal, bool)
}

// collectionOf and arrayOf are the two lookups with the nil-field rule
// applied once, so no caller below repeats it.
func (sources flattenedSources) collectionOf(name string) (MapLocal, bool) {
	if sources.Collection == nil {
		return MapLocal{}, false
	}
	return sources.Collection(name)
}

func (sources flattenedSources) arrayOf(name string) (ArrayLocal, bool) {
	if sources.Array == nil {
		return ArrayLocal{}, false
	}
	return sources.Array(name)
}

// ArrayLocalOf is the recognizer: a declaration `const a = [e, …]`
// whose every use in the body is one of the recognized forms becomes
// the two slots "a.len" and "a.elem"; anything else declines.
//
// `sources` answers what a spelled name in a lone-spread initializer
// already is — a flattened Map or Set for the BRIDGE, a flattened array
// for the COPY. A lone call may pass the zero value, admitting neither.
func ArrayLocalOf(body *ast.Node, declaration *ast.Node, sources flattenedSources) (ArrayLocal, bool) {
	if !ast.IsIdentifier(declaration.AsVariableDeclaration().Name()) {
		return ArrayLocal{}, false
	}
	// the COPY, tried first: `const b = [...a]` is a lone spread of a bare
	// name, which the bridge reader below would otherwise take for a Set
	// and the literal reader would decline outright
	if copied, ok := copiedArrayLocalOf(body, declaration, sources); ok {
		return copied, true
	}
	// the BRIDGE: its initializer IS an array literal (a lone spread),
	// which the literal reader below would otherwise decline
	if bridged, ok := bridgedArrayLocalOf(body, declaration, sources); ok {
		return bridged, true
	}
	literal := arrayLiteralOfDeclaration(declaration)
	if literal == nil {
		return ArrayLocal{}, false
	}
	name := declaration.AsVariableDeclaration().Name().Text()
	elements, ok := arrayElementsOf(literal)
	if !ok {
		return ArrayLocal{}, false
	}
	// an element's own initializer must not mention the array — it would
	// read slots the declaration has not written yet
	for _, element := range elements {
		if mentionsName(element, name) {
			return ArrayLocal{}, false
		}
	}
	if !usesAreAllArrayForms(body, declaration, name) {
		return ArrayLocal{}, false
	}
	return ArrayLocal{
		Declaration:  declaration,
		Name:         name,
		LenSlotName:  name + arrayLenSuffix,
		ElemSlotName: name + arrayElemSuffix,
		Elements:     elements,
	}, true
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

// ArrayLocalsOf runs the recognizer over a body's collected locals and
// answers the ones that flatten, keyed by declaration.
//
// `collections` is the flattened Map/Set table (MapLocalsOf's answer),
// which the BRIDGE consults — `const a = [...m.values()]` needs it to
// know m is flattened. Passing nil admits no bridge, which is the
// behaviour before the bridge existed.
//
// TWO phases, the same shape MapLocalsOf runs: the literal and bridged
// arrays flatten first, then the COPIES (`const b = [...a]`), which need
// the table the first phase built to know their source is flattened. A
// copy of a copy resolves on a later pass, and the passes stop as soon
// as one adds nothing — a cycle among copies cannot arise (a source must
// be declared before the spread reads it), and the fixpoint terminates
// regardless because each pass either grows the table or ends it.
func ArrayLocalsOf(body *ast.Node, locals []*ast.Node, collections map[*ast.Node]MapLocal) map[*ast.Node]ArrayLocal {
	collectionsByName := map[string]MapLocal{}
	for _, collection := range collections {
		collectionsByName[collection.Name] = collection
	}
	arraysByName := map[string]ArrayLocal{}
	sources := flattenedSources{
		Collection: func(name string) (MapLocal, bool) {
			held, found := collectionsByName[name]
			return held, found
		},
		Array: func(name string) (ArrayLocal, bool) {
			held, found := arraysByName[name]
			return held, found
		},
	}
	out := map[*ast.Node]ArrayLocal{}
	// the first phase admits no copy: arraysByName is still empty, so
	// copiedArrayLocalOf finds no sibling and every declaration is read as
	// a literal or a bridge
	for _, declaration := range locals {
		if local, ok := ArrayLocalOf(body, declaration, sources); ok {
			out[declaration] = local
			arraysByName[local.Name] = local
		}
	}
	// the second phase: the forms that stand on an already-flattened
	// SIBLING array — the copy `const b = [...a]` and the callback result
	// `const b = a.map(cb)`. Both need the table the first phase built.
	for added := true; added; {
		added = false
		for _, declaration := range locals {
			if _, already := out[declaration]; already {
				continue
			}
			local, ok := copiedArrayLocalOf(body, declaration, sources)
			if !ok {
				local, ok = mappedArrayLocalOf(body, declaration, sources)
			}
			if !ok {
				continue
			}
			out[declaration] = local
			arraysByName[local.Name] = local
			added = true
		}
	}
	return out
}

// ArrayElementSort is a flattened array's ELEMENT sort, read from the
// literal's own elements: every element string-shaped by syntax makes a
// string element slot; anything else the lowering reads numerically. An
// EMPTY literal has no element to read, so its sort is unknown until a
// push writes one — and the number sort is the one the pushes and the
// index reads speak, so an empty literal takes it too.
func ArrayElementSort(local ArrayLocal) BindingKind {
	if len(local.Elements) == 0 {
		return BindingKindNumber
	}
	for _, element := range local.Elements {
		if !SpelledSequenceShape(element) {
			return BindingKindNumber
		}
	}
	return BindingKindString
}

// ArrayElementTypeof is a flattened array's element typeof evidence,
// from the literal's syntax alone. Only an all-same reading claims
// anything; a mixed or unreadable literal claims nothing, and typeof
// tests on the element slot then decline.
func ArrayElementTypeof(local ArrayLocal) TypeofTag {
	if len(local.Elements) == 0 {
		return TypeofTagNone
	}
	var held TypeofTag
	for index, element := range local.Elements {
		e := Unwrapped(element)
		var tag TypeofTag
		switch {
		case SpelledSequenceShape(e):
			tag = TypeofTagString
		case e.Kind == ast.KindTrueKeyword || e.Kind == ast.KindFalseKeyword:
			tag = TypeofTagBoolean
		default:
			if _, isNumber := NumberOf(e); ast.IsNumericLiteral(e) || isNumber {
				tag = TypeofTagNumber
			} else {
				return TypeofTagNone
			}
		}
		if index == 0 {
			held = tag
			continue
		}
		if tag != held {
			return TypeofTagNone
		}
	}
	return held
}

// arraySlotsOf resolves a spelled array name to its two slots, or
// declines: a name with no "a.len"/"a.elem" pair is not a flattened
// array here.
func arraySlotsOf(context *LoweringContext, name string) (lenSlot int, elemSlot int, ok bool) {
	lenSlot, lenOk := slotIndexOfName(context, name+arrayLenSuffix)
	elemSlot, elemOk := slotIndexOfName(context, name+arrayElemSuffix)
	if !lenOk || !elemOk {
		return 0, 0, false
	}
	return lenSlot, elemSlot, true
}

// ArrayLengthSlotOf resolves `a.length` to the len slot — the one
// property read a flattened array answers. IndexOf routes through here
// so an `a.length` read and an `i < a.length` head both land on the
// ordinary number slot the guards and the loop head already speak.
func ArrayLengthSlotOf(context *LoweringContext, node *ast.Node) (int, bool) {
	head := Unwrapped(node)
	if !ast.IsPropertyAccessExpression(head) {
		return 0, false
	}
	access := head.AsPropertyAccessExpression()
	if access.QuestionDotToken != nil {
		return 0, false
	}
	if !ast.IsIdentifier(access.Expression) || !ast.IsIdentifier(access.Name()) {
		return 0, false
	}
	if access.Name().Text() != "length" {
		return 0, false
	}
	lenSlot, _, ok := arraySlotsOf(context, access.Expression.Text())
	if !ok {
		return 0, false
	}
	return lenSlot, true
}

// ArrayElementSlotOf resolves an index access `a[i]` to the element
// slot its read answers — the sort gate a caller consults before
// admitting the read into arithmetic or a sequence.
func ArrayElementSlotOf(context *LoweringContext, node *ast.Node) (int, bool) {
	head := Unwrapped(node)
	if !ast.IsElementAccessExpression(head) {
		return 0, false
	}
	receiver := head.AsElementAccessExpression().Expression
	if !ast.IsIdentifier(receiver) {
		return 0, false
	}
	if _, isIndex := indexAccessOf(head, receiver.Text()); !isIndex {
		return 0, false
	}
	_, elemSlot, ok := arraySlotsOf(context, receiver.Text())
	if !ok {
		return 0, false
	}
	return elemSlot, true
}

// varEffect is a slot read as an effect — the one-liner every array
// and record lowering below reaches for.
func varEffect(index int) kernelbridge.LoopEffect {
	return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectVar, Index: index}
}

// joinEffect pairs two effects into the effect grammar's join — what a
// weak update writes.
func joinEffect(a, b kernelbridge.LoopEffect) kernelbridge.LoopEffect {
	return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectJoin, A: &a, B: &b}
}

// constNumber is the exact-value set constant.
func constNumber(w float64) kernelbridge.LoopEffect {
	return kernelbridge.LoopEffect{
		Kind: kernelbridge.LoopEffectConst,
		Set:  refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{w})),
	}
}

// bridgedDeclarationAssignmentsOf is the BRIDGE's lowering: `const a =
// [...m.values()]` writes `a.len := var m.size` and `a.elem := var
// m.vals` (or `var m.keys` for a `keys()` bridge). Two ordinary slot
// reads — the array's slots hold exactly what the collection's held, so
// every later `a.length`, `a[i]`, `a.push(v)` and for-of over `a` reads
// the same shapes it would over a literal-built array.
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
		{Target: lenSlot, Effect: varEffect(sizeSlot)},
		{Target: elemSlot, Effect: varEffect(elementSource)},
	}, true
}

// copiedArrayDeclarationAssignmentsOf is the array COPY's lowering:
// `const b = [...a]` writes `b.len := var a.len` and `b.elem := var
// a.elem`. Two ordinary slot reads — the copy holds exactly what the
// source held, so every later `b.length`, `b[i]`, `b.push(v)` and for-of
// over `b` reads the same shapes it would over a literal-built array.
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
		{Target: lenSlot, Effect: varEffect(sourceLen)},
		{Target: elemSlot, Effect: varEffect(sourceElem)},
	}, true
}

// ArrayDeclarationAssignmentsOf is the lowering-side entry for a
// flattened array's declaration: the len slot takes the literal's
// count as an exact constant, and the elem slot takes the JOIN of the
// literal's element effects. An EMPTY literal writes the absent-
// carrying constant into the elem slot — there is no element, so an
// index read must produce undefined, and the absent flag is where that
// lives.
func ArrayDeclarationAssignmentsOf(context *LoweringContext, statement *ast.Node) ([]AssignmentTarget, bool) {
	if !ast.IsVariableStatement(statement) {
		return nil, false
	}
	declarations := statement.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes
	if len(declarations) != 1 {
		return nil, false
	}
	declaration := declarations[0]
	// the array-to-array COPY, tried first: `const b = [...a]` writes b's
	// two slots from a's own. Ahead of the bridge, whose bare-name reader
	// would otherwise take the source for a Set.
	if copied, ok := copiedArrayDeclarationAssignmentsOf(context, declaration); ok {
		return copied, true
	}
	// the BRIDGE: `const a = [...m.values()]` writes the two array slots
	// from the collection's own — the count from its size and the element
	// join from its values (or its keys, for a `keys()` bridge). Both are
	// plain slot reads, so what the array's readers see afterwards is
	// indistinguishable from a literal's lowering.
	if bridged, ok := bridgedDeclarationAssignmentsOf(context, declaration); ok {
		return bridged, true
	}
	literal := arrayLiteralOfDeclaration(declaration)
	if literal == nil {
		return nil, false
	}
	if !ast.IsIdentifier(declaration.AsVariableDeclaration().Name()) {
		return nil, false
	}
	name := declaration.AsVariableDeclaration().Name().Text()
	lenSlot, elemSlot, ok := arraySlotsOf(context, name)
	if !ok {
		return nil, false
	}
	elements, elementsOk := arrayElementsOf(literal)
	if !elementsOk {
		return nil, false
	}
	out := []AssignmentTarget{{Target: lenSlot, Effect: constNumber(float64(len(elements)))}}
	if len(elements) == 0 {
		out = append(out, AssignmentTarget{Target: elemSlot, Effect: kernelbridge.AbsentConst()})
		return out, true
	}
	var joined kernelbridge.LoopEffect
	for index, element := range elements {
		effect, effectOk := RhsEffect(context, context.Sorts[elemSlot], element)
		if !effectOk {
			return nil, false
		}
		if index == 0 {
			joined = effect
			continue
		}
		joined = joinEffect(joined, effect)
	}
	out = append(out, AssignmentTarget{Target: elemSlot, Effect: joined})
	return out, true
}

// pushSlotEffectsOf is the two slot writes a `a.push(v, …)` call makes,
// given the call node and the array's spelled receiver name: the len
// slot steps by the ARGUMENT COUNT and the elem slot JOINS every pushed
// value onto its old reading. The join is the weak update — after the
// push the slot holds everything the array could hold, which is exactly
// what an index read may answer.
//
// Answers the two assignments and the len slot, which the expression
// form below reads the new length back out of.
func pushSlotEffectsOf(context *LoweringContext, call *ast.Node, name string) (assignments []AssignmentTarget, lenSlot int, ok bool) {
	arguments, isPush := pushCallOf(call, name)
	if !isPush {
		return nil, 0, false
	}
	lenSlot, elemSlot, slotsOk := arraySlotsOf(context, name)
	if !slotsOk {
		return nil, 0, false
	}
	// every argument joins onto the element slot's old reading, left to
	// right — the same weak update the one-argument push made, applied
	// once per pushed value
	joined := varEffect(elemSlot)
	for _, argument := range arguments {
		pushed, pushedOk := RhsEffect(context, context.Sorts[elemSlot], argument)
		if !pushedOk {
			return nil, 0, false
		}
		joined = joinEffect(joined, pushed)
	}
	// the count steps by exactly as many values as were pushed
	step := constNumber(float64(len(arguments)))
	lenVar := varEffect(lenSlot)
	return []AssignmentTarget{
		{
			Target: lenSlot,
			Effect: kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectBinary, Op: kernelbridge.LoopOpAdd, A: &lenVar, B: &step},
		},
		{Target: elemSlot, Effect: joined},
	}, lenSlot, true
}

// pushReceiverOf is the array name a call expression pushes onto —
// `a` in `a.push(v)` — or ("", false) for anything that is not a
// property-access call on a plain name.
func pushReceiverOf(call *ast.Node) (string, bool) {
	if !ast.IsCallExpression(call) {
		return "", false
	}
	access := call.AsCallExpression().Expression
	if !ast.IsPropertyAccessExpression(access) {
		return "", false
	}
	receiver := access.AsPropertyAccessExpression().Expression
	if !ast.IsIdentifier(receiver) {
		return "", false
	}
	return receiver.Text(), true
}

// ArrayPushAssignmentsOf is `a.push(v, …)` in a statement position: as a
// bare expression statement, and as the right side of a declaration
// (`const n = a.push(v)`) or an assignment (`n = a.push(v)`).
//
// push ANSWERS the array's new length, which is exactly what the len
// slot holds after the step — so the value form lowers as the push's own
// two slot writes followed by `n := var a.len`, reading the length the
// two writes just established. The assignments emit in order, so the
// read lands after the step.
func ArrayPushAssignmentsOf(context *LoweringContext, statement *ast.Node) ([]AssignmentTarget, bool) {
	// `a.push(v);` — the return value discarded
	if ast.IsExpressionStatement(statement) {
		e := Unwrapped(statement.AsExpressionStatement().Expression)
		if name, isReceiver := pushReceiverOf(e); isReceiver {
			assignments, _, ok := pushSlotEffectsOf(context, e, name)
			if ok {
				return assignments, true
			}
		}
		// `n = a.push(v);` — the new length written into a tracked name
		if ast.IsBinaryExpression(e) {
			bin := e.AsBinaryExpression()
			if bin.OperatorToken.Kind == ast.KindEqualsToken {
				target, spelled := SpelledNameOf(Unwrapped(bin.Left))
				if spelled {
					if assignments, ok := pushValueAssignmentsOf(context, bin.Right, target); ok {
						return assignments, true
					}
				}
			}
		}
		return nil, false
	}
	// `const n = a.push(v);` — the same, spelled as a declaration
	if !ast.IsVariableStatement(statement) {
		return nil, false
	}
	declarations := statement.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes
	if len(declarations) != 1 {
		return nil, false
	}
	declaration := declarations[0].AsVariableDeclaration()
	if declaration.Initializer == nil || !ast.IsIdentifier(declaration.Name()) {
		return nil, false
	}
	return pushValueAssignmentsOf(context, declaration.Initializer, declaration.Name().Text())
}

// pushValueAssignmentsOf is a push whose RESULT is written into the
// spelled target name: the push's own slot writes, then the target
// taking the len slot's var — the new length, which is what push
// answers.
//
// Declines where the target has no slot of its own, or where its sort is
// not the number sort the length is read as.
func pushValueAssignmentsOf(context *LoweringContext, value *ast.Node, target string) ([]AssignmentTarget, bool) {
	call := Unwrapped(value)
	name, isReceiver := pushReceiverOf(call)
	if !isReceiver {
		return nil, false
	}
	assignments, lenSlot, ok := pushSlotEffectsOf(context, call, name)
	if !ok {
		return nil, false
	}
	targetSlot, targetOk := slotIndexOfName(context, target)
	if !targetOk {
		return nil, false
	}
	// the length is a count; a target the slot vector reads as a sequence
	// has no reading for it
	if context.Sorts[targetSlot] != BindingKindNumber {
		return nil, false
	}
	return append(assignments, AssignmentTarget{Target: targetSlot, Effect: varEffect(lenSlot)}), true
}

// compoundIndexOp is the arithmetic a compound assignment operator
// stands for — `+=` is add, `-=` is sub, and so on. Answers ok=false for
// every operator whose reading the effect grammar does not carry
// (`**=`, and the logical `&&=` / `||=` / `??=`, whose write is
// CONDITIONAL and so says something the unconditional effect grammar
// cannot).
func compoundIndexOp(kind ast.Kind) (kernelbridge.LoopEffectOp, bool) {
	switch kind {
	case ast.KindPlusEqualsToken:
		return kernelbridge.LoopOpAdd, true
	case ast.KindMinusEqualsToken:
		return kernelbridge.LoopOpSub, true
	case ast.KindAsteriskEqualsToken:
		return kernelbridge.LoopOpMul, true
	case ast.KindSlashEqualsToken:
		return kernelbridge.LoopOpDiv, true
	case ast.KindPercentEqualsToken:
		return kernelbridge.LoopOpRem, true
	// the bitwise and shift compounds: transferBitwise decides them
	case ast.KindAmpersandEqualsToken:
		return kernelbridge.LoopOpBitAnd, true
	case ast.KindBarEqualsToken:
		return kernelbridge.LoopOpBitOr, true
	case ast.KindCaretEqualsToken:
		return kernelbridge.LoopOpBitXor, true
	case ast.KindLessThanLessThanEqualsToken:
		return kernelbridge.LoopOpShl, true
	case ast.KindGreaterThanGreaterThanEqualsToken:
		return kernelbridge.LoopOpSar, true
	case ast.KindGreaterThanGreaterThanGreaterThanEqualsToken:
		return kernelbridge.LoopOpShr, true
	}
	return "", false
}

// ArrayIndexWriteOf is `a[i] = v` as a statement, and the COMPOUND forms
// `a[i] += v` / `-=` / `*=` / `/=` / `%=` with it: the elem slot JOINS
// the written value and the len slot is untouched. Weak on purpose —
// the two slots hold no per-position knowledge, so a write can only
// widen what a read may answer. A write PAST the end would also grow
// the length, which this does not claim; the length staying put is the
// honest reading, since the len slot is only ever read as a bound and a
// smaller bound admits fewer index reads, never more values.
//
// A compound write reads the position first, and the elem slot's var is
// what that read answers — the join of everything the array holds — so
// `a[i] += 1` writes join(elem, elem + 1). Same weak update, one
// arithmetic step in front of it.
func ArrayIndexWriteOf(context *LoweringContext, statement *ast.Node) ([]AssignmentTarget, bool) {
	if !ast.IsExpressionStatement(statement) {
		return nil, false
	}
	e := Unwrapped(statement.AsExpressionStatement().Expression)
	if !ast.IsBinaryExpression(e) {
		return nil, false
	}
	bin := e.AsBinaryExpression()
	compoundOp, isCompound := compoundIndexOp(bin.OperatorToken.Kind)
	if bin.OperatorToken.Kind != ast.KindEqualsToken && !isCompound {
		return nil, false
	}
	left := Unwrapped(bin.Left)
	if !ast.IsElementAccessExpression(left) {
		return nil, false
	}
	receiver := left.AsElementAccessExpression().Expression
	if !ast.IsIdentifier(receiver) {
		return nil, false
	}
	if _, isIndex := indexAccessOf(left, receiver.Text()); !isIndex {
		return nil, false
	}
	_, elemSlot, ok := arraySlotsOf(context, receiver.Text())
	if !ok {
		return nil, false
	}
	written, writtenOk := RhsEffect(context, context.Sorts[elemSlot], bin.Right)
	if !writtenOk {
		return nil, false
	}
	if isCompound {
		// the arithmetic forms speak the number sort only; a sequence slot
		// has no reading for `+=` here
		if context.Sorts[elemSlot] != BindingKindNumber {
			return nil, false
		}
		read := varEffect(elemSlot)
		written = kernelbridge.LoopEffect{
			Kind: kernelbridge.LoopEffectBinary, Op: compoundOp, A: &read, B: &written,
		}
	}
	return []AssignmentTarget{
		{Target: elemSlot, Effect: joinEffect(varEffect(elemSlot), written)},
	}, true
}

// ArrayIndexReadEffect is `a[i]` as an effect: the elem slot's var
// where a dominating `i < a.length` bounds the index, and the
// or-absent wrapping of that var where nothing does — an unguarded
// read may fall past the end, and the absent outcome is what falling
// past the end produces.
//
// "Dominating" is the guard scope the lowering carries: the if
// statement's lowering holds `i < a.length` while it lowers the THEN
// arm and drops it straight after, so a read outside the arm never
// sees the bound.
func ArrayIndexReadEffect(context *LoweringContext, node *ast.Node) (kernelbridge.LoopEffect, bool) {
	head := Unwrapped(node)
	if !ast.IsElementAccessExpression(head) {
		return kernelbridge.LoopEffect{}, false
	}
	receiver := head.AsElementAccessExpression().Expression
	if !ast.IsIdentifier(receiver) {
		return kernelbridge.LoopEffect{}, false
	}
	index, isIndex := indexAccessOf(head, receiver.Text())
	if !isIndex {
		return kernelbridge.LoopEffect{}, false
	}
	_, elemSlot, ok := arraySlotsOf(context, receiver.Text())
	if !ok {
		return kernelbridge.LoopEffect{}, false
	}
	elem := varEffect(elemSlot)
	if indexName, spelled := SpelledNameOf(Unwrapped(index)); spelled &&
		IndexIsBounded(context, indexName, receiver.Text()) {
		return elem, true
	}
	return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectOrAbsent, A: &elem}, true
}

// boundIndexKey spells one "this index is below that array's length"
// fact, which is what the guard scope holds.
func boundIndexKey(indexName string, arrayName string) string {
	return indexName + "<" + arrayName
}

// IndexIsBounded is whether the lowering currently stands inside a
// guard that proved `index < array.length`.
func IndexIsBounded(context *LoweringContext, indexName string, arrayName string) bool {
	if context.BoundedIndices == nil {
		return false
	}
	_, held := context.BoundedIndices[boundIndexKey(indexName, arrayName)]
	return held
}

// BoundIndexOfTest reads a guard head as an index bound: `i < a.length`
// (and the mirrored `a.length > i`) against a flattened array. Answers
// the pair the THEN arm may read unguarded.
func BoundIndexOfTest(context *LoweringContext, head *ast.Node) (indexName string, arrayName string, ok bool) {
	e := Unwrapped(head)
	if !ast.IsBinaryExpression(e) {
		return "", "", false
	}
	bin := e.AsBinaryExpression()
	left, right := Unwrapped(bin.Left), Unwrapped(bin.Right)
	switch bin.OperatorToken.Kind {
	case ast.KindLessThanToken:
		// i < a.length
	case ast.KindGreaterThanToken:
		// a.length > i — the same fact, mirrored
		left, right = right, left
	default:
		return "", "", false
	}
	index, spelled := SpelledNameOf(left)
	if !spelled {
		return "", "", false
	}
	if !ast.IsPropertyAccessExpression(right) {
		return "", "", false
	}
	access := right.AsPropertyAccessExpression()
	if access.QuestionDotToken != nil || !ast.IsIdentifier(access.Expression) ||
		!ast.IsIdentifier(access.Name()) || access.Name().Text() != "length" {
		return "", "", false
	}
	array := access.Expression.Text()
	if _, _, isArray := arraySlotsOf(context, array); !isArray {
		return "", "", false
	}
	return index, array, true
}

// HoldBoundIndex adds one proved index/array bound to the lowering
// context and answers the undo. The caller lowers the guarded arm and
// then calls the undo, so the bound never escapes the arm it was proved
// in.
//
// The context itself is mutated rather than copied: the slot vectors it
// carries GROW during lowering (an inlined callee allocates fresh
// slots), and a copy taken before that growth would index past its own
// Sorts. One context, one growing vector, and only the bound set moves.
func HoldBoundIndex(context *LoweringContext, indexName string, arrayName string) (undo func()) {
	previous := context.BoundedIndices
	held := map[string]struct{}{}
	for key := range previous {
		held[key] = struct{}{}
	}
	held[boundIndexKey(indexName, arrayName)] = struct{}{}
	context.BoundedIndices = held
	return func() { context.BoundedIndices = previous }
}

// forOfElementSlotOf is the slot a for-of hands its per-pass value to,
// read from the loop's initializer position: a FRESH single binding
// (`for (const x of a)`), or a name declared OUTSIDE the loop that the
// pass assigns into (`for (x of a)`). Both name one tracked slot, and
// the per-pass effect written into it is the same either way.
//
// Declines on a destructuring pattern in either spelling — the elem slot
// holds one scalar, and a pattern reads into a shape it does not have —
// and on a multi-declarator list, which the for-of grammar does not
// produce anyway.
func forOfElementSlotOf(context *LoweringContext, initializer *ast.Node) (int, bool) {
	if initializer == nil {
		return 0, false
	}
	if ast.IsVariableDeclarationList(initializer) {
		declarations := initializer.AsVariableDeclarationList().Declarations.Nodes
		if len(declarations) != 1 {
			return 0, false
		}
		elementName := declarations[0].AsVariableDeclaration().Name()
		if !ast.IsIdentifier(elementName) {
			return 0, false
		}
		return slotIndexOfName(context, elementName.Text())
	}
	// `for (x of a)` — the loop writes an already-declared name, which is
	// a slot exactly as a fresh binding is
	target, spelled := SpelledNameOf(Unwrapped(initializer))
	if !spelled {
		return 0, false
	}
	return slotIndexOfName(context, target)
}

// ArrayForOfLowering is a `for (const x of a)` over a flattened array,
// lowered as the ordinary loop: the element binding's per-pass effect
// is the elem slot's var (every pass hands it one element, and the slot
// holds the join of all of them), and the rest of the body folds
// exactly as any loop body does. NO numeric head bounds the loop — the
// trip count is the array's length, which the two-slot flattening does
// not relate to any binding — so every cond and after entry stays nil
// and the solver certifies whatever the body's effects support.
//
// The element binding may be FRESH (`for (const x of a)`) or a name
// declared outside the loop (`for (x of a)`) — both name one slot the
// pass writes, and forOfElementSlotOf reads either spelling.
//
// Declines where the element binding is not a plain single name, where
// the array is not flattened, or where the body leaves the fold's
// grammar.
func ArrayForOfLowering(context *LoweringContext, statement *ast.Node) (kernelbridge.IrStatement, bool) {
	if !ast.IsForOfStatement(statement) {
		return kernelbridge.IrStatement{}, false
	}
	forOf := statement.AsForInOrOfStatement()
	// `for await (… of …)` awaits each element — the value the binding
	// takes is the awaited one, which the element slot does not hold
	if forOf.AwaitModifier != nil {
		return kernelbridge.IrStatement{}, false
	}
	iterated := Unwrapped(forOf.Expression)
	if !ast.IsIdentifier(iterated) {
		return kernelbridge.IrStatement{}, false
	}
	_, elemSlot, ok := arraySlotsOf(context, iterated.Text())
	if !ok {
		return kernelbridge.IrStatement{}, false
	}
	elementSlot, elementOk := forOfElementSlotOf(context, forOf.Initializer)
	if !elementOk {
		return kernelbridge.IrStatement{}, false
	}
	// the fold starts every binding at its own var, then the element
	// binding takes the elem slot's — the per-pass value — and the body's
	// statements fold over that
	current := make([]kernelbridge.LoopEffect, len(context.Bindings))
	for index := range current {
		current[index] = varEffect(index)
	}
	if elementSlot >= len(current) || elemSlot >= len(current) {
		return kernelbridge.IrStatement{}, false
	}
	current[elementSlot] = varEffect(elemSlot)
	if !FoldBody(context, StatementsOf(forOf.Statement), current) {
		return kernelbridge.IrStatement{}, false
	}
	written := make([]bool, len(current))
	for index, effect := range current {
		written[index] = !(effect.Kind == kernelbridge.LoopEffectVar && effect.Index == index)
	}
	return kernelbridge.IrStatement{
		Kind:    kernelbridge.IrStatementLoop,
		Written: written,
		Cond:    make([]*refinementsets.RefinedSet, len(current)),
		After:   make([]*refinementsets.RefinedSet, len(current)),
		Body:    current,
	}, true
}
