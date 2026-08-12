// The @refinedts-expect-error reader — ONE recognizer for every
// consumer. A marker declares that a line is EXPECTED to fire: a
// standalone comment line covers the NEXT line (tsc's own
// expect-error convention), a trailing comment covers its own line,
// and an optional code (`@refinedts-expect-error 7001`) narrows the
// expectation to that code.
//
// Two presentations share this reader:
//
//	the CLI (check_cli.ts) prints covered fires with the `expected`
//	prefix and fails the run on stale markers;
//	the editor seam (checkWithProgram) SUPPRESSES covered fires and
//	surfaces each stale marker as its own 7005 diagnostic — tsc's
//	expect-error semantics, in the refinement layer.
//
// Ported 1:1 from service/expect_error.ts.
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

var expectErrorLinePattern = regexp.MustCompile(`(^|\s)(//|/\*|\*)?[^\n]*@refinedts-expect-error(?:\s+(\d+))?`)
var commentStartPattern = regexp.MustCompile(`//|/\*`)

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
	Used    bool
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
			line = i + 2
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
		markers = append(markers, &Expectation{
			Line:       line,
			MarkerLine: i + 1,
			Code:       code,
			HasCode:    hasCode,
			Used:       false,
		})
	}
	return markers
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
			if e.Line == line && (!e.HasCode || e.Code == d.Code) {
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
		kept = append(kept, assignability.RefinementDiagnostic{
			Code: 7005,
			MessageText: fmt.Sprintf(
				"Expected a refinement error on line %d%s and nothing fired — "+
					"remove the %s marker or restore the failing code.",
				e.Line, codeSuffix, expectErrorMarker,
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
