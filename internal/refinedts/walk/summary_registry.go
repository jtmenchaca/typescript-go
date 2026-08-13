// The context-free summary store.
//
// A summary is a slot-program the kernel compiles from a body's IR
// ONCE. It quantifies over all entries, so it is context-free and
// shared across every check with no per-check scoping and no
// fingerprints: the store keys a declaration's blob by the
// DECLARATION ALONE, under one mutex.
//
// The build is bottom-up by recursion: lowering a body whose call
// sites lower as IrStatementCall demands each callee's blob through
// SummaryBlobFor, and that demand IS the order — a callee's blob is
// finished before the caller's compile sees it in the table.

package walk

import (
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// summaryBlobEntry is one declaration's answer. Ok false is a
// REMEMBERED decline: a declaration whose body failed to lower, or
// whose compile the kernel refused, answers false forever rather
// than paying the lowering again at every call.
type summaryBlobEntry struct {
	Blob kernelbridge.SummaryBlob
	// OutShape is where the compiled summary's out-vector puts the
	// return value — lowerSummary's own result slot index, carried
	// beside the blob so the lowering agent's call sites can name it
	// without re-deriving the slot layout.
	OutShape int
	Ok       bool
}

// summaryBlobsMu guards BOTH maps below. summaryBlobs holds the
// finished answers; summaryBuilding holds the declarations whose
// build is in flight — the cycle guard.
//
// Keyed by declaration ALONE — a summary quantifies over all entries,
// so it is context-free and shared across every check with no
// per-check scoping and no fingerprints.
var (
	summaryBlobsMu  sync.Mutex
	summaryBlobs    = map[*ast.Node]summaryBlobEntry{}
	summaryBuilding = map[*ast.Node]struct{}{}
)

// summaryBuilder is the seam the registry compiles through: lower the
// declaration's body to IR (which may recursively demand callee
// blobs), then ask the kernel to compile it. A test replaces this to
// count builds without instantiating the kernel; nil means the
// production builder (assigned lazily — a package-level assignment
// would be an initialization cycle, since the builder's lowering
// recursively reaches SummaryBlobFor).
var summaryBuilder func(ctx *FlowContext, declaration *ast.Node) (kernelbridge.SummaryBlob, int, bool)

// SummaryBlobFor answers the declaration's compiled summary, building
// it on the first ask and storing the answer — hit or decline — under
// the declaration.
//
// A body that declines to lower is remembered as a decline: the same
// declaration answers false forever, so a call site pays the lowering
// attempt once.
//
// A re-entry while this declaration's own build is in flight is a
// CYCLE — a recursive function. It answers false WITHOUT storing a
// decline, so the outer build's real answer is what lands in the
// store. Recursive functions keep the Go inline route for now: the
// summary compiler has no fixpoint over its own table, so a recursive
// callee has no blob to splice.
func SummaryBlobFor(ctx *FlowContext, declaration *ast.Node) (kernelbridge.SummaryBlob, bool) {
	if declaration == nil {
		return "", false
	}
	summaryBlobsMu.Lock()
	if held, has := summaryBlobs[declaration]; has {
		summaryBlobsMu.Unlock()
		return held.Blob, held.Ok
	}
	if _, cycling := summaryBuilding[declaration]; cycling {
		summaryBlobsMu.Unlock()
		return "", false
	}
	summaryBuilding[declaration] = struct{}{}
	summaryBlobsMu.Unlock()

	// The build runs OUTSIDE the mutex: lowering a body recursively
	// asks SummaryBlobFor for its callees, which takes the same lock.
	builder := summaryBuilder
	if builder == nil {
		builder = buildSummaryBlob
	}
	blob, outShape, ok := builder(ctx, declaration)

	summaryBlobsMu.Lock()
	delete(summaryBuilding, declaration)
	summaryBlobs[declaration] = summaryBlobEntry{Blob: blob, OutShape: outShape, Ok: ok}
	summaryBlobsMu.Unlock()
	return blob, ok
}

// SummaryOutShapeFor answers which index of a compiled summary's
// out-vector carries the RETURN value — the out-vector convention a
// call site reads its result from.
//
// It is lowerSummary's own result slot index. The lowering places the
// done flag and the result slot as the last two of its base binding
// vector, and the compiler's out vector is the whole binding row (the
// design's `summarize` ends `out := cur`, one entry per binding), so
// binding index and out index are the same number. A call site
// writing the return into a caller slot names THIS index in the
// statement's rets.
//
// Building the summary if it is not yet built, so a call site can ask
// for the shape and the blob in either order.
func SummaryOutShapeFor(ctx *FlowContext, declaration *ast.Node) (int, bool) {
	if declaration == nil {
		return 0, false
	}
	summaryBlobsMu.Lock()
	held, has := summaryBlobs[declaration]
	summaryBlobsMu.Unlock()
	if has {
		return held.OutShape, held.Ok
	}
	if _, ok := SummaryBlobFor(ctx, declaration); !ok {
		return 0, false
	}
	summaryBlobsMu.Lock()
	held = summaryBlobs[declaration]
	summaryBlobsMu.Unlock()
	return held.OutShape, held.Ok
}

// buildSummaryBlob is the production builder: run the declaration
// through the same gate and lowering the whole-body route uses, then
// hand the body's IR to the kernel's compiler exactly once.
//
// The lowered body carries the composed-call TABLE the lowering built
// — every callee blob its IrStatementCall statements index — and that
// table rides into the compile beside the statements.
func buildSummaryBlob(ctx *FlowContext, declaration *ast.Node) (kernelbridge.SummaryBlob, int, bool) {
	lowered, ok := LowerSummaryBody(ctx, declaration)
	if !ok {
		return "", 0, false
	}
	// arity is the WHOLE slot count, not the parameter count: the
	// compiler numbers one entry state per binding (cur starts as
	// [0, arity)), and the apply side sends one state per slot — the
	// two numberings must be the same or every local's read collapses
	// onto entry 0 (summarize_eq's inRange hypothesis)
	blob, asked := kernelbridge.AskSummarize(lowered.SlotCount, lowered.Stmts, lowered.Table)
	if !asked {
		return "", 0, false
	}
	return blob, lowered.RetIndex, true
}
