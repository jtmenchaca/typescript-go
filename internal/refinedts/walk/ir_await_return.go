// split from ir_await.go — the return route: `return await f(…)`, the
// adopted bare return, and the async question behind it.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

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
