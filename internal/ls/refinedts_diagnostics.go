// The refinement half of ProvideDiagnostics: run the live-program
// refinement check on the pulled file and append the judgments as
// external diagnostics with source "refinedts" — the Go LS analogue
// of the classic tsserver plugin's getSemanticDiagnostics proxy
// (tsc-vscode/plugin/index.cjs), synchronous because pull diagnostics
// are (no cache-then-refresh dance).
//
// Every refinement code (7001–7005) is an Error — the plugin forces
// DiagnosticCategory.Error for all of them, and Category drives the
// LSP severity in lsconv.
//
// A finding may also carry RELATED STEPS — other places the reader has
// to see. They ride the host diagnostic's own relatedInformation, which
// the LSP renderer already turns into navigable links
// (ls/lsconv/converters.go:454-472); nothing in the renderer changes.
//
// The foreign-file case (a step in the Python half of a crossing) works
// for a file that EXISTS on disk. Two host reads stand behind that
// condition and neither is nil-checked in the renderer:
//
//   - converters.go:467 calls related.File().OriginalFileName(), so
//     every step must name a file — hence the synthetic carrier.
//   - converters.go:376-378 converts the offset with
//     getLineMap(script.FileName()), which the session answers from the
//     editor's file system (project/snapshot.go:107); for a path the fs
//     cannot read it answers nil and line 378 dereferences it.
//
// A step naming an unreadable path is therefore dropped here. Lifting
// that would take one converter change: a nil line map in
// positionToLineAndCharacter falling back to a line map computed from
// script.Text() — the carrier already holds the text. That change is
// not made here because the converter is host code shared with every
// other diagnostic path.

package ls

import (
	"context"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/compiler"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/diagnostics"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/service"
	"github.com/microsoft/typescript-go/internal/tspath"
)

// refinementDiagnostics judges the file on the SAME live program the
// shape diagnostics came from (unsaved overlay contents included) and
// answers the judgments as external AST diagnostics. The plugin's file
// gate is `fileName.endsWith(".ts")` — which INCLUDES .d.ts and
// excludes .tsx — matched here exactly. On any error the pull degrades
// to shape-only, never fails (the plugin logs and keeps prior on the
// same failure).
//
// The raw results (with their Fixes) are recorded in the Session's
// RefinementCache under the file's overlay version, where the
// RefinementFix code-action provider reads them back — external AST
// diagnostics cannot carry a Fix.
func (l *LanguageService) refinementDiagnostics(ctx context.Context, program *compiler.Program, file *ast.SourceFile) []*ast.Diagnostic {
	fileName := file.FileName()
	if !strings.HasSuffix(fileName, ".ts") {
		return nil
	}
	result, err := service.CheckWithProgram(ctx, program, fileName, service.SurfacePathsOf(program))
	if err != nil {
		// degrade to shape-only; the next pull retries
		return nil
	}
	l.host.RefinementCache().Put(fileName, l.host.ScriptVersion(fileName), result.Refinements)
	out := make([]*ast.Diagnostic, 0, len(result.Refinements))
	for _, d := range result.Refinements {
		out = append(out, RefinementToDiagnostic(file, d, l.host.FileExists))
	}
	return out
}

// RefinementToDiagnostic is the one place a RefinementDiagnostic
// becomes a host diagnostic. The finding itself is an external
// diagnostic on the checked file, as it has always been; its related
// steps become the host's own relatedInformation entries — each a
// full diagnostic with its own file and span, which is what
// lsconv.diagnosticToLSP already turns into navigable links. Nothing
// in the renderer changes.
//
// `onDisk` answers whether a path is one the editor's file system can
// read; it gates foreign steps (see relatedStepDiagnostic). Nil admits
// every path, which is what a test with a hand-built text wants.
//
// Exported so the cross-language edge can build the same shape: a
// crossing's diagnostic is a finding at the TypeScript call site plus
// one step per language it passed through.
func RefinementToDiagnostic(
	file *ast.SourceFile,
	d assignability.RefinementDiagnostic,
	onDisk func(path string) bool,
) *ast.Diagnostic {
	built := ast.NewExternalDiagnostic(
		file,
		core.NewTextRange(d.Start, d.Start+d.Length),
		"refinedts",
		diagnostics.CategoryError,
		int32(d.Code),
		d.MessageText,
	)
	for _, step := range d.Steps {
		if related := relatedStepDiagnostic(step, onDisk); related != nil {
			built.AddRelatedInfo(related)
		}
	}
	return built
}

// relatedStepDiagnostic turns one step into the related diagnostic the
// renderer reads. A step's file is REQUIRED: lsconv dereferences
// `related.File()` without a nil check (converters.go:458,467), so a
// step that cannot name a file is dropped here rather than crashing
// the pull.
//
// A foreign step — a place in a file the TypeScript program never
// compiled — is carried on a file-shaped stand-in built from the path
// and text the adapter read, so the renderer's FileName / Text /
// SpanMap reads all answer. The stand-in is never bound, never
// checked, and never enters the program; it holds a name and a text so
// a byte offset can become a line and character.
//
// One condition the stand-in cannot supply: the renderer converts the
// offset with the CONVERTERS' line map, which is keyed by file name
// and read from the editor's file system (project/snapshot.go:107 →
// SnapshotFS.GetFileByPath, which reads any path off disk), and
// lsconv.positionToLineAndCharacter dereferences that map without a
// nil check (converters.go:378). So a foreign step renders exactly
// when its file EXISTS on disk, and a step naming a path the file
// system cannot read is dropped rather than rendered — see the file
// comment on the converter change that would lift this.
func relatedStepDiagnostic(step assignability.RelatedStep, onDisk func(path string) bool) *ast.Diagnostic {
	stepFile := step.File
	if stepFile == nil {
		if step.ForeignFile == "" {
			return nil
		}
		if onDisk != nil && !onDisk(step.ForeignFile) {
			return nil
		}
		stepFile = foreignStepFile(step.ForeignFile, step.ForeignText)
		if stepFile == nil {
			return nil
		}
	}
	start := step.Start
	length := step.Length
	if start < 0 || start+length > len(stepFile.Text()) {
		// an offset the text does not contain places the step at the
		// head of its file rather than reporting a span that is not there
		start, length = 0, 0
	}
	return ast.NewExternalDiagnostic(
		stepFile,
		core.NewTextRange(start, start+length),
		"refinedts",
		diagnostics.CategoryMessage,
		0,
		step.Sentence,
	)
}

// foreignStepFile builds the file-shaped carrier for a step outside
// the compiled program. NewSourceFile requires a normalized absolute
// path (ast.go:2544 panics otherwise), so a path that is not one
// answers nil and its step is dropped. The Path field is the program's
// identity key and nothing in the render path reads it; it is filled
// from the normalized name so the value is never a lie.
func foreignStepFile(path string, text string) *ast.SourceFile {
	normalized := tspath.NormalizePath(path)
	if tspath.GetEncodedRootLength(normalized) == 0 {
		return nil
	}
	factory := ast.NewNodeFactory(ast.NodeFactoryHooks{})
	node := factory.NewSourceFile(
		ast.SourceFileParseOptions{FileName: normalized, Path: tspath.Path(normalized)},
		text,
		factory.NewNodeList(nil),
		factory.NewToken(ast.KindEndOfFile),
	)
	return node.AsSourceFile()
}
