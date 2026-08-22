package kernelbridge

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWriteLastQuestionThenClearRemovesTheFile pins the write/clear
// discipline WriteLastQuestion/ClearLastQuestion promise: a question in
// flight leaves a record naming it; once it answers, the record is
// gone — nothing left over to confuse a LATER death for THIS one.
func TestWriteLastQuestionThenClearRemovesTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "last-question.json")
	SetLastQuestionRecordPath(path)
	t.Cleanup(func() { SetLastQuestionRecordPath("") })

	WriteLastQuestion("member", "member\x00abc")

	op, key, ok := ReadLastQuestion(path)
	if !ok {
		t.Fatalf("ReadLastQuestion after WriteLastQuestion: ok = false, want true")
	}
	if op != "member" || key != "member\x00abc" {
		t.Fatalf("ReadLastQuestion = (%q, %q), want (member, member\\x00abc)", op, key)
	}

	ClearLastQuestion()

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("record file still exists after ClearLastQuestion: err = %v", err)
	}
	if _, _, ok := ReadLastQuestion(path); ok {
		t.Fatalf("ReadLastQuestion after ClearLastQuestion: ok = true, want false")
	}
}

// TestWriteLastQuestionOverwritesAPriorRecord pins that a second
// question in flight replaces the first's record rather than appending
// to it — the record holds AT MOST ONE question, the one currently
// asked.
func TestWriteLastQuestionOverwritesAPriorRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "last-question.json")
	SetLastQuestionRecordPath(path)
	t.Cleanup(func() { SetLastQuestionRecordPath("") })

	WriteLastQuestion("member", "member\x00first")
	WriteLastQuestion("scalarSubset", "scalarSubset\x00second")

	op, key, ok := ReadLastQuestion(path)
	if !ok {
		t.Fatalf("ReadLastQuestion: ok = false, want true")
	}
	if op != "scalarSubset" || key != "scalarSubset\x00second" {
		t.Fatalf("ReadLastQuestion = (%q, %q), want the SECOND write, not the first", op, key)
	}
}

// TestNoRecordPathConfiguredIsANoOp pins the disabled-by-default
// discipline: with no path set, WriteLastQuestion/ClearLastQuestion do
// nothing (never panic, never write anywhere) — the batch-CLI default
// this package's file comment states.
func TestNoRecordPathConfiguredIsANoOp(t *testing.T) {
	SetLastQuestionRecordPath("")
	if got := LastQuestionRecordPath(); got != "" {
		t.Fatalf("LastQuestionRecordPath = %q, want empty", got)
	}
	// must not panic with no path configured
	WriteLastQuestion("member", "member\x00abc")
	ClearLastQuestion()
}

// TestReadLastQuestionAbsentFileReportsNotOK pins the "clean exit, or
// between questions" reading: a coordinator that restarts a child which
// exited cleanly must see ok=false, never a stale or fabricated
// question name.
func TestReadLastQuestionAbsentFileReportsNotOK(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.json")
	if _, _, ok := ReadLastQuestion(path); ok {
		t.Fatalf("ReadLastQuestion on an absent file: ok = true, want false")
	}
}

// TestReadLastQuestionMalformedFileReportsNotOK pins the torn/garbage
// read as absent-equivalent, never a crash — the parent process reading
// mid-write (or a corrupted leftover) must degrade to "nothing to
// quarantine," not fail its own restart.
func TestReadLastQuestionMalformedFileReportsNotOK(t *testing.T) {
	path := filepath.Join(t.TempDir(), "garbage.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("os.WriteFile: %v", err)
	}
	if _, _, ok := ReadLastQuestion(path); ok {
		t.Fatalf("ReadLastQuestion on malformed JSON: ok = true, want false")
	}
}

// TestWriteLastQuestionUsesTempThenRename pins the atomic-write
// discipline (mirroring service/export_fact.go's atomicWriteArtifact):
// no stray ".tmp-*" file survives a successful write — only the final
// path remains — so a concurrent reader in the parent process never
// finds a torn or leftover temp file.
func TestWriteLastQuestionUsesTempThenRename(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "last-question.json")
	SetLastQuestionRecordPath(path)
	t.Cleanup(func() { SetLastQuestionRecordPath("") })

	WriteLastQuestion("member", "member\x00abc")

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("os.ReadDir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("directory has %d entries after WriteLastQuestion, want 1 (the final file only): %v", len(entries), entries)
	}
	if entries[0].Name() != "last-question.json" {
		t.Fatalf("leftover entry %q, want only last-question.json (no stray temp file)", entries[0].Name())
	}
}
