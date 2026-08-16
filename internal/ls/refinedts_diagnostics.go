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

package ls

import (
	"context"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/compiler"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/diagnostics"
	"github.com/microsoft/typescript-go/internal/refinedts/service"
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
		out = append(out, ast.NewExternalDiagnostic(
			file,
			core.NewTextRange(d.Start, d.Start+d.Length),
			"refinedts",
			diagnostics.CategoryError,
			int32(d.Code),
			d.MessageText,
		))
	}
	return out
}
