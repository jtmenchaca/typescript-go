// The inline walk's two-axis budget: a call chain can only nest so
// deep before exact replay stops determining anything useful, and an
// entry's total inline volume can only grow so large before the walk
// stops finishing at all.

package walk

import "github.com/microsoft/typescript-go/internal/refinedts/tracing"

// inlineDepthLimit bounds how many contract bodies nest inside one
// another during a single inline. A genuine call chain 24 deep is
// past what exact replay determines anything useful about — direct
// recursion is already caught by ctx.Inlining and answers unknown at
// the first re-entry, so this limit is for long ACYCLIC chains
// (DI-style constructor graphs) that no per-symbol guard catches.
const inlineDepthLimit = 24

// InlineCallLimit bounds how many contract bodies one entry walk may
// inline in total, across every call chain the entry reaches. The
// recharts corpus determines everything it determines under roughly
// 6,300 inlines per entry; the nest corpus's DI chains multiply calls
// across paths and run past millions with no bound. Exported so
// service/check.go can size the per-entry budget it hands to
// FlowContext.InlineBudget.
const InlineCallLimit = 50_000

// inlineBudgetOpen answers whether ctx may inline one more contract
// call. It answers false past the depth limit — checked before the
// volume decrement, since a call that is too deep should not also
// spend a unit of the entry's shared budget. When ctx carries a
// budget pointer (one entry walk, decremented across every inline in
// it), this decrements it and answers false once it reaches zero. A
// nil budget pointer means unbudgeted on the volume axis — the
// single-file Check paths and the tests build FlowContext directly
// and never set one.
func inlineBudgetOpen(ctx *FlowContext) bool {
	if ctx.InlineDepth >= inlineDepthLimit {
		tracing.Count("inline.budget.depth", 0)
		return false
	}
	if ctx.InlineBudget == nil {
		return true
	}
	if *ctx.InlineBudget <= 0 {
		tracing.Count("inline.budget.volume", 0)
		return false
	}
	*ctx.InlineBudget--
	return true
}
