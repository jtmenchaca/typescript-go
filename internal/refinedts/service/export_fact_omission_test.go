// The exporter's own omission sentences: a non-literal argv index (both
// inside readFileSync and bare), an unexportable multi-parameter
// function, and the nullable/object return shapes' own emitted cases.
// Split out of export_fact_test.go, which still holds the shared
// harness fixture writers (writeHarnessFixture, meterLevelBody, and
// friends) and exportFactSurfacePath every export_fact_*_test.go file
// in this package reuses.

package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestExportFact_NonLiteralArgvIndexInsideReadFileSyncOmitsNamingTheHarnessGap:
// a harness reading readFileSync(process.argv[i], "utf8") for a
// variable i (not a literal) matches none of the three recognized
// harness shapes, so ExportFact declines the whole file with the same
// "no recognized stdio harness" sentence — naming the construct
// HarnessCallOf requires (a literal argv index inside readFileSync)
// rather than silently guessing one.
func TestExportFact_NonLiteralArgvIndexInsideReadFileSyncOmitsNamingTheHarnessGap(t *testing.T) {
	dir := t.TempDir()
	source := `import { readFileSync } from "node:fs";
import * as z from "` + exportFactSurfacePath + `";

const zSamples = z.array(z.number().min(-2).max(2)).min(1);

function meterLevel(samples: z.infer<typeof zSamples>): number {
  return samples.length;
}

const i = 2;
console.log(JSON.stringify(meterLevel(JSON.parse(readFileSync(process.argv[i], "utf8")))));
`
	path := filepath.Join(dir, "meter_level.ts")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}
	outPath := filepath.Join(dir, "meter_level.ts.refined.json")

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
	want := path + ": no recognized stdio harness — " +
		`a bare top-level console.log(JSON.stringify(<fn>(JSON.parse(readFileSync(0, "utf8"))))), ` +
		`console.log(JSON.stringify(<fn>(JSON.parse(process.argv[<literal int>])))), ` +
		`or console.log(JSON.stringify(<fn>(JSON.parse(readFileSync(process.argv[<literal int>], "utf8"))))) is the only shape read`
	if omissions[0] != want {
		t.Errorf("omissions[0] = %q, want %q", omissions[0], want)
	}
	if _, statErr := os.Stat(outPath); statErr == nil {
		t.Errorf("an omitted function must not write an artifact file")
	}
}

// TestExportFact_NonLiteralArgvIndexOmitsNamingTheHarnessGap: a harness
// reading process.argv[i] for a variable i (not a literal) matches
// none of the three recognized harness shapes, so ExportFact declines
// the whole file with the same "no recognized stdio harness" sentence
// a file with no harness at all gets — naming the construct
// HarnessCallOf requires (a literal argv index) rather than silently
// guessing one. No kernel needed: HarnessCallOf's own decline is a
// pure AST scan, reached before ExportFact ever sets one up.
func TestExportFact_NonLiteralArgvIndexOmitsNamingTheHarnessGap(t *testing.T) {
	dir := t.TempDir()
	source := `import * as z from "` + exportFactSurfacePath + `";

const zSamples = z.array(z.number().min(-2).max(2)).min(1);

function meterLevel(samples: z.infer<typeof zSamples>): number {
  return samples.length;
}

const i = 2;
console.log(JSON.stringify(meterLevel(JSON.parse(process.argv[i]))));
`
	path := filepath.Join(dir, "meter_level.ts")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}
	outPath := filepath.Join(dir, "meter_level.ts.refined.json")

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
	want := path + ": no recognized stdio harness — " +
		`a bare top-level console.log(JSON.stringify(<fn>(JSON.parse(readFileSync(0, "utf8"))))), ` +
		`console.log(JSON.stringify(<fn>(JSON.parse(process.argv[<literal int>])))), ` +
		`or console.log(JSON.stringify(<fn>(JSON.parse(readFileSync(process.argv[<literal int>], "utf8"))))) is the only shape read`
	if omissions[0] != want {
		t.Errorf("omissions[0] = %q, want %q", omissions[0], want)
	}
	if _, statErr := os.Stat(outPath); statErr == nil {
		t.Errorf("an omitted function must not write an artifact file")
	}
}

// TestExportFact_AnUnexportableFunctionPinsTheOmissionSentence: the
// harness calls a function taking two parameters — the exporter
// refuses symmetrically with the consumer's own one-entry rule — and
// ExportFact's own omission sentence names it by the fixed shape
// cmd/refinedts-check/main.go prints verbatim. Fixes the ledgered
// exporter asymmetry (ISSUES.md, "Exporter asymmetry: an all-omitted
// target yields an artifact from the Python exporter... but NO file
// from the Go exporter"): the harness surface IS recognized here, so
// the artifact is still written — target/language/runtime/surface
// stated in full, "functions" left an empty object — rather than no
// file at all, matching the Python exporter's own module-level shape.
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
	if written != outPath {
		t.Fatalf("written = %q, want %q — a recognized surface still writes the artifact", written, outPath)
	}
	if len(omissions) != 1 {
		t.Fatalf("omissions = %v, want exactly one", omissions)
	}
	want := path + ": 'meterLevel' is not exported: " +
		"the function declares more than one parameter, and the stdin harness hands one JSON value to one parameter"
	if omissions[0] != want {
		t.Errorf("omissions[0] = %q, want %q", omissions[0], want)
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
	if calls, _ := surface["calls"].(string); calls != "meterLevel" {
		t.Errorf("surface.calls = %q, want meterLevel — the surface still names the harness's called function", calls)
	}
	functions, ok := parsed["functions"].(map[string]any)
	if !ok {
		t.Fatalf("the artifact carries no functions: %v", parsed)
	}
	if len(functions) != 0 {
		t.Errorf("functions = %v, want an empty object — meterLevel itself states no fact", functions)
	}
}

// nullableMeterLevelBody is meterLevelBody's nullable-return twin:
// samples.length is always present, so a null branch is added purely
// to exercise the RULED schema's null-case export — the body still
// calls the ONE harness-recognized name (meterLevel) with the ONE
// z-bounded array parameter writeHarnessFixture's harness line names.
const nullableMeterLevelBody = `function meterLevel(samples: z.infer<typeof zSamples>): number | null {
  return samples.length > 5 ? null : samples.length;
}
`

// TestExportFact_ANullableReturnEmitsTheInnerCasePlusNull pins the
// RULED schema's own rule for a DeclaredPossiblyUndefined-shaped
// derived return: the emitted "cases" array carries the inner number
// case PLUS {"sort":"null"} appended, and the envelope carries no
// "version" field anywhere.
func TestExportFact_ANullableReturnEmitsTheInnerCasePlusNull(t *testing.T) {
	requireExportFactKernel(t)
	path := writeHarnessFixture(t, nullableMeterLevelBody)
	outPath := filepath.Join(filepath.Dir(path), "meter_level.ts.refined.json")

	written, omissions, err := ExportFact(path, exportFactSurfacePath, outPath)
	if err != nil {
		t.Fatalf("ExportFact: %v", err)
	}
	if len(omissions) != 0 {
		t.Fatalf("ExportFact reported omissions for an exportable nullable-return function: %v", omissions)
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
	if _, hasVersion := envelope["version"]; hasVersion {
		t.Errorf(`envelope carries a "version" field (%v), want none`, envelope["version"])
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
	cases, ok := returned["cases"].([]any)
	if !ok {
		t.Fatalf("return carries no cases list: %v", returned)
	}
	if len(cases) != 2 {
		t.Fatalf("len(cases) = %d, want 2 (the inner number case plus null): %v", len(cases), cases)
	}
	first, ok := cases[0].(map[string]any)
	if !ok {
		t.Fatalf("cases[0] is not an object: %v", cases[0])
	}
	if sort, _ := first["sort"].(string); sort != "number" {
		t.Errorf(`cases[0].sort = %q, want "number"`, sort)
	}
	if _, hasSet := first["set"]; !hasSet {
		t.Errorf("cases[0] carries no set: %v", first)
	}
	last, ok := cases[1].(map[string]any)
	if !ok {
		t.Fatalf("cases[1] is not an object: %v", cases[1])
	}
	if sort, _ := last["sort"].(string); sort != "null" {
		t.Errorf(`cases[1].sort = %q, want "null"`, sort)
	}
	if _, hasSet := last["set"]; hasSet {
		t.Errorf("cases[1] (the null case) carries a set, want none: %v", last)
	}
}

// objectReturnMeterLevelBody is meterLevelBody's object-return twin:
// the same z-bounded array parameter, but the body wraps samples.length
// in a plain object literal — Item 1's own exporter reading
// (FaithfulReturnCases' object arm, fact_export.go) applied end to end
// through the real ExportFact CLI path rather than a unit call.
const objectReturnMeterLevelBody = `function meterLevel(samples: z.infer<typeof zSamples>) {
  return { ok: true, level: samples.length };
}
`

// TestExportFact_AnObjectReturnEmitsTheObjectCaseWithMembers pins Item
// 1's whole producer path: a body returning a plain object literal
// exports {"sort":"object","members":{...},"closed":true} rather than
// the old omission ("an object" — returnKindWords' refusal before this
// fix), verified against the actual bytes ExportFact writes to disk —
// the same JSON a Python consumer would read across the FFI edge.
func TestExportFact_AnObjectReturnEmitsTheObjectCaseWithMembers(t *testing.T) {
	requireExportFactKernel(t)
	path := writeHarnessFixture(t, objectReturnMeterLevelBody)
	outPath := filepath.Join(filepath.Dir(path), "meter_level.ts.refined.json")

	written, omissions, err := ExportFact(path, exportFactSurfacePath, outPath)
	if err != nil {
		t.Fatalf("ExportFact: %v", err)
	}
	if len(omissions) != 0 {
		t.Fatalf("ExportFact reported omissions for an exportable object-return function: %v", omissions)
	}
	if written != outPath {
		t.Fatalf("written = %q, want %q", written, outPath)
	}

	raw, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("reading the written artifact: %v", err)
	}
	t.Logf("written artifact:\n%s", raw)
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
	cases, ok := returned["cases"].([]any)
	if !ok {
		t.Fatalf("return carries no cases list: %v", returned)
	}
	if len(cases) != 1 {
		t.Fatalf("len(cases) = %d, want 1: %v", len(cases), cases)
	}
	c, ok := cases[0].(map[string]any)
	if !ok {
		t.Fatalf("cases[0] is not an object: %v", cases[0])
	}
	if sort, _ := c["sort"].(string); sort != "object" {
		t.Fatalf(`cases[0].sort = %q, want "object"`, sort)
	}
	closed, _ := c["closed"].(bool)
	if !closed {
		t.Errorf("cases[0].closed = %v, want true — a plain object literal with no spread states its exact key set", c["closed"])
	}
	members, ok := c["members"].(map[string]any)
	if !ok {
		t.Fatalf("cases[0] carries no members object: %v", c)
	}
	if len(members) != 2 {
		t.Fatalf("len(members) = %d, want 2: %v", len(members), members)
	}
	okMemberCases, ok := members["ok"].([]any)
	if !ok || len(okMemberCases) != 1 {
		t.Fatalf(`members["ok"] = %v, want a one-element cases list`, members["ok"])
	}
	okCase, ok := okMemberCases[0].(map[string]any)
	if !ok || okCase["sort"] != "boolean" {
		t.Errorf(`members["ok"][0] = %v, want {"sort":"boolean"}`, okMemberCases[0])
	}
	levelMemberCases, ok := members["level"].([]any)
	if !ok || len(levelMemberCases) != 1 {
		t.Fatalf(`members["level"] = %v, want a one-element cases list`, members["level"])
	}
	levelCase, ok := levelMemberCases[0].(map[string]any)
	if !ok || levelCase["sort"] != "number" {
		t.Errorf(`members["level"][0] = %v, want {"sort":"number",...}`, levelMemberCases[0])
	}
	if _, hasSet := levelCase["set"]; !hasSet {
		t.Errorf(`members["level"][0] carries no set: %v`, levelCase)
	}
}
