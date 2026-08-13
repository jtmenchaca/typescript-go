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
