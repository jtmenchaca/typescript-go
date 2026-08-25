// The direct-call entry points onto the summary route (tried ahead of
// the effect scan for every contracted call), and the async-boundary
// wrap applySummary's answer passes through before it reaches a caller.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// promiseWrappedIfAsync is the ret-as-inner convention's boundary: an
// async declaration's #ret holds the settled inner value, so the
// caller's view of the call is a PROMISE of it. The rule is exactly
// AsCalleeResult's — an unknown inner is silence.Residue() (no promise
// of nothing is worth spelling), a value already Promise-kinded passes
// through unwrapped (a returned promise is adopted, never double-
// wrapped), and everything else becomes Promise{Inner}. Read from the
// same ModifierFlagsAsync bit AsCalleeResult reads, so the two answer
// on the same declarations.
//
// WHERE it lands, and why here: the inline route wraps LAST. In
// InlineContractBody the walk finishes, the returned value takes its
// absence and its grade, and only then does `return AsCalleeResult(…)`
// run — the absence rides INSIDE the promise's inner there, because
// the wrapper closes over a value that already carries it. The Direct
// route is the same shape: inline_contract_body.go calls
// AsCalleeResult on whatever KernelSummaryDirect answered, after the
// answer is complete. So the wrap goes AFTER the fall-off
// PossiblyUndefined and AFTER the trust floor here too, which makes
// this line byte-identical to what the Direct route's outer
// AsCalleeResult already produced — the same inner, the same flags in
// the same order, the same grade. AsCalleeResult passes a
// KindPromise through untouched, so the Direct route's second
// application on this already-wrapped answer is the identity: no
// double wrap, and no route sees a different value than before.
func promiseWrappedIfAsync(declaration *ast.Node, answer abstractdomain.AbstractValue) abstractdomain.AbstractValue {
	if ast.GetCombinedModifierFlags(declaration)&ast.ModifierFlagsAsync == 0 {
		return answer
	}
	if answer.Kind == abstractdomain.KindPromise {
		return answer
	}
	if answer.Kind == abstractdomain.KindUnknown {
		return silence.Residue()
	}
	inner := answer
	return abstractdomain.AbstractValue{Kind: abstractdomain.KindPromise, Inner: &inner}
}

// KernelSummaryDirect is the summary route tried for EVERY contracted
// call, ahead of the effect scan — this port's completion of
// speed-ladder S5 ("apply per distinct argument tuple, not per
// call"; the TS source reaches the route only behind the effect-free
// gate). Sound without an effect pre-scan because the lowering is
// TOTAL-OR-DECLINE over effects: an assignment lowers only onto a
// param or local slot (a property write, an outer name, `this` — no
// slot, decline), a call lowers only as a resolvable contracted
// body's own slots (anything else, decline), throw/try/await/yield
// have no lowering, and scalar-only argument states mean no reference
// argument exists for the caller to observe. Whatever the lowering
// admits therefore has exactly one caller-visible outcome — the
// return value — which the kernel's walk_sound answer covers, and
// which summarize_eq carries through the compile.
// The receiver is UNKNOWN in this spelling, so a METHOD's this-entries
// all fill TOP. A caller that can reach the call's receiver goes through
// KernelSummaryDirectOn.
func KernelSummaryDirect(ctx *FlowContext, argKnowns []abstractdomain.AbstractValue, contract *FunctionContract) (abstractdomain.AbstractValue, bool) {
	return KernelSummaryDirectOn(ctx, argKnowns, contract, unknownReceiver())
}

// KernelSummaryDirectOn is the same route with the call's RECEIVER
// supplied — the value a METHOD's this-field entries are filled from,
// read off the call expression's property access in the caller's env
// (SummaryCallReceiver, inline_contract_body.go). A plain function call,
// and a receiver the caller could not read without running it, supply
// silence and every this-entry fills TOP.
func KernelSummaryDirectOn(
	ctx *FlowContext,
	argKnowns []abstractdomain.AbstractValue,
	contract *FunctionContract,
	receiver abstractdomain.AbstractValue,
) (abstractdomain.AbstractValue, bool) {
	if !summaryLowerable(contract.Declaration) {
		return abstractdomain.AbstractValue{}, false
	}
	return applySummary(ctx, contract.Declaration, argKnowns, receiver, false)
}

// KernelSummaryDirectExactOn is KernelSummaryDirectOn where the
// caller's own effective-arguments reading found the call EXACT — see
// SummaryResultExactIn's comment for what that changes: a rest
// parameter's TOP-fed entry no longer lets a COMPLETE body serve a
// TOP ret over this one call's own exact tail, so the inline route
// (InlineContractBody, whose ParameterKnown already builds that exact
// tail) gets the chance the plain spelling would have skipped past.
func KernelSummaryDirectExactOn(
	ctx *FlowContext,
	argKnowns []abstractdomain.AbstractValue,
	contract *FunctionContract,
	receiver abstractdomain.AbstractValue,
) (abstractdomain.AbstractValue, bool) {
	if !summaryLowerable(contract.Declaration) {
		return abstractdomain.AbstractValue{}, false
	}
	return applySummary(ctx, contract.Declaration, argKnowns, receiver, true)
}
