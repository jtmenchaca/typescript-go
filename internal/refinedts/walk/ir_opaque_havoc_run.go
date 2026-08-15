// split from ir_opaque_havoc.go — run bookkeeping and naming a decline

package walk

import (
	"sync"
)

/* ── naming a decline ────────────────────────────────────────────── */

// The lowering's decline reasons, one per lowering run, keyed by the
// context the run carries. A body that declines answers a bool and
// nothing else — the signature every route and every caller is built
// around — so the NAME of what it refused rides here beside it.
//
// Why this and not a context field: the field would say the same thing,
// and this says it without the whole package's contexts changing shape.
// Keyed by POINTER, which is the identity a lowering run has: nested
// LowerStatements calls for an if's arms share the caller's context, so
// an arm's refusal names the body's refusal, which is what the report
// wants. First-wins, exactly as FirstHavoc is, so the name points at
// the earliest place the body could not be read.
var (
	declinedConstructsLock sync.Mutex
	declinedConstructs     = map[*LoweringContext]string{}
	// how many LowerStatements runs are in flight on this context. The
	// OUTERMOST one owns the name: it clears any name left behind before
	// it starts, and drops the name on the way out when it SUCCEEDED —
	// an inner arm may have declined and been stood in for by the havoc
	// floor, and a body that lowered has no decline to report.
	loweringDepth = map[*LoweringContext]int{}
)

// EnterLoweringRun marks a LowerStatements run beginning on a context and
// answers whether it is the OUTERMOST one. The outermost run starts with
// a clean slate, so a name left by an earlier lowering of the same
// context cannot be read as this one's.
func EnterLoweringRun(context *LoweringContext) bool {
	if context == nil {
		return false
	}
	declinedConstructsLock.Lock()
	defer declinedConstructsLock.Unlock()
	depth := loweringDepth[context]
	loweringDepth[context] = depth + 1
	if depth == 0 {
		delete(declinedConstructs, context)
		return true
	}
	return false
}

// LeaveLoweringRun marks a run finished. The outermost run that
// SUCCEEDED drops the name: an inner arm's decline that the havoc floor
// stood in for is not the body's decline, and the body lowered.
func LeaveLoweringRun(context *LoweringContext, succeeded bool) {
	if context == nil {
		return
	}
	declinedConstructsLock.Lock()
	defer declinedConstructsLock.Unlock()
	depth := loweringDepth[context] - 1
	if depth <= 0 {
		delete(loweringDepth, context)
		if succeeded {
			delete(declinedConstructs, context)
		}
		return
	}
	loweringDepth[context] = depth
}

// NoteDeclinedConstruct records WHAT a lowering run refused, first-wins.
// Every decline that has a name for its construct calls this on the way
// out; the body-level owner reads it with DeclinedConstructOf and puts
// it in the outcome report, so a histogram row names syntax someone can
// act on rather than "a statement the lowering does not read".
func NoteDeclinedConstruct(context *LoweringContext, construct string) {
	if context == nil || construct == "" {
		return
	}
	declinedConstructsLock.Lock()
	defer declinedConstructsLock.Unlock()
	if _, held := declinedConstructs[context]; held {
		return
	}
	declinedConstructs[context] = construct
}

// DeclinedConstructOf is the name a declining lowering left behind, and
// it CLEARS it: one lowering run, one read. Empty where the run declined
// with no name of its own, and the caller then says what it knows.
func DeclinedConstructOf(context *LoweringContext) string {
	if context == nil {
		return ""
	}
	declinedConstructsLock.Lock()
	defer declinedConstructsLock.Unlock()
	held := declinedConstructs[context]
	delete(declinedConstructs, context)
	return held
}
