// split from ir_map_slots.go — the syntax readers
//
// One reading per shape a flattened Map or Set can wear in source: the
// construction, the copy source, the seed literal's rows, a method call
// on the spelled receiver, the size read, the view calls, the bridge
// source, and the one position a `has` is admitted in. Nothing here
// consults slots — these answer syntax only, and the recognizer and the
// lowering both stand on them so the two agree on one vocabulary.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
)

// collectionConstructionOf reads a declaration's initializer as
// `new Map(…)` / `new Set(…)`, answering whether it is a Map and the
// seed argument (nil where there is none). Anything else — a call
// without `new`, a qualified `globalThis.Map`, a WeakMap, a type
// argument list is fine but a receiver is not — declines.
func collectionConstructionOf(declaration *ast.Node) (isMap bool, seed *ast.Node, ok bool) {
	if !ast.IsVariableDeclaration(declaration) {
		return false, nil, false
	}
	initializer := declaration.AsVariableDeclaration().Initializer
	if initializer == nil {
		return false, nil, false
	}
	return constructionOfNewExpression(Unwrapped(initializer))
}

// constructionOfNewExpression is the same reading against a `new`
// expression standing anywhere, not only in a declaration's initializer
// position — what the use scan needs to rule on `new Map(m)`, where the
// collection being copied FROM appears inside someone else's
// construction.
func constructionOfNewExpression(head *ast.Node) (isMap bool, seed *ast.Node, ok bool) {
	if !ast.IsNewExpression(head) {
		return false, nil, false
	}
	expression := head.AsNewExpression()
	if !ast.IsIdentifier(expression.Expression) {
		return false, nil, false
	}
	switch expression.Expression.Text() {
	case "Map":
		isMap = true
	case "Set":
		isMap = false
	default:
		return false, nil, false
	}
	if expression.Arguments == nil || len(expression.Arguments.Nodes) == 0 {
		return isMap, nil, true
	}
	if len(expression.Arguments.Nodes) != 1 {
		return false, nil, false
	}
	return isMap, expression.Arguments.Nodes[0], true
}

// copySourceOf is the spelled name a construction COPIES from:
// `new Map(m)` / `new Set(s)`, the seed a bare identifier rather than an
// array literal. Answers ("", false) for every other seed shape.
//
// Whether that name is an already-flattened collection of the MATCHING
// kind is the caller's question — this reads the syntax only.
func copySourceOf(seed *ast.Node) (string, bool) {
	if seed == nil {
		return "", false
	}
	head := Unwrapped(seed)
	if !ast.IsIdentifier(head) {
		return "", false
	}
	return head.Text(), true
}

// seedEntriesOf reads the seed argument of `new Map([[k, v], …])` or
// `new Set([v, …])` as its key and value expressions. Only an ARRAY
// LITERAL seeds — a variable or an iterator names no rows the slots can
// join. A Map's rows must each be a two-element array literal; a Set's
// rows are its members. A nested object or array value is not a scalar
// the slots can hold, so it declines.
func seedEntriesOf(seed *ast.Node, isMap bool) (keys []*ast.Node, vals []*ast.Node, ok bool) {
	if seed == nil {
		return nil, nil, true
	}
	literal := Unwrapped(seed)
	if !ast.IsArrayLiteralExpression(literal) {
		return nil, nil, false
	}
	for _, row := range literal.AsArrayLiteralExpression().Elements.Nodes {
		if ast.IsSpreadElement(row) || ast.IsOmittedExpression(row) {
			return nil, nil, false
		}
		value := Unwrapped(row)
		if !isMap {
			if ast.IsObjectLiteralExpression(value) || ast.IsArrayLiteralExpression(value) {
				return nil, nil, false
			}
			vals = append(vals, row)
			continue
		}
		// a Map row is `[k, v]`
		if !ast.IsArrayLiteralExpression(value) {
			return nil, nil, false
		}
		pair := value.AsArrayLiteralExpression().Elements.Nodes
		if len(pair) != 2 {
			return nil, nil, false
		}
		for _, half := range pair {
			if ast.IsSpreadElement(half) || ast.IsOmittedExpression(half) {
				return nil, nil, false
			}
			inner := Unwrapped(half)
			if ast.IsObjectLiteralExpression(inner) || ast.IsArrayLiteralExpression(inner) {
				return nil, nil, false
			}
		}
		keys = append(keys, pair[0])
		vals = append(vals, pair[1])
	}
	return keys, vals, true
}

// collectionMethodCallOf is `m.<method>(…)` with a plain (non-optional,
// non-computed) member name on the spelled receiver — the shape every
// recognized operation wears. Answers the method name and its arguments.
func collectionMethodCallOf(node *ast.Node, name string) (method string, arguments []*ast.Node, ok bool) {
	if !ast.IsCallExpression(node) {
		return "", nil, false
	}
	call := node.AsCallExpression()
	if !ast.IsPropertyAccessExpression(call.Expression) {
		return "", nil, false
	}
	access := call.Expression.AsPropertyAccessExpression()
	if access.QuestionDotToken != nil {
		return "", nil, false
	}
	if !ast.IsIdentifier(access.Expression) || access.Expression.Text() != name {
		return "", nil, false
	}
	if !ast.IsIdentifier(access.Name()) {
		return "", nil, false
	}
	if call.Arguments != nil {
		for _, argument := range call.Arguments.Nodes {
			if ast.IsSpreadElement(argument) {
				return "", nil, false
			}
		}
		arguments = call.Arguments.Nodes
	}
	return access.Name().Text(), arguments, true
}

// sizeReadOf is `m.size` — the one property read the size slot answers.
func sizeReadOf(node *ast.Node, name string) bool {
	if !ast.IsPropertyAccessExpression(node) {
		return false
	}
	access := node.AsPropertyAccessExpression()
	if access.QuestionDotToken != nil {
		return false
	}
	return ast.IsIdentifier(access.Expression) && access.Expression.Text() == name &&
		ast.IsIdentifier(access.Name()) && access.Name().Text() == "size"
}

// iteratorCallOf is `m.values()` / `m.keys()` / `m.entries()` — the
// zero-argument view methods an iteration or a bridge may stand on.
// Answers the view's name.
func iteratorCallOf(node *ast.Node, name string) (view string, ok bool) {
	method, arguments, isCall := collectionMethodCallOf(node, name)
	if !isCall || len(arguments) != 0 {
		return "", false
	}
	switch method {
	case "values", "keys", "entries":
		return method, true
	}
	return "", false
}

// admitBridgeSource rules on the expression a spread or an
// `Array.from` stands on, where it names THIS collection: a Set spread
// bare, or either collection's `values()` / `keys()` view. A bare Map
// yields pairs, which one element slot cannot hold, so it is a source
// the bridge refuses — reported as (false, true) so the caller declines
// the collection rather than reading past it. isBridge=false means the
// expression names something else entirely, and the caller keeps
// scanning.
func admitBridgeSource(source *ast.Node, name string, isMap bool) (admitted bool, isBridge bool) {
	head := Unwrapped(source)
	if ast.IsIdentifier(head) && head.Text() == name {
		return !isMap, true
	}
	view, isView := iteratorCallOf(head, name)
	if !isView {
		return false, false
	}
	switch view {
	case "values":
		return true, true
	case "keys":
		return isMap, true
	}
	// `entries()` yields pairs
	return false, true
}

// hasCallInTestPosition reads a test expression as `m.has(k)` — the one
// shape a `has` is admitted in. Parens, casts, and any number of leading
// `!` are stripped first: `if (!m.has(k))` and `if (!!(m.has(k)))` say
// the same thing about the collection as the bare form, and the opaque
// branch reads none of them. Answers the has call's arguments, which
// still scan.
func hasCallInTestPosition(test *ast.Node, name string) (arguments []*ast.Node, ok bool) {
	head := Unwrapped(test)
	for ast.IsPrefixUnaryExpression(head) &&
		head.AsPrefixUnaryExpression().Operator == ast.KindExclamationToken {
		head = Unwrapped(head.AsPrefixUnaryExpression().Operand)
	}
	method, arguments, isCall := collectionMethodCallOf(head, name)
	if !isCall || method != "has" || len(arguments) != 1 {
		return nil, false
	}
	return arguments, true
}

// setAlgebraPredicateNames is the three boolean-returning, write-free
// Set methods that read no per-key knowledge and touch no slot — the
// same shape `has` already rides. Set.prototype.isSubsetOf /
// isSupersetOf / isDisjointFrom (lib.es2025.collection.d.ts) each take
// one ReadonlySetLike argument and answer a boolean with no mutation of
// either operand, so the opaque branch spells exactly what is known
// about the result: nothing, same as `has`.
var setAlgebraPredicateNames = map[string]bool{
	"isSubsetOf":     true,
	"isSupersetOf":   true,
	"isDisjointFrom": true,
}

// setPredicateCallInTestPosition is setAlgebraPredicateNames' call
// reading — the same parens/`!`-stripped test-position admission
// hasCallInTestPosition gives `has`, generalized to the three Set
// algebra predicates. A Set operation only: none of the three exist on
// Map's own interface (lib.es2025.collection.d.ts declares them on Set
// and ReadonlySet, not on Map).
func setPredicateCallInTestPosition(test *ast.Node, name string, isMap bool) (arguments []*ast.Node, ok bool) {
	if isMap {
		return nil, false
	}
	head := Unwrapped(test)
	for ast.IsPrefixUnaryExpression(head) &&
		head.AsPrefixUnaryExpression().Operator == ast.KindExclamationToken {
		head = Unwrapped(head.AsPrefixUnaryExpression().Operand)
	}
	method, arguments, isCall := collectionMethodCallOf(head, name)
	if !isCall || !setAlgebraPredicateNames[method] || len(arguments) != 1 {
		return nil, false
	}
	return arguments, true
}

// setAlgebraProducerNames is the four Set-producing algebra methods
// (Set.prototype.union / intersection / difference / symmetricDifference,
// lib.es2025.collection.d.ts) each returning a FRESH Set built from the
// receiver and one ReadonlySetLike argument. Every member of the result
// is drawn from the receiver's own members, the argument's own members,
// or both — union_setSpec.md-equivalent: SetUnion/SetIntersection/
// SetDifference/SetSymmetricDifference (ECMA-262) each iterate one or
// both operands and add only values already present in one of them, so
// join(receiver.vals, argument.vals) is a SOUND — if imprecise — claim
// for every one of the four, regardless of which operator it is. What
// is NOT claimable from syntax alone is the resulting SIZE: duplicates
// between the two operands collapse for union, and intersection/
// difference/symmetricDifference each depend on which members actually
// coincide, none of which the two flattened operands' summaries can
// settle — so size stays unknown for all four.
var setAlgebraProducerNames = map[string]bool{
	"union":               true,
	"intersection":        true,
	"difference":          true,
	"symmetricDifference": true,
}

// setProducerCallOf reads a node as `a.union(b)` / `.intersection(b)` /
// `.difference(b)` / `.symmetricDifference(b)` — a Set-producing algebra
// call standing anywhere, not only in a declaration's initializer
// position — what the use scan needs to rule on the call wherever it
// appears, both the receiver and the argument bare identifiers. Mirrors
// constructionOfNewExpression's split from collectionConstructionOf for
// the same reason.
//
// A qualified receiver, a computed method name, an argument that is not
// a bare name (a fresh set literal, a call, a property access) — none
// of these are read: the two-sibling construction only fires where BOTH
// operands are bare names this recognizer can look up against the
// already-flattened Set table.
func setProducerCallOf(node *ast.Node) (receiver string, argument string, ok bool) {
	call := Unwrapped(node)
	if !ast.IsCallExpression(call) {
		return "", "", false
	}
	expression := call.AsCallExpression()
	if !ast.IsPropertyAccessExpression(expression.Expression) {
		return "", "", false
	}
	access := expression.Expression.AsPropertyAccessExpression()
	if access.QuestionDotToken != nil {
		return "", "", false
	}
	if !ast.IsIdentifier(access.Expression) || !ast.IsIdentifier(access.Name()) {
		return "", "", false
	}
	if !setAlgebraProducerNames[access.Name().Text()] {
		return "", "", false
	}
	if expression.Arguments == nil || len(expression.Arguments.Nodes) != 1 {
		return "", "", false
	}
	argumentHead := Unwrapped(expression.Arguments.Nodes[0])
	if !ast.IsIdentifier(argumentHead) {
		return "", "", false
	}
	return access.Expression.Text(), argumentHead.Text(), true
}

// setProducerConstructionOf is the same reading against a declaration's
// initializer specifically — what the recognizer (setProducerMapLocalOf)
// consults to read the NEW local's own construction.
func setProducerConstructionOf(declaration *ast.Node) (receiver string, argument string, ok bool) {
	if !ast.IsVariableDeclaration(declaration) {
		return "", "", false
	}
	initializer := declaration.AsVariableDeclaration().Initializer
	if initializer == nil {
		return "", "", false
	}
	return setProducerCallOf(initializer)
}

// testPositionOf is the condition an IF tests, or nil for every other
// statement — the one position a `has` is admitted in. A while head is
// NOT admitted: the loop form carries per-binding condition sets and a
// body effect, and there is no opaque-head loop, so `while (m.has(k))`
// declines at the loop whether or not the collection flattened.
// Admitting it here would flatten a collection whose body still declines.
func testPositionOf(node *ast.Node) *ast.Node {
	if ast.IsIfStatement(node) {
		return node.AsIfStatement().Expression
	}
	return nil
}
