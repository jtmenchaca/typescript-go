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
	Set:    refinementsets.MakeRefinedSet(refinementsets.OneOf(nil)),
	Absent: true,
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
// A GENERATOR still declines: its call's value is an iterator, and no
// slot in this grammar spells one, so there is no inner value for a
// boundary wrapper to adopt.
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
	return applySummary(&FlowContext{P: p}, declaration, argKnowns, unknownReceiver())
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
	return applySummary(ctx, declaration, argKnowns, unknownReceiver())
}

// SummaryResultOn is SummaryResultIn with the call's RECEIVER supplied —
// what a method's this-entries are filled from.
func SummaryResultOn(
	ctx *FlowContext,
	declaration *ast.Node,
	argKnowns []abstractdomain.AbstractValue,
	receiver abstractdomain.AbstractValue,
) (abstractdomain.AbstractValue, bool) {
	return applySummary(ctx, declaration, argKnowns, receiver)
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
func applySummary(
	ctx *FlowContext,
	declaration *ast.Node,
	argKnowns []abstractdomain.AbstractValue,
	receiver abstractdomain.AbstractValue,
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
	// THE SERVING RULE: only a COMPLETE body serves, and it serves
	// unconditionally — TOP ret included. The compile is proved equal
	// to the kernel walk (summarize_eq), and the lowering that produced
	// it read every statement, so the answer carries the same knowledge
	// the inline walk would derive. A POROUS or unrecorded outcome
	// DECLINES OUTRIGHT, even with a concrete ret: a havocked construct
	// is precisely a place the walk still reads and the lowering does
	// not, so a porous answer can be weaker than the inline walk's —
	// and serving those was measured (recharts, 2026-08-13) to widen
	// the downstream sets until the call-site join machinery burned 14x
	// the file's whole former wall. Porous blobs still count coverage;
	// they no longer answer calls.
	outcome, _, recorded := SummaryOutcomeOf(declaration)
	if !recorded || outcome != SummaryComplete {
		return abstractdomain.AbstractValue{}, false
	}
	serveTop := retExit.Top
	// the result slot's own absent flag is the ENTRY state surviving
	// the joins — path correlation the encoding routes through the
	// done flag instead: every RETURNED value was written into the
	// set, and only a fall-off path leaves undefined, which is exactly
	// the flag-still-down case decided below
	answer := KnownOfState(kernelbridge.KnownStateWire{Set: retExit.Set, Absent: false, Nan: retExit.Nan})
	if answer.Kind == abstractdomain.KindUnknown {
		// a COMPLETE body serving a TOP ret answers SILENCE, which is what
		// "the return value is unconstrained" spells — and the route still
		// says it SERVED, so the caller keeps this answer instead of
		// re-walking the body to derive the same nothing. Every other
		// unknown answer declines, exactly as before.
		if !serveTop {
			return abstractdomain.AbstractValue{}, false
		}
		tracing.Count("summaryServed", 0)
		return promiseWrappedIfAsync(declaration, silence.Residue()), true
	}
	// a path may fall off the end (the flag can still be down at exit):
	// the return is undefined on it
	allReturned := !doneExit.Top && !doneExit.Absent && !mayContainZero(doneExit.Set)
	if !allReturned {
		answer = abstractdomain.PossiblyUndefined(answer, "", false, false)
	}
	// the claim's grade floors at the standing of everything the entries
	// were built from — the arguments, and (below) a method's receiver.
	// The argument walk is indexed by DECLARED PARAMETER, not by entry —
	// and an EXPANDED parameter's entries were built from the object's
	// FIELDS, so each read field's own grade joins the floor beside the
	// object's: an object at proved standing whose lo came from a spec row
	// states no more than spec.
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
	tracing.Count("summaryServed", 0)
	return promiseWrappedIfAsync(declaration, abstractdomain.AtTrustLevel(answer, floor)), true
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

// constEffectState reads a CONST or CONSTSTATE effect as the entry
// state it spells — the only two effect kinds whose meaning does not
// depend on any binding space, which is what lets a callee's lowered
// default cross to a caller's entry vector.
func constEffectState(effect kernelbridge.LoopEffect) (kernelbridge.KnownStateWire, bool) {
	switch effect.Kind {
	case kernelbridge.LoopEffectConst:
		return kernelbridge.KnownStateWire{Set: effect.Set}, true
	case kernelbridge.LoopEffectConstState:
		return kernelbridge.KnownStateWire{Set: effect.Set, Absent: effect.Absent, Nan: effect.Nan}, true
	}
	return kernelbridge.KnownStateWire{}, false
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
// A NON-OBJECT argument for an expanded parameter declines the call —
// the entries were laid out expecting the members, and no scalar spells
// them. So does an object missing a declared member, or one whose field
// is itself unspellable.
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
		argument := argKnowns[index]
		if argument.Kind != abstractdomain.KindObject {
			return nil, false
		}
		for _, member := range members {
			at, has := objectKeyIndex(argument, member.Key)
			if !has {
				return nil, false
			}
			wire, ok := StateOfKnown(argument.Keys[at].Value)
			if !ok {
				return nil, false
			}
			states = append(states, wire)
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
	return applySummary(ctx, contract.Declaration, argKnowns, receiver)
}
