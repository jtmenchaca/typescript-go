// Pins construct A's fix (today's a-statements diagnosis, the 11
// undetermined rows a checked declaration's unbounded number ground
// used to decline at): return_type_ground.go's typeGroundOf now stamps
// TrustLibrary on every ground it reads from a checked declaration —
// opaqueWorn's (unmodeled_call_result.go) own precedent, moved into
// the one reader every caller shares. nan_wrapper.go's CheckPossiblyNaN
// keys its "adds nothing beyond the sort ground" skip on that grade: a
// GRADED wrapper takes the subset question and judges normally against
// a bounded sink (the 11 rows' own expectation); an UNGRADED wrapper —
// AfterReaders' fallback seed for an expression this walk never
// examined at all — keeps today's decline. This file pins both halves
// together with the grade itself, so the fix cannot regress either
// side silently.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// groundProvenanceCallExpr builds a real program from source, finds
// the named top-level function, and returns its first statement's
// return expression — expected to be a call expression reaching a
// bodiless (`declare function`) callee, the shape every one of the 11
// fixture rows routes an unbounded number through.
func groundProvenanceCallExpr(t *testing.T, source string, functionName string) (*FlowContext, *ast.Node) {
	t.Helper()
	p := entryEnvTestProgram(t, source)
	fn := entryEnvFunctionNamed(t, p, functionName)
	body := fn.AsFunctionDeclaration().Body
	if body == nil || !ast.IsBlock(body) {
		t.Fatalf("%s has no block body", functionName)
	}
	statements := body.AsBlock().Statements.Nodes
	if len(statements) == 0 || !ast.IsReturnStatement(statements[0]) {
		t.Fatalf("%s's first statement is not `return …;`", functionName)
	}
	expr := statements[0].AsReturnStatement().Expression
	if expr == nil || !ast.IsCallExpression(expr) {
		t.Fatalf("%s's return expression is not a call", functionName)
	}
	ctx := &FlowContext{P: p}
	return ctx, expr
}

// TestReturnTypeGround_CheckedDeclarationGroundCarriesLibraryGrade pins
// item 1 and item 3 of the ruled design directly on the reader, ahead
// of the gate: a ground typeGroundOf reads from a bodiless `declare
// function` returning bare `number` — the exact shape unreadNumber()
// wears in a-statements.ts (lines 55, 74, 195, 238, 489 among others) —
// is KindPossiblyNaN and carries known.Grade == TrustLibrary the
// moment it leaves ReturnTypeGround, before any caller (opaqueWorn or
// otherwise) does its own AtTrustLevel wrapping.
func TestReturnTypeGround_CheckedDeclarationGroundCarriesLibraryGrade(t *testing.T) {
	ctx, callExpr := groundProvenanceCallExpr(t,
		"declare function unreadNumber(): number;\n"+
			"function f(): number {\n"+
			"  return unreadNumber();\n"+
			"}\n",
		"f",
	)
	ground := ReturnTypeGround(ctx, callExpr)
	if ground == nil {
		t.Fatalf("ReturnTypeGround(unreadNumber()) = nil, want the number ground")
	}
	if ground.Kind != abstractdomain.KindPossiblyNaN {
		t.Fatalf("ReturnTypeGround(unreadNumber()) kind = %v, want KindPossiblyNaN — a bare `number` return admits NaN", ground.Kind)
	}
	if ground.Grade != abstractdomain.TrustLibrary {
		t.Errorf("ReturnTypeGround(unreadNumber()) Grade = %q, want %q — the ground is read from a checked declaration, opaqueWorn's own standing", ground.Grade, abstractdomain.TrustLibrary)
	}
}

// TestCheckPossiblyNaN_GradedUnboundedGroundFiresAtABoundedSink pins
// the gate's graded half: known built exactly as ReturnTypeGround now
// builds it (PossiblyNaN(KnownSet(Numbers)) stamped TrustLibrary) is a
// SERVED claim against a bounded target — "number, possibly NaN" is a
// declaration's own claim, and Age (0..120, integer) excludes both the
// unbounded real half and NaN. Fires 7001, never the 7002 decline the
// identical-shaped ungraded seed still takes below.
func TestCheckPossiblyNaN_GradedUnboundedGroundFiresAtABoundedSink(t *testing.T) {
	node := nanWrapperTestNode(t)
	ctx := &FlowContext{Kernel: nanWrapperLoadKernel(t)}
	var reported []assignability.RefinementDiagnostic
	ctx.Report = func(d assignability.RefinementDiagnostic) { reported = append(reported, d) }

	graded := abstractdomain.AtTrustLevel(
		abstractdomain.PossiblyNaN(
			abstractdomain.KnownSet(refinementsets.Numbers, nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone),
		),
		abstractdomain.TrustLibrary,
	)
	if graded.Grade != abstractdomain.TrustLibrary {
		t.Fatalf("the fixture's own graded wrapper carries Grade = %q, want %q — fix the fixture before trusting the pin below", graded.Grade, abstractdomain.TrustLibrary)
	}

	CheckPossiblyNaN(ctx, graded, ageTarget(), node, "a checked declaration's return")

	if len(reported) != 1 {
		t.Fatalf("reported %d diagnostics, want exactly 1: %+v", len(reported), reported)
	}
	if reported[0].Code != 7001 {
		t.Errorf("Code = %d, want 7001 — a GRADED unbounded number is a served claim, and Age (0..120) excludes it; declining here would be silence about a checked declaration's own signature, at %+v", reported[0].Code, reported[0])
	}
}

// TestCheckPossiblyNaN_UngradedSeedStillDeclinesAtTheSameSink pins the
// gate's other half, at the SAME sink the fired test above uses: the
// identical PossiblyNaN(Numbers) shape, built with no grade — AfterReaders'
// own fallback seed for an expression the walk never examined
// (typereading.NumberWithNaN's exact construction). "Any real, or NaN"
// here is not a derived fact, so the gate must keep today's 7002 —
// refusing would report a range violation about a value nothing ever
// looked at. Mirrors nan_wrapper_test.go's own pin so the two-case
// split reads as one rule in one place.
func TestCheckPossiblyNaN_UngradedSeedStillDeclinesAtTheSameSink(t *testing.T) {
	node := nanWrapperTestNode(t)
	var reported []assignability.RefinementDiagnostic
	ctx := &FlowContext{Report: func(d assignability.RefinementDiagnostic) { reported = append(reported, d) }}

	ungraded := abstractdomain.PossiblyNaN(
		abstractdomain.KnownSet(refinementsets.Numbers, nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone),
	)
	if ungraded.Grade != "" {
		t.Fatalf("the fixture's own ungraded wrapper carries Grade = %q, want \"\" — fix the fixture before trusting the pin below", ungraded.Grade)
	}

	CheckPossiblyNaN(ctx, ungraded, ageTarget(), node, "an expression this walk never examined")

	if len(reported) != 1 {
		t.Fatalf("reported %d diagnostics, want exactly 1: %+v", len(reported), reported)
	}
	if reported[0].Code != 7002 {
		t.Errorf("Code = %d, want 7002 — an UNGRADED seed carries no more information than KindUnknown, so it must still decline, at %+v", reported[0].Code, reported[0])
	}
}
