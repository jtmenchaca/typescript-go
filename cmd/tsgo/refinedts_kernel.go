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
	"os"
	"path/filepath"

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
