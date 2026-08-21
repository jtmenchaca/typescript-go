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
	// Steps are the other places the reader has to see to understand
	// this finding: the declaration a bound was instantiated from, the
	// guard that established a fact, and — once the edge lands — the
	// statement in the OTHER language that a crossed obligation came
	// from. Each carries its own file and span, so the host renders
	// them as relatedInformation and the editor shows them as
	// navigable links. Empty for every finding that says everything at
	// its own position.
	Steps []RelatedStep
}

// RelatedStep is one place-and-sentence a finding points at. The
// file is named two ways and exactly one of them is filled:
//
//   - File, for a step inside the compiled program — the checker
//     already holds the *ast.SourceFile, and the host renders the span
//     against the very text it parsed.
//   - ForeignFile + ForeignText, for a step in a file the TypeScript
//     program never compiled (the Python side of a crossing). The
//     host has no SourceFile for it, so the step carries the path and
//     the text it was read from; the bridge synthesizes a
//     file-shaped carrier from them (see ls/refinedts_diagnostics.go).
//
// Start/Length are byte offsets into that file's text, the same
// coordinates RefinementDiagnostic itself uses.
type RelatedStep struct {
	// Sentence is what this place said, in plain words — the same
	// register as MessageText, and never a category name.
	Sentence string
	// File is the in-program file the step sits in. Nil for a foreign
	// step.
	File *ast.SourceFile
	// ForeignFile is the absolute path of a file outside the compiled
	// program. Empty for an in-program step.
	ForeignFile string
	// ForeignText is that file's text, which the host needs to turn a
	// byte offset into a line and character. Empty when the step is
	// in-program, or when the position is the file's head (offset 0).
	ForeignText string
	Start       int
	Length      int
}

// StepAt builds an in-program related step spanning the node, with
// the same leading-trivia-skipping start `At` uses — so a step and a
// finding hung on the same node cover the same characters.
func StepAt(node *ast.Node, sentence string) RelatedStep {
	sourceFile := ast.GetSourceFileOfNode(node)
	start := scanner.GetTokenPosOfNode(node, sourceFile, false)
	return RelatedStep{
		Sentence: sentence,
		File:     sourceFile,
		Start:    start,
		Length:   node.End() - start,
	}
}

// StepInForeignFile builds a related step in a file the TypeScript
// program never compiled — the Python half of a crossing. The text is
// the file's contents as the adapter read them; the host needs it to
// place the offset on a line. Passing an empty text pins the step to
// the head of the file.
func StepInForeignFile(path string, text string, start int, length int, sentence string) RelatedStep {
	return RelatedStep{
		Sentence:    sentence,
		ForeignFile: path,
		ForeignText: text,
		Start:       start,
		Length:      length,
	}
}

// WithSteps returns the diagnostic carrying the given related steps.
// Additive by construction: a finding without steps is exactly what it
// was before, and every existing reader that ignores Steps still reads
// the same message at the same span.
func (d RefinementDiagnostic) WithSteps(steps ...RelatedStep) RefinementDiagnostic {
	d.Steps = append(append([]RelatedStep(nil), d.Steps...), steps...)
	return d
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
