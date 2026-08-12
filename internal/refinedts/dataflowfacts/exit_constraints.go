// Loop-exit difference rows: a condition-tested loop that exits hands
// the negated condition to everything after it in the same statement
// list. The rows ride this side channel because the list walk — not
// the loop — owns the continuation's context.
package dataflowfacts

import (
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
)

// loopExitConstraintsMu guards loopExitConstraintsCache.
//
// The TS source keys this store with a WeakMap<ts.Node, …>; Go has no
// weak maps, so this substitutes a regular map guarded by a mutex.
// Functionally identical per program: entries live exactly as long as
// the program that produced their nodes is in use by this port.
var (
	loopExitConstraintsMu    sync.Mutex
	loopExitConstraintsCache = map[*ast.Node][]DifferenceConstraint{}
)

// NoteExitConstraints records the difference rows a loop's exit hands to
// its continuation.
func NoteExitConstraints(loop *ast.Node, rows []DifferenceConstraint) {
	loopExitConstraintsMu.Lock()
	loopExitConstraintsCache[loop] = rows
	loopExitConstraintsMu.Unlock()
}

// ExitConstraintsOf reads the rows recorded for a statement, if any.
func ExitConstraintsOf(statement *ast.Node) ([]DifferenceConstraint, bool) {
	loopExitConstraintsMu.Lock()
	rows, ok := loopExitConstraintsCache[statement]
	loopExitConstraintsMu.Unlock()
	return rows, ok
}
