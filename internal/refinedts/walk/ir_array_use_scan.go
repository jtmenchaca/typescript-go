// split from ir_array_slots.go — the recognized-use scan and the
// per-form syntax readers it consults

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
)

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

// readMethodArgCounts is the fixed argument count each non-callback,
// non-mutating read method admits here — the shape the value readers
// in ir_array_search.go and ir_array_join.go actually convert. A call
// at any other argument count falls through to the whole-array decline
// below, exactly like an unlisted method.
//
// `indexOf`/`lastIndexOf`/`includes` take a SECOND `fromIndex`
// argument the readers do not convert (the drawn-from window they
// answer holds only for a search of the WHOLE array), so only the
// one-argument call is admitted. `at` and `join` each take exactly the
// one argument their own clause names.
var readMethodArgCounts = map[string]int{
	"indexOf":     1,
	"lastIndexOf": 1,
	"includes":    1,
	"at":          1,
	"join":        1,
}

// arrayReadMethodCallOf is `a.<method>(arg)` for one of
// readMethodArgCounts's names — the value-producing read methods that
// consume the array's own occurrence whole and still scan their one
// argument. Mirrors callbackMethodCallOf's shape one level down (a
// fixed count instead of "one or more"). Answers the matched method's
// own name alongside the argument, since a caller reading a SPECIFIC
// method (arraySearchIndexEffect's "indexOf" vs "lastIndexOf") needs
// to tell which one matched.
func arrayReadMethodCallOf(node *ast.Node, name string) (method string, argument *ast.Node, ok bool) {
	if !ast.IsCallExpression(node) {
		return "", nil, false
	}
	call := node.AsCallExpression()
	if call.QuestionDotToken != nil {
		return "", nil, false
	}
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
	method = access.Name().Text()
	if readMethodArgCounts[method] != 1 {
		return "", nil, false
	}
	if call.Arguments == nil || len(call.Arguments.Nodes) != 1 {
		return "", nil, false
	}
	if ast.IsSpreadElement(call.Arguments.Nodes[0]) {
		return "", nil, false
	}
	return method, call.Arguments.Nodes[0], true
}

// arrayShrinkCallOf is `a.pop()` / `a.shift()` — the zero-argument
// shrinking calls ir_array_pop_shift.go converts.
func arrayShrinkCallOf(node *ast.Node, name string) (string, bool) {
	if !ast.IsCallExpression(node) {
		return "", false
	}
	call := node.AsCallExpression()
	if call.QuestionDotToken != nil {
		return "", false
	}
	if !ast.IsPropertyAccessExpression(call.Expression) {
		return "", false
	}
	access := call.Expression.AsPropertyAccessExpression()
	if access.QuestionDotToken != nil {
		return "", false
	}
	if !ast.IsIdentifier(access.Expression) || access.Expression.Text() != name {
		return "", false
	}
	if !ast.IsIdentifier(access.Name()) {
		return "", false
	}
	method := access.Name().Text()
	if method != "pop" && method != "shift" {
		return "", false
	}
	if call.Arguments != nil && len(call.Arguments.Nodes) != 0 {
		return "", false
	}
	return method, true
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
	return usesAreAllArrayFormsFrom(body, declaration.AsVariableDeclaration().Name(), name)
}

// usesAreAllArrayFormsFrom is the same scan with the DECLARING NAME NODE
// handed in rather than read off a variable declaration. A PARAMETER's
// name node lives on a ParameterDeclaration, which has no
// AsVariableDeclaration, and the scan itself only ever needs the node so
// it can tell the declaration's own name position from a use of it.
func usesAreAllArrayFormsFrom(body *ast.Node, declarationName *ast.Node, name string) bool {
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
		// `a.indexOf(v)` / `.lastIndexOf(v)` / `.includes(v)` / `.at(i)` /
		// `.join(sep)` — a value read off the two slots (ir_array_search.go,
		// ir_array_join.go); the one argument still scans.
		if _, argument, isRead := arrayReadMethodCallOf(node, name); isRead {
			visitIfPresent(argument)
			return false
		}
		// `a.pop()` / `a.shift()` — the shrinking mutation
		// (ir_array_pop_shift.go): no argument to scan.
		if _, isShrink := arrayShrinkCallOf(node, name); isShrink {
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
