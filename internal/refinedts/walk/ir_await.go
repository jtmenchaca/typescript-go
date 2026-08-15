// Await, lowered for the flow IR.
//
// The convention the summary route holds: a lowered async body's `#ret`
// slot carries the SETTLED inner value, never the promise — the Promise
// wrapper is put back on at the adapter boundary (AsCalleeResult), never
// inside the slot vector. So an await of a call whose callee has a
// compiled summary needs NO unwrapping step at all: the call statement's
// ret is already the awaited value, and `await f(…)` lowers to EXACTLY
// what `f(…)` lowers to.
//
// That is the whole content of the four call forms below — `x = await
// f(…)`, `const x = await f(…)`, `await f(…);`, `return await f(…)` are
// each the corresponding non-await form with the AwaitExpression peeled
// off its operand. `await this.method(…)` works wherever `f(…)` does,
// because the callee resolves through the lowering context's own
// ResolveCallee (ContractOf behind it, which reads a property-access
// callee by its property name) exactly as the plain call route does.
//
// Two shapes beyond the plain peel:
//
//   - A promise HELD in a local: `const p = f(…); … await p`, where
//     EVERY later use of p is `await p`. The local flattens to one slot
//     spelled "p.inner" — the call statement's ret writes it, and each
//     `await p` reads its var. Total-or-decline over p's uses: a p that
//     is passed, returned, `.then`-ed, or read any other way declines.
//   - `await Promise.all([f(a), g(b)])` whose VALUE is unused: the
//     calls run in sequence, each as a call statement with no ret
//     target.
//
// `await s` where s is a tracked scalar slot is the identity read of s
// — awaiting a non-promise settles to the value itself.
//
// Everything else await-shaped — `.then`/`.catch`, `for await`, any
// other Promise.all shape — declines whole, total-or-decline as always.
//
// Soundness note on rejection: a rejected await means the continuation
// never ran concretely. Claiming slot states for a path that did not run
// is the same over-approximation the walk already makes for `throw`, and
// costs nothing.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// AwaitedOperandOf is the expression an await awaits, through parens and
// casts on both sides of the keyword — or (nil, false) where the
// expression is not an await at all.
func AwaitedOperandOf(e *ast.Node) (*ast.Node, bool) {
	head := Unwrapped(e)
	if !ast.IsAwaitExpression(head) {
		return nil, false
	}
	return Unwrapped(head.AsAwaitExpression().Expression), true
}

// awaitIdentityEffect is `await s` where s is a tracked SCALAR slot:
// awaiting a non-promise settles to the value itself, so the read is the
// slot's own var. Also answers for `await p` where p is a recognized
// promise-held local — that reads the flattened "p.inner" slot; for
// `await Promise.resolve(e)`, where e's own reading stands in for the
// settled value; and for `await Promise.race([…])`, the join of every
// element's own reading.
//
// Declines for anything else, including an await of a plain call: a
// plain call has statements to emit that this effect-only reader has no
// way to carry — race and resolve are different only because their
// element/argument reading (RhsEffect, or a hoisted call temp) rides
// its own statements through context.Hoisted rather than through this
// function's own return.
func awaitIdentityEffect(context *LoweringContext, e *ast.Node) (kernelbridge.LoopEffect, bool) {
	operand, isAwait := AwaitedOperandOf(e)
	if !isAwait {
		return kernelbridge.LoopEffect{}, false
	}
	if ast.IsIdentifier(operand) {
		if slot, held := promiseInnerSlotOf(context, operand.Text()); held {
			return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectVar, Index: slot}, true
		}
	}
	// a tracked scalar: the identity read
	if slot, tracked := IndexOf(context, operand); tracked {
		return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectVar, Index: slot}, true
	}
	// `await Promise.resolve(e)` — e's own reading stands in for the
	// settled value, gated on e's own shape ruling out a thenable
	if inner, isResolve := promiseResolveArgumentOf(operand); isResolve {
		if effect, ok := RhsEffect(context, BindingKindUnknown, inner); ok {
			return effect, true
		}
	}
	// `await Promise.race([a, b, …])` — the join of every element's own
	// reading, admitted only where every element is
	if elements, isRace := promiseRaceArrayOf(operand); isRace {
		if effect, ok := promiseRaceJoinEffect(context, elements); ok {
			return effect, true
		}
	}
	return kernelbridge.LoopEffect{}, false
}

// AwaitStatementOf is the lowering-side entry for await STATEMENTS,
// dispatched ahead of the plain call route:
//
//	x = await f(…)        the call statement, ret → x's slot
//	const x = await f(…)  the same
//	await f(…);           the call statement, no ret target
//	const p = f(…)        where every later use of p is `await p`:
//	                      the call statement, ret → the "p.inner" slot
//	x = await p           the identity read of "p.inner"
//	await Promise.all([f(a), g(b)]);   the calls in sequence
//
// Declines everything else, and the statement then takes whatever route
// it took before.
func AwaitStatementOf(context *LoweringContext, statement *ast.Node) ([]kernelbridge.IrStatement, bool) {
	// NOTE on `await Promise.reject(e)` in STATEMENT position (bare,
	// assigned, or a declaration's initializer): NOT wired here. The
	// thrown-exit shape (#ret := thrown, #done := {1}, same as
	// lowerThrowStatement) is sound only where the caller ENDS the
	// statement list on the spot the way lowerStatementList's own
	// `return`/`throw` cases do (lowering_to_kernel_ir.go:139-146) — a
	// route reached through lowerFlatteningRoutes, as this one is, has
	// its result appended and the loop simply CONTINUES to the next
	// source statement, with no RaisesDone gate wrapping the remainder
	// the way the switch/try/if routes explicitly build one
	// (lowering_to_kernel_ir.go:219-234, :245-260, and the if route).
	// Wiring the statement-position arm here would silently lower every
	// statement AFTER the reject as if it still ran. The return-position
	// arm (ir_await_return.go, AwaitReturnStatements) has no such gap:
	// its caller already `return`s the whole list at the return
	// statement (lowering_to_kernel_ir.go:140), so nothing downstream
	// gets a chance to see the stale statements at all. See
	// awaitRejectThrownStatements below, used only from
	// AwaitReturnStatements.
	//
	// `await Promise.all([…]);` — the value goes nowhere
	if lowered, ok := promiseAllStatementOf(context, statement); ok {
		return lowered, true
	}
	// `await f(…);` — the value goes nowhere, but the call still runs
	if ast.IsExpressionStatement(statement) {
		if operand, isAwait := AwaitedOperandOf(statement.AsExpressionStatement().Expression); isAwait {
			// `await Promise.resolve(e);` / `await Promise.race([…]);` /
			// `await s;` / `await p;` on their own read and drop the value
			// — no statement to emit for the read itself, though a
			// race/resolve element that is a call still hoists its own
			// call statement (which the flush after this route picks up
			// regardless of what this branch itself returns). Tried
			// AHEAD of the bare call-expression route: Promise.resolve
			// and Promise.race are themselves call expressions, and
			// letting the plain route see them first would send them to
			// SummaryCallOrHavoc, which cannot resolve "Promise" as a
			// callee at all.
			if _, isEffect := awaitIdentityEffect(context, statement.AsExpressionStatement().Expression); isEffect {
				return nil, true
			}
			if ast.IsCallExpression(operand) {
				call, ok := SummaryCallOrHavoc(context, operand, -1)
				if !ok {
					return nil, false
				}
				return call, true
			}
			return nil, false
		}
	}
	target, rhs, shaped := callAssignmentShapeOf(context, statement)
	if shaped {
		// `x = await p` / `x = await s` — the identity read
		if effect, isEffect := awaitIdentityEffect(context, rhs); isEffect {
			return []kernelbridge.IrStatement{{
				Kind:   kernelbridge.IrStatementAssign,
				Target: target,
				Effect: effect,
			}}, true
		}
		// `x = await f(…)` — exactly the `x = f(…)` lowering
		if operand, isAwait := AwaitedOperandOf(rhs); isAwait && ast.IsCallExpression(operand) {
			// `x = await Promise.all([…])` USES the array the calls settle
			// to, which no slot here spells — the sequence lowering above
			// is sound only because the value goes nowhere. Declined
			// explicitly rather than left to Promise.all failing to
			// resolve as a callee.
			if _, isPromiseAll := promiseAllArrayOf(operand); isPromiseAll {
				return nil, false
			}
			return SummaryCallOrHavoc(context, operand, target)
		}
	}
	// `const p = f(…)` held as a promise and only ever awaited
	if lowered, ok := promiseLocalDeclarationOf(context, statement); ok {
		return lowered, true
	}
	return nil, false
}

// promiseResolveArgumentOf is the ARGUMENT of `Promise.resolve(e)` — the
// same property-access shape promiseAllArrayOf reads, `Promise.<name>`,
// but for "resolve" and a single argument of any shape.
//
// The caller (awaitIdentityEffect) is the thenable gate: it hands the
// argument to RhsEffect/EffectOf, which admits only a tracked slot's
// var copy, the absent keyword, a literal, or an arithmetic/sequence
// build over those — every one of those shapes is a primitive read, so
// under CannotBeThenable's own reasoning (a non-Object carries no
// `then`) none of them is ever a thenable. A call, a fresh object
// literal, or anything else RhsEffect does not read declines here by
// declining there, never by a separate check duplicating the same
// answer (ECMA-262 sec-promise-resolve: IsPromise(x) adopts, and
// PromiseResolve's fallback path fulfills a non-thenable x unchanged).
func promiseResolveArgumentOf(node *ast.Node) (*ast.Node, bool) {
	if !ast.IsCallExpression(node) {
		return nil, false
	}
	call := node.AsCallExpression()
	access := Unwrapped(call.Expression)
	if !ast.IsPropertyAccessExpression(access) {
		return nil, false
	}
	property := access.AsPropertyAccessExpression()
	if property.QuestionDotToken != nil {
		return nil, false
	}
	if !ast.IsIdentifier(property.Expression) || property.Expression.Text() != "Promise" {
		return nil, false
	}
	if !ast.IsIdentifier(property.Name()) || property.Name().Text() != "resolve" {
		return nil, false
	}
	if call.Arguments == nil || len(call.Arguments.Nodes) != 1 {
		return nil, false
	}
	return Unwrapped(call.Arguments.Nodes[0]), true
}

// promiseRejectArgumentOf is the ARGUMENT of `Promise.reject(e)` — the
// same property-access shape promiseResolveArgumentOf reads, but for
// "reject". Unlike resolve, reject takes NO thenable check at all
// (ECMA-262 sec-promise.reject: reason is passed straight to
// [[Reject]]), so any argument shape is admitted — e need not be a
// primitive, only present.
func promiseRejectArgumentOf(node *ast.Node) (*ast.Node, bool) {
	if !ast.IsCallExpression(node) {
		return nil, false
	}
	call := node.AsCallExpression()
	access := Unwrapped(call.Expression)
	if !ast.IsPropertyAccessExpression(access) {
		return nil, false
	}
	property := access.AsPropertyAccessExpression()
	if property.QuestionDotToken != nil {
		return nil, false
	}
	if !ast.IsIdentifier(property.Expression) || property.Expression.Text() != "Promise" {
		return nil, false
	}
	if !ast.IsIdentifier(property.Name()) || property.Name().Text() != "reject" {
		return nil, false
	}
	if call.Arguments == nil || len(call.Arguments.Nodes) != 1 {
		return nil, false
	}
	return Unwrapped(call.Arguments.Nodes[0]), true
}

// awaitedRejectExpressionOf finds the AWAIT EXPRESSION whose operand is
// `Promise.reject(e)`, sitting anywhere a statement's own shape puts an
// await: the bare expression statement (`await Promise.reject(e);`),
// the right side of a plain assignment (`x = await Promise.reject(e)`),
// or a single declaration's initializer (`const x = await
// Promise.reject(e)`). (nil, false) for any other shape.
//
// Kept for statement-shape recognition even though the statement-
// position write is not wired live (see the note beside AwaitStatementOf)
// — a future caller that gates the rest of the block on the done flag
// can reuse this reader unchanged.
func awaitedRejectExpressionOf(statement *ast.Node) (*ast.Node, bool) {
	var expression *ast.Node
	switch {
	case ast.IsVariableStatement(statement):
		declarations := statement.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes
		if len(declarations) != 1 {
			return nil, false
		}
		expression = declarations[0].AsVariableDeclaration().Initializer
	case ast.IsExpressionStatement(statement):
		e := Unwrapped(statement.AsExpressionStatement().Expression)
		if ast.IsBinaryExpression(e) && e.AsBinaryExpression().OperatorToken.Kind == ast.KindEqualsToken {
			expression = e.AsBinaryExpression().Right
		} else {
			expression = e
		}
	}
	if expression == nil {
		return nil, false
	}
	if _, isReject := promiseRejectExpressionOperandOf(expression); !isReject {
		return nil, false
	}
	return expression, true
}

// promiseRejectExpressionOperandOf answers whether an expression is
// itself `await Promise.reject(e)` — the one test both
// awaitedRejectExpressionOf (statement shapes) and
// awaitRejectThrownStatements (a bare expression, from the return
// route) need, kept as one answer so the two recognitions can never
// disagree about what counts as a reject.
func promiseRejectExpressionOperandOf(expression *ast.Node) (*ast.Node, bool) {
	operand, isAwait := AwaitedOperandOf(expression)
	if !isAwait {
		return nil, false
	}
	return promiseRejectArgumentOf(operand)
}

// awaitRejectThrownStatements is `await Promise.reject(e)`, lowered
// exactly as lowerThrowStatement lowers `throw e`: the rejection
// propagates before whatever the expression sat inside — a return, a
// statement's own assignment or declaration — ever completes, so the
// sound answer is the same escaping-throw shape, keyed off this
// EXPRESSION's own parent chain rather than a throw node's —
//
//	#ret  := thrown
//	#done := {1}
//
// preceded by the mention havoc of whatever the reject's own argument
// expression could move, and gated by the same try coverage a real
// `throw` obeys: a reject inside a try this body's own try route does
// not cover keeps the decline, named "await Promise.reject inside try"
// so the report points at the construct. ThrowReachesATry and
// ThrowCoveredByItsTry both walk purely through node.Parent and never
// assume the node they were handed is a throw, so anchoring them at the
// AWAIT EXPRESSION (whose parent chain passes through the same try
// nodes a throw at that position would) answers the identical question.
//
// (nil, false) wherever expression is not `await Promise.reject(…)`,
// or context.Result is nil. The DONE RAISE is the caller's own — the
// return route already builds one for the ordinary return shapes and
// hands it to AwaitReturnStatements, so this only writes #ret and lets
// the caller append its raise, rather than building a second one.
func awaitRejectThrownStatements(context *LoweringContext, expression *ast.Node) ([]kernelbridge.IrStatement, bool) {
	if context.Result == nil {
		return nil, false
	}
	if _, isReject := promiseRejectExpressionOperandOf(expression); !isReject {
		return nil, false
	}
	if ThrowReachesATry(expression) && !ThrowCoveredByItsTry(expression) {
		NoteDeclinedConstruct(context, "await Promise.reject inside try")
		return nil, false
	}
	var out []kernelbridge.IrStatement
	if slots, enumerable := havocSlotsOfStatement(context, expression); enumerable {
		out = append(out, havocAssignments(slots)...)
	} else {
		NoteFirstHavoc(context, "await Promise.reject")
	}
	out = append(out, kernelbridge.IrStatement{
		Kind:   kernelbridge.IrStatementAssign,
		Target: context.Result.Ret,
		Effect: kernelbridge.ThrownConst(),
	})
	return out, true
}
