// A call through a BARE IMPORTED IDENTIFIER whose own declaration this
// lowering never reads — `useAppSelector(selectSomething)`,
// `useContext(SomeContext)`, `useAppDispatch()`. React/redux hooks
// imported from `react` or an app's own state module are the common
// shape (tmp/recharts-src/src/state/hooks.ts): the callee resolves to
// no FunctionContract (a hook's body lives past a module boundary, or
// is a signature-only .d.ts), so every such call fell to the opaque
// havoc floor before this file — sound, but wider than it needs to be.
//
// THE SOUND MODEL this file adds a floor above. A call whose every
// argument is writeAndCallFree, or whose only non-simple argument is a
// FUNCTION LITERAL that captures no WRITE of this body's tracked
// slots, hands the callee no reference through which it could move a
// slot this lowering tracks — a selector like
// `useAppSelector(state => selectAxisWithScale(state, xAxisId))`
// passes `xAxisId` by VALUE inside a closure the callee may run at any
// time, but never writes it, so nothing here needs to be believed
// forgotten. The call may still read and write the OUTSIDE world
// freely (the hook itself, the module state it closes over, a real
// DOM/store side effect) — this file claims nothing about any of
// that, only that THIS body's own tracked slots survive the call
// unmoved.
//
// WHAT THIS DELIBERATELY DOES NOT MODEL: hook semantics, store state,
// re-renders. The call's own VALUE is always unknown, exactly as the
// opaque floor already answers it — the only change is that no
// tracked slot is havocked alongside it, because none could have
// been reached.
//
// GATED TIGHT. The callee must be a bare imported identifier — its
// declaration resolves, through symbolAt's alias hop, to a node
// outside this file (another module) or inside a declaration file.
// A LOCAL function or closure must never match here: this recognizer
// is tried BELOW SummaryCallOrHavocNamed's blob/summary tier and its
// closure tier (ir_summary_call.go) precisely so a resolvable local
// callee is served by its own stronger route first, and only a callee
// those routes already declined on reaches this one. The map/set
// receiver recognizers (ir_summary_field_map_calls.go,
// ir_summary_module_set_calls.go) sit ABOVE those tiers instead
// because their receivers are never a plain identifier a
// FunctionContract could resolve — this file's callee IS a plain
// identifier, so it must wait its turn.
package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// importedHookCallOf recognizes `<name>(args)` where `name` is a bare
// identifier resolving — through symbolAt's alias hop — to a
// declaration this lowering does not read: one sitting in a different
// source file, or in a declaration file. Answers the callee identifier
// and the call's arguments.
func importedHookCallOf(context *LoweringContext, call *ast.Node) (identifier *ast.Node, arguments []*ast.Node, ok bool) {
	if context == nil || context.Flow == nil || context.Flow.P == nil || context.Flow.P.Checker == nil {
		return nil, nil, false
	}
	if !ast.IsCallExpression(call) {
		return nil, nil, false
	}
	callExpr := call.AsCallExpression()
	callee := Unwrapped(callExpr.Expression)
	if callee == nil || !ast.IsIdentifier(callee) {
		return nil, nil, false
	}
	if !resolvesOutsideThisFile(context.Flow, callee) {
		return nil, nil, false
	}
	if callExpr.Arguments != nil {
		arguments = callExpr.Arguments.Nodes
	}
	return callee, arguments, true
}

// resolvesOutsideThisFile answers whether an identifier's declaration
// — through the alias hop symbolAt already follows for every import —
// sits in a file OTHER than the one the call itself is written in, or
// in a declaration file. Either reading means the checker has no body
// here to read: a same-file local (a `function`/`const` this lowering
// might inline or summarize) always answers false, which is what
// keeps a local callee out of this recognizer's gate — the earlier
// tiers already had first refusal on it.
func resolvesOutsideThisFile(ctx *FlowContext, identifier *ast.Node) bool {
	symbol := symbolAt(ctx.P.Checker, identifier)
	if symbol == nil || len(symbol.Declarations) == 0 {
		return false
	}
	callerFile := ast.GetSourceFileOfNode(identifier)
	for _, declaration := range symbol.Declarations {
		declaredFile := ast.GetSourceFileOfNode(declaration)
		if declaredFile.IsDeclarationFile || declaredFile != callerFile {
			continue
		}
		// at least one declaration sits in THIS file, undeclared —
		// a same-file overload or a genuinely local binding, either
		// of which the earlier tiers own
		return false
	}
	return true
}

// importedHookArgumentsFree answers whether every argument hands the
// callee no reference through which it could move a slot this
// lowering tracks: an ordinary writeAndCallFree argument passes by
// value outright, and a bare FUNCTION LITERAL argument (an arrow or
// function expression — a selector, a comparator) is admitted too
// exactly when its own body WRITES no slot this context tracks
// (ClosureWriteSlots, the same census a stored closure's hand-over
// havoc reads). A closure that only READS a captured name — the
// common selector shape, `state => selectThing(state, xAxisId)` — is
// free: the value moves into the closure by value, same as any other
// read, and nothing about calling the closure later can un-read it.
//
// Any other non-simple argument (a call, a `new`, an await, a nested
// object/array literal wrapping a call) declines — this recognizer
// only ever narrows the SET of arguments it can vouch for, never
// reasons about what an unvouched one might do.
func importedHookArgumentsFree(context *LoweringContext, arguments []*ast.Node) bool {
	for _, argument := range arguments {
		if writeAndCallFree(argument) {
			continue
		}
		literal := Unwrapped(argument)
		if literal == nil || !ast.IsFunctionLike(literal) {
			return false
		}
		if len(ClosureWriteSlots(context, literal)) != 0 {
			return false
		}
	}
	return true
}

// importedHookCallStatement lowers a recognized imported-hook call to
// its statement, or declines. Tried in SummaryCallOrHavocNamed
// (ir_summary_call.go) BELOW the blob/summary tier and the closure
// tier — see this file's header for why the ordering is load-bearing.
//
// `target` is the caller slot the call's value lands in, or -1 for a
// bare call whose value nothing reads. There is no receiver here (a
// bare call has none), so unlike the field-map/module-set statements'
// -1 case there is no identity no-op to write either — a discarded
// call whose arguments are all vouched-for moves nothing this
// lowering tracks, so the sound and complete answer is to lower NO
// statement at all, exactly the "return nil, true" idiom every other
// genuinely-empty route in this package already answers with.
func importedHookCallStatement(context *LoweringContext, call *ast.Node, target int) ([]kernelbridge.IrStatement, bool) {
	if context == nil || context.Flow == nil {
		return nil, false
	}
	_, arguments, isHookCall := importedHookCallOf(context, call)
	if !isHookCall {
		return nil, false
	}
	if !importedHookArgumentsFree(context, arguments) {
		return nil, false
	}
	if target < 0 {
		return nil, true
	}
	return []kernelbridge.IrStatement{
		{Kind: kernelbridge.IrStatementAssign, Target: target, Effect: unknownEffect},
	}, true
}
