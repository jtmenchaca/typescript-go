// Every declared value gets one of two verdicts. Either the checker
// DETERMINED everything the file determines about it — an exact
// value, a set, or provably nothing beyond the type (the callee has
// no body anywhere in the file, TypeScript's own line already shows
// the exact value, a function is read at its calls) — or the value is
// UNSUPPORTED: something in the file determines more than the checker
// states, and the count of those is the work list.
//
// A walk records the reason at the position it finds one, in a plain
// sentence about that position — never a category name. A value with
// no answer and no recorded reason is unsupported by default:
// unexplained weighs against the checker, never for it.
//
// Recording changes no conclusion. A note is a fact about the
// checker, never about the value.
//
// Ported 1:1 from assignability/decline_reasons.ts.

package assignability

import (
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
)

type ReasonNote struct {
	// Site is which reader recorded it: "expression" | "guard".
	Site string
	// Node is the node the reason is about.
	Node *ast.Node
	// Said is the plain sentence about this position.
	Said string
	// Unsupported is true when something in the file determines more
	// than the checker states here — the verdict that counts. False
	// when the checker provably determined everything the file does.
	Unsupported bool
}

// A stack, because collections nest: coverage collects across a whole
// file while each position's own walk collects for its row. A note
// lands in every open collector, so the outer view sees what the
// inner walks saw.
//
// BLOCKED under goroutine-per-entry (parallel-sweep-audit.md): this
// stack's whole design is per-check nesting, and the fix the audit
// calls for is a context-carried collector keyed on the goroutine's
// check handle (e.g. *program.CheckerProgram, which walk/check.go
// allocates fresh per entry file). But BeginReasonNotes, EndReasonNotes,
// CollectingReasons, and NoteReason are the exported surface every
// walk/ caller uses (evaluate_expression.go, builtin_models.go,
// unmodeled_call_result.go, web_api_models.go, binary_arithmetic.go,
// coercion_models.go, math_builtin_models.go, flow_state_at_position.go)
// and NONE of their call sites pass a handle through — Begin/End/
// Collecting take zero arguments, and NoteReason takes only the
// ReasonNote being recorded. (web_api_models.go's WebMethodCall is the
// sharpest case: its caller in builtin_models.go HAS a *FlowContext in
// scope but only forwards WebMethodCall's own narrower
// *program.CheckerProgram/*ast.Node/string parameters — no handle
// reaches these four calls either way.) Changing any of these four
// signatures to accept one is exactly the exported-signature change
// this port unit is not allowed to make (walk/ calls them and is owned
// by other agents in this wave).
//
// So this stays the single global stack, now mutex-guarded so a push,
// pop, or append is at least atomic — but the guard does NOT make
// concurrent checks correct. Two goroutines each mid-walk push their
// own frame onto the SAME stack; a note recorded by goroutine A lands
// in whichever frames happen to be open at that instant, which can
// include a frame goroutine B pushed for an unrelated file, and B's
// EndReasonNotes can pop A's frame instead of its own (LIFO order
// across two independent call sequences is not the nesting order
// either one intended). The mutex only prevents a torn slice header;
// it does not prevent this cross-attribution. Under goroutine-per-entry
// concurrency this package MUST NOT be used from more than one
// goroutine at a time until a handle is threaded through; the
// process-per-shard design (parallel-sweep-audit.md's ship-first row)
// sidesteps this entirely because every global is per-process there.
var (
	collectorsMu sync.Mutex
	collectors   [][]ReasonNote
)

func BeginReasonNotes() {
	collectorsMu.Lock()
	defer collectorsMu.Unlock()
	collectors = append(collectors, []ReasonNote{})
}

func EndReasonNotes() []ReasonNote {
	collectorsMu.Lock()
	defer collectorsMu.Unlock()
	if len(collectors) == 0 {
		return []ReasonNote{}
	}
	held := collectors[len(collectors)-1]
	collectors = collectors[:len(collectors)-1]
	return held
}

// CollectingReasons says whether anything is collecting — the guard
// for note text that costs something to build (source text, symbol
// walks).
func CollectingReasons() bool {
	collectorsMu.Lock()
	defer collectorsMu.Unlock()
	return len(collectors) > 0
}

// NoteReason records one reason. A no-op when nothing is collecting —
// the editor's hover path pays a length check and nothing else.
func NoteReason(note ReasonNote) {
	collectorsMu.Lock()
	defer collectorsMu.Unlock()
	if len(collectors) == 0 {
		return
	}
	for i := range collectors {
		collectors[i] = append(collectors[i], note)
	}
}
