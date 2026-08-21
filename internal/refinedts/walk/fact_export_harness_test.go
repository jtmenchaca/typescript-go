// Ports the harness recognizer against real parsed sources — the same
// discipline foreign_edge_test.go states for its own syntactic halves:
// no checker, no program, a throwaway source file parsed straight from
// text.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/parser"
)

// harnessSourceFileOf parses a whole file's text (not one expression,
// unlike switchExprOf) since HarnessCallOf reads top-level statements
// including import declarations.
func harnessSourceFileOf(t *testing.T, source string) *ast.SourceFile {
	t.Helper()
	opts := ast.SourceFileParseOptions{FileName: "/harness.ts", Path: "/harness.ts"}
	return parser.ParseSourceFile(opts, source, core.ScriptKindTS)
}

func TestHarnessCallOf_TheExactShapeNamesTheFunction(t *testing.T) {
	file := harnessSourceFileOf(t, `
import { readFileSync } from "node:fs";
function audioLevel(samples) { return samples.length; }
console.log(JSON.stringify(audioLevel(JSON.parse(readFileSync(0, "utf8")))));
`)
	name, shape, _, ok := HarnessCallOf(file)
	if !ok {
		t.Fatalf("the exact shape did not recognize")
	}
	if name != "audioLevel" {
		t.Errorf("got name %q, want %q", name, "audioLevel")
	}
	if shape != HarnessShapeStdinJSON {
		t.Errorf("got shape %v, want HarnessShapeStdinJSON", shape)
	}
}

func TestHarnessCallOf_NamespaceImportOfFsRecognizes(t *testing.T) {
	file := harnessSourceFileOf(t, `
import * as fs from "node:fs";
function audioLevel(samples) { return samples.length; }
console.log(JSON.stringify(audioLevel(JSON.parse(fs.readFileSync(0, "utf8")))));
`)
	name, shape, _, ok := HarnessCallOf(file)
	if !ok {
		t.Fatalf("the fs.readFileSync namespace spelling did not recognize")
	}
	if name != "audioLevel" {
		t.Errorf("got name %q, want %q", name, "audioLevel")
	}
	if shape != HarnessShapeStdinJSON {
		t.Errorf("got shape %v, want HarnessShapeStdinJSON", shape)
	}
}

func TestHarnessCallOf_InsideAnIfGuardDeclines(t *testing.T) {
	file := harnessSourceFileOf(t, `
import { readFileSync } from "node:fs";
function audioLevel(samples) { return samples.length; }
if (require.main === module) {
  console.log(JSON.stringify(audioLevel(JSON.parse(readFileSync(0, "utf8")))));
}
`)
	if _, _, _, ok := HarnessCallOf(file); ok {
		t.Fatalf("a guarded statement recognized — v1 declines guards, the target file has no import surface a guard would protect")
	}
}

func TestHarnessCallOf_JSONParseOfAnythingElseDeclines(t *testing.T) {
	file := harnessSourceFileOf(t, `
import { readFileSync } from "node:fs";
function audioLevel(samples) { return samples.length; }
const raw = readFileSync(0, "utf8");
console.log(JSON.stringify(audioLevel(JSON.parse(raw))));
`)
	if _, _, _, ok := HarnessCallOf(file); ok {
		t.Fatalf("JSON.parse of a bare variable (not the stdin read directly) recognized")
	}
}

func TestHarnessCallOf_TwoMatchingStatementsDecline(t *testing.T) {
	file := harnessSourceFileOf(t, `
import { readFileSync } from "node:fs";
function audioLevel(samples) { return samples.length; }
function otherLevel(samples) { return samples.length; }
console.log(JSON.stringify(audioLevel(JSON.parse(readFileSync(0, "utf8")))));
console.log(JSON.stringify(otherLevel(JSON.parse(readFileSync(0, "utf8")))));
`)
	if _, _, _, ok := HarnessCallOf(file); ok {
		t.Fatalf("two matching top-level statements recognized — one harness fact cannot stand for two")
	}
}

func TestHarnessCallOf_ProcessStdoutWriteSpellingDeclines(t *testing.T) {
	file := harnessSourceFileOf(t, `
import { readFileSync } from "node:fs";
function audioLevel(samples) { return samples.length; }
process.stdout.write(JSON.stringify(audioLevel(JSON.parse(readFileSync(0, "utf8")))) + "\n");
`)
	if _, _, _, ok := HarnessCallOf(file); ok {
		t.Fatalf("process.stdout.write recognized — console.log is the one recognized sink")
	}
}

func TestHarnessCallOf_ShadowedReadFileSyncDeclines(t *testing.T) {
	file := harnessSourceFileOf(t, `
import { readFileSync } from "node:fs";
function audioLevel(samples) { return samples.length; }
function run() {
  const readFileSync = () => "{}";
  console.log(JSON.stringify(audioLevel(JSON.parse(readFileSync(0, "utf8")))));
}
`)
	if _, _, _, ok := HarnessCallOf(file); ok {
		t.Fatalf("a shadowed readFileSync recognized — the call is not a top-level statement and the name is not node:fs's own binding at that scope")
	}
}

func TestHarnessCallOf_NoImportOfNodeFsDeclines(t *testing.T) {
	file := harnessSourceFileOf(t, `
function readFileSync(fd, encoding) { return "{}"; }
function audioLevel(samples) { return samples.length; }
console.log(JSON.stringify(audioLevel(JSON.parse(readFileSync(0, "utf8")))));
`)
	if _, _, _, ok := HarnessCallOf(file); ok {
		t.Fatalf("a locally declared readFileSync (never imported from node:fs) recognized")
	}
}

func TestHarnessCallOf_NoMatchingStatementDeclines(t *testing.T) {
	file := harnessSourceFileOf(t, `
import { readFileSync } from "node:fs";
function audioLevel(samples) { return samples.length; }
`)
	if _, _, _, ok := HarnessCallOf(file); ok {
		t.Fatalf("a file with no harness statement at all recognized")
	}
}

func TestHarnessCallOf_ArgvJSONShapeNamesTheFunctionAndIndex(t *testing.T) {
	file := harnessSourceFileOf(t, `
function audioLevel(samples) { return samples.length; }
console.log(JSON.stringify(audioLevel(JSON.parse(process.argv[2]))));
`)
	name, shape, argIndex, ok := HarnessCallOf(file)
	if !ok {
		t.Fatalf("the argv-json shape did not recognize")
	}
	if name != "audioLevel" {
		t.Errorf("got name %q, want %q", name, "audioLevel")
	}
	if shape != HarnessShapeArgvJSON {
		t.Errorf("got shape %v, want HarnessShapeArgvJSON", shape)
	}
	if argIndex != 2 {
		t.Errorf("got argIndex %v, want 2", argIndex)
	}
}

func TestHarnessCallOf_ArgvJSONNonLiteralIndexDeclines(t *testing.T) {
	file := harnessSourceFileOf(t, `
function audioLevel(samples) { return samples.length; }
const i = 2;
console.log(JSON.stringify(audioLevel(JSON.parse(process.argv[i]))));
`)
	if _, _, _, ok := HarnessCallOf(file); ok {
		t.Fatalf("a non-literal argv index (a variable) recognized — the exporter can only pin an argIndex read directly off the source")
	}
}

func TestHarnessCallOf_BothShapesInOneFileDecline(t *testing.T) {
	file := harnessSourceFileOf(t, `
import { readFileSync } from "node:fs";
function audioLevel(samples) { return samples.length; }
function otherLevel(samples) { return samples.length; }
console.log(JSON.stringify(audioLevel(JSON.parse(readFileSync(0, "utf8")))));
console.log(JSON.stringify(otherLevel(JSON.parse(process.argv[2]))));
`)
	if _, _, _, ok := HarnessCallOf(file); ok {
		t.Fatalf("a stdin-json statement and an argv-json statement together recognized — one harness fact cannot stand for two, whatever their shapes")
	}
}

func TestHarnessCallOf_FileJSONShapeNamesTheFunctionAndIndex(t *testing.T) {
	file := harnessSourceFileOf(t, `
import { readFileSync } from "node:fs";
function audioLevel(samples) { return samples.length; }
console.log(JSON.stringify(audioLevel(JSON.parse(readFileSync(process.argv[2], "utf8")))));
`)
	name, shape, argIndex, ok := HarnessCallOf(file)
	if !ok {
		t.Fatalf("the file-json shape did not recognize")
	}
	if name != "audioLevel" {
		t.Errorf("got name %q, want %q", name, "audioLevel")
	}
	if shape != HarnessShapeFileJSON {
		t.Errorf("got shape %v, want HarnessShapeFileJSON", shape)
	}
	if argIndex != 2 {
		t.Errorf("got argIndex %v, want 2", argIndex)
	}
}

func TestHarnessCallOf_FileJSONNamespaceImportOfFsRecognizes(t *testing.T) {
	file := harnessSourceFileOf(t, `
import * as fs from "node:fs";
function audioLevel(samples) { return samples.length; }
console.log(JSON.stringify(audioLevel(JSON.parse(fs.readFileSync(process.argv[2], "utf8")))));
`)
	name, shape, argIndex, ok := HarnessCallOf(file)
	if !ok {
		t.Fatalf("the fs.readFileSync namespace spelling did not recognize for the file-json shape")
	}
	if name != "audioLevel" {
		t.Errorf("got name %q, want %q", name, "audioLevel")
	}
	if shape != HarnessShapeFileJSON {
		t.Errorf("got shape %v, want HarnessShapeFileJSON", shape)
	}
	if argIndex != 2 {
		t.Errorf("got argIndex %v, want 2", argIndex)
	}
}

func TestHarnessCallOf_FileJSONNonLiteralIndexDeclines(t *testing.T) {
	file := harnessSourceFileOf(t, `
import { readFileSync } from "node:fs";
function audioLevel(samples) { return samples.length; }
const i = 2;
console.log(JSON.stringify(audioLevel(JSON.parse(readFileSync(process.argv[i], "utf8")))));
`)
	if _, _, _, ok := HarnessCallOf(file); ok {
		t.Fatalf("a non-literal argv index (a variable) recognized inside readFileSync — the exporter can only pin an argIndex read directly off the source")
	}
}

func TestHarnessCallOf_FileJSONWrongEncodingDeclines(t *testing.T) {
	file := harnessSourceFileOf(t, `
import { readFileSync } from "node:fs";
function audioLevel(samples) { return samples.length; }
console.log(JSON.stringify(audioLevel(JSON.parse(readFileSync(process.argv[2], "ascii")))));
`)
	if _, _, _, ok := HarnessCallOf(file); ok {
		t.Fatalf("an encoding other than \"utf8\" recognized")
	}
}

func TestHarnessCallOf_AllThreeShapesTogetherDecline(t *testing.T) {
	file := harnessSourceFileOf(t, `
import { readFileSync } from "node:fs";
function audioLevel(samples) { return samples.length; }
function otherLevel(samples) { return samples.length; }
function thirdLevel(samples) { return samples.length; }
console.log(JSON.stringify(audioLevel(JSON.parse(readFileSync(0, "utf8")))));
console.log(JSON.stringify(otherLevel(JSON.parse(process.argv[2]))));
console.log(JSON.stringify(thirdLevel(JSON.parse(readFileSync(process.argv[3], "utf8")))));
`)
	if _, _, _, ok := HarnessCallOf(file); ok {
		t.Fatalf("three matching statements across all shapes recognized — one harness fact cannot stand for three")
	}
}
