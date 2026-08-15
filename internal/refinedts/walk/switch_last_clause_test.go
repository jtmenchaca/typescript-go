// Row 2 (switch): a switch whose last clause falls off the end
// without a break — a `default:` with no trailing break, or a lone
// case with no default at all — still leaves the switch normally.
// Before this fix, AnalyzeSwitchStatement treated that exactly like
// a fallthrough INTO a further clause and havoc'd everything the
// switch might write, so a declared local's post-switch read lost
// its invariant and read back as the plain host type instead of the
// narrow per-arm join.

package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/narrowing"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// switchTestBody runs `AnalyzeStatements` over a plain function's
// body, with `age` pre-declared to the stated set 0..120 integer —
// bypassing the z.* annotation surface entirely, since what row 2
// fixes lives in the switch's own env join, not in annotation
// reading. Reports collected in order.
func switchTestBody(t *testing.T, source string) (Env, []assignability.RefinementDiagnostic) {
	t.Helper()
	kernel := kernelDelegationLoadKernel(t)
	SetTransferKernel(kernel)
	narrowing.SetNarrowKernel(kernel)
	SetEngineKernel(kernel)

	p := entryEnvTestProgram(t, source)
	fn := entryEnvFunctionNamed(t, p, "f")

	var reports []assignability.RefinementDiagnostic
	ageSet := refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.AtMost(120), refinementsets.Integer)
	ctx := &FlowContext{
		P:        p,
		Kernel:   kernel,
		Contracts: map[*ast.Symbol]*FunctionContract{},
		Report:   func(d assignability.RefinementDiagnostic) { reports = append(reports, d) },
		Aliases:  dataflowfacts.NewAliasClasses(),
		Declared: map[string]*annotations.DeclaredRefinement{"age": {Kind: annotations.DeclaredSet, Set: &ageSet}},
	}
	env := NewEnv()
	env.Set("age", abstractdomain.KnownValues([]float64{0}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved))

	body := fn.AsFunctionDeclaration().Body.AsBlock().Statements.Nodes
	AnalyzeStatements(ctx, env, body, nil)
	return env, reports
}

// TestAnalyzeSwitchStatement_ADefaultArmWithNoTrailingBreakStillJoinsRatherThanHavocs
// covers switchOk from a-statements.ts: a case ending in `break`, a
// `default` ending the switch by falling off the list — neither
// clause falls through into another, so the join stays the per-arm
// union and `age`'s declared invariant survives the read after the
// switch.
func TestAnalyzeSwitchStatement_ADefaultArmWithNoTrailingBreakStillJoinsRatherThanHavocs(t *testing.T) {
	env, reports := switchTestBody(t, `
function f(pick: number): number {
  let age: number = 0;
  switch (pick) {
    case 2:
      age = 40;
      break;
    default:
      age = 10;
  }
  return age;
}
`)
	if len(reports) != 0 {
		t.Fatalf("reports = %+v, want none (the read after the switch should stay silent)", reports)
	}
	held, ok := env.Get("age")
	if !ok {
		t.Fatalf("env[age] missing after the switch")
	}
	if held.Kind == abstractdomain.KindUnknown {
		t.Fatalf("env[age].Kind = KindUnknown, want the per-arm join (10 or 40) — the last clause's fall-off-the-end havoc'd everything")
	}
}

// TestLastRunnableClause_TheLastNonEmptyClauseHasNoFurtherClauseToFallInto
// is the pure predicate the fix adds: no clause after `index` (skipping
// the excluded one and empty ones) carries any statements, so falling
// off the end of `index` is a normal switch exit.
func TestLastRunnableClause_TheLastNonEmptyClauseHasNoFurtherClauseToFallInto(t *testing.T) {
	p := entryEnvTestProgram(t, `
function f(pick: number): number {
  switch (pick) {
    case 1:
      pick = 1;
      break;
    case 2:
      pick = 2;
    default:
      pick = 3;
  }
  return pick;
}
`)
	fn := entryEnvFunctionNamed(t, p, "f")
	var switchStmt *ast.Node
	body := fn.AsFunctionDeclaration().Body.AsBlock().Statements.Nodes
	for _, s := range body {
		if ast.IsSwitchStatement(s) {
			switchStmt = s
		}
	}
	if switchStmt == nil {
		t.Fatalf("no switch statement in the parsed body")
	}
	clauses := switchStmt.AsSwitchStatement().CaseBlock.AsCaseBlock().Clauses.Nodes
	if len(clauses) != 3 {
		t.Fatalf("len(clauses) = %d, want 3", len(clauses))
	}
	// clause 0 (`case 1:`) has a further non-empty clause after it
	// (clause 2, `default:`) — NOT the last runnable clause
	if lastRunnableClause(clauses, 0, -1) {
		t.Errorf("lastRunnableClause(clauses, 0, -1) = true, want false — clause 2 still has statements")
	}
	// clause 1 (`case 2:`, falls through into `default:`) is not last
	// either — clause 2 follows it directly
	if lastRunnableClause(clauses, 1, -1) {
		t.Errorf("lastRunnableClause(clauses, 1, -1) = true, want false — clause 2 follows it")
	}
	// clause 2 (`default:`) is the last clause in the list
	if !lastRunnableClause(clauses, 2, -1) {
		t.Errorf("lastRunnableClause(clauses, 2, -1) = false, want true — nothing follows it")
	}
}
