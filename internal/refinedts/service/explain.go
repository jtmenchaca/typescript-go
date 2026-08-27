// The -explain entry point (DERIVATION-TRACE.md, "Gating"):
//
//	refinedts-check -explain <path>:<line> <file.ts>
//
// runs the ordinary check and, for every judged position on that line,
// records the derivation the walk already performs. The rendered tree
// prints by default; -explain-json prints the raw spans, which validate
// against packages/tests/diagnostics/trace.schema.json.
//
// Gating is per POSITION, exactly as the spec states: the walk runs
// normally and spans are recorded only where the current range
// intersects the requested one. The judge (walk/check_assignability.go)
// is what applies that test — it asks WantsLine before opening a root.
//
// OFF IS A NIL TEST. With no -explain, ExplainRecorder stays nil, the
// package's tracing atomic stays zero, and every seam pays one atomic
// load. The wall gate never sets it.

package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/microsoft/typescript-go/internal/refinedts/derivation"
)

// ExplainRecorder is the run's Recorder while -explain is in effect,
// nil otherwise. CheckFiles' per-entry workers adopt it so their spans
// land in one trace.
var ExplainRecorder *derivation.Recorder

// explainClose unregisters the recorder from the goroutine that began
// it.
var explainClose = func() {}

// ParseExplainRequest reads the flag's value — `<path>:<line>` — into
// its two parts. The path may itself contain colons on no platform this
// runs on, so the LAST colon separates the line, and a value with no
// parsable line is refused rather than read as line 0 (which would mean
// "every position in the file" and silently answer a different
// question).
func ParseExplainRequest(value string) (path string, line int, ok bool) {
	at := strings.LastIndex(value, ":")
	if at < 0 {
		return "", 0, false
	}
	parsed, err := strconv.Atoi(value[at+1:])
	if err != nil || parsed < 1 {
		return "", 0, false
	}
	return value[:at], parsed, true
}

// BeginExplain starts recording for the requested position. The
// returned closer stops it.
func BeginExplain(path string, line int) func() {
	position := path + ":" + strconv.Itoa(line)
	recorder, closer := derivation.BeginRecording("ts", position, line)
	ExplainRecorder = recorder
	explainClose = closer
	return func() {
		closer()
		explainClose = func() {}
	}
}

// ExplainTraces are the traces recorded for the requested position, in
// the order their roots closed. Empty means no judged position sat on
// that line — which is an answer: nothing there was checked.
//
// PATHS ARE SPELLED RELATIVE TO THE REPOSITORY ROOT HERE, at emission,
// per DERIVATION-TRACE.md ("Gating"): "Paths in refinery.position and
// refinery.range are spelled relative to the repository root in the
// emitted document, whatever the adapter's internal form — conformance
// diffs across languages depend on one spelling." The walk's own form is
// whatever tsgo resolved the source file to, which is absolute and
// therefore different on every machine; a conformance diff against the
// Rust or clang-tidy trace would read those home-directory prefixes as
// a disagreement about the program. Internal machinery keeps the
// absolute form — only the document that leaves this process is
// rewritten.
func ExplainTraces() []derivation.Trace {
	if ExplainRecorder == nil {
		return nil
	}
	traces := ExplainRecorder.Traces()
	root := repositoryRoot()
	// indexed, not ranged: Trace.Position is a field on the value, and a
	// ranged copy's rewrite would never reach the slice
	for at := range traces {
		relativizeTrace(&traces[at], root)
	}
	return traces
}

// repositoryRoot is the nearest enclosing directory holding a .git
// entry, walking up from the working directory. Empty when none is found
// — a checkout-less run then keeps whatever paths it had, which is the
// truthful answer rather than a guess at a prefix to strip.
func repositoryRoot() string {
	at, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(at, ".git")); err == nil {
			return at
		}
		parent := filepath.Dir(at)
		if parent == at {
			return ""
		}
		at = parent
	}
}

// relativizeTrace rewrites every path the emitted document carries: the
// trace's own position, and each span's refinery.position and
// refinery.range, through the whole tree and the chained roots.
func relativizeTrace(trace *derivation.Trace, root string) {
	if trace == nil || root == "" {
		return
	}
	trace.Position = relativePath(trace.Position, root)
	var walk func(span *derivation.Span)
	walk = func(span *derivation.Span) {
		if span == nil {
			return
		}
		for _, key := range []string{derivation.AttrPosition, derivation.AttrRange} {
			if value, held := span.Attributes[key]; held {
				span.Attributes[key] = relativePath(value, root)
			}
		}
		for _, child := range span.Children {
			walk(child)
		}
	}
	walk(trace.Root)
	for _, bound := range trace.Chain {
		walk(bound)
	}
}

// relativePath strips the repository root from a `path:…` string,
// leaving everything after the path untouched — the line/column tail is
// not a path and is never rewritten.
//
// The path is the leading segment before the first coordinate, and it is
// recognized by prefix rather than parsed: a value that does not start
// inside the repository (a library file outside the checkout) is left
// exactly as it is, because a path that cannot be made relative is
// better spelled in full than half-stripped.
func relativePath(value string, root string) string {
	prefix := root
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	if !strings.HasPrefix(value, prefix) {
		return value
	}
	return filepath.ToSlash(value[len(prefix):])
}

// ExplainJSON is the traces as schema-valid JSON — one object per
// judged position, in a JSON array so several positions on one line
// come back together.
func ExplainJSON() (string, error) {
	traces := ExplainTraces()
	if traces == nil {
		traces = []derivation.Trace{}
	}
	encoded, err := json.MarshalIndent(traces, "", "  ")
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// ExplainText is the traces as rendered trees — the default -explain
// output, one tree per judged position, each followed by its
// projection.
func ExplainText() string {
	var out strings.Builder
	traces := ExplainTraces()
	if len(traces) == 0 {
		return "no judged position on that line\n"
	}
	for i, trace := range traces {
		if i > 0 {
			out.WriteString("\n")
		}
		out.WriteString(derivation.Render(trace))
	}
	return out.String()
}
