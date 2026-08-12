// Ported 1:1 from service/corpus_manifest.test.ts.
package service

import "testing"

func TestSourceFiles_TsTsxOutsideTestsAndBuildOutput(t *testing.T) {
	mustBool(t, SweepSourceFile("packages/core/scanner.ts"), true)
	mustBool(t, SweepSourceFile("src/App.tsx"), true)
	mustBool(t, SweepSourceFile("src/util.d.ts"), false)
	mustBool(t, SweepSourceFile("src/util.spec.ts"), false)
	mustBool(t, SweepSourceFile("src/util.test.tsx"), false)
	mustBool(t, SweepSourceFile("node_modules/x/index.ts"), false)
	mustBool(t, SweepSourceFile("a/dist/out.ts"), false)
	mustBool(t, SweepSourceFile("a/test/helper.ts"), false)
	mustBool(t, SweepSourceFile("a/__tests__/helper.ts"), false)
	mustBool(t, SweepSourceFile("a/protest/march.ts"), true)
	mustBool(t, SweepSourceFile("readme.md"), false)
}

func TestFixtureCorpora_KeepFilesUnderFixtures(t *testing.T) {
	mustBool(t, SourceFileOf(CorpusTutorial, "packages/refinedts/tsc-vscode/fixtures/tutorial/01-user-age.ts"), true)
	mustBool(t, SweepSourceFile("packages/refinedts/tsc-vscode/fixtures/tutorial/01-user-age.ts"), false)
	mustBool(t, FixtureSourceFile("a/dist/out.ts"), false)
}

func TestEveryNamedCorpus_HasRootAndOutcomesPath(t *testing.T) {
	for name, row := range Corpora {
		if len(row.Root) == 0 {
			t.Errorf("corpus %s: root is empty", name)
		}
		if !hasSuffix(row.Outcomes, ".json") {
			t.Errorf("corpus %s: outcomes %q does not end with .json", name, row.Outcomes)
		}
	}
}

func mustBool(t *testing.T, got, want bool) {
	t.Helper()
	if got != want {
		t.Errorf("got %v, want %v", got, want)
	}
}
