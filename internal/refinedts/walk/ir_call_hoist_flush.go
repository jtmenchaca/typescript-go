// split from ir_call_hoist.go — the flush and its bookkeeping

package walk

import (
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

/* ── the flush ───────────────────────────────────────────────────── */

// TakeHoisted is the statement stream's half of the seam: the hoists
// accumulated since the last take, emptied out of the context.
//
// A statement route calls this AFTER its own readers have run and BEFORE
// it appends its own statements, so the temps are written before the
// statement that reads them — which is the whole point of the hoist.
func TakeHoisted(context *LoweringContext) []kernelbridge.IrStatement {
	if context == nil {
		return nil
	}
	// the per-node memo belongs to ONE statement: the next statement's own
	// occurrences of the same call node are its own call, so the map goes
	// out with the accumulation
	context.HoistedTemp = nil
	if len(context.Hoisted) == 0 {
		return nil
	}
	taken := context.Hoisted
	context.Hoisted = nil
	return taken
}

// DropHoistedFrom discards every hoist accumulated past a mark — what a
// statement route calls when its own reading DECLINED, so a half-read
// statement leaves no call statements behind.
//
// WHY THIS IS THE CLEAN VERSION, and what leaking would actually cost.
// Leaking a declined statement's hoists would not be UNSOUND: a hoisted
// call statement is the callee's own compiled program applied to argument
// effects the caller really does evaluate, and its only writes are a fresh
// temp nothing reads and bundle write-backs the ordering gate already
// proved disjoint from the statement's other slots. Running it and then
// havocking the statement's slots on top computes a state no weaker than
// havocking alone.
//
// It would be WASTEFUL and it would be CONFUSING. Wasteful: a call
// statement the kernel splices costs a summary application, and the
// statement whose expression needed it just lost its reading, so nothing
// will ever read the temp. Confusing: the havoc floor's whole contract is
// "this statement lowered to assignments of unknown, and nothing else",
// and a stray call statement ahead of it makes the lowering of a declined
// statement depend on how far its reading got before declining — the same
// source in the same context could lower two ways. So the mark is taken
// before each statement's routes run and the accumulation is truncated
// back to it on every decline.
func DropHoistedFrom(context *LoweringContext, mark int) {
	if context == nil {
		return
	}
	if mark < 0 || mark > len(context.Hoisted) {
		return
	}
	context.Hoisted = context.Hoisted[:mark]
	// the memo goes with them: a node whose call statement was just
	// truncated away must hoist AGAIN for whichever route reads it next,
	// or that route would answer a temp no statement ever writes — which
	// reads as the temp's entry state, a WRONG answer rather than a weak
	// one. Clearing the whole map is right because the mark is always the
	// statement's own start (LowerStatements takes it once per statement),
	// so everything in the map was accumulated past it.
	if mark == 0 {
		context.HoistedTemp = nil
	}
}

// HoistedMark is the length of the accumulation, taken before a
// statement's routes run so a decline can truncate back to it.
func HoistedMark(context *LoweringContext) int {
	if context == nil {
		return 0
	}
	return len(context.Hoisted)
}
