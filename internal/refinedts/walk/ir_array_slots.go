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
//   - `a.push(v)` → len := len + 1; elem := join(elem, v).
//   - `a[i]` under a dominating `i < a.length` → the elem slot's var.
//   - `a[i]` with nothing bounding i → orAbsent(elem): the element or
//     undefined, which is exactly what an out-of-range read yields.
//   - `a[i] = v` → elem := join(elem, v) (weak update; len unchanged).
//   - `for (const x of a)` → the ordinary loop lowering with x's
//     per-pass effect the elem slot's var.
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

// pushCallOf is `a.push(v)` with exactly one argument — the one growth
// shape the two slots can carry.
func pushCallOf(node *ast.Node, name string) (*ast.Node, bool) {
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
	if call.Arguments == nil || len(call.Arguments.Nodes) != 1 {
		return nil, false
	}
	if ast.IsSpreadElement(call.Arguments.Nodes[0]) {
		return nil, false
	}
	return call.Arguments.Nodes[0], true
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
		// `a.push(v)` — the argument still has to be scanned (it could
		// mention a), the callee half is consumed here
		if argument, isPush := pushCallOf(node, name); isPush {
			visitIfPresent(argument)
			return false
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

// ArrayLocalOf is the recognizer: a declaration `const a = [e, …]`
// whose every use in the body is one of the recognized forms becomes
// the two slots "a.len" and "a.elem"; anything else declines.
//
// `flattenedCollection` answers whether a spelled name is a flattened
// Map or Set — what the BRIDGE (`const a = [...m.values()]`) needs, and
// the one thing the array recognizer cannot read off its own
// declaration. A lone call may pass nil, admitting no bridge.
func ArrayLocalOf(body *ast.Node, declaration *ast.Node, flattenedCollection func(name string) (MapLocal, bool)) (ArrayLocal, bool) {
	if !ast.IsIdentifier(declaration.AsVariableDeclaration().Name()) {
		return ArrayLocal{}, false
	}
	// the BRIDGE, tried first: its initializer IS an array literal (a lone
	// spread), which the literal reader below would otherwise decline
	if bridged, ok := bridgedArrayLocalOf(body, declaration, flattenedCollection); ok {
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
func bridgedArrayLocalOf(body *ast.Node, declaration *ast.Node, flattenedCollection func(name string) (MapLocal, bool)) (ArrayLocal, bool) {
	if flattenedCollection == nil {
		return ArrayLocal{}, false
	}
	source, view, isBridge := bridgeSourceOfDeclaration(declaration)
	if !isBridge {
		return ArrayLocal{}, false
	}
	collection, flattened := flattenedCollection(source)
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

// ArrayLocalsOf runs the recognizer over a body's collected locals and
// answers the ones that flatten, keyed by declaration.
//
// `collections` is the flattened Map/Set table (MapLocalsOf's answer),
// which the BRIDGE consults — `const a = [...m.values()]` needs it to
// know m is flattened. Passing nil admits no bridge, which is the
// behaviour before the bridge existed.
func ArrayLocalsOf(body *ast.Node, locals []*ast.Node, collections map[*ast.Node]MapLocal) map[*ast.Node]ArrayLocal {
	byName := map[string]MapLocal{}
	for _, collection := range collections {
		byName[collection.Name] = collection
	}
	flattenedCollection := func(name string) (MapLocal, bool) {
		held, found := byName[name]
		return held, found
	}
	out := map[*ast.Node]ArrayLocal{}
	for _, declaration := range locals {
		if local, ok := ArrayLocalOf(body, declaration, flattenedCollection); ok {
			out[declaration] = local
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
	// the BRIDGE, tried first: `const a = [...m.values()]` writes the two
	// array slots from the collection's own — the count from its size and
	// the element join from its values (or its keys, for a `keys()`
	// bridge). Both are plain slot reads, so what the array's readers see
	// afterwards is indistinguishable from a literal's lowering.
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

// ArrayPushAssignmentsOf is `a.push(v)` as a statement: the len slot
// steps by one and the elem slot JOINS the pushed value. The join is
// the weak update — after the push the slot holds everything the array
// could hold, which is exactly what an index read may answer.
func ArrayPushAssignmentsOf(context *LoweringContext, statement *ast.Node) ([]AssignmentTarget, bool) {
	if !ast.IsExpressionStatement(statement) {
		return nil, false
	}
	call := Unwrapped(statement.AsExpressionStatement().Expression)
	if !ast.IsCallExpression(call) {
		return nil, false
	}
	access := call.AsCallExpression().Expression
	if !ast.IsPropertyAccessExpression(access) {
		return nil, false
	}
	receiver := access.AsPropertyAccessExpression().Expression
	if !ast.IsIdentifier(receiver) {
		return nil, false
	}
	argument, isPush := pushCallOf(call, receiver.Text())
	if !isPush {
		return nil, false
	}
	lenSlot, elemSlot, ok := arraySlotsOf(context, receiver.Text())
	if !ok {
		return nil, false
	}
	pushed, pushedOk := RhsEffect(context, context.Sorts[elemSlot], argument)
	if !pushedOk {
		return nil, false
	}
	one := constNumber(1)
	lenVar := varEffect(lenSlot)
	return []AssignmentTarget{
		{
			Target: lenSlot,
			Effect: kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectBinary, Op: kernelbridge.LoopOpAdd, A: &lenVar, B: &one},
		},
		{Target: elemSlot, Effect: joinEffect(varEffect(elemSlot), pushed)},
	}, true
}

// ArrayIndexWriteOf is `a[i] = v` as a statement: the elem slot JOINS
// the written value and the len slot is untouched. Weak on purpose —
// the two slots hold no per-position knowledge, so a write can only
// widen what a read may answer. A write PAST the end would also grow
// the length, which this does not claim; the length staying put is the
// honest reading, since the len slot is only ever read as a bound and a
// smaller bound admits fewer index reads, never more values.
func ArrayIndexWriteOf(context *LoweringContext, statement *ast.Node) ([]AssignmentTarget, bool) {
	if !ast.IsExpressionStatement(statement) {
		return nil, false
	}
	e := Unwrapped(statement.AsExpressionStatement().Expression)
	if !ast.IsBinaryExpression(e) {
		return nil, false
	}
	bin := e.AsBinaryExpression()
	if bin.OperatorToken.Kind != ast.KindEqualsToken {
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

// ArrayForOfLowering is a `for (const x of a)` over a flattened array,
// lowered as the ordinary loop: the element binding's per-pass effect
// is the elem slot's var (every pass hands it one element, and the slot
// holds the join of all of them), and the rest of the body folds
// exactly as any loop body does. NO numeric head bounds the loop — the
// trip count is the array's length, which the two-slot flattening does
// not relate to any binding — so every cond and after entry stays nil
// and the solver certifies whatever the body's effects support.
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
	if forOf.Initializer == nil || !ast.IsVariableDeclarationList(forOf.Initializer) {
		return kernelbridge.IrStatement{}, false
	}
	declarations := forOf.Initializer.AsVariableDeclarationList().Declarations.Nodes
	if len(declarations) != 1 {
		return kernelbridge.IrStatement{}, false
	}
	elementName := declarations[0].AsVariableDeclaration().Name()
	if !ast.IsIdentifier(elementName) {
		return kernelbridge.IrStatement{}, false
	}
	elementSlot, elementOk := slotIndexOfName(context, elementName.Text())
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
