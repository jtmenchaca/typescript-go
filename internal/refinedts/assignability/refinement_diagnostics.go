// The refinement diagnostics — tsc-shaped payloads with the span of
// the offending node. Two verdicts and two unsupported outcomes:
//
//	7001  not assignable        — the kernel refuted the subset (on
//	                              the scalars this is a theorem
//	                              with a counterexample behind it;
//	                              on sequence shapes "not proven
//	                              assignable", read conservatively)
//	7002  not yet determined    — nothing is proven about the value at
//	                              a checked position; the one honest
//	                              alert
//	7003  the empty set         — an annotation denotes ∅
//	7004  unhonorable statement — an annotation reference the checker
//	                              cannot read; unsupported, never dropped
//	7005  stale expectation     — a @refinedts-expect-error marker
//	                              covers a line nothing fired on
//	                              (service/expect_error.ts, the
//	                              editor view only)
//
// Ported 1:1 from assignability/refinement_diagnostics.ts — this
// file lands ahead of its directory because FlowContext's report
// sink carries the type. (The TS file also re-exports
// formatForDiagnostics; Go callers import refinementsets directly.)

package assignability

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/scanner"
)

type RefinementFix struct {
	Title   string
	NewText string
	// InsertAt is the insertion offset in the file (a pure
	// insertion: length 0).
	InsertAt int
}

type RefinementDiagnostic struct {
	Code        int // 7001 | 7002 | 7003 | 7004 | 7005
	MessageText string
	Start       int
	Length      int
	// Fix is the repair the editor can apply, where the checked
	// position spells one — computed at assignability time, where the
	// target and the node are both in view. The plugin only relays it.
	Fix *RefinementFix
}

const AlertText = "Type not yet determined. Narrow type for safe type inference."

// PowAlertStem is the `**` alert, at the checked position an unpinned
// power reaches. Grounding: exponentiation transfers exactly on
// ECMA-262's pinned branches (transfers/pow_pinned.lean); the spec's
// final step is implementation-approximated — no error bound, no
// rounding requirement, so no cross-engine result exists to verify
// (V8 and JavaScriptCore return different doubles for `10 ** 33`
// today, and neither is uniformly correctly rounded). The message
// shows the one repair that always works: comparisons are exactly
// specified, so checking the result proves what the comparison
// states. Where the checked position's stated set spells a liftable
// guard, the site-aware form appends it.
const PowAlertStem = "The result type of `**` cannot be verified in JavaScript. " +
	"Consider adding a comparison to re-establish the desired type"

const PowAlertText = PowAlertStem + "."

// At builds a diagnostic spanning the node. (TS `at` — getStart()
// skips leading trivia, which in tsgo is scanner.GetTokenPosOfNode;
// a bare node.Pos() would hang the span on the whitespace before it.)
func At(node *ast.Node, code int, messageText string) RefinementDiagnostic {
	sourceFile := ast.GetSourceFileOfNode(node)
	start := scanner.GetTokenPosOfNode(node, sourceFile, false)
	return RefinementDiagnostic{
		Code:        code,
		MessageText: messageText,
		Start:       start,
		Length:      node.End() - start,
	}
}
