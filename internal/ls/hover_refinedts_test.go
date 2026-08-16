// The splice rule is the plugin's rendering contract: a brace-opening
// spelling appends after the host type; anything else replaces the
// right-hand side after the last `=` or `:`.

package ls

import "testing"

func TestSpliceRefinementSpellingAppend(t *testing.T) {
	// a brace-opening spelling is a suffix — it appends after the
	// host type, never replaces it
	got := spliceRefinementSpelling("const total: number", "{integer, 0 ≤ 𝑥 ≤ 100}")
	if got != "const total: number {integer, 0 ≤ 𝑥 ≤ 100}" {
		t.Fatalf("unexpected splice %q", got)
	}
}

func TestSpliceRefinementSpellingReplace(t *testing.T) {
	got := spliceRefinementSpelling("type When = string", "Temporal.PlainDate {2020-01-01 … 2020-12-31}")
	want := "type When = Temporal.PlainDate {2020-01-01 … 2020-12-31}"
	if got != want {
		t.Fatalf("expected the right-hand side replaced, got %q", got)
	}
}

func TestSpliceRefinementSpellingEmpty(t *testing.T) {
	if got := spliceRefinementSpelling("const x: number", ""); got != "const x: number" {
		t.Fatalf("an empty spelling must not touch the line, got %q", got)
	}
}

// TestSpliceRefinementSpellingBareFunctionDropped pins the fix: a
// function-name hover's own signature already says it is a function,
// so the bare "{a function}" spelling — no refinement past the sort —
// adds nothing and must not append.
func TestSpliceRefinementSpellingBareFunctionDropped(t *testing.T) {
	quickInfo := "function after(t: Cutoff): number"
	got := spliceRefinementSpelling(quickInfo, "{a function}")
	if got != quickInfo {
		t.Fatalf("expected the bare function spelling dropped, got %q", got)
	}
}

// TestSpliceRefinementSpellingFunctionWithRefinementKept guards the
// other side of the same rule: a spelling that carries refinement
// content BESIDE the sort word (here, possible absence) still
// appends — only the exact bare spelling is dropped.
func TestSpliceRefinementSpellingFunctionWithRefinementKept(t *testing.T) {
	quickInfo := "const maybeAfter: ((t: Cutoff) => number) | undefined"
	got := spliceRefinementSpelling(quickInfo, "{a function, or absent}")
	want := quickInfo + " {a function, or absent}"
	if got != want {
		t.Fatalf("expected the refinement-bearing spelling to append, got %q", got)
	}
}
