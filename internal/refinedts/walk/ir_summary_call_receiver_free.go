// The RECEIVER-CALLEE twin of importedHookCallStatement
// (ir_summary_imported_hook_calls.go): `<receiver>.<method>(args)`
// where `receiver` is not necessarily a bare identifier and `method`
// resolves to a declaration OUTSIDE this file — a default-lib global
// (`document.getElementById`, `window.addEventListener`), an imported
// namespace member (`Children.forEach`), or a PARAMETER's own
// interface method (`listenerApi.getState()`, the shape
// tmp/recharts-src/src/state/keyboardEventsMiddleware.ts's listener
// effect calls).
//
// importedHookCallOf's own gate is a BARE IDENTIFIER callee — `f(x)`,
// never `o.m(x)` — so none of these shapes ever reached it, and every
// one fell to the opaque call floor. The floor is sound (it is what
// this file's own non-interference argument generalizes past), but it
// ran unconditionally: nothing here TRIED the write-and-call-free proof
// before havocking.
//
// THE ARGUMENT, restated for a receiver callee. A method declared
// outside this file has no body this lowering reads, so the only way
// calling it could move a tracked slot is through what the call HANDS
// OVER: the arguments, and the RECEIVER itself (the method may write
// back through `this`). Write-and-call-free arguments (or a write-free
// function-literal argument, importedHookArgumentsFree's own test) rule
// out the first channel exactly as they do for a bare-identifier hook.
// The second channel is not new either — withReceiverBundleHavoc
// already havocs a call's receiver bundle on every opaque route; this
// recognizer reuses the SAME receiverBundleHavocSlots reading rather
// than inventing a second one, so a served site and a havocked site
// agree about which slots the receiver contributes.
//
// GATED THE SAME WAY importedHookCallStatement is: tried in
// SummaryCallOrHavocNamed BELOW the blob/summary tier and the closure
// tiers, so a callee those tiers can still read (a same-file class
// method with a body, a resolvable local) keeps first refusal.
package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// receiverCalleeCallOf recognizes `<receiver>.<method>(args)` whose
// METHOD NAME resolves — through symbolAt's alias hop — to a
// declaration this lowering has no BODY to read: a different source
// file, a declaration file (a default-lib global's own method, a
// module namespace's exported function), or a SAME-FILE interface/type
// member signature — `listenerApi.getState()` where `listenerApi` is
// typed by a same-file `interface ListenerApi { getState(): … }`
// (tmp/recharts-src/src/state/keyboardEventsMiddleware.ts's shape). A
// signature has no statements to summarize or inline whatever file it
// sits in, so the same non-interference argument applies to it as to a
// cross-file method — the two are one condition,
// hasNoReachableMethodBody below.
//
// Declines a computed or optional method step, and a receiver this
// lowering cannot spell as a dotted path at all (dottedPathOf's own
// declines: a computed step, a call's result, a literal).
func receiverCalleeCallOf(context *LoweringContext, call *ast.Node) (arguments []*ast.Node, ok bool) {
	if context == nil || context.Flow == nil || context.Flow.P == nil || context.Flow.P.Checker == nil {
		return nil, false
	}
	if !ast.IsCallExpression(call) {
		return nil, false
	}
	callExpr := call.AsCallExpression()
	callee := Unwrapped(callExpr.Expression)
	if callee == nil || !ast.IsPropertyAccessExpression(callee) {
		return nil, false
	}
	access := callee.AsPropertyAccessExpression()
	if access.QuestionDotToken != nil {
		return nil, false
	}
	method := access.Name()
	if method == nil || !ast.IsIdentifier(method) {
		return nil, false
	}
	if !hasNoReachableMethodBody(context.Flow, method) {
		return nil, false
	}
	if receiverRootIsReassignable(context.Flow, access.Expression) {
		return nil, false
	}
	if callExpr.Arguments != nil {
		arguments = callExpr.Arguments.Nodes
	}
	return arguments, true
}

// receiverRootIsReassignable answers whether the receiver expression's
// ROOT identifier resolves to a SAME-FILE `let`/`var` binding — a name
// this lowering has no proof stays bound to the same value for the
// site's own duration. moduleSetCallStatement's own
// onceAssignedModuleSetConst gate (ir_summary_module_set_calls.go)
// draws this exact line for its narrower Set/Map family — a
// `let`-bound module receiver declines there too — and this recognizer
// respects the same boundary rather than silently serving a WEAKER
// claim (unknown value, no state pinned) through a receiver identity
// the stronger family already refused to trust.
//
// SAME-FILE ONLY: a global `declare var document: Document` /
// `declare var window: Window` sits in a declaration file, technically
// a `var` and therefore reassignable in the language's OWN terms, but
// application code never meaningfully reassigns the DOM globals, and a
// same-file reassignment scan (ReassignedNames) cannot see a
// declaration-file binding's uses across the whole program even if it
// COULD be reassigned somewhere reachable — so the file check is what
// keeps `document.getElementById(...)` served while still refusing a
// same-file `let MutableSet = new Set(...)`. A `const` root, a
// `this`-rooted receiver, and a PARAMETER all answer false and keep
// this recognizer's ordinary reading.
func receiverRootIsReassignable(ctx *FlowContext, receiver *ast.Node) bool {
	if ctx == nil || ctx.P == nil || ctx.P.Checker == nil {
		return false
	}
	root := Unwrapped(receiver)
	for ast.IsPropertyAccessExpression(root) {
		root = Unwrapped(root.AsPropertyAccessExpression().Expression)
	}
	if root == nil || !ast.IsIdentifier(root) {
		// `this`, a call result, a literal: no reassignable NAME roots the
		// receiver at all
		return false
	}
	symbol := symbolAt(ctx.P.Checker, root)
	if symbol == nil || symbol.ValueDeclaration == nil {
		return false
	}
	declaration := symbol.ValueDeclaration
	if !ast.IsVariableDeclaration(declaration) {
		// a parameter, a function declaration, a class: not a let/var/const
		// binding at all, so this gate has nothing to say about it
		return false
	}
	if ast.GetSourceFileOfNode(declaration).IsDeclarationFile {
		// a lib/ambient global: outside this scan's reach either way, and
		// application code does not reassign the DOM/BOM globals
		return false
	}
	if ast.GetSourceFileOfNode(declaration) != ast.GetSourceFileOfNode(root) {
		// a same-program, DIFFERENT-file binding: this file's own
		// ReassignedNames scan cannot see whether the OTHER file ever
		// reassigns it, so this reading has nothing to say — same
		// under-reach the module-set gate's own file-wide scan already
		// accepts for its own const check
		return false
	}
	list := declaration.Parent
	if list == nil || !ast.IsVariableDeclarationList(list) {
		return false
	}
	// same-file, `let`/`var`: declared mutable is the bar
	// onceAssignedModuleSetConst draws too — whether an actual
	// reassignment appears anywhere is beside the point that test holds,
	// so this reading does not additionally consult ReassignedNames.
	return list.Flags&ast.NodeFlagsConst == 0
}

// hasNoReachableMethodBody answers whether a property-access callee's
// METHOD NAME resolves to something this lowering could never inline or
// summarize: every one of the symbol's declarations is either outside
// this file (resolvesOutsideThisFile's own question) or a SIGNATURE
// with no body of its own — an interface method, a call/construct
// signature, an ambient method declaration. A symbol with even one
// same-file declaration carrying a real body answers false, which
// keeps a resolvable local method under the earlier tiers' first
// refusal exactly as resolvesOutsideThisFile already does for a bare
// identifier callee.
func hasNoReachableMethodBody(ctx *FlowContext, identifier *ast.Node) bool {
	if resolvesOutsideThisFile(ctx, identifier) {
		return true
	}
	symbol := symbolAt(ctx.P.Checker, identifier)
	if symbol == nil || len(symbol.Declarations) == 0 {
		return false
	}
	for _, declaration := range symbol.Declarations {
		if declaration.Body() != nil {
			// a real, same-file body: the blob/inline tiers own this callee
			return false
		}
		switch declaration.Kind {
		case ast.KindMethodSignature, ast.KindCallSignature, ast.KindConstructSignature,
			ast.KindPropertySignature, ast.KindFunctionType:
			continue
		default:
			// a declaration this reading does not recognize as bodyless: the
			// safe answer is "might have a body", so the callee stays with
			// whatever tier already declined on it
			return false
		}
	}
	return true
}

// receiverCalleeCallStatement lowers a recognized receiver-callee call
// to its statement, or declines. Tried in SummaryCallOrHavocNamed
// (ir_summary_call.go) alongside importedHookCallStatement, below every
// tier that owns a callee this lowering can read a body for.
//
// `target` is the caller slot the call's value lands in, or -1 for a
// bare call whose value nothing reads. The receiver's own slots — its
// leaves and its own whole-name slot where it has one — are havocked
// unconditionally: an external method may write back through `this`,
// and receiverBundleHavocSlots is the one reading every opaque route
// already trusts for that question.
func receiverCalleeCallStatement(context *LoweringContext, call *ast.Node, target int) ([]kernelbridge.IrStatement, bool) {
	if context == nil || context.Flow == nil {
		return nil, false
	}
	arguments, isReceiverCall := receiverCalleeCallOf(context, call)
	if !isReceiverCall {
		return nil, false
	}
	if !importedHookArgumentsFree(context, arguments) {
		return nil, false
	}
	slots := map[int]struct{}{}
	for _, slot := range receiverBundleHavocSlots(context, call) {
		if slot >= 0 {
			slots[slot] = struct{}{}
		}
	}
	out := havocAssignments(slots)
	if target >= 0 {
		out = append(out, kernelbridge.IrStatement{
			Kind:   kernelbridge.IrStatementAssign,
			Target: target,
			Effect: unknownEffect,
		})
	}
	return out, true
}
