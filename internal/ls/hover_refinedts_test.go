// The splice rule is the plugin's rendering contract: a brace-opening
// spelling appends after the host type; anything else replaces the
// right-hand side after the last `=` or `:`. When the host's
// right-hand side states no claim of its own ("any"/"unknown") and
// the sort word is known, the rendering is TWO lines: the sort word
// replaces the host's non-claim, and a second line notes the host's
// original type.

package ls

import "testing"

func TestSpliceRefinementSpellingAppend(t *testing.T) {
	// a brace-opening spelling is a suffix — it appends after the
	// host type, never replaces it
	got, note := spliceRefinementSpelling("const total: number", "{integer, 0 ≤ 𝑥 ≤ 100}", "number")
	if got != "const total: number {integer, 0 ≤ 𝑥 ≤ 100}" {
		t.Fatalf("unexpected splice %q", got)
	}
	if note != "" {
		t.Fatalf("an ordinary append must carry no note, got %q", note)
	}
}

func TestSpliceRefinementSpellingReplace(t *testing.T) {
	got, note := spliceRefinementSpelling("type When = string", "Temporal.PlainDate {2020-01-01 … 2020-12-31}", "")
	want := "type When = Temporal.PlainDate {2020-01-01 … 2020-12-31}"
	if got != want {
		t.Fatalf("expected the right-hand side replaced, got %q", got)
	}
	if note != "" {
		t.Fatalf("an ordinary replace must carry no note, got %q", note)
	}
}

func TestSpliceRefinementSpellingEmpty(t *testing.T) {
	got, note := spliceRefinementSpelling("const x: number", "", "number")
	if got != "const x: number" {
		t.Fatalf("an empty spelling must not touch the line, got %q", got)
	}
	if note != "" {
		t.Fatalf("an empty spelling must carry no note, got %q", note)
	}
}

// TestSpliceRefinementSpellingBareFunctionDropped pins the fix: a
// function-name hover's own signature already says it is a function,
// so the bare "{a function}" spelling — no refinement past the sort —
// adds nothing and must not append.
func TestSpliceRefinementSpellingBareFunctionDropped(t *testing.T) {
	quickInfo := "function after(t: Cutoff): number"
	got, note := spliceRefinementSpelling(quickInfo, "{a function}", "")
	if got != quickInfo {
		t.Fatalf("expected the bare function spelling dropped, got %q", got)
	}
	if note != "" {
		t.Fatalf("a dropped spelling must carry no note, got %q", note)
	}
}

// TestSpliceRefinementSpellingFunctionWithRefinementKept guards the
// other side of the same rule: a spelling that carries refinement
// content BESIDE the sort word (here, possible absence) still
// appends — only the exact bare spelling is dropped.
func TestSpliceRefinementSpellingFunctionWithRefinementKept(t *testing.T) {
	quickInfo := "const maybeAfter: ((t: Cutoff) => number) | undefined"
	got, note := spliceRefinementSpelling(quickInfo, "{a function, or absent}", "")
	want := quickInfo + " {a function, or absent}"
	if got != want {
		t.Fatalf("expected the refinement-bearing spelling to append, got %q", got)
	}
	if note != "" {
		t.Fatalf("a suffix rendering must carry no note, got %q", note)
	}
}

// TestSpliceRefinementSpellingAnyHostReplacedWithSortWordAndNote pins
// the ruled two-line rendering: the host states "any" — no claim of
// its own — so the sort word REPLACES it, and the original quickInfo
// comes back as the note for the caller to render beneath, verbatim.
func TestSpliceRefinementSpellingAnyHostReplacedWithSortWordAndNote(t *testing.T) {
	quickInfo := "const level: any"
	got, note := spliceRefinementSpelling(quickInfo, "{0 ≤ 𝑥 ≤ 1}", "number")
	if got != "const level: number {0 ≤ 𝑥 ≤ 1}" {
		t.Fatalf("expected the sort word to replace the any right-hand side, got %q", got)
	}
	if note != quickInfo {
		t.Fatalf("expected the note to carry the host's original quickInfo verbatim, got %q", note)
	}
}

// TestSpliceRefinementSpellingUnknownHostReplacedWithSortWordAndNote
// is the same rule over "unknown" rather than "any".
func TestSpliceRefinementSpellingUnknownHostReplacedWithSortWordAndNote(t *testing.T) {
	quickInfo := "const raw: unknown"
	got, note := spliceRefinementSpelling(quickInfo, "{a nonempty string}", "string")
	if got != "const raw: string {a nonempty string}" {
		t.Fatalf("expected the sort word to replace the unknown right-hand side, got %q", got)
	}
	if note != quickInfo {
		t.Fatalf("expected the note to carry the host's original quickInfo verbatim, got %q", note)
	}
}

// TestSpliceRefinementSpellingAnyHostNoSortWordStaysSingleLine guards
// the gate: an "any" host with no known sort word (sortWord == "")
// keeps the ordinary single-line replace — the two-line rendering
// only fires when the sort word is known. Temporal.PlainDate is a
// replace-style (non-brace-opening) spelling, so this genuinely
// exercises the sortWord=="" branch of the any-host gate rather than
// being intercepted earlier by the bare-function drop.
func TestSpliceRefinementSpellingAnyHostNoSortWordStaysSingleLine(t *testing.T) {
	quickInfo := "const level: any"
	got, note := spliceRefinementSpelling(quickInfo, "Temporal.PlainDate {2020-01-01 … 2020-12-31}", "")
	want := "const level: Temporal.PlainDate {2020-01-01 … 2020-12-31}"
	if got != want {
		t.Fatalf("expected the ordinary single-line replace with no sort word, got %q", got)
	}
	if note != "" {
		t.Fatalf("no sort word must carry no note, got %q", note)
	}
}
