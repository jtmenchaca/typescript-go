// Targeted tests for bindObjectPattern's defaulted-slot binding
// (destructure_binding.go) and SlotOf's completeness reading
// (destructuring.go): `const { age: ok = 18 } = person` where person
// is a COMPLETE, empty object literal. The member is PROVABLY absent
// — SlotOf now says so exactly (Undef, not generic unknown) — so the
// binding takes the default's own value rather than residuing away
// the default entirely, which is what the checker did before this
// fix (destructure_binding.go's old `held.Kind == KindUnknown` branch
// discarded be.Initializer without reading it).
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
)

// destructureBindingVariableStatementNamed finds the Nth top-level
// VariableStatement inside a named function's body — mirrors
// entryEnvFunctionNamed/callSiteFunctionNamed's "walk the entry file
// for one named shape" recipe, one level deeper (inside the body).
func destructureBindingVariableStatementNamed(t *testing.T, fn *ast.Node, index int) *ast.Node {
	t.Helper()
	body := fn.AsFunctionDeclaration().Body
	if body == nil {
		t.Fatalf("function has no body")
	}
	found := 0
	for _, statement := range body.AsBlock().Statements.Nodes {
		if !ast.IsVariableStatement(statement) {
			continue
		}
		if found == index {
			return statement
		}
		found++
	}
	t.Fatalf("no variable statement at index %d", index)
	return nil
}

// destructureBindingCtx is a checker-backed FlowContext with an empty
// registry — the same recipe call_site_snapshot_fill.go's minimal
// contexts use (P, Report, Aliases, Declared).
func destructureBindingCtx(p *program.CheckerProgram, report func(assignability.RefinementDiagnostic)) *FlowContext {
	if report == nil {
		report = func(assignability.RefinementDiagnostic) {}
	}
	return &FlowContext{
		P:        p,
		Report:   report,
		Aliases:  dataflowfacts.NewAliasClasses(),
		Declared: map[string]*annotations.DeclaredRefinement{},
	}
}

// TestBindObjectPattern_AProvablyAbsentMemberTakesExactlyTheDefault is
// the fixture's own shape (a-statements.ts, objectDestructureWithDefault):
// person is `{}` typed `{ age?: number }` — a complete object with no
// keys, so `age` is provably absent — and ok's binding must equal the
// default literal 18 exactly, not residue to unknown.
func TestBindObjectPattern_AProvablyAbsentMemberTakesExactlyTheDefault(t *testing.T) {
	p := entryEnvTestProgram(t, `
function f(): void {
  const person: { age?: number } = {};
  const { age: ok = 18 } = person;
  ok;
}
`)
	fn := entryEnvFunctionNamed(t, p, "f")
	ctx := destructureBindingCtx(p, nil)
	env := NewEnv()

	personDecl := destructureBindingVariableStatementNamed(t, fn, 0)
	AnalyzeVariableStatement(ctx, env, personDecl)

	patternDecl := destructureBindingVariableStatementNamed(t, fn, 1)
	AnalyzeVariableStatement(ctx, env, patternDecl)

	held, ok := env.Get("ok")
	if !ok {
		t.Fatalf("ok was never bound")
	}
	formatted, formatOk := abstractdomain.FormatAbstractValue(held)
	if !formatOk || formatted != "18" {
		t.Errorf("env[ok] = %q, %v (Kind=%v), want %q, true — the default is the whole binding when the member is provably absent", formatted, formatOk, held.Kind, "18")
	}
}

// TestBindObjectPattern_TheMarkedDefaultReadsItsOwnOutOfRangeValue
// mirrors the fixture's second destructure — `{ age: bad = 200 }` —
// confirming the join carries 200 through to bad exactly, the value
// the fixture's `return bad` line depends on firing against Age.
func TestBindObjectPattern_TheMarkedDefaultReadsItsOwnOutOfRangeValue(t *testing.T) {
	p := entryEnvTestProgram(t, `
function f(): void {
  const person: { age?: number } = {};
  const { age: bad = 200 } = person;
  bad;
}
`)
	fn := entryEnvFunctionNamed(t, p, "f")
	ctx := destructureBindingCtx(p, nil)
	env := NewEnv()

	personDecl := destructureBindingVariableStatementNamed(t, fn, 0)
	AnalyzeVariableStatement(ctx, env, personDecl)

	patternDecl := destructureBindingVariableStatementNamed(t, fn, 1)
	AnalyzeVariableStatement(ctx, env, patternDecl)

	held, ok := env.Get("bad")
	if !ok {
		t.Fatalf("bad was never bound")
	}
	formatted, formatOk := abstractdomain.FormatAbstractValue(held)
	if !formatOk || formatted != "200" {
		t.Errorf("env[bad] = %q, %v (Kind=%v), want %q, true", formatted, formatOk, held.Kind, "200")
	}
}

// TestSlotOf_ACompleteObjectMissingAKeyIsProvablyAbsent is the
// narrower unit case: SlotOf on a complete, empty object answers
// Undef for a missing key (provably absent), not generic unknown —
// the precision withDefaultValue's join depends on to tell "the
// default always runs" from "maybe it runs, maybe it doesn't."
func TestSlotOf_ACompleteObjectMissingAKeyIsProvablyAbsent(t *testing.T) {
	empty := abstractdomain.KnownObject(nil, nil, true, abstractdomain.TrustProved, false)
	held := SlotOf(empty, "age")
	if held.Kind != abstractdomain.KindUndef {
		t.Errorf("SlotOf(complete empty object, age).Kind = %v, want Undef", held.Kind)
	}
}

// TestSlotOf_AnIncompleteObjectMissingAKeyStaysUnknown is the sound
// counterpart: an INCOMPLETE source (a spread of unknown origin, a
// widened join) may still carry the key under cover this read cannot
// see, so a miss there must stay honestly unknown — never Undef.
func TestSlotOf_AnIncompleteObjectMissingAKeyStaysUnknown(t *testing.T) {
	incomplete := abstractdomain.KnownObject(nil, nil, false, abstractdomain.TrustProved, false)
	held := SlotOf(incomplete, "age")
	if held.Kind == abstractdomain.KindUndef {
		t.Errorf("SlotOf(incomplete empty object, age).Kind = Undef, want anything but Undef — the key may still be there")
	}
}
