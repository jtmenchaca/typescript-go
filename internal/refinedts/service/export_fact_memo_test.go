// The consumer-side reads that share ExportFact's own artifact shape:
// memo freshness after a rewrite, a hand-authored cases-shape fixture
// read against the shared spec, and the version-field / wrong-triple
// declines. Split out of export_fact_test.go, which still holds the
// shared harness fixture writers (writeHarnessFixture, meterLevelBody,
// and friends) and exportFactSurfacePath every export_fact_*_test.go
// file in this package reuses.

package service

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/walk"
)

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
		text := `{"refined": {"kind": "fact-artifact"},
"target": {"file": "` + targetPath + `", "contentHash": "` + hash + `"},
"language": "python",
"runtime": {"band": "cpython-3.11+"},
"surface": {"kind": "stdin-json", "stdin": "json", "stdout": "json", "calls": "audio_level"},
"functions": {"audio_level": {
  "entry": [{"name": "samples", "sequence": {
    "element": {"cases": [{"sort": "number", "set": {"forms": [{"form": "atLeast", "a": {"num": -1, "exp": 0}},
                          {"form": "atMost", "a": {"num": 1, "exp": 0}}]}}]},
    "lengthAtLeast": 1}}],
  "return": {"cases": [{"sort": "number", "set": {"forms": [{"form": "atLeast", "a": {"num": 0, "exp": 0}},
                               {"form": "atMost", "a": {"num": 1, "exp": 0}}]}}],
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

// audioLevelCasesArtifact is one hand-authored fact-artifact row —
// entry, return, provenance — spelled under the RULED cases schema's
// "fact-artifact"/language/surface shape (no version field, ever).
// Hand-authored against the schema doc, not generated, so
// TestReadForeignArtifact_CasesFixtureReads proves the reader agrees
// with the shared spec, not merely with its own producer.
func audioLevelCasesArtifact(targetPath string, hash string) string {
	return `{"refined": {"kind": "fact-artifact"},
"target": {"file": "` + targetPath + `", "contentHash": "` + hash + `"},
"language": "python",
"runtime": {"band": "cpython-3.11+"},
"surface": {"kind": "stdin-json", "stdin": "json", "stdout": "json", "calls": "audio_level"},
"functions": {"audio_level": {
  "entry": [{"name": "samples", "sequence": {
    "element": {"cases": [{"sort": "number", "set": {"forms": [{"form": "atLeast", "a": {"num": -1, "exp": 0}},
                          {"form": "atMost", "a": {"num": 1, "exp": 0}}]}}]},
    "lengthAtLeast": 1}}],
  "return": {"cases": [{"sort": "number", "set": {"forms": [{"form": "atLeast", "a": {"num": 0, "exp": 0}},
                               {"form": "atMost", "a": {"num": 1, "exp": 0}}]}}],
             "stdoutPure": true},
  "provenance": {"line": 4, "said": "the cases shape"}
}}}`
}

// writeTargetAndArtifact writes targetPath's source and text to
// artifactPath, answering the target's own content hash — the shared
// setup every dispatch test below needs (a real target file so
// checkTargetIntegrity's hash comparison is a genuine check, not a
// vacuous one).
func writeTargetAndArtifact(t *testing.T, dir string, source string, text func(targetPath string, hash string) string) (targetPath string, artifactPath string) {
	t.Helper()
	targetPath = filepath.Join(dir, "audio_level.py")
	if err := os.WriteFile(targetPath, []byte(source), 0o644); err != nil {
		t.Fatalf("writing the target: %v", err)
	}
	sum := sha256.Sum256([]byte(source))
	hash := "sha256:" + hex.EncodeToString(sum[:])
	artifactPath = walk.ForeignCacheArtifactPath(targetPath)
	if err := os.MkdirAll(filepath.Dir(artifactPath), 0o755); err != nil {
		t.Fatalf("creating the cache directory: %v", err)
	}
	if err := os.WriteFile(artifactPath, []byte(text(targetPath, hash)), 0o644); err != nil {
		t.Fatalf("writing the artifact: %v", err)
	}
	return targetPath, artifactPath
}

// TestReadForeignArtifact_CasesFixtureReads pins that a hand-authored
// cases-shape artifact — written directly from the schema doc, never
// copied from this reader's own code — reads cleanly: the reader
// agrees with the shared spec, not merely with its own producer.
func TestReadForeignArtifact_CasesFixtureReads(t *testing.T) {
	source := "def audio_level(samples):\n    return 0.5\n"
	targetPath, _ := writeTargetAndArtifact(t, t.TempDir(), source, audioLevelCasesArtifact)

	artifact, sentence := walk.ReadForeignArtifact(targetPath)
	if sentence != "" {
		t.Fatalf("the cases fixture declined: %s", sentence)
	}
	if artifact.RuntimeBand != walk.ForeignRuntimeBand {
		t.Errorf("RuntimeBand = %q, want %q", artifact.RuntimeBand, walk.ForeignRuntimeBand)
	}
	if artifact.Called.Name != "audio_level" {
		t.Errorf("Called.Name = %q, want audio_level", artifact.Called.Name)
	}
	if !artifact.Called.Return.StdoutPure {
		t.Errorf("Called.Return.StdoutPure = false, want true")
	}
	if len(artifact.Called.Entry) != 1 {
		t.Fatalf("len(Called.Entry) = %d, want 1", len(artifact.Called.Entry))
	}
	if !artifact.Called.Entry[0].IsSequence || artifact.Called.Entry[0].LengthAtLeast != 1 {
		t.Errorf("Called.Entry[0] = %+v, want a sequence with lengthAtLeast 1", artifact.Called.Entry[0])
	}
	if len(artifact.Called.Entry[0].ElementCases) != 1 || artifact.Called.Entry[0].ElementCases[0].Sort != walk.CaseSortNumber {
		t.Errorf("Called.Entry[0].ElementCases = %+v, want one number case", artifact.Called.Entry[0].ElementCases)
	}
	if len(artifact.Called.Return.Cases) != 1 || artifact.Called.Return.Cases[0].Sort != walk.CaseSortNumber {
		t.Errorf("Called.Return.Cases = %+v, want one number case", artifact.Called.Return.Cases)
	}
	if artifact.Called.Provenance.Said != "the cases shape" {
		t.Errorf("Called.Provenance.Said = %q, want %q", artifact.Called.Provenance.Said, "the cases shape")
	}
}

// TestReadForeignArtifact_AVersionFieldDeclinesAsSuperseded: an
// envelope carrying a "version" field at all — the pre-ruling shape —
// is NO-FACT under the RULED schema, declined by name before the
// kind/language pair is even asked.
func TestReadForeignArtifact_AVersionFieldDeclinesAsSuperseded(t *testing.T) {
	source := "def audio_level(samples):\n    return 0.5\n"
	targetPath, _ := writeTargetAndArtifact(t, t.TempDir(), source,
		func(targetPath string, hash string) string {
			return `{"refined": {"kind": "fact-artifact", "version": 2},
"target": {"file": "` + targetPath + `", "contentHash": "` + hash + `"},
"language": "python",
"runtime": {"band": "cpython-3.11+"},
"surface": {"kind": "stdin-json", "stdin": "json", "stdout": "json", "calls": "audio_level"},
"functions": {"audio_level": {
  "entry": [{"name": "samples", "cases": [{"sort": "number", "set": {"forms": [{"form": "atLeast", "a": {"num": 0, "exp": 0}}]}}]}],
  "return": {"cases": [{"sort": "number", "set": {"forms": [{"form": "atLeast", "a": {"num": 0, "exp": 0}}]}}], "stdoutPure": true},
  "provenance": {"line": 1, "said": "a superseded shape"}
}}}`
		})

	_, sentence := walk.ReadForeignArtifact(targetPath)
	if sentence == "" {
		t.Fatalf("an envelope carrying a version field must decline, and it did not")
	}
	if !strings.Contains(sentence, "superseded shape") {
		t.Errorf("decline sentence = %q, want it to name the superseded shape", sentence)
	}
}

// TestReadForeignArtifact_WrongTripleDeclinesByName: a (kind,
// language) pair that is not the one accepted form — here,
// "fact-artifact" with NO language field (the RULED schema requires
// "python" or "typescript") — must decline with a sentence naming the
// stated pair and the one accepted form, never fall back to reading
// it anyway.
func TestReadForeignArtifact_WrongTripleDeclinesByName(t *testing.T) {
	source := "def audio_level(samples):\n    return 0.5\n"
	targetPath, _ := writeTargetAndArtifact(t, t.TempDir(), source,
		func(targetPath string, hash string) string {
			return `{"refined": {"kind": "fact-artifact"},
"target": {"file": "` + targetPath + `", "contentHash": "` + hash + `"},
"runtime": {"band": "cpython-3.11+"},
"surface": {"kind": "stdin-json", "stdin": "json", "stdout": "json", "calls": "audio_level"},
"functions": {"audio_level": {
  "entry": [{"name": "samples", "cases": [{"sort": "number", "set": {"forms": [{"form": "atLeast", "a": {"num": 0, "exp": 0}}]}}]}],
  "return": {"cases": [{"sort": "number", "set": {"forms": [{"form": "atLeast", "a": {"num": 0, "exp": 0}}]}}], "stdoutPure": true},
  "provenance": {"line": 1, "said": "no language"}
}}}`
		})

	_, sentence := walk.ReadForeignArtifact(targetPath)
	if sentence == "" {
		t.Fatalf("a pair with no language field must decline, and it did not")
	}
	for _, want := range []string{
		`kind "fact-artifact"`, `language ""`,
		`"fact-artifact", "python"`,
	} {
		if !strings.Contains(sentence, want) {
			t.Errorf("decline sentence = %q, want it to contain %q", sentence, want)
		}
	}
}

func hasPrefix(s string, prefix string) bool {
	return strings.HasPrefix(s, prefix)
}
