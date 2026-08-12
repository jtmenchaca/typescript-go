// Interface tests for the retired-word gate. Ported from
// service/retired_words.test.ts.
//
// The TS suite's second test has two halves: RetiredIdentifiersFrom
// against the real VOCABULARY.md (ported below), and a whole-tree
// walk of refined-ts-typescript/*.ts asserting no retired identifier
// reappears in live TS source. That second half checks the TS
// source tree's own hygiene — it has no Go-shaped twin (this package
// does not own that tree, and unlike silence/unknown_test.go's
// three concrete function bodies, there is nothing here for a
// functional Go equivalent to assert against) — same precedent as
// silence/unknown_test.go's own banner. Left unported.
package service

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRetiredIdentifiersFrom_CamelCaseAndConstantCase_NotEnglish(t *testing.T) {
	markdown := `
| retired | now |
|---|---|
| judge / judgeAgainst / judgeDependentEdge | checkAssignability / checkAssignabilityAgainst |
| declaredFunctionBindings / callbackCallBindings | callSiteBindings |
| CALLBACK_PARAM_METHODS / CALLBACK_METHODS | ARRAY_CALLBACK_METHODS |
| ` + "`Stated`" + ` (the type) | ` + "`DeclaredRefinement`" + ` |
| evalExpression | evaluateExpression |
`
	ids := RetiredIdentifiersFrom(markdown)
	mustContain(t, ids, "judgeAgainst")
	mustContain(t, ids, "declaredFunctionBindings")
	mustContain(t, ids, "callbackCallBindings")
	mustContain(t, ids, "CALLBACK_PARAM_METHODS")
	mustContain(t, ids, "CALLBACK_METHODS")
	mustContain(t, ids, "evalExpression")
	mustNotContain(t, ids, "judge")
	mustNotContain(t, ids, "Stated")
}

func TestLiveCheckerDoesNotReviveVocabularyRetiredIdentifiers(t *testing.T) {
	vocabularyPath := vocabularyMdPath(t)
	data, err := os.ReadFile(vocabularyPath)
	if err != nil {
		t.Fatalf("reading VOCABULARY.md: %v", err)
	}
	retired := RetiredIdentifiersFrom(string(data))
	mustHave(t, retired, "callbackCallBindings", true)
	mustHave(t, retired, "declaredFunctionBindings", true)
	mustHave(t, retired, "knownMaybe", true)
	mustHave(t, retired, "dialectOfNode", true)
	mustHave(t, retired, "dialectNamed", true)
	mustHave(t, retired, "DIALECTS", true)
	mustHave(t, retired, "spellSetKey", true)
	mustHave(t, retired, "isRefusal", true)
	mustHave(t, retired, "judge", false)
	mustHave(t, retired, "Stated", false)
	mustHave(t, retired, "Dialect", false)
	mustHave(t, retired, "Known", false)
	mustHave(t, retired, "grade", false)
	mustHave(t, retired, "display", false)

	// The tree-walk half (retiredHitsIn over every live refined-ts-typescript/*.ts
	// file) is not ported — see the file header banner.
}

// vocabularyMdPath finds packages/refinedts/VOCABULARY.md from this
// test's own file location (the TS test used
// `new URL("../../VOCABULARY.md", import.meta.url)` from
// refined-ts-typescript/service/).
func vocabularyMdPath(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	dir := wd
	for range 12 {
		candidate := filepath.Join(dir, "VOCABULARY.md")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("could not locate packages/refinedts/VOCABULARY.md above %s", wd)
	return ""
}

func mustContain(t *testing.T, ids []string, want string) {
	t.Helper()
	for _, id := range ids {
		if id == want {
			return
		}
	}
	t.Errorf("expected %v to contain %q", ids, want)
}

func mustNotContain(t *testing.T, ids []string, unwanted string) {
	t.Helper()
	for _, id := range ids {
		if id == unwanted {
			t.Errorf("expected %v not to contain %q", ids, unwanted)
			return
		}
	}
}

func mustHave(t *testing.T, ids []string, id string, want bool) {
	t.Helper()
	got := false
	for _, v := range ids {
		if v == id {
			got = true
			break
		}
	}
	if got != want {
		t.Errorf("retired.includes(%q) = %v, want %v", id, got, want)
	}
}
