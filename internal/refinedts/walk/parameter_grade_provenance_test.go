// Pins the parameter-seeding half of construct A's fix (today's
// f-type-nodes diagnosis, fixture line 25): `function plainNumberAnnotation(age:
// number): Age { return age; }` — a declared parameter's PLAIN type is a
// declaration-backed claim exactly as a checked call's return type is
// (return_type_ground.go's typeGroundOf, ground_provenance_test.go). The
// parameter's own annotation is READ, not proved by execution — the same
// standing typeof_ground.go's GroundOfTypeofWord already stamps TrustSpec
// for, and the Rust twin's seed_parameters states in so many words
// ("known_set, TrustSpec — the annotation is read, not proved by
// execution"). entry_env.go's InitialStateOfPlainParameter now stamps
// TrustSpec on every plain-typed parameter seed it hands back, so
// nan_wrapper.go's CheckPossiblyNaN takes the fire path (a GRADED
// unbounded number at a bounded sink) instead of the ungraded-seed
// decline. This file pins both halves together with the grade itself, so
// the fix cannot regress either side silently.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/typereading"
)

// TestInitialStateOfPlainParameter_BareNumberParameterCarriesSpecGrade
// pins the reader directly, ahead of the gate: a bare `age: number`
// parameter's plain-type seed is KindPossiblyNaN and carries
// Grade == TrustSpec the moment it leaves InitialStateOfPlainParameter —
// the annotation is read off this ONE declaration, never proved by a
// kernel derivation or another declaration's own checking (the library
// standing return_type_ground.go's checked-call grounds carry instead).
func TestInitialStateOfPlainParameter_BareNumberParameterCarriesSpecGrade(t *testing.T) {
	p := entryEnvTestProgram(t, "function f(age: number) { age; }\n")
	fn := entryEnvFunctionNamed(t, p, "f")
	parameter := fn.AsFunctionDeclaration().Parameters.Nodes[0]

	held := InitialStateOfPlainParameter(p, parameter)
	if held.Kind != abstractdomain.KindPossiblyNaN {
		t.Fatalf("InitialStateOfPlainParameter(age: number).Kind = %v, want KindPossiblyNaN — a bare `number` parameter admits NaN", held.Kind)
	}
	if held.Grade != abstractdomain.TrustSpec {
		t.Errorf("InitialStateOfPlainParameter(age: number).Grade = %q, want %q — the annotation is read, not proved by execution", held.Grade, abstractdomain.TrustSpec)
	}
}

// TestBindEntryEnv_BareNumberParameterEntersTheEnvGraded pins the same
// grade through BindEntryEnv's own identifier-parameter branch (the path
// plainNumberAnnotation's own body actually walks): no stated annotation
// (a bare `number` keyword compiles to no annotations.DeclaredRefinement),
// no call-site join, so the final fallback
// (input.Env.Set(name, InitialStateOfPlainParameter(...))) is what seeds
// `age` — the exact line this construct's fix touches.
func TestBindEntryEnv_BareNumberParameterEntersTheEnvGraded(t *testing.T) {
	p := entryEnvTestProgram(t, "function f(age: number) { age; }\n")
	fn := entryEnvFunctionNamed(t, p, "f")
	env := NewEnv()
	BindEntryEnv(BindEntryEnvInput{
		P:                     p,
		Env:                   env,
		Parameters:            fn.AsFunctionDeclaration().Parameters.Nodes,
		StatedParams:          nil,
		CallSiteInitialStates: nil,
	})
	held, ok := env.Get("age")
	if !ok {
		t.Fatalf("env.Get(age) = _, false, want a bound value")
	}
	if held.Kind != abstractdomain.KindPossiblyNaN {
		t.Fatalf("env[age].Kind = %v, want KindPossiblyNaN", held.Kind)
	}
	if held.Grade != abstractdomain.TrustSpec {
		t.Errorf("env[age].Grade = %q, want %q", held.Grade, abstractdomain.TrustSpec)
	}
}

// TestCheckPossiblyNaN_GradedParameterSeedFiresAtABoundedSink composes the
// parameter-seeding fix with the existing gate (nan_wrapper.go's
// CheckPossiblyNaN, unmodified): the seed InitialStateOfPlainParameter now
// hands back for a bare `age: number` parameter — graded exactly as
// ReturnTypeGround's own ground reads, one level below it on the trust
// ladder — fires 7001 against a bounded sink (Age, 0..120 integer). This
// is f-type-nodes.ts:25's own expected state: `plainNumberAnnotation`
// returning its own unbounded parameter into `Age` is a fire, not a
// decline.
func TestCheckPossiblyNaN_GradedParameterSeedFiresAtABoundedSink(t *testing.T) {
	node := nanWrapperTestNode(t)
	ctx := &FlowContext{Kernel: nanWrapperLoadKernel(t)}
	var reported []assignability.RefinementDiagnostic
	ctx.Report = func(d assignability.RefinementDiagnostic) { reported = append(reported, d) }

	p := entryEnvTestProgram(t, "function f(age: number) { age; }\n")
	fn := entryEnvFunctionNamed(t, p, "f")
	parameter := fn.AsFunctionDeclaration().Parameters.Nodes[0]
	seed := InitialStateOfPlainParameter(p, parameter)
	if seed.Grade != abstractdomain.TrustSpec {
		t.Fatalf("the fixture's own seed carries Grade = %q, want %q — fix the fixture before trusting the pin below", seed.Grade, abstractdomain.TrustSpec)
	}

	CheckPossiblyNaN(ctx, seed, ageTarget(), node, "a declared parameter")

	if len(reported) != 1 {
		t.Fatalf("reported %d diagnostics, want exactly 1: %+v", len(reported), reported)
	}
	if reported[0].Code != 7001 {
		t.Errorf("Code = %d, want 7001 — a graded parameter seed is a served claim, and Age (0..120) excludes it; declining here would be silence about the parameter's own annotation, at %+v", reported[0].Code, reported[0])
	}
}

// TestCheckPossiblyNaN_GradedParameterSeedStaysSilentAtItsOwnUnboundedSink
// pins the no-regression half: the SAME graded parameter seed, judged
// against a target that adds nothing beyond the number sort's own ground
// (a bare, unrefined `number` position — every existing corpus row where an
// unbounded `number` parameter flows to a matching `number` sink) must stay
// silent. The grade changes only the unbounded-ground-at-a-BOUNDED-sink
// case; AddsNothingSet(target) short-circuits CheckPossiblyNaN to its
// return-early branch before the grade is ever consulted, so a graded seed
// and an ungraded one answer identically here.
func TestCheckPossiblyNaN_GradedParameterSeedStaysSilentAtItsOwnUnboundedSink(t *testing.T) {
	node := nanWrapperTestNode(t)
	var reported []assignability.RefinementDiagnostic
	ctx := &FlowContext{Report: func(d assignability.RefinementDiagnostic) { reported = append(reported, d) }}

	p := entryEnvTestProgram(t, "function f(age: number) { age; }\n")
	fn := entryEnvFunctionNamed(t, p, "f")
	parameter := fn.AsFunctionDeclaration().Parameters.Nodes[0]
	seed := InitialStateOfPlainParameter(p, parameter)
	if seed.Grade != abstractdomain.TrustSpec {
		t.Fatalf("the fixture's own seed carries Grade = %q, want %q — fix the fixture before trusting the pin below", seed.Grade, abstractdomain.TrustSpec)
	}

	CheckPossiblyNaN(ctx, seed, bareNumberTarget(t, p), node, "a declared parameter returned as its own unbounded sort")

	if len(reported) != 0 {
		t.Errorf("reported %d diagnostics, want 0 — a bare `number` sink states nothing beyond the parameter's own ground, so the grade adds no new failure case, at %+v", len(reported), reported)
	}
}

// bareNumberTarget reads a bare, unrefined `number` return type into the
// same annotations.DeclaredRefinement shape ageTarget() builds for Age —
// AddsNothingSet(this set) must read true (the whole ℝ̄ ray, no forms) for
// the silent-stays-silent pin above to test the real gate rather than a
// hand-built stand-in.
func bareNumberTarget(t *testing.T, p *program.CheckerProgram) annotations.DeclaredRefinement {
	t.Helper()
	fn := entryEnvFunctionNamed(t, p, "f")
	parameter := fn.AsFunctionDeclaration().Parameters.Nodes[0]
	decl := parameter.AsParameterDeclaration()
	held, ok := typereading.ReadDeclaredType(p.Checker, decl.Type, decl.Name())
	if !ok {
		t.Fatalf("ReadDeclaredType(age: number) = _, false, want a read")
	}
	inner := held
	if inner.Kind == abstractdomain.KindPossiblyNaN {
		inner = *inner.Inner
	}
	set, ok := abstractdomain.SetOfKnown(inner)
	if !ok {
		t.Fatalf("SetOfKnown(the bare number ground) = _, false, want a set")
	}
	if !AddsNothingSet(set) {
		t.Fatalf("AddsNothingSet(the bare number ground) = false, want true — fix the fixture before trusting the pin above")
	}
	return annotations.DeclaredRefinement{Kind: annotations.DeclaredSet, Set: &set}
}
