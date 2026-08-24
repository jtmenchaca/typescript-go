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

// refinementSpellingAt answers the refined-set spelling at a
// position, plus the plain sort word of the known value ("number",
// "string", "boolean", "bigint") where the walk has it in hand — "",
// "" where nothing is stated or known. Gated to .ts files (the
// plugin's own endsWith(".ts") gate — includes .d.ts, excludes .tsx).
func (l *LanguageService) refinementSpellingAt(ctx context.Context, program *compiler.Program, file *ast.SourceFile, position int) (string, string) {
	if !strings.HasSuffix(file.FileName(), ".ts") {
		return "", ""
	}
	spelled, sortWord, ok := service.FormatRefinementAt(ctx, program, file.FileName(), position, service.SurfacePathsOf(program))
	if !ok {
		return "", ""
	}
	return spelled, sortWord
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
//
// One case renders as TWO lines instead, and it is checked BEFORE the
// append/replace choice above — it overrides that choice rather than
// following it: the host's right-hand side is exactly "any" or
// "unknown" (trimmed) — no claim of its own — and sortWord is known.
// There the sort word REPLACES that right-hand side and the spelling
// (brace-opening or not — an "any"/"unknown" host has no claim for
// ReplacesHostType to weigh against) rides after it
// ("const level: number {0 ≤ 𝑥 ≤ 1}"), and the SECOND return value
// carries the host's original, unmodified quickInfo for the caller to
// render as an italic note below the type line ("(Note: tsc type is
// 'const level: any')"). Every other case returns "" for the note and
// behaves exactly as before (single line).
func spliceRefinementSpelling(quickInfo string, spelled string, sortWord string) (string, string) {
	if spelled == "" || spelled == bareFunctionSpelling {
		return quickInfo, ""
	}
	cut := strings.LastIndexAny(quickInfo, "=:")
	if cut != -1 && sortWord != "" {
		rhs := strings.TrimSpace(quickInfo[cut+1:])
		if rhs == "any" || rhs == "unknown" {
			return quickInfo[:cut+1] + " " + sortWord + " " + spelled, quickInfo
		}
	}
	if !refinementsets.ReplacesHostType(spelled) {
		return quickInfo + " " + spelled, ""
	}
	if cut == -1 {
		return quickInfo + " " + spelled, ""
	}
	return quickInfo[:cut+1] + " " + spelled, ""
}
