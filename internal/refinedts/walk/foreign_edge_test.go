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

package walk

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
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

/* ── the outbound leg, against a real kernel ─────────────────────── */

// foreignCrossingEnv seeds the value that crosses out under one name,
// as a repetition of a bounded element — the shape `boosted` wears in
// the tutorial fixture after the map derivation.
func foreignCrossingEnv(name string, lo float64, hi float64, atLeast int) Env {
	env := NewEnv()
	element := refinementsets.MakeRefinedSet(
		refinementsets.AtLeast(lo), refinementsets.AtMost(hi))
	env.Set(name, abstractdomain.KnownSet(
		refinementsets.Repetition(element, atLeast, nil), nil,
		abstractdomain.TrustProved, abstractdomain.SetKindTagNone))
	return env
}

// foreignEdgeFixture is the whole outbound-leg setup: an artifact read
// off disk, a context carrying a real kernel and a collecting sink, an
// env holding the crossing value, and the node it is judged at.
type foreignEdgeFixture struct {
	artifact *ForeignArtifact
	ctx      *FlowContext
	env      Env
	edge     *ForeignEdge
	reported *[]assignability.RefinementDiagnostic
}

func foreignOutboundFixture(t *testing.T, lo float64, hi float64, atLeast int) foreignEdgeFixture {
	t.Helper()
	targetPath, contentHash := writeForeignTarget(t)
	writeForeignArtifact(t, targetPath, foreignArtifactJSON(contentHash, targetPath, true))
	artifact, sentence := ReadForeignArtifact(targetPath)
	if sentence != "" {
		t.Fatalf("the fixture artifact declined: %s", sentence)
	}
	p := entryEnvTestProgram(t, "function f(boosted: number[]) { boosted; }\n")
	fn := entryEnvFunctionNamed(t, p, "f")
	payload := fn.AsFunctionDeclaration().Body.AsBlock().Statements.Nodes[0].
		AsExpressionStatement().Expression
	var reported []assignability.RefinementDiagnostic
	ctx := &FlowContext{
		P:         p,
		Kernel:    nanWrapperLoadKernel(t),
		Contracts: map[*ast.Symbol]*FunctionContract{},
		Aliases:   dataflowfacts.NewAliasClasses(),
		Declared:  map[string]*annotations.DeclaredRefinement{},
		Report:    func(d assignability.RefinementDiagnostic) { reported = append(reported, d) },
	}
	return foreignEdgeFixture{
		artifact: artifact,
		ctx:      ctx,
		env:      foreignCrossingEnv("boosted", lo, hi, atLeast),
		edge:     &ForeignEdge{Call: payload, TargetPath: targetPath, Payload: payload, StdoutName: "stdout"},
		reported: &reported,
	}
}

// foreignLiteralArrayOutboundFixture is the array-LITERAL counterpart of
// foreignOutboundFixture: the payload identifier reads `samples`'s bare
// array literal — declared either at MODULE scope (declareSamplesInModule
// true, d-data-legs.ts's own shape), where the identifier resolves through
// evaluateExpression's env.Get miss into UntrackedIdentifier's module-const
// follow, or inside the function body one statement above the call, where
// the const's own preceding statement is walked first (AnalyzeVariableStatement)
// so env carries exactly the binding an ordinary function-local const
// leaves behind — the ordinary env.Get hit, never the module-const follow.
// audio_level.py's own entry (−2 … 2, lengthAtLeast 1) is the target
// throughout — the payload literal's own values decide whether the
// crossing fits, not the fixture itself.
func foreignLiteralArrayOutboundFixture(
	t *testing.T, samplesLiteral string, declareSamplesInModule bool,
) foreignEdgeFixture {
	t.Helper()
	targetPath, contentHash := writeForeignTarget(t)
	writeForeignArtifact(t, targetPath, foreignArtifactJSON(contentHash, targetPath, true))
	artifact, sentence := ReadForeignArtifact(targetPath)
	if sentence != "" {
		t.Fatalf("the fixture artifact declined: %s", sentence)
	}
	var source string
	var callStatementIndex int
	if declareSamplesInModule {
		source = "const samples = " + samplesLiteral + ";\n" +
			"function f() {\n" +
			"	samples;\n" +
			"}\n"
		callStatementIndex = 0
	} else {
		source = "function f() {\n" +
			"	const samples = " + samplesLiteral + ";\n" +
			"	samples;\n" +
			"}\n"
		callStatementIndex = 1
	}
	p := entryEnvTestProgram(t, source)
	statements := relationalAccumulationBodyOf(t, p, "f")
	payload := statements[callStatementIndex].AsExpressionStatement().Expression
	var reported []assignability.RefinementDiagnostic
	ctx := &FlowContext{
		P:         p,
		Kernel:    nanWrapperLoadKernel(t),
		Contracts: map[*ast.Symbol]*FunctionContract{},
		Aliases:   dataflowfacts.NewAliasClasses(),
		Declared:  map[string]*annotations.DeclaredRefinement{},
		Report:    func(d assignability.RefinementDiagnostic) { reported = append(reported, d) },
	}
	env := NewEnv()
	if !declareSamplesInModule {
		// the ordinary local-binding path: `samples`'s own preceding const
		// statement is walked for real, so env holds exactly what an
		// ordinary function-local const leaves behind — no synthetic seed
		AnalyzeVariableStatement(ctx, env, statements[0])
	}
	return foreignEdgeFixture{
		artifact: artifact,
		ctx:      ctx,
		env:      env,
		edge:     &ForeignEdge{Call: payload, TargetPath: targetPath, Payload: payload, StdoutName: "stdout"},
		reported: &reported,
	}
}

func TestCheckOutboundLeg_AValueInsideTheStatedEntryPassesEveryPremise(t *testing.T) {
	// -2 … 2, at least one element: exactly what the artifact admits
	fixture := foreignOutboundFixture(t, -2, 2, 1)
	if outcome := checkOutboundLeg(fixture.ctx, fixture.env, fixture.edge, fixture.artifact); outcome != nil {
		t.Fatalf("a fitting crossing did not pass: %+v", outcome)
	}
	if len(*fixture.reported) != 0 {
		t.Errorf("a fitting crossing reported %d diagnostics: %+v", len(*fixture.reported), *fixture.reported)
	}
}

func TestCheckOutboundLeg_ElementsOutsideTheStatedEntryFireAtTheCall(t *testing.T) {
	// -4 … 4 is not inside the target's -2 … 2: the value can escape what
	// the target states it accepts, which is a defect in THIS program
	fixture := foreignOutboundFixture(t, -4, 4, 1)
	outcome := checkOutboundLeg(fixture.ctx, fixture.env, fixture.edge, fixture.artifact)
	if outcome == nil {
		t.Fatalf("a crossing outside the stated entry passed")
	}
	if outcome.Decline != "" {
		t.Fatalf("a crossing outside the stated entry DECLINED (%q); a fit failure is a refutation", outcome.Decline)
	}
	if len(*fixture.reported) != 1 {
		t.Fatalf("reported %d diagnostics, want exactly 1: %+v", len(*fixture.reported), *fixture.reported)
	}
	fired := (*fixture.reported)[0]
	if fired.Code != 7001 {
		t.Errorf("Code = %d, want 7001 — the target admits -2 … 2 and the value reaches ±4", fired.Code)
	}
	if !strings.Contains(fired.MessageText, "audio_level") {
		t.Errorf("the message %q does not name the target function", fired.MessageText)
	}
	// the provenance no longer rides the message text — it is the second
	// step of the two-language explanation, a related step in the Python
	// file itself
	if strings.Contains(fired.MessageText, "said:") {
		t.Errorf("the message %q still concatenates the flat provenance sentence", fired.MessageText)
	}
	if len(fired.Steps) != 1 {
		t.Fatalf("want exactly 1 related step (the target's provenance line), got %d: %+v", len(fired.Steps), fired.Steps)
	}
	step := fired.Steps[0]
	if step.ForeignFile != fixture.artifact.TargetFile {
		t.Errorf("the step's ForeignFile = %q, want the target path %q", step.ForeignFile, fixture.artifact.TargetFile)
	}
	if step.Sentence != "math.sqrt of a mean of squares of -1 … 1 is 0 … 1" {
		t.Errorf("the step's Sentence = %q, want the artifact's own provenance.said", step.Sentence)
	}
	// line 5 of foreignTargetSource is "    return math.sqrt(...)" — the
	// step's span must start there, not merely name the file
	wantStart := strings.Index(foreignTargetSource, "    return math.sqrt")
	if step.Start != wantStart {
		t.Errorf("the step's Start = %d, want %d (the start of line 5)", step.Start, wantStart)
	}
	wantLength := len("    return math.sqrt(sum(s * s for s in samples) / len(samples))")
	if step.Length != wantLength {
		t.Errorf("the step's Length = %d, want %d (line 5's own length, no trailing newline)", step.Length, wantLength)
	}
}

func TestCheckOutboundLeg_AShorterSequenceThanTheTargetReliesOnFires(t *testing.T) {
	// elements fit, but the target's body divides by len and the artifact
	// states lengthAtLeast 1 — a possibly-empty sequence is a different
	// program on the other side
	fixture := foreignOutboundFixture(t, -2, 2, 0)
	outcome := checkOutboundLeg(fixture.ctx, fixture.env, fixture.edge, fixture.artifact)
	if outcome == nil || outcome.Decline != "" {
		t.Fatalf("a too-short crossing did not fire: %+v", outcome)
	}
	if len(*fixture.reported) != 1 || (*fixture.reported)[0].Code != 7001 {
		t.Fatalf("want exactly one 7001 for the length floor: %+v", *fixture.reported)
	}
	if !strings.Contains((*fixture.reported)[0].MessageText, "at least 1") {
		t.Errorf("the message %q does not carry the target's stated length floor", (*fixture.reported)[0].MessageText)
	}
}

func TestCheckOutboundLeg_APossiblyNaNCrossingFiresNamingTheStringifyBehaviour(t *testing.T) {
	fixture := foreignOutboundFixture(t, -2, 2, 1)
	// NaN stringifies to null (§4), so the target would receive a value
	// this program never computed
	fixture.env.Set("boosted", abstractdomain.PossiblyNaN(abstractdomain.KnownSet(
		refinementsets.MakeRefinedSet(refinementsets.AtLeast(-2), refinementsets.AtMost(2)),
		nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone)))

	outcome := checkOutboundLeg(fixture.ctx, fixture.env, fixture.edge, fixture.artifact)
	if outcome == nil || outcome.Decline != "" {
		t.Fatalf("a possibly-NaN crossing did not fire: %+v", outcome)
	}
	if len(*fixture.reported) != 1 || (*fixture.reported)[0].Code != 7001 {
		t.Fatalf("want exactly one 7001 for NaN-freedom: %+v", *fixture.reported)
	}
	if !strings.Contains((*fixture.reported)[0].MessageText, "null") {
		t.Errorf("the message %q does not say what stringify does to NaN", (*fixture.reported)[0].MessageText)
	}
}

/* ── a module-level (and function-local) const array LITERAL payload ── */

// TestCheckOutboundLeg_AModuleLevelConstArrayLiteralPayloadPassesEveryPremise
// pins d-data-legs.ts's own jsonStdinCapturedStdoutRecognized shape: `const
// samples = [0.5, -0.3, 0.2];` at MODULE scope, read as the payload inside
// a function that never itself declares `samples` — the identifier
// resolves through UntrackedIdentifier's module-const follow to
// EvaluateArrayLiteral's flat KindValues{PrimitiveArray} reading, and
// checkSequenceCrossing's own sequenceCrossingOfExactTuple converts that
// tuple to the same Repetition-shaped window a declared `number[]`
// parameter wears. Every element (0.5, −0.3, 0.2) sits inside audio_level's
// stated −2 … 2, and the length (3) clears its lengthAtLeast (1): the
// crossing passes with no diagnostic.
func TestCheckOutboundLeg_AModuleLevelConstArrayLiteralPayloadPassesEveryPremise(t *testing.T) {
	fixture := foreignLiteralArrayOutboundFixture(t, "[0.5, -0.3, 0.2]", true)
	if outcome := checkOutboundLeg(fixture.ctx, fixture.env, fixture.edge, fixture.artifact); outcome != nil {
		t.Fatalf("a module-level const array literal inside the stated entry did not pass: %+v", outcome)
	}
	if len(*fixture.reported) != 0 {
		t.Errorf("a fitting module-level literal crossing reported %d diagnostics: %+v", len(*fixture.reported), *fixture.reported)
	}
}

// TestCheckOutboundLeg_AModuleConstArrayMutatedByPushFiresNaNFreedomLikeLet
// mirrors the pass test above with ONE addition: a `samples.push(...)`
// statement anywhere in the module (here, inside a second, unrelated
// function — the brief's "top level or inside another function" case).
// RULING (JT 2026-08-21): a mutation site anywhere in the module blocks
// the const-follow's array serve — UntrackedIdentifier falls to its
// last-reader path instead of handing out the stale literal, exactly the
// path TestCheckOutboundLeg_ALetBoundModuleArrayReadsByTypeAndFiresNaNFreedom
// already pins for a `let`-bound array: the identifier reads through its
// DECLARED type (a sequence of unbounded numbers, which admit NaN), and
// the NaN-freedom premise fires rather than the crossing converting the
// stale tuple.
func TestCheckOutboundLeg_AModuleConstArrayMutatedByPushFiresNaNFreedomLikeLet(t *testing.T) {
	targetPath, contentHash := writeForeignTarget(t)
	writeForeignArtifact(t, targetPath, foreignArtifactJSON(contentHash, targetPath, true))
	artifact, sentence := ReadForeignArtifact(targetPath)
	if sentence != "" {
		t.Fatalf("the fixture artifact declined: %s", sentence)
	}
	p := entryEnvTestProgram(t, "const samples = [0.5, -0.3, 0.2];\n"+
		"function f() {\n"+
		"	samples;\n"+
		"}\n"+
		"function mutateElsewhere() {\n"+
		"	samples.push(0.1);\n"+
		"}\n")
	statements := relationalAccumulationBodyOf(t, p, "f")
	payload := statements[0].AsExpressionStatement().Expression
	var reported []assignability.RefinementDiagnostic
	ctx := &FlowContext{
		P:         p,
		Kernel:    nanWrapperLoadKernel(t),
		Contracts: map[*ast.Symbol]*FunctionContract{},
		Aliases:   dataflowfacts.NewAliasClasses(),
		Declared:  map[string]*annotations.DeclaredRefinement{},
		Report:    func(d assignability.RefinementDiagnostic) { reported = append(reported, d) },
	}
	edge := &ForeignEdge{Call: payload, TargetPath: targetPath, Payload: payload, StdoutName: "stdout"}
	outcome := checkOutboundLeg(ctx, NewEnv(), edge, artifact)
	if outcome == nil {
		t.Fatalf("a mutated module const array passed with no outcome at all — the literal was served despite the push")
	}
	if outcome.Decline != "" || outcome.Override != nil {
		t.Fatalf("expected the NaN-freedom fire's empty outcome, got %+v", outcome)
	}
	if len(reported) != 1 {
		t.Fatalf("expected exactly one NaN-freedom refutation (the literal must not have been served), got %d: %+v", len(reported), reported)
	}
	if !strings.Contains(reported[0].MessageText, "JSON.stringify writes NaN as null") {
		t.Errorf("the refutation does not name the stringify behaviour: %q", reported[0].MessageText)
	}
}

// TestCheckOutboundLeg_AFunctionLocalConstArrayLiteralPayloadPassesEveryPremise
// is the function-local mirror: the SAME literal, one statement above the
// call inside the function body rather than at module scope. The payload
// identifier now resolves through env.Get directly (the ordinary tracked
// local-binding path, never UntrackedIdentifier's module-const follow) —
// evaluateExpression still reads it through EvaluateArrayLiteral's flat
// reading either way, so both routes converge on the same
// KindValues{PrimitiveArray} tuple and the same converted crossing.
func TestCheckOutboundLeg_AFunctionLocalConstArrayLiteralPayloadPassesEveryPremise(t *testing.T) {
	fixture := foreignLiteralArrayOutboundFixture(t, "[0.5, -0.3, 0.2]", false)
	if outcome := checkOutboundLeg(fixture.ctx, fixture.env, fixture.edge, fixture.artifact); outcome != nil {
		t.Fatalf("a function-local const array literal inside the stated entry did not pass: %+v", outcome)
	}
	if len(*fixture.reported) != 0 {
		t.Errorf("a fitting function-local literal crossing reported %d diagnostics: %+v", len(*fixture.reported), *fixture.reported)
	}
}

// TestCheckOutboundLeg_AModuleLevelConstArrayLiteralOutsideTheEntryFires
// pins the fit-failure side of the same conversion: a module-level const
// array literal whose own elements sit outside audio_level's stated
// −2 … 2 still fires 7001 at the call — the conversion feeds the same
// element-fit and length-floor premises checkSequenceCrossing always
// asked, it does not skip them.
func TestCheckOutboundLeg_AModuleLevelConstArrayLiteralOutsideTheEntryFires(t *testing.T) {
	fixture := foreignLiteralArrayOutboundFixture(t, "[4, -0.3, 0.2]", true)
	outcome := checkOutboundLeg(fixture.ctx, fixture.env, fixture.edge, fixture.artifact)
	if outcome == nil {
		t.Fatalf("a module-level literal crossing outside the stated entry passed")
	}
	if outcome.Decline != "" {
		t.Fatalf("a crossing outside the stated entry DECLINED (%q); a fit failure is a refutation", outcome.Decline)
	}
	if len(*fixture.reported) != 1 || (*fixture.reported)[0].Code != 7001 {
		t.Fatalf("want exactly one 7001 for the element fit: %+v", *fixture.reported)
	}
}

// TestCheckOutboundLeg_ALetBoundModuleArrayLiteralPayloadStaysUndetermined
// pins the const-only gate UntrackedIdentifier's own module-const follow
// already carries (untracked_identifier.go: `(d.Parent.Flags&ast.NodeFlagsConst)
// != 0`): a MODULE-LEVEL `let samples = [...]` the rest of the module could
// rewrite never reaches the follow, so the identifier reads through its
// DECLARED type instead — a sequence whose elements are unbounded numbers,
// which admit NaN. That reading derives a real stringify hazard, so the
// NaN-freedom premise fires the refutation and the outcome carries no
// override and no decline sentence of its own.
func TestCheckOutboundLeg_ALetBoundModuleArrayReadsByTypeAndFiresNaNFreedom(t *testing.T) {
	targetPath, contentHash := writeForeignTarget(t)
	writeForeignArtifact(t, targetPath, foreignArtifactJSON(contentHash, targetPath, true))
	artifact, sentence := ReadForeignArtifact(targetPath)
	if sentence != "" {
		t.Fatalf("the fixture artifact declined: %s", sentence)
	}
	p := entryEnvTestProgram(t, "let samples = [0.5, -0.3, 0.2];\n"+
		"function f() {\n"+
		"	samples;\n"+
		"}\n")
	statements := relationalAccumulationBodyOf(t, p, "f")
	payload := statements[0].AsExpressionStatement().Expression
	var reported []assignability.RefinementDiagnostic
	ctx := &FlowContext{
		P:         p,
		Kernel:    nanWrapperLoadKernel(t),
		Contracts: map[*ast.Symbol]*FunctionContract{},
		Aliases:   dataflowfacts.NewAliasClasses(),
		Declared:  map[string]*annotations.DeclaredRefinement{},
		Report: func(d assignability.RefinementDiagnostic) {
			reported = append(reported, d)
		},
	}
	edge := &ForeignEdge{Call: payload, TargetPath: targetPath, Payload: payload, StdoutName: "stdout"}
	outcome := checkOutboundLeg(ctx, NewEnv(), edge, artifact)
	if outcome == nil {
		t.Fatalf("a let-bound module array literal passed with no outcome at all")
	}
	if outcome.Decline != "" || outcome.Override != nil {
		t.Fatalf("expected the NaN-freedom fire's empty outcome, got %+v", outcome)
	}
	if len(reported) != 1 {
		t.Fatalf("expected exactly one NaN-freedom refutation, got %d", len(reported))
	}
	if !strings.Contains(reported[0].MessageText, "JSON.stringify writes NaN as null") {
		t.Errorf("the refutation does not name the stringify behaviour: %q", reported[0].MessageText)
	}
}

// TestCheckOutboundLeg_AModuleConstArrayWithANonLiteralElementFiresNaNFreedom
// pins the other edge of the exact shape: a module-level const array
// literal with ONE non-literal element (`gain`, a declared number) never
// reaches EvaluateArrayLiteral's flat path — flat requires every item to
// be a single-number KindValues (array_literal.go) — so
// sequenceCrossingOfExactTuple's gate never fires and the reading falls
// to a sequence whose elements admit NaN (the declared `number` carries
// the possibly-NaN wrapper). That derives the same stringify hazard, so
// the NaN-freedom premise fires rather than serving or declining.
func TestCheckOutboundLeg_AModuleConstArrayWithANonLiteralElementFiresNaNFreedom(t *testing.T) {
	targetPath, contentHash := writeForeignTarget(t)
	writeForeignArtifact(t, targetPath, foreignArtifactJSON(contentHash, targetPath, true))
	artifact, sentence := ReadForeignArtifact(targetPath)
	if sentence != "" {
		t.Fatalf("the fixture artifact declined: %s", sentence)
	}
	p := entryEnvTestProgram(t, "declare const gain: number;\n"+
		"const samples = [gain, -0.3, 0.2];\n"+
		"function f() {\n"+
		"	samples;\n"+
		"}\n")
	statements := relationalAccumulationBodyOf(t, p, "f")
	payload := statements[0].AsExpressionStatement().Expression
	var reported []assignability.RefinementDiagnostic
	ctx := &FlowContext{
		P:         p,
		Kernel:    nanWrapperLoadKernel(t),
		Contracts: map[*ast.Symbol]*FunctionContract{},
		Aliases:   dataflowfacts.NewAliasClasses(),
		Declared:  map[string]*annotations.DeclaredRefinement{},
		Report: func(d assignability.RefinementDiagnostic) {
			reported = append(reported, d)
		},
	}
	edge := &ForeignEdge{Call: payload, TargetPath: targetPath, Payload: payload, StdoutName: "stdout"}
	outcome := checkOutboundLeg(ctx, NewEnv(), edge, artifact)
	if outcome == nil {
		t.Fatalf("a module const array with a non-literal element passed with no outcome at all")
	}
	if outcome.Decline != "" || outcome.Override != nil {
		t.Fatalf("expected the NaN-freedom fire's empty outcome, got %+v", outcome)
	}
	if len(reported) != 1 {
		t.Fatalf("expected exactly one NaN-freedom refutation, got %d", len(reported))
	}
	if !strings.Contains(reported[0].MessageText, "JSON.stringify writes NaN as null") {
		t.Errorf("the refutation does not name the stringify behaviour: %q", reported[0].MessageText)
	}
}

/* ── the argv-value leg, against a real kernel ───────────────────── */

// foreignArgvOutboundFixture is the argv-crossing counterpart of
// foreignOutboundFixture: an argv-scalar artifact read off disk, a
// context carrying a real kernel and a collecting sink, and an edge
// whose ArgvValue is the parsed source's own `const gain = <literalText>;`
// initializer node — the same shape d-data-legs.ts's row spells.
func foreignArgvOutboundFixture(t *testing.T, literalText string) foreignEdgeFixture {
	t.Helper()
	targetPath, contentHash := writeForeignScalarArgvTarget(t)
	writeForeignArtifact(t, targetPath, foreignArgvScalarArtifactJSON(contentHash, targetPath))
	artifact, sentence := ReadForeignArtifact(targetPath)
	if sentence != "" {
		t.Fatalf("the fixture artifact declined: %s", sentence)
	}
	p := entryEnvTestProgram(t, `function f() { const gain = `+strconv.Quote(literalText)+`; gain; }`+"\n")
	statements := relationalAccumulationBodyOf(t, p, "f")
	gainDeclaration := statements[0].AsVariableStatement().DeclarationList.
		AsVariableDeclarationList().Declarations.Nodes[0].AsVariableDeclaration()
	argvValue := gainDeclaration.Initializer
	var reported []assignability.RefinementDiagnostic
	ctx := &FlowContext{
		P:         p,
		Kernel:    nanWrapperLoadKernel(t),
		Contracts: map[*ast.Symbol]*FunctionContract{},
		Aliases:   dataflowfacts.NewAliasClasses(),
		Declared:  map[string]*annotations.DeclaredRefinement{},
		Report:    func(d assignability.RefinementDiagnostic) { reported = append(reported, d) },
	}
	return foreignEdgeFixture{
		artifact: artifact,
		ctx:      ctx,
		env:      NewEnv(),
		edge:     &ForeignEdge{Call: argvValue, TargetPath: targetPath, ArgvValue: argvValue, StdoutName: "stdout"},
		reported: &reported,
	}
}

func TestCheckArgvCrossing_ALiteralInsideTheStatedEntryIsSilent(t *testing.T) {
	// 0.5 is inside the target's stated 0 … 4 gain window
	fixture := foreignArgvOutboundFixture(t, "0.5")
	if outcome := checkOutboundLeg(fixture.ctx, fixture.env, fixture.edge, fixture.artifact); outcome != nil {
		t.Fatalf("a fitting argv crossing did not pass: %+v", outcome)
	}
	if len(*fixture.reported) != 0 {
		t.Errorf("a fitting argv crossing reported %d diagnostics: %+v", len(*fixture.reported), *fixture.reported)
	}
}

func TestCheckArgvCrossing_ALiteralOutsideTheStatedEntryFiresWithThePinnedSentence(t *testing.T) {
	// 9 is outside the target's stated 0 … 4 gain window
	fixture := foreignArgvOutboundFixture(t, "9")
	outcome := checkOutboundLeg(fixture.ctx, fixture.env, fixture.edge, fixture.artifact)
	if outcome == nil {
		t.Fatalf("an out-of-set argv literal passed")
	}
	if outcome.Decline != "" {
		t.Fatalf("an out-of-set argv literal DECLINED (%q); a fit failure is a refutation", outcome.Decline)
	}
	if len(*fixture.reported) != 1 {
		t.Fatalf("reported %d diagnostics, want exactly 1: %+v", len(*fixture.reported), *fixture.reported)
	}
	fired := (*fixture.reported)[0]
	if fired.Code != 7001 {
		t.Errorf("Code = %d, want 7001", fired.Code)
	}
	// old wording (pre house-fire reword): "the argv value crossing to
	// level_from_gain is 9, and the target admits 0 … 4 — the value can
	// escape what the target states it accepts"
	wantSentence := "the argv value sent to level_from_gain is of type '9', which is not " +
		"assignable to the target's stated entry '0 … 4'"
	if !strings.Contains(fired.MessageText, "which is not assignable to the target's stated entry") {
		t.Errorf("the message %q does not carry the fit-failure sentence, want it to contain %q", fired.MessageText, wantSentence)
	}
	if !strings.Contains(fired.MessageText, "level_from_gain") {
		t.Errorf("the message %q does not name the target function", fired.MessageText)
	}
}

func TestCheckArgvCrossing_ANonLiteralArgvValueIsRecognizedUndetermined(t *testing.T) {
	targetPath, contentHash := writeForeignScalarArgvTarget(t)
	writeForeignArtifact(t, targetPath, foreignArgvScalarArtifactJSON(contentHash, targetPath))
	artifact, sentence := ReadForeignArtifact(targetPath)
	if sentence != "" {
		t.Fatalf("the fixture artifact declined: %s", sentence)
	}
	p := entryEnvTestProgram(t, "function f(gain: string) { gain; }\n")
	statements := relationalAccumulationBodyOf(t, p, "f")
	argvValue := statements[0].AsExpressionStatement().Expression
	ctx := &FlowContext{
		P:         p,
		Kernel:    nanWrapperLoadKernel(t),
		Contracts: map[*ast.Symbol]*FunctionContract{},
		Aliases:   dataflowfacts.NewAliasClasses(),
		Declared:  map[string]*annotations.DeclaredRefinement{},
		Report:    func(assignability.RefinementDiagnostic) {},
	}
	edge := &ForeignEdge{Call: argvValue, TargetPath: targetPath, ArgvValue: argvValue, StdoutName: "stdout"}
	outcome := checkOutboundLeg(ctx, NewEnv(), edge, artifact)
	if outcome == nil {
		t.Fatalf("a non-literal argv value passed with no outcome at all")
	}
	if outcome.Decline != "the argv value is not a written string literal; its parsed value cannot be pinned" {
		t.Errorf("Decline = %q, want the recognized-undetermined sentence naming the construct", outcome.Decline)
	}
}

func TestCheckArgvCrossing_ANaNLiteralIsRefused(t *testing.T) {
	fixture := foreignArgvOutboundFixture(t, "nan")
	outcome := checkOutboundLeg(fixture.ctx, fixture.env, fixture.edge, fixture.artifact)
	if outcome == nil || outcome.Decline != "" {
		t.Fatalf("a NaN argv literal did not fire: %+v", outcome)
	}
	if len(*fixture.reported) != 1 || (*fixture.reported)[0].Code != 7001 {
		t.Fatalf("want exactly one 7001 for NaN-freedom: %+v", *fixture.reported)
	}
	if !strings.Contains((*fixture.reported)[0].MessageText, "NaN") {
		t.Errorf("the message %q does not name NaN", (*fixture.reported)[0].MessageText)
	}
}

func TestCheckArgvCrossing_AParseFailureFiresNamingTheParseFailure(t *testing.T) {
	fixture := foreignArgvOutboundFixture(t, "--fast")
	outcome := checkOutboundLeg(fixture.ctx, fixture.env, fixture.edge, fixture.artifact)
	if outcome == nil || outcome.Decline != "" {
		t.Fatalf("an unparseable argv literal did not fire: %+v", outcome)
	}
	if len(*fixture.reported) != 1 || (*fixture.reported)[0].Code != 7001 {
		t.Fatalf("want exactly one 7001 for the parse failure: %+v", *fixture.reported)
	}
	if !strings.Contains((*fixture.reported)[0].MessageText, "does not parse as a Python float()") {
		t.Errorf("the message %q does not name the parse failure", (*fixture.reported)[0].MessageText)
	}
}

func TestCheckArgvCrossing_AnArgvCallAtAStdinJsonTargetDeclinesWithTheChannelMismatchSentence(t *testing.T) {
	targetPath, contentHash := writeForeignTarget(t)
	writeForeignArtifact(t, targetPath, foreignArtifactJSON(contentHash, targetPath, true))
	artifact, sentence := ReadForeignArtifact(targetPath)
	if sentence != "" {
		t.Fatalf("the fixture artifact declined: %s", sentence)
	}
	p := entryEnvTestProgram(t, `function f() { const gain = "0.5"; gain; }`+"\n")
	statements := relationalAccumulationBodyOf(t, p, "f")
	gainDeclaration := statements[0].AsVariableStatement().DeclarationList.
		AsVariableDeclarationList().Declarations.Nodes[0].AsVariableDeclaration()
	argvValue := gainDeclaration.Initializer
	ctx := &FlowContext{
		P:         p,
		Kernel:    nanWrapperLoadKernel(t),
		Contracts: map[*ast.Symbol]*FunctionContract{},
		Aliases:   dataflowfacts.NewAliasClasses(),
		Declared:  map[string]*annotations.DeclaredRefinement{},
		Report:    func(assignability.RefinementDiagnostic) {},
	}
	edge := &ForeignEdge{Call: argvValue, TargetPath: targetPath, ArgvValue: argvValue, StdoutName: "stdout"}
	outcome := checkOutboundLeg(ctx, NewEnv(), edge, artifact)
	if outcome == nil {
		t.Fatalf("an argv call at a stdin-json target passed with no outcome at all")
	}
	want := "the call passes the value as argv[1], but the target's fact serves JSON on stdin — " +
		"the channels do not meet"
	if outcome.Decline != want {
		t.Errorf("Decline = %q, want %q", outcome.Decline, want)
	}
}

func TestCheckOutboundLeg_AStdinCallAtAnArgvScalarTargetDeclinesWithTheMirroredChannelMismatchSentence(t *testing.T) {
	targetPath, contentHash := writeForeignScalarArgvTarget(t)
	writeForeignArtifact(t, targetPath, foreignArgvScalarArtifactJSON(contentHash, targetPath))
	artifact, sentence := ReadForeignArtifact(targetPath)
	if sentence != "" {
		t.Fatalf("the fixture artifact declined: %s", sentence)
	}
	p := entryEnvTestProgram(t, "function f(gain: number) { gain; }\n")
	fn := entryEnvFunctionNamed(t, p, "f")
	payload := fn.AsFunctionDeclaration().Body.AsBlock().Statements.Nodes[0].
		AsExpressionStatement().Expression
	ctx := &FlowContext{
		P:         p,
		Kernel:    nanWrapperLoadKernel(t),
		Contracts: map[*ast.Symbol]*FunctionContract{},
		Aliases:   dataflowfacts.NewAliasClasses(),
		Declared:  map[string]*annotations.DeclaredRefinement{},
		Report:    func(assignability.RefinementDiagnostic) {},
	}
	env := NewEnv()
	env.Set("gain", abstractdomain.KnownValues([]float64{1}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved))
	// Payload set, ArgvValue nil: the ordinary stdin shape, at a target
	// whose surface is argv-scalar
	edge := &ForeignEdge{Call: payload, TargetPath: targetPath, Payload: payload, StdoutName: "stdout"}
	outcome := checkOutboundLeg(ctx, env, edge, artifact)
	if outcome == nil {
		t.Fatalf("a stdin call at an argv-scalar target passed with no outcome at all")
	}
	want := "the call passes the value as JSON on stdin, but the target's fact serves an argv[1] " +
		"scalar — the channels do not meet"
	if outcome.Decline != want {
		t.Errorf("Decline = %q, want %q", outcome.Decline, want)
	}
}

/* ── the mixed shape (stdin + argv, both legs), against a real kernel ── */

// foreignMixedFixture builds a mixed-surface artifact (level_gain_argv.py's
// own real anatomy: samples on stdin, gain from argv[1]) and an edge whose
// Payload is a declared `samples: number[]` parameter (the stdin leg) and
// whose ArgvValue is a `const gain = <gainLiteral>;` initializer node (the
// argv leg) in the SAME function — the exact dual-channel shape the brief
// names: both an options-object `input: JSON.stringify(...)` and a
// two-element argv, recognized together rather than declined as a mix.
func foreignMixedFixture(t *testing.T, gainLiteral string) foreignEdgeFixture {
	t.Helper()
	targetPath, contentHash := writeForeignMixedTarget(t)
	writeForeignArtifact(t, targetPath, foreignMixedArtifactJSON(contentHash, targetPath))
	artifact, sentence := ReadForeignArtifact(targetPath)
	if sentence != "" {
		t.Fatalf("the fixture artifact declined: %s", sentence)
	}
	p := entryEnvTestProgram(t, "function f(samples: number[]) { samples; const gain = "+
		strconv.Quote(gainLiteral)+"; gain; }\n")
	statements := relationalAccumulationBodyOf(t, p, "f")
	payload := statements[0].AsExpressionStatement().Expression
	gainDeclaration := statements[1].AsVariableStatement().DeclarationList.
		AsVariableDeclarationList().Declarations.Nodes[0].AsVariableDeclaration()
	argvValue := gainDeclaration.Initializer
	var reported []assignability.RefinementDiagnostic
	ctx := &FlowContext{
		P:         p,
		Kernel:    nanWrapperLoadKernel(t),
		Contracts: map[*ast.Symbol]*FunctionContract{},
		Aliases:   dataflowfacts.NewAliasClasses(),
		Declared:  map[string]*annotations.DeclaredRefinement{},
		Report:    func(d assignability.RefinementDiagnostic) { reported = append(reported, d) },
	}
	env := NewEnv()
	env.Set("samples", abstractdomain.KnownSet(
		refinementsets.Repetition(
			refinementsets.MakeRefinedSet(refinementsets.AtLeast(-2), refinementsets.AtMost(2)), 1, nil),
		nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone))
	return foreignEdgeFixture{
		artifact: artifact,
		ctx:      ctx,
		env:      env,
		edge:     &ForeignEdge{Call: payload, TargetPath: targetPath, Payload: payload, ArgvValue: argvValue, StdoutName: "stdout"},
		reported: &reported,
	}
}

// TestCheckMixedCrossing_BothLegsInsideTheStatedEntryAreSilent pins the
// green path: samples (-2 … 2, at least 1 element) fits entry[0], and
// gain 0.5 fits entry[1]'s 0 … 4 — both legs fit, so checkOutboundLeg
// answers clean with nothing reported.
func TestCheckMixedCrossing_BothLegsInsideTheStatedEntryAreSilent(t *testing.T) {
	fixture := foreignMixedFixture(t, "0.5")
	if outcome := checkOutboundLeg(fixture.ctx, fixture.env, fixture.edge, fixture.artifact); outcome != nil {
		t.Fatalf("a fitting mixed crossing did not pass: %+v", outcome)
	}
	if len(*fixture.reported) != 0 {
		t.Errorf("a fitting mixed crossing reported %d diagnostics: %+v", len(*fixture.reported), *fixture.reported)
	}
}

// TestCheckMixedCrossing_AnOutOfSetArgvLiteralFiresOnThatLegOnly pins that
// each leg's refutation is independent: gain "9" sits outside the
// target's 0 … 4 window, and fires 7001 on the ARGV leg specifically,
// while the stdin leg (samples, in-range) is judged and passes cleanly —
// exactly one diagnostic, naming the argv leg's own values.
func TestCheckMixedCrossing_AnOutOfSetArgvLiteralFiresOnThatLegOnly(t *testing.T) {
	fixture := foreignMixedFixture(t, "9")
	outcome := checkOutboundLeg(fixture.ctx, fixture.env, fixture.edge, fixture.artifact)
	if outcome == nil {
		t.Fatalf("an out-of-set argv leg passed")
	}
	if outcome.Decline != "" {
		t.Fatalf("an out-of-set argv leg DECLINED (%q); a fit failure is a refutation", outcome.Decline)
	}
	if len(*fixture.reported) != 1 {
		t.Fatalf("reported %d diagnostics, want exactly 1 (the argv leg's own): %+v", len(*fixture.reported), *fixture.reported)
	}
	fired := (*fixture.reported)[0]
	if fired.Code != 7001 {
		t.Errorf("Code = %d, want 7001", fired.Code)
	}
	// old wording (pre house-fire reword): "the argv value crossing to
	// level_gain_argv is 9"
	if !strings.Contains(fired.MessageText, "the argv value sent to level_gain_argv is of type '9'") {
		t.Errorf("the message %q does not name the argv leg's own out-of-set value", fired.MessageText)
	}
}

// TestCheckMixedCrossing_AMixedCallAtAStdinJsonTargetDeclines pins the
// channel-match premise: a mixed call (both legs recognized) at a target
// whose fact serves plain stdin-json (never both channels) declines
// naming the surface it actually found, never silently picking one leg.
func TestCheckMixedCrossing_AMixedCallAtAStdinJsonTargetDeclines(t *testing.T) {
	targetPath, contentHash := writeForeignTarget(t)
	writeForeignArtifact(t, targetPath, foreignArtifactJSON(contentHash, targetPath, true))
	artifact, sentence := ReadForeignArtifact(targetPath)
	if sentence != "" {
		t.Fatalf("the fixture artifact declined: %s", sentence)
	}
	p := entryEnvTestProgram(t, `function f(samples: number[]) { samples; const gain = "0.5"; gain; }`+"\n")
	statements := relationalAccumulationBodyOf(t, p, "f")
	payload := statements[0].AsExpressionStatement().Expression
	gainDeclaration := statements[1].AsVariableStatement().DeclarationList.
		AsVariableDeclarationList().Declarations.Nodes[0].AsVariableDeclaration()
	argvValue := gainDeclaration.Initializer
	ctx := &FlowContext{
		P:         p,
		Kernel:    nanWrapperLoadKernel(t),
		Contracts: map[*ast.Symbol]*FunctionContract{},
		Aliases:   dataflowfacts.NewAliasClasses(),
		Declared:  map[string]*annotations.DeclaredRefinement{},
		Report:    func(assignability.RefinementDiagnostic) {},
	}
	edge := &ForeignEdge{Call: payload, TargetPath: targetPath, Payload: payload, ArgvValue: argvValue, StdoutName: "stdout"}
	outcome := checkOutboundLeg(ctx, NewEnv(), edge, artifact)
	if outcome == nil {
		t.Fatalf("a mixed call at a stdin-json target passed with no outcome at all")
	}
	if !strings.Contains(outcome.Decline, "the channels do not meet") {
		t.Errorf("Decline = %q, want the channel-mismatch sentence", outcome.Decline)
	}
	if !strings.Contains(outcome.Decline, "stdin-json") {
		t.Errorf("Decline = %q, want it to name the surface actually found (stdin-json)", outcome.Decline)
	}
}

// TestCheckOutboundLeg_AStdinOnlyCallAtAMixedSurfaceTargetDeclinesNamingTheAbsentArgvLeg
// pins the single-channel-at-mixed-surface premise: a call that sends only
// the stdin leg (no ArgvValue at all) at a target whose fact serves the
// mixed surface declines naming the ABSENT leg, never silently judging
// the one leg it has against the mixed surface's own entry[0].
func TestCheckOutboundLeg_AStdinOnlyCallAtAMixedSurfaceTargetDeclinesNamingTheAbsentArgvLeg(t *testing.T) {
	targetPath, contentHash := writeForeignMixedTarget(t)
	writeForeignArtifact(t, targetPath, foreignMixedArtifactJSON(contentHash, targetPath))
	artifact, sentence := ReadForeignArtifact(targetPath)
	if sentence != "" {
		t.Fatalf("the fixture artifact declined: %s", sentence)
	}
	p := entryEnvTestProgram(t, "function f(samples: number[]) { samples; }\n")
	fn := entryEnvFunctionNamed(t, p, "f")
	payload := fn.AsFunctionDeclaration().Body.AsBlock().Statements.Nodes[0].
		AsExpressionStatement().Expression
	ctx := &FlowContext{
		P:         p,
		Kernel:    nanWrapperLoadKernel(t),
		Contracts: map[*ast.Symbol]*FunctionContract{},
		Aliases:   dataflowfacts.NewAliasClasses(),
		Declared:  map[string]*annotations.DeclaredRefinement{},
		Report:    func(assignability.RefinementDiagnostic) {},
	}
	env := NewEnv()
	env.Set("samples", abstractdomain.KnownSet(
		refinementsets.Repetition(
			refinementsets.MakeRefinedSet(refinementsets.AtLeast(-2), refinementsets.AtMost(2)), 1, nil),
		nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone))
	// Payload set, ArgvValue nil: only the stdin leg, at a mixed-surface target
	edge := &ForeignEdge{Call: payload, TargetPath: targetPath, Payload: payload, StdoutName: "stdout"}
	outcome := checkOutboundLeg(ctx, env, edge, artifact)
	if outcome == nil {
		t.Fatalf("a stdin-only call at a mixed-surface target passed with no outcome at all")
	}
	if !strings.Contains(outcome.Decline, "the channels do not meet") {
		t.Errorf("Decline = %q, want the channel-mismatch sentence", outcome.Decline)
	}
	if !strings.Contains(outcome.Decline, "argv leg is absent") {
		t.Errorf("Decline = %q, want it to name the absent argv leg specifically", outcome.Decline)
	}
}

// TestCheckArgvCrossing_AnArgvOnlyCallAtAMixedSurfaceTargetDeterminesRatherThanDeclines
// pins d-data-legs.ts's mixedStdinAndArgvChannelUndetermined row's own
// construct: a call sending ONLY argv[1] (no stdin `input` at all) at a
// target whose fact serves the mixed stdin-json-argv-scalar surface no
// longer declines with "the channels do not meet" — the target's own
// stdin read (json.load on an EOF-empty stream, since this call writes
// no stdin bytes) throws before the argv leg is ever consulted, so
// every concrete run already fails before reaching a state this fact
// could contradict. checkArgvCrossing now judges the argv leg's OWN fit
// against entry[1] instead (argvScalarFitAgainst, the same function the
// pure argv-scalar surface and the true-mixed call both route through):
// a fitting literal answers a clean outcome (nil), exactly as if the
// channel mismatch had never been a decline at all.
func TestCheckArgvCrossing_AnArgvOnlyCallAtAMixedSurfaceTargetDeterminesRatherThanDeclines(t *testing.T) {
	targetPath, contentHash := writeForeignMixedTarget(t)
	writeForeignArtifact(t, targetPath, foreignMixedArtifactJSON(contentHash, targetPath))
	artifact, sentence := ReadForeignArtifact(targetPath)
	if sentence != "" {
		t.Fatalf("the fixture artifact declined: %s", sentence)
	}
	p := entryEnvTestProgram(t, `function f() { const gain = "0.5"; gain; }` + "\n")
	statements := relationalAccumulationBodyOf(t, p, "f")
	gainDeclaration := statements[0].AsVariableStatement().DeclarationList.
		AsVariableDeclarationList().Declarations.Nodes[0].AsVariableDeclaration()
	argvValue := gainDeclaration.Initializer
	ctx := &FlowContext{
		P:         p,
		Kernel:    nanWrapperLoadKernel(t),
		Contracts: map[*ast.Symbol]*FunctionContract{},
		Aliases:   dataflowfacts.NewAliasClasses(),
		Declared:  map[string]*annotations.DeclaredRefinement{},
		Report:    func(assignability.RefinementDiagnostic) {},
	}
	// ArgvValue set, Payload nil: only the argv leg, at a mixed-surface
	// target whose stdin the call never writes — d:115's own shape.
	edge := &ForeignEdge{Call: argvValue, TargetPath: targetPath, ArgvValue: argvValue, StdoutName: "stdout"}
	outcome := checkOutboundLeg(ctx, NewEnv(), edge, artifact)
	if outcome != nil {
		t.Fatalf("an argv-only call at a mixed-surface target with a fitting argv literal reported an outcome, want none (a clean determination): %+v", outcome)
	}
}

// TestCheckArgvCrossing_AnArgvOnlyCallAtAMixedSurfaceTargetStillFiresOnAnUnfittingLiteral
// pins the "judge the rest normally" half: even though the call always
// throws at the target's own stdin read, the argv leg's own fit is
// still a real premise — an argv literal OUTSIDE the target's stated
// entry[1] still fires 7001, exactly as it would at a pure argv-scalar
// surface.
func TestCheckArgvCrossing_AnArgvOnlyCallAtAMixedSurfaceTargetStillFiresOnAnUnfittingLiteral(t *testing.T) {
	targetPath, contentHash := writeForeignMixedTarget(t)
	writeForeignArtifact(t, targetPath, foreignMixedArtifactJSON(contentHash, targetPath))
	artifact, sentence := ReadForeignArtifact(targetPath)
	if sentence != "" {
		t.Fatalf("the fixture artifact declined: %s", sentence)
	}
	// 9 is outside the target's stated gain window (0 … 4)
	p := entryEnvTestProgram(t, `function f() { const gain = "9"; gain; }` + "\n")
	statements := relationalAccumulationBodyOf(t, p, "f")
	gainDeclaration := statements[0].AsVariableStatement().DeclarationList.
		AsVariableDeclarationList().Declarations.Nodes[0].AsVariableDeclaration()
	argvValue := gainDeclaration.Initializer
	var reported []assignability.RefinementDiagnostic
	ctx := &FlowContext{
		P:         p,
		Kernel:    nanWrapperLoadKernel(t),
		Contracts: map[*ast.Symbol]*FunctionContract{},
		Aliases:   dataflowfacts.NewAliasClasses(),
		Declared:  map[string]*annotations.DeclaredRefinement{},
		Report:    func(d assignability.RefinementDiagnostic) { reported = append(reported, d) },
	}
	edge := &ForeignEdge{Call: argvValue, TargetPath: targetPath, ArgvValue: argvValue, StdoutName: "stdout"}
	outcome := checkOutboundLeg(ctx, NewEnv(), edge, artifact)
	if outcome == nil {
		t.Fatalf("an unfitting argv literal at a mixed-surface target passed with no outcome at all")
	}
	if outcome.Decline != "" {
		t.Errorf("Decline = %q, want the fit refutation to fire rather than decline", outcome.Decline)
	}
	if len(reported) != 1 {
		t.Fatalf("expected exactly one fit refutation, got %d", len(reported))
	}
	if !strings.Contains(reported[0].MessageText, "not assignable") {
		t.Errorf("the refutation does not name the fit failure: %q", reported[0].MessageText)
	}
}

/* ── the file-carried shape, against a real kernel and real syntax ──── */

// foreignFileFixture builds a file-json artifact (level_from_file.py's own
// real anatomy) and an edge whose Payload/FilePath are set directly — the
// grain checkOutboundLeg itself judges at, mirroring foreignOutboundFixture's
// own direct-edge-construction style rather than round-tripping through
// fileCrossingOf's own syntax recognition (pinned separately, below).
func foreignFileFixture(t *testing.T, samplesLiteral string) foreignEdgeFixture {
	t.Helper()
	targetPath, contentHash := writeForeignFileTarget(t)
	writeForeignArtifact(t, targetPath, foreignFileJSONArtifactJSON(contentHash, targetPath))
	artifact, sentence := ReadForeignArtifact(targetPath)
	if sentence != "" {
		t.Fatalf("the fixture artifact declined: %s", sentence)
	}
	p := entryEnvTestProgram(t, "function f() {\n"+
		"	const samples = "+samplesLiteral+";\n"+
		"	samples;\n"+
		"}\n")
	statements := relationalAccumulationBodyOf(t, p, "f")
	payload := statements[1].AsExpressionStatement().Expression
	var reported []assignability.RefinementDiagnostic
	ctx := &FlowContext{
		P:         p,
		Kernel:    nanWrapperLoadKernel(t),
		Contracts: map[*ast.Symbol]*FunctionContract{},
		Aliases:   dataflowfacts.NewAliasClasses(),
		Declared:  map[string]*annotations.DeclaredRefinement{},
		Report:    func(d assignability.RefinementDiagnostic) { reported = append(reported, d) },
	}
	env := NewEnv()
	AnalyzeVariableStatement(ctx, env, statements[0])
	// FilePath only needs to be non-nil here to route checkOutboundLeg's
	// dispatch to checkFileCrossing — its own value never enters the fit
	// judged below (only a channel-mismatch DeclineNode, not exercised by
	// this fixture's green/fit-only cases). statements[1] (a real, distinct
	// node) stands in rather than aliasing Payload to two different roles.
	return foreignEdgeFixture{
		artifact: artifact,
		ctx:      ctx,
		env:      env,
		edge:     &ForeignEdge{Call: payload, TargetPath: targetPath, Payload: payload, FilePath: statements[1], StdoutName: "stdout"},
		reported: &reported,
	}
}

// TestCheckFileCrossing_AFittingPayloadIsSilent pins the green path: the
// SAME stdin fit chain the pure-stdin shape uses, applied to a file-json
// target — samples (0.5, -0.3, 0.2) fit -2 … 2 at length 3 >= 1, so the
// crossing passes with nothing reported.
func TestCheckFileCrossing_AFittingPayloadIsSilent(t *testing.T) {
	fixture := foreignFileFixture(t, "[0.5, -0.3, 0.2]")
	if outcome := checkOutboundLeg(fixture.ctx, fixture.env, fixture.edge, fixture.artifact); outcome != nil {
		t.Fatalf("a fitting file-carried crossing did not pass: %+v", outcome)
	}
	if len(*fixture.reported) != 0 {
		t.Errorf("a fitting file-carried crossing reported %d diagnostics: %+v", len(*fixture.reported), *fixture.reported)
	}
}

/* ── fileCrossingOf: the writeFileSync-then-execFileSync syntax reader ── */

// TestFileCrossingOf_AMatchingWriteImmediatelyBeforeTheCallIsRecognized
// pins d-data-legs.ts's tempFileNamedInArgvUndetermined shape: the
// statement immediately before the call writes the SAME path the call's
// own argv names, so fileCrossingOf answers the written payload and the
// argv element, with no sentence at all.
func TestFileCrossingOf_AMatchingWriteImmediatelyBeforeTheCallIsRecognized(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function execFileSync(file: string, args: string[], options: unknown): string;
declare function writeFileSync(path: string, data: string): void;
function f(samples: number[]) {
	writeFileSync("./targets/level_payload.json", JSON.stringify(samples));
	const stdout = execFileSync("python3", ["./targets/level_from_file.py", "./targets/level_payload.json"], { encoding: "utf8" });
	return stdout;
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	ctx := relationalAccumulationContext(p)
	_, call, _ := constBoundCallOf(statements[1])
	args, _ := callArguments(call)
	payload, filePathElement, sentence, ok := fileCrossingOf(ctx, statements, 1, args)
	if sentence != "" {
		t.Fatalf("a matching immediately-preceding write declined: %s", sentence)
	}
	if !ok {
		t.Fatalf("a matching immediately-preceding write was not recognized")
	}
	if payload == nil || !ast.IsIdentifier(payload) || payload.Text() != "samples" {
		t.Errorf("payload = %v, want the identifier samples (JSON.stringify's own argument)", payload)
	}
	if filePathElement == nil {
		t.Errorf("filePathElement is nil, want the argv element naming the file")
	}
}

// TestFileCrossingOf_AnInterveningStatementBetweenTheWriteAndTheCallDeclines
// pins the carrier premise's own boundary: the SAME write and the SAME
// matching argv path, but with one statement between them — the write is
// recognized (the path IS named by this call's argv), and the carrier
// premise (no statement between the write and the call) is what refuses
// it, named rather than silently falling through to an unrelated reading.
func TestFileCrossingOf_AnInterveningStatementBetweenTheWriteAndTheCallDeclines(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function execFileSync(file: string, args: string[], options: unknown): string;
declare function writeFileSync(path: string, data: string): void;
function f(samples: number[]) {
	writeFileSync("./targets/level_payload.json", JSON.stringify(samples));
	const unrelated = 1;
	const stdout = execFileSync("python3", ["./targets/level_from_file.py", "./targets/level_payload.json"], { encoding: "utf8" });
	return stdout + unrelated;
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	ctx := relationalAccumulationContext(p)
	_, call, _ := constBoundCallOf(statements[2])
	args, _ := callArguments(call)
	_, _, sentence, ok := fileCrossingOf(ctx, statements, 2, args)
	if ok {
		t.Fatalf("a write separated from the call by an intervening statement was recognized")
	}
	if sentence == "" {
		t.Fatalf("an intervening statement between the write and the call was silently not-this-shape")
	}
	if !strings.Contains(sentence, "intervening statement") {
		t.Errorf("sentence %q does not name the intervening statement", sentence)
	}
}

// TestFileCrossingOf_AMismatchedWritePathStaysUndeterminedNamingIt pins
// the path-mismatch case: the immediately preceding statement DOES write
// a file, but a DIFFERENT path than either argv element names — recognized
// (the write and the call both exist) and undetermined (the carrier
// premise does not hold), named rather than silently read as no write at
// all or as an ordinary argv-scalar call.
func TestFileCrossingOf_AMismatchedWritePathStaysUndeterminedNamingIt(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function execFileSync(file: string, args: string[], options: unknown): string;
declare function writeFileSync(path: string, data: string): void;
function f(samples: number[]) {
	writeFileSync("./targets/wrong_path.json", JSON.stringify(samples));
	const stdout = execFileSync("python3", ["./targets/level_from_file.py", "./targets/level_payload.json"], { encoding: "utf8" });
	return stdout;
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	ctx := relationalAccumulationContext(p)
	_, call, _ := constBoundCallOf(statements[1])
	args, _ := callArguments(call)
	_, _, sentence, ok := fileCrossingOf(ctx, statements, 1, args)
	if ok {
		t.Fatalf("a mismatched write path was recognized as a fit")
	}
	if sentence == "" {
		t.Fatalf("a mismatched write path was silently not-this-shape")
	}
	if !strings.Contains(sentence, "wrong_path.json") {
		t.Errorf("sentence %q does not name the mismatched written path", sentence)
	}
	if !strings.Contains(sentence, "carrier premise") {
		t.Errorf("sentence %q does not name the carrier premise", sentence)
	}
}

/* ── recognition, syntax halves ──────────────────────────────────── */

func TestForeignEdgeRecognition_ReadsTheArgvAndOptionsTheFixtureSpells(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function execFileSync(file: string, args: string[], options: unknown): string;
function f(boosted: number[]) {
	const stdout = execFileSync("python3", ["./audio_level.py"], {
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
	if name != "stdout" {
		t.Errorf("bound name = %q, want stdout", name)
	}
	args, _ := callArguments(call)
	if len(args) != 3 {
		t.Fatalf("len(args) = %d, want 3", len(args))
	}
	if interpreter, ok := stringLiteralText(args[0]); !ok || interpreter != "python3" {
		t.Errorf("argv[0] = %q (ok=%v), want python3", interpreter, ok)
	}
	runnerWord, script, _, scriptOk, sentence, _ := runnerAndScriptArgvOf(nil, args[0], args[1])
	if sentence != "" {
		t.Fatalf("the runner/script read declined: %s", sentence)
	}
	if !scriptOk || script != "./audio_level.py" {
		t.Errorf("argv[1] = %q (ok=%v), want ./audio_level.py", script, scriptOk)
	}
	if runnerWord != "python3" {
		t.Errorf("runnerWord = %q, want python3", runnerWord)
	}
	payload, encodingOk, sentence := execFileSyncOptionsOf(args[2])
	if sentence != "" {
		t.Fatalf("the options declined: %s", sentence)
	}
	if !encodingOk {
		t.Errorf(`encoding "utf8" was not read as making stdout a string`)
	}
	if payload == nil || !ast.IsIdentifier(payload) || payload.Text() != "boosted" {
		t.Errorf("the stringified payload is %v, want the identifier boosted", payload)
	}
	// the return leg's node: the sole JSON.parse of the bound name
	parse, at, parseSentence := soleParseConsumerOf(statements, 0, "stdout")
	if parseSentence != "" {
		t.Fatalf("the sole-parse scan declined: %s", parseSentence)
	}
	if at != 1 {
		t.Errorf("the parse sits in statement %d, want 1 — the override scopes to that one statement", at)
	}
	if !isForeignParseOf(parse, "stdout") {
		t.Errorf("the found node is not JSON.parse(stdout): %v", parse)
	}
}

// TestForeignEdgeRecognition_ATwoElementArgvNamesTheScriptAndTheDataElement
// pins the argv-value leg's own recognition: a plain interpreter's argv
// carrying [<script>, <data>] now reads as the script PLUS a data
// element, not "more arguments than this edge models" — d-data-legs.ts's
// numericValueAsArgvUndetermined row moving off the old refusal.
func TestForeignEdgeRecognition_ATwoElementArgvNamesTheScriptAndTheDataElement(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function execFileSync(file: string, args: string[], options: unknown): string;
function f() {
	const gain = "0.5";
	const stdout = execFileSync("python3", ["./targets/level_scalar_argv.py", gain], { encoding: "utf8" });
	return stdout;
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	_, call, _ := constBoundCallOf(statements[1])
	args, _ := callArguments(call)
	runnerWord, script, dataElement, ok, sentence, _ := runnerAndScriptArgvOf(nil, args[0], args[1])
	if sentence != "" {
		t.Fatalf("a two-element python argv declined: %s", sentence)
	}
	if !ok || runnerWord != "python3" || script != "./targets/level_scalar_argv.py" {
		t.Errorf("runnerWord=%q script=%q ok=%v, want python3 / ./targets/level_scalar_argv.py / true", runnerWord, script, ok)
	}
	if dataElement == nil || !ast.IsIdentifier(dataElement) || dataElement.Text() != "gain" {
		t.Fatalf("dataElement = %v, want the gain identifier node", dataElement)
	}
}

// TestForeignEdgeRecognition_AnArgvWithArgumentsBeyondTheScriptAndDataElementDeclines
// pins the remaining boundary: THREE or more argv elements for a plain
// interpreter still name a shape this edge does not model (neither a
// bare script nor a script-plus-one-data-element).
func TestForeignEdgeRecognition_AnArgvWithArgumentsBeyondTheScriptAndDataElementDeclines(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function execFileSync(file: string, args: string[], options: unknown): string;
function f() {
	const stdout = execFileSync("python3", ["./audio_level.py", "--fast", "--verbose"], { encoding: "utf8" });
	return stdout;
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	_, call, _ := constBoundCallOf(statements[0])
	args, _ := callArguments(call)
	// the target takes arguments this edge models nothing about
	if _, _, _, ok, sentence, _ := runnerAndScriptArgvOf(nil, args[0], args[1]); ok || sentence != "" {
		t.Errorf("a three-element argv was read as a modeled shape (ok=%v, sentence=%q)", ok, sentence)
	}
}

func TestForeignEdgeRecognition_AMissingEncodingIsReadAsABufferResult(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function execFileSync(file: string, args: string[], options: unknown): string;
function f(boosted: number[]) {
	const stdout = execFileSync("python3", ["./audio_level.py"], { input: JSON.stringify(boosted) });
	return JSON.parse(stdout);
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	_, call, _ := constBoundCallOf(statements[0])
	args, _ := callArguments(call)
	_, encodingOk, sentence := execFileSyncOptionsOf(args[2])
	if sentence != "" {
		t.Fatalf("the options declined for the wrong reason: %s", sentence)
	}
	if encodingOk {
		t.Errorf("a missing encoding was read as making stdout a string; without one the sync exec answers a Buffer")
	}
}

/* ── runner + script argv: the b-runners.ts / c-reference-shapes.ts legs ── */

func TestRunnerAndScriptArgvOf_BarePythonIsRecognized(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function execFileSync(file: string, args: string[], options: unknown): string;
function f() {
	const stdout = execFileSync("python", ["./targets/level_ok.py"], { encoding: "utf8" });
	return stdout;
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	_, call, _ := constBoundCallOf(statements[0])
	args, _ := callArguments(call)
	runnerWord, script, _, ok, sentence, _ := runnerAndScriptArgvOf(nil, args[0], args[1])
	if sentence != "" {
		t.Fatalf("bare python declined: %s", sentence)
	}
	if !ok || runnerWord != "python" || script != "./targets/level_ok.py" {
		t.Errorf("runnerWord=%q script=%q ok=%v, want python / ./targets/level_ok.py / true", runnerWord, script, ok)
	}
}

// TestRunnerAndScriptArgvOf_UvRunWithInterpreterIsRecognized pins
// b-runners.ts's uv-run row: `execFileSync("uv", ["run", "python3",
// "./targets/level_ok.py"], ...)` — a 3-element argv[1], not a
// runner-plus-one-script shape at all, read as "uv run" with the last
// element as the script.
func TestRunnerAndScriptArgvOf_UvRunWithInterpreterIsRecognized(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function execFileSync(file: string, args: string[], options: unknown): string;
function f() {
	const stdout = execFileSync("uv", ["run", "python3", "./targets/level_ok.py"], { encoding: "utf8" });
	return stdout;
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	_, call, _ := constBoundCallOf(statements[0])
	args, _ := callArguments(call)
	runnerWord, script, _, ok, sentence, _ := runnerAndScriptArgvOf(nil, args[0], args[1])
	if sentence != "" {
		t.Fatalf("uv run python3 <script> declined: %s", sentence)
	}
	if !ok || runnerWord != "uv run" || script != "./targets/level_ok.py" {
		t.Errorf("runnerWord=%q script=%q ok=%v, want %q / ./targets/level_ok.py / true", runnerWord, script, ok, "uv run")
	}
}

// TestRunnerAndScriptArgvOf_UvRunWithoutAnInterpreterIsRecognized pins
// the two-element uv shape the brief also names: `uv run <script>` with
// no interpreter word in between.
func TestRunnerAndScriptArgvOf_UvRunWithoutAnInterpreterIsRecognized(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function execFileSync(file: string, args: string[], options: unknown): string;
function f() {
	const stdout = execFileSync("uv", ["run", "./targets/level_ok.py"], { encoding: "utf8" });
	return stdout;
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	_, call, _ := constBoundCallOf(statements[0])
	args, _ := callArguments(call)
	runnerWord, script, _, ok, sentence, _ := runnerAndScriptArgvOf(nil, args[0], args[1])
	if sentence != "" {
		t.Fatalf("uv run <script> declined: %s", sentence)
	}
	if !ok || runnerWord != "uv run" || script != "./targets/level_ok.py" {
		t.Errorf("runnerWord=%q script=%q ok=%v, want %q / ./targets/level_ok.py / true", runnerWord, script, ok, "uv run")
	}
}

// TestRunnerAndScriptArgvOf_UvWithAnUnrecognizedMiddleInterpreterIsNotThisEdge
// pins that a THIRD uv element that is not a recognized python spelling
// is a different program, not a decline (uv can run non-python targets
// too, and this reader only ever claims python's own runner words).
func TestRunnerAndScriptArgvOf_UvWithAnUnrecognizedMiddleInterpreterIsNotThisEdge(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function execFileSync(file: string, args: string[], options: unknown): string;
function f() {
	const stdout = execFileSync("uv", ["run", "node", "./targets/level_ok.js"], { encoding: "utf8" });
	return stdout;
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	_, call, _ := constBoundCallOf(statements[0])
	args, _ := callArguments(call)
	_, _, _, ok, sentence, _ := runnerAndScriptArgvOf(nil, args[0], args[1])
	if ok || sentence != "" {
		t.Errorf("uv run node <script> was read as a python edge (ok=%v, sentence=%q)", ok, sentence)
	}
}

// TestScriptElementOf_AConstBoundIdentifierResolvesToItsLiteralInitializer
// pins c-reference-shapes.ts's pathInConstUndetermined row moving to
// RECOGNIZED: a `const scriptPath = "./targets/level_ok.py"` one
// statement above the call, spread by name into argv, now resolves
// through the same const-follow relational_accumulation.go's
// accumulationLengthNodeOf already performs for a `.length` read.
func TestScriptElementOf_AConstBoundIdentifierResolvesToItsLiteralInitializer(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function execFileSync(file: string, args: string[], options: unknown): string;
function f() {
	const scriptPath = "./targets/level_ok.py";
	const stdout = execFileSync("python3", [scriptPath], { encoding: "utf8" });
	return stdout;
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	ctx := relationalAccumulationContext(p)
	_, call, _ := constBoundCallOf(statements[1])
	args, _ := callArguments(call)
	runnerWord, script, _, ok, sentence, _ := runnerAndScriptArgvOf(ctx, args[0], args[1])
	if sentence != "" {
		t.Fatalf("a const-resolved script path declined: %s", sentence)
	}
	if !ok || runnerWord != "python3" || script != "./targets/level_ok.py" {
		t.Errorf("runnerWord=%q script=%q ok=%v, want python3 / ./targets/level_ok.py / true", runnerWord, script, ok)
	}
}

// TestScriptElementOf_AConcatenatedPathOfTwoConstsFolds pins
// c-reference-shapes.ts's pathByConcatenationUndetermined row moving
// from a law-2 decline to a resolved script path: the argv element is a
// `+` BinaryExpression, but BOTH sides are constant-foldable — a
// same-file const string on the left, a written literal on the right —
// so foldedConstStringOf (called from scriptElementOf, past the plain
// literal/identifier checks) folds the concatenation exactly, and the
// call resolves to the SAME target literalPathRecognized's plain
// literal names.
func TestScriptElementOf_AConcatenatedPathOfTwoConstsFolds(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function execFileSync(file: string, args: string[], options: unknown): string;
function f() {
	const directory = "./targets/";
	const stdout = execFileSync("python3", [directory + "level_ok.py"], { encoding: "utf8" });
	return stdout;
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	ctx := relationalAccumulationContext(p)
	_, call, _ := constBoundCallOf(statements[1])
	args, _ := callArguments(call)
	_, script, _, ok, sentence, _ := runnerAndScriptArgvOf(ctx, args[0], args[1])
	if sentence != "" {
		t.Fatalf("a const-concatenated script path declined: %s", sentence)
	}
	if !ok || script != "./targets/level_ok.py" {
		t.Errorf("script=%q ok=%v, want ./targets/level_ok.py / true", script, ok)
	}
}

// TestScriptElementOf_ATemplateSubstitutionOfAConstFolds pins
// c-reference-shapes.ts's pathViaTemplateSubstitutionUndetermined row
// moving from a law-2 decline to a resolved script path: the argv
// element is a TemplateExpression carrying one substitution, and that
// substitution names a same-file const string — foldedConstStringOf's
// TemplateExpression arm folds the head text, the substitution's own
// folded text, and the trailing literal text in source order.
func TestScriptElementOf_ATemplateSubstitutionOfAConstFolds(t *testing.T) {
	p := entryEnvTestProgram(t, "declare function execFileSync(file: string, args: string[], options: unknown): string;\n"+
		"function f() {\n"+
		"  const dir = \"./targets\";\n"+
		"  const stdout = execFileSync(\"python3\", [`${dir}/level_ok.py`], { encoding: \"utf8\" });\n"+
		"  return stdout;\n"+
		"}\n")
	statements := relationalAccumulationBodyOf(t, p, "f")
	ctx := relationalAccumulationContext(p)
	_, call, _ := constBoundCallOf(statements[1])
	args, _ := callArguments(call)
	_, script, _, ok, sentence, _ := runnerAndScriptArgvOf(ctx, args[0], args[1])
	if sentence != "" {
		t.Fatalf("a const-templated script path declined: %s", sentence)
	}
	if !ok || script != "./targets/level_ok.py" {
		t.Errorf("script=%q ok=%v, want ./targets/level_ok.py / true", script, ok)
	}
}

// TestScriptElementOf_AParameterHeldPathOwesTheLawTwoSentence pins
// c-reference-shapes.ts's pathFromParameterUndetermined row: the argv
// element is an identifier, but its ValueDeclaration is a
// ParameterDeclaration, not a const VariableDeclaration — resolution
// fails and the same law-2 sentence is owed, not silence.
func TestScriptElementOf_AParameterHeldPathOwesTheLawTwoSentence(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function execFileSync(file: string, args: string[], options: unknown): string;
function f(scriptPath: string) {
	const stdout = execFileSync("python3", [scriptPath], { encoding: "utf8" });
	return stdout;
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	ctx := relationalAccumulationContext(p)
	_, call, _ := constBoundCallOf(statements[0])
	args, _ := callArguments(call)
	_, _, _, ok, sentence, sentenceNode := runnerAndScriptArgvOf(ctx, args[0], args[1])
	if ok {
		t.Fatalf("a parameter-held path was read as a resolved script")
	}
	if sentence != scriptPathLawTwoSentence {
		t.Errorf("sentence = %q, want %q", sentence, scriptPathLawTwoSentence)
	}
	if sentenceNode == nil {
		t.Errorf("the law-2 sentence carries no node to point at")
	}
}

// TestScriptElementOf_ALetBoundIdentifierDoesNotResolve pins the const-
// only gate itself: a `let` the following statements could rewrite
// carries no fixed value, so the follow must not treat it as resolved.
func TestScriptElementOf_ALetBoundIdentifierDoesNotResolve(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function execFileSync(file: string, args: string[], options: unknown): string;
function f() {
	let scriptPath = "./targets/level_ok.py";
	const stdout = execFileSync("python3", [scriptPath], { encoding: "utf8" });
	return stdout;
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	ctx := relationalAccumulationContext(p)
	_, call, _ := constBoundCallOf(statements[1])
	args, _ := callArguments(call)
	_, _, _, ok, sentence, _ := runnerAndScriptArgvOf(ctx, args[0], args[1])
	if ok {
		t.Fatalf("a let-bound path was read as a resolved script")
	}
	if sentence != scriptPathLawTwoSentence {
		t.Errorf("sentence = %q, want %q", sentence, scriptPathLawTwoSentence)
	}
}

/* ── spawnSync: same argv/options shape, a result OBJECT ─────────────── */

func TestSpawnSyncEdgeOf_ReadsTheSameArgvAndOptionsShapeAsExecFileSync(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function spawnSync(file: string, args: string[], options: unknown): { stdout: string };
function f(boosted: number[]) {
	const result = spawnSync("python3", ["./targets/level_ok.py"], {
		input: JSON.stringify(boosted),
		encoding: "utf8",
	});
	return JSON.parse(result.stdout);
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	name, call, ok := constBoundCallOf(statements[0])
	if !ok {
		t.Fatalf("the const-bound spawnSync call was not read")
	}
	edge, recognized, sentence, _, path := spawnSyncEdgeOf(nil, call, name, statements, 0)
	if sentence != "" {
		t.Fatalf("spawnSync declined: %s", sentence)
	}
	if !recognized || edge == nil {
		t.Fatalf("spawnSync was not recognized")
	}
	if edge.StdoutName != "result" {
		t.Errorf("StdoutName = %q, want result", edge.StdoutName)
	}
	if !strings.HasSuffix(path, filepath.Join("targets", "level_ok.py")) {
		t.Errorf("TargetPath = %q, want it to end in targets/level_ok.py", path)
	}
	// the return leg reads result.stdout, not a bare identifier
	parse, at, parseSentence := soleParseConsumerOf(statements, 0, edge.StdoutName)
	if parseSentence != "" {
		t.Fatalf("the sole-parse scan declined: %s", parseSentence)
	}
	if at != 1 {
		t.Errorf("the parse sits in statement %d, want 1", at)
	}
	if !isForeignParseOf(parse, "result") {
		t.Errorf("the found node is not JSON.parse(result.stdout): %v", parse)
	}
}

func TestIsForeignParseOf_ReadsBothTheBareNameAndTheDotStdoutShape(t *testing.T) {
	p := entryEnvTestProgram(t, `
function f(result: { stdout: string }, stdout: string) {
	JSON.parse(result.stdout);
	JSON.parse(stdout);
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	var resultDotStdout, bareStdout *ast.Node
	countResult, countBare := 0, 0
	foreignParseCallsIn(statements[0], "result", &resultDotStdout, &countResult)
	foreignParseCallsIn(statements[1], "stdout", &bareStdout, &countBare)
	if countResult != 1 || resultDotStdout == nil {
		t.Fatalf("JSON.parse(result.stdout) was not read as result's parse: count=%d", countResult)
	}
	if countBare != 1 || bareStdout == nil {
		t.Fatalf("JSON.parse(stdout) was not read as stdout's parse: count=%d", countBare)
	}
}

/* ── execSync: a shell command STRING, not an argv array ─────────────── */

// TestExecSyncEdgeOf_ALiteralSimpleCommandIsFollowedAndJudged pins the
// one execSync shape this reader follows: a written string literal
// tokenizing on single spaces into exactly a runner word and a `.py`
// path, neither token carrying any shell syntax.
func TestExecSyncEdgeOf_ALiteralSimpleCommandIsFollowedAndJudged(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function execSync(command: string, options: unknown): string;
function f() {
	const stdout = execSync("python3 ./targets/level_ok.py", { encoding: "utf8" });
	return JSON.parse(stdout);
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	name, call, ok := constBoundCallOf(statements[0])
	if !ok {
		t.Fatalf("the const-bound execSync call was not read")
	}
	edge, recognized, sentence, _, path := execSyncEdgeOf(call, name)
	if sentence != "" {
		t.Fatalf("a literal simple command declined: %s", sentence)
	}
	if !recognized || edge == nil {
		t.Fatalf("a literal simple execSync command was not recognized")
	}
	if edge.StdoutName != "stdout" {
		t.Errorf("StdoutName = %q, want stdout — execSync's bound name IS the stdout string", edge.StdoutName)
	}
	if !strings.HasSuffix(path, filepath.Join("targets", "level_ok.py")) {
		t.Errorf("TargetPath = %q, want it to end in targets/level_ok.py", path)
	}
	// the return leg reads the bound name directly, exactly like execFileSync's
	parse, at, parseSentence := soleParseConsumerOf(statements, 0, edge.StdoutName)
	if parseSentence != "" {
		t.Fatalf("the sole-parse scan declined: %s", parseSentence)
	}
	if at != 1 {
		t.Errorf("the parse sits in statement %d, want 1", at)
	}
	if !isForeignParseOf(parse, "stdout") {
		t.Errorf("the found node is not JSON.parse(stdout): %v", parse)
	}
}

// TestExecSyncEdgeOf_ATemplateWithASubstitutionRecognizesTheHeredocShape
// pins the ONE substitution shape execSyncHeredocCommandOf recognizes:
// a template whose constant prefix is `<runner> <script> <<<` and whose
// single substitution is JSON.stringify(<payload>) — the stdin-json
// convention spelled through a shell here-string
// (a-invocation-functions.ts's own execSyncUndetermined row, now
// recognized rather than declined). A template substitution that is NOT
// this narrow shape still owes the ordinary shell-string sentence — see
// TestExecSyncEdgeOf_ATemplateSubstitutionThatIsNotTheHeredocShapeStillDeclines.
func TestExecSyncEdgeOf_ATemplateWithASubstitutionRecognizesTheHeredocShape(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function execSync(command: string, options: unknown): string;
function f(samples: number[]) {
	const stdout = execSync(
		` + "`python3 ./targets/level_ok.py <<< '${JSON.stringify(samples)}'`" + `,
		{ encoding: "utf8" },
	);
	return JSON.parse(stdout);
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	name, call, _ := constBoundCallOf(statements[0])
	edge, recognized, sentence, _, _ := execSyncEdgeOf(call, name)
	if sentence != "" {
		t.Fatalf("the heredoc shape declined: %s", sentence)
	}
	if !recognized || edge == nil {
		t.Fatalf("the heredoc shape was not recognized")
	}
	if edge.Payload == nil || !ast.IsIdentifier(edge.Payload) || edge.Payload.Text() != "samples" {
		t.Errorf("edge.Payload = %v, want the identifier samples", edge.Payload)
	}
}

// TestExecSyncEdgeOf_ATemplateSubstitutionThatIsNotTheHeredocShapeStillDeclines
// pins the boundary: a template substitution that does NOT match the
// narrow heredoc shape (here, a substitution that is not
// JSON.stringify(...)) still owes the ordinary shell-string sentence —
// the recognizer is exactly as narrow as its own doc states.
func TestExecSyncEdgeOf_ATemplateSubstitutionThatIsNotTheHeredocShapeStillDeclines(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function execSync(command: string, options: unknown): string;
function f(samples: string) {
	const stdout = execSync(
		` + "`python3 ./targets/level_ok.py <<< '${samples}'`" + `,
		{ encoding: "utf8" },
	);
	return JSON.parse(stdout);
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	name, call, _ := constBoundCallOf(statements[0])
	edge, recognized, sentence, sentenceNode, _ := execSyncEdgeOf(call, name)
	if recognized || edge != nil {
		t.Fatalf("a non-stringify substitution was read as a followed command")
	}
	if sentence != execSyncShellStringSentence {
		t.Errorf("sentence = %q, want %q", sentence, execSyncShellStringSentence)
	}
	if sentenceNode == nil {
		t.Errorf("the law-2 sentence carries no node to point at")
	}
}

func TestExecSyncEdgeOf_APipeInTheCommandOwesTheShellStringSentence(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function execSync(command: string, options: unknown): string;
function f() {
	const stdout = execSync("python3 ./targets/level_ok.py | cat", { encoding: "utf8" });
	return JSON.parse(stdout);
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	name, call, _ := constBoundCallOf(statements[0])
	_, recognized, sentence, _, _ := execSyncEdgeOf(call, name)
	if recognized {
		t.Fatalf("a piped command was read as a followed command")
	}
	if sentence != execSyncShellStringSentence {
		t.Errorf("sentence = %q, want %q", sentence, execSyncShellStringSentence)
	}
}

func TestExecSyncSimpleCommandTokens_SplitsOnSingleSpacesAndRejectsShellSyntax(t *testing.T) {
	if _, _, ok := execSyncSimpleCommandTokens("python3 ./targets/level_ok.py"); !ok {
		t.Errorf("a plain two-word command was not read")
	}
	if _, _, ok := execSyncSimpleCommandTokens("python3 ./a.py ./b.py"); ok {
		t.Errorf("a three-token command was read as a two-word one")
	}
	for _, unsupported := range []string{
		"python3 './targets/level_ok.py'",
		"python3 $HOME/level_ok.py",
		"python3 ./a.py > out.txt",
		"python3 ./a.py < in.json",
		"python3 ./a.py & echo done",
		"python3 ./a.py; echo done",
		"python3 `./a.py`",
	} {
		if _, _, ok := execSyncSimpleCommandTokens(unsupported); ok {
			t.Errorf("%q was read as a plain simple command", unsupported)
		}
	}
}

/* ── spawn (async): recognized, and its own "does not determine (yet)" ── */

// TestSpawnAsyncEdgeOf_TheArgvIsRecognizedAndTheAsyncResultOwesItsOwnSentence
// pins a-invocation-functions.ts's spawnUndetermined row: the call
// itself is recognized (the argv names the script exactly as
// execFileSync's does), and the sentence names the missing reader —
// "does not determine (yet)" — never "cannot".
func TestSpawnAsyncEdgeOf_TheArgvIsRecognizedAndTheAsyncResultOwesItsOwnSentence(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function spawn(file: string, args: string[]): unknown;
function f() {
	const child = spawn("python3", ["./targets/level_ok.py"]);
	return child;
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	name, call, ok := constBoundCallOf(statements[0])
	if !ok {
		t.Fatalf("the const-bound spawn call was not read")
	}
	edge, recognized, sentence, sentenceNode, path := spawnAsyncEdgeOf(nil, call, name, statements, 0)
	if edge != nil {
		t.Fatalf("spawn (async) answered a served edge: %+v", edge)
	}
	if recognized {
		t.Fatalf("spawn (async) answered recognized=true with no edge")
	}
	if !strings.Contains(sentence, "does not determine (yet)") {
		t.Errorf("sentence %q does not use the does-not-determine-yet wording", sentence)
	}
	if strings.Contains(sentence, "cannot") {
		t.Errorf("sentence %q says \"cannot\" rather than naming what would resolve it", sentence)
	}
	if sentenceNode == nil {
		t.Errorf("the sentence carries no node to point at")
	}
	if !strings.HasSuffix(path, filepath.Join("targets", "level_ok.py")) {
		t.Errorf("TargetPath = %q, want it to end in targets/level_ok.py — the reference is still read", path)
	}
}

/* ── spawnReturnLegOf: the accumulate-then-parse `.on()` pair reader ── */

// spawnPairSource is the declared-shape scaffold every spawnReturnLegOf
// case parses against — a minimal EventEmitter-shaped spawn result, so
// the fixture's own accumulate-then-parse body type-checks without a
// real @types/node.
const spawnPairSource = `
declare function spawn(file: string, args: string[]): {
	stdin: { write(chunk: string): void; end(): void };
	stdout: { on(event: string, cb: (chunk: string) => void): void };
	on(event: string, cb: () => void): void;
};
`

// TestSpawnReturnLegOf_TheAccumulateThenParsePairIsRecognized pins
// a-invocation-functions.ts's spawnUndetermined row: the accumulator
// (out) and the JSON.parse node inside the close handler are both
// named.
func TestSpawnReturnLegOf_TheAccumulateThenParsePairIsRecognized(t *testing.T) {
	p := entryEnvTestProgram(t, spawnPairSource+`
function f() {
	const child = spawn("python3", ["./targets/level_ok.py"]);
	let out = "";
	child.stdout.on("data", (d) => {
		out += d;
	});
	child.on("close", () => {
		const level = JSON.parse(out);
		return level;
	});
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	leg, sentence := spawnReturnLegOf(statements, 0, "child")
	if sentence != "" {
		t.Fatalf("the accumulate-then-parse pair declined: %s", sentence)
	}
	if leg.AccumulatorName != "out" {
		t.Errorf("AccumulatorName = %q, want out", leg.AccumulatorName)
	}
	if leg.DataHandler == nil || leg.CloseHandler == nil {
		t.Fatalf("the handlers were not both recorded: %+v", leg)
	}
	if leg.ParseNode == nil || !isForeignParseOf(leg.ParseNode, "out") {
		t.Errorf("ParseNode = %v, want JSON.parse(out)", leg.ParseNode)
	}
}

// TestSpawnReturnLegOf_ACallbackReadingADifferentNameThanItsOwnParameterIsNotRecognized
// pins the specific-parameter discipline: the 'data' handler's `+=`
// right side is an outer identifier spelled "d" that is NOT the
// handler's own parameter — name equality is not enough.
func TestSpawnReturnLegOf_ACallbackReadingADifferentNameThanItsOwnParameterIsNotRecognized(t *testing.T) {
	p := entryEnvTestProgram(t, spawnPairSource+`
function f(d: string) {
	const child = spawn("python3", ["./targets/level_ok.py"]);
	let out = "";
	child.stdout.on("data", () => {
		out += d;
	});
	child.on("close", () => {
		const level = JSON.parse(out);
		return level;
	});
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	_, sentence := spawnReturnLegOf(statements, 0, "child")
	if sentence == "" {
		t.Fatalf("a handler reading an outer name rather than its own parameter was recognized")
	}
	if !strings.Contains(sentence, "no accumulator is named") {
		t.Errorf("sentence %q does not name the accumulator gap", sentence)
	}
}

// TestSpawnReturnLegOf_AMissingCloseHandlerIsNotRecognized pins
// a-invocation-functions.ts's ORIGINAL spawnUndetermined body: a 'data'
// handler alone, with no 'close' handler at all.
func TestSpawnReturnLegOf_AMissingCloseHandlerIsNotRecognized(t *testing.T) {
	p := entryEnvTestProgram(t, spawnPairSource+`
function f() {
	const child = spawn("python3", ["./targets/level_ok.py"]);
	let out = "";
	child.stdout.on("data", (d) => {
		out += d;
	});
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	_, sentence := spawnReturnLegOf(statements, 0, "child")
	if sentence == "" {
		t.Fatalf("a missing 'close' handler was recognized")
	}
	if !strings.Contains(sentence, `on("close"`) {
		t.Errorf("sentence %q does not name the missing close handler", sentence)
	}
}

// TestSpawnReturnLegOf_AnInterveningWriteToTheAccumulatorIsNotRecognized
// pins the hazard the brief calls out: a THIRD statement (neither
// handler) writing the accumulator between the call and the close
// handler must decline — the value the close handler reads is then not
// the value the two handlers alone accumulated.
func TestSpawnReturnLegOf_AnInterveningWriteToTheAccumulatorIsNotRecognized(t *testing.T) {
	p := entryEnvTestProgram(t, spawnPairSource+`
function f() {
	const child = spawn("python3", ["./targets/level_ok.py"]);
	let out = "";
	child.stdout.on("data", (d) => {
		out += d;
	});
	out = "reset";
	child.on("close", () => {
		const level = JSON.parse(out);
		return level;
	});
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	_, sentence := spawnReturnLegOf(statements, 0, "child")
	if sentence == "" {
		t.Fatalf("an intervening write to the accumulator was recognized")
	}
	if !strings.Contains(sentence, "a statement other than the 'data' handler writes out") {
		t.Errorf("sentence %q does not name the intervening write", sentence)
	}
}

func TestForeignEdgeRecognition_TwoParsesOfTheStdoutBindingDecline(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function execFileSync(file: string, args: string[], options: unknown): string;
function f(boosted: number[]) {
	const stdout = execFileSync("python3", ["./audio_level.py"], {
		input: JSON.stringify(boosted),
		encoding: "utf8",
	});
	const a = JSON.parse(stdout);
	const b = JSON.parse(stdout);
	return [a, b];
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	// one published fact cannot stand for two expressions
	if _, _, sentence := soleParseConsumerOf(statements, 0, "stdout"); sentence == "" {
		t.Errorf("two parses of the stdout binding were served one fact")
	} else if !strings.Contains(sentence, "parsed 2 times") {
		t.Errorf("the sentence %q does not say how many consumers there are", sentence)
	}
}

// TestForeignEdgeRecognition_NoParseOfTheStdoutBindingIsVacuouslyFine
// pins construct 2's own determination: a recognized crossing whose
// result NO expression consumes needs no fact at all — soleParseConsumerOf
// answers (nil, -1, "") — a NIL node, an empty sentence — rather than a
// decline. There is no expression for a fact to attach to, and that is
// not a defect: the outbound leg's own judgment (a separate premise
// this function does not touch) still stands unchanged.
func TestForeignEdgeRecognition_NoParseOfTheStdoutBindingIsVacuouslyFine(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function execFileSync(file: string, args: string[], options: unknown): string;
function f(boosted: number[]) {
	const stdout = execFileSync("python3", ["./audio_level.py"], {
		input: JSON.stringify(boosted),
		encoding: "utf8",
	});
	return stdout.length;
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	found, at, sentence := soleParseConsumerOf(statements, 0, "stdout")
	if sentence != "" {
		t.Errorf("a stdout binding nothing parses reported a decline %q, want none", sentence)
	}
	if found != nil {
		t.Errorf("soleParseConsumerOf found a parse node %+v where none exists", found)
	}
	if at != -1 {
		t.Errorf("soleParseConsumerOf answered statement index %d for an absent consumer, want -1", at)
	}
}

// TestForeignEdgeRecognition_AParseInsideANestedFunctionIsVacuouslyFine
// is the nested-function twin of the "no consumer" row above: the arrow
// runs an unstated number of times, so foreignParseCallsIn's own
// function-boundary skip never counts it — from soleParseConsumerOf's
// point of view this body has NO top-level consumer either, and answers
// the same (nil, -1, "") as a body with no JSON.parse anywhere.
func TestForeignEdgeRecognition_AParseInsideANestedFunctionIsVacuouslyFine(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function execFileSync(file: string, args: string[], options: unknown): string;
function f(boosted: number[]) {
	const stdout = execFileSync("python3", ["./audio_level.py"], {
		input: JSON.stringify(boosted),
		encoding: "utf8",
	});
	return () => JSON.parse(stdout);
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	// the arrow runs an unstated number of times, so no fact can be
	// pinned to one evaluation of that node — and, as with the "no
	// consumer at all" row, that is not a defect: there is simply no
	// top-level expression for a fact to land on.
	found, _, sentence := soleParseConsumerOf(statements, 0, "stdout")
	if sentence != "" {
		t.Errorf("a parse inside a nested function reported a decline %q, want none", sentence)
	}
	if found != nil {
		t.Errorf("soleParseConsumerOf found a parse node %+v inside a nested function", found)
	}
}

func TestForeignEdgeAt_ACallToSomeOtherProgramIsNotAnEdgeAndOwesNoSentence(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function execFileSync(file: string, args: string[], options: unknown): string;
function f() {
	const out = execFileSync("ls", ["-l"], { encoding: "utf8" });
	return out;
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	ctx := relationalAccumulationContext(p)
	if outcome, isEdge := ForeignEdgeAt(ctx, NewEnv(), statements, 0); isEdge {
		t.Errorf("a call to a non-python program was read as a cross-language edge: %+v", outcome)
	}
}

func TestForeignEdgeAt_AnOrdinaryStatementIsNotAnEdge(t *testing.T) {
	p := entryEnvTestProgram(t, "function f(x: number) { const y = x + 1; return y; }\n")
	statements := relationalAccumulationBodyOf(t, p, "f")
	ctx := relationalAccumulationContext(p)
	if outcome, isEdge := ForeignEdgeAt(ctx, NewEnv(), statements, 0); isEdge {
		t.Errorf("an ordinary declaration was read as a cross-language edge: %+v", outcome)
	}
}

/* ── the consumer-index push (analyze_statement.go's ForeignEdgeAt call) ── */
//
// A whole-route ForeignEdgeAt call that RECOGNIZES an edge (isEdge true)
// needs resolvesToChildProcessMember to resolve execFileSync to a
// real child_process.d.ts declaration, which this file's own banner
// names as deliberately not stood up here (a service-level fixture, not
// a walk-level one — E4's row is the real end-to-end check). So the
// "a fixture edge records its path" half of this unit cannot be pinned
// at this grain without building exactly the fixture the banner declines
// to fake; only the reachable half — an ordinary statement pushes
// nothing — is asserted here.

// TestAnalyzeStatements_AnOrdinaryStatementPushesNothingToTheForeignSink
// pins the other side of the push: listWalk's new
// `if running.ConsumedForeignSink != nil && outcome.TargetPath != ""`
// line never fires for a statement ForeignEdgeAt does not even
// recognize (isEdge false, no outcome at all) — the sink stays exactly
// as empty as a walk that never heard of the cross-language edge.
func TestAnalyzeStatements_AnOrdinaryStatementPushesNothingToTheForeignSink(t *testing.T) {
	p := entryEnvTestProgram(t, "function f(x: number) { const y = x + 1; return y; }\n")
	statements := relationalAccumulationBodyOf(t, p, "f")
	ctx := relationalAccumulationContext(p)
	var consumed []string
	ctx.ConsumedForeignSink = &consumed
	env := NewEnv()
	env.Set("x", abstractdomain.KnownValues([]float64{1}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved))
	AnalyzeStatements(ctx, env, statements, nil)
	if len(consumed) != 0 {
		t.Errorf("ConsumedForeignSink = %v, want empty — no edge was ever recognized here", consumed)
	}
}

/* ── the return leg's ±Infinity corner (json.dumps spells a token JSON.parse rejects) ── */
//
// json.dumps(float("inf")) writes the bare token `Infinity` rather than
// a JSON number literal (JSON itself is not at fault: `1e999` is a
// legal JSON number and parses to Infinity in both runtimes — the bare
// token is Python's default serializer's own choice), and JSON.parse
// throws on that token at runtime. A return set admitting either
// infinite corner must not bind as the parse's fact: it degrades to a
// named undetermined instead. foreignReturnCornerObstacle is the gate;
// these pin it directly against a real kernel's Member ask (x ∈ A), the
// same idiom effect_math_test.go's own Math.min/max corner checks and
// kernel_bridge_test.go's ℝ̄∖{0} row already use.

// foreignReturnCornerKernel loads the same kernel every other
// kernel-backed test in this file loads — skips (never a faked pass)
// when the native dylib is absent.
func foreignReturnCornerKernel(t *testing.T) *kernelbridge.RefinedTSKernel {
	t.Helper()
	return nanWrapperLoadKernel(t)
}

func TestForeignReturnCornerObstacle_APlusInfinityAdmittingSetDegrades(t *testing.T) {
	ctx := &FlowContext{Kernel: foreignReturnCornerKernel(t)}
	// ℝ̄ ∖ {0}: the same "admits +∞" set kernel_bridge_test.go's own
	// TestMembershipTheRuntimeCheckOverTheWireRoundTrip pins Member true
	// for at +∞ — a derived Python return this wide (e.g. no upper
	// bound at all) reaches this shape.
	admitsPosInf := refinementsets.MakeRefinedSet(refinementsets.Difference(
		refinementsets.Numbers, refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{0})),
	))
	sentence := foreignReturnCornerObstacle(ctx, admitsPosInf)
	if sentence == "" {
		t.Fatalf("a +Infinity-admitting return set answered no obstacle, want the named corner")
	}
	if !strings.Contains(sentence, "Infinity") {
		t.Errorf("sentence %q does not name the Infinity corner", sentence)
	}
	if !strings.Contains(sentence, "JSON") {
		t.Errorf("sentence %q does not name json.dumps's bare Infinity token that JSON.parse rejects", sentence)
	}
}

func TestForeignReturnCornerObstacle_AMinusInfinityAdmittingSetDegradesIdentically(t *testing.T) {
	ctx := &FlowContext{Kernel: foreignReturnCornerKernel(t)}
	// AtMost(0): a one-sided ray to -∞ admits -Infinity as a member
	// (the same ray shape refinement_forms_test.go's own
	// TestInfinitiesAreElementsNaNIsRefused pins AtLeast/AtMost's
	// corners as elements, never excluded members).
	admitsNegInf := refinementsets.MakeRefinedSet(refinementsets.AtMost(0))
	sentence := foreignReturnCornerObstacle(ctx, admitsNegInf)
	if sentence == "" {
		t.Fatalf("a -Infinity-admitting return set answered no obstacle, want the named corner")
	}
	if !strings.Contains(sentence, "-Infinity") {
		t.Errorf("sentence %q does not name the -Infinity corner", sentence)
	}
	if !strings.Contains(sentence, "JSON") {
		t.Errorf("sentence %q does not name json.dumps's bare Infinity token that JSON.parse rejects", sentence)
	}
}

// TestForeignReturnCornerObstacle_AFiniteWindowBindsExactlyAsBefore is
// the regression pin: audio_level.py's own real return window (0 … 1,
// the exact shape foreignArtifactJSON states and TestCheckOutboundLeg's
// own fixtures already exercise) admits neither corner, so the gate
// answers no obstacle and foreignReturnValue serves the same
// TrustSpec-graded fact it always has.
func TestForeignReturnCornerObstacle_AFiniteWindowBindsExactlyAsBefore(t *testing.T) {
	ctx := &FlowContext{Kernel: foreignReturnCornerKernel(t)}
	finiteWindow := refinementsets.MakeRefinedSet(
		refinementsets.AtLeast(0), refinementsets.AtMost(1),
	)
	if sentence := foreignReturnCornerObstacle(ctx, finiteWindow); sentence != "" {
		t.Fatalf("a finite 0 … 1 return window reported an obstacle, want none: %q", sentence)
	}
	artifact := &ForeignArtifact{Called: ForeignFunctionFact{
		Name:   "audio_level",
		Return: ForeignReturn{Cases: []Case{{Sort: CaseSortNumber, Set: finiteWindow}}, StdoutPure: true},
	}}
	got := foreignReturnValue(artifact)
	want := abstractdomain.KnownSet(finiteWindow, nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("foreignReturnValue(finite window) = %+v, want %+v — a finite return must bind unchanged", got, want)
	}
}

// TestForeignReturnCornerObstacle_ARefusedQuestionAnswersNoObstacle pins
// the "no proof, no obstacle" reading a nil kernel gives — the same
// fallthrough foreignScalarSubset and subsetProved already answer for a
// question the kernel cannot decide, so an untested corner never
// falsely degrades a set the checker simply could not ask about.
func TestForeignReturnCornerObstacle_ARefusedQuestionAnswersNoObstacle(t *testing.T) {
	ctx := &FlowContext{}
	admitsPosInf := refinementsets.MakeRefinedSet(refinementsets.AtLeast(0))
	if sentence := foreignReturnCornerObstacle(ctx, admitsPosInf); sentence != "" {
		t.Errorf("a nil kernel answered an obstacle sentence %q, want none (refused, not refuted)", sentence)
	}
}

/* ── construct 1: the ±Infinity corner DETERMINES a finite return ── */
//
// h-numeric-edges.ts's maybeInfiniteStaticallySilentRuntimeThrows row: a
// return set admitting +Infinity no longer declines — the corner is
// DIFFERENCED OUT (foreignFiniteReturnSet) and the finite remainder
// binds, because a completed JSON.parse call never actually carries the
// corner value (every concrete run that would have is a thrown
// SyntaxError, per json.dumps's own bare-Infinity-token behavior).

// TestForeignFiniteReturnSet_APlusInfinityAdmittingSetNarrowsToFinite
// pins the ray-narrowing case h-numeric-edges.ts's own H3 row derives:
// atLeast(0) (an unbounded ray admitting +Infinity) narrows to a set
// that STILL admits every finite value at or above 0, but no longer
// admits +Infinity itself.
func TestForeignFiniteReturnSet_APlusInfinityAdmittingSetNarrowsToFinite(t *testing.T) {
	ctx := &FlowContext{Kernel: foreignReturnCornerKernel(t)}
	admitsPosInf := refinementsets.MakeRefinedSet(refinementsets.AtLeast(0))
	narrowed := foreignFiniteReturnSet(ctx, admitsPosInf)
	if ctx.Kernel.Member(narrowed, []float64{math.Inf(1)}) {
		t.Errorf("foreignFiniteReturnSet(atLeast(0)) still admits +Infinity, want it differenced out")
	}
	if !ctx.Kernel.Member(narrowed, []float64{1000}) {
		t.Errorf("foreignFiniteReturnSet(atLeast(0)) no longer admits an ordinary finite member (1000)")
	}
	if !ctx.Kernel.Member(narrowed, []float64{0}) {
		t.Errorf("foreignFiniteReturnSet(atLeast(0)) no longer admits its own finite floor (0)")
	}
}

// TestForeignFiniteReturnSet_AMinusInfinityAdmittingSetNarrowsToFinite
// is the mirror for a ray to -∞ (AtMost(0)).
func TestForeignFiniteReturnSet_AMinusInfinityAdmittingSetNarrowsToFinite(t *testing.T) {
	ctx := &FlowContext{Kernel: foreignReturnCornerKernel(t)}
	admitsNegInf := refinementsets.MakeRefinedSet(refinementsets.AtMost(0))
	narrowed := foreignFiniteReturnSet(ctx, admitsNegInf)
	if ctx.Kernel.Member(narrowed, []float64{math.Inf(-1)}) {
		t.Errorf("foreignFiniteReturnSet(atMost(0)) still admits -Infinity, want it differenced out")
	}
	if !ctx.Kernel.Member(narrowed, []float64{-1000}) {
		t.Errorf("foreignFiniteReturnSet(atMost(0)) no longer admits an ordinary finite member (-1000)")
	}
}

// TestForeignFiniteReturnSet_AFiniteWindowIsUnchanged pins the no-op
// case: a set admitting neither corner (audio_level.py's own 0…1 shape)
// answers unchanged — narrowing a set with nothing to narrow is a no-op,
// not a spurious difference-with-nothing wrapper.
func TestForeignFiniteReturnSet_AFiniteWindowIsUnchanged(t *testing.T) {
	ctx := &FlowContext{Kernel: foreignReturnCornerKernel(t)}
	finiteWindow := refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.AtMost(1))
	narrowed := foreignFiniteReturnSet(ctx, finiteWindow)
	if !reflect.DeepEqual(narrowed, finiteWindow) {
		t.Errorf("foreignFiniteReturnSet(0…1) = %+v, want unchanged %+v", narrowed, finiteWindow)
	}
}

// TestForeignFiniteReturnSet_ARefusedQuestionAnswersUnchanged pins the
// "no proof, no narrowing" reading a nil kernel gives — the same
// fallthrough foreignReturnCornerObstacle itself already answers for a
// refused question, so an untested corner never gets narrowed away on
// a set the checker simply could not ask about.
func TestForeignFiniteReturnSet_ARefusedQuestionAnswersUnchanged(t *testing.T) {
	ctx := &FlowContext{}
	admitsPosInf := refinementsets.MakeRefinedSet(refinementsets.AtLeast(0))
	narrowed := foreignFiniteReturnSet(ctx, admitsPosInf)
	if !reflect.DeepEqual(narrowed, admitsPosInf) {
		t.Errorf("foreignFiniteReturnSet with a nil kernel = %+v, want unchanged %+v", narrowed, admitsPosInf)
	}
}

/* ── item 1: object-shaped values cross the wire (a Result-style return) ── */

// foreignResultTargetSource is the Python body a Result-shaped
// artifact describes: two object cases in one return cases list —
// {"ok": true, "value": <number>} on success, {"ok": false, "error":
// <string>} on failure — the RULED schema's own reading of a
// Result-style union.
const foreignResultTargetSource = "def parse_level(text):\n" +
	"    try:\n" +
	"        return {\"ok\": True, \"value\": float(text)}\n" +
	"    except ValueError:\n" +
	"        return {\"ok\": False, \"error\": \"not a number\"}\n"

// writeForeignResultTarget writes the Result-shaped target into a
// fresh temp directory and answers its path and the sha256 the
// artifact must state to match it.
func writeForeignResultTarget(t *testing.T) (string, string) {
	t.Helper()
	targetPath := filepath.Join(t.TempDir(), "parse_level.py")
	if err := os.WriteFile(targetPath, []byte(foreignResultTargetSource), 0o644); err != nil {
		t.Fatalf("writing the target: %v", err)
	}
	sum := sha256.Sum256([]byte(foreignResultTargetSource))
	return targetPath, "sha256:" + hex.EncodeToString(sum[:])
}

// stringsWireSet is refinementsets.Strings' own wire text
// (kernelbridge.EncodeSet(refinementsets.Strings)), spelled once here
// through the real encoder rather than hand-transcribed — a hand-
// written "star of codepoints" guess drifted from the decoder's actual
// form names (wire_decode.go's "star" case reads a capitalized "A"
// wrapping a full nested set, never a bare {"form":"codepoints"}).
var stringsWireSet = kernelbridge.EncodeSet(refinementsets.Strings)

// foreignResultArtifactJSON builds an artifact whose return "cases" is
// the RULED schema's object vocabulary: two object cases, each CLOSED
// (the producer states the exact key set each branch holds) — the
// success branch {"ok": boolean-true-only, "value": a finite number}
// and the failure branch {"ok": boolean-false-only, "error": a
// string}.
func foreignResultArtifactJSON(contentHash string, targetFile string) string {
	return `{
  "refined": {"kind": "fact-artifact"},
  "target": {"file": "` + targetFile + `", "contentHash": "` + contentHash + `"},
  "language": "python",
  "runtime": {"band": "cpython-3.11+"},
  "surface": {"kind": "stdin-json", "stdin": "json", "stdout": "json", "calls": "parse_level"},
  "functions": {
    "parse_level": {
      "entry": [{"name": "text", "cases": [{"sort": "string", "set": ` + stringsWireSet + `}]}],
      "return": {"cases": [
        {"sort": "object", "closed": true, "members": {
          "ok": [{"sort": "boolean"}],
          "value": [{"sort": "number", "set": {"forms": [{"form": "atLeast", "a": {"num": -1000000, "exp": 0}},
                                                            {"form": "atMost", "a": {"num": 1000000, "exp": 0}}]}}]
        }},
        {"sort": "object", "closed": true, "members": {
          "ok": [{"sort": "boolean"}],
          "error": [{"sort": "string", "set": ` + stringsWireSet + `}]
        }}
      ], "stdoutPure": true},
      "provenance": {"line": 3, "said": "parse_level answers {ok, value} or {ok, error}"}
    }
  }
}`
}

// TestForeignResultReturn_ATwoCaseObjectReturnBindsAndOkStyleAccessJudges
// pins Item 1 end to end at the grain this package can reach without a
// @types/node program host (this file's own header names that limit):
// a fixture-registered Result-shaped artifact — two object cases in
// one return cases list — reads through ReadForeignArtifact, lowers
// through foreignReturnValue into abstractdomain.KnownObject-shaped
// knowledge (a KindKindUnion of two KindObject arms, the union channel
// scalar multi-cases already use), and a `.ok`-style member judged
// through the EXISTING object-assignability law (CheckObjectTarget,
// via CheckAssignabilityOfArm's own union-arm dispatch) proves the
// member exists and fits on BOTH arms — never a re-derived reading
// specific to this artifact.
func TestForeignResultReturn_ATwoCaseObjectReturnBindsAndOkStyleAccessJudges(t *testing.T) {
	kernel := foreignReturnCornerKernel(t)
	targetPath, contentHash := writeForeignResultTarget(t)
	writeForeignArtifact(t, targetPath, foreignResultArtifactJSON(contentHash, targetPath))

	artifact, sentence := ReadForeignArtifact(targetPath)
	if sentence != "" {
		t.Fatalf("the Result-shaped fixture artifact declined: %s", sentence)
	}
	returnCases := artifact.Called.Return.Cases
	if len(returnCases) != 2 {
		t.Fatalf("len(returnCases) = %d, want 2 (the two object cases)", len(returnCases))
	}
	for i, c := range returnCases {
		if c.Sort != CaseSortObject {
			t.Fatalf("returnCases[%d].Sort = %q, want %q", i, c.Sort, CaseSortObject)
		}
		if !c.Closed {
			t.Errorf("returnCases[%d].Closed = false, want true — the artifact states \"closed\": true", i)
		}
	}

	bound := foreignReturnValue(artifact)
	if bound.Kind != abstractdomain.KindKindUnion {
		t.Fatalf("foreignReturnValue(Result artifact).Kind = %v, want KindKindUnion (two object arms joined)", bound.Kind)
	}
	if len(bound.Arms) != 2 {
		t.Fatalf("len(bound.Arms) = %d, want 2", len(bound.Arms))
	}
	for i, arm := range bound.Arms {
		if arm.Kind != abstractdomain.KindObject {
			t.Fatalf("bound.Arms[%d].Kind = %v, want KindObject", i, arm.Kind)
		}
		if !arm.Complete {
			t.Errorf("bound.Arms[%d].Complete = false, want true — Closed carried through from the artifact", i)
		}
		var hasOk bool
		for _, key := range arm.Keys {
			if key.Name == "ok" {
				hasOk = true
				if key.Value.Kind != abstractdomain.KindValues || key.Value.KindTag != abstractdomain.PrimitiveBoolean {
					t.Errorf("bound.Arms[%d]'s 'ok' key = %+v, want a KindValues{PrimitiveBoolean}", i, key.Value)
				}
			}
		}
		if !hasOk {
			t.Errorf("bound.Arms[%d] carries no 'ok' key: %+v", i, arm.Keys)
		}
	}

	// the ".ok"-style member access judges: a target object annotation
	// stating only {ok: boolean} (every arm the union may take carries
	// that key at that sort) must PROVE for this union, through the
	// EXISTING object-assignability laws — CheckAssignabilityOfArm's own
	// union dispatch (CheckKindUnion) walks each arm through
	// CheckObjectTarget, and a captured 7001/7002 on any arm is what
	// would fail this pin.
	p := entryEnvTestProgram(t, "const anchor = 1;\n")
	anchorStatement := p.Entry.Statements.Nodes[0]
	anchorNode := anchorStatement.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes[0].AsVariableDeclaration().Initializer

	okTarget := annotations.DeclaredRefinement{
		Kind: annotations.DeclaredObject,
		Object: &annotations.ObjectAnnotation{
			Keys: []annotations.ObjectKeySpec{
				{
					Name:  "ok",
					Count: setPtr(refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{1}))),
					At:    anchorNode,
					Value: annotations.ObjectKeyValue{
						Kind: annotations.KeyValueSet,
						Set:  setPtr(refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{0, 1}))),
					},
				},
			},
		},
	}

	var captured []assignability.RefinementDiagnostic
	ctx := &FlowContext{
		Kernel: kernel,
		Report: func(d assignability.RefinementDiagnostic) { captured = append(captured, d) },
	}
	CheckAssignability(ctx, bound, okTarget, anchorNode, "the returned value", nil)
	for _, d := range captured {
		t.Errorf("checking '.ok' against the Result union reported %d: %s — want no refutation, every arm carries 'ok: boolean'", d.Code, d.MessageText)
	}
}

// TestReadForeignArtifact_AnObjectCaseWithNoMembersDeclinesNamingIt pins
// casesOf's strict object arm: an object case stating no "members" at
// all is a malformed claim (not a shape this edge can guess a key set
// for), and the decline names it — the same "recognized and named,
// never silently guessed past" discipline every other malformed-case
// row in this file already gets.
func TestReadForeignArtifact_AnObjectCaseWithNoMembersDeclinesNamingIt(t *testing.T) {
	targetPath, contentHash := writeForeignResultTarget(t)
	text := strings.Replace(
		foreignResultArtifactJSON(contentHash, targetPath),
		`{"sort": "object", "closed": true, "members": {
          "ok": [{"sort": "boolean"}],
          "value": [{"sort": "number", "set": {"forms": [{"form": "atLeast", "a": {"num": -1000000, "exp": 0}},
                                                            {"form": "atMost", "a": {"num": 1000000, "exp": 0}}]}}]
        }}`,
		`{"sort": "object", "closed": true}`, 1)
	writeForeignArtifact(t, targetPath, text)

	artifact, sentence := ReadForeignArtifact(targetPath)
	if artifact != nil {
		t.Fatalf("an object case with no members answered a fact: %+v", artifact)
	}
	if !strings.Contains(sentence, "members") {
		t.Errorf("the sentence %q does not name the missing \"members\" object", sentence)
	}
}

/* ── construct: the no-stdin call against a stdin-reading target DETERMINES ── */
//
// d-data-legs.ts's computedInputKeySilentlySkippedUndetermined row (and any
// other call that reaches execFileSyncEdgeOf with no stdin `input` and no
// second argv element) recognizes as an edge with Payload/ArgvValue/FilePath
// all nil — mirroring checkArgvCrossing's ForeignSurfaceMixedStdinArgv case,
// checkOutboundLeg's own no-channel branch determines rather than declines
// when the target's surface is stdin-json: the target's harness reads its
// one value from stdin, this call sends none, so every concrete run throws
// at the target's own read before any value crosses — nothing here
// contradicts the target's stated entry.

func TestExecFileSyncEdgeOf_NoInputAndNoSecondArgvElementRecognizesWithNoPayload(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function execFileSync(file: string, args: string[], options: unknown): string;
function f() {
	const stdout = execFileSync("python3", ["./targets/level_ok.py"], { encoding: "utf8" });
	return stdout;
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	_, call, _ := constBoundCallOf(statements[0])
	edge, ok, sentence, _, _ := execFileSyncEdgeOf(nil, call, "stdout", statements, 0)
	if sentence != "" {
		t.Fatalf("a call with no input and no second argv element declined: %s", sentence)
	}
	if !ok || edge == nil {
		t.Fatalf("a call with no input and no second argv element was not recognized (ok=%v)", ok)
	}
	if edge.Payload != nil || edge.ArgvValue != nil || edge.FilePath != nil {
		t.Errorf("edge = %+v, want Payload/ArgvValue/FilePath all nil", edge)
	}
}

func TestCheckOutboundLeg_NoPayloadAgainstAStdinJSONTargetDeterminesRatherThanDeclines(t *testing.T) {
	fixture := foreignOutboundFixture(t, -2, 2, 1)
	edge := &ForeignEdge{Call: fixture.edge.Call, TargetPath: fixture.edge.TargetPath, StdoutName: "stdout"}
	outcome := checkOutboundLeg(fixture.ctx, fixture.env, edge, fixture.artifact)
	if outcome != nil {
		t.Fatalf("a no-payload call against a stdin-json target declined/fired: %+v", outcome)
	}
	if len(*fixture.reported) != 0 {
		t.Errorf("a no-payload call against a stdin-json target reported %d diagnostics: %+v", len(*fixture.reported), *fixture.reported)
	}
}

/* ── construct: a mixed-element array literal reads as a sequence crossing (KindList) ── */
//
// checkSequenceCrossing previously judged only KindValues{PrimitiveArray}
// (every element an exact literal number, sequenceCrossingOfExactTuple) —
// a literal with ANY non-exact element (a range, a parameter's declared
// window) evaluates through EvaluateArrayLiteral's non-flat path to
// KindList, which SetOfKnown explicitly refuses (lattice_operations.go).
// sequenceCrossingOfKindList reads each slot the same per-position way
// array_literal.go's own scalarPositionSet already does and rebuilds the
// Repetition window checkSequenceCrossing judges every other sequence
// shape through.

func TestSequenceCrossingOfKindList_EveryScalarSlotUnionsIntoARepetitionWindow(t *testing.T) {
	items := []abstractdomain.AbstractValue{
		abstractdomain.KnownSet(
			refinementsets.MakeRefinedSet(refinementsets.AtLeast(-1), refinementsets.AtMost(1)),
			nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone),
		abstractdomain.KnownValues([]float64{-0.3}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved),
		abstractdomain.KnownValues([]float64{0.2}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved),
	}
	crossing := abstractdomain.KnownList(items, abstractdomain.TrustProved)
	converted, ok := sequenceCrossingOfKindList(crossing)
	if !ok {
		t.Fatalf("sequenceCrossingOfKindList declined a list of scalar-shaped slots")
	}
	window, windowOk := refinementsets.AsRepetition(converted.Set)
	if !windowOk {
		t.Fatalf("converted.Set is not a Repetition: %+v", converted)
	}
	if window.Lo != 3 || window.Hi == nil || *window.Hi != 3 {
		t.Errorf("window = %+v, want an exact 3-element repetition", window)
	}
}

func TestSequenceCrossingOfKindList_AnObjectShapedSlotDeclines(t *testing.T) {
	items := []abstractdomain.AbstractValue{
		abstractdomain.KnownObject(nil, nil, true, abstractdomain.TrustProved, false),
		abstractdomain.KnownValues([]float64{0.2}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved),
	}
	crossing := abstractdomain.KnownList(items, abstractdomain.TrustProved)
	if _, ok := sequenceCrossingOfKindList(crossing); ok {
		t.Errorf("sequenceCrossingOfKindList converted a list with an object-shaped slot")
	}
}

func TestCheckOutboundLeg_AMixedElementArrayLiteralInsideTheStatedEntryPasses(t *testing.T) {
	fixture := foreignOutboundFixture(t, -2, 2, 1)
	fixture.env.Set("boosted", abstractdomain.KnownList([]abstractdomain.AbstractValue{
		abstractdomain.KnownSet(
			refinementsets.MakeRefinedSet(refinementsets.AtLeast(-1), refinementsets.AtMost(1)),
			nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone),
		abstractdomain.KnownValues([]float64{-0.3}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved),
		abstractdomain.KnownValues([]float64{0.2}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved),
	}, abstractdomain.TrustProved))
	outcome := checkOutboundLeg(fixture.ctx, fixture.env, fixture.edge, fixture.artifact)
	if outcome != nil {
		t.Fatalf("a mixed-element literal inside the stated entry did not pass: %+v", outcome)
	}
	if len(*fixture.reported) != 0 {
		t.Errorf("a fitting mixed-element crossing reported %d diagnostics: %+v", len(*fixture.reported), *fixture.reported)
	}
}

func TestCheckOutboundLeg_AnObjectShapedSlotInAMixedLiteralStaysUndetermined(t *testing.T) {
	fixture := foreignOutboundFixture(t, -2, 2, 1)
	fixture.env.Set("boosted", abstractdomain.KnownList([]abstractdomain.AbstractValue{
		abstractdomain.KnownObject(nil, nil, true, abstractdomain.TrustProved, false),
		abstractdomain.KnownValues([]float64{0.2}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved),
	}, abstractdomain.TrustProved))
	outcome := checkOutboundLeg(fixture.ctx, fixture.env, fixture.edge, fixture.artifact)
	if outcome == nil || outcome.Decline == "" {
		t.Fatalf("a list with an object-shaped slot did not decline: %+v", outcome)
	}
	if !strings.Contains(outcome.Decline, "not read as one here") {
		t.Errorf("Decline = %q, want the ordinary sequence-shape decline", outcome.Decline)
	}
}

/* ── construct: the runner word folds through a const binding ── */
//
// runnerAndScriptArgvOf's own interpreter test previously read only a
// written literal (stringLiteralText) — a `const runner = "python3"`
// binding at argv[0] answered false, so b-runners.ts's own
// runnerInVariableUndetermined row was never even recognized as a Python
// edge. runnerWordOf follows the SAME const-identifier and const-composed
// resolution scriptElementOf already performs for the script position.

func TestRunnerAndScriptArgvOf_AConstBoundRunnerWordResolves(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function execFileSync(file: string, args: string[], options: unknown): string;
function f() {
	const runner = "python3";
	const stdout = execFileSync(runner, ["./targets/level_ok.py"], { encoding: "utf8" });
	return stdout;
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	_, call, _ := constBoundCallOf(statements[1])
	args, _ := callArguments(call)
	ctx := &FlowContext{P: p}
	runnerWord, script, _, ok, sentence, _ := runnerAndScriptArgvOf(ctx, args[0], args[1])
	if sentence != "" {
		t.Fatalf("a const-bound runner word declined: %s", sentence)
	}
	if !ok || runnerWord != "python3" || script != "./targets/level_ok.py" {
		t.Errorf("runnerWord=%q script=%q ok=%v, want python3 / ./targets/level_ok.py / true", runnerWord, script, ok)
	}
}

func TestRunnerAndScriptArgvOf_ALetBoundRunnerWordStaysUnrecognized(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function execFileSync(file: string, args: string[], options: unknown): string;
function f() {
	let runner = "python3";
	const stdout = execFileSync(runner, ["./targets/level_ok.py"], { encoding: "utf8" });
	return stdout;
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	_, call, _ := constBoundCallOf(statements[1])
	args, _ := callArguments(call)
	ctx := &FlowContext{P: p}
	if _, _, _, ok, sentence, _ := runnerAndScriptArgvOf(ctx, args[0], args[1]); ok || sentence != "" {
		t.Errorf("a let-bound runner word was read as resolvable (ok=%v, sentence=%q) — a rewritable binding is not fixed by its declaration", ok, sentence)
	}
}

/* ── construct: the execSync shell heredoc recognizer ── */
//
// a-invocation-functions.ts's execSyncUndetermined row spells the
// stdin-json convention through a shell here-string:
// `python3 ./targets/level_ok.py <<< '${JSON.stringify(samples)}'`.
// execSyncHeredocCommandOf recognizes EXACTLY this shape — a template
// whose constant prefix parses as `<runner> <script> <<<` (with an
// optional matched quote) and whose single substitution is
// JSON.stringify(<payload>) — and lowers it to the same recognized edge
// an execFileSync call with an `input` key gets.

func TestExecSyncHeredocCommandOf_TheSingleQuotedHereStringRecognizes(t *testing.T) {
	p := entryEnvTestProgram(t, "function f() {\n"+
		"	const samples = [0.5, -0.3, 0.2];\n"+
		"	const stdout = `python3 ./targets/level_ok.py <<< '${JSON.stringify(samples)}'`;\n"+
		"	return stdout;\n"+
		"}\n")
	statements := relationalAccumulationBodyOf(t, p, "f")
	template := statements[1].AsVariableStatement().DeclarationList.
		AsVariableDeclarationList().Declarations.Nodes[0].AsVariableDeclaration().Initializer
	runnerWord, script, payload, ok := execSyncHeredocCommandOf(template)
	if !ok {
		t.Fatalf("the single-quoted here-string command was not recognized")
	}
	if runnerWord != "python3" || script != "./targets/level_ok.py" {
		t.Errorf("runnerWord=%q script=%q, want python3 / ./targets/level_ok.py", runnerWord, script)
	}
	if payload == nil || !ast.IsIdentifier(payload) || payload.Text() != "samples" {
		t.Errorf("payload = %v, want the identifier samples", payload)
	}
}

func TestExecSyncHeredocCommandOf_AnUnquotedHereStringRecognizes(t *testing.T) {
	p := entryEnvTestProgram(t, "function f() {\n"+
		"	const samples = [0.5, -0.3, 0.2];\n"+
		"	const stdout = `python3 ./targets/level_ok.py <<< ${JSON.stringify(samples)}`;\n"+
		"	return stdout;\n"+
		"}\n")
	statements := relationalAccumulationBodyOf(t, p, "f")
	template := statements[1].AsVariableStatement().DeclarationList.
		AsVariableDeclarationList().Declarations.Nodes[0].AsVariableDeclaration().Initializer
	runnerWord, script, payload, ok := execSyncHeredocCommandOf(template)
	if !ok {
		t.Fatalf("the unquoted here-string command was not recognized")
	}
	if runnerWord != "python3" || script != "./targets/level_ok.py" {
		t.Errorf("runnerWord=%q script=%q, want python3 / ./targets/level_ok.py", runnerWord, script)
	}
	if payload == nil || !ast.IsIdentifier(payload) || payload.Text() != "samples" {
		t.Errorf("payload = %v, want the identifier samples", payload)
	}
}

func TestExecSyncHeredocCommandOf_APipeInThePrefixIsRefused(t *testing.T) {
	p := entryEnvTestProgram(t, "function f() {\n"+
		"	const samples = [0.5, -0.3, 0.2];\n"+
		"	const stdout = `python3 ./targets/level_ok.py | tee out <<< '${JSON.stringify(samples)}'`;\n"+
		"	return stdout;\n"+
		"}\n")
	statements := relationalAccumulationBodyOf(t, p, "f")
	template := statements[1].AsVariableStatement().DeclarationList.
		AsVariableDeclarationList().Declarations.Nodes[0].AsVariableDeclaration().Initializer
	if _, _, _, ok := execSyncHeredocCommandOf(template); ok {
		t.Errorf("a prefix carrying a pipe was recognized as the narrow heredoc shape")
	}
}

func TestExecSyncHeredocCommandOf_TwoSubstitutionsAreRefused(t *testing.T) {
	p := entryEnvTestProgram(t, "function f() {\n"+
		"	const samples = [0.5, -0.3, 0.2];\n"+
		"	const extra = \"x\";\n"+
		"	const stdout = `python3 ./targets/level_ok.py ${extra} <<< '${JSON.stringify(samples)}'`;\n"+
		"	return stdout;\n"+
		"}\n")
	statements := relationalAccumulationBodyOf(t, p, "f")
	template := statements[2].AsVariableStatement().DeclarationList.
		AsVariableDeclarationList().Declarations.Nodes[0].AsVariableDeclaration().Initializer
	if _, _, _, ok := execSyncHeredocCommandOf(template); ok {
		t.Errorf("a template with two substitutions was recognized as the narrow heredoc shape")
	}
}

func TestExecSyncHeredocCommandOf_ANonStringifySubstitutionIsRefused(t *testing.T) {
	p := entryEnvTestProgram(t, "function f() {\n"+
		"	const samples = \"[0.5, -0.3, 0.2]\";\n"+
		"	const stdout = `python3 ./targets/level_ok.py <<< '${samples}'`;\n"+
		"	return stdout;\n"+
		"}\n")
	statements := relationalAccumulationBodyOf(t, p, "f")
	template := statements[1].AsVariableStatement().DeclarationList.
		AsVariableDeclarationList().Declarations.Nodes[0].AsVariableDeclaration().Initializer
	if _, _, _, ok := execSyncHeredocCommandOf(template); ok {
		t.Errorf("a substitution that is not JSON.stringify(...) was recognized as the narrow heredoc shape")
	}
}

func TestExecSyncEdgeOf_TheHeredocShapeRecognizesWithThePayload(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function execSync(command: string, options: unknown): string;
function f() {
	const samples = [0.5, -0.3, 0.2];
	const stdout = execSync(`+"`python3 ./targets/level_ok.py <<< '${JSON.stringify(samples)}'`"+`, { encoding: "utf8" });
	return stdout;
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	_, call, _ := constBoundCallOf(statements[1])
	edge, ok, sentence, _, _ := execSyncEdgeOf(call, "stdout")
	if sentence != "" {
		t.Fatalf("the heredoc shape declined: %s", sentence)
	}
	if !ok || edge == nil {
		t.Fatalf("the heredoc shape was not recognized (ok=%v)", ok)
	}
	if edge.Payload == nil || !ast.IsIdentifier(edge.Payload) || edge.Payload.Text() != "samples" {
		t.Errorf("edge.Payload = %v, want the identifier samples", edge.Payload)
	}
	if !strings.HasSuffix(edge.TargetPath, filepath.Join("targets", "level_ok.py")) {
		t.Errorf("edge.TargetPath = %q, want it to end in targets/level_ok.py", edge.TargetPath)
	}
}
