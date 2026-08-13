// The summary questions: compile a lowered body once, then apply that
// compilation wherever the body is called.
//
// A summary is a straight-line program over an indexed state space,
// quantified over every entry — so the kernel builds it once per
// declaration and the checker replays it against each call site's
// entry states instead of sending the body's whole IR again. The
// compiled form is the KERNEL's; nothing here reads inside it. It
// crosses back as the JSON text the kernel wrote, is held verbatim,
// and is spliced into the next question's wire unchanged.
//
// Both questions route the way the walk question routes: encode,
// ask1 through the question cache and the cost recorder, decode. The
// walk's own entry and answer state types are reused as they stand —
// entries are KnownStateWire, and an answer is the same state list the
// walk answers.
package kernelbridge

import (
	"fmt"
	"strings"
)

// SummaryBlob is the kernel's compiled summary, exactly as the kernel
// wrote it. The checker stores it, hands it back, and never parses it:
// the shape inside is the kernel's (`{"arity":k,"steps":[…],"out":[…]}`),
// and every reading of it happens kernel-side where the compile was
// proved faithful to the walk.
type SummaryBlob string

// StateWires encodes a list of entry states — the walk question's own
// entry encoding, shared with the two summary questions so all three
// spell a state the same way.
func StateWires(states []KnownStateWire) []string {
	parts := make([]string, len(states))
	for i, s := range states {
		parts[i] = StateWire(s)
	}
	return parts
}

// StmtWires encodes a list of lowered statements.
func StmtWires(stmts []IrStatement) []string {
	parts := make([]string, len(stmts))
	for i, s := range stmts {
		parts[i] = StmtWire(s)
	}
	return parts
}

// TableWire splices the summary table in raw: each entry IS the
// summary JSON the kernel answered, so it goes onto the wire as its own
// text with nothing wrapped around it.
func TableWire(table []SummaryBlob) string {
	parts := make([]string, len(table))
	for i, blob := range table {
		parts[i] = string(blob)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// TableField is the walk question's OPTIONAL table: an empty table
// writes nothing at all, so a question that carries no summaries keeps
// the exact bytes it had before summaries existed and every cached
// answer for it stays valid.
func TableField(table []SummaryBlob) string {
	if len(table) == 0 {
		return ""
	}
	return `,"table":` + TableWire(table)
}

// SummarizeWire is the summarize question: the body's arity, its
// lowered statements, and the table its `call` statements index.
func SummarizeWire(arity int, stmts []IrStatement, table []SummaryBlob) string {
	return fmt.Sprintf(
		`{"arity":%d,"stmts":[%s],"table":%s}`,
		arity, joinComma(StmtWires(stmts)), TableWire(table),
	)
}

// ApplySummaryWire is the applySummary question: the compiled summary
// spliced in raw, and the concrete entry states to run it on.
func ApplySummaryWire(blob SummaryBlob, entries []KnownStateWire) string {
	return fmt.Sprintf(
		`{"summary":%s,"entries":[%s]}`, string(blob), joinComma(StateWires(entries)),
	)
}

// DecodeSummaryBlob reads the summarize answer. The summary is captured
// WHOLE — the kernel's own text becomes the blob, with no structural
// decode of the steps — so the only reading done here is the envelope
// every answer wears: a stated error throws, and an answer that is not
// even a JSON object is a contract violation.
func DecodeSummaryBlob(raw string) SummaryBlob {
	Answered(raw)
	return SummaryBlob(raw)
}

// DecodeWalkStates reads the state list a body-shaped question answers
// — the walk's answer decode, shared with applySummary, which answers
// the same list. `question` names the asker in the panic message.
func DecodeWalkStates(parsed map[string]any, question string) []KnownStateWire {
	out, ok := parsed["states"].([]any)
	if !ok {
		panic(fmt.Sprintf("kernel %s answered an unexpected shape: %v", question, parsed))
	}
	states := make([]KnownStateWire, len(out))
	for i, s := range out {
		states[i] = DecodeWireState(s)
	}
	return states
}

// AskSummarize compiles one body to its summary, answering false when
// no kernel is loaded or when the kernel refuses the question — the
// caller keeps whatever route it had. A refusal panics inside the ask
// (every question in this package does), and the recover here is what
// turns that into the false half of the pair.
func AskSummarize(arity int, stmts []IrStatement, table []SummaryBlob) (blob SummaryBlob, ok bool) {
	kernel := KernelIfLoaded()
	if kernel == nil {
		return "", false
	}
	defer func() {
		if recover() != nil {
			blob, ok = "", false
		}
	}()
	return kernel.Summarize(arity, stmts, table), true
}

// AskApplySummary runs a compiled summary on concrete entry states,
// answering the exit state per out-slot. Same refusal discipline as
// AskSummarize: no kernel, or a kernel that declines, answers false and
// the caller degrades rather than claiming anything.
func AskApplySummary(blob SummaryBlob, entries []KnownStateWire) (exits []KnownStateWire, ok bool) {
	kernel := KernelIfLoaded()
	if kernel == nil {
		return nil, false
	}
	defer func() {
		if recover() != nil {
			exits, ok = nil, false
		}
	}()
	return kernel.ApplySummary(blob, entries), true
}
