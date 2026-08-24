// DIAGNOSIS pin (not a fix pin): the "return (template over call ...)"
// census rows — BarStack.tsx's `return \`url(#${getClipPathId(stackId,
// index)})\`;`. Confirms the blocker sits in the STRING-SORTED sequence
// grammar (effect_sequence.go's templateSequenceOf / sequenceEffectOf),
// not in anything this agent's return-route arms can reach: RhsEffect
// (lowering_to_kernel_ir_return.go line ~115) already tries
// SequenceEffectOf ahead of every arm this agent owns, so a template
// substitution that is a CALL never falls through to
// ir_return_call.go's arm at all — the whole templated string either
// resolves through the sequence grammar or the return declines before
// this agent's code is even reached.
package walk

import (
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
)

func TestReturnTemplateDiagnosis_CallSubstitutionStillBlocked(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	// Go raw strings cannot embed a backtick, so the TS template literals
	// below are spelled with "B" placeholders and swapped in — the source
	// itself is BarStack.tsx's own two functions verbatim.
	source := strings.ReplaceAll(`
		const getClipPathId = (stackId: string, index: number): string => {
			return Brecharts-bar-stack-clip-path-${stackId}-${index}B;
		};
		function urlFor(stackId: string, index: number) {
			return Burl(#${getClipPathId(stackId, index)})B;
		}
	`, "B", "`")
	p := entryEnvTestProgram(t, source)
	declaration := entryEnvFunctionNamed(t, p, "urlFor")
	_, ok := RelowerSummaryBody(&FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(p.Checker, declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	t.Logf("ok=%v outcome=%q construct=%q", ok, outcome, construct)
}
