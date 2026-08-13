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
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
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

// summaryLowerable gates the declarations a summary may lower at all:
// an async body's value is a Promise and a generator's an iterator —
// walking either as its raw return set would mistype the call. (The
// TS source reaches lowering only behind the effect-free scan and
// carries no such gate; an awaitless async pure body could slip
// through there — noted for the correspondence.)
func summaryLowerable(declaration *ast.Node) bool {
	if declaration == nil || declaration.Body() == nil {
		return false
	}
	if ast.HasSyntacticModifier(declaration, ast.ModifierFlagsAsync) {
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

// SummaryResult is the summary route as the older call sites spell
// it — a program handle and a callee resolver, with no walk context.
// The route now runs off the DECLARATION alone and resolves callees
// through the walk's own contract registry, so a caller that hands
// over only a program handle gets a context with no registry: its
// bodies lower, and any call inside them declines. The one such
// caller is function_summaries.go's RecoverPure (another agent's
// file); moving it to SummaryResultIn is a report item.
func SummaryResult(
	p *program.CheckerProgram,
	declaration *ast.Node,
	argKnowns []abstractdomain.AbstractValue,
	resolveCallee func(callee *ast.Node) *ast.Node,
) (abstractdomain.AbstractValue, bool) {
	return applySummary(&FlowContext{P: p}, declaration, argKnowns)
}

// SummaryResultIn is the same route with the walk's own context — the
// spelling a caller that HAS a FlowContext should use, so composed
// calls resolve through its contract registry.
func SummaryResultIn(ctx *FlowContext, declaration *ast.Node, argKnowns []abstractdomain.AbstractValue) (abstractdomain.AbstractValue, bool) {
	return applySummary(ctx, declaration, argKnowns)
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
	states := make([]kernelbridge.KnownStateWire, 0, summary.SlotCount)
	for i := 0; i < summary.ParamCount; i++ {
		if i >= len(argKnowns) {
			states = append(states, absentState)
			continue
		}
		wire, ok := StateOfKnown(argKnowns[i])
		if !ok {
			return abstractdomain.AbstractValue{}, false
		}
		states = append(states, wire)
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
	// a TOP result determines nothing the inline walk could not say
	// better — decline rather than answer weaker than the fallback
	if retExit.Top {
		return abstractdomain.AbstractValue{}, false
	}
	// the result slot's own absent flag is the ENTRY state surviving
	// the joins — path correlation the encoding routes through the
	// done flag instead: every RETURNED value was written into the
	// set, and only a fall-off path leaves undefined, which is exactly
	// the flag-still-down case decided below
	answer := KnownOfState(kernelbridge.KnownStateWire{Set: retExit.Set, Absent: false, Nan: retExit.Nan})
	if answer.Kind == abstractdomain.KindUnknown {
		return abstractdomain.AbstractValue{}, false
	}
	// a path may fall off the end (the flag can still be down at exit):
	// the return is undefined on it
	allReturned := !doneExit.Top && !doneExit.Absent && !mayContainZero(doneExit.Set)
	if !allReturned {
		answer = abstractdomain.PossiblyUndefined(answer, "", false, false)
	}
	// the claim's grade floors at the arguments' own standing
	floor := abstractdomain.TrustProved
	for i := 0; i < summary.ParamCount; i++ {
		if i < len(argKnowns) {
			floor = abstractdomain.MinTrustLevel(floor, abstractdomain.TrustLevelOf(argKnowns[i]))
		}
	}
	tracing.Count("summaryServed", 0)
	return abstractdomain.AtTrustLevel(answer, floor), true
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
func KernelSummaryDirect(ctx *FlowContext, argKnowns []abstractdomain.AbstractValue, contract *FunctionContract) (abstractdomain.AbstractValue, bool) {
	if !summaryLowerable(contract.Declaration) {
		return abstractdomain.AbstractValue{}, false
	}
	return applySummary(ctx, contract.Declaration, argKnowns)
}
