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
//
// This file holds the summary types, the per-declaration memo, the
// shared entry-state constants, and the gate that decides which
// declarations may lower at all. kernel_summaries_apply.go reads the
// memo to serve a call; kernel_summaries_entry_states.go builds what a
// call sends into it.

package walk

import (
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
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
// declaration. Keyed by (checker, declaration), matching the registry
// above it: the lowering reads the declaration's own parameter type
// annotations THROUGH a checker, and checkers are worker-exclusive
// lazy resolvers, so a bare-node key would let whichever worker's
// checker lowers first win the entry for every later reader on any
// checker (this package's summary_registry.go carries the same key
// shape, for the same reason). An entry with Ok false remembers a
// body that declined (the TS source's Map value of `null`,
// distinguished from "no entry yet" the same way
// class_field_invariants.go's invariantMemoSet distinguishes
// re-entry from "no answer computed").
type summaryEntry struct {
	Summary LoweredSummary
	Ok      bool
}

var (
	kernelSummariesMu sync.Mutex
	kernelSummaries   = map[summaryKey]summaryEntry{}
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
	key := summaryKey{checker: checkerOf(ctx), declaration: declaration}
	kernelSummariesMu.Lock()
	held, has := kernelSummaries[key]
	kernelSummariesMu.Unlock()
	if has {
		return held.Summary, held.Ok
	}
	summary, ok := lowerSummaryBody(ctx, declaration)
	kernelSummariesMu.Lock()
	kernelSummaries[key] = summaryEntry{Summary: summary, Ok: ok}
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
