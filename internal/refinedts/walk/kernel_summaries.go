// from interprocedural/kernel_summaries.ts
//
// The callee summary: a function body lowers to the kernel's flow IR
// ONCE — returns encoded through a result slot and a done flag over
// the existing proved grammar (lowering_to_kernel_ir) — the kernel
// COMPILES that IR to a slot-program once per declaration, and every
// call applies the compiled program to its own argument states.
// Every piece the walk composes is individually proved and the
// composition theorem (walk_sound, set_functions/walk.lean) covers
// the whole body, returns included, because the encoding uses only
// the proved statements: `return e` is an assignment pair, and the
// continuation after a returning branch runs under an ordinary
// branch on the flag; the compile itself is proved faithful to that
// walk (summarize_eq), so an application carries the same soundness.
//
// A body the lowering cannot spell — an object, a string method, a
// loop that returns — declines here and keeps today's JS inline
// walk. The ledger counts which route served.

package walk

import (
	"strings"
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
	"github.com/microsoft/typescript-go/internal/refinedts/tracing"
)

// slotBudget is SLOT_BUDGET in the TS source: slots past this stop
// paying for themselves — a body carrying more locals than this is
// not the small computation summaries serve.
const slotBudget = 32

// LoweredSummary is Summary in the TS source (renamed to avoid
// colliding with function_summaries.go's exported Summarize/
// EffectSummary vocabulary — the TWO "summary" concepts in this
// directory are unrelated: an effect summary and a kernel-lowering
// summary). It is the body's IR plus the slot bookkeeping a call
// site reads its answer out of.
type LoweredSummary struct {
	Stmts      []kernelbridge.IrStatement
	ParamCount int
	DoneIndex  int
	RetIndex   int
	// SlotCount is every slot, composition's grown ones included —
	// the walk's state vector is this long.
	SlotCount int
	// Table is the composed-call table the lowering built: one entry
	// per callee an IrStatementCall statement indexes, in the order
	// the lowering assigned. It rides into AskSummarize beside the
	// statements. A body with no composed calls carries an empty
	// table.
	Table []kernelbridge.SummaryBlob
	// DefaultEffects: one lowered default per DEFAULTED parameter slot.
	// The body already applies the default under a definedness branch;
	// the compile joins branch arms (a summary quantifies over all
	// entries), so a call whose argument is DEFINITELY missing fills its
	// entry from this effect instead of absent — the join then collapses
	// to the default exactly. Only a CONST or CONSTSTATE effect may
	// cross a call boundary this way: every other kind indexes the
	// callee's own binding space.
	DefaultEffects map[int]kernelbridge.LoopEffect
	// ReturnsReceiver: the body ends `return this`. The value has no
	// scalar spelling (the ret rides unknown), and the CALLER gains an
	// alias to the receiver it may write through later — so the direct
	// apply route must FORGET the caller's knowledge of the receiver
	// (parity with the opaque path's ForgetThrough), and the statement
	// route must decline outright: a composed caller's later writes
	// through the alias would leave the receiver's slots stale.
	ReturnsReceiver bool
	// BundleEntries: one row per expanded bundle entry — this-fields
	// and record-parameter leaves — in slot order. Path is the slot
	// spelling ("this.container", "p.lo"), Index its slot index, and
	// Written whether the BODY writes that field (the census's Writes).
	//
	// The call sites read this to map written field exits back through
	// rets, the way a return value rides: an entry the body wrote holds a
	// different value at exit than the caller's own knowledge of the
	// field, and nothing else in the summary names WHICH slots those are.
	// A body with no expanded bundle carries no rows.
	BundleEntries []BundleEntry
	// RetShape / RetMembers: what the RETURNED VALUE is, where the body
	// returns a literal whose members ride their own slots
	// (returnedLiteralShape, ir_summary_body.go). RetShapeObject names one
	// row per key; RetShapeArray names the ".len"/".elem" pair.
	//
	// The apply route rebuilds the value from these exits instead of
	// reading the scalar #ret alone — a returned object's members are
	// otherwise lost at the boundary, since no scalar slot can spell an
	// object. RetShapeNone (the ordinary case) carries no rows and every
	// route reads #ret exactly as before.
	RetShape   RetShapeKind
	RetMembers []RetMemberEntry
}

// BundleEntry is one expanded bundle entry: where its slot sits and
// whether the body moves it. The layout fills these rows and the call
// sites read them — a second reading of the census at a call site would
// be a chance for the two to disagree about which slot is which, so the
// layout's own answer rides out beside the statements.
type BundleEntry struct {
	Path    string
	Index   int
	Written bool
}

// kernelSummariesMu guards kernelSummaries: the LOWERED body per
// declaration. Keyed by declaration alone, matching the registry
// above it: the lowering no longer takes the call's argument sorts,
// so one declaration has exactly one lowering. An entry with Ok
// false remembers a body that declined (the TS source's Map value of
// `null`, distinguished from "no entry yet" the same way
// class_field_invariants.go's invariantMemoSet distinguishes
// re-entry from "no answer computed").
type summaryEntry struct {
	Summary LoweredSummary
	Ok      bool
}

var (
	kernelSummariesMu sync.Mutex
	kernelSummaries   = map[*ast.Node]summaryEntry{}
)

// absentState is ABSENT in the TS source: the definitely-undefined
// entry state — no real value, absent.
var absentState = kernelbridge.KnownStateWire{
	Set:   refinementsets.MakeRefinedSet(refinementsets.OneOf(nil)),
	Undef: true,
	Null:  true,
}

// doneDownState is DONE_DOWN in the TS source: the done flag's entry
// state — exactly "not yet returned".
var doneDownState = kernelbridge.KnownStateWire{
	Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{0})),
}

// mayContainZero is mayContainZero in the TS source: whether a
// scalar set may admit 0 — the flag-still-down question. The forms
// list is an intersection, so EVERY form must admit 0; a shape this
// reader cannot judge answers true, which only wraps the result in a
// spurious maybe, never drops a real one.
func mayContainZero(set refinementsets.RefinedSet) bool {
	admits := func(f refinementsets.Refinement) bool {
		switch f.Form {
		case refinementsets.FormOneOf:
			for _, w := range f.W {
				if w == 0 {
					return true
				}
			}
			return false
		case refinementsets.FormAtLeast:
			return f.A <= 0
		case refinementsets.FormAbove:
			return f.A < 0
		case refinementsets.FormAtMost:
			return f.A >= 0
		case refinementsets.FormBelow:
			return f.A > 0
		case refinementsets.FormInteger, refinementsets.FormMultipleOf:
			return true
		case refinementsets.FormUnion:
			return mayContainZero(*f.A_) || mayContainZero(*f.B)
		case refinementsets.FormDifference:
			// a subtrahend that is EXACTLY a value list holding 0 removes
			// it for certain; any other subtrahend may or may not, so the
			// minuend answers
			removesZero := false
			if len(f.B.Forms) == 1 && f.B.Forms[0].Form == refinementsets.FormOneOf {
				for _, w := range f.B.Forms[0].W {
					if w == 0 {
						removesZero = true
						break
					}
				}
			}
			return !removesZero && mayContainZero(*f.A_)
		default:
			return true
		}
	}
	for _, f := range set.Forms {
		if !admits(f) {
			return false
		}
	}
	return true
}

// (typeofOfKnown lived here: the typeof evidence a call's ARGUMENT
// knowledge carried into the lowering. A summary quantifies over all
// entries, so no call's arguments may gate what it admits — the
// evidence now comes from the declaration's own annotations, through
// declaredParamTypeof below.)

// summaryLowerable gates the declarations a summary may lower at all.
//
// An ASYNC body lowers. The convention the lowering and this file
// share: a lowered async body's #ret slot holds the SETTLED INNER
// value, never the promise — `return e` writes e's own state, and an
// awaited call writes the callee's settled ret. The Promise wrapper is
// the ADAPTER's job, applied once at the boundary where the caller
// reads the call's value (AsCalleeResult for the inline route,
// applySummary's own wrap below for every summary route). Keeping the
// wrapper out of the slots is what lets one body's ret compose into
// another body's slot: an awaited call reads a settled value, which is
// exactly what the callee's ret already holds.
//
// A GENERATOR still declines, and the reason is the RESUMPTION
// PROTOCOL, not the shape of its result. Every `yield` in the body is a
// re-entry point: the body runs to that expression, hands its value
// out, stops, and resumes there later with a value the CALLER supplies
// to `next(v)` — so one call of the declaration is many entries and
// many exits, in an order no call site fixes. The statement grammar
// this lowering targets holds one entry and one exit per body, and a
// summary quantifies over entries; neither can carry a body whose
// control flow leaves and re-enters at every yield. That is outside the
// grammar rather than unbuilt in it, so this refusal stands where the
// others are provisional.
//
// What the call site does with the refusal is the part that matters: it
// does NOT decline the calling body. A generator call admits at the
// opaque tier — the call value enters from outside this walk's
// determination (evaluate_call_expression.go's GeneratorCallResult on
// the walk side, OpaqueCallHavoc's tier 3 on the lowering side) — and
// the caller keeps going. The values the generator hands over are read
// on top of that admission, from the body's own yields rather than from
// a summary of it (generator_element.go).
func summaryLowerable(declaration *ast.Node) bool {
	if declaration == nil || declaration.Body() == nil {
		return false
	}
	switch declaration.Kind {
	case ast.KindFunctionDeclaration:
		if declaration.AsFunctionDeclaration().AsteriskToken != nil {
			return false
		}
	case ast.KindFunctionExpression:
		if declaration.AsFunctionExpression().AsteriskToken != nil {
			return false
		}
	case ast.KindMethodDeclaration:
		if declaration.AsMethodDeclaration().AsteriskToken != nil {
			return false
		}
	}
	return true
}

// declaredParamSort reads a parameter's sort from the DECLARATION
// alone — its own type annotation, never a call's arguments. A
// summary quantifies over all entries, so the lowering that produces
// it must not read any one call's argument knowledge; an unannotated
// or richer-typed parameter is "unknown", which admits only the
// definedness test.
func declaredParamSort(parameter *ast.Node) BindingKind {
	typeNode := parameter.AsParameterDeclaration().Type
	if typeNode == nil {
		return BindingKindUnknown
	}
	switch typeNode.Kind {
	case ast.KindNumberKeyword, ast.KindBooleanKeyword:
		// booleans ride the number sort — their typeof differs, which
		// declaredParamTypeof answers separately
		return BindingKindNumber
	case ast.KindStringKeyword:
		return BindingKindString
	default:
		return BindingKindUnknown
	}
}

// declaredParamTypeof reads what `typeof` answers for a parameter's
// every defined value, from the declaration's own annotation.
func declaredParamTypeof(parameter *ast.Node) TypeofTag {
	typeNode := parameter.AsParameterDeclaration().Type
	if typeNode == nil {
		return TypeofTagNone
	}
	switch typeNode.Kind {
	case ast.KindNumberKeyword:
		return TypeofTagNumber
	case ast.KindStringKeyword:
		return TypeofTagString
	case ast.KindBooleanKeyword:
		return TypeofTagBoolean
	default:
		return TypeofTagNone
	}
}

// LowerSummaryBody lowers a declaration's body to the kernel's flow
// IR, remembering the answer — hit or decline — under the
// declaration. Standalone so the registry can compile a summary
// without going through a call site: the lowering reads only the
// declaration, so the same IR serves every entry.
//
// The lowering's own call sites may demand a callee's compiled blob
// (SummaryBlobFor), and that recursion is what puts the table in
// bottom-up order.
func LowerSummaryBody(ctx *FlowContext, declaration *ast.Node) (LoweredSummary, bool) {
	kernelSummariesMu.Lock()
	held, has := kernelSummaries[declaration]
	kernelSummariesMu.Unlock()
	if has {
		return held.Summary, held.Ok
	}
	summary, ok := lowerSummaryBody(ctx, declaration)
	kernelSummariesMu.Lock()
	kernelSummaries[declaration] = summaryEntry{Summary: summary, Ok: ok}
	kernelSummariesMu.Unlock()
	return summary, ok
}

// RelowerSummaryBody lowers a declaration's body WITHOUT reading or
// writing the memo — the same lowering LowerSummaryBody runs behind its
// memo. The fixpoint (summary_fixpoint.go) needs it: each round
// re-lowers the same declaration under a different self table entry, and
// the memo would hand back the first round's statements every time.
//
// The lowering itself is a pure function of the declaration and the
// blobs the registry answers for its callees, so re-running it is
// exactly as sound as running it once.
func RelowerSummaryBody(ctx *FlowContext, declaration *ast.Node) (LoweredSummary, bool) {
	return lowerSummaryBody(ctx, declaration)
}

// (The body lowering itself lives in ir_summary_body.go's
// lowerSummaryBody — the slot layout there flattens records by leaf
// path, arrays to len/elem pairs, and admits destructuring, and its
// lowering context carries the composed-call table builder. This file
// keeps the memo, the gate, and the apply route.)

// syntheticReturnStatement builds the single-statement body a
// concise arrow's expression reads as — the TS source's
// `ts.factory.createReturnStatement(body)`. Built as a bare Node
// with just enough shape for LowerStatements' ast.IsReturnStatement/
// AsReturnStatement().Expression reads (the lowering never asks for
// this synthetic node's position, parent, or any other field).
func syntheticReturnStatement(expression *ast.Node) *ast.Node {
	factory := ast.NewNodeFactory(ast.NodeFactoryHooks{})
	return factory.NewReturnStatement(expression)
}

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
	outcome, _, recorded := SummaryOutcomeOf(declaration)
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
	if outcome, _, recorded := SummaryOutcomeOf(declaration); !recorded || outcome != SummaryComplete {
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

// summaryEntryStates builds the entry states a call sends, one per
// ENTRY — which is no longer one per declared parameter: a type-literal
// parameter expands to one entry per member (SummaryParameterEntries,
// ir_summary_body.go), and its argKnown is one OBJECT abstract value
// that has to be read apart field by field.
//
// argKnowns is indexed by DECLARED PARAMETER; the state vector runs
// ahead of it wherever a parameter expanded. Everything else is the
// existing scalar rule verbatim: a missing argument enters absent, and a
// value the wire cannot spell declines THIS call (never the summary,
// which quantifies over every entry).
//
// An expanded member's fill reads the argument three ways:
//
//   - a POSITIVELY NON-OBJECT argument (a definite scalar — a "values"
//     kind holding a number, string, or boolean) DECLINES this call: the
//     caller's own value can never carry the member the layout expects,
//     so no entry state exists to send and the whole call is refused
//     rather than served on a fabricated TOP;
//   - a KNOWN OBJECT that does not name a declared member ALSO DECLINES:
//     the caller's own object told the checker its keys, and none of
//     them is the one the layout expects, so again there is no state to
//     send;
//   - anything else — an argument whose kind is unknown or opaque —
//     fills that one entry TOP, thisEntryState's rule below, which the
//     class bundle above already takes through BundleParamEntryStates.
//     TOP, and never absent: an argument the caller knows nothing about
//     made no claim that the member is undefined, and TOP is what the
//     entry quantifier already covers, so filling it costs precision and
//     never soundness.
//
// The SLOT VECTOR's shape is unaffected by any of the three: one entry
// per declared member is what the layout agreement requires, and only a
// DECLINE (never a fill) changes how many entries a call sends.
//
// Past the declared parameters the vector holds a METHOD's this-entries,
// which the RECEIVER fills field by field (thisEntryStates below), or an
// arrow route's captures, which this route never has; anything still
// short of ParamCount fills absent.
func summaryEntryStates(
	ctx *FlowContext,
	declaration *ast.Node,
	summary LoweredSummary,
	argKnowns []abstractdomain.AbstractValue,
	receiver abstractdomain.AbstractValue,
) ([]kernelbridge.KnownStateWire, bool) {
	states := make([]kernelbridge.KnownStateWire, 0, summary.SlotCount)
	for index, parameter := range declaration.Parameters() {
		if len(states) >= summary.ParamCount {
			break
		}
		// a CLASS-TYPED parameter's bundle rows fill from that
		// parameter's own argument, field by field, in the layout's own
		// order — the scalar rule would push one whole-name state at the
		// wrong index. A non-object or missing argument tops every row
		// (BundleParamEntryStates' rule): a class instance the caller
		// knows nothing about is the routine case, and the bundle exists
		// so the body can read fields the caller never had.
		if _, census, _, isBundle := BundleParamCensus(ctx, declaration.Body(), parameter); isBundle && census.Believable() && len(census.Reads) > 0 {
			var argument abstractdomain.AbstractValue
			if index < len(argKnowns) {
				argument = argKnowns[index]
			}
			row, filled := BundleParamEntryStates(argument, census.Reads)
			if !filled {
				return nil, false
			}
			states = append(states, row...)
			continue
		}
		// a REST parameter's entry is TOP whatever the call passed: the
		// bound array is always defined and its contents unspellable —
		// never absent, which would claim an array that always exists is
		// undefined
		if parameter.AsParameterDeclaration().DotDotDotToken != nil {
			states = append(states, kernelbridge.KnownStateWire{Top: true})
			continue
		}
		// an ARRAY-TYPED parameter carries the entries the layout emitted:
		// a length plus either one scalar elem entry, or — where the
		// element itself expands as a record (ElementMembers, step 1/2 of
		// the records-as-array-elements build) — one "xs.elem.<member>"
		// entry per member, in place of the single scalar one. Every entry
		// enters TOP: the direct apply reads an argument's abstract value,
		// which carries no length, no element join, and no per-member
		// join this route can spell, and TOP is what the entry quantifier
		// already covers. Never absent, which would claim an array the
		// caller passed is undefined; never a narrower count, which would
		// slide every later parameter's entries by the difference —
		// reading the SAME local arrayParamSlotsIn's memo already resolved
		// (rather than counting members again here) is what keeps this
		// seam and the layout seam pushing entries of one agreed width.
		if local, flattened := arrayParamSlotsIn(ctx, parameter); flattened {
			count := 1 + max(1, len(local.ElementMembers))
			for i := 0; i < count; i++ {
				states = append(states, kernelbridge.KnownStateWire{Top: true})
			}
			continue
		}
		// a BINDING-PATTERN parameter: one state per bound entry, each
		// read from the argument object's member by the entry's Key —
		// thisEntryState's TOP fallbacks for a non-object argument or an
		// unnamed member, exactly the bundle rule
		if pd := parameter.AsParameterDeclaration(); pd.Name() != nil && ast.IsObjectBindingPattern(pd.Name()) {
			entries, entriesOk := SummaryParameterEntriesIn(ctx, parameter)
			if !entriesOk {
				return nil, false
			}
			var argument abstractdomain.AbstractValue
			if index < len(argKnowns) {
				argument = argKnowns[index]
			}
			for _, entry := range entries {
				// a TOP-entry row (a defaulted or rest pattern element) never
				// fills from the argument's member: the bound value is
				// member-or-default (or a fresh rest object), and a definite
				// member state would claim what the runtime did not bind
				// (bodySlot.TopEntry's doc)
				if entry.TopEntry {
					states = append(states, kernelbridge.KnownStateWire{Top: true})
					continue
				}
				states = append(states, thisEntryState(argument, entry.Key))
			}
			continue
		}
		members, expanded := recordParamMembersIn(ctx, parameter)
		if !expanded {
			if index >= len(argKnowns) {
				// a DEFINITELY-MISSING argument on a DEFAULTED parameter
				// enters holding the default: the body's definedness branch
				// then joins two identical values and the answer stays
				// exact. Only a CONST or CONSTSTATE default can cross —
				// anything else indexes the callee's binding space.
				if effect, defaulted := summary.DefaultEffects[len(states)]; defaulted {
					if state, constant := constEffectState(effect); constant {
						states = append(states, state)
						continue
					}
				}
				states = append(states, absentState)
				continue
			}
			wire, ok := StateOfKnown(argKnowns[index])
			if !ok {
				return nil, false
			}
			states = append(states, wire)
			continue
		}
		if index >= len(argKnowns) {
			for range members {
				states = append(states, absentState)
			}
			continue
		}
		// each member entry reads the argument's own field, one PATH STEP
		// at a time for a nested member — expandedMemberEntryState's rule,
		// applied at every step: DECLINES this call on a positively
		// non-object argument or a known object missing the step, TOPS
		// only where the step's own kind is unknown or opaque.
		argument := argKnowns[index]
		for _, member := range members {
			state, filled := expandedMemberEntryState(argument, member.Path)
			if !filled {
				return nil, false
			}
			states = append(states, state)
		}
	}
	// the METHOD's this-entries, filled from the receiver's own field
	// knowledge — the same objectKeyIndex/StateOfKnown reading the
	// record-parameter apply above takes
	for _, entry := range summary.BundleEntries {
		if entry.Index < len(states) || entry.Index >= summary.ParamCount {
			continue
		}
		field, isThis := thisFieldNameOf(entry.Path)
		if !isThis {
			continue
		}
		for len(states) < entry.Index {
			states = append(states, absentState)
		}
		states = append(states, thisEntryState(receiver, field))
	}
	for len(states) < summary.ParamCount {
		states = append(states, absentState)
	}
	return states, true
}

// thisFieldNameOf is the field behind a this-entry's slot spelling:
// "this.container" is container. Anything not spelled under `this` is
// some other bundle's row (a record parameter's leaf) and answers false.
func thisFieldNameOf(path string) (string, bool) {
	const under = "this."
	if len(path) <= len(under) || path[:len(under)] != under {
		return "", false
	}
	return path[len(under):], true
}

// thisEntryState is what ONE this-field entry enters holding, read from
// the call's receiver.
//
// A receiver that is an OBJECT carrying knowledge of the field enters
// with that field's own state. Everything else enters TOP — a receiver
// that is not an object, one whose knowledge does not name this field,
// and a field whose value the wire cannot spell.
//
// TOP, and never ABSENT: an unknown field is UNKNOWN, while absent would
// claim the field is undefined — a claim no receiver made, and one the
// body's reads would then narrow on. TOP is what the entry quantifier
// already covers, so filling it costs precision and never soundness.
func thisEntryState(receiver abstractdomain.AbstractValue, field string) kernelbridge.KnownStateWire {
	if receiver.Kind != abstractdomain.KindObject {
		return kernelbridge.KnownStateWire{Top: true}
	}
	at, has := objectKeyIndex(receiver, field)
	if !has {
		return kernelbridge.KnownStateWire{Top: true}
	}
	wire, ok := StateOfKnown(receiver.Keys[at].Value)
	if !ok {
		return kernelbridge.KnownStateWire{Top: true}
	}
	return wire
}

// isDefiniteScalarArgument answers whether an argument is a definite
// SCALAR — a "values" kind holding a number, string, or boolean — the
// one shape an expanded record parameter's member can never read a
// value from, whatever the member's name.
func isDefiniteScalarArgument(argument abstractdomain.AbstractValue) bool {
	if argument.Kind != abstractdomain.KindValues {
		return false
	}
	switch argument.KindTag {
	case abstractdomain.PrimitiveNumber, abstractdomain.PrimitiveString, abstractdomain.PrimitiveBoolean:
		return true
	}
	return false
}

// expandedMemberEntryState is what ONE expanded record parameter's
// member entry enters holding, read from the call's own argument,
// walking the member's key PATH one step per nesting level — ["lo"] for
// a flat member, ["inner","deep"] for a member nestedMemberLeavesOf
// recursed into.
//
// THE THREE-WAY RULE APPLIES AT EVERY STEP, interior or leaf. A
// POSITIVELY NON-OBJECT value at that step (isDefiniteScalarArgument)
// DECLINES THIS CALL: it can never carry the next segment, so there is
// no state to send. A KNOWN OBJECT not naming the next segment also
// DECLINES: the caller told the checker its own keys, and the segment
// is not among them. Everything else — the step's own kind is unknown
// or opaque — TOPS, thisEntryState's rule, because nothing here rules
// the rest of the path either present or absent. An INTERIOR step that
// TOPs stops the walk there: unknown of the parent means unknown of
// every child, so the leaf entry also fills TOP rather than reading
// further into a value nothing is known about.
//
// A single-segment path — every member before nested families existed,
// and every flat member after — reads exactly ONE step and answers
// thisEntryState's own reading, so nothing already landed changes
// behavior.
func expandedMemberEntryState(argument abstractdomain.AbstractValue, path []string) (kernelbridge.KnownStateWire, bool) {
	if len(path) == 0 {
		return kernelbridge.KnownStateWire{}, false
	}
	current := argument
	for index, step := range path {
		leaf := index == len(path)-1
		if isDefiniteScalarArgument(current) {
			return kernelbridge.KnownStateWire{}, false
		}
		if current.Kind != abstractdomain.KindObject {
			// unknown or opaque at this step: TOP covers the rest of the
			// path, leaf or interior alike — nothing here rules further
			return kernelbridge.KnownStateWire{Top: true}, true
		}
		keyAt, has := objectKeyIndex(current, step)
		if !has {
			return kernelbridge.KnownStateWire{}, false
		}
		if leaf {
			return thisEntryState(current, step), true
		}
		current = current.Keys[keyAt].Value
	}
	return kernelbridge.KnownStateWire{}, false
}

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
