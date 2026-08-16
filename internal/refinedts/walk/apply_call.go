// Function.prototype.apply on a contracted callee with a written
// `this` parameter: `withThis.apply({ age: 40 }, [1, 2])`.
// ECMA-262 pins the binding (sec-function.prototype.apply): argArray
// is read into an argument list (CreateListFromArrayLike) and the
// call runs as Call(func, thisArg, argList) — the SAME [[Call]]
// .call's own clause names, so once argArray's own items are known
// this reads exactly as `withThis.call(t, ...items)` would
// (this_parameter_call.go's thisParameterCallBindKnown is the one
// binder both recognizers share).
//
// The array argument must be EXACTLY known — an array literal written
// in place (ItemsOf, through EvaluateArrayLiteral), or a tracked
// identifier whose held value is an exact sequence. An argArray this
// walk cannot read exactly declines outright: the position count past
// it is unknown, so no parameter can be placed against any item, and
// the call falls through to whatever fallback an unmodeled method
// call already wears — this recognizer answers nil rather than
// serving a weaker guess.
//
// EVALUATED-ONCE DISCIPLINE: a decline here must not have run any
// caller-visible effect the FALLBACK evaluation (EvaluateCallExpression's
// own EffectiveArgumentsOf over the written arguments) will run again —
// the same rule ThisParameterCallResult's own comment states
// (evaluate_call_expression.go). So the array-argument's own EXACTNESS
// is checked SYNTACTICALLY (arrayLiteralSafeToReadOnce, or a tracked
// identifier through ReadsWithoutEffect) BEFORE either argument is
// evaluated: a `withThis.apply(t, computeArgs())` call — a callee whose
// own return this walk cannot read exactly — declines UNEVALUATED, so
// computeArgs() runs exactly once, through the fallback. Once the
// syntactic gate passes, both arguments evaluate here exactly once and
// this recognizer is committed to answering (never nil past that
// point).

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
)

// ApplyCallResult recognizes `<callee>.apply(thisArg, argsArray)`
// where <callee> resolves (through thisParameterCalleeOf) to a
// function declaration or expression with its own written `this`
// parameter, and argsArray is exactly known — an array literal or a
// tracked exact array. Answers the call's value through the same
// binding ThisParameterCallResult uses for `.call`, with argsArray's
// own items standing in for `.call`'s ...rest. Nil wherever the
// callee shape does not match, the this-argument or array-argument
// node is not safe to read here, or argsArray is not exactly known —
// in every nil case, nothing has run yet.
func ApplyCallResult(ctx *FlowContext, env Env, e *ast.Node) *abstractdomain.AbstractValue {
	call := e.AsCallExpression()
	callee := call.Expression
	if !ast.IsPropertyAccessExpression(callee) {
		return nil
	}
	access := callee.AsPropertyAccessExpression()
	methodName := access.Name()
	if methodName == nil || !ast.IsIdentifier(methodName) || methodName.Text() != "apply" {
		return nil
	}
	receiver := access.Expression
	shape := thisParameterCalleeOf(ctx, receiver)
	if shape == nil {
		return nil
	}
	var written []*ast.Node
	if call.Arguments != nil {
		written = call.Arguments.Nodes
	}
	// argArray undefined, null, or altogether absent (sec-function.
	// prototype.apply steps 3.a-b): Call(func, thisArg) with no
	// argument list at all — the zero/one-argument .call shape, read
	// through the same binder. thisArg alone is always safe: it is
	// evaluated once, here, and nothing else runs.
	if len(written) < 2 || isNullishLiteral(written[1]) {
		var thisOnly []*ast.Node
		if len(written) >= 1 {
			thisOnly = written[:1]
		}
		return thisParameterCallBind(ctx, env, receiver, *shape, thisOnly)
	}
	thisArgNode := written[0]
	arrayNode := written[1]
	// the array-argument must be checked for EXACTNESS before either
	// argument evaluates — a literal or tracked identifier that fails
	// ExactSpreadItems still declines cleanly this way, with nothing
	// run yet for EvaluateCallExpression's own fallback to repeat
	if !arrayLiteralSafeToReadOnce(ctx, arrayNode) && !ReadsWithoutEffect(arrayNode) {
		return nil
	}
	thisArgumentValue := evaluateExpression(ctx, env, thisArgNode)
	arrayValue := evaluateExpression(ctx, env, arrayNode)
	items, exact := ExactSpreadItems(arrayValue)
	if !exact {
		return nil
	}
	nodes := make([]*ast.Node, 0, len(items)+1)
	knowns := make([]abstractdomain.AbstractValue, 0, len(items)+1)
	nodes = append(nodes, thisArgNode)
	knowns = append(knowns, thisArgumentValue)
	// each item is an element of argArray's OWN value, not an
	// expression the caller wrote at this call site — nil node, the
	// same rule EffectiveArgumentsOf's own spread-expansion slots
	// carry, so the epilogue writes nothing back through it
	for _, item := range items {
		nodes = append(nodes, nil)
		knowns = append(knowns, item)
	}
	effective := EffectiveArguments{Nodes: nodes, Knowns: knowns, Exact: true}
	return thisParameterCallBindKnown(ctx, env, receiver, *shape, effective)
}

// isNullishLiteral: argArray written literally as `undefined` or
// `null` — sec-function.prototype.apply's own no-argument-list branch,
// syntactically recognized so it costs no evaluation either.
func isNullishLiteral(argArray *ast.Node) bool {
	if argArray.Kind == ast.KindNullKeyword {
		return true
	}
	return ast.IsIdentifier(argArray) && argArray.Text() == "undefined"
}

// arrayLiteralSafeToReadOnce: an array literal, read here exactly
// ONCE (never twice — a decline before this check runs means nothing
// has evaluated yet, so a literal failing it never pays a second
// read). The only real restriction is a nested spread whose own
// length is not pinned, the same restriction SyntacticSpreadLength
// places on a spread's own source array: a spread past that point
// would move the item count by an unknown amount, and ExactSpreadItems
// (called on the evaluated literal) already answers false for it —
// this syntactic check exists only to keep the effect from running at
// all in that case, rather than running once and then declining.
func arrayLiteralSafeToReadOnce(ctx *FlowContext, node *ast.Node) bool {
	literal := Unwrapped(node)
	if !ast.IsArrayLiteralExpression(literal) {
		return false
	}
	for _, element := range literal.AsArrayLiteralExpression().Elements.Nodes {
		if ast.IsSpreadElement(element) {
			if _, exact := SyntacticSpreadLength(ctx.P.Checker, element.AsSpreadElement().Expression); !exact {
				return false
			}
		}
	}
	return true
}
