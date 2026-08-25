// What a served summary moves in the CALLER's world: whether the
// receiver must be forgotten, which argument positions carry a
// written parameter bundle, and the exit values of any written
// this-fields — the read side of applySummary's own call
// (kernel_summaries_apply.go) that the caller's write-back seam folds
// onto its tracked env instead of a blanket forget.

package walk

import (
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// SummaryReceiverEffects answers what a served summary moves in the
// CALLER's world: whether the receiver must be forgotten (a written
// this-field, or a returned receiver — the caller would otherwise keep
// object knowledge the body moved or may move through the alias), and
// which ARGUMENT positions carry a parameter bundle the body writes.
// The direct apply route reads this and applies the same ForgetThrough
// the opaque path applies; without it a served answer leaves stale
// Keys behind — the exact asymmetry the opaque path never had.
func SummaryReceiverEffects(ctx *FlowContext, declaration *ast.Node) (receiverTouched bool, writtenArguments []int) {
	summary, ok := LowerSummaryBody(ctx, declaration)
	if !ok {
		return false, nil
	}
	receiverTouched = summary.ReturnsReceiver
	parameters := declaration.Parameters()
	names := make([]string, len(parameters))
	for index, parameter := range parameters {
		pd := parameter.AsParameterDeclaration()
		if pd.Name() != nil && ast.IsIdentifier(pd.Name()) {
			names[index] = pd.Name().Text()
		}
	}
	seen := map[int]struct{}{}
	for _, entry := range summary.BundleEntries {
		if !entry.Written {
			continue
		}
		if strings.HasPrefix(entry.Path, "this.") {
			receiverTouched = true
			continue
		}
		for index, name := range names {
			if name != "" && strings.HasPrefix(entry.Path, name+".") {
				if _, held := seen[index]; !held {
					seen[index] = struct{}{}
					writtenArguments = append(writtenArguments, index)
				}
			}
		}
	}
	return receiverTouched, writtenArguments
}

// SummaryWrittenThisExits answers the EXIT VALUES of a served summary's
// written this-fields, keyed by bare field name — what the callee's
// body left in each receiver field it wrote, computed by the same
// compile-once/apply-per-call ask applySummary makes (the question
// cache makes the repeated ask free). The serving seam folds these onto
// the caller's tracked receiver object in place of the whole-receiver
// forget (foldWrittenReceiverExits), which is what carries
// `over.write(200)`'s 200 into the caller's `over.#held` instead of
// wiping everything the caller knew.
//
// (nil, false) — the caller keeps the forget — wherever the summary is
// not COMPLETE, the body returns its receiver (the alias moves
// knowledge no exit spells), any written exit is TOP or rides a thrown
// path, or an exit state converts to no value.
func SummaryWrittenThisExits(
	ctx *FlowContext,
	declaration *ast.Node,
	argKnowns []abstractdomain.AbstractValue,
	receiver abstractdomain.AbstractValue,
) (map[string]abstractdomain.AbstractValue, bool) {
	if EngineKernelHeld() == nil {
		return nil, false
	}
	summary, lowered := LowerSummaryBody(ctx, declaration)
	if !lowered || summary.ReturnsReceiver {
		return nil, false
	}
	if outcome, _, recorded := SummaryOutcomeOf(checkerOf(ctx), declaration); !recorded || outcome != SummaryComplete {
		return nil, false
	}
	blob, hasBlob := SummaryBlobFor(ctx, declaration)
	if !hasBlob {
		return nil, false
	}
	states, statesOk := summaryEntryStates(ctx, declaration, summary, argKnowns, receiver)
	if !statesOk {
		return nil, false
	}
	for len(states) < summary.SlotCount {
		states = append(states, absentState)
	}
	states[summary.DoneIndex] = doneDownState
	exits, ok := kernelbridge.AskApplySummary(blob, states)
	if !ok {
		return nil, false
	}
	floor := summaryTrustFloor(ctx, declaration, summary, argKnowns, receiver)
	out := map[string]abstractdomain.AbstractValue{}
	for _, entry := range summary.BundleEntries {
		if !entry.Written {
			continue
		}
		field, isThis := thisFieldNameOf(entry.Path)
		if !isThis {
			continue
		}
		if entry.Index < 0 || entry.Index >= len(exits) {
			return nil, false
		}
		exit := exits[entry.Index]
		if exit.Top || exit.Thrown {
			return nil, false
		}
		value := KnownOfState(kernelbridge.KnownStateWire{Set: exit.Set, Undef: exit.Undef, Null: exit.Null, Nan: exit.Nan})
		if value.Kind == abstractdomain.KindUnknown {
			return nil, false
		}
		out[field] = abstractdomain.AtTrustLevel(value, floor)
	}
	return out, len(out) > 0
}

// constEffectState reads a CONST or CONSTSTATE effect as the entry
// state it spells — the only two effect kinds whose meaning does not
// depend on any binding space, which is what lets a callee's lowered
// default cross to a caller's entry vector.
func constEffectState(effect kernelbridge.LoopEffect) (kernelbridge.KnownStateWire, bool) {
	switch effect.Kind {
	case kernelbridge.LoopEffectConst:
		return kernelbridge.KnownStateWire{Set: effect.Set}, true
	case kernelbridge.LoopEffectConstState:
		// the effect's Undef/Null pair maps directly to the state wire's
		// own pair — no conflation, each admission crosses on its own flag
		return kernelbridge.KnownStateWire{Set: effect.Set, Undef: effect.Undef, Null: effect.Null, Nan: effect.Nan}, true
	}
	return kernelbridge.KnownStateWire{}, false
}

// declarationHasRestParameter: whether the declaration binds a
// trailing rest parameter — the one shape summaryEntryStates always
// feeds TOP, whatever a call passed (its own comment).
//
// UNUSED BY applySummary as of the serve-only-when-it-determines rule:
// a TOP ret now declines unconditionally, whatever produced it, so the
// EXACT-gated distinction this once carved out (a TOP-fed rest entry
// versus a genuinely unconstrained body) is moot — both decline the
// same way. Kept for a caller that still wants the syntactic fact
// alone; not consulted by the serving rule anymore.
func declarationHasRestParameter(declaration *ast.Node) bool {
	for _, parameter := range declaration.Parameters() {
		if parameter.AsParameterDeclaration().DotDotDotToken != nil {
			return true
		}
	}
	return false
}

// declarationHasArrayParameter: whether the declaration binds a
// parameter that flattens to the two-slot "p.len"/"p.elem" pair
// (arrayParamSlotsIn) — the other shape summaryEntryStates always
// feeds TOP for BOTH entries, whatever exact array a call passed
// (summaryEntryStates' own comment: "the direct apply reads an
// argument's abstract value, which carries no length and no element
// join this route can spell").
//
// UNUSED BY applySummary as of the serve-only-when-it-determines rule
// (kernel_summaries.go's applySummary): a TOP ret now declines
// unconditionally, so the EXACT-gated distinction this once carved
// out is moot the same way declarationHasRestParameter's is. Kept
// for array_and_default_parameter_test.go's own direct pin on the
// syntactic fact; not consulted by the serving rule anymore.
func declarationHasArrayParameter(ctx *FlowContext, declaration *ast.Node) bool {
	for _, parameter := range declaration.Parameters() {
		if _, flattened := arrayParamSlotsIn(ctx, parameter); flattened {
			return true
		}
	}
	return false
}

// declarationReturnsArrayProducingCall: whether ANY return in the
// declaration's body carries an array-producing collection call
// (`.map`/`.filter`, or a `.reduce` whose accumulator spells an array —
// isArrayProducingCollectionCall's own reading, ir_summary_returned_shape.go)
// as its head — the shape whose scalar #ret holds TOP by construction (a
// member-shaped result written nowhere a scalar slot can hold it), not
// because the return value is unconstrained.
//
// UNUSED BY applySummary as of the serve-only-when-it-determines rule: a
// TOP ret now declines unconditionally, whatever produced it, so this
// distinction (TOP-by-construction versus TOP-because-unconstrained) no
// longer changes the outcome — both decline the same way, and the
// walk-based recovery (InlineContractBody's general walk, whose
// MapOutcome derives the real element-bounded array off the receiver as
// it is actually bound at the call) answers either way. Kept as a
// syntactic reader in case a narrower caller wants the distinction back;
// not consulted by the serving rule anymore.
//
// Mirrors returnedExpressionsOf's own body scan (ir_summary_returned_shape.go)
// rather than a fresh AST walk: the same nested-function-skip, the same
// concise-arrow-is-its-own-return reading, so this answers exactly the set
// of returns returnedLiteralShape itself would have looked at.
func declarationReturnsArrayProducingCall(declaration *ast.Node) bool {
	body := declaration.Body()
	if body == nil {
		return false
	}
	for _, returned := range returnedExpressionsOf(body) {
		head := Unwrapped(returned)
		if head == nil {
			continue
		}
		// isArrayProducingCollectionCall covers the bare-identifier
		// receiver and the array-accumulator reduce; the direct test
		// below covers the INTERIOR-PATH receiver (`request.samples
		// .map(cb)`) that collectionCallOf's identifier gate cannot
		// see — the exact shape whose pair never allocates and whose
		// scalar #ret is therefore TOP by construction. A broader test
		// is safe here: this helper only ever DECLINES a serve.
		if isArrayProducingCollectionCall(head) || returnHeadIsCollectionMapOrFilter(head) {
			return true
		}
	}
	return false
}

// returnHeadIsCollectionMapOrFilter: the return's head is a call whose
// callee is a `.map`/`.filter` property access, whatever the receiver's
// shape — the receiver-agnostic reading the carve-out needs, since the
// pair-allocating reader's own receiver gate (collectionCallOf's bare
// identifier) is exactly what makes this body's scalar ret TOP.
func returnHeadIsCollectionMapOrFilter(head *ast.Node) bool {
	if head == nil || !ast.IsCallExpression(head) {
		return false
	}
	callee := head.AsCallExpression().Expression
	if callee == nil || !ast.IsPropertyAccessExpression(callee) {
		return false
	}
	name := callee.AsPropertyAccessExpression().Name()
	if name == nil || !ast.IsIdentifier(name) {
		return false
	}
	return name.Text() == "map" || name.Text() == "filter"
}
