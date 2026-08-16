// The RefinementFix relay: the checker computes each repair at judge
// time and the diagnostic carries it — this provider only relays,
// exactly the classic plugin's getCodeFixesAtPosition seam
// (tsc-vscode/plugin/index.cjs ~151–187, fixName
// "refinedts-apply-guard"). Fix content is never decided here.
//
// External AST diagnostics cannot carry a Fix, so the provider reads
// the raw refinement results back from the Session's RefinementCache
// under the file's overlay version (put there by the diagnostics
// pull); on a version miss it re-runs CheckWithProgram — the doc's
// sanctioned fallback (GO-LSP-EDITOR-PATH.md §11.8).
//
// Fix-all is out of scope by decision lock (§15.6): no
// GetAllCodeActions, no FixIds.

package ls

import (
	"context"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/compiler"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/ls/lsconv"
	"github.com/microsoft/typescript-go/internal/lsp/lsproto"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/service"
	"github.com/microsoft/typescript-go/internal/spanmap"
)

// RefinedTSFixProvider relays RefinementFix repairs for the
// refinement codes. Fixes are attached at assignability time only
// where a repair exists — many judgments carry none, and those
// produce no action.
var RefinedTSFixProvider = &CodeFixProvider{
	ErrorCodes:     []int32{7001, 7002, 7003, 7004, 7005},
	GetCodeActions: getRefinedTSFixActions,
}

func getRefinedTSFixActions(ctx context.Context, fixContext *CodeFixContext) ([]*CodeAction, error) {
	l := fixContext.LS
	file := fixContext.SourceFile
	fileName := file.FileName()

	held, ok := l.host.RefinementCache().Get(fileName, l.host.ScriptVersion(fileName))
	if !ok {
		result, err := service.CheckWithProgram(ctx, fixContext.Program, fileName, service.SurfacePathsOf(fixContext.Program))
		if err != nil {
			// no refinement answer — no refinement fixes; never fail
			// the whole code-action request over it
			return nil, nil
		}
		held = result.Refinements
		l.host.RefinementCache().Put(fileName, l.host.ScriptVersion(fileName), held)
	}

	var actions []*CodeAction
	for _, d := range held {
		if d.Fix == nil || int32(d.Code) != fixContext.ErrorCode {
			continue
		}
		// the plugin's span test: skip when the diagnostic lies wholly
		// outside the requested range
		if d.Start > fixContext.Span.End() || d.Start+d.Length < fixContext.Span.Pos() {
			continue
		}
		actions = append(actions, refinementFixAction(l, file, d))
	}
	return actions, nil
}

// refinementFixAction is one relay: a pure insertion (length 0) at
// the Fix's stated offset.
func refinementFixAction(l *LanguageService, file *ast.SourceFile, d assignability.RefinementDiagnostic) *CodeAction {
	insertAt := core.NewTextRange(d.Fix.InsertAt, d.Fix.InsertAt)
	rng, _ := l.converters.ToLSPRangeForFeature(file, insertAt, spanmap.FeatureCodeActions)
	return &CodeAction{
		Description: d.Fix.Title,
		Changes: []*lsproto.TextEdit{{
			Range:   rng,
			NewText: d.Fix.NewText,
		}},
	}
}

// refinedtsAnnotateAction is the annotate-from-inference action as a
// POSITION-BASED quickfix — the C1 lock (GO-LSP-EDITOR-PATH.md
// §15.7): the Go LS advertises no refactor kinds, so the plugin's
// getApplicableRefactors relay lands as a quickfix branch in
// ProvideCodeActions, not a codeFixProviders row (it fires on a
// position, not a diagnostic). The checker computes the whole edit
// (the z-chain const plus the z.infer parameter type); this seam only
// relays it.
func (l *LanguageService) refinedtsAnnotateAction(
	ctx context.Context,
	program *compiler.Program,
	file *ast.SourceFile,
	params *lsproto.CodeActionParams,
) (lsproto.CommandOrCodeAction, bool) {
	if !strings.HasSuffix(file.FileName(), ".ts") {
		return lsproto.CommandOrCodeAction{}, false
	}
	mapped := lsconv.FromLSPRangeForSourceFile(l.converters, file, params.Range, spanmap.FeatureCodeActions)
	if len(mapped) == 0 {
		return lsproto.CommandOrCodeAction{}, false
	}
	position := mapped[0].Span.Pos()
	action, ok := service.AnnotateAt(ctx, program, file.FileName(), position, service.SurfacePathsOf(program))
	if !ok {
		return lsproto.CommandOrCodeAction{}, false
	}
	edits := make([]*lsproto.TextEdit, 0, len(action.Edits))
	for _, edit := range action.Edits {
		rng, _ := l.converters.ToLSPRangeForFeature(file, core.NewTextRange(edit.Start, edit.Start+edit.Length), spanmap.FeatureCodeActions)
		edits = append(edits, &lsproto.TextEdit{Range: rng, NewText: edit.NewText})
	}
	kind := lsproto.CodeActionKindQuickFix
	changes := map[lsproto.DocumentUri][]*lsproto.TextEdit{
		params.TextDocument.Uri: edits,
	}
	return lsproto.CommandOrCodeAction{
		CodeAction: &lsproto.CodeAction{
			Title: action.Title,
			Kind:  &kind,
			Edit:  &lsproto.WorkspaceEdit{Changes: &changes},
		},
	}, true
}
