// The forked LSP's kernel bootstrap: state where the native
// refinement kernel dylib lives BEFORE the server serves requests.
// kernelbridge.ResolveDylibPath alone is not production-ready for an
// editor: it answers only an explicit SetDylibPath or a cwd that
// already sits at an internal/refinedts package (the `go test` case)
// — never an editor whose cwd is a user workspace. So this mirrors
// cmd/refinedts-check/main.go's derivation exactly: the binary's own
// location first, then the cwd's ancestry under packages/refinedts.
// No environment variables (the standing rule).
//
// SetDylibPath is all that must happen at start — the kernel itself
// loads on the first check (service.setupKernel → loadedKernel), per
// the locked D3 decision (GO-LSP-EDITOR-PATH.md §15.4).

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// configureRefinedTSKernel derives and states the dylib path. Silent
// when nothing is found: every kernel question then declines, exactly
// as though the dylib were genuinely absent, and shape diagnostics
// still flow.
func configureRefinedTSKernel() {
	if derived, ok := refinedtsRepoRelative(
		"refined-lean/native/build/librefined_kernel.dylib"); ok {
		kernelbridge.SetDylibPath(derived)
	}
}

// configureKernelDeathQuarantine wires this LSP session into the
// host-level replace-and-quarantine design (ISSUES.md "The native
// kernel seam has no death signal"): a mid-session Lean panic
// abort()s THIS WHOLE PROCESS (kernelbridge/ask_kernel.go's file
// comment), so the only place that can ever detect the death and act
// on it is whatever PARENT respawns this process — cmd/refined-lsp's
// coordinator, for the LSP path.
//
// lastQuestionRecordFlag ("" unless the coordinator passes
// -last-question-record) states where kernelbridge persists the ONE
// question currently in flight, so a parent that detects this process
// died can read that file and learn what killed it. Defaulting to ""
// here rather than always deriving a path keeps a bare `tsgo --lsp`
// (no coordinator watching) from paying a write per question for
// nothing: the record is only useful to a parent that knows where to
// look, and only the coordinator knows that (it is the one choosing
// the path and passing it in) — see cmd/refined-lsp's own comment at
// the call site that builds this flag.
//
// quarantineFileFlag ("" unless the coordinator passes
// -quarantine-file) names a file of newline-separated question cache
// keys (op\x00rest, the exact spelling last_question_record.go writes
// and ask_kernel.go's ask1/ask2 compute) this run declines outright —
// the killing question from a PRIOR run's record, so a restarted
// process never re-asks the same question and crash-loops. A file
// rather than a flag value: a question's own wire is arbitrary encoded
// JSON and could in principle hold any byte a shell-quoted argv could
// mangle, where a file read with no shell interpretation cannot.
func configureKernelDeathQuarantine(lastQuestionRecordFlag string, quarantineFileFlag string) {
	if lastQuestionRecordFlag != "" {
		kernelbridge.SetLastQuestionRecordPath(lastQuestionRecordFlag)
	}
	if quarantineFileFlag == "" {
		return
	}
	raw, err := os.ReadFile(quarantineFileFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "refinedts: -quarantine-file %s: %v\n", quarantineFileFlag, err)
		return
	}
	var keys []string
	for _, line := range strings.Split(string(raw), "\n") {
		if trimmed := strings.TrimRight(line, "\r"); trimmed != "" {
			keys = append(keys, trimmed)
		}
	}
	if len(keys) == 0 {
		return
	}
	kernelbridge.SetQuarantinedQuestions(keys)
	for _, key := range keys {
		fmt.Fprintf(os.Stderr, "refinedts: quarantining a question from the prior session: %s\n", key)
	}
}

// refinedtsRepoRelative resolves a path relative to packages/, first
// against this binary's own location (the built tsgo binary sits
// inside packages/refinedts/refined-ts-go, so two levels up is
// packages/; a `go run ./cmd/tsgo` build sits one deeper), then
// against the working directory's ancestry. ok=false when neither
// holds the file. A copy of cmd/refinedts-check/main.go's
// repoRelative — the two binaries derive the same layout and neither
// can import the other's main package. refined-lean sits beside
// refinedts under packages/ (moved out from under
// refinedts/refined-ts-lean), so callers spell their target from
// packages/ down — "refined-lean/…" here.
func refinedtsRepoRelative(underPackages string) (string, bool) {
	var roots []string
	if exe, err := os.Executable(); err == nil {
		// <repo>/packages/refinedts/refined-ts-go/<binary>
		roots = append(roots, filepath.Join(filepath.Dir(exe), "..", ".."))
		// <repo>/packages/refinedts/refined-ts-go/cmd/tsgo/<binary>
		roots = append(roots, filepath.Join(filepath.Dir(exe), "..", "..", "..", ".."))
	}
	if cwd, err := os.Getwd(); err == nil {
		for dir := cwd; ; dir = filepath.Dir(dir) {
			roots = append(roots, filepath.Join(dir, "packages"))
			if dir == filepath.Dir(dir) {
				break
			}
		}
	}
	for _, root := range roots {
		candidate := filepath.Join(root, underPackages)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, true
		}
	}
	return "", false
}
