// Rows 3 and 4 (do-while guard): a do-while's checked body pass must
// enter with the guard assumed for iterations 2+ (row 3 — the body
// step reads a narrowed operand, not the full declared range) and
// the loop's exit value must reflect the same narrowing (row 4 — a
// read straight after the loop sees the guard-bounded result, not
// the unguarded one). Before this fix, SolveLoop applied the guard
// only to what LEAVES stepImage's body — used solely to build the
// entry-state union candidate — and ran the ONE checked/reporting
// pass against that candidate unnarrowed, so the body statement
// itself, and the value the loop hands downstream, both read the
// full declared range instead of the guard-bounded one.
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

// doWhileGuardTestBody runs a function body whose local `age` is
// pre-declared to the stated set 0..120 integer, through
// AnalyzeStatements directly — bypassing the z.* annotation surface,
// since what rows 3/4 fix lives in the loop's own entry-env
// construction, not in annotation reading.
func doWhileGuardTestBody(t *testing.T, source string) (Env, []assignability.RefinementDiagnostic) {
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
		P:         p,
		Kernel:    kernel,
		Contracts: map[*ast.Symbol]*FunctionContract{},
		Report:    func(d assignability.RefinementDiagnostic) { reports = append(reports, d) },
		Aliases:   dataflowfacts.NewAliasClasses(),
		Declared:  map[string]*annotations.DeclaredRefinement{"age": {Kind: annotations.DeclaredSet, Set: &ageSet}},
	}
	env := NewEnv()
	env.Set("age", abstractdomain.KnownValues([]float64{0}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved))

	body := fn.AsFunctionDeclaration().Body.AsBlock().Statements.Nodes
	AnalyzeStatements(ctx, env, body, nil)
	return env, reports
}

// TestSolveLoop_ADoWhileBodyStepReadsUnderTheHeldGuardNotTheFullDeclaredRange
// covers doWhileLoop's body step from a-statements.ts: `age = (age +
// 1) as Age` under `while (age < 2)` must not fire — the guard bounds
// age to < 2 on every entry (iteration 1 enters at the raw 0, which
// already satisfies it; iterations 2+ enter through the held test),
// so the step never leaves the declared 0..120 range.
func TestSolveLoop_ADoWhileBodyStepReadsUnderTheHeldGuardNotTheFullDeclaredRange(t *testing.T) {
	_, reports := doWhileGuardTestBody(t, `
function f(): number {
  let age: number = 0;
  do {
    age = age + 1;
  } while (age < 2);
  return age;
}
`)
	if len(reports) != 0 {
		t.Fatalf("reports = %+v, want none — the guard bounds the step inside the declared range", reports)
	}
}

// TestSolveLoop_ADoWhileExitValueCarriesTheHeldGuardsBound covers
// doWhileLoop's `const done: Age = age;` read straight after the
// loop: the loop's only normal exit is where the held test finally
// failed (age reaches exactly 2 at runtime: 0 → 1 → 2, test 2<2
// false, exit), so `age` should read close to the guard's own bound,
// never the full declared range the pre-fix `after` join carried in
// regardless of the guard. The exit narrowing here is not proved
// exact — {2,3} rather than the runtime-exact {2} — so the assertion
// is the honest one: bounded well below the declared ceiling, not
// bounded exactly.
func TestSolveLoop_ADoWhileExitValueCarriesTheHeldGuardsBound(t *testing.T) {
	env, reports := doWhileGuardTestBody(t, `
function f(): number {
  let age: number = 0;
  do {
    age = age + 1;
  } while (age < 2);
  return age;
}
`)
	if len(reports) != 0 {
		t.Fatalf("reports = %+v, want none", reports)
	}
	held, ok := env.Get("age")
	if !ok {
		t.Fatalf("env[age] missing after the loop")
	}
	formatted, _ := abstractdomain.FormatAbstractValue(held)
	t.Logf("age after the loop = %s", formatted)
	r := RangeOfKnown(held)
	if r == nil {
		t.Fatalf("RangeOfKnown(age) = nil, want a bounded range")
	}
	if r.Lo < 2 {
		t.Errorf("age's exit lower bound = %v, want >= 2 — the refuted guard (age < 2 failed) should exclude 0 and 1", r.Lo)
	}
	if r.Hi > 10 {
		t.Errorf("age's exit upper bound = %v, want well below the declared ceiling (120) — the pre-fix join carried the full declared range in regardless of the guard", r.Hi)
	}
}
