// The D1 proof: a forked `tsgo --lsp` serves refinement judgments with
// source "refinedts" on a live overlay, through the standard pull
// (`textDocument/diagnostic`). In-process harness per the locked
// decision (GO-LSP-EDITOR-PATH.md §15.12): lsptestutil.NewLSPClient,
// empty client capabilities (skips workspace/configuration), the
// client answering client/registerCapability.
//
// The workspace mirrors the tsc-vscode fixtures: refuted.analysis.ts
// (fee(150) against z.number().min(0).max(100) → RTS7001) and
// alert.analysis.ts (2 ** n at a checked position → RTS7002), with
// the real surface module vendored into the virtual workspace at a
// path discovery recognizes (…/surface/z.ts). The surface sources are
// read from the actual refined-ts-typescript tree so this test never
// drifts from the shipped surface.
//
// The native kernel dylib is required for both codes — skipped (never
// faked) when it is not built.

package lsp_test

import (
	"context"
	"io"
	"os"
	"testing"

	"github.com/microsoft/typescript-go/internal/bundled"
	"github.com/microsoft/typescript-go/internal/lsp"
	"github.com/microsoft/typescript-go/internal/lsp/lsproto"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/testutil/lsptestutil"
	"github.com/microsoft/typescript-go/internal/vfs/vfstest"
	"gotest.tools/v3/assert"
)

// relative to this package's directory (the `go test` cwd)
const (
	refinedtsSurfaceDir = "../../../refined-ts-typescript"
	refinedtsKernel     = "../../../../refined-lean/native/build/librefined_kernel.dylib"
)

// refinedtsSurfaceFiles reads the shipped surface module and its
// imports from disk, placed at the same relative layout the module's
// own import specifiers assume (surface/ beside refinement_sets/).
func refinedtsSurfaceFiles(t *testing.T, files map[string]string) {
	t.Helper()
	vendor := map[string]string{
		"/home/project/vendor/refinedts/surface/z.ts":                          "surface/z.ts",
		"/home/project/vendor/refinedts/surface/object_runtime.ts":             "surface/object_runtime.ts",
		"/home/project/vendor/refinedts/refinement_sets/refinement_forms.ts":   "refinement_sets/refinement_forms.ts",
		"/home/project/vendor/refinedts/refinement_sets/repetition_windows.ts": "refinement_sets/repetition_windows.ts",
		"/home/project/vendor/refinedts/refinement_sets/codepoint_sets.ts":     "refinement_sets/codepoint_sets.ts",
		"/home/project/vendor/refinedts/refinement_sets/regex_compiler.ts":     "refinement_sets/regex_compiler.ts",
	}
	for virtual, rel := range vendor {
		text, err := os.ReadFile(refinedtsSurfaceDir + "/" + rel)
		if err != nil {
			t.Skipf("surface source not readable (%v) — refined-ts-typescript tree not present", err)
		}
		files[virtual] = string(text)
	}
}

func initRefinedTSClient(t *testing.T, files map[string]string) *lsptestutil.LSPClient {
	t.Helper()

	fs := bundled.WrapFS(vfstest.FromMap(files, true /*useCaseSensitiveFileNames*/))

	onServerRequest := func(_ context.Context, req *lsproto.RequestMessage) *lsproto.ResponseMessage {
		switch req.Method {
		case lsproto.MethodClientRegisterCapability, lsproto.MethodClientUnregisterCapability, lsproto.MethodWindowWorkDoneProgressCreate:
			return &lsproto.ResponseMessage{
				ID:      req.ID,
				JSONRPC: req.JSONRPC,
				Result:  lsproto.Null{},
			}
		default:
			return nil
		}
	}

	client, closeClient := lsptestutil.NewLSPClient(t, lsp.ServerOptions{
		Err:                io.Discard,
		Cwd:                "/home/project",
		FS:                 fs,
		DefaultLibraryPath: bundled.LibPath(),
	}, onServerRequest)
	t.Cleanup(func() { _ = closeClient() })

	initMsg, _, ok := lsptestutil.SendRequest(t, client, lsproto.InitializeInfo, &lsproto.InitializeParams{
		Capabilities: &lsproto.ClientCapabilities{},
	})
	assert.Assert(t, ok && initMsg.AsResponse().Error == nil, "initialize failed")
	lsptestutil.SendNotification(t, client, lsproto.InitializedInfo, &lsproto.InitializedParams{})
	<-client.Server.InitComplete()

	return client
}

// pullRefinements opens the file as a live overlay and pulls its
// diagnostics, answering only the source=="refinedts" rows.
func pullRefinements(t *testing.T, client *lsptestutil.LSPClient, uri lsproto.DocumentUri, text string) []*lsproto.Diagnostic {
	t.Helper()
	lsptestutil.SendNotification(t, client, lsproto.TextDocumentDidOpenInfo, &lsproto.DidOpenTextDocumentParams{
		TextDocument: &lsproto.TextDocumentItem{Uri: uri, LanguageId: "typescript", Version: 1, Text: text},
	})
	msg, report, ok := lsptestutil.SendRequest(t, client, lsproto.TextDocumentDiagnosticInfo, &lsproto.DocumentDiagnosticParams{
		TextDocument: lsproto.TextDocumentIdentifier{Uri: uri},
	})
	assert.Assert(t, ok && msg.AsResponse().Error == nil, "diagnostic pull failed")
	assert.Assert(t, report.FullDocumentDiagnosticReport != nil, "expected a full report")
	var refinements []*lsproto.Diagnostic
	for _, item := range report.FullDocumentDiagnosticReport.Items {
		if item.Source != nil && *item.Source == "refinedts" {
			refinements = append(refinements, item)
		}
	}
	return refinements
}

func refinementCodes(diags []*lsproto.Diagnostic) []int32 {
	var codes []int32
	for _, d := range diags {
		if d.Code != nil && d.Code.Integer != nil {
			codes = append(codes, *d.Code.Integer)
		}
	}
	return codes
}

const refinedtsTsconfig = `{
	"compilerOptions": {
		"strict": true,
		"noEmit": true,
		"target": "es2022",
		"module": "esnext",
		"moduleResolution": "bundler",
		"allowImportingTsExtensions": true
	}
}`

// the tsc-vscode/fixtures/refuted.analysis.ts shape, with the surface
// import pointed at the vendored copy
const refutedFixture = `import * as z from "./vendor/refinedts/surface/z.ts";

const zPct = z.number().min(0).max(100);

type Pct = z.infer<typeof zPct>;

function fee(p: Pct): number {
  return 0;
}

const result = "test"

fee(150);
`

// the tsc-vscode/fixtures/alert.analysis.ts shape
const alertFixture = `import * as z from "./vendor/refinedts/surface/z.ts";

const Pct = z.number().min(0).max(100);

function fee(p: z.infer<typeof Pct>): number {
  return 0;
}

function f(n: number): number {
  return fee(2 ** n);
}
`

func TestRefinedTSDiagnosticPull(t *testing.T) {
	t.Parallel()
	if !bundled.Embedded {
		t.Skip("bundled files are not embedded")
	}
	if !kernelbridge.KernelArtifactsPresent(refinedtsKernel) {
		t.Skip("native kernel dylib not built")
	}
	kernelbridge.SetDylibPath(refinedtsKernel)

	files := map[string]string{
		"/home/project/tsconfig.json":       refinedtsTsconfig,
		"/home/project/refuted.analysis.ts": refutedFixture,
		"/home/project/alert.analysis.ts":   alertFixture,
	}
	refinedtsSurfaceFiles(t, files)

	client := initRefinedTSClient(t, files)

	t.Run("refuted fires 7001", func(t *testing.T) {
		refinements := pullRefinements(t, client,
			"file:///home/project/refuted.analysis.ts", refutedFixture)
		assert.Assert(t, len(refinements) > 0, "expected refinedts diagnostics on the refuted fixture")
		codes := refinementCodes(refinements)
		assert.Assert(t, containsCode(codes, 7001), "expected RTS7001, got %v", codes)
	})

	t.Run("alert fires 7002", func(t *testing.T) {
		refinements := pullRefinements(t, client,
			"file:///home/project/alert.analysis.ts", alertFixture)
		assert.Assert(t, len(refinements) > 0, "expected refinedts diagnostics on the alert fixture")
		codes := refinementCodes(refinements)
		assert.Assert(t, containsCode(codes, 7002), "expected RTS7002, got %v", codes)
	})
}

// TestRefinedTSDiagnosticExpectErrorView: the editor view rides the
// pull — a marker covering the fire suppresses it, and a stale marker
// is its own 7005. Overlay contents (never on disk) are what is
// judged: the file opens with text that differs from nothing on disk.
func TestRefinedTSDiagnosticExpectErrorView(t *testing.T) {
	t.Parallel()
	if !bundled.Embedded {
		t.Skip("bundled files are not embedded")
	}
	if !kernelbridge.KernelArtifactsPresent(refinedtsKernel) {
		t.Skip("native kernel dylib not built")
	}
	kernelbridge.SetDylibPath(refinedtsKernel)

	covered := `import * as z from "./vendor/refinedts/surface/z.ts";

const zPct = z.number().min(0).max(100);

type Pct = z.infer<typeof zPct>;

function fee(p: Pct): number {
  return 0;
}

// @refinedts-expect-error 7001
fee(150);

// @refinedts-expect-error
const clean: number = 1;
`
	files := map[string]string{
		"/home/project/tsconfig.json":       refinedtsTsconfig,
		"/home/project/covered.analysis.ts": covered,
	}
	refinedtsSurfaceFiles(t, files)

	client := initRefinedTSClient(t, files)
	refinements := pullRefinements(t, client,
		"file:///home/project/covered.analysis.ts", covered)
	codes := refinementCodes(refinements)
	assert.Assert(t, !containsCode(codes, 7001), "the covered 7001 should be suppressed, got %v", codes)
	assert.Assert(t, containsCode(codes, 7005), "the stale marker should surface as 7005, got %v", codes)
}

func containsCode(codes []int32, code int32) bool {
	for _, held := range codes {
		if held == code {
			return true
		}
	}
	return false
}
