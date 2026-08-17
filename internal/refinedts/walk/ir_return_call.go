// The RETURN-POSITION CALL arm: `return f(…)` where the callee has no
// compiled summary and inlining never resolves it — a call the
// STATEMENT route already knows how to serve (SummaryCallOrHavocNamed,
// ir_summary_call.go) through its own tiers: the compiled-summary
// splice, the recursion floor, the closure write-set havoc, the
// imported-hook recognizer, and — where none of those vouch for the
// call — the opaque call havoc that writes `unknown` into the target
// and into every flattened-local leaf the receiver or arguments
// mention.
//
// Before this file, a return whose value was a call with no
// FunctionContract (a React/redux hook: `return useAppSelector(sel)`,
// `return useContext(Ctx)`) fell all the way to the return route's own
// OPAQUE RETURN, which writes #ret unknown and notes the havoc — the
// same VALUE as this arm answers, but WIDER: the opaque return's own
// mention-havoc has no reading for a call at all, so it could only ever
// name the return construct and decline, never actually cover what the
// call's receiver or arguments mention. This arm reuses the statement
// route's own call machinery with target = context.Result.Ret, so a
// return-position call now serves EXACTLY as well as the equivalent
// `const v = f(…); return v;` two-statement spelling already did.
package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// returnCallStatements lowers `return <call>` through the statement
// route's own call door. `head` is the return expression's unwrapped
// call node (an await already peeled off by the caller, matching every
// other return arm's own convention).
//
// SummaryCallOrHavocNamed's own three tiers decide the shape:
//
//  1. a callee with a compiled COMPLETE summary → the call statement,
//     spliced with target = #ret, exactly as the inlining route above
//     it in lowerReturnStatement would have answered had InlineCall
//     resolved the callee instead — this arm is tried BELOW that route,
//     so it only ever reaches a callee InlineCall already declined.
//  2. a callee whose build is in flight (recursion) → TOP into #ret,
//     the recursion floor's own unconditional-sound answer.
//  3. every other callee — unresolvable, no blob, an imported hook —
//     → the opaque call havoc: #ret takes unknown, and so does every
//     flattened-local leaf the call could have written through its
//     receiver or arguments. The imported-hook recognizer inside tier 3
//     narrows this further: where every argument is vouched for
//     (writeAndCallFree, or a closure that writes no tracked slot), NO
//     slot beyond #ret is havocked at all.
//
// The raise is appended by the caller (lowerReturnStatement), the same
// discipline every other return arm keeps — this function answers only
// the value-writing statements.
func returnCallStatements(
	context *LoweringContext,
	head *ast.Node,
) ([]kernelbridge.IrStatement, bool) {
	if context == nil || context.Result == nil || head == nil || !ast.IsCallExpression(head) {
		return nil, false
	}
	// the construct name carries the SAME "return (call …)" spelling
	// OpaqueReturnName would have given this site had it fallen all the
	// way to the opaque return below — a body this arm still cannot
	// fully vouch for (no argument-freedom, no blob, no closure) reads
	// its histogram row exactly as it did before this arm existed; only
	// a body the imported-hook/blob/closure tiers DO vouch for moves
	// from "porous, this name" to COMPLETE.
	construct := "return (" + havocCallName(head) + ")"
	return SummaryCallOrHavocNamed(context, head, context.Result.Ret, construct)
}
