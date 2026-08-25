// ExportFact end to end, against a real file on disk (ProgramFromDisk,
// the same construction path CheckFile takes) — a temp-dir fixture in
// the harness shape fact_export_harness.go recognizes, so the whole
// producer chain (harness recognition, ExportFunctionFact, the
// envelope, the atomic write) is exercised the way the real
// `-export-fact` CLI mode runs it, not a unit slice of one function.
//
// FILE MAP. This file holds the shared harness fixture writers every
// other export_fact_*_test.go file reuses (writeHarnessFixture and its
// argv/file/execFileSync twins, meterLevelBody, exportFactSurfacePath,
// requireExportFactKernel) and the core well-formed-envelope tests
// (the frozen shape, the execFileSync purity gate both ways, the
// argv-json and file-json surfaces). The omission-sentence tests
// (non-literal argv index, the unexportable-function pin, the
// nullable/object return shapes) live in export_fact_omission_test.go;
// the consumer-side memo-freshness and cases-fixture reads live in
// export_fact_memo_test.go.

package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
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

// writeArgvHarnessFixture writes a temp-dir .ts fixture in the
// argv-json harness shape fact_export_harness.go recognizes — the same
// anatomy writeHarnessFixture builds, but the harness line reads
// process.argv[2] instead of readFileSync(0, "utf8"), and carries no
// node:fs import at all (the argv shape needs none).
func writeArgvHarnessFixture(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	source := `import * as z from "` + exportFactSurfacePath + `";

const zSamples = z.array(z.number().min(-2).max(2)).min(1);

` + body + `

console.log(JSON.stringify(meterLevel(JSON.parse(process.argv[2]))));
`
	path := filepath.Join(dir, "meter_level.ts")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}
	return path
}

// writeFileHarnessFixture writes a temp-dir .ts fixture in the
// file-json harness shape fact_export_harness.go recognizes — the same
// anatomy writeHarnessFixture builds, but the harness line reads
// readFileSync(process.argv[2], "utf8") instead of readFileSync(0,
// "utf8") — the payload lives in the FILE named at that argv position,
// not on stdin.
func writeFileHarnessFixture(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	source := `import { readFileSync } from "node:fs";
import * as z from "` + exportFactSurfacePath + `";

const zSamples = z.array(z.number().min(-2).max(2)).min(1);

` + body + `

console.log(JSON.stringify(meterLevel(JSON.parse(readFileSync(process.argv[2], "utf8")))));
`
	path := filepath.Join(dir, "meter_level.ts")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}
	return path
}

// writeExecFileSyncHarnessFixture writes a temp-dir .ts fixture in the
// same stdin-json harness shape writeHarnessFixture builds, but with
// `execFileSync` imported and available to the harness's own called
// function — the chain_boost.ts shape (a function whose only
// "impurity" is a captured-stdout spawn), reproduced here as a small
// inline source rather than the corpus file.
func writeExecFileSyncHarnessFixture(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	source := `import { readFileSync } from "node:fs";
import { execFileSync } from "node:child_process";
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

func requireExportFactKernel(t *testing.T) {
	t.Helper()
	if !kernelbridge.KernelArtifactsPresent(kernelbridge.DylibPath) {
		t.Skip("native kernel dylib not built")
	}
}

// TestExportFact_AWellFormedHarnessWritesTheFrozenEnvelope pins the
// whole shape foreign_edge_artifact.go's doc comment freezes: kind, NO
// version field, contentHash prefix, harness.calls, a non-empty entry
// row, a non-empty return cases list, stdoutPure, and a provenance
// line ≥ 1.
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
	if _, hasVersion := envelope["version"]; hasVersion {
		t.Errorf(`envelope carries a "version" field (%v), want none — the RULED schema states no version, ever`, envelope["version"])
	}
	if language, _ := parsed["language"].(string); language != ExportFactLanguage {
		t.Errorf("language = %q, want %q", language, ExportFactLanguage)
	}

	target, ok := parsed["target"].(map[string]any)
	if !ok {
		t.Fatalf("the artifact carries no target: %v", parsed)
	}
	contentHash, _ := target["contentHash"].(string)
	if !hasPrefix(contentHash, "sha256:") {
		t.Errorf("contentHash = %q, want a sha256: prefix", contentHash)
	}

	surface, ok := parsed["surface"].(map[string]any)
	if !ok {
		t.Fatalf("the artifact carries no surface: %v", parsed)
	}
	if kind, _ := surface["kind"].(string); kind != "stdin-json" {
		t.Errorf("surface.kind = %q, want stdin-json", kind)
	}
	if calls, _ := surface["calls"].(string); calls != "meterLevel" {
		t.Errorf("surface.calls = %q, want meterLevel", calls)
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
	cases, hasCases := returned["cases"].([]any)
	if !hasCases || len(cases) == 0 {
		t.Errorf("return carries no non-empty cases list: %v", returned)
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

// TestExportFact_AnExecFileSyncSpawningBodyExportsItsFact pins the
// fix for the ledgered conservative-wrong purity refusal (ISSUES.md,
// "Go export: the stdout-purity guard refuses any body that spawns a
// child"): a body whose only "impurity" is an execFileSync call —
// chain_boost.ts's own shape — must still export, since execFileSync
// pipes the child's stdout back as this call's own return value
// rather than writing the parent's stdout. The return is
// samples.length rather than anything read off the spawn's own
// result: resolving what execFileSync's return derives to needs the
// cross-language edge (ForeignEdgeAt), which needs @types/node in
// reach — a program-construction premise this package's own test
// programs do not carry (foreign_edge_test.go's banner) — so this
// test keeps the derived return independent of that gap and stays
// scoped to the purity gate alone.
func TestExportFact_AnExecFileSyncSpawningBodyExportsItsFact(t *testing.T) {
	requireExportFactKernel(t)
	spawningBody := `function meterLevel(samples: z.infer<typeof zSamples>): number {
  const stdout = execFileSync("python3", ["./leaf.py"], {
    input: JSON.stringify(samples),
    encoding: "utf8",
  });
  void stdout;
  return samples.length;
}
`
	path := writeExecFileSyncHarnessFixture(t, spawningBody)
	outPath := filepath.Join(filepath.Dir(path), "meter_level.ts.refined.json")

	written, omissions, err := ExportFact(path, exportFactSurfacePath, outPath)
	if err != nil {
		t.Fatalf("ExportFact: %v", err)
	}
	if len(omissions) != 0 {
		t.Fatalf("ExportFact reported omissions for a captured-stdout spawning body: %v", omissions)
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
	functions, ok := parsed["functions"].(map[string]any)
	if !ok {
		t.Fatalf("the artifact carries no functions: %v", parsed)
	}
	row, ok := functions["meterLevel"].(map[string]any)
	if !ok {
		t.Fatalf("the artifact states no fact for meterLevel: %v", functions)
	}
	returned, ok := row["return"].(map[string]any)
	if !ok {
		t.Fatalf("the row states no return: %v", row)
	}
	if pure, _ := returned["stdoutPure"].(bool); !pure {
		t.Errorf("stdoutPure = %v, want true — execFileSync's captured stdout never writes the parent's own stdout", returned["stdoutPure"])
	}
}

// TestExportFact_ExecFileSyncWithInheritedStdioStillRefuses pins the
// one shape that must still refuse: an explicit `stdio: "inherit"`
// sends the child's stdout to the SAME stdout the JSON wire channel
// must carry alone, so the purity-guard fix must not admit this row.
func TestExportFact_ExecFileSyncWithInheritedStdioStillRefuses(t *testing.T) {
	requireExportFactKernel(t)
	inheritedStdioBody := `function meterLevel(samples: z.infer<typeof zSamples>): number {
  const stdout = execFileSync("python3", ["./leaf.py"], {
    input: JSON.stringify(samples),
    stdio: "inherit",
  });
  return samples.length;
}
`
	path := writeExecFileSyncHarnessFixture(t, inheritedStdioBody)
	outPath := filepath.Join(filepath.Dir(path), "meter_level.ts.refined.json")

	written, omissions, err := ExportFact(path, exportFactSurfacePath, outPath)
	if err != nil {
		t.Fatalf("ExportFact: %v", err)
	}
	if written != outPath {
		t.Fatalf("written = %q, want %q — a recognized surface still writes the artifact", written, outPath)
	}
	if len(omissions) != 1 {
		t.Fatalf("omissions = %v, want exactly one", omissions)
	}
	want := path + ": 'meterLevel' is not exported: " +
		"its body may write to stdout, which the JSON wire channel must carry alone"
	if omissions[0] != want {
		t.Errorf("omissions[0] = %q, want %q", omissions[0], want)
	}
}

// TestExportFact_ArgvJSONHarnessExportsTheArgvJSONSurface pins the
// argv-json surface verbatim ({"kind": "argv-json", "argIndex": 2,
// "stdout": "json", "calls": "meterLevel"} — no "stdin" field) for a
// fixture whose harness reads process.argv[2] rather than stdin, and
// confirms the rest of the envelope (entry rows, return set,
// stdoutPure) exports exactly as the stdin fixture's own test pins.
func TestExportFact_ArgvJSONHarnessExportsTheArgvJSONSurface(t *testing.T) {
	requireExportFactKernel(t)
	path := writeArgvHarnessFixture(t, meterLevelBody)
	outPath := filepath.Join(filepath.Dir(path), "meter_level.ts.refined.json")

	written, omissions, err := ExportFact(path, exportFactSurfacePath, outPath)
	if err != nil {
		t.Fatalf("ExportFact: %v", err)
	}
	if len(omissions) != 0 {
		t.Fatalf("ExportFact reported omissions for an exportable argv-json function: %v", omissions)
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

	surface, ok := parsed["surface"].(map[string]any)
	if !ok {
		t.Fatalf("the artifact carries no surface: %v", parsed)
	}
	wantSurface := map[string]any{
		"kind":     "argv-json",
		"argIndex": float64(2),
		"stdout":   "json",
		"calls":    "meterLevel",
	}
	if len(surface) != len(wantSurface) {
		t.Fatalf("surface = %v, want exactly %v (no extra fields, no \"stdin\")", surface, wantSurface)
	}
	for key, want := range wantSurface {
		if got := surface[key]; got != want {
			t.Errorf("surface[%q] = %v, want %v", key, got, want)
		}
	}
	if _, hasStdin := surface["stdin"]; hasStdin {
		t.Errorf(`surface carries a "stdin" field — argv-json has no stdin carrier`)
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
	if pure, _ := returned["stdoutPure"].(bool); !pure {
		t.Errorf("stdoutPure = %v, want true — the fixture writes nothing else to stdout", returned["stdoutPure"])
	}
}

// TestExportFact_FileJSONHarnessExportsTheFileJSONSurface pins the
// file-json surface verbatim ({"kind": "file-json", "argIndex": 2,
// "stdout": "json", "calls": "meterLevel"} — no "stdin" field) for a
// fixture whose harness reads readFileSync(process.argv[2], "utf8")
// rather than stdin or the argv value itself, and confirms the rest of
// the envelope (entry rows, return set, stdoutPure) exports exactly as
// the stdin fixture's own test pins.
func TestExportFact_FileJSONHarnessExportsTheFileJSONSurface(t *testing.T) {
	requireExportFactKernel(t)
	path := writeFileHarnessFixture(t, meterLevelBody)
	outPath := filepath.Join(filepath.Dir(path), "meter_level.ts.refined.json")

	written, omissions, err := ExportFact(path, exportFactSurfacePath, outPath)
	if err != nil {
		t.Fatalf("ExportFact: %v", err)
	}
	if len(omissions) != 0 {
		t.Fatalf("ExportFact reported omissions for an exportable file-json function: %v", omissions)
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

	surface, ok := parsed["surface"].(map[string]any)
	if !ok {
		t.Fatalf("the artifact carries no surface: %v", parsed)
	}
	wantSurface := map[string]any{
		"kind":     "file-json",
		"argIndex": float64(2),
		"stdout":   "json",
		"calls":    "meterLevel",
	}
	if len(surface) != len(wantSurface) {
		t.Fatalf("surface = %v, want exactly %v (no extra fields, no \"stdin\")", surface, wantSurface)
	}
	for key, want := range wantSurface {
		if got := surface[key]; got != want {
			t.Errorf("surface[%q] = %v, want %v", key, got, want)
		}
	}
	if _, hasStdin := surface["stdin"]; hasStdin {
		t.Errorf(`surface carries a "stdin" field — file-json has no stdin carrier`)
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
	if pure, _ := returned["stdoutPure"].(bool); !pure {
		t.Errorf("stdoutPure = %v, want true — the fixture writes nothing else to stdout", returned["stdoutPure"])
	}
}
