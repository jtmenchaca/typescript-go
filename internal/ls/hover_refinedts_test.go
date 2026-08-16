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
