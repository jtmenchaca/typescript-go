// The cross-language edge, at the two grains this package can pin
// without a Node type environment.
//
// WHAT IS PINNED HERE, and why it stops where it does. The artifact
// reader is pure I/O plus the kernel's own set decoder, so every one of
// its premises — envelope, target integrity, runtime band, harness,
// channel purity's carried flag — is exercised against REAL temp files
// with real sha256 sums. The recognizer's syntactic halves (the argv
// reading, the options reading, the sole-parse scan) are exercised
// against real parsed sources.
//
// What is NOT pinned here is the whole-route ForeignEdgeAt on the
// fixture's own spelling: resolvesToChildProcessMember asks the
// checker for execFileSync's declaring file, and entryEnvTestProgram
// builds a program with the default lib alone — no @types/node, so the
// name resolves to nothing and the route declines before it reaches
// disk. Standing this up needs a program host carrying a
// child_process.d.ts, which is a service-level fixture rather than a
// walk-level one; the end-to-end verification is E4's own row
// (CROSS-LANGUAGE-EDGE.md §17), run against the real tutorial fixture.
// Naming that here rather than faking the resolution is the point.
//
// FILE MAP. This file holds the shared fixture builders every other
// foreign_edge_*_test.go file reuses (write*Target, *ArtifactJSON) and
// the core artifact-read tests (TestReadForeignArtifact_*: envelope,
// provenance span, staleness, target integrity, runtime band, harness,
// channel, version field, undecodable set). The orchestration-level
// tests (ForeignEdgeAt, the artifact-states-nothing policy split, the
// ConsumedForeignSink push) live in foreign_edge_policy_test.go; the
// ±Infinity return corner, the Result-shaped object return, and the
// bare-sort declared return live in foreign_edge_return_test.go.

package walk

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// foreignTargetSource is the Python body the artifacts below describe —
// its exact bytes are what the contentHash premise is checked against.
const foreignTargetSource = "import math\n\n\ndef audio_level(samples):\n" +
	"    return math.sqrt(sum(s * s for s in samples) / len(samples))\n"

// writeForeignTarget writes the .py file into a fresh temp directory and
// answers its path and the sha256 the artifact must state to match it.
func writeForeignTarget(t *testing.T) (string, string) {
	t.Helper()
	targetPath := filepath.Join(t.TempDir(), "audio_level.py")
	if err := os.WriteFile(targetPath, []byte(foreignTargetSource), 0o644); err != nil {
		t.Fatalf("writing the target: %v", err)
	}
	sum := sha256.Sum256([]byte(foreignTargetSource))
	return targetPath, "sha256:" + hex.EncodeToString(sum[:])
}

// foreignArtifactJSON builds the frozen-schema artifact text: a sequence
// entry of -2 … 2 with at least one element, a 0 … 1 return, and the
// two flags the premises read.
func foreignArtifactJSON(contentHash string, targetFile string, stdoutPure bool) string {
	pure := "false"
	if stdoutPure {
		pure = "true"
	}
	return `{
  "refined": {"kind": "fact-artifact"},
  "target": {"file": "` + targetFile + `", "contentHash": "` + contentHash + `"},
  "language": "python",
  "runtime": {"band": "cpython-3.11+"},
  "surface": {"kind": "stdin-json", "stdin": "json", "stdout": "json", "calls": "audio_level"},
  "functions": {
    "audio_level": {
      "entry": [{"name": "samples", "sequence": {
        "element": {"cases": [{"sort": "number", "set": {"forms": [{"form": "atLeast", "a": {"num": -2, "exp": 0}},
                              {"form": "atMost", "a": {"num": 2, "exp": 0}}]}}]},
        "lengthAtLeast": 1}}],
      "return": {"cases": [{"sort": "number", "set": {"forms": [{"form": "atLeast", "a": {"num": 0, "exp": 0}},
                                   {"form": "atMost", "a": {"num": 1, "exp": 0}}]}}],
                 "stdoutPure": ` + pure + `},
      "provenance": {"line": 5, "said": "math.sqrt of a mean of squares of -1 … 1 is 0 … 1"}
    }
  }
}`
}

// foreignScalarArgvTargetSource is the Python body the argv-scalar
// artifacts below describe — level_scalar_argv.py's own anatomy, kept
// tiny: one float parameter crossing on argv[1] alone, no stdin at all.
const foreignScalarArgvTargetSource = "def level_from_gain(gain):\n" +
	"    return min(1.0, gain / 4.0)\n"

// writeForeignScalarArgvTarget writes the argv-scalar target into a
// fresh temp directory and answers its path and the sha256 the
// artifact must state to match it.
func writeForeignScalarArgvTarget(t *testing.T) (string, string) {
	t.Helper()
	targetPath := filepath.Join(t.TempDir(), "level_scalar_argv.py")
	if err := os.WriteFile(targetPath, []byte(foreignScalarArgvTargetSource), 0o644); err != nil {
		t.Fatalf("writing the target: %v", err)
	}
	sum := sha256.Sum256([]byte(foreignScalarArgvTargetSource))
	return targetPath, "sha256:" + hex.EncodeToString(sum[:])
}

// foreignArgvScalarArtifactJSON builds an artifact stating the
// argv-scalar surface: one scalar entry 0 … 4 (level_scalar_argv.py's
// own Gain window), argIndex 1, calling level_from_gain.
func foreignArgvScalarArtifactJSON(contentHash string, targetFile string) string {
	return `{
  "refined": {"kind": "fact-artifact"},
  "target": {"file": "` + targetFile + `", "contentHash": "` + contentHash + `"},
  "language": "python",
  "runtime": {"band": "cpython-3.11+"},
  "surface": {"kind": "argv-scalar", "argIndex": 1, "parse": "float", "stdout": "json", "calls": "level_from_gain"},
  "functions": {
    "level_from_gain": {
      "entry": [{"name": "gain", "cases": [{"sort": "number", "set": {"forms": [{"form": "atLeast", "a": {"num": 0, "exp": 0}},
                                                    {"form": "atMost", "a": {"num": 4, "exp": 0}}]}}]}],
      "return": {"cases": [{"sort": "number", "set": {"forms": [{"form": "atLeast", "a": {"num": 0, "exp": 0}},
                                   {"form": "atMost", "a": {"num": 1, "exp": 0}}]}}],
                 "stdoutPure": true},
      "provenance": {"line": 2, "said": "min(1.0, gain / 4.0) is 0 … 1 for gain 0 … 4"}
    }
  }
}`
}

// foreignMixedTargetSource is the Python body the mixed-surface
// artifacts below describe — level_gain_argv.py's own real anatomy
// (samples on stdin, gain on argv[1]), kept exactly as the real target
// reads for it.
const foreignMixedTargetSource = "import math\n\n\n" +
	"def level_gain_argv(samples, gain):\n" +
	"    clamped = [max(-1.0, min(1.0, s * gain)) for s in samples]\n" +
	"    total = sum(s * s for s in clamped)\n" +
	"    return math.sqrt(total / len(samples))\n"

// writeForeignMixedTarget writes the mixed-surface target into a fresh
// temp directory and answers its path and the sha256 the artifact must
// state to match it.
func writeForeignMixedTarget(t *testing.T) (string, string) {
	t.Helper()
	targetPath := filepath.Join(t.TempDir(), "level_gain_argv.py")
	if err := os.WriteFile(targetPath, []byte(foreignMixedTargetSource), 0o644); err != nil {
		t.Fatalf("writing the target: %v", err)
	}
	sum := sha256.Sum256([]byte(foreignMixedTargetSource))
	return targetPath, "sha256:" + hex.EncodeToString(sum[:])
}

// foreignMixedArtifactJSON builds an artifact stating the mixed
// stdin-json-argv-scalar surface: entry[0] a sequence -2 … 2 at least
// one element (the stdin leg, "samples"), entry[1] a scalar 0 … 4 (the
// argv leg, "gain"), argIndex 1, calling level_gain_argv.
func foreignMixedArtifactJSON(contentHash string, targetFile string) string {
	return `{
  "refined": {"kind": "fact-artifact"},
  "target": {"file": "` + targetFile + `", "contentHash": "` + contentHash + `"},
  "language": "python",
  "runtime": {"band": "cpython-3.11+"},
  "surface": {"kind": "stdin-json-argv-scalar", "stdin": "json", "argIndex": 1, "parse": "float",
              "stdout": "json", "calls": "level_gain_argv"},
  "functions": {
    "level_gain_argv": {
      "entry": [{"name": "samples", "sequence": {
                  "element": {"cases": [{"sort": "number", "set": {"forms": [{"form": "atLeast", "a": {"num": -2, "exp": 0}},
                                        {"form": "atMost", "a": {"num": 2, "exp": 0}}]}}]},
                  "lengthAtLeast": 1}},
                {"name": "gain", "cases": [{"sort": "number", "set": {"forms": [{"form": "atLeast", "a": {"num": 0, "exp": 0}},
                                                    {"form": "atMost", "a": {"num": 4, "exp": 0}}]}}]}],
      "return": {"cases": [{"sort": "number", "set": {"forms": [{"form": "atLeast", "a": {"num": 0, "exp": 0}},
                                   {"form": "atMost", "a": {"num": 1, "exp": 0}}]}}],
                 "stdoutPure": true},
      "provenance": {"line": 4, "said": "clamped levels stay 0 … 1 for samples -2 … 2 and gain 0 … 4"}
    }
  }
}`
}

// foreignFileTargetSource is the Python body the file-json artifacts
// below describe — level_from_file.py's own real anatomy (samples read
// as JSON from the file named at argv[1]), kept exactly as the real
// target reads for it.
const foreignFileTargetSource = "import json\nimport math\nimport sys\n\n\n" +
	"def level_from_file(samples):\n" +
	"    clamped = [max(-1.0, min(1.0, s)) for s in samples]\n" +
	"    total = sum(s * s for s in clamped)\n" +
	"    return math.sqrt(total / len(samples))\n"

// writeForeignFileTarget writes the file-json target into a fresh temp
// directory and answers its path and the sha256 the artifact must
// state to match it.
func writeForeignFileTarget(t *testing.T) (string, string) {
	t.Helper()
	targetPath := filepath.Join(t.TempDir(), "level_from_file.py")
	if err := os.WriteFile(targetPath, []byte(foreignFileTargetSource), 0o644); err != nil {
		t.Fatalf("writing the target: %v", err)
	}
	sum := sha256.Sum256([]byte(foreignFileTargetSource))
	return targetPath, "sha256:" + hex.EncodeToString(sum[:])
}

// foreignFileJSONArtifactJSON builds an artifact stating the file-json
// surface: one sequence entry -2 … 2 at least one element ("samples",
// the file's own JSON content), argIndex 1 (the argv position naming
// the file), calling level_from_file.
func foreignFileJSONArtifactJSON(contentHash string, targetFile string) string {
	return `{
  "refined": {"kind": "fact-artifact"},
  "target": {"file": "` + targetFile + `", "contentHash": "` + contentHash + `"},
  "language": "python",
  "runtime": {"band": "cpython-3.11+"},
  "surface": {"kind": "file-json", "argIndex": 1, "stdout": "json", "calls": "level_from_file"},
  "functions": {
    "level_from_file": {
      "entry": [{"name": "samples", "sequence": {
                  "element": {"cases": [{"sort": "number", "set": {"forms": [{"form": "atLeast", "a": {"num": -2, "exp": 0}},
                                        {"form": "atMost", "a": {"num": 2, "exp": 0}}]}}]},
                  "lengthAtLeast": 1}}],
      "return": {"cases": [{"sort": "number", "set": {"forms": [{"form": "atLeast", "a": {"num": 0, "exp": 0}},
                                   {"form": "atMost", "a": {"num": 1, "exp": 0}}]}}],
                 "stdoutPure": true},
      "provenance": {"line": 6, "said": "clamped levels stay 0 … 1 for samples -2 … 2"}
    }
  }
}`
}

// writeForeignArtifact drops an artifact text at the target's cache
// entry and clears the read memo, so each case reads its own file
// rather than a previous case's answer. Every test also pins the
// producer resolution to a dead path, so a developer's PATH (or a
// project-root build) cannot turn a missing-artifact case into a live
// export.
func writeForeignArtifact(t *testing.T, targetPath string, text string) {
	t.Helper()
	SetPythonProducerPath("/nonexistent/refinedpy-check")
	t.Cleanup(func() { SetPythonProducerPath("") })
	artifactPath := ForeignCacheArtifactPath(targetPath)
	if err := os.MkdirAll(filepath.Dir(artifactPath), 0o755); err != nil {
		t.Fatalf("creating the cache directory: %v", err)
	}
	if err := os.WriteFile(artifactPath, []byte(text), 0o644); err != nil {
		t.Fatalf("writing the artifact: %v", err)
	}
	forgetForeignArtifact(targetPath)
}

// forgetForeignArtifact drops the memoized row for one target — the
// memo is held for the process, and a test that rewrites a file must
// not read the previous file's verdict.
func forgetForeignArtifact(targetPath string) {
	foreignArtifactsMu.Lock()
	delete(foreignArtifacts, targetPath)
	foreignArtifactsMu.Unlock()
}

func TestReadForeignArtifact_AWellFormedArtifactAnswersTheCalledFunctionsFact(t *testing.T) {
	targetPath, contentHash := writeForeignTarget(t)
	writeForeignArtifact(t, targetPath, foreignArtifactJSON(contentHash, targetPath, true))

	artifact, sentence := ReadForeignArtifact(targetPath)
	if sentence != "" {
		t.Fatalf("a well-formed artifact declined: %s", sentence)
	}
	if artifact.Called.Name != "audio_level" {
		t.Errorf("Called.Name = %q, want audio_level — the harness names it", artifact.Called.Name)
	}
	if artifact.RuntimeBand != ForeignRuntimeBand {
		t.Errorf("RuntimeBand = %q, want %q", artifact.RuntimeBand, ForeignRuntimeBand)
	}
	if len(artifact.Called.Entry) != 1 {
		t.Fatalf("len(Entry) = %d, want 1: %+v", len(artifact.Called.Entry), artifact.Called.Entry)
	}
	entry := artifact.Called.Entry[0]
	if !entry.IsSequence || entry.LengthAtLeast != 1 {
		t.Errorf("Entry[0] = %+v, want a sequence with lengthAtLeast 1", entry)
	}
	// the element case's set came through the kernel's own decoder, so
	// it is the same object a kernel answer would have been
	if len(entry.ElementCases) != 1 || entry.ElementCases[0].Sort != CaseSortNumber {
		t.Fatalf("entry.ElementCases = %+v, want exactly one number case", entry.ElementCases)
	}
	if words := foreignSetWords(entry.ElementCases[0].Set); !strings.Contains(words, "-2") || !strings.Contains(words, "2") {
		t.Errorf("the entry element reads %q, want the -2 … 2 window the artifact states", words)
	}
	if !artifact.Called.Return.StdoutPure {
		t.Errorf("Return.StdoutPure = false, and the artifact states true")
	}
	if got := artifact.Called.Provenance.ProvenanceSentence(); !strings.Contains(got, ":5") {
		t.Errorf("ProvenanceSentence() = %q, want the target's line 5 in it", got)
	}
}

// TestReadForeignArtifact_ProvenanceSpanPointsAtTheStatedLinesOwnStart pins
// the offset computation itself, against foreignTargetSource's real 5
// lines and the fixture artifact's "line": 5 provenance: the span must
// start exactly where line 5 starts (column 1) and run to line 5's own
// length, with no trailing newline folded in, so foreign_edge.go's
// StepInForeignFile(File, Text, Start, Length, Said) call lands on the
// right characters without re-reading the file itself.
func TestReadForeignArtifact_ProvenanceSpanPointsAtTheStatedLinesOwnStart(t *testing.T) {
	targetPath, contentHash := writeForeignTarget(t)
	writeForeignArtifact(t, targetPath, foreignArtifactJSON(contentHash, targetPath, true))

	artifact, sentence := ReadForeignArtifact(targetPath)
	if sentence != "" {
		t.Fatalf("a well-formed artifact declined: %s", sentence)
	}

	provenance := artifact.Called.Provenance
	if provenance.Text != foreignTargetSource {
		t.Fatalf("Provenance.Text = %q, want the target's own bytes (no re-read, no rewrite)", provenance.Text)
	}

	const line5 = "    return math.sqrt(sum(s * s for s in samples) / len(samples))"
	wantStart := strings.Index(foreignTargetSource, line5)
	if wantStart < 0 {
		t.Fatalf("test fixture drift: foreignTargetSource no longer contains %q", line5)
	}
	if provenance.Start != wantStart {
		t.Errorf("Provenance.Start = %d, want %d — line 5's own byte offset", provenance.Start, wantStart)
	}
	if provenance.Length != len(line5) {
		t.Errorf("Provenance.Length = %d, want %d — line 5's length with no trailing newline", provenance.Length, len(line5))
	}
	// the span must land EXACTLY on line 5's text, neither short nor
	// spilling into the newline or the next line (there is none here,
	// but a spill would still be a wrong length)
	if got := foreignTargetSource[provenance.Start : provenance.Start+provenance.Length]; got != line5 {
		t.Errorf("the computed span reads %q, want line 5 verbatim: %q", got, line5)
	}
}

func TestReadForeignArtifact_AMissingArtifactNamesTheFileAndTheCommandThatWritesIt(t *testing.T) {
	targetPath, _ := writeForeignTarget(t)
	SetPythonProducerPath("/nonexistent/refinedpy-check")
	t.Cleanup(func() { SetPythonProducerPath("") })
	forgetForeignArtifact(targetPath)

	artifact, sentence := ReadForeignArtifact(targetPath)
	if artifact != nil {
		t.Fatalf("a missing artifact answered a fact: %+v", artifact)
	}
	if !strings.Contains(sentence, ForeignCacheArtifactPath(targetPath)) {
		t.Errorf("the sentence %q does not name the cache entry that is missing", sentence)
	}
	if !strings.Contains(sentence, ForeignExportCommand) {
		t.Errorf("the sentence %q does not name the command that writes it — a missing fact is a work-queue item, not a silence", sentence)
	}
}

// writeFakeProducerScript writes a tiny shell script standing in for
// refinedpy-check: run as `<script> --export-fact <target> -o <out>`,
// it writes artifactText to the `-o` path verbatim, ignoring every
// other argument — enough to exercise exportForeignArtifact's own
// exec.Command call without a real Rust binary in reach.
func writeFakeProducerScript(t *testing.T, dir string, artifactText string) string {
	t.Helper()
	scriptPath := filepath.Join(dir, "fake-producer.sh")
	script := "#!/bin/sh\n" +
		"out=\"\"\n" +
		"prev=\"\"\n" +
		"for arg in \"$@\"; do\n" +
		"  if [ \"$prev\" = \"-o\" ]; then out=\"$arg\"; fi\n" +
		"  prev=\"$arg\"\n" +
		"done\n" +
		"cat > \"$out\" <<'ARTIFACT'\n" + artifactText + "\nARTIFACT\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("writing the fake producer script: %v", err)
	}
	return scriptPath
}

// TestReadForeignArtifact_AProducerBinaryNewerThanTheArtifactReExports
// pins the RULED staleness rule: the content hash alone cannot notice
// a REBUILT producer deriving a different fact for the SAME target
// bytes, so the resolved producer binary's own mtime, compared against
// the cache entry's, is the second freshness signal. A cached artifact
// that reads cleanly is still re-exported when the producer is newer —
// no stamps, no counters, just the two files' mtimes.
func TestReadForeignArtifact_AProducerBinaryNewerThanTheArtifactReExports(t *testing.T) {
	targetPath, contentHash := writeForeignTarget(t)
	writeForeignArtifact(t, targetPath, foreignArtifactJSON(contentHash, targetPath, true))
	artifactPath := ForeignCacheArtifactPath(targetPath)

	// back-date the cache entry so the fake producer (written just now,
	// by this test) reads unambiguously newer — mtime comparisons need a
	// real gap, not a same-instant race
	old := time.Now().Add(-1 * time.Hour)
	if err := os.Chtimes(artifactPath, old, old); err != nil {
		t.Fatalf("back-dating the cache entry: %v", err)
	}

	rebuiltArtifact := strings.Replace(
		foreignArtifactJSON(contentHash, targetPath, true),
		`"said": "math.sqrt of a mean of squares of -1 … 1 is 0 … 1"`,
		`"said": "a rebuilt producer's own sentence"`, 1)
	producer := writeFakeProducerScript(t, t.TempDir(), rebuiltArtifact)
	SetPythonProducerPath(producer)
	t.Cleanup(func() { SetPythonProducerPath("") })
	forgetForeignArtifact(targetPath)

	artifact, sentence := ReadForeignArtifact(targetPath)
	if sentence != "" {
		t.Fatalf("the re-exported artifact declined: %s", sentence)
	}
	if artifact.Called.Provenance.Said != "a rebuilt producer's own sentence" {
		t.Errorf("Called.Provenance.Said = %q, want the REBUILT producer's own sentence — "+
			"a newer producer binary must trigger a re-export even though the cached artifact "+
			"already read cleanly and the target's content hash never changed", artifact.Called.Provenance.Said)
	}
}

// TestReadForeignArtifact_AnOlderProducerBinaryDoesNotReExport is the
// mirror: a producer binary OLDER than the cache entry (the ordinary
// case once a rebuilt producer has already re-exported once) must NOT
// trigger a second re-export — the cached artifact's own provenance
// reads through unchanged.
func TestReadForeignArtifact_AnOlderProducerBinaryDoesNotReExport(t *testing.T) {
	targetPath, contentHash := writeForeignTarget(t)
	writeForeignArtifact(t, targetPath, foreignArtifactJSON(contentHash, targetPath, true))

	producer := writeFakeProducerScript(t, t.TempDir(), "this text must never be read")
	old := time.Now().Add(-1 * time.Hour)
	if err := os.Chtimes(producer, old, old); err != nil {
		t.Fatalf("back-dating the producer: %v", err)
	}
	SetPythonProducerPath(producer)
	t.Cleanup(func() { SetPythonProducerPath("") })
	forgetForeignArtifact(targetPath)

	artifact, sentence := ReadForeignArtifact(targetPath)
	if sentence != "" {
		t.Fatalf("the cached artifact declined: %s", sentence)
	}
	if artifact.Called.Provenance.Said != "math.sqrt of a mean of squares of -1 … 1 is 0 … 1" {
		t.Errorf("Called.Provenance.Said = %q, want the ORIGINAL cached sentence — an older producer "+
			"binary must not trigger a re-export", artifact.Called.Provenance.Said)
	}
}

func TestReadForeignArtifact_AStaleContentHashDeclinesNamingTargetIntegrity(t *testing.T) {
	targetPath, contentHash := writeForeignTarget(t)
	writeForeignArtifact(t, targetPath, foreignArtifactJSON(contentHash, targetPath, true))
	// the target changes AFTER the fact was exported: the claim is now
	// about code that is not the code being checked (§5)
	if err := os.WriteFile(targetPath, []byte(foreignTargetSource+"\n# edited\n"), 0o644); err != nil {
		t.Fatalf("rewriting the target: %v", err)
	}
	forgetForeignArtifact(targetPath)

	artifact, sentence := ReadForeignArtifact(targetPath)
	if artifact != nil {
		t.Fatalf("a stale hash answered a fact: %+v", artifact)
	}
	if !strings.Contains(sentence, "different code than the code being checked") {
		t.Errorf("the sentence %q does not name the target-integrity premise", sentence)
	}
}

func TestReadForeignArtifact_AnotherRuntimeBandDeclinesNamingIt(t *testing.T) {
	targetPath, contentHash := writeForeignTarget(t)
	text := strings.Replace(
		foreignArtifactJSON(contentHash, targetPath, true),
		`"band": "cpython-3.11+"`, `"band": "pypy-3.10"`, 1)
	writeForeignArtifact(t, targetPath, text)

	artifact, sentence := ReadForeignArtifact(targetPath)
	if artifact != nil {
		t.Fatalf("a foreign runtime band answered a fact: %+v", artifact)
	}
	if !strings.Contains(sentence, "pypy-3.10") || !strings.Contains(sentence, ForeignRuntimeBand) {
		t.Errorf("the sentence %q does not name both bands — the premise is which semantics the pins transcribe", sentence)
	}
}

func TestReadForeignArtifact_AHarnessCallingAnUnstatedFunctionDeclinesNamingIt(t *testing.T) {
	targetPath, contentHash := writeForeignTarget(t)
	text := strings.Replace(
		foreignArtifactJSON(contentHash, targetPath, true),
		`"calls": "audio_level"`, `"calls": "audio_level_unclamped"`, 1)
	writeForeignArtifact(t, targetPath, text)

	artifact, sentence := ReadForeignArtifact(targetPath)
	if artifact != nil {
		t.Fatalf("a harness naming an unstated function answered a fact: %+v", artifact)
	}
	if !strings.Contains(sentence, "audio_level_unclamped") {
		t.Errorf("the sentence %q does not name the function whose fact is missing", sentence)
	}
}

func TestReadForeignArtifact_ANonJsonHarnessDeclinesNamingTheChannel(t *testing.T) {
	targetPath, contentHash := writeForeignTarget(t)
	text := strings.Replace(
		foreignArtifactJSON(contentHash, targetPath, true),
		`"stdout": "json"`, `"stdout": "lines"`, 1)
	writeForeignArtifact(t, targetPath, text)

	if _, sentence := ReadForeignArtifact(targetPath); !strings.Contains(sentence, "JSON transport model") {
		t.Errorf("the sentence %q does not say the transport model has nothing to apply to", sentence)
	}
}

// TestReadForeignArtifact_AVersionFieldDeclinesAsSuperseded pins the
// RULED schema's own rule: NO version field, ever — an envelope
// carrying one at all (any value) is a superseded shape, declined by
// name rather than read as this-version-or-that.
func TestReadForeignArtifact_AVersionFieldDeclinesAsSuperseded(t *testing.T) {
	targetPath, contentHash := writeForeignTarget(t)
	text := strings.Replace(
		foreignArtifactJSON(contentHash, targetPath, true),
		`"refined": {"kind": "fact-artifact"}`, `"refined": {"kind": "fact-artifact", "version": 2}`, 1)
	writeForeignArtifact(t, targetPath, text)

	if _, sentence := ReadForeignArtifact(targetPath); !strings.Contains(sentence, "version") {
		t.Errorf("the sentence %q does not name the version field — the current schema states no version, ever", sentence)
	}
}

func TestReadForeignArtifact_AnUnreadableSetDeclinesRatherThanCrashingTheChecker(t *testing.T) {
	targetPath, contentHash := writeForeignTarget(t)
	// DecodeWireSet panics on a form it does not know; an artifact is a
	// file another program wrote, so the panic must become a decline
	text := strings.Replace(
		foreignArtifactJSON(contentHash, targetPath, true),
		`{"form": "atLeast", "a": {"num": -2, "exp": 0}}`,
		`{"form": "someFormFromTheFuture"}`, 1)
	writeForeignArtifact(t, targetPath, text)

	artifact, sentence := ReadForeignArtifact(targetPath)
	if artifact != nil {
		t.Fatalf("an undecodable set answered a fact: %+v", artifact)
	}
	if !strings.Contains(sentence, "kernel grammar") {
		t.Errorf("the sentence %q does not say the set could not be decoded", sentence)
	}
}
