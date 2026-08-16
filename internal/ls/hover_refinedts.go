// The refinement half of ProvideHover: the spelled refined set at the
// hovered position, put inside the type line the way the classic
// plugin does it (tsc-vscode/plugin/index.cjs ~284–329). The plugin
// splices tsserver displayParts; the Go quickInfo is one rendered
// string, so the splice reads the string — the last `=` or `:`
// separator stands in for the last `=`/`:` display part.

package ls

import (
	"context"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/compiler"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/service"
)

// refinementSpellingAt answers the refined-set spelling at a position,
// "" where nothing is stated or known. Gated to .ts files (the
// plugin's own endsWith(".ts") gate — includes .d.ts, excludes .tsx).
func (l *LanguageService) refinementSpellingAt(ctx context.Context, program *compiler.Program, file *ast.SourceFile, position int) string {
	if !strings.HasSuffix(file.FileName(), ".ts") {
		return ""
	}
	spelled, ok := service.FormatRefinementAt(ctx, program, file.FileName(), position, service.SurfacePathsOf(program))
	if !ok {
		return ""
	}
	return spelled
}

// bareFunctionSpelling is the ENTIRE hover value FormatAbstractValue
// produces for a plain function value with no refinement beyond its
// sort (abstractdomain/format_abstract_values.go's KindHostFunction,
// top position). The declared signature already says "this is a
// function" — appending this exact spelling repeats that and nothing
// more, so the splice below drops it rather than appending it.
const bareFunctionSpelling = "{a function}"

// spliceRefinementSpelling applies the plugin's rendering rule: a
// spelling that opens with a brace is a suffix — it appends after the
// host type; anything else REPLACES the right-hand side, everything
// after the last `=` or `:`. A suffix that says nothing past the sort
// the signature already shows (bareFunctionSpelling) is dropped
// entirely — refinement-bearing spellings ("{a function, or absent}",
// any {...} with real content) still append as before.
func spliceRefinementSpelling(quickInfo string, spelled string) string {
	if spelled == "" || spelled == bareFunctionSpelling {
		return quickInfo
	}
	if !refinementsets.ReplacesHostType(spelled) {
		return quickInfo + " " + spelled
	}
	cut := strings.LastIndexAny(quickInfo, "=:")
	if cut == -1 {
		return quickInfo + " " + spelled
	}
	return quickInfo[:cut+1] + " " + spelled
}
