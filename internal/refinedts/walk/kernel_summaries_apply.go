// The summary route's entry points and the COMPILE-ONCE/APPLY-PER-CALL
// serving rule: SummaryResult/SummaryResultIn/SummaryResultOn read a
// declaration's lowered summary (kernel_summaries_memo.go) and a call's
// argument states (kernel_summaries_entry_states.go) and ask the kernel
// for the exit row, then decide — under the serve-only-when-it-
// determines rule — whether that row answers the call at all.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
	"github.com/microsoft/typescript-go/internal/refinedts/tracing"
)

// SummaryResult is the summary route as an older call site would
// spell it — a program handle and a callee resolver, with no walk
// context. The route runs off the DECLARATION alone and resolves
// callees through the walk's own contract registry, so a caller that
// hands over only a program handle gets a context with no registry:
// its bodies lower, and any call inside them declines. Every live
// caller has moved to SummaryResultIn; this spelling stays for the
// signature's one historical shape.
func SummaryResult(
	p *program.CheckerProgram,
	declaration *ast.Node,
	argKnowns []abstractdomain.AbstractValue,
	resolveCallee func(callee *ast.Node) *ast.Node,
) (abstractdomain.AbstractValue, bool) {
	return applySummary(&FlowContext{P: p}, declaration, argKnowns, unknownReceiver(), false)
}

// SummaryResultIn is the same route with the walk's own context — the
// spelling a caller that HAS a FlowContext should use, so composed
// calls resolve through its contract registry.
//
// The receiver is UNKNOWN here: this spelling holds no call node to read
// one off, so a method's this-entries all fill TOP — sound, because the
// entries are what the summary quantifies over. A caller that can reach
// the receiver goes through SummaryResultOn.
func SummaryResultIn(ctx *FlowContext, declaration *ast.Node, argKnowns []abstractdomain.AbstractValue) (abstractdomain.AbstractValue, bool) {
	return applySummary(ctx, declaration, argKnowns, unknownReceiver(), false)
}

// SummaryResultExactIn is SummaryResultIn where the caller ALSO knows
// the call's own argument list is EXACT — the effective-arguments
// reading found no unread spread, so every position (a trailing rest
// parameter's tail included) is really the one the call wrote. A
// summary's own compiled program still cannot carry that: it is
// proved once for every call, so a REST parameter's entry always
// enters TOP (summaryEntryStates), whatever this one call passed —
// and an ARRAY-TYPED parameter's TWO entries (its "p.len"/"p.elem"
// pair) enter TOP the same unconditional way, for the same reason:
// the compiled program is reused across every call, so it cannot
// carry one call's own array length or element values either. A
// TOP-fed rest or array-parameter read then answers a TOP ret, and a
// COMPLETE body's own serving rule (applySummary's comment) would
// otherwise hand that TOP back as the call's answer — discarding the
// caller's own exact tuple for nothing the summary route could ever
// have used it for. EXACT tells applySummary to decline THAT ONE
// serving instead: the call falls through to the walk-based recovery
// (recoverPureBody/InlineContractBody), which binds the rest
// parameter through ParameterKnown's own exact list, or the array
// parameter through BoundParameterKnown/ReadDestructuring's own
// element reads, and reads what the summary could not spell.
//
// Every OTHER answer a summary can determine — a scalar ret, an
// object member, anything not fed from a topped rest or array slot —
// is unaffected: EXACT only removes the one case where TOP-serving
// would have thrown away knowledge the call actually had.
func SummaryResultExactIn(ctx *FlowContext, declaration *ast.Node, argKnowns []abstractdomain.AbstractValue) (abstractdomain.AbstractValue, bool) {
	return applySummary(ctx, declaration, argKnowns, unknownReceiver(), true)
}

// SummaryResultOn is SummaryResultIn with the call's RECEIVER supplied —
// what a method's this-entries are filled from.
func SummaryResultOn(
	ctx *FlowContext,
	declaration *ast.Node,
	argKnowns []abstractdomain.AbstractValue,
	receiver abstractdomain.AbstractValue,
) (abstractdomain.AbstractValue, bool) {
	return applySummary(ctx, declaration, argKnowns, receiver, false)
}

// unknownReceiver is the receiver a call site that reached none supplies:
// a value nothing is known about, which fills every this-entry TOP.
func unknownReceiver() abstractdomain.AbstractValue {
	return silence.Residue()
}

// applySummary is the summary route: COMPILE once per declaration,
// apply per call.
//
// What crosses the wire: the body's IR travels exactly once ever, in
// the AskSummarize that builds the declaration's blob; every call
// after that sends only its own entry states to AskApplySummary. The
// answer handling below is unchanged from the whole-body walk — the
// same result/done slot reading, the same TOP decline.
//
// (result, false) — no claim — wherever the body, the arguments, or
// the kernel decline; the JS inline walk then serves exactly as
// before.
//
// EXACT (the exact parameter, and the ExactIn/ExactOn callers that pass
// true) distinguished a TOP-fed rest/array entry's serve from a
// genuinely unconstrained body's, back when a TOP ret still served
// silence conditionally. The serve-only-when-it-determines rule below
// now declines EVERY TOP/unknown ret unconditionally, whatever produced
// it — a REST parameter's always-TOP entry, an ARRAY-TYPED parameter's
// always-TOP pair, or anything else — so EXACT no longer changes this
// function's answer. It still selects which entry-state reading a
// caller wants (summaryEntryStates is unaffected), so the parameter and
// its dedicated callers (SummaryResultExactIn, KernelSummaryDirectExactOn)
// stay, but nothing here branches on it anymore.
func applySummary(
	ctx *FlowContext,
	declaration *ast.Node,
	argKnowns []abstractdomain.AbstractValue,
	receiver abstractdomain.AbstractValue,
	exact bool,
) (abstractdomain.AbstractValue, bool) {
	if EngineKernelHeld() == nil {
		return abstractdomain.AbstractValue{}, false
	}
	// the slot layout the answer is read out of comes from the lowering;
	// the blob is what the kernel compiled from it
	summary, lowered := LowerSummaryBody(ctx, declaration)
	if !lowered {
		return abstractdomain.AbstractValue{}, false
	}
	blob, hasBlob := SummaryBlobFor(ctx, declaration)
	if !hasBlob {
		return abstractdomain.AbstractValue{}, false
	}
	// entry states: the arguments' own knowledge; every other slot —
	// locals and composition's grown ones — starts absent, and each
	// inlined done flag is assigned {0} in the statements themselves;
	// a state the wire cannot spell declines THIS call, not the summary
	states, statesOk := summaryEntryStates(ctx, declaration, summary, argKnowns, receiver)
	if !statesOk {
		return abstractdomain.AbstractValue{}, false
	}
	for len(states) < summary.SlotCount {
		states = append(states, absentState)
	}
	states[summary.DoneIndex] = doneDownState
	exits, ok := kernelbridge.AskApplySummary(blob, states)
	if !ok {
		return abstractdomain.AbstractValue{}, false
	}
	if summary.RetIndex >= len(exits) || summary.DoneIndex >= len(exits) {
		return abstractdomain.AbstractValue{}, false
	}
	retExit := exits[summary.RetIndex]
	doneExit := exits[summary.DoneIndex]
	// THE SERVING RULE: only a COMPLETE body serves, and only where it
	// DETERMINES something — a TOP/unknown ret declines instead of
	// serving silence (the block below). The compile is proved equal
	// to the kernel walk (summarize_eq), and the lowering that produced
	// it read every statement, so a served answer carries the same
	// knowledge the inline walk would derive — but "the same knowledge"
	// is nothing when that knowledge is TOP, and serving nothing ahead
	// of a walk-based recovery that determines something is a strictly
	// worse answer, never a cheaper equal one. A POROUS or unrecorded
	// outcome DECLINES OUTRIGHT, even with a concrete ret: a havocked
	// construct is precisely a place the walk still reads and the
	// lowering does not, so a porous answer can be weaker than the
	// inline walk's — and serving those was measured (recharts,
	// 2026-08-13) to widen the downstream sets until the call-site join
	// machinery burned 14x the file's whole former wall. Porous blobs
	// still count coverage; they no longer answer calls.
	outcome, _, recorded := SummaryOutcomeOf(checkerOf(ctx), declaration)
	if !recorded || outcome != SummaryComplete {
		return abstractdomain.AbstractValue{}, false
	}
	// THE MEMBER-CARRYING RETURN. A body whose returns are object or array
	// literals wrote each member into its own slot, so the value is
	// rebuilt from those exits rather than read off the scalar #ret —
	// which for such a body holds unknown by construction, the object
	// having no scalar spelling. Everything below (the trust floor, the
	// async wrapping) applies to the rebuilt value the same way.
	if summary.RetShape != RetShapeNone && len(summary.RetMembers) > 0 {
		if rebuilt, rebuiltOk := summaryMemberResult(summary, exits, doneExit); rebuiltOk {
			tracing.Count("summaryServed", 0)
			return promiseWrappedIfAsync(declaration,
				abstractdomain.AtTrustLevel(rebuilt, summaryTrustFloor(ctx, declaration, summary, argKnowns, receiver))), true
		}
		return abstractdomain.AbstractValue{}, false
	}
	// THE RET ROW SPLIT. The ret slot at the exit stands for every run:
	// the values the returns wrote, and — on a body that guards with
	// `if (x) throw` — the thrown exits that never reached a return.
	// Returned() is the half the runs that COMPLETED left there, which
	// the kernel proves admits every non-thrown outcome the whole state
	// admitted (returned_denotes, set_functions/known_state.lean). The
	// throw arm wrote the THROWN outcome, so the returned half drops it
	// and `if (x) throw new E(); return v` reads `v` rather than
	// `v ∪ undefined`.
	//
	// The result slot's own absent flag is the ENTRY state surviving the
	// joins — path correlation the encoding routes through the done flag
	// instead: every RETURNED value was written into the set, and only a
	// fall-off path leaves undefined, which is exactly the flag-still-down
	// case decided below.
	returned := retExit.Returned()
	// Top must ride along here: KnownOfState's own gate (`if s.Top:
	// return silence.Residue()`) is what turns a TOP ret into
	// KindUnknown, which is what lets the decline below ever run.
	// Building this wire with Top left at its zero value (false) made
	// a TOP ret read as KindSet over an EMPTY RefinedSet instead —
	// FormatForDiagnostics' own "any value" spelling for zero Forms,
	// RTS7001's "not assignable" symptom on an in-set leg, and a
	// decline gate that never got the chance to run because
	// answer.Kind was never KindUnknown to begin with.
	answer := KnownOfState(kernelbridge.KnownStateWire{Top: returned.Top, Set: returned.Set, Undef: false, Null: false, Nan: returned.Nan})
	if answer.Kind == abstractdomain.KindUnknown {
		// THE SERVE-ONLY-WHEN-IT-DETERMINES RULE. A COMPLETE body whose ret
		// still comes back TOP/unknown has determined NOTHING about the
		// call's value — the compile is proved equal to the kernel walk
		// (summarize_eq), but "equal to a walk that also answers nothing" is
		// not a claim serving silence with ok=true earns any right to make
		// ahead of a walk-based recovery that CAN determine a value. The
		// route declines here exactly as a POROUS or unrecorded outcome
		// already declines above: an unknown ret is unconditionally not
		// served, whatever produced it — a TOP-fed rest/array entry
		// (summaryEntryStates' own always-TOP fill), an array-producing
		// return with no scalar slot to hold it, or a logical-join
		// (`??`/`||`/`&&`) over an operand this call's own arguments prove
		// absent or defined. The distinction is the ANSWER's emptiness, not
		// the body's syntax — every caller of applySummary (SummaryResult,
		// SummaryResultIn/ExactIn, SummaryResultOn, KernelSummaryDirectOn,
		// KernelSummaryDirectExactOn) already treats ok=false as "try the
		// next route," so a call that used to get a served-but-empty answer
		// now falls through to InlineContractCall/InlineContractBody's own
		// walk, which reads this call's OWN argument absence exactly.
		return abstractdomain.AbstractValue{}, false
	}
	// a path may fall off the end (the flag can still be down at exit):
	// the return is undefined on it
	allReturned := !doneExit.Top && !doneExit.Undef && !doneExit.Null && !mayContainZero(doneExit.Set)
	if !allReturned {
		answer = abstractdomain.PossiblyUndefined(answer, "", false, false)
	}
	tracing.Count("summaryServed", 0)
	return promiseWrappedIfAsync(declaration,
		abstractdomain.AtTrustLevel(answer, summaryTrustFloor(ctx, declaration, summary, argKnowns, receiver))), true
}

// summaryTrustFloor is the standing a served answer floors at: the
// standing of everything the entries were built from — the arguments,
// and a method's receiver.
//
// The argument walk is indexed by DECLARED PARAMETER, not by entry — and
// an EXPANDED parameter's entries were built from the object's FIELDS, so
// each read field's own grade joins the floor beside the object's: an
// object at proved standing whose lo came from a spec row states no more
// than spec.
//
// Both result routes read this one function — the scalar #ret and the
// rebuilt member object — so a value assembled from several exits carries
// exactly the standing a value read from one exit would.
func summaryTrustFloor(
	ctx *FlowContext,
	declaration *ast.Node,
	summary LoweredSummary,
	argKnowns []abstractdomain.AbstractValue,
	receiver abstractdomain.AbstractValue,
) abstractdomain.TrustLevel {
	floor := abstractdomain.TrustProved
	for index, parameter := range declaration.Parameters() {
		if index >= len(argKnowns) {
			continue
		}
		argument := argKnowns[index]
		floor = abstractdomain.MinTrustLevel(floor, abstractdomain.TrustLevelOf(argument))
		members, expanded := recordParamMembersIn(ctx, parameter)
		if !expanded {
			continue
		}
		for _, member := range members {
			// objectKeyIndex reads the ARGUMENT'S OWN top-level fields, so
			// only a DEPTH-1 member (len(Path) == 1) can ever name one of
			// them — a nested member's value sits inside a child object
			// this flat lookup does not walk, and contributes nothing to
			// the floor rather than being read under the wrong key.
			if len(member.Path) != 1 {
				continue
			}
			if at, has := objectKeyIndex(argument, member.Key); has {
				floor = abstractdomain.MinTrustLevel(floor, abstractdomain.TrustLevelOf(argument.Keys[at].Value))
			}
		}
	}
	// a METHOD's this-entries were built from the RECEIVER's fields, so
	// each read field's own standing joins the floor the same way — a
	// receiver at proved standing whose container came from a spec row
	// states no more than spec. A field the receiver does not name entered
	// TOP and claims nothing, so it floors nothing.
	if receiver.Kind == abstractdomain.KindObject {
		for _, entry := range summary.BundleEntries {
			field, isThis := thisFieldNameOf(entry.Path)
			if !isThis {
				continue
			}
			if at, has := objectKeyIndex(receiver, field); has {
				floor = abstractdomain.MinTrustLevel(floor, abstractdomain.TrustLevelOf(receiver.Keys[at].Value))
			}
		}
		floor = abstractdomain.MinTrustLevel(floor, abstractdomain.TrustLevelOf(receiver))
	}
	return floor
}

// summaryMemberResult rebuilds the RETURNED VALUE from the member exits
// the layout allocated for it: an object from one exit per key, or a
// sequence from the ".len"/".elem" pair.
//
// The kernel answers the WHOLE exit row (kernelApplySummary maps
// encodeState over every state), and summarize_eq proves that row equal
// to the walk's — for any index, not only #ret — so reading several
// slots' exits carries exactly the soundness reading one does.
//
// A member whose exit says nothing takes SILENCE in its key, not an
// absence: unknown in one member claims nothing about that member while
// the readable ones keep their values, which is the partial object this
// route exists to serve. A member whose exit is ABSENT is a key the
// returning path did not write — the object genuinely has no such key on
// that path, so the key carries "possibly undefined" and the object stays
// honest about it.
//
// COMPLETENESS is false. The literal's own key set is complete by
// construction, but the member slots name only the keys the layout could
// spell, and a body whose literal held a member this reader passed over
// has keys not in these rows. Claiming complete would claim the absence
// of keys the rows never enumerated.
//
// (false) where a path may fall off the end without returning: the value
// is then sometimes the object and sometimes undefined, and an object
// with an undefined arm is not something these rows spell — the caller's
// own walk serves that body instead.
func summaryMemberResult(
	summary LoweredSummary,
	exits []kernelbridge.KnownStateWire,
	doneExit kernelbridge.KnownStateWire,
) (abstractdomain.AbstractValue, bool) {
	// every path returned, or the value is sometimes undefined — which
	// these rows have no arm for
	if doneExit.Top || doneExit.Undef || doneExit.Null || mayContainZero(doneExit.Set) {
		return abstractdomain.AbstractValue{}, false
	}
	memberValue := func(index int) (abstractdomain.AbstractValue, bool) {
		if index < 0 || index >= len(exits) {
			return abstractdomain.AbstractValue{}, false
		}
		return KnownOfState(exits[index]), true
	}
	switch summary.RetShape {
	case RetShapeObject:
		keys := make([]abstractdomain.ObjectKey, 0, len(summary.RetMembers))
		for _, member := range summary.RetMembers {
			value, has := memberValue(member.Index)
			if !has {
				return abstractdomain.AbstractValue{}, false
			}
			keys = append(keys, abstractdomain.ObjectKey{Name: member.Name, Value: value})
		}
		if len(keys) == 0 {
			return abstractdomain.AbstractValue{}, false
		}
		return abstractdomain.KnownObject(keys, nil, false, abstractdomain.TrustProved, false), true
	case RetShapeArray:
		// both the ".len" and ".elem" exits are computed here, but this
		// route has no single-value spelling for the pair yet: serving
		// length alone as a KnownObject was a claim strictly weaker than
		// the inline walk's own answer for the same return (MapOutcome,
		// callback_element_outcome.go, builds the real element-bounded
		// repetition set from the same exits) — every element consumer
		// downstream (an inline parameter meet, iteration, division) then
		// read a length-only object where an element-bounded array should
		// have been. Declining here instead routes the call to that
		// walk-based recovery, which is what this file's own decline
		// discipline (applySummary's comment, and the false-ok returns
		// throughout this function) already requires: no claim rather
		// than a weaker one.
		return abstractdomain.AbstractValue{}, false
	}
	return abstractdomain.AbstractValue{}, false
}
