// Full-fixture reproduction: the SAME e-class-and-function.ts and
// c-reads-and-values.ts text the judge reads, run through Check() —
// every sibling function registers its contract exactly as it does
// under the real judge run, unlike syntax_wave_kernel_divergence_test.go's
// minimal two-function files. AGENT-BRIEF.md's "syntax-wave facts"
// section is explicit that a row can pass in an isolated single-function
// walk test and still fail once every sibling row's contract shares the
// same file's registries and summary/memo stores — this file is what
// catches that class of cross-function interference, if it exists.
//
// The fixture's own import line names a relative path
// (../../../../refined-ts-typescript/surface/z.ts) meant for its home
// directory under tsc-vscode/fixtures/; here it is rewritten to the
// in-memory /surface/z.ts ProgramFromSource already serves, and nothing
// else in the file is touched — AGENT-BRIEF.md's "do not edit fixtures"
// rule is about the checked-in file on disk, never touched by this
// test, which only reads it and edits an in-memory copy's first line.

package service

import (
	"os"
	"regexp"
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

const syntaxCoverageFixtureDir = "../../../../tsc-vscode/fixtures/language/syntax-coverage"

var fixtureImportLine = regexp.MustCompile(`(?m)^import \* as z from "[^"]+";\n`)

// fixtureSourceRewritten reads one syntax-coverage fixture file and
// rewrites its own relative surface import to the in-memory
// "/surface/z.ts" path ProgramFromSource serves — the only edit; every
// function, class, and expect-error marker rides through verbatim.
func fixtureSourceRewritten(t *testing.T, filename string) string {
	t.Helper()
	text, err := os.ReadFile(syntaxCoverageFixtureDir + "/" + filename)
	if err != nil {
		t.Fatalf("reading fixture %s: %v", filename, err)
	}
	rewritten := fixtureImportLine.ReplaceAllString(string(text), "import * as z from \"/surface/z.ts\";\n")
	if rewritten == string(text) {
		t.Fatalf("fixture %s: the import-line rewrite matched nothing — the fixture's own import spelling moved", filename)
	}
	return rewritten
}

// diagnosticAt finds every reported refinement whose Start offset falls
// WITHIN the marker string's own span — marker is always the full
// statement text (e.g. "const good: Age = new Sealed(40).years();"),
// so a diagnostic reported anywhere on that statement's own tokens
// (the declaration, the call, the argument) lands inside
// [pos, pos+len(marker)). This used to be a fixed [pos, pos+200)
// window, wide enough to reach past the statement's own closing `;`
// into the NEXT statement — every one of these fixture rows immediately
// follows its "good" leg with the deliberately out-of-set "over" leg
// (a `@refinedts-expect-error` marker of its own), so the over leg's
// own CORRECT 7001 fire sat inside the good leg's 200-byte window and
// was misattributed to it, reporting a false divergence on a row the
// judge already determines cleanly.
func diagnosticsBetween(result CheckResult, source string, marker string) []int {
	var codes []int
	at := indexOfAll(source, marker)
	for _, d := range result.Refinements {
		for _, pos := range at {
			if d.Start >= pos && d.Start < pos+len(marker) {
				codes = append(codes, d.Code)
			}
		}
	}
	return codes
}

func indexOfAll(source, marker string) []int {
	var out []int
	from := 0
	for {
		idx := indexFromPlain(source, marker, from)
		if idx < 0 {
			break
		}
		out = append(out, idx)
		from = idx + len(marker)
	}
	return out
}

func indexFromPlain(source, marker string, from int) int {
	if from > len(source) {
		return -1
	}
	rel := indexPlain(source[from:], marker)
	if rel < 0 {
		return -1
	}
	return from + rel
}

func indexPlain(haystack, needle string) int {
	n := len(needle)
	for i := 0; i+n <= len(haystack); i++ {
		if haystack[i:i+n] == needle {
			return i
		}
	}
	return -1
}

// TestFullFixture_EClassAndFunction_OverloadedCallAndRestParameter runs
// the REAL e-class-and-function.ts (every sibling row registered
// alongside pickYears/firstAge) through Check() and checks the two
// good legs the brief names as rows 2 and 3: overloadedCall's
// pickYears(40) and restParameter's firstAge(40, 41). Both must
// determine silently; a 7001/7002 anywhere near either call site is
// the divergence the brief describes.
func TestFullFixture_EClassAndFunction_OverloadedCallAndRestParameter(t *testing.T) {
	if !kernelbridge.KernelArtifactsPresent(kernelbridge.DylibPath) {
		t.Skip("native kernel dylib not built")
	}
	source := fixtureSourceRewritten(t, "e-class-and-function.ts")
	result := checkFor(t, source)

	overloadedGoodCodes := diagnosticsBetween(result, source, "const good: Age = pickYears(40);")
	if len(overloadedGoodCodes) != 0 {
		t.Errorf("overloadedCall's good leg (pickYears(40)) reported codes %v against the full fixture — want none", overloadedGoodCodes)
	}
	restGoodCodes := diagnosticsBetween(result, source, "const good: Age = firstAge(40, 41);")
	if len(restGoodCodes) != 0 {
		t.Errorf("restParameter's good leg (firstAge(40, 41)) reported codes %v against the full fixture — want none", restGoodCodes)
	}
}

// TestFullFixture_EClassAndFunction_PrivateFieldThroughConstructor runs
// the REAL e-class-and-function.ts through Check() and checks
// privateFieldThroughConstructor's good leg: `new Sealed(40).years()`,
// a private field (`#age`) written once in the constructor and read
// back through a plain method. Before the SpelledNameOf/
// propertyPathReading/fieldAccessOf fix (AGENT-BRIEF.md's syntax-wave
// facts), the private name broke the summary-lowering census, marking
// the whole `this` bundle Escaped and leaving years() porous.
func TestFullFixture_EClassAndFunction_PrivateFieldThroughConstructor(t *testing.T) {
	if !kernelbridge.KernelArtifactsPresent(kernelbridge.DylibPath) {
		t.Skip("native kernel dylib not built")
	}
	source := fixtureSourceRewritten(t, "e-class-and-function.ts")
	result := checkFor(t, source)

	sealedGoodCodes := diagnosticsBetween(result, source, "const good: Age = new Sealed(40).years();")
	if len(sealedGoodCodes) != 0 {
		t.Errorf("privateFieldThroughConstructor's good leg (new Sealed(40).years()) reported codes %v against the full fixture — want none", sealedGoodCodes)
	}
}

// TestFullFixture_CReadsAndValues_ArraySort runs the REAL
// c-reads-and-values.ts through Check() and checks arraySort's good
// leg: `const good: Age = ages[0];` after `[41, 40].sort()`. This file
// carries ~100 sibling functions, so it is the sharpest test of
// whether row 1's divergence needs the full-file contract/summary
// registry population the isolated walk test never builds.
func TestFullFixture_CReadsAndValues_ArraySort(t *testing.T) {
	if !kernelbridge.KernelArtifactsPresent(kernelbridge.DylibPath) {
		t.Skip("native kernel dylib not built")
	}
	source := fixtureSourceRewritten(t, "c-reads-and-values.ts")
	result := checkFor(t, source)

	sortGoodCodes := diagnosticsBetween(result, source, "const good: Age = ages[0];\n  void good;\n  const overs = [201, 200];")
	if len(sortGoodCodes) != 0 {
		t.Errorf("arraySort's good leg (ages[0] after sort()) reported codes %v against the full fixture — want none", sortGoodCodes)
	}
}

// TestFullFixture_BBodyExpressions_ThisFieldRead runs the REAL
// b-body-expressions.ts through Check() and checks thisFieldRead's
// good leg: `new ThisPerson().years()` reading a plain field
// initializer through `this.age`.
func TestFullFixture_BBodyExpressions_ThisFieldRead(t *testing.T) {
	if !kernelbridge.KernelArtifactsPresent(kernelbridge.DylibPath) {
		t.Skip("native kernel dylib not built")
	}
	source := fixtureSourceRewritten(t, "b-body-expressions.ts")
	result := checkFor(t, source)

	thisFieldCodes := diagnosticsBetween(result, source, "const ok: Age = new ThisPerson().years();")
	if len(thisFieldCodes) != 0 {
		t.Errorf("thisFieldRead's good leg (new ThisPerson().years()) reported codes %v against the full fixture — want none", thisFieldCodes)
	}
}

// TestFullFixture_IMoreExpressions_PrivateAccessorRead runs the REAL
// i-more-expressions.ts through Check() and checks
// privateAccessorSlotLaidOut's good leg: a private get accessor over a
// backing field, read through a method.
func TestFullFixture_IMoreExpressions_PrivateAccessorRead(t *testing.T) {
	if !kernelbridge.KernelArtifactsPresent(kernelbridge.DylibPath) {
		t.Skip("native kernel dylib not built")
	}
	source := fixtureSourceRewritten(t, "i-more-expressions.ts")
	result := checkFor(t, source)

	accessorCodes := diagnosticsBetween(result, source, "const good: Age = new PrivateAccessorHolder().read();")
	if len(accessorCodes) != 0 {
		t.Errorf("privateAccessorSlotLaidOut's good leg reported codes %v against the full fixture — want none", accessorCodes)
	}
}

// TestFullFixture_IMoreExpressions_ArrayAndRestIdentifierParameters runs
// the REAL i-more-expressions.ts through Check() and checks the last two
// of the four TOP-ret-mislabeled-as-determined rows: arrayTypedParameter's
// ".len"/".elem" pair and restIdentifierParameter's rest tuple both feed
// applySummary a TOP ret (summaryEntryStates' own always-TOP fill for
// these two parameter shapes) — kernel_summaries.go's applySummary built
// the KnownStateWire it fed KnownOfState from returned.Set/.Absent/.Nan
// alone, never copying returned.Top across, so KnownOfState's own `if
// s.Top: return silence.Residue()` gate never fired and a TOP ret read as
// KindSet over an EMPTY RefinedSet (FormatForDiagnostics' own "any
// value" spelling for zero Forms) instead of KindUnknown — which is what
// the declarationHasRestParameter/declarationHasArrayParameter EXACT
// decline needs to see to hand the call to the walk-based recovery
// instead of serving this fabricated value.
func TestFullFixture_IMoreExpressions_ArrayAndRestIdentifierParameters(t *testing.T) {
	if !kernelbridge.KernelArtifactsPresent(kernelbridge.DylibPath) {
		t.Skip("native kernel dylib not built")
	}
	source := fixtureSourceRewritten(t, "i-more-expressions.ts")
	result := checkFor(t, source)

	arrayGoodCodes := diagnosticsBetween(result, source, "const good: Age = arrayTypedParameter([40, 41]);")
	if len(arrayGoodCodes) != 0 {
		t.Errorf("arrayTypedParameterRefines's good leg (arrayTypedParameter([40, 41])) reported codes %v against the full fixture — want none", arrayGoodCodes)
	}
	restGoodCodes := diagnosticsBetween(result, source, "const good: Age = restIdentifierParameter(40, 41);")
	if len(restGoodCodes) != 0 {
		t.Errorf("restIdentifierParameterRefines's good leg (restIdentifierParameter(40, 41)) reported codes %v against the full fixture — want none", restGoodCodes)
	}
}
