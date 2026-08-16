package walk

import (
	"reflect"
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// The two survey patterns from
// refined-ts-typescript/findings/surveys/vite/20847-css-url-token.ts:
// Vite's buggy regex (a path can be truncated at an escaped ')') and
// its fixed regex (the escaped-close-paren case reads through).
const (
	viteBuggyPattern = `url\(([^'")]+)\)`
	viteFixedPattern = `url\(((?:\\.|[^'")\\])+)\)`
	// UrlToken's own stated pattern (z.string().regex(...) in the
	// survey file) — extractAfter's capture group should compile to
	// EXACTLY this set once anchored, not merely a subset of it.
	urlTokenPattern = `(?:\\.|[^'")\\])+`
)

func TestCaptureGroupSourceSpansOnTheSurveyPatterns(t *testing.T) {
	t.Run("the buggy regex has one capturing group, never optional", func(t *testing.T) {
		spans, ok := captureGroupSourceSpans(viteBuggyPattern)
		if !ok {
			t.Fatalf("expected the pattern to read as balanced")
		}
		if len(spans) != 1 {
			t.Fatalf("expected one capturing group, got %d", len(spans))
		}
		if spans[0].optional {
			t.Errorf("group 1 sits under a + quantifier applied to its OWN plus a fixed +group wrapper — " +
				"not a zero-admitting one, no lookaround, no enclosing alternation — so it should never be optional")
		}
		runes := []rune(viteBuggyPattern)
		got := string(runes[spans[0].innerStart:spans[0].innerEnd])
		want := `[^'")]+`
		if got != want {
			t.Errorf("group 1's inner source = %q, want %q", got, want)
		}
	})

	t.Run("the fixed regex has one capturing group, never optional", func(t *testing.T) {
		spans, ok := captureGroupSourceSpans(viteFixedPattern)
		if !ok {
			t.Fatalf("expected the pattern to read as balanced")
		}
		if len(spans) != 1 {
			t.Fatalf("expected one capturing group, got %d", len(spans))
		}
		if spans[0].optional {
			t.Errorf("group 1 wraps (?:\\\\.|[^'\")\\\\])+ with no zero-admitting quantifier on the " +
				"group itself, no lookaround, and the | inside belongs to the NESTED non-capturing " +
				"group, not group 1's own scope — so it should never be optional")
		}
		runes := []rune(viteFixedPattern)
		got := string(runes[spans[0].innerStart:spans[0].innerEnd])
		want := `(?:\\.|[^'")\\])+`
		if got != want {
			t.Errorf("group 1's inner source = %q, want %q", got, want)
		}
	})
}

func TestCaptureGroupSetOfCompilesThroughTheSameDoorAsDotRegex(t *testing.T) {
	t.Run("the buggy group's language is a REAL superset of UrlToken, not equal to it", func(t *testing.T) {
		groupSet, compiled, optional := captureGroupSetOf(viteBuggyPattern, "", 1)
		if !compiled {
			t.Fatalf("expected [^'\")]+ to compile under the format grammar")
		}
		if optional {
			t.Errorf("group 1 should not be optional")
		}
		urlToken := refinementsets.FormatGrammar("^"+urlTokenPattern+"$", "")
		if !urlToken.Ok {
			t.Fatalf("expected UrlToken's own pattern to compile: %s", urlToken.Unsupported)
		}
		// the buggy group admits a trailing lone backslash (\ is not in
		// the excluded set [^'")]) that UrlToken's pattern does not admit
		// (every unit there is either an escaped pair \\. or a
		// non-backslash exclusion) — the two compiled sets must NOT be
		// the same set, or this test is not exercising the honesty gap
		// the survey file's header describes
		if reflect.DeepEqual(groupSet, urlToken.Set) {
			t.Errorf("the buggy group's compiled set should differ from UrlToken's " +
				"(it admits strings UrlToken excludes, e.g. a trailing lone backslash)")
		}
	})

	t.Run("the fixed group's language is EXACTLY UrlToken's own set", func(t *testing.T) {
		groupSet, compiled, optional := captureGroupSetOf(viteFixedPattern, "", 1)
		if !compiled {
			t.Fatalf("expected (?:\\\\.|[^'\")\\\\])+ to compile under the format grammar")
		}
		if optional {
			t.Errorf("group 1 should not be optional")
		}
		urlToken := refinementsets.FormatGrammar("^"+urlTokenPattern+"$", "")
		if !urlToken.Ok {
			t.Fatalf("expected UrlToken's own pattern to compile: %s", urlToken.Unsupported)
		}
		// group 1's own source IS urlTokenPattern verbatim, so the two
		// compiled sets (same door, same anchoring, same flags) must be
		// structurally identical — the subset question the survey file
		// cares about is in fact equality
		if !reflect.DeepEqual(groupSet, urlToken.Set) {
			t.Errorf("the fixed group's compiled set should equal UrlToken's compiled set exactly\ngot:  %#v\nwant: %#v",
				groupSet, urlToken.Set)
		}
	})

	t.Run("an out-of-range group index answers not-compiled", func(t *testing.T) {
		if _, compiled, _ := captureGroupSetOf(viteFixedPattern, "", 2); compiled {
			t.Errorf("there is only one capturing group; index 2 should not compile")
		}
	})
}
