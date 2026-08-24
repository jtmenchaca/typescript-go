// cross.6's TypeScript-consumer half: a recognized foreign-edge argv
// naming a COMPILED BINARY (no interpreter word, no .py/.ts extension
// the existing recognizers model) reads its fact from a SIBLING
// `<binary_path>.facts.json` file, in the RULED cases-schema envelope,
// and serves the edge exactly as a script target's fact serves — entry
// judged, return set bound.
//
// Mirrors the Rust twin's own compiled-binary test module
// (packages/refinedpy/pyrefly/crates/refinedpy/src/foreign_edge_artifact.rs
// and foreign_edge.rs): the same three-rung ladder (no sibling file →
// the named no-fact sentence; sibling exists but unparseable → decline
// naming the unreadable file; parsed → served), pinned here at the two
// grains this package can pin without a Node type environment — see
// foreign_edge_test.go's own banner for why a whole-route ForiegnEdgeAt
// call needs a service-level fixture rather than a walk-level one.
package walk

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

/* ── recognition ──────────────────────────────────────────────────── */

// A single argv element whose text carries a leading "./" recognizes
// as a compiled binary's own path — the triangle's own row.
func TestCompiledBinaryArgvOf_ARelativePathRecognizes(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function execFileSync(file: string, args: string[], options: unknown): string;
function f() {
	const stdout = execFileSync("./targets/cpp_level", [], { encoding: "utf8" });
	return stdout;
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	_, call, ok := constBoundCallOf(statements[0])
	if !ok {
		t.Fatalf("the const-bound execFileSync call was not read")
	}
	args, _ := callArguments(call)
	path, isBinary := compiledBinaryArgvOf(nil, args[0])
	if !isBinary || path != "./targets/cpp_level" {
		t.Errorf("path=%q isBinary=%v, want ./targets/cpp_level / true", path, isBinary)
	}
}

// "../" and a bare "/" both recognize too — the three path markers
// isCompiledBinaryPathShaped checks.
func TestIsCompiledBinaryPathShaped_RecognizesAllThreeMarkers(t *testing.T) {
	for _, path := range []string{"./cpp_level", "../targets/cpp_level", "/usr/local/bin/cpp_level"} {
		if !isCompiledBinaryPathShaped(path) {
			t.Errorf("isCompiledBinaryPathShaped(%q) = false, want true", path)
		}
	}
}

// A recognized python/uv runner word is never ALSO read as a compiled
// binary's own path — the two shapes are mutually exclusive, even
// though "python3" resolves to a string just as readily as a path
// does.
func TestCompiledBinaryArgvOf_ARecognizedRunnerWordIsNotABinaryPath(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function execFileSync(file: string, args: string[], options: unknown): string;
function f() {
	const stdout = execFileSync("python3", ["./audio_level.py"], { encoding: "utf8" });
	return stdout;
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	_, call, _ := constBoundCallOf(statements[0])
	args, _ := callArguments(call)
	if path, isBinary := compiledBinaryArgvOf(nil, args[0]); isBinary {
		t.Errorf("a recognized runner word %q read as a compiled-binary path", path)
	}
}

// A bare word with no path marker at all (an unrecognized, unmodeled
// interpreter spelling) is not read as a binary path either — the
// checker cannot tell the two apart by spelling alone once the path
// markers are absent.
func TestCompiledBinaryArgvOf_ABareWordWithNoPathMarkerDoesNotRecognize(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function execFileSync(file: string, args: string[], options: unknown): string;
function f() {
	const stdout = execFileSync("someinterpreter", ["./audio_level.py"], { encoding: "utf8" });
	return stdout;
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	_, call, _ := constBoundCallOf(statements[0])
	args, _ := callArguments(call)
	if path, isBinary := compiledBinaryArgvOf(nil, args[0]); isBinary {
		t.Errorf("a bare unmodeled word %q read as a compiled-binary path", path)
	}
}

// execFileSyncEdgeOf itself recognizes the whole call as a compiled-
// binary edge — IsCompiledBinary set, TargetPath resolved against the
// source file's own directory (the SAME relative-path rule every
// other recognized argv element in this file applies), no extension
// premise applied (unlike resolveForeignScriptPath's own ".py" check).
func TestExecFileSyncEdgeOf_ABareBinaryArgvRecognizesAsACompiledBinaryEdge(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function execFileSync(file: string, args: string[], options: unknown): string;
function f(boosted: number[]) {
	const stdout = execFileSync("./targets/cpp_level", [], {
		input: JSON.stringify(boosted),
		encoding: "utf8",
	});
	return JSON.parse(stdout);
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	name, call, ok := constBoundCallOf(statements[0])
	if !ok {
		t.Fatalf("the const-bound execFileSync call was not read")
	}
	edge, recognized, sentence, _, _ := execFileSyncEdgeOf(nil, call, name, statements, 0)
	if sentence != "" {
		t.Fatalf("the compiled-binary edge declined: %s", sentence)
	}
	if !recognized || edge == nil {
		t.Fatalf("a bare compiled-binary argv was not recognized")
	}
	if !edge.IsCompiledBinary {
		t.Errorf("edge.IsCompiledBinary = false, want true")
	}
	if !strings.HasSuffix(edge.TargetPath, filepath.Join("targets", "cpp_level")) {
		t.Errorf("edge.TargetPath = %q, want it to end in targets/cpp_level", edge.TargetPath)
	}
}

/* ── the sibling fact-file ladder ────────────────────────────────── */

// compiledBinaryFactJSON is a well-formed compiled-binary fact —
// cpp_level's own contract in miniature: one sequence entry (numbers
// -2…2, at least one element) and a number 0…1 return, through the
// identical RULED cases envelope the TypeScript-target reader already
// applies, with language "cpp" and the c++17 runtime band in place of
// the Python reading.
func compiledBinaryFactJSON(called string) string {
	return `{
  "refined": {"kind": "fact-artifact"},
  "language": "cpp",
  "runtime": {"band": "c++17"},
  "surface": {"kind": "stdin-json", "stdin": "json", "stdout": "json", "calls": "` + called + `"},
  "functions": {
    "` + called + `": {
      "entry": [{"name": "samples", "sequence": {
        "element": {"cases": [{"sort": "number", "set": {"forms": [{"form": "atLeast", "a": {"num": -2, "exp": 0}},
                                {"form": "atMost", "a": {"num": 2, "exp": 0}}]}}]},
        "lengthAtLeast": 1}}],
      "return": {"cases": [{"sort": "number", "set": {"forms": [{"form": "atLeast", "a": {"num": 0, "exp": 0}},
                             {"form": "atMost", "a": {"num": 1, "exp": 0}}]}}],
                 "stdoutPure": true},
      "provenance": {"line": 35, "said": "clamp, mean of squares, sqrt"}
    }
  }
}`
}

// discovery + parse + serve: a real sibling <binary>.facts.json beside
// the binary path reads cleanly and binds the return set — the
// triangle's own designed row once cpp_level.facts.json exists (it
// already does, at examples/cross-language/audio-level-triangle/
// targets/cpp_level.facts.json).
func TestReadCompiledBinaryArtifact_AWellFormedSiblingServes(t *testing.T) {
	root := t.TempDir()
	binaryPath := filepath.Join(root, "targets", "cpp_level")
	if err := os.MkdirAll(filepath.Dir(binaryPath), 0o755); err != nil {
		t.Fatalf("creating the targets dir: %v", err)
	}
	factPath := CompiledBinaryFactPath(binaryPath)
	if err := os.WriteFile(factPath, []byte(compiledBinaryFactJSON("level")), 0o644); err != nil {
		t.Fatalf("writing the sibling fact: %v", err)
	}

	artifact, exists, sentence := ReadCompiledBinaryArtifact(binaryPath)
	if sentence != "" {
		t.Fatalf("a well-formed sibling fact declined: %s", sentence)
	}
	if !exists {
		t.Fatalf("exists = false, want true — the sibling file is on disk")
	}
	if artifact == nil {
		t.Fatalf("artifact = nil, want a served fact")
	}
	if artifact.RuntimeBand != CompiledBinaryRuntimeBand {
		t.Errorf("RuntimeBand = %q, want %q", artifact.RuntimeBand, CompiledBinaryRuntimeBand)
	}
	if artifact.Called.Name != "level" {
		t.Errorf("Called.Name = %q, want level", artifact.Called.Name)
	}
	if len(artifact.Called.Entry) != 1 || !artifact.Called.Entry[0].IsSequence {
		t.Fatalf("Called.Entry = %+v, want one sequence entry", artifact.Called.Entry)
	}
	if len(artifact.Called.Return.Cases) != 1 || artifact.Called.Return.Cases[0].Sort != CaseSortNumber {
		t.Fatalf("Called.Return.Cases = %+v, want one number case", artifact.Called.Return.Cases)
	}
	if !artifact.Called.Return.StdoutPure {
		t.Errorf("Called.Return.StdoutPure = false, want true")
	}
}

// absent: no sibling file at all keeps the ladder's floor rung — the
// caller (readForeignEdgeArtifact) is the one that turns exists=false
// into CompiledBinaryNoFactSentence; ReadCompiledBinaryArtifact itself
// answers no sentence at this rung (there is nothing to name past "the
// file is not there", which the caller already knows how to say).
func TestReadCompiledBinaryArtifact_NoSiblingFileAnswersNotExists(t *testing.T) {
	root := t.TempDir()
	binaryPath := filepath.Join(root, "targets", "cpp_level")
	if err := os.MkdirAll(filepath.Dir(binaryPath), 0o755); err != nil {
		t.Fatalf("creating the targets dir: %v", err)
	}
	// deliberately no fact file written

	artifact, exists, sentence := ReadCompiledBinaryArtifact(binaryPath)
	if exists {
		t.Fatalf("exists = true, want false — no sibling file was written")
	}
	if artifact != nil {
		t.Errorf("artifact = %+v, want nil", artifact)
	}
	if sentence != "" {
		t.Errorf("sentence = %q, want \"\" — the caller names the no-fact sentence, not this reader", sentence)
	}
}

// unreadable: a sibling file exists but is not parseable JSON declines
// naming the unreadable file — told apart from the absent rung by
// exists=true.
func TestReadCompiledBinaryArtifact_AnUnparseableSiblingDeclinesNamingTheFile(t *testing.T) {
	root := t.TempDir()
	binaryPath := filepath.Join(root, "targets", "cpp_level")
	if err := os.MkdirAll(filepath.Dir(binaryPath), 0o755); err != nil {
		t.Fatalf("creating the targets dir: %v", err)
	}
	factPath := CompiledBinaryFactPath(binaryPath)
	if err := os.WriteFile(factPath, []byte("not json at all"), 0o644); err != nil {
		t.Fatalf("writing the malformed sibling: %v", err)
	}

	artifact, exists, sentence := ReadCompiledBinaryArtifact(binaryPath)
	if !exists {
		t.Fatalf("exists = false, want true — the sibling file IS on disk, just unreadable")
	}
	if artifact != nil {
		t.Errorf("artifact = %+v, want nil", artifact)
	}
	if !strings.Contains(sentence, factPath) {
		t.Errorf("sentence = %q, want it to name %q", sentence, factPath)
	}
}

// a sibling that carries a "version" field, or the wrong language, is
// the same NO-FACT superseded-shape decline every other envelope
// mismatch earns in this tree — named, not silently tolerated.
func TestReadCompiledBinaryArtifact_AWrongLanguageDeclinesNamingTheEnvelope(t *testing.T) {
	root := t.TempDir()
	binaryPath := filepath.Join(root, "targets", "cpp_level")
	if err := os.MkdirAll(filepath.Dir(binaryPath), 0o755); err != nil {
		t.Fatalf("creating the targets dir: %v", err)
	}
	wrongLanguage := `{
  "refined": {"kind": "fact-artifact"},
  "language": "python",
  "runtime": {"band": "cpython-3.11+"},
  "surface": {"kind": "stdin-json", "stdin": "json", "stdout": "json", "calls": "level"},
  "functions": {"level": {"entry": [], "return": {"cases": [{"sort": "number", "set": {"forms": []}}], "stdoutPure": true}}}
}`
	factPath := CompiledBinaryFactPath(binaryPath)
	if err := os.WriteFile(factPath, []byte(wrongLanguage), 0o644); err != nil {
		t.Fatalf("writing the sibling: %v", err)
	}

	_, exists, sentence := ReadCompiledBinaryArtifact(binaryPath)
	if !exists {
		t.Fatalf("exists = false, want true")
	}
	if !strings.Contains(sentence, `language "cpp"`) {
		t.Errorf("sentence = %q, want it to name the required language \"cpp\"", sentence)
	}
}

/* ── readForeignEdgeArtifact's own routing ──────────────────────── */

// readForeignEdgeArtifact routes a compiled-binary edge to
// ReadCompiledBinaryArtifact's ladder, and turns its own "no sibling"
// rung into CompiledBinaryNoFactSentence — the generic sentence naming
// the construct, never the Python reader's "-export-fact" wording,
// which has no meaning for a compiled binary.
func TestReadForeignEdgeArtifact_ACompiledBinaryEdgeWithNoSiblingNamesTheConstruct(t *testing.T) {
	root := t.TempDir()
	binaryPath := filepath.Join(root, "targets", "cpp_level")
	if err := os.MkdirAll(filepath.Dir(binaryPath), 0o755); err != nil {
		t.Fatalf("creating the targets dir: %v", err)
	}
	edge := &ForeignEdge{TargetPath: binaryPath, IsCompiledBinary: true}

	artifact, sentence := readForeignEdgeArtifact(edge)
	if artifact != nil {
		t.Errorf("artifact = %+v, want nil", artifact)
	}
	want := CompiledBinaryNoFactSentence(binaryPath)
	if sentence != want {
		t.Errorf("sentence = %q, want %q", sentence, want)
	}
	if !strings.Contains(sentence, "is a compiled binary") {
		t.Errorf("sentence = %q, want it to name the compiled-binary construct", sentence)
	}
	if strings.Contains(sentence, "-export-fact") {
		t.Errorf("sentence = %q, wrongly names the Python producer's export command", sentence)
	}
}

// readForeignEdgeArtifact serves a compiled-binary edge whose sibling
// fact parses cleanly — the SAME artifact ReadCompiledBinaryArtifact
// itself answers, routed through unchanged.
func TestReadForeignEdgeArtifact_ACompiledBinaryEdgeWithAWellFormedSiblingServes(t *testing.T) {
	root := t.TempDir()
	binaryPath := filepath.Join(root, "targets", "cpp_level")
	if err := os.MkdirAll(filepath.Dir(binaryPath), 0o755); err != nil {
		t.Fatalf("creating the targets dir: %v", err)
	}
	factPath := CompiledBinaryFactPath(binaryPath)
	if err := os.WriteFile(factPath, []byte(compiledBinaryFactJSON("level")), 0o644); err != nil {
		t.Fatalf("writing the sibling fact: %v", err)
	}
	edge := &ForeignEdge{TargetPath: binaryPath, IsCompiledBinary: true}

	artifact, sentence := readForeignEdgeArtifact(edge)
	if sentence != "" {
		t.Fatalf("a well-formed compiled-binary edge declined: %s", sentence)
	}
	if artifact == nil {
		t.Fatalf("artifact = nil, want a served fact")
	}
	if artifact.Called.Name != "level" {
		t.Errorf("Called.Name = %q, want level", artifact.Called.Name)
	}
}

// a NON-compiled-binary edge still routes to the ordinary
// ReadForeignArtifact project-cache path, unchanged — this construct
// adds a new rung without touching the Python-target route.
func TestReadForeignEdgeArtifact_ANonCompiledBinaryEdgeRoutesToTheProjectCacheReader(t *testing.T) {
	targetPath, contentHash := writeForeignTarget(t)
	root := filepath.Dir(targetPath)
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("marking the temp root: %v", err)
	}
	SetProjectRootOverride(root)
	t.Cleanup(func() { SetProjectRootOverride("") })
	SetPythonProducerPath("/nonexistent/refinedpy-check")
	t.Cleanup(func() { SetPythonProducerPath("") })

	artifactPath := ForeignCacheArtifactPath(targetPath)
	if err := os.MkdirAll(filepath.Dir(artifactPath), 0o755); err != nil {
		t.Fatalf("creating the cache dir: %v", err)
	}
	if err := os.WriteFile(artifactPath, []byte(foreignArtifactJSON(contentHash, targetPath, true)), 0o644); err != nil {
		t.Fatalf("writing the artifact: %v", err)
	}
	forgetForeignArtifact(targetPath)

	edge := &ForeignEdge{TargetPath: targetPath, IsCompiledBinary: false}
	artifact, sentence := readForeignEdgeArtifact(edge)
	if sentence != "" {
		t.Fatalf("the ordinary Python-target edge declined: %s", sentence)
	}
	if artifact == nil {
		t.Fatalf("artifact = nil, want the project-cache fact")
	}
}
