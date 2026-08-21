// ExportFact end to end, against a real file on disk (ProgramFromDisk,
// the same construction path CheckFile takes) — a temp-dir fixture in
// the harness shape fact_export_harness.go recognizes, so the whole
// producer chain (harness recognition, ExportFunctionFact, the
// envelope, the atomic write) is exercised the way the real
// `-export-fact` CLI mode runs it, not a unit slice of one function.

package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/walk"
)

// exportFactSurfacePathRelative is the real on-disk surface/z.ts
// ProgramFromDisk recognizes, spelled relative to this package — the
// same file cmd/refinedts-check/main.go's surfaceZPath derives.
// ProgramFromDisk keys SurfacePaths by the EXACT string handed in, so
// every caller here resolves it to the same absolute form the fixture's
// own import specifier uses (exportFactSurfacePath, below) — a
// relative spelling would key the recognition map under a path the
// resolved module's FileName never equals.
const exportFactSurfacePathRelative = "../../../../refined-ts-typescript/surface/z.ts"

// exportFactSurfacePath is exportFactSurfacePathRelative resolved to
// an absolute path once, at package init — both ExportFact's own
// surfacePath argument and the fixture's import specifier use this
// exact string, so the two agree on which file is "the surface."
var exportFactSurfacePath = mustAbs(exportFactSurfacePathRelative)

func mustAbs(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		panic(err)
	}
	return filepath.ToSlash(abs)
}

// writeHarnessFixture writes a temp-dir .ts fixture in the ONE harness
// shape fact_export_harness.go recognizes: a bounded array parameter
// (z.array(z.number().min(-2).max(2)).min(1)) mapped down to a bounded
// number, read from stdin and written to stdout as the sole top-level
// statement. Returns the fixture's own path.
func writeHarnessFixture(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	source := `import { readFileSync } from "node:fs";
import * as z from "` + exportFactSurfacePath + `";

const zSamples = z.array(z.number().min(-2).max(2)).min(1);

` + body + `

console.log(JSON.stringify(meterLevel(JSON.parse(readFileSync(0, "utf8")))));
`
	path := filepath.Join(dir, "meter_level.ts")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}
	return path
}

// meterLevelBody is the exportable shape: one z-bounded array
// parameter, and a body whose returns derive a faithful set with no
// call site in view — samples.length, the same shape
// fact_export_test.go's own DerivedReturnOf tests already confirm
// derives cleanly with no caller.
const meterLevelBody = `function meterLevel(samples: z.infer<typeof zSamples>): number {
  return samples.length;
}
`

func requireExportFactKernel(t *testing.T) {
	t.Helper()
	if !kernelbridge.KernelArtifactsPresent(kernelbridge.DylibPath) {
		t.Skip("native kernel dylib not built")
	}
}

// TestExportFact_AWellFormedHarnessWritesTheFrozenEnvelope pins the
// whole shape foreign_edge_artifact.go's doc comment freezes: kind,
// version, contentHash prefix, harness.calls, a non-empty entry row,
// a non-empty return set, stdoutPure, and a provenance line ≥ 1.
func TestExportFact_AWellFormedHarnessWritesTheFrozenEnvelope(t *testing.T) {
	requireExportFactKernel(t)
	path := writeHarnessFixture(t, meterLevelBody)
	outPath := filepath.Join(filepath.Dir(path), "meter_level.ts.refined.json")

	written, omissions, err := ExportFact(path, exportFactSurfacePath, outPath)
	if err != nil {
		t.Fatalf("ExportFact: %v", err)
	}
	if len(omissions) != 0 {
		t.Fatalf("ExportFact reported omissions for an exportable function: %v", omissions)
	}
	if written != outPath {
		t.Fatalf("written = %q, want %q", written, outPath)
	}

	raw, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("reading the written artifact: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("the written artifact is not valid JSON: %v", err)
	}

	envelope, ok := parsed["refined"].(map[string]any)
	if !ok {
		t.Fatalf(`the artifact carries no "refined" envelope: %v`, parsed)
	}
	if kind, _ := envelope["kind"].(string); kind != ExportFactArtifactKind {
		t.Errorf("kind = %q, want %q", kind, ExportFactArtifactKind)
	}
	if version, _ := envelope["version"].(float64); int(version) != ExportFactArtifactVersion {
		t.Errorf("version = %v, want %d", envelope["version"], ExportFactArtifactVersion)
	}

	target, ok := parsed["target"].(map[string]any)
	if !ok {
		t.Fatalf("the artifact carries no target: %v", parsed)
	}
	contentHash, _ := target["contentHash"].(string)
	if !hasPrefix(contentHash, "sha256:") {
		t.Errorf("contentHash = %q, want a sha256: prefix", contentHash)
	}

	harness, ok := parsed["harness"].(map[string]any)
	if !ok {
		t.Fatalf("the artifact carries no harness: %v", parsed)
	}
	if calls, _ := harness["calls"].(string); calls != "meterLevel" {
		t.Errorf("harness.calls = %q, want meterLevel", calls)
	}

	functions, ok := parsed["functions"].(map[string]any)
	if !ok {
		t.Fatalf("the artifact carries no functions: %v", parsed)
	}
	row, ok := functions["meterLevel"].(map[string]any)
	if !ok {
		t.Fatalf("the artifact states no fact for meterLevel: %v", functions)
	}
	entries, ok := row["entry"].([]any)
	if !ok || len(entries) == 0 {
		t.Fatalf("entry = %v, want a non-empty entry row list", row["entry"])
	}
	returned, ok := row["return"].(map[string]any)
	if !ok {
		t.Fatalf("the row states no return: %v", row)
	}
	if _, hasSet := returned["set"]; !hasSet {
		t.Errorf("return carries no set: %v", returned)
	}
	if pure, _ := returned["stdoutPure"].(bool); !pure {
		t.Errorf("stdoutPure = %v, want true — the fixture writes nothing else to stdout", returned["stdoutPure"])
	}
	provenance, ok := row["provenance"].(map[string]any)
	if !ok {
		t.Fatalf("the row states no provenance: %v", row)
	}
	if line, _ := provenance["line"].(float64); line < 1 {
		t.Errorf("provenance.line = %v, want >= 1", provenance["line"])
	}
	if said, _ := provenance["said"].(string); said == "" {
		t.Errorf("provenance.said is empty")
	}
}

// TestExportFact_AnUnexportableFunctionPinsTheOmissionSentence: the
// harness calls a function taking two parameters — the exporter
// refuses symmetrically with the consumer's own one-entry rule — and
// ExportFact's own omission sentence names it by the fixed shape
// cmd/refinedts-check/main.go prints verbatim.
func TestExportFact_AnUnexportableFunctionPinsTheOmissionSentence(t *testing.T) {
	requireExportFactKernel(t)
	twoParamBody := `function meterLevel(samples: z.infer<typeof zSamples>, gain: number): number {
  return gain;
}
`
	path := writeHarnessFixture(t, twoParamBody)
	outPath := filepath.Join(filepath.Dir(path), "meter_level.ts.refined.json")

	written, omissions, err := ExportFact(path, exportFactSurfacePath, outPath)
	if err != nil {
		t.Fatalf("ExportFact: %v", err)
	}
	if written != "" {
		t.Fatalf("written = %q, want \"\" alongside an omission", written)
	}
	if len(omissions) != 1 {
		t.Fatalf("omissions = %v, want exactly one", omissions)
	}
	want := path + ": 'meterLevel' is not exported: " +
		"the function declares more than one parameter, and the stdin harness hands one JSON value to one parameter"
	if omissions[0] != want {
		t.Errorf("omissions[0] = %q, want %q", omissions[0], want)
	}
	if _, statErr := os.Stat(outPath); statErr == nil {
		t.Errorf("an omitted function must not write an artifact file")
	}
}

// TestReadForeignArtifact_MemoFreshness_ARewrittenArtifactIsReadAfresh
// pins the fact-freshness stopgap docs/one-checker/fact-freshness.md
// names: ReadForeignArtifact memoizes for the process, and a later
// write to the SAME cache entry (a live producer, or — as here — a
// rewrite standing in for one) must be read on the next call rather
// than served from what the memo held before the write. This test
// lives beside ExportFact because it exercises the SAME artifact
// shape ExportFact writes, read back by the consumer's own path.
func TestReadForeignArtifact_MemoFreshness_ARewrittenArtifactIsReadAfresh(t *testing.T) {
	dir := t.TempDir()
	targetPath := filepath.Join(dir, "audio_level.py")
	firstSource := "def audio_level(samples):\n    return 0.5\n"
	if err := os.WriteFile(targetPath, []byte(firstSource), 0o644); err != nil {
		t.Fatalf("writing the target: %v", err)
	}

	artifactPath := walk.ForeignCacheArtifactPath(targetPath)
	if err := os.MkdirAll(filepath.Dir(artifactPath), 0o755); err != nil {
		t.Fatalf("creating the cache directory: %v", err)
	}
	writeArtifactSayingLine := func(source string, line int) {
		sum := sha256.Sum256([]byte(source))
		hash := "sha256:" + hex.EncodeToString(sum[:])
		text := `{"refined": {"kind": "python-fact-artifact", "version": 1},
"target": {"file": "` + targetPath + `", "contentHash": "` + hash + `"},
"runtime": {"band": "cpython-3.11+"},
"harness": {"stdin": "json", "stdout": "json", "calls": "audio_level"},
"functions": {"audio_level": {
  "entry": [{"name": "samples", "sequence": {
    "element": {"forms": [{"form": "atLeast", "a": {"num": -1, "exp": 0}},
                          {"form": "atMost", "a": {"num": 1, "exp": 0}}]},
    "lengthAtLeast": 1}}],
  "return": {"set": {"forms": [{"form": "atLeast", "a": {"num": 0, "exp": 0}},
                               {"form": "atMost", "a": {"num": 1, "exp": 0}}]},
             "stdoutPure": true},
  "provenance": {"line": ` + strconv.Itoa(line) + `, "said": "first"}
}}}`
		if err := os.WriteFile(artifactPath, []byte(text), 0o644); err != nil {
			t.Fatalf("writing the artifact: %v", err)
		}
	}

	// fill the memo against the first artifact
	writeArtifactSayingLine(firstSource, 2)
	first, sentence := walk.ReadForeignArtifact(targetPath)
	if sentence != "" {
		t.Fatalf("the first read declined: %s", sentence)
	}
	if first.Called.Provenance.Line != 2 {
		t.Fatalf("first read's provenance line = %d, want 2", first.Called.Provenance.Line)
	}

	// the target and the artifact both change together, standing in for
	// a live producer re-exporting after a save — the memo must not
	// serve the FIRST read's answer for a file that has since changed
	secondSource := "def audio_level(samples):\n\n    return 0.5\n"
	if err := os.WriteFile(targetPath, []byte(secondSource), 0o644); err != nil {
		t.Fatalf("rewriting the target: %v", err)
	}
	writeArtifactSayingLine(secondSource, 3)

	second, sentence := walk.ReadForeignArtifact(targetPath)
	if sentence != "" {
		t.Fatalf("the second read declined: %s", sentence)
	}
	if second.Called.Provenance.Line != 3 {
		t.Fatalf("second read's provenance line = %d, want 3 — the memo served a stale mtime", second.Called.Provenance.Line)
	}
}

func hasPrefix(s string, prefix string) bool {
	return strings.HasPrefix(s, prefix)
}
