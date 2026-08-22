// The last-question record: an on-disk trace of the ONE question
// currently in flight to the native kernel, for a process that owns no
// in-process death signal to read AFTER the fact.
//
// ask_kernel.go's file comment states the constraint this answers: a
// Lean panic on the kernel's worker thread calls abort() (kernel_wrapper.c,
// lean_set_exit_on_panic) and takes the WHOLE HOST PROCESS down with it —
// there is no host code left running afterward to detect the death,
// name the question that caused it, or quarantine it. The detection and
// quarantine therefore cannot live inside this process at all; they
// live one level up, in whatever PARENT respawns this process
// (cmd/refined-lsp's coordinator, for the LSP path). That parent has no
// way to ask a dead process what it was doing — but it can read a file
// the dead process wrote just before the question that killed it.
//
// The record holds AT MOST ONE question: the one currently asked and
// not yet answered. WriteLastQuestion is called immediately before the
// question crosses to the dylib (the same seam traceKernelQuestion
// already fires from — ask_kernel.go's timed()); ClearLastQuestion is
// called immediately after a successful answer returns. A process that
// aborts mid-question therefore leaves the record naming exactly the
// question that killed it; a process that exits cleanly (or is between
// questions) leaves no record, or a stale one from a question that
// answered fine last time and was since cleared.
//
// Gated behind SetLastQuestionRecordPath the same way kernel_trace.go
// gates its writer: nil (unset) by default, so a batch CLI run — which
// has no parent watching for its death and nothing to gain from the
// record — pays no extra write per question. The LSP entry point
// (cmd/tsgo's runLSP) sets the path unconditionally: an editor session
// is exactly the case with a parent able to read the record, and the
// cost is one small atomic file write per novel kernel question (cache
// hits never reach this seam at all — see ask_kernel.go's timed()),
// which is negligible next to the FFI call itself.
package kernelbridge

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
)

// lastQuestionRecordPath holds the configured record path behind an
// atomic pointer — same shape as kernelTraceWriter (kernel_trace.go):
// every ask reads it, so the disabled path (nil, the default) must cost
// one atomic load and a nil compare, never a mutex lock.
var lastQuestionRecordPath atomic.Pointer[string]

// SetLastQuestionRecordPath states where WriteLastQuestion/
// ClearLastQuestion persist the in-flight question, or disables the
// record entirely when path is "". Mirrors SetDylibPath's plain-setter
// shape (kernel_bridge.go) — no environment variable, the standing rule.
func SetLastQuestionRecordPath(path string) {
	if path == "" {
		lastQuestionRecordPath.Store(nil)
		return
	}
	lastQuestionRecordPath.Store(&path)
}

// LastQuestionRecordPath reads the currently configured path, or ""
// when the record is disabled.
func LastQuestionRecordPath() string {
	p := lastQuestionRecordPath.Load()
	if p == nil {
		return ""
	}
	return *p
}

// lastQuestionRecord is the on-disk shape: the question's own cache key
// (op\x00rest — the same key ask1/ask2 build and AskCached looks up by,
// so a quarantine list built from this file's Op/Key pair matches
// QuarantinedQuestion's own comparison exactly) plus the op alone,
// duplicated for a human reading the file without decoding the key.
type lastQuestionRecord struct {
	Op  string `json:"op"`
	Key string `json:"key"`
}

// WriteLastQuestion records op/key as the question now in flight. A
// no-op when no path is configured (the disabled default). Errors are
// swallowed rather than surfaced to the caller: a failed WRITE of the
// death record must never itself fail — or slow — the question it is
// trying to describe; the record is best-effort diagnostic state, not
// part of the ask's own contract.
func WriteLastQuestion(op string, key string) {
	path := LastQuestionRecordPath()
	if path == "" {
		return
	}
	data, err := json.Marshal(lastQuestionRecord{Op: op, Key: key})
	if err != nil {
		return
	}
	_ = atomicWriteLastQuestionRecord(path, data)
}

// ClearLastQuestion removes the record after a question answers
// successfully — the record only ever names a question STILL in
// flight. A no-op when no path is configured, and errors are swallowed
// for the same reason WriteLastQuestion's are: clearing is best-effort
// bookkeeping, never a reason to fail the answer it is clearing after.
func ClearLastQuestion() {
	path := LastQuestionRecordPath()
	if path == "" {
		return
	}
	_ = os.Remove(path)
}

// ReadLastQuestion reads the record at path — the parent-process half
// of this seam, called after detecting a child's death. ok=false when
// the file is absent (a clean exit, or a death between questions) or
// unparsable (a torn read raced a write mid-rename, which the atomic
// write below is specifically built to prevent, but a caller reading
// while the child is still mid-write to a DIFFERENT temp name is not
// ruled out by that alone — treated the same as absent: no question
// named, nothing to quarantine).
func ReadLastQuestion(path string) (op string, key string, ok bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", false
	}
	var record lastQuestionRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return "", "", false
	}
	if record.Op == "" && record.Key == "" {
		return "", "", false
	}
	return record.Op, record.Key, true
}

// atomicWriteLastQuestionRecord writes data to path by writing a temp
// file in the SAME directory and renaming it into place — rename is
// atomic on the same volume, so a parent process reading the record
// mid-write never observes a torn file. Mirrors
// service/export_fact.go's atomicWriteArtifact exactly (the artifact
// writers' discipline this package is told to reuse rather than
// reinvent).
func atomicWriteLastQuestionRecord(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	temp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("creating a temp file in %s: %w", dir, err)
	}
	tempPath := temp.Name()
	if _, writeErr := temp.Write(data); writeErr != nil {
		temp.Close()
		os.Remove(tempPath)
		return fmt.Errorf("writing %s: %w", tempPath, writeErr)
	}
	if closeErr := temp.Close(); closeErr != nil {
		os.Remove(tempPath)
		return fmt.Errorf("closing %s: %w", tempPath, closeErr)
	}
	if renameErr := os.Rename(tempPath, path); renameErr != nil {
		os.Remove(tempPath)
		return fmt.Errorf("renaming %s into place at %s: %w", tempPath, path, renameErr)
	}
	return nil
}
