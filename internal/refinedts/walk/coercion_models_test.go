// Pins the fix for A2.edge.process: parseFloat's own unpinned-argument
// fallback (coercion_models.go's readCoercionGlobals) used to answer
// abstractdomain.PossiblyNaN(KnownSet(refinementsets.MakeRefinedSet()))
// — a ZERO-form RefinedSet, which refinementsets.Numbers' own doc
// comment names as a set the kernel's scalar questions cannot even
// pose ("the kernel's scalar questions ask for at least one
// refinement"). Every downstream question against that malformed set
// silently declined, so `parseFloat(line)` against a refined Unit
// target reported nothing rather than the designated RTS7001. Fixed
// by spelling the real ground the same way Number(value)'s own
// widest-answer branch already does: refinementsets.Numbers (the
// AtLeast(-Infinity) ray), never the zero-form spelling.
package walk

import (
	"testing"
)

// TestParseFloatOfUnpinnedArgument_RefusesAgainstARefinedTarget pins
// the fix directly against the e2e fixture's own shape
// (A2.edge.process.ts): parseFloat of an unread string argument,
// assigned to a binding declared a genuinely refined Unit ([0, 1]),
// must report 7001 — the real half (the whole number ground) is not a
// subset of [0, 1], so CheckPossiblyNaN's own subset question must
// refuse rather than decline.
//
// Uses declaredLocalSinkRun (declared_local_sink_test.go), not the
// bare parseVocabRun: the RETURN type `Unit` here is checked through
// CheckAssignability against the function's declared Result
// (AnalyzeReturnStatement, return_statement.go), which resolves ONLY
// through the populated annotation registry (Unit =
// z.infer<typeof zUnit> reads registry[zUnit's symbol],
// annotations.AnnotationOfType). Plain parseVocabRun never runs
// CompileAnnotationFileFacts, so CompileContractFileFacts's own
// readSignature call (contract_file_facts.go) resolved Unit to
// Unsupported with reporting=false — swallowed, never surfaced as a
// 7004 — leaving contract.Result nil. With a nil Result,
// AnalyzeReturnStatement's own gate (`if result != nil …`) skips
// CheckAssignability entirely, so `return x;` was never checked
// against anything: no 7001, no 7002. The hop that swallowed the
// refusal is that nil Result, not WriteBinding or CheckPossiblyNaN —
// both already handle a genuinely graded PossiblyNaN(Numbers) value
// correctly once they are ever reached.
func TestParseFloatOfUnpinnedArgument_RefusesAgainstARefinedTarget(t *testing.T) {
	kernel := parseVocabKernel(t)
	source := `import * as z from "/surface/z.ts";
const zUnit = z.number().min(0).max(1);
type Unit = z.infer<typeof zUnit>;
function parseFloatOfLineOutside(line: string): Unit {
  const x = parseFloat(line);
  return x;
}
`
	diagnostics := declaredLocalSinkRun(t, kernel, source, "parseFloatOfLineOutside")
	found7001 := false
	for _, d := range diagnostics {
		if d.Code == 7001 {
			found7001 = true
		}
	}
	if !found7001 {
		t.Errorf("parseFloat(line) into a declared Unit target reported no 7001 (all diagnostics: %+v) — want the refused-write refusal, not a silent decline", diagnostics)
	}
}
