// from control_flow/ir_lowering_context.ts
//
// The slot vector and name map a lowering walk carries: which
// bindings are tracked, under which sort and typeof evidence, and
// how an inlined body resolves names without capturing the caller.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// LoweringResult is the (done, ret) slot pair for FUNCTION BODIES:
// `return e` writes the result slot and raises the done flag;
// statements after a returning branch run only under the flag's
// falsity. Both slots ride the ordinary proved grammar — no new
// kernel statement exists, so walk_sound covers returning bodies
// unchanged.
type LoweringResult struct {
	Done int
	Ret  int
}

// LoweringContext mirrors the TS LoweringContext interface.
type LoweringContext struct {
	// Bindings: tracked binding names, in walk order.
	Bindings []string
	// Sorts: per binding, the sort its occurrences wear.
	Sorts []BindingKind
	// Typeofs: per binding, what typeof answers for its defined
	// values. Nil means the TS `typeofs?` was absent.
	Typeofs []TypeofTag
	// Narrow: the kernel's narrowing question, for loop heads.
	Narrow func(tree kernelbridge.NarrowTree) kernelbridge.NarrowAnswer
	// Result: nil means the TS `result?` was absent (not a function
	// body lowering).
	Result *LoweringResult
	// Names: CLOSED name resolution for an inlined callee body: a
	// name not in the map is untracked, never the enclosing caller's
	// slot — an inlined body reading a free name must decline, not
	// capture. Nil means the TS `names?` was absent (top-level
	// lowering, resolve through Bindings instead).
	Names map[string]int
	// ResolveCallee: the declaration behind a called name, for
	// composition — a call to a resolvable pure callee lowers as its
	// body inlined into fresh slots. Nil: calls decline as before.
	ResolveCallee func(callee *ast.Node) *ast.Node
	// Allocate: grow the slot vector (composition needs fresh
	// slots); returns (0, false) past the owner's budget. Nil means
	// the TS `allocate?` was absent.
	Allocate func(name string, sort BindingKind, typeofTag TypeofTag) (int, bool)
	// Inlining: declarations currently being lowered — the
	// composition cycle guard. Nil means the TS `inlining?` was
	// absent.
	Inlining map[*ast.Node]struct{}
	// BoundedIndices: the index/array pairs a dominating guard proved
	// in range ("i<a" for `i < a.length`). An index read inside such a
	// guard's arm answers the element slot outright; outside it, the
	// read carries the or-absent wrapping. HoldBoundIndex swaps a fresh
	// set in for the arm and swaps the old one back after, so a bound
	// never escapes the arm that proved it. Nil is "nothing proved".
	BoundedIndices map[string]struct{}
	// Flow: the check's flow context, which the summary registry needs
	// to build a callee's blob. Nil: call statements decline (no
	// registry to ask), and the lowering keeps the inlining route.
	Flow *FlowContext
	// SummaryTable: the composed-call table this lowering is building —
	// one blob per callee, in the order IrStatementCall's Callee field
	// indexes. Shared through the pointer so an inlined or nested
	// lowering appends to the SAME table the whole body rides with.
	// Nil: call statements decline (no table to grow).
	SummaryTable *SummaryTableBuilder
	// CaptureHavocSlots: the this-bundle slots a method-calling CAPTURE
	// can move (the captured methods' transitive write set, resolved to
	// slot indices). Non-empty puts the statement walk in havoc mode:
	// every statement that runs code is bracketed by unknown-assigns of
	// these slots, because the stored closure may run inside any callee.
	// Empty (the routine case) changes nothing.
	CaptureHavocSlots []int
	// FirstHavoc: the spelling of the FIRST construct this lowering
	// havocked, or "" where nothing did. Set once — the first havoc
	// route to reach it wins, and every later one leaves it alone — so
	// the name always points at the earliest place the body stopped
	// being read whole.
	//
	// This field is only FILLED here. The body-level owner reads it
	// when the lowering finishes and reports the body's outcome from it
	// (empty is `complete`, non-empty is `porous` naming this
	// construct); no route in the lowering reads it back, and no route
	// in the lowering records an outcome of its own.
	FirstHavoc string
	// CanHoist: does a STATEMENT STREAM exist to hoist a call
	// subexpression into? A call inside an expression lowers as a
	// temp-slot call statement emitted BEFORE the statement that
	// contained it (ir_call_hoist.go) — which is only possible where
	// something is collecting statements.
	//
	// False everywhere else, and the two "everywhere else" are real: the
	// loop solver's body folding (FoldBody, ir_loop.go) reads the same
	// effect grammar into an effect-per-binding vector with no statement
	// stream at all, and the loop-effect lowering (loop_effect.go) never
	// builds a LoweringContext in the first place. A hoist in either
	// place would append a statement nothing ever emits — the call would
	// silently vanish and the temp would read its entry state, which is
	// a WRONG answer, not a weak one. So the flag is set true by
	// LowerStatements alone, and cleared for the duration of any reading
	// that has no statement position.
	CanHoist bool
	// HoistStatement: the statement whose expressions are being read right
	// now — what the ordering gate measures a candidate hoist against
	// (hoistingIsOrderSafe). Set by LowerStatements alongside CanHoist;
	// nil refuses every hoist, since a reordering cannot be proved safe
	// against a statement nobody named.
	HoistStatement *ast.Node
	// Hoisted: the call statements accumulated for the statement being
	// read, in the order the expression readers reached them — which is
	// left to right, so several calls in one expression run among
	// themselves exactly as JavaScript runs them. The statement route
	// flushes these ahead of its own statements (TakeHoisted) or
	// truncates them away when its reading declined (DropHoistedFrom).
	Hoisted []kernelbridge.IrStatement
	// HoistedTemp: the temp slot each call NODE already hoisted to, for
	// the statement being read. One call site runs ONCE in the real run,
	// so it lowers to one call statement however many times a reader
	// reaches it — and readers do reach the same node repeatedly:
	// SortOfArg (ir_guard.go) probes an argument through EffectOf and
	// SequenceEffectOf purely to learn its sort, discarding the effect it
	// built, and the statement routes try one reading after another over
	// the same expression. Without this map each probe would allocate a
	// fresh temp and append a duplicate call statement, so a call inside
	// an argument would run two or three times kernel-side.
	//
	// Keyed by the call NODE, so two textually identical calls
	// (`g(y) + g(y)`) are two different nodes and correctly hoist twice.
	// Cleared with the accumulation by whoever owns the statement.
	HoistedTemp map[*ast.Node]int
	// RetShape / RetMembers: the RETURNED VALUE'S own member slots, where
	// the layout allocated any (returnedLiteralShape, ir_summary_body.go).
	// A body whose every return carries an object literal takes one slot
	// per member beside #ret; one whose every return carries an array
	// literal takes the ".len"/".elem" pair.
	//
	// The return arm reads these to write each member's effect into its
	// own slot. RetShapeNone — the ordinary case — leaves every route
	// exactly as it was: the scalar #ret alone carries the value.
	RetShape   RetShapeKind
	RetMembers []RetMemberEntry
}

// RetMemberSlotOf resolves ONE member of the returned value's shape to
// its slot index, through the layout's own row list — the same list the
// call sites read, so the writer and the readers never disagree about
// which slot holds which member.
func RetMemberSlotOf(context *LoweringContext, member string) (int, bool) {
	if context == nil {
		return 0, false
	}
	for _, entry := range context.RetMembers {
		if entry.Name == member {
			return entry.Index, true
		}
	}
	return 0, false
}

// NoteFirstHavoc records a havocked construct's spelling, first-wins:
// the earliest havoc in the lowering names the body's outcome, and every
// later one leaves the name where it stands.
//
// Called by every havoc route — the opaque statement floor, the opaque
// call, and the recursion cycle floor — so a body that lowered with any
// havoc at all carries a name for it.
func NoteFirstHavoc(context *LoweringContext, construct string) {
	if context == nil || construct == "" || context.FirstHavoc != "" {
		return
	}
	context.FirstHavoc = construct
}

// SummaryTableBuilder grows the summary table a lowered body carries:
// one blob per callee, appended on first use, with the declaration's
// index remembered so a second call to the same callee reuses it.
type SummaryTableBuilder struct {
	Blobs []kernelbridge.SummaryBlob
	Index map[*ast.Node]int
}

// CalleeIndex is the table index of a callee's blob, appending it on
// first use. The caller has already obtained the blob from the
// registry; this only assigns it a slot in THIS body's table.
func (builder *SummaryTableBuilder) CalleeIndex(declaration *ast.Node, blob kernelbridge.SummaryBlob) int {
	if builder.Index == nil {
		builder.Index = map[*ast.Node]int{}
	}
	if held, has := builder.Index[declaration]; has {
		return held
	}
	builder.Blobs = append(builder.Blobs, blob)
	index := len(builder.Blobs) - 1
	builder.Index[declaration] = index
	return index
}

// IndexOf is indexOf in the TS source: the tracked index of an
// identifier or a property path (`o.k`, and — since nested records
// flatten by leaf path — `o.a.b`), matched by spelled name. An inlined
// body resolves ONLY through its own name map.
//
// SpelledNameOf spells one step, so a DEEP path takes the path
// resolver; a one-step path answers the same slot either way.
func IndexOf(context *LoweringContext, name *ast.Node) (int, bool) {
	if spelled, ok := SpelledNameOf(name); ok {
		if index, found := slotIndexOfName(context, spelled); found {
			return index, true
		}
	}
	// `a.length` on a flattened array is the len slot, spelled "a.len"
	if index, ok := ArrayLengthSlotOf(context, name); ok {
		return index, true
	}
	// `m.size` on a flattened Map or Set is the size slot, spelled
	// "m.size". SpelledNameOf already spells that one step, so the branch
	// above answers it in the ordinary case; this is the resolver the
	// flattening OWNS, so a caller that reaches IndexOf with a size read
	// lands on the same slot whichever route it took — the same shape
	// ArrayLengthSlotOf has, and what keeps guards and loop heads reading
	// the collection's count with no special case of their own.
	if index, ok := MapSizeSlotOf(context, name); ok {
		return index, true
	}
	return PathSlotIndexOf(context, name)
}

// NumberIndexOf is numberIndexOf in the TS source: a tracked
// NUMBER-sorted read, or (0, false) — arithmetic admits only the
// number sort.
func NumberIndexOf(context *LoweringContext, name *ast.Node) (int, bool) {
	i, ok := IndexOf(context, name)
	if !ok {
		return 0, false
	}
	if context.Sorts[i] == BindingKindNumber {
		return i, true
	}
	return 0, false
}

// StatementsOf is statementsOf in the TS source: a statement or
// block as a flat statement list.
func StatementsOf(s *ast.Node) []*ast.Node {
	if s == nil {
		return nil
	}
	if ast.IsBlock(s) {
		return append([]*ast.Node{}, s.AsBlock().Statements.Nodes...)
	}
	return []*ast.Node{s}
}
