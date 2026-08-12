// Named corpora and the one predicate that counts a source file.
// Coverage and sweep share this file — a TOTAL line's file count is
// the manifest's count for that corpus, not a hand-typed number.
//
// Ported 1:1 from service/corpus_manifest.ts.
package service

import "regexp"

// Corpus is the TS Corpus interface.
type Corpus struct {
	Name string
	Root string
	// Outcomes is where `--save` writes the per-row outcomes JSON.
	Outcomes string
}

// CorpusName is the TS CorpusName (keyof typeof CORPORA).
type CorpusName string

const (
	CorpusTutorial CorpusName = "tutorial"
	CorpusExamples CorpusName = "examples"
	CorpusLanguage CorpusName = "language"
	CorpusRecharts CorpusName = "recharts"
	CorpusNest     CorpusName = "nest"
	CorpusZod      CorpusName = "zod"
)

// Corpora is the TS CORPORA object.
var Corpora = map[CorpusName]Corpus{
	CorpusTutorial: {
		Name:     "tutorial",
		Root:     "packages/refinedts/tsc-vscode/fixtures/tutorial",
		Outcomes: "packages/refinedts/refined-ts-typescript/conformance/outcomes/tutorial.json",
	},
	CorpusExamples: {
		Name:     "examples",
		Root:     "packages/refinedts/tsc-vscode/fixtures/examples",
		Outcomes: "packages/refinedts/refined-ts-typescript/conformance/outcomes/examples.json",
	},
	CorpusLanguage: {
		Name:     "language",
		Root:     "packages/refinedts/tsc-vscode/fixtures/language",
		Outcomes: "packages/refinedts/refined-ts-typescript/conformance/outcomes/language.json",
	},
	CorpusRecharts: {
		Name:     "recharts",
		Root:     "tmp/recharts-src",
		Outcomes: "packages/refinedts/refined-ts-typescript/conformance/outcomes/recharts.json",
	},
	CorpusNest: {
		Name:     "nest",
		Root:     "tmp/nest-src",
		Outcomes: "packages/refinedts/refined-ts-typescript/conformance/outcomes/nest.json",
	},
	CorpusZod: {
		Name:     "zod",
		Root:     "tmp/zod-src",
		Outcomes: "packages/refinedts/refined-ts-typescript/conformance/outcomes/zod.json",
	},
}

var specOrTestSuffix = regexp.MustCompile(`\.(spec|test)\.tsx?$`)
var sweepExcludedDir = regexp.MustCompile(`(^|/)(node_modules|dist|build|coverage|__tests__|__mocks__|test|tests|fixtures)/`)
var fixtureExcludedDir = regexp.MustCompile(`(^|/)(node_modules|dist|build)/`)

// SweepSourceFile reports whether a path names a SOURCE file a sweep
// or coverage corpus should judge: .ts or .tsx, not a declaration,
// not a test or spec, not build output or dependencies. One
// predicate, shared.
func SweepSourceFile(path string) bool {
	if !hasSuffix(path, ".ts") && !hasSuffix(path, ".tsx") {
		return false
	}
	if hasSuffix(path, ".d.ts") {
		return false
	}
	if specOrTestSuffix.MatchString(path) {
		return false
	}
	if sweepExcludedDir.MatchString(path) {
		return false
	}
	return true
}

// FixtureSourceFile is for fixture corpora, which include files under
// `fixtures/` — the sweep predicate would drop them. Coverage of the
// tutorial/examples/language trees uses this instead.
func FixtureSourceFile(path string) bool {
	if !hasSuffix(path, ".ts") && !hasSuffix(path, ".tsx") {
		return false
	}
	if hasSuffix(path, ".d.ts") {
		return false
	}
	if specOrTestSuffix.MatchString(path) {
		return false
	}
	if fixtureExcludedDir.MatchString(path) {
		return false
	}
	return true
}

// SourceFileOf dispatches to FixtureSourceFile or SweepSourceFile
// depending on the corpus.
func SourceFileOf(corpus CorpusName, path string) bool {
	if corpus == CorpusTutorial || corpus == CorpusExamples || corpus == CorpusLanguage {
		return FixtureSourceFile(path)
	}
	return SweepSourceFile(path)
}

func hasSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}
