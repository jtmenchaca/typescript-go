// The outcome labels, pinned on one real body each — the executable
// definition of what the three labels mean.
//
// A body ends in one of exactly TWO user-visible states: calls through
// it get real answers, or they get no answer. `complete` is the first
// and is the only outcome the serving rule admits. `porous` and
// `declined` are both the second — the only difference is whether a
// partial translation was built before serving was refused. "Escapes"
// is not a third outcome: it is one of the construct diagnoses a porous
// body carries.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
)

// outcomeOf lowers a declaration and answers the recorded outcome and
// construct.
func outcomeOf(t *testing.T, declaration *ast.Node) (SummaryOutcome, string) {
	t.Helper()
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	RelowerSummaryBody(ctx, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(declaration)
	if !recorded {
		t.Fatalf("no outcome recorded — the lowering ran and must report a fate")
	}
	return outcome, construct
}

// labelMethodOf answers the first method declaration of a class source.
func labelMethodOf(t *testing.T, source string) *ast.Node {
	t.Helper()
	statements := bundleParse(t, source)
	for _, member := range statements[0].AsClassDeclaration().Members.Nodes {
		if ast.IsMethodDeclaration(member) {
			return member
		}
	}
	t.Fatalf("no method in %q", source)
	return nil
}

// DECLINED: the whole body is refused — no IR exists at all. A
// generator's calls produce an iterator, which no slot in the kernel
// grammar spells, so there is nothing partial to build.
func TestOutcomeLabels_AGeneratorDeclinesWhole(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearSummaryOutcomes()
	outcome, construct := outcomeOf(t, summaryDeclarationOf(t,
		"function* f(n: number) { yield n; return n; }"))
	if outcome != SummaryDeclined {
		t.Errorf("outcome = %q, want declined — nothing of a generator body translates", outcome)
	}
	if construct != "a generator body" {
		t.Errorf("construct = %q, want %q", construct, "a generator body")
	}
}

// POROUS: the body translates, but one statement no route reads is
// replaced by "assign unknown to every name it binds" — sound, and by
// the serving rule the summary never answers a call.
func TestOutcomeLabels_AnUntranslatableStatementMakesTheBodyPorous(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearSummaryOutcomes()
	outcome, construct := outcomeOf(t, summaryDeclarationOf(t,
		"function f(n: number) { let s = n + 1; const [a, b] = xs; return s; }"))
	if outcome != SummaryPorous {
		t.Errorf("outcome = %q, want porous — the arithmetic translated, the pattern wiped", outcome)
	}
	// the floor's own coarse naming; a finer diagnosis would say "an
	// array binding pattern". Recorded, not endorsed.
	if construct != "declaration" {
		t.Errorf("construct = %q, want %q (the current coarse spelling)", construct, "declaration")
	}
}

// "ESCAPES" IS A DIAGNOSIS, NOT AN OUTCOME: handing `this` to a callee
// the scan cannot see means no field of `this` can be trusted, the
// field reads lose their slots, and the body records POROUS carrying
// the escape as its construct.
func TestOutcomeLabels_AnEscapeIsAPorousDiagnosisNotAThirdOutcome(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearSummaryOutcomes()
	outcome, construct := outcomeOf(t, labelMethodOf(t,
		"class C { count: number; m(): number { register(this); return this.count; } }"))
	if outcome != SummaryPorous {
		t.Errorf("outcome = %q, want porous — the escape drops field knowledge, the body still lowers", outcome)
	}
	if construct != "this escapes" {
		t.Errorf("construct = %q, want %q", construct, "this escapes")
	}
}

// COMPLETE: every statement translates exactly — the one outcome whose
// summary the serving rule lets answer calls.
func TestOutcomeLabels_AFullyTranslatedBodyIsComplete(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearSummaryOutcomes()
	outcome, construct := outcomeOf(t, summaryDeclarationOf(t,
		"function f(n: number) { let s = n + 1; return s * 2; }"))
	if outcome != SummaryComplete {
		t.Errorf("outcome = %q, want complete", outcome)
	}
	if construct != "" {
		t.Errorf("construct = %q, want empty — a complete body names no construct", construct)
	}
}
