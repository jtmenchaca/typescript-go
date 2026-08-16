// from service/program_host.ts
//
// The program.CheckerProgram construction API: tsc's own diagnostics
// for the entry file come from the same Program the refinement walk
// questions — never a reimplementation.
//
// String mode builds an in-memory program: the entry source at
// /main.ts, the surface module (and the modules it imports) read from
// disk at runtime and served at fixed virtual paths, everything else
// (lib files) from the bundled default library. Recognition
// downstream is symbol resolution: a name is "the surface" exactly
// when its declaration lives in one of SurfacePaths.
//
// program.CheckerProgram is the ALREADY-LANDED type this file builds
// (internal/refinedts/program/checker_program.go) — its Checker field
// replaces the TS CheckerHost entirely, per PORT.md's adapter rule.
// The TsgoOracle/hostFor/parseOnlyBitsOf machinery program_host.ts
// itself calls through has no Go twin (program_disk_host.go's header
// explains why). ProgramFromExisting, by contrast, DOES port: the TS
// programFromExisting is the language-service wrapper — reuse a live
// Program someone else holds so unsaved buffers are judged and no
// Program is rebuilt per call — and hostFor's oracle branch collapses
// into the in-process checker lease the same way every other seam's
// did (GO-LSP-EDITOR-PATH.md §5.1, §8).

package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/bundled"
	"github.com/microsoft/typescript-go/internal/compiler"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/tsoptions"
	"github.com/microsoft/typescript-go/internal/tspath"
	"github.com/microsoft/typescript-go/internal/vfs/osvfs"
	"github.com/microsoft/typescript-go/internal/vfs/vfstest"
)

const entryPath = "/main.ts"

// SurfacePath is SURFACE_PATH in the TS source's program_surface.ts.
const SurfacePath = "/surface/z.ts"

// surfaceDeps is SURFACE_DEPS in the TS source: the surface module and
// its imports, at their fixed virtual paths, read from the real
// refined-ts-typescript/surface tree at runtime (a directory
// parameter, not embedded, per the task's instruction) rather than
// import.meta.url.
var surfaceDeps = map[string]string{
	SurfacePath:                              "z.ts",
	"/surface/object_runtime.ts":             "object_runtime.ts",
	"/refinement_sets/refinement_forms.ts":   "../refinement_sets/refinement_forms.ts",
	"/refinement_sets/repetition_windows.ts": "../refinement_sets/repetition_windows.ts",
	"/refinement_sets/codepoint_sets.ts":     "../refinement_sets/codepoint_sets.ts",
	"/refinement_sets/regex_compiler.ts":     "../refinement_sets/regex_compiler.ts",
}

// ProgramFromSource builds the in-memory program for one entry
// source, given the directory holding refined-ts-typescript/surface
// (surfaceDir) — the caller states it explicitly (a runtime directory
// parameter, per the task's instruction), since Go has no
// import.meta.url to resolve it from automatically.
func ProgramFromSource(source string, surfaceDir string) (*program.CheckerProgram, error) {
	virtual := map[string]string{entryPath: source}
	for path, rel := range surfaceDeps {
		text, err := os.ReadFile(filepath.Join(surfaceDir, rel))
		if err != nil {
			return nil, err
		}
		virtual[path] = string(text)
	}

	fs := bundled.WrapFS(vfstest.FromMap(virtual, true /*useCaseSensitiveFileNames*/))
	host := compiler.NewCompilerHost("/", fs, bundled.LibPath(), nil, nil, nil)
	options := Options()
	config := tsoptions.NewParsedCommandLine(options, []string{entryPath}, tspath.ComparePathsOptions{
		UseCaseSensitiveFileNames: true,
		CurrentDirectory:          "/",
	})
	p := compiler.NewProgram(compiler.ProgramOptions{Config: config, Host: host})
	p.BindSourceFiles()
	entry := p.GetSourceFile(entryPath)
	if entry == nil {
		return nil, errEntryDidNotParse("the entry file did not parse")
	}
	// the checker's LEASE must stay open for as long as this
	// CheckerProgram is questioned — GetTypeChecker's release function
	// returns the checker to the program's pool immediately, and a
	// released checker segfaults on its next question
	// (typereading/read_type_test.go holds it open via t.Cleanup for
	// the same reason). CheckerProgram.Done carries the release
	// forward to whoever discards this program.
	c, done := p.GetTypeChecker(context.Background())
	return &program.CheckerProgram{
		Program:      p,
		Checker:      c,
		Entry:        entry.AsSourceFile(),
		SurfacePaths: map[string]bool{SurfacePath: true},
		Done:         done,
	}, nil
}

// ProgramFromDisk is programFromDisk in the TS source: build a
// program over REAL files — the entry as it sits on disk, module
// resolution over the real filesystem, and the surface recognized at
// ITS real path (surfacePath, the caller's copy of surface/z.ts).
func ProgramFromDisk(entryFilePath string, surfacePath string) (*program.CheckerProgram, error) {
	resolved, err := filepath.Abs(entryFilePath)
	if err != nil {
		resolved = entryFilePath
	}
	if held, ok := CachedProgram(resolved); ok && StillCurrent(held) {
		TouchCachedProgram(resolved, held)
		return held.Built, nil
	}
	p := BuiltProgram([]string{resolved})
	entry := p.GetSourceFile(resolved)
	if entry == nil {
		return nil, errEntryDidNotParse("the entry file did not parse: " + resolved)
	}
	// see ProgramFromSource's comment: the lease stays open for the
	// CheckerProgram's whole lifetime, which for a REMEMBERED program
	// is the cache's lifetime — RememberProgram's LRU eviction is the
	// only place that ever discards one, so eviction is where Done
	// must eventually be called (a future wave's concern; today's
	// capacity is small enough that leases accumulate rather than
	// leaking meaningfully within one process run).
	c, done := p.GetTypeChecker(context.Background())
	built := &program.CheckerProgram{
		Program:      p,
		Checker:      c,
		Entry:        entry.AsSourceFile(),
		SurfacePaths: map[string]bool{surfacePath: true},
		Done:         done,
	}
	RememberProgram(resolved, &HeldProgram{Built: built, Stamps: StampsOf(p)})
	return built, nil
}

// ProgramFromExisting is programFromExisting in the TS source
// (program_host.ts): wrap a Program the caller already holds — the
// language-service / live-overlay seam. It must NOT RememberProgram
// and must NOT build a new compiler.Program.
//
// The checker comes from GetTypeCheckerForFile with the REQUEST
// context — never GetTypeChecker(context.Background()) — because a
// language service leases from the project checker pool, where the
// per-file lease and the ctx's checker lifetime are load-bearing
// (compiler/program.go's GetTypeCheckerForFile comment; the locked
// Pattern 1 of GO-LSP-EDITOR-PATH.md §15.2). Done carries the real
// release; the caller must call it when the check is over.
//
// Surface recognition: the caller's paths verbatim when given, else
// discovery over the program (SurfacePathsOf) — the plugin's own
// order (plugin/index.cjs ~62–78).
func ProgramFromExisting(
	ctx context.Context,
	prog *compiler.Program,
	entryPath string,
	surfacePaths []string,
) (*program.CheckerProgram, error) {
	entry := prog.GetSourceFile(entryPath)
	if entry == nil {
		return nil, errEntryDidNotParse("the entry file is not in the program: " + entryPath)
	}
	if len(surfacePaths) == 0 {
		surfacePaths = SurfacePathsOf(prog)
	}
	surface := make(map[string]bool, len(surfacePaths))
	for _, path := range surfacePaths {
		surface[path] = true
	}
	c, done := prog.GetTypeCheckerForFile(ctx, entry)
	return &program.CheckerProgram{
		Program:      prog,
		Checker:      c,
		Entry:        entry.AsSourceFile(),
		SurfacePaths: surface,
		Done:         done,
	}, nil
}

// SurfacePathsOf is the plugin's surfacePathsOf discovery half
// (plugin/index.cjs ~70–78): every source file in the program whose
// path ends in /surface/z.ts, inserted VERBATIM as its own
// FileName() — SurfacePaths membership is an exact string test on
// declaration file names (annotations/chain_roots.go), so nothing is
// re-spelled here. The tsconfig plugins[].surfacePaths config channel
// is the follow-on production parser (GO-LSP-EDITOR-PATH.md §15.9,
// §16.3 item 2) — not read here.
func SurfacePathsOf(prog *compiler.Program) []string {
	var found []string
	for _, sourceFile := range prog.SourceFiles() {
		if strings.HasSuffix(sourceFile.FileName(), "/surface/z.ts") {
			found = append(found, sourceFile.FileName())
		}
	}
	return found
}

// ProgramFromDiskMany is programFromDiskMany in the TS source: one
// program over MANY entry files — batch mode.
func ProgramFromDiskMany(entryPaths []string) *compiler.Program {
	if len(entryPaths) == 0 {
		host := compiler.NewCompilerHost("/", bundled.WrapFS(osvfs.FS()), bundled.LibPath(), nil, nil, nil)
		config := tsoptions.NewParsedCommandLine(Options(), nil, tspath.ComparePathsOptions{UseCaseSensitiveFileNames: true, CurrentDirectory: "/"})
		p := compiler.NewProgram(compiler.ProgramOptions{Config: config, Host: host})
		p.BindSourceFiles()
		return p
	}
	return BuiltProgram(entryPaths)
}

// ShapeDiagnostics is shapeDiagnostics in the TS source: tsc's own
// diagnostics for the entry file, reported before any refinement
// judgment. The shape channel is Program.GetSemanticDiagnostics,
// direct — the parse-only tsgo-oracle branch has no Go twin (this
// tree's checker is always the real in-process one).
func ShapeDiagnostics(p *program.CheckerProgram) []*ast.Diagnostic {
	return p.Program.GetSemanticDiagnostics(context.Background(), p.Entry)
}

type errEntryDidNotParse string

func (e errEntryDidNotParse) Error() string { return string(e) }
