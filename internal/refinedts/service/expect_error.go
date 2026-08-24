// The @refinedts-expect-error reader — ONE recognizer for every
// consumer. A marker is recognized when @refinedts-expect-error is
// the first word of a comment's content, immediately after `//` or
// `/*` (whitespace only in between), OR when it directly follows
// another KNOWN directive token — today, `@ts-expect-error` — with
// only whitespace between them: the corpus's established compound for
// a row that is both a plain TypeScript error and a refinement fire,
// `// @ts-expect-error @refinedts-expect-error — reason`. This is the
// same discipline markers.rs applies to `# refinedpy: expect-error`,
// extended to the one compound spelling the corpus actually writes.
// The token appearing mid-sentence in arbitrary prose, even inside a
// real comment, is never a marker. A marker declares that a line is
// EXPECTED to carry an error: a
// standalone comment line covers the next line that is not itself a
// comment-only line — comment lines between the marker and the code
// are skipped, matching markers.rs — a trailing comment covers its
// own line, and an optional code (`@refinedts-expect-error 7001`)
// narrows the expectation to that code. Any text remaining after the
// marker and its optional code is the reason, printed when the marker
// goes stale. RTS7002 (the undetermined channel) is never matched by
// any marker, coded or not — see Covers.
//
// Two presentations share this reader, and both go through Covers so
// the exclusion holds in one place:
//
//	the CLI (cmd/refinedts-check/main.go) prints covered fires with
//	the `expected` prefix and fails the run on stale markers;
//	the editor seam (checkWithProgram) SUPPRESSES covered fires and
//	surfaces each stale marker as its own 7005 diagnostic — tsc's
//	expect-error semantics, in the refinement layer.
//
// Ported from service/expect_error.ts, with the comment-skip, reason
// capture, and marker-comment-only recognition adopted from
// markers.rs for marker-grammar parity.
package service

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
)

const expectErrorMarker = "@refinedts-expect-error"

// knownLeadingDirectives are the OTHER directive tokens that may sit
// between the comment opener and @refinedts-expect-error without
// disqualifying it as a marker — today, only `@ts-expect-error`, the
// corpus's one compound spelling for a row that is both a plain
// TypeScript error and a refinement fire. Adding a token here is a
// grammar change, not a per-fixture patch: search the corpus first.
const knownLeadingDirectives = `@ts-expect-error`

// expectErrorLinePattern recognizes a marker when
// @refinedts-expect-error is the first word of a comment's content —
// immediately after `//` or `/*`, optionally with spaces/tabs between
// — OR when it is preceded by nothing but one of
// knownLeadingDirectives, again with only spaces/tabs between (never
// other characters, so prose before the token never matches even
// inside a real comment). This mirrors markers.rs's own discipline:
// the token must open the comment or directly follow another known
// directive, not merely appear somewhere inside one. A
// `*`-continuation line of a block comment (no `//` or `/*` of its
// own) is never a comment opener, so a multi-line doc header that
// mentions the token mid-prose on such a line is never read as a
// marker.
var expectErrorLinePattern = regexp.MustCompile(`(^|\s)(//|/\*)[ \t]*(?:` + knownLeadingDirectives + `[ \t]+)?@refinedts-expect-error(?:\s+(\d+))?(.*)$`)
var commentStartPattern = regexp.MustCompile(`//|/\*`)
var commentOnlyLinePattern = regexp.MustCompile(`^\s*//`)
var reasonSeparatorPattern = regexp.MustCompile(`^[\s—-]+`)

// Expectation is the TS Expectation interface.
type Expectation struct {
	// Line is the 1-based source line the expectation covers.
	Line int
	// MarkerLine is the 1-based line the marker itself sits on.
	MarkerLine int
	// Code is the narrowed diagnostic code, or 0 when the marker
	// names no code (TS `number | null`; 0 is not a valid
	// RefinementDiagnostic code, so it is unambiguous here).
	Code    int
	HasCode bool
	// Reason is the text after the marker token (and its optional
	// numeric code), leading separators (space, `-`, `—`) trimmed —
	// ported from markers.rs's own reason capture. Empty when the
	// marker carries no trailing text.
	Reason string
	Used   bool
}

// undeterminedCode is RTS7002, the undetermined channel: nothing was
// proven about a checked position. No marker — coded or code-less —
// ever matches it. A marker swallowing "nothing was determined" would
// fake progress; the Python reader enforces the same law absolutely
// (markers.rs:11-12; its matcher has no numeric-code narrowing at
// all, so its ban already covers every marker shape). No fixture in
// this tree writes `@refinedts-expect-error 7002`, so the ban is
// adopted without exception, for parity.
const undeterminedCode = 7002

// Covers reports whether expectation e matches diagnostic code.
// RTS7002 is never matched, by any marker: a stale marker that only
// covered a 7002 line is the honest outcome, not a bug in the
// matcher.
func (e *Expectation) Covers(code int) bool {
	if code == undeterminedCode {
		return false
	}
	return !e.HasCode || e.Code == code
}

// ExpectationsOf reports every `@refinedts-expect-error` marker in the
// text, resolved to the line it covers.
func ExpectationsOf(text string) []*Expectation {
	markers := []*Expectation{}
	lines := strings.Split(text, "\n")
	for i, lineText := range lines {
		match := expectErrorLinePattern.FindStringSubmatchIndex(lineText)
		if match == nil {
			continue
		}
		commentStart := indexOfCommentStart(lineText)
		standalone := commentStart >= 0 && strings.TrimSpace(lineText[:commentStart]) == ""
		line := i + 1
		if standalone {
			line = coveredLineAfterComments(lines, i)
		}
		code := 0
		hasCode := false
		if match[6] >= 0 && match[7] >= 0 {
			n, err := strconv.Atoi(lineText[match[6]:match[7]])
			if err == nil {
				code = n
				hasCode = true
			}
		}
		reason := ""
		if match[8] >= 0 && match[9] >= 0 {
			reason = reasonSeparatorPattern.ReplaceAllString(lineText[match[8]:match[9]], "")
			reason = strings.TrimSpace(reason)
		}
		markers = append(markers, &Expectation{
			Line:       line,
			MarkerLine: i + 1,
			Code:       code,
			HasCode:    hasCode,
			Reason:     reason,
			Used:       false,
		})
	}
	return markers
}

// coveredLineAfterComments is markers.rs's own skip: a standalone
// marker at 0-based line markerIndex covers the next line that is not
// itself a comment-only line (trimmed content starting with `//`) —
// host-marker or explanatory comment lines may sit between the marker
// and the code it covers. Blank lines are not skipped; they become
// the covered line, same as any other non-comment line. Falls back to
// one past the last line when every remaining line is a comment.
func coveredLineAfterComments(lines []string, markerIndex int) int {
	for j := markerIndex + 1; j < len(lines); j++ {
		if !commentOnlyLinePattern.MatchString(lines[j]) {
			return j + 1 // 1-based
		}
	}
	return markerIndex + 2
}

// indexOfCommentStart is the TS `lineText.search(/\/\/|\/\*/)` — the
// index of the first `//` or `/*`, or -1 when neither appears.
func indexOfCommentStart(lineText string) int {
	loc := commentStartPattern.FindStringIndex(lineText)
	if loc == nil {
		return -1
	}
	return loc[0]
}

// EditorView is the editor's view of a file's judgments: every fire a
// marker covers is suppressed, and every marker covering a line
// nothing fired on becomes its own 7005 diagnostic anchored on the
// marker text — so a stale expectation is as visible in the editor as
// a real fire, exactly the @ts-expect-error contract.
func EditorView(text string, refinements []assignability.RefinementDiagnostic) []assignability.RefinementDiagnostic {
	expectations := ExpectationsOf(text)
	if len(expectations) == 0 {
		return refinements
	}
	lineStarts := []int{0}
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			lineStarts = append(lineStarts, i+1)
		}
	}
	lineOf := func(offset int) int {
		lo := 0
		hi := len(lineStarts) - 1
		for lo < hi {
			mid := (lo + hi + 1) >> 1
			if lineStarts[mid] <= offset {
				lo = mid
			} else {
				hi = mid - 1
			}
		}
		return lo + 1 // 1-based
	}
	kept := []assignability.RefinementDiagnostic{}
	for _, d := range refinements {
		line := lineOf(d.Start)
		var covering *Expectation
		for _, e := range expectations {
			if e.Line == line && e.Covers(d.Code) {
				covering = e
				break
			}
		}
		if covering != nil {
			covering.Used = true
			continue
		}
		kept = append(kept, d)
	}
	for _, e := range expectations {
		if e.Used {
			continue
		}
		lineStart := 0
		if e.MarkerLine-1 < len(lineStarts) {
			lineStart = lineStarts[e.MarkerLine-1]
		}
		lineEnd := len(text)
		if e.MarkerLine < len(lineStarts) {
			lineEnd = lineStarts[e.MarkerLine] - 1
		}
		markerAt := indexFrom(text, expectErrorMarker, lineStart)
		anchored := markerAt >= 0 && markerAt < lineEnd
		start := lineStart
		length := lineEnd - lineStart
		if length < 1 {
			length = 1
		}
		if anchored {
			start = markerAt
			length = len(expectErrorMarker)
		}
		codeSuffix := ""
		if e.HasCode {
			codeSuffix = fmt.Sprintf(" (RTS%d)", e.Code)
		}
		reasonSuffix := ""
		if e.Reason != "" {
			reasonSuffix = fmt.Sprintf(" (%s)", e.Reason)
		}
		kept = append(kept, assignability.RefinementDiagnostic{
			Code: 7005,
			MessageText: fmt.Sprintf(
				"Expected a refinement error on line %d%s and nothing fired — "+
					"remove the %s marker or restore the failing code%s.",
				e.Line, codeSuffix, expectErrorMarker, reasonSuffix,
			),
			Start:  start,
			Length: length,
		})
	}
	sort.SliceStable(kept, func(i, j int) bool { return kept[i].Start < kept[j].Start })
	return kept
}

// indexFrom is the TS `text.indexOf(MARKER, lineStart)`.
func indexFrom(text, marker string, from int) int {
	if from > len(text) {
		return -1
	}
	idx := strings.Index(text[from:], marker)
	if idx < 0 {
		return -1
	}
	return idx + from
}
