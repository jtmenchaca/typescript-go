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
	"sync/atomic"

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

// summaryBlobsMu guards EVERY map below. summaryBlobs holds the
// finished answers; summaryBuilding holds the declarations whose
// build is in flight — the cycle guard; summaryCycled remembers which
// builds re-entered themselves, so the fixpoint knows which
// declarations recurse; summarySelfBlobs is the fixpoint's injection
// seam.
//
// Keyed by declaration ALONE — a summary quantifies over all entries,
// so it is context-free and shared across every check with no
// per-check scoping and no fingerprints.
var (
	summaryBlobsMu  sync.Mutex
	summaryBlobs    = map[*ast.Node]summaryBlobEntry{}
	summaryBuilding = map[*ast.Node]struct{}{}
	// summaryCycled: declarations whose own build re-entered
	// SummaryBlobFor for themselves — the recursive ones. Set by the
	// cycle arm, read once by the outer build when it finishes so the
	// fixpoint upgrade is attempted exactly on the bodies that need it.
	summaryCycled = map[*ast.Node]struct{}{}
	// summarySelfBlobs: the fixpoint's SELF-CONST override. While a
	// declaration has an entry here, SummaryBlobFor answers that blob
	// for it outright — before the store, before the cycle guard — so a
	// re-lowering of the body lowers its own self-call as a REAL call
	// statement against a table entry the fixpoint controls, instead of
	// re-entering the in-flight build. Cleared when the round ends.
	summarySelfBlobs = map[*ast.Node]kernelbridge.SummaryBlob{}
)

// SummaryCycleInFlight answers whether the declaration's own summary
// build is running right now — a call to it from inside its own
// lowering is the recursive case. Read under the registry mutex, so a
// lowering on another goroutine sees a consistent answer.
//
// The lowering route uses this to lower a cycle call as HAVOC rather
// than declining the whole body: writing top is sound unconditionally,
// so a body containing a recursive call still compiles and the
// recursive declaration itself gets a blob. That havoc tier is the
// FLOOR; summary_fixpoint.go's certified constant sits on top of it.
func SummaryCycleInFlight(declaration *ast.Node) bool {
	if declaration == nil {
		return false
	}
	summaryBlobsMu.Lock()
	defer summaryBlobsMu.Unlock()
	_, building := summaryBuilding[declaration]
	return building
}

// summaryHitOwnCycle answers whether the declaration's build re-entered
// itself, clearing the bit — asked once, by the outer build, right
// after it stores its answer.
func summaryHitOwnCycle(declaration *ast.Node) bool {
	summaryBlobsMu.Lock()
	defer summaryBlobsMu.Unlock()
	_, cycled := summaryCycled[declaration]
	delete(summaryCycled, declaration)
	return cycled
}

// fixpointGateMu serializes the fixpoint against ITSELF and against
// every ordinary registry ask, so that while a self-const override is
// installed the only asks reaching it are the fixpoint's own
// re-lowerings.
//
// The gate has to be RE-ENTRANT for its holder: the fixpoint's own
// re-lowering runs the ordinary lowering code, which calls
// SummaryBlobFor, which waits on this gate — a plain mutex would
// self-deadlock there. fixpointGateHeld is what makes the holder pass
// through: it is set only while the gate is held, and because the gate
// is EXCLUSIVE, the only goroutine that can be running registry code
// while it is set is the holder's own. Every other goroutine is
// parked in waitOutFixpoint's Lock, before it ever reads the flag.
// The flag is atomic because the holder writes it while other
// goroutines read it outside the gate — that read is the whole point,
// and reading it unsynchronized would be a race the detector calls.
var (
	fixpointGateMu   sync.Mutex
	fixpointGateHeld atomic.Bool
)

// holdRegistryForFixpoint takes the exclusive hold and answers the
// release. While it is held, an ordinary SummaryBlobFor from any other
// goroutine waits, so no route outside the fixpoint can observe a
// self-const override as if it were a certified answer.
func holdRegistryForFixpoint() func() {
	fixpointGateMu.Lock()
	fixpointGateHeld.Store(true)
	return func() {
		fixpointGateHeld.Store(false)
		fixpointGateMu.Unlock()
	}
}

// waitOutFixpoint makes an ordinary ask wait for a running fixpoint to
// finish, then proceed; the fixpoint's own re-entrant asks pass
// straight through. Taking and immediately releasing the gate is what
// "wait it out" means here: the ask holds nothing afterwards, so it
// cannot block the next fixpoint either.
//
// A goroutine that is NOT the holder and reads the flag as true has
// only misread a fixpoint that is starting or ending; it skips the
// wait for that one ask and proceeds. That is a precision cost, not a
// soundness one: the answer it gets is whatever the STORE holds, which
// is the certified constant or the havoc floor — both sound. What it
// cannot get is an uncertified override, because the override arm
// below is reached only from inside the fixpoint (the fixpoint
// installs the override AFTER taking the gate and removes it before
// releasing, and the arm is guarded by the same flag).
func waitOutFixpoint() {
	if fixpointGateHeld.Load() {
		return
	}
	fixpointGateMu.Lock()
	fixpointGateMu.Unlock()
}

// holdSelfBlob installs the fixpoint's self-const override for one
// declaration and answers the release. While it is held AND the gate is
// held, summaryBlobForUngated answers that blob for that declaration.
func holdSelfBlob(declaration *ast.Node, blob kernelbridge.SummaryBlob) func() {
	summaryBlobsMu.Lock()
	previous, had := summarySelfBlobs[declaration]
	summarySelfBlobs[declaration] = blob
	summaryBlobsMu.Unlock()
	return func() {
		summaryBlobsMu.Lock()
		if had {
			summarySelfBlobs[declaration] = previous
		} else {
			delete(summarySelfBlobs, declaration)
		}
		summaryBlobsMu.Unlock()
	}
}

// replaceStoredBlob swaps a declaration's stored answer for a certified
// one, keeping the out-shape the original build assigned: the certified
// constant answers through the SAME out index the body's ret rides, so
// every call site that already read the shape stays correct.
func replaceStoredBlob(declaration *ast.Node, blob kernelbridge.SummaryBlob) {
	summaryBlobsMu.Lock()
	held, has := summaryBlobs[declaration]
	if has {
		held.Blob = blob
		held.Ok = true
		summaryBlobs[declaration] = held
	}
	summaryBlobsMu.Unlock()
}

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
// store; the lowering reads SummaryCycleInFlight and lowers that call
// as havoc, so the recursive body still compiles at the havoc floor.
// The cycle is REMEMBERED, and when the outer build finishes the
// fixpoint tries once to replace the havoc floor with a certified
// constant (summary_fixpoint.go).
//
// A declaration the fixpoint is holding a SELF-CONST override for
// answers that blob outright — that is how a re-lowering inside the
// fixpoint gets its own self-call lowered as a real call statement
// against a table entry the fixpoint controls. Only the fixpoint's own
// re-lowerings can be inside here while an override is installed: an
// ordinary ask waits the fixpoint out first (waitOutFixpoint), and the
// fixpoint's re-lowerings reach the ungated core below directly.
func SummaryBlobFor(ctx *FlowContext, declaration *ast.Node) (kernelbridge.SummaryBlob, bool) {
	if declaration == nil {
		return "", false
	}
	waitOutFixpoint()
	return summaryBlobForUngated(ctx, declaration)
}

// summaryBlobForUngated is SummaryBlobFor without the gate wait — the
// body of the ask, reached directly by the fixpoint (which already
// holds the gate and would deadlock waiting on it) and through the
// gated door by everyone else.
func summaryBlobForUngated(ctx *FlowContext, declaration *ast.Node) (kernelbridge.SummaryBlob, bool) {
	if declaration == nil {
		return "", false
	}
	summaryBlobsMu.Lock()
	// the self-const override, consulted ONLY while a fixpoint holds the
	// gate — so the uncertified constant is visible to the fixpoint's own
	// re-lowering and to nothing else
	if fixpointGateHeld.Load() {
		if held, has := summarySelfBlobs[declaration]; has {
			summaryBlobsMu.Unlock()
			return held, true
		}
	}
	if held, has := summaryBlobs[declaration]; has {
		summaryBlobsMu.Unlock()
		return held.Blob, held.Ok
	}
	if _, cycling := summaryBuilding[declaration]; cycling {
		summaryCycled[declaration] = struct{}{}
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

	// the build hit its OWN cycle guard: its self-calls lowered as
	// havoc, so its blob is the floor. Try once for better — a
	// certified constant. Failure keeps the floor, and the attempt
	// never runs again for this declaration because the store now holds
	// an entry the ask above hits first.
	if ok && summaryHitOwnCycle(declaration) {
		if certified, upgraded := upgradeRecursiveSummary(ctx, declaration); upgraded {
			replaceStoredBlob(declaration, certified)
			return certified, true
		}
	}
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
