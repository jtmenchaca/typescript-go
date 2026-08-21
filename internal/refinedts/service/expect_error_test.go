// The @refinedts-expect-error reader and EditorView, at the unit
// level: covered fires suppress, stale markers surface as 7005, and
// RTS7002 (the undetermined channel) is never matched by any marker —
// coded or code-less. Diagnostics are built directly
// (assignability.RefinementDiagnostic{...}) so these tests need no
// kernel, mirroring internal/ls/refinedts_related_steps_test.go's own
// pattern.

package service

import (
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
)

func fireAt(start int, code int) assignability.RefinementDiagnostic {
	return assignability.RefinementDiagnostic{Code: code, MessageText: "x", Start: start, Length: 1}
}

// TestMarkerPlacementsResolveLines: a standalone marker covers the
// next line, a trailing one covers its own, and a numeric suffix
// narrows the expectation to that code.
func TestMarkerPlacementsResolveLines(t *testing.T) {
	text := "// @refinedts-expect-error\n" + // covers line 2
		"bad();\n" +
		"worse(); // @refinedts-expect-error 7001\n" // covers line 3, code-narrowed
	read := ExpectationsOf(text)
	if len(read) != 2 {
		t.Fatalf("expected 2 expectations, got %d", len(read))
	}
	if read[0].Line != 2 || read[0].HasCode {
		t.Fatalf("expected line 2, no code, got %+v", read[0])
	}
	if read[1].Line != 3 || !read[1].HasCode || read[1].Code != 7001 {
		t.Fatalf("expected line 3, code 7001, got %+v", read[1])
	}
}

// TestEditorViewSuppressesCoveredFires: a code-less marker covers a
// 7001 fire on its line and leaves an uncovered fire on another line.
func TestEditorViewSuppressesCoveredFires(t *testing.T) {
	text := "// @refinedts-expect-error\n" +
		"bad();\n" +
		"plain();\n"
	// offsets: line 2 starts at 28, line 3 at 35
	line2 := len("// @refinedts-expect-error\n")
	line3 := line2 + len("bad();\n")
	viewed := EditorView(text, []assignability.RefinementDiagnostic{fireAt(line2, 7001), fireAt(line3, 7002)})
	if len(viewed) != 1 {
		t.Fatalf("expected 1 surviving diagnostic, got %+v", viewed)
	}
	if viewed[0].Start != line3 {
		t.Fatalf("expected the surviving diagnostic at line 3's offset, got %+v", viewed[0])
	}
}

// TestCodeNarrowedMarkerLeavesDifferentCodeVisible: a marker naming
// 7001 does not cover a 7002 fire on the same line — the fire stays,
// and the unused expectation surfaces as its own 7005.
func TestCodeNarrowedMarkerLeavesDifferentCodeVisible(t *testing.T) {
	text := "bad(); // @refinedts-expect-error 7001\n"
	viewed := EditorView(text, []assignability.RefinementDiagnostic{fireAt(0, 7002)})
	if len(viewed) != 2 {
		t.Fatalf("expected the 7002 fire plus a stale 7005, got %+v", viewed)
	}
	codes := map[int]bool{}
	for _, d := range viewed {
		codes[d.Code] = true
	}
	if !codes[7002] || !codes[7005] {
		t.Fatalf("expected codes {7002, 7005}, got %+v", viewed)
	}
}

// TestCodeLessMarkerNeverSwallowsRTS7002: the defect fixture. A
// code-less marker sits over a line whose only diagnostic is a 7002 —
// the undetermined channel. The law (mirrored from
// packages/refinedpy/pyrefly/pyrefly/lib/refinedpy/markers.rs:11-12)
// is that no marker, coded or not, ever matches 7002: the 7002 must
// still surface, and the marker — having covered nothing — must
// report stale (its own 7005), which is the honest outcome, not a
// bug.
func TestCodeLessMarkerNeverSwallowsRTS7002(t *testing.T) {
	text := "// @refinedts-expect-error\n" +
		"undetermined();\n"
	line2 := len("// @refinedts-expect-error\n")
	viewed := EditorView(text, []assignability.RefinementDiagnostic{fireAt(line2, 7002)})
	sawUndetermined := false
	sawStale := false
	for _, d := range viewed {
		if d.Code == 7002 {
			sawUndetermined = true
		}
		if d.Code == 7005 {
			sawStale = true
		}
	}
	if !sawUndetermined {
		t.Fatalf("the 7002 must surface uncovered, got %+v", viewed)
	}
	if !sawStale {
		t.Fatalf("the marker covered nothing real, so it must report stale, got %+v", viewed)
	}
	if len(viewed) != 2 {
		t.Fatalf("expected exactly the 7002 plus the stale 7005, got %+v", viewed)
	}
}

// TestExplicitCode7002MarkerAlsoNeverMatches: pins the adopted rule.
// No fixture in this tree writes `@refinedts-expect-error 7002`
// (searched: refined-ts-go, tsc-vscode, refined-ts-typescript), so the
// ban is absolute — an explicit `7002` code on the marker does not
// carve out an exception, matching Python's markers.rs, whose matcher
// has no numeric-code narrowing at all and so already bans every
// marker shape from covering 7002.
func TestExplicitCode7002MarkerAlsoNeverMatches(t *testing.T) {
	text := "undetermined(); // @refinedts-expect-error 7002\n"
	viewed := EditorView(text, []assignability.RefinementDiagnostic{fireAt(0, 7002)})
	sawUndetermined := false
	sawStale := false
	for _, d := range viewed {
		if d.Code == 7002 {
			sawUndetermined = true
		}
		if d.Code == 7005 {
			sawStale = true
		}
	}
	if !sawUndetermined {
		t.Fatalf("an explicit 7002 code must not carve out an exception, got %+v", viewed)
	}
	if !sawStale {
		t.Fatalf("the explicit-code marker covered nothing, so it must report stale, got %+v", viewed)
	}
}

// TestStaleMarkerBecomes7005: a marker covering a line nothing fired
// on becomes its own 7005, anchored on the marker text.
func TestStaleMarkerBecomes7005(t *testing.T) {
	text := "fine();\n" +
		"also(); // @refinedts-expect-error\n"
	viewed := EditorView(text, nil)
	if len(viewed) != 1 {
		t.Fatalf("expected exactly 1 stale diagnostic, got %+v", viewed)
	}
	if viewed[0].Code != 7005 {
		t.Fatalf("expected code 7005, got %+v", viewed[0])
	}
	wantStart := indexFrom(text, expectErrorMarker, 0)
	if viewed[0].Start != wantStart {
		t.Fatalf("expected the 7005 anchored at %d, got %d", wantStart, viewed[0].Start)
	}
}

// TestFileWithoutMarkersPassesThrough: no markers means EditorView is
// a no-op.
func TestFileWithoutMarkersPassesThrough(t *testing.T) {
	fires := []assignability.RefinementDiagnostic{fireAt(0, 7001)}
	viewed := EditorView("plain();\n", fires)
	if len(viewed) != 1 || viewed[0].Code != fires[0].Code || viewed[0].Start != fires[0].Start {
		t.Fatalf("expected the fires to pass through untouched, got %+v", viewed)
	}
}

// TestStandaloneMarkerSkipsCommentLines: a standalone marker covers
// the next line that is not itself a comment-only line — one or more
// host-marker or explanatory comment lines may sit between the marker
// and the code it covers, matching markers.rs's own skip.
func TestStandaloneMarkerSkipsCommentLines(t *testing.T) {
	testCases := []struct {
		name     string
		text     string
		wantLine int
	}{
		{
			name: "one comment line between marker and code",
			text: "// @refinedts-expect-error\n" + // marker, line 1
				"// a host-marker line explaining the fire\n" + // line 2, skipped
				"bad();\n", // line 3, covered
			wantLine: 3,
		},
		{
			name: "two comment lines between marker and code",
			text: "// @refinedts-expect-error\n" + // marker, line 1
				"// first explanatory line\n" + // line 2, skipped
				"// second explanatory line\n" + // line 3, skipped
				"bad();\n", // line 4, covered
			wantLine: 4,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			read := ExpectationsOf(testCase.text)
			if len(read) != 1 {
				t.Fatalf("expected 1 expectation, got %+v", read)
			}
			if read[0].Line != testCase.wantLine {
				t.Fatalf("expected line %d, got %+v", testCase.wantLine, read[0])
			}
		})
	}
}

// TestProseMentionOfTheTokenIsNotAMarker: the measured defect. A
// marker is recognized only when @refinedts-expect-error is the first
// word of a comment's content, immediately after `//` or `/*` — never
// merely present somewhere inside a comment. Both measured
// false-positive shapes (docs/one-checker/marker-parity.md's first
// divergence row) are pinned here: a JSDoc header whose `*`-
// continuation line mentions the token mid-prose (the exact shape in
// language/edge-coverage/b-runners.ts and its three siblings), and a
// `//` line comment with prose before the token.
func TestProseMentionOfTheTokenIsNotAMarker(t *testing.T) {
	testCases := []struct {
		name string
		text string
	}{
		{
			name: "JSDoc block header mentioning the token mid-prose on a continuation line",
			text: "/**\n" +
				" * ONE-CHECKER.md the runner-word leg.\n" +
				" *\n" +
				" * An undetermined row also carries no marker —\n" +
				" * @refinedts-expect-error never matches RTS7002 — so the row is a\n" +
				" * bare unmarked call whose comment names the first blocking construct.\n" +
				" */\n" +
				"bad();\n",
		},
		{
			name: "a line comment with prose before the token",
			text: "// see @refinedts-expect-error for details\n" +
				"bad();\n",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			read := ExpectationsOf(testCase.text)
			if len(read) != 0 {
				t.Fatalf("expected no expectations from prose mentioning the token, got %+v", read)
			}
		})
	}
}

// TestReasonTextCapturedAndPrintedWhenStale: text after the marker
// (and its optional code) is the reason, ported from markers.rs's own
// reason capture — read here, and surfaced in the 7005 sentence when
// the marker goes stale.
func TestReasonTextCapturedAndPrintedWhenStale(t *testing.T) {
	testCases := []struct {
		name       string
		text       string
		wantReason string
	}{
		{
			name:       "standalone marker with reason, no code",
			text:       "// @refinedts-expect-error narrows on the callee's return type\n" + "bad();\n",
			wantReason: "narrows on the callee's return type",
		},
		{
			name:       "trailing marker with reason after a code",
			text:       "bad(); // @refinedts-expect-error 7001 narrows on the callee's return type\n",
			wantReason: "narrows on the callee's return type",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			read := ExpectationsOf(testCase.text)
			if len(read) != 1 {
				t.Fatalf("expected 1 expectation, got %+v", read)
			}
			if read[0].Reason != testCase.wantReason {
				t.Fatalf("expected reason %q, got %+v", testCase.wantReason, read[0])
			}
			// nothing fires, so the marker goes stale and its 7005
			// sentence must carry the reason text.
			viewed := EditorView(testCase.text, nil)
			if len(viewed) != 1 || viewed[0].Code != 7005 {
				t.Fatalf("expected exactly 1 stale 7005, got %+v", viewed)
			}
			if !strings.Contains(viewed[0].MessageText, testCase.wantReason) {
				t.Fatalf("expected the stale sentence to carry the reason %q, got %q",
					testCase.wantReason, viewed[0].MessageText)
			}
		})
	}
}
