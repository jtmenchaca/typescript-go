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
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// promiseInnerSuffix is the one slot spelling a promise-held local wears
// below its name.
const promiseInnerSuffix = ".inner"

// promiseLocalSlots holds the "p.inner" slot each recognized
// promise-held local was allocated, per lowering context. Keyed by the
// context POINTER: one lowering walk, one set of flattened promise
// locals, and the map dies with the walk.
//
// It lives here rather than on LoweringContext because the slot is
// allocated on demand, at the declaration statement, rather than laid
// out with the body's other locals — a promise local is recognized by
// how its name is USED downstream, which the slot layout does not scan.
var (
	promiseLocalSlotsMu sync.Mutex
	promiseLocalSlots   = map[*LoweringContext]map[string]int{}
)

// promiseInnerSlotOf answers the "p.inner" slot held for a name in this
// lowering, if the declaration statement recognized one.
func promiseInnerSlotOf(context *LoweringContext, name string) (int, bool) {
	promiseLocalSlotsMu.Lock()
	defer promiseLocalSlotsMu.Unlock()
	held, has := promiseLocalSlots[context]
	if !has {
		return 0, false
	}
	slot, found := held[name]
	return slot, found
}

// holdPromiseInnerSlot remembers the "p.inner" slot for a name.
func holdPromiseInnerSlot(context *LoweringContext, name string, slot int) {
	promiseLocalSlotsMu.Lock()
	defer promiseLocalSlotsMu.Unlock()
	held, has := promiseLocalSlots[context]
	if !has {
		held = map[string]int{}
		promiseLocalSlots[context] = held
	}
	held[name] = slot
}

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

// AwaitReturnStatements is `return await f(…)` and — from an ASYNC
// declaration's body — `return f(…)` where f resolves with a blob:
// returning a promise from an async function ADOPTS it, so the two
// spellings settle to the same value and lower identically.
//
// The caller supplies the `raise` statement that lifts the done flag;
// the answer is the call statement, the ret copy, and that raise.
func AwaitReturnStatements(
	context *LoweringContext,
	expression *ast.Node,
	raise kernelbridge.IrStatement,
) ([]kernelbridge.IrStatement, bool) {
	if context.Result == nil {
		return nil, false
	}
	// `return await s` / `return await p` — the identity read into the
	// result slot
	if effect, isEffect := awaitIdentityEffect(context, expression); isEffect {
		return []kernelbridge.IrStatement{
			{Kind: kernelbridge.IrStatementAssign, Target: context.Result.Ret, Effect: effect},
			raise,
		}, true
	}
	call, isCall := awaitedReturnCallOf(context, expression)
	if !isCall {
		return nil, false
	}
	lowered, ok := SummaryCallOrHavoc(context, call, context.Result.Ret)
	if !ok {
		return nil, false
	}
	out := append([]kernelbridge.IrStatement{}, lowered...)
	out = append(out, raise)
	return out, true
}

// awaitedReturnCallOf is the call a return statement's value settles to:
// the operand of `return await f(…)`, or — inside an ASYNC declaration —
// the callee of a bare `return f(…)` whose callee resolves. Returning a
// promise from an async function adopts it, so the bare form settles the
// same way the awaited one does.
//
// The async question is asked of the return's own enclosing declaration,
// walked up through the parent chain: the lowering context carries the
// slot vector, not the declaration it came from.
func awaitedReturnCallOf(context *LoweringContext, expression *ast.Node) (*ast.Node, bool) {
	if operand, isAwait := AwaitedOperandOf(expression); isAwait {
		if !ast.IsCallExpression(operand) {
			return nil, false
		}
		// `return await Promise.all([…])` USES the settled array, which no
		// slot here spells — the sequence lowering is sound only where the
		// value goes nowhere
		if _, isPromiseAll := promiseAllArrayOf(operand); isPromiseAll {
			return nil, false
		}
		return operand, true
	}
	head := Unwrapped(expression)
	if !ast.IsCallExpression(head) {
		return nil, false
	}
	if !insideAsyncDeclaration(head) {
		return nil, false
	}
	if summaryCalleeOf(context, head) == nil {
		return nil, false
	}
	return head, true
}

// insideAsyncDeclaration walks up from a node to its nearest enclosing
// function-like declaration and answers whether that declaration is
// async. A node with no enclosing function — a top-level lowering — is
// not inside one.
func insideAsyncDeclaration(node *ast.Node) bool {
	for parent := node.Parent; parent != nil; parent = parent.Parent {
		if ast.IsSourceFile(parent) {
			return false
		}
		switch parent.Kind {
		case ast.KindFunctionDeclaration, ast.KindFunctionExpression,
			ast.KindArrowFunction, ast.KindMethodDeclaration,
			ast.KindGetAccessor, ast.KindSetAccessor, ast.KindConstructor:
			return ast.GetCombinedModifierFlags(parent)&ast.ModifierFlagsAsync != 0
		}
	}
	return false
}

// promiseLocalDeclarationOf is `const p = f(…)` where f resolves with a
// blob and EVERY later use of p is `await p`. Such a local holds a
// promise that is only ever settled, so it flattens to ONE slot spelled
// "p.inner": the call statement's ret writes it here, and each `await p`
// downstream reads its var (awaitIdentityEffect above).
//
// Total-or-decline over p's uses, the same law every other flattening
// here obeys: a p that is passed to something, returned, `.then`-ed, or
// read in any position other than the operand of an await declines the
// whole recognition, and the declaration then takes its former route.
func promiseLocalDeclarationOf(context *LoweringContext, statement *ast.Node) ([]kernelbridge.IrStatement, bool) {
	if context.Allocate == nil {
		return nil, false
	}
	if !ast.IsVariableStatement(statement) {
		return nil, false
	}
	declarations := statement.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes
	if len(declarations) != 1 {
		return nil, false
	}
	declaration := declarations[0].AsVariableDeclaration()
	if !ast.IsIdentifier(declaration.Name()) || declaration.Initializer == nil {
		return nil, false
	}
	call := Unwrapped(declaration.Initializer)
	if !ast.IsCallExpression(call) {
		return nil, false
	}
	// only a callee the registry answers for: a promise from anything
	// else has no ret out-state to land in the flattened slot
	callee := summaryCalleeOf(context, call)
	if callee == nil || context.Flow == nil {
		return nil, false
	}
	if _, hasBlob := SummaryBlobFor(context.Flow, callee); !hasBlob {
		return nil, false
	}
	name := declaration.Name().Text()
	// a name already flattened is a second declaration of the same
	// spelling — one name, one slot, so the second declines rather than
	// silently reusing the first's slot
	if _, already := promiseInnerSlotOf(context, name); already {
		return nil, false
	}
	body := enclosingBodyOf(statement)
	if body == nil {
		return nil, false
	}
	if !usesAreAllAwaits(body, declarations[0], name) {
		return nil, false
	}
	// the inner slot's sort is UNKNOWN: nothing in the declaration
	// spells what the callee settles to, and an unknown-sorted slot
	// admits only the definedness test — which loses coverage, never
	// soundness
	slot, allocated := context.Allocate(name+promiseInnerSuffix, BindingKindUnknown, TypeofTagNone)
	if !allocated {
		return nil, false
	}
	lowered, ok := SummaryCallOrHavoc(context, call, slot)
	if !ok {
		return nil, false
	}
	holdPromiseInnerSlot(context, name, slot)
	return lowered, true
}

// enclosingBodyOf is the block a statement sits in, walked up to the
// nearest function body or source file — the scan region a use analysis
// covers.
func enclosingBodyOf(statement *ast.Node) *ast.Node {
	for parent := statement.Parent; parent != nil; parent = parent.Parent {
		if ast.IsSourceFile(parent) {
			return parent
		}
		switch parent.Kind {
		case ast.KindFunctionDeclaration, ast.KindFunctionExpression,
			ast.KindArrowFunction, ast.KindMethodDeclaration,
			ast.KindGetAccessor, ast.KindSetAccessor, ast.KindConstructor:
			return parent.Body()
		}
	}
	return nil
}

// usesAreAllAwaits scans a body for every occurrence of the name and
// answers whether each one is the operand of an `await` or the
// receiver of a `.then(…)` call — the two positions the flattened
// "p.inner" slot can serve (thenStatements lowers the then). The
// declaration's own name position is not a use.
func usesAreAllAwaits(body *ast.Node, declaration *ast.Node, name string) bool {
	declarationName := declaration.AsVariableDeclaration().Name()
	ok := true
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if !ok {
			return true
		}
		// `await p` — consumed whole; the operand's occurrence is admitted
		if ast.IsAwaitExpression(node) {
			operand := Unwrapped(node.AsAwaitExpression().Expression)
			if ast.IsIdentifier(operand) && operand.Text() == name {
				return false
			}
		}
		// `p.then(cb)` — the receiver reads through the settled slot the
		// then-lowering serves; the CALLBACK still scans on its own
		if ast.IsCallExpression(node) {
			access := Unwrapped(node.AsCallExpression().Expression)
			if ast.IsPropertyAccessExpression(access) {
				pa := access.AsPropertyAccessExpression()
				if pa.QuestionDotToken == nil && ast.IsIdentifier(pa.Expression) &&
					pa.Expression.Text() == name && ast.IsIdentifier(pa.Name()) &&
					pa.Name().Text() == "then" {
					if node.AsCallExpression().Arguments != nil {
						for _, argument := range node.AsCallExpression().Arguments.Nodes {
							argument.ForEachChild(visit)
						}
					}
					return false
				}
			}
		}
		// every other occurrence of the bare name — an argument, a return,
		// an alias, `.catch` — is the PROMISE itself in a position one
		// settled-value slot cannot spell
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

// promiseAllStatementOf is `await Promise.all([f(a), g(b), …])` as a
// STATEMENT — the value unused — lowered as the calls IN SEQUENCE, each
// a call statement with no ret target.
//
// Sequencing is sound for the concurrent original because a lowered
// effect rides ONLY through a call statement's rets into distinct target
// slots: no lowered call reads or writes a slot another lowered call
// touches, so any interleaving of the concurrent runs computes the same
// exit states as running them one after another. (Only scalar arguments
// lower into calls today — a record or array argument declines the call
// before it reaches here — so there is no shared reference for one call
// to observe another's write through.)
//
// Any OTHER Promise.all shape — a value that is used, a `.map` argument,
// a non-literal array, a non-call element — declines whole.
func promiseAllStatementOf(context *LoweringContext, statement *ast.Node) ([]kernelbridge.IrStatement, bool) {
	if !ast.IsExpressionStatement(statement) {
		return nil, false
	}
	operand, isAwait := AwaitedOperandOf(statement.AsExpressionStatement().Expression)
	if !isAwait {
		return nil, false
	}
	elements, isPromiseAll := promiseAllArrayOf(operand)
	if !isPromiseAll {
		return nil, false
	}
	var out []kernelbridge.IrStatement
	for _, element := range elements {
		call := Unwrapped(element)
		if !ast.IsCallExpression(call) {
			return nil, false
		}
		lowered, ok := SummaryCallOrHavoc(context, call, -1)
		if !ok {
			return nil, false
		}
		out = append(out, lowered...)
	}
	return out, true
}

// promiseAllArrayOf is the ARRAY LITERAL argument of `Promise.all([…])`
// — its elements, or (nil, false) for any other shape. A spread, a named
// array, a `.map` result, or a second argument all decline.
func promiseAllArrayOf(node *ast.Node) ([]*ast.Node, bool) {
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
	if !ast.IsIdentifier(property.Name()) || property.Name().Text() != "all" {
		return nil, false
	}
	if call.Arguments == nil || len(call.Arguments.Nodes) != 1 {
		return nil, false
	}
	literal := Unwrapped(call.Arguments.Nodes[0])
	if !ast.IsArrayLiteralExpression(literal) {
		return nil, false
	}
	for _, element := range literal.AsArrayLiteralExpression().Elements.Nodes {
		if ast.IsSpreadElement(element) || ast.IsOmittedExpression(element) {
			return nil, false
		}
	}
	return literal.AsArrayLiteralExpression().Elements.Nodes, true
}
