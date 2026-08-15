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
// promise-held local — that reads the flattened "p.inner" slot.
//
// Declines for anything else, including an await of a call: a call has
// statements to emit, which an effect cannot carry.
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
	// `await Promise.all([…]);` — the value goes nowhere
	if lowered, ok := promiseAllStatementOf(context, statement); ok {
		return lowered, true
	}
	// `await f(…);` — the value goes nowhere, but the call still runs
	if ast.IsExpressionStatement(statement) {
		if operand, isAwait := AwaitedOperandOf(statement.AsExpressionStatement().Expression); isAwait {
			if ast.IsCallExpression(operand) {
				call, ok := SummaryCallOrHavoc(context, operand, -1)
				if !ok {
					return nil, false
				}
				return call, true
			}
			// `await s;` on its own reads a slot and drops the value — no
			// statement to emit, and nothing else in the body changes
			if _, isEffect := awaitIdentityEffect(context, statement.AsExpressionStatement().Expression); isEffect {
				return nil, true
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
