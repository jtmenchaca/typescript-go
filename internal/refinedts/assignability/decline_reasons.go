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

import "github.com/microsoft/typescript-go/internal/ast"

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
var collectors [][]ReasonNote

func BeginReasonNotes() {
	collectors = append(collectors, []ReasonNote{})
}

func EndReasonNotes() []ReasonNote {
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
	return len(collectors) > 0
}

// NoteReason records one reason. A no-op when nothing is collecting —
// the editor's hover path pays a length check and nothing else.
func NoteReason(note ReasonNote) {
	if len(collectors) == 0 {
		return
	}
	for i := range collectors {
		collectors[i] = append(collectors[i], note)
	}
}
