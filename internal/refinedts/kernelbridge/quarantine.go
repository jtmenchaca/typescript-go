// The quarantine list: questions declined BY NAME on a restarted
// process, because the last run's own last-question record
// (last_question_record.go) names them as the question that killed the
// kernel. This is the host-level replace-and-quarantine half of the
// ruled kernel-death design (ISSUES.md "The native kernel seam has no
// death signal"): a parent process (cmd/refined-lsp's coordinator)
// reads the dead child's record, restarts the child, and passes the
// killing question's identity back in so THIS run never re-asks it —
// closing the crash-loop a naive restart would otherwise fall into
// (restart, re-open the same file, ask the same question, die again).
//
// The quarantine is keyed on the SAME string ask1/ask2 (ask_kernel.go)
// already build as the question cache key (op + "\x00" + the wire or
// caller-supplied key) — the exact string WriteLastQuestion records
// alongside the op, so a key read back from the record file matches a
// later ask's own key without any re-derivation or re-encoding.
package kernelbridge

import "sync"

// quarantineMu guards quarantinedKeys.
var quarantineMu sync.Mutex

// quarantinedKeys holds the question cache keys declined outright this
// run — set once at process start (SetQuarantinedQuestions), read on
// every ask1/ask2 call before the cache lookup or the FFI call.
var quarantinedKeys map[string]bool

// SetQuarantinedQuestions states which question cache keys this run
// declines outright, replacing whatever was set before. An empty or nil
// keys disables the quarantine (the default: nothing declined). Called
// once at startup — cmd/tsgo's -quarantine-file flag, threaded from
// cmd/refined-lsp's restart of a child whose last-question record named
// a killing question.
func SetQuarantinedQuestions(keys []string) {
	quarantineMu.Lock()
	defer quarantineMu.Unlock()
	if len(keys) == 0 {
		quarantinedKeys = nil
		return
	}
	set := make(map[string]bool, len(keys))
	for _, k := range keys {
		set[k] = true
	}
	quarantinedKeys = set
}

// isQuarantined reports whether key names a question this run declines
// outright.
func isQuarantined(key string) bool {
	quarantineMu.Lock()
	defer quarantineMu.Unlock()
	return quarantinedKeys[key]
}

// quarantineDeclineMessage is the decline sentence for a quarantined
// question — the same "kernel: declined — <reason>" shape ask_kernel.go
// and wire_nesting_guard.go already use, naming what happened rather
// than just refusing silently: this exact question ended the process
// that last asked it.
const quarantineDeclineMessage = "kernel: declined — the kernel died answering this question last run"
