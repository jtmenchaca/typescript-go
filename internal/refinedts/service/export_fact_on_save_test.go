// Pure-function coverage for ExportFactOnSave's two cheap gates: the
// content-hash short-circuit (cachedArtifactContentHashMatches) and
// the atomic-write helper both writers share (atomicWriteArtifact,
// already exercised end to end by export_fact_test.go, pinned here at
// the unit level per docs/one-checker/fact-freshness.md's write
// discipline). Neither needs a program, a checker, or the kernel.

package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCachedArtifactContentHashMatches_NoCacheEntry_AnswersFalse(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "nothing.refined.json")
	if cachedArtifactContentHashMatches(missing, "sha256:abc") {
		t.Errorf("a missing cache entry must never match — the correct answer is \"export it\"")
	}
}

func TestCachedArtifactContentHashMatches_MatchingHash_AnswersTrue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audio_level.py.refined.json")
	body := map[string]any{"target": map[string]any{"file": "audio_level.py", "contentHash": "sha256:same"}}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshaling the fixture: %v", err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}
	if !cachedArtifactContentHashMatches(path, "sha256:same") {
		t.Errorf("a cache entry whose contentHash equals the freshly hashed bytes must match")
	}
}

func TestCachedArtifactContentHashMatches_DifferentHash_AnswersFalse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audio_level.py.refined.json")
	body := map[string]any{"target": map[string]any{"file": "audio_level.py", "contentHash": "sha256:old"}}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshaling the fixture: %v", err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}
	if cachedArtifactContentHashMatches(path, "sha256:new") {
		t.Errorf("a changed target must never match its stale cache entry")
	}
}

func TestCachedArtifactContentHashMatches_UnreadableJSON_AnswersFalse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "corrupt.refined.json")
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}
	if cachedArtifactContentHashMatches(path, "sha256:anything") {
		t.Errorf("unreadable JSON must never match — a torn or corrupt cache entry re-exports")
	}
}

func TestCachedArtifactContentHashMatches_NoTargetField_AnswersFalse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "no_target.refined.json")
	if err := os.WriteFile(path, []byte(`{"refined": {"kind": "typescript-fact-artifact"}}`), 0o644); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}
	if cachedArtifactContentHashMatches(path, "sha256:anything") {
		t.Errorf("an artifact with no target.contentHash must never match")
	}
}

// TestAtomicWriteArtifact_WritesNoTempFileLeftBehind pins the write
// discipline docs/one-checker/fact-freshness.md names: a temp file in
// the SAME directory, then rename — no stray `.tmp-*` file survives a
// successful write, and the final path holds exactly what was given.
func TestAtomicWriteArtifact_WritesNoTempFileLeftBehind(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "nested", "audio_level.py.refined.json")
	payload := []byte(`{"ok": true}`)

	if err := atomicWriteArtifact(target, payload); err != nil {
		t.Fatalf("atomicWriteArtifact: %v", err)
	}

	written, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("reading the written artifact: %v", err)
	}
	if string(written) != string(payload) {
		t.Errorf("written = %q, want %q", written, payload)
	}

	entries, err := os.ReadDir(filepath.Dir(target))
	if err != nil {
		t.Fatalf("reading the target directory: %v", err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".tmp-") {
			t.Errorf("a temp file survived the write: %s", entry.Name())
		}
	}
}

// TestAtomicWriteArtifact_OverwritesInPlace pins last-write-wins: a
// second write to the same path replaces the first rather than
// erroring or appending — the two writers (this producer and a
// concurrent one) both derive from the same disk bytes, so last-write-
// wins is semantically safe (fact-freshness.md's own reasoning).
func TestAtomicWriteArtifact_OverwritesInPlace(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "audio_level.py.refined.json")

	if err := atomicWriteArtifact(target, []byte("first")); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := atomicWriteArtifact(target, []byte("second")); err != nil {
		t.Fatalf("second write: %v", err)
	}
	written, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("reading the written artifact: %v", err)
	}
	if string(written) != "second" {
		t.Errorf("written = %q, want %q — the second write must win", written, "second")
	}
}
