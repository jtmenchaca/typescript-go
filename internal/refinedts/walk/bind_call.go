// Function.prototype.bind on a contracted callee with a written
// `this` parameter: a direct call of a bind result —
// `withThis.bind({ age: 40 })()` and `withThis.bind({ age: 40 },
// 1)(2)` — and a bind stored in a tracked const, then called by name
// — `const g = withThis.bind({ age: 40 }); g(1)`.
//
// ECMA-262 pins the binding in two clauses read together
// (sec-function.prototype.bind, BoundFunctionCreate/
// sec-boundfunctioncreate): .bind(thisArg, ...partials) builds a
// bound function exotic object carrying [[BoundThis]] = thisArg and
// [[BoundArguments]] = partials, and calling that object
// (sec-bound-function-exotic-objects-call-thisargument-argumentslist)
// runs Call(target, boundThis, list-concatenation(boundArgs, argList))
// — the partials come FIRST, then the later call's own arguments,
// bound through the SAME [[Call]] .call and .apply both reach
// (this_parameter_call.go's thisParameterCallBind is the one binder
// every one of the three uses).
//
// Anything past these two shapes declines: a bind result stored in a
// let/var (reassignable — the checker cannot see every write), passed
// as an argument, or read off a property, all name a bound function
// VALUE this walk does not track, and the call reaches no
// this-parameter callee to bind through.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
)

// BindCallResult recognizes a direct call of a `.bind(...)` result —
// `<callee>.bind(thisArg, ...partials)(...rest)` — or a call through
// an identifier a const declaration binds to exactly that shape —
// `const g = <callee>.bind(thisArg, ...partials); g(...rest)`. Nil
// wherever neither shape matches, the bind's own callee is not a
// this-parameter callee thisParameterCalleeOf recognizes, or the
// bind's OWN argument list cannot be read as a plain (non-spread)
// list — a spread inside the .bind(...) call itself declines, since
// this reader answers from the bind call's own written argument nodes
// rather than re-deriving them from an evaluated value the way
// ApplyCallResult does for its array.
func BindCallResult(ctx *FlowContext, env Env, e *ast.Node) *abstractdomain.AbstractValue {
	call := e.AsCallExpression()
	callee := call.Expression

	// the DIRECT shape first: the callee IS itself a `.bind(...)` call
	bindCall := bindCallOf(callee)
	if bindCall == nil && ast.IsIdentifier(callee) {
		// the STORED-CONST shape: the callee names a const bound to
		// exactly a `.bind(...)` call
		bindCall = bindCallThroughConst(ctx, callee)
	}
	if bindCall == nil {
		return nil
	}
	shape := thisParameterCalleeOf(ctx, bindCall.receiver)
	if shape == nil {
		return nil
	}
	var laterWritten []*ast.Node
	if call.Arguments != nil {
		laterWritten = call.Arguments.Nodes
	}
	// list-concatenation(boundArgs, argList) — the partials bound at
	// .bind(...) occupy the FRONT positions, this call's own arguments
	// follow. bindCall.thisArg is nil wherever .bind(...) was itself
	// written with no arguments at all (`f.bind()`) — every node
	// downstream (EffectiveArgumentsOf's own IsSpreadElement test
	// included) reads a nil entry as "spread-expanded item", never as
	// "absent argument", so an absent thisArg contributes NO slot here
	// rather than a nil one — the same zero-argument reading .call's
	// own `arguments` (built straight from real syntax) already gives.
	arguments := make([]*ast.Node, 0, len(bindCall.partials)+len(laterWritten)+1)
	if bindCall.thisArg != nil {
		arguments = append(arguments, bindCall.thisArg)
	}
	arguments = append(arguments, bindCall.partials...)
	arguments = append(arguments, laterWritten...)
	return thisParameterCallBind(ctx, env, bindCall.receiver, *shape, arguments)
}

// boundCall is one `<receiver>.bind(thisArg, ...partials)` call's own
// written pieces — thisArg kept separate from partials the same way
// thisParameterCallBind's own arguments list wants thisArg first.
type boundCall struct {
	receiver *ast.Node
	thisArg  *ast.Node
	partials []*ast.Node
}

// bindCallOf reads a `.bind(...)` call expression's own pieces —
// tried against the CALLEE of a call expression (`f.bind(t, a)(b)`'s
// outer call) or against a const's own initializer (`g`'s declaration
// in `const g = f.bind(t)`). Nil wherever the node is not a `.bind(…)`
// call at all, its own receiver is not an identifier, or one of its
// own arguments is a spread — SpreadElement expansion is
// EffectiveArgumentsOf's own job elsewhere in this walk, and reusing
// it here would mean evaluating the bind's own arguments before the
// callee shape is even confirmed, running effects a decline must not
// run (the same evaluated-once discipline apply_call.go's own banner
// states). A plain (thisArg, ...partials) list needs no evaluation to
// read as nodes, so bind never pays that cost.
func bindCallOf(node *ast.Node) *boundCall {
	if !ast.IsCallExpression(node) {
		return nil
	}
	inner := node.AsCallExpression()
	if !ast.IsPropertyAccessExpression(inner.Expression) {
		return nil
	}
	access := inner.Expression.AsPropertyAccessExpression()
	methodName := access.Name()
	if methodName == nil || !ast.IsIdentifier(methodName) || methodName.Text() != "bind" {
		return nil
	}
	receiver := access.Expression
	if !ast.IsIdentifier(receiver) {
		return nil
	}
	var written []*ast.Node
	if inner.Arguments != nil {
		written = inner.Arguments.Nodes
	}
	for _, argument := range written {
		if ast.IsSpreadElement(argument) {
			return nil
		}
	}
	var thisArg *ast.Node
	var partials []*ast.Node
	if len(written) > 0 {
		thisArg = written[0]
		partials = written[1:]
	}
	return &boundCall{receiver: receiver, thisArg: thisArg, partials: partials}
}

// bindCallThroughConst reads `const g = <callee>.bind(...)`'s own
// bind call off the identifier `g` names — the second shape
// BindCallResult answers, tried only after a direct
// `<callee>.bind(...)(...)` call fails to match. ConstInitializerOf
// (dataflowfacts) is the same resolver every other const-chain reader
// in this walk uses: nil wherever `g` is not bound by a const
// declaration with an initializer (a let, a var, a parameter, an
// import), which is what stops a REASSIGNABLE binding — the checker
// cannot see every write to it — from being read as if it always
// named the one bind result.
func bindCallThroughConst(ctx *FlowContext, name *ast.Node) *boundCall {
	initializer, ok := dataflowfacts.ConstInitializerOf(ctx.P.Checker, name)
	if !ok {
		return nil
	}
	return bindCallOf(initializer)
}
