// Pins the property-access half of construct A's fix: object_key_access.go's
// memberValueGraded threads a receiver's own Grade onto the member it
// reads off it, the same standing return_type_ground.go's typeGroundOf
// already stamps a checked declaration's own return type with
// (ground_provenance_test.go). `handle.value` off `using handle =
// device()` — device(): { readonly value: number } & Disposable — is
// the exact shape a-statements.ts:672 and :679 route through: the
// object return already carries Grade == TrustLibrary (typeGroundOf's
// AtTrustLevel stamp), and the member read must inherit it rather than
// handing back the ungraded NumberWithNaN shape readHostTypeUncached
// builds for every object member by construction.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// memberGradePropertyAccessExpr builds a real program, finds the named
// top-level function, and returns its first statement's variable
// declaration name (the receiver identifier to bind in env) beside the
// second statement's return expression — expected to be `receiver.key`.
func memberGradePropertyAccessExpr(t *testing.T, source string, functionName string) (*FlowContext, string, *ast.Node) {
	t.Helper()
	p := entryEnvTestProgram(t, source)
	fn := entryEnvFunctionNamed(t, p, functionName)
	body := fn.AsFunctionDeclaration().Body
	if body == nil || !ast.IsBlock(body) {
		t.Fatalf("%s has no block body", functionName)
	}
	statements := body.AsBlock().Statements.Nodes
	if len(statements) < 2 || !ast.IsVariableStatement(statements[0]) {
		t.Fatalf("%s's first statement is not a variable declaration", functionName)
	}
	declarations := statements[0].AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes
	if len(declarations) == 0 {
		t.Fatalf("%s's first statement declares no name", functionName)
	}
	receiverName := declarations[0].AsVariableDeclaration().Name()
	if receiverName == nil || !ast.IsIdentifier(receiverName) {
		t.Fatalf("%s's declared name is not a plain identifier", functionName)
	}
	if !ast.IsReturnStatement(statements[1]) {
		t.Fatalf("%s's second statement is not `return …;`", functionName)
	}
	expr := statements[1].AsReturnStatement().Expression
	if expr == nil || (!ast.IsPropertyAccessExpression(expr) && !ast.IsElementAccessExpression(expr)) {
		t.Fatalf("%s's return expression is not a property or element access", functionName)
	}
	ctx := &FlowContext{P: p}
	return ctx, receiverName.Text(), expr
}

// gradedDeviceObject is the exact shape typeGroundOf's AtTrustLevel
// stamp leaves on `device(): { readonly value: number } & Disposable`'s
// call result: a KindObject whose OWN Grade reads TrustLibrary (the
// checked declaration's own return type ground), while the `value`
// member underneath still carries readHostTypeUncached's default
// ungraded NumberWithNaN shape — the exact split memberValueGraded
// exists to close.
func gradedDeviceObject() abstractdomain.AbstractValue {
	member := abstractdomain.PossiblyNaN(
		abstractdomain.KnownSet(refinementsets.Numbers, nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone),
	)
	obj := abstractdomain.KnownObject(
		[]abstractdomain.ObjectKey{{Name: "value", Value: member}},
		nil, true, abstractdomain.TrustProved, false,
	)
	return abstractdomain.AtTrustLevel(obj, abstractdomain.TrustLibrary)
}

// TestReadPropertyAccess_DeclarationBackedReceiverGradesTheMember pins
// item 1 of construct A's property-access fix: `handle.value` off a
// receiver whose own Grade reads TrustLibrary (device()'s checked
// return) answers a member carrying that same grade — not the bare
// ungraded PossiblyNaN(Numbers) the object's own construction left on
// the key.
func TestReadPropertyAccess_DeclarationBackedReceiverGradesTheMember(t *testing.T) {
	ctx, receiverName, propertyAccess := memberGradePropertyAccessExpr(t,
		"declare function device(): { readonly value: number };\n"+
			"function f(): number {\n"+
			"  const handle = device();\n"+
			"  return handle.value;\n"+
			"}\n",
		"f",
	)
	receiver := gradedDeviceObject()
	if receiver.Grade != abstractdomain.TrustLibrary {
		t.Fatalf("the fixture's own receiver carries Grade = %q, want %q — fix the fixture before trusting the pin below", receiver.Grade, abstractdomain.TrustLibrary)
	}
	if receiver.Keys[0].Value.Grade != "" {
		t.Fatalf("the fixture's own member carries Grade = %q, want \"\" (readHostTypeUncached's default) — fix the fixture before trusting the pin below", receiver.Keys[0].Value.Grade)
	}

	env := NewEnv()
	env.Set(receiverName, receiver)

	got := ReadPropertyAccess(ctx, env, propertyAccess)
	if got == nil {
		t.Fatalf("ReadPropertyAccess(handle.value) = nil, want the graded member")
	}
	if got.Kind != abstractdomain.KindPossiblyNaN {
		t.Fatalf("ReadPropertyAccess(handle.value).Kind = %v, want KindPossiblyNaN", got.Kind)
	}
	if got.Grade != abstractdomain.TrustLibrary {
		t.Errorf("ReadPropertyAccess(handle.value).Grade = %q, want %q — the member's own type is exactly as declaration-backed as the receiver it came from", got.Grade, abstractdomain.TrustLibrary)
	}
}

// TestReadPropertyAccess_UngradedResidueReceiverNeverInventsAGrade pins
// item 2: a receiver that is itself an ungraded residue — the walk
// never proved anything about it, so its own Grade reads "" — must NOT
// have a grade invented for the member read off it. memberValueGraded's
// own gate (receiver.Grade == "") is what this test observes; without
// it, an ordinary object literal or an unmodeled receiver's member
// would start reporting a manufactured TrustLibrary claim.
func TestReadPropertyAccess_UngradedResidueReceiverNeverInventsAGrade(t *testing.T) {
	ctx, receiverName, propertyAccess := memberGradePropertyAccessExpr(t,
		"declare function device(): { readonly value: number };\n"+
			"function f(): number {\n"+
			"  const handle = device();\n"+
			"  return handle.value;\n"+
			"}\n",
		"f",
	)
	member := abstractdomain.PossiblyNaN(
		abstractdomain.KnownSet(refinementsets.Numbers, nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone),
	)
	receiver := abstractdomain.KnownObject(
		[]abstractdomain.ObjectKey{{Name: "value", Value: member}},
		nil, true, abstractdomain.TrustProved, false,
	)
	if receiver.Grade != "" {
		t.Fatalf("the fixture's own receiver carries Grade = %q, want \"\" — fix the fixture before trusting the pin below", receiver.Grade)
	}

	env := NewEnv()
	env.Set(receiverName, receiver)

	got := ReadPropertyAccess(ctx, env, propertyAccess)
	if got == nil {
		t.Fatalf("ReadPropertyAccess(handle.value) = nil, want the ungraded member")
	}
	if got.Grade != "" {
		t.Errorf("ReadPropertyAccess(handle.value).Grade = %q, want \"\" — an ungraded receiver names no checked declaration to derive a member grade from", got.Grade)
	}
}

// TestCheckPossiblyNaN_MemberGradedFromDeclarationBackedReceiverFires
// composes the property-access fix with the existing gate
// (nan_wrapper.go's CheckPossiblyNaN, unmodified): the member
// ReadPropertyAccess now hands back for `handle.value` — graded exactly
// as ReturnTypeGround's own ground reads — fires 7001 against a bounded
// sink (Age, 0..120 integer), the same verdict a checked declaration's
// unbounded return already earns. This is a-statements.ts:672's
// (`using`) and :679's (`await using`) own expected state.
func TestCheckPossiblyNaN_MemberGradedFromDeclarationBackedReceiverFires(t *testing.T) {
	node := nanWrapperTestNode(t)
	ctx := &FlowContext{Kernel: nanWrapperLoadKernel(t)}
	var reported []assignability.RefinementDiagnostic
	ctx.Report = func(d assignability.RefinementDiagnostic) { reported = append(reported, d) }

	receiver := gradedDeviceObject()
	member := memberValueGraded(receiver, receiver.Keys[0].Value)
	if member.Grade != abstractdomain.TrustLibrary {
		t.Fatalf("the fixture's own graded member carries Grade = %q, want %q — fix the fixture before trusting the pin below", member.Grade, abstractdomain.TrustLibrary)
	}

	CheckPossiblyNaN(ctx, member, ageTarget(), node, "a member read off a checked declaration's return")

	if len(reported) != 1 {
		t.Fatalf("reported %d diagnostics, want exactly 1: %+v", len(reported), reported)
	}
	if reported[0].Code != 7001 {
		t.Errorf("Code = %d, want 7001 — a member graded from a declaration-backed receiver is a served claim, and Age (0..120) excludes it, at %+v", reported[0].Code, reported[0])
	}
}

// TestReadElementAccess_DeclarationBackedReceiverGradesTheMember pins
// element_access.go's own memberValueGraded threading:
// `handle["value"]` is the bracketed spelling of the identical read
// `handle.value` makes (sec-property-accessors — both evaluate to the
// same Reference), so it must carry the identical grade.
func TestReadElementAccess_DeclarationBackedReceiverGradesTheMember(t *testing.T) {
	ctx, receiverName, propertyAccess := memberGradePropertyAccessExpr(t,
		"declare function device(): { readonly value: number };\n"+
			"function f(): number {\n"+
			"  const handle = device();\n"+
			"  return handle[\"value\"];\n"+
			"}\n",
		"f",
	)
	if !ast.IsElementAccessExpression(propertyAccess) {
		t.Fatalf("f's return expression is not an element access — memberGradePropertyAccessExpr's own gate should have caught this")
	}
	receiver := gradedDeviceObject()

	env := NewEnv()
	env.Set(receiverName, receiver)

	got := ReadElementAccess(ctx, env, propertyAccess)
	if got == nil {
		t.Fatalf(`ReadElementAccess(handle["value"]) = nil, want the graded member`)
	}
	if got.Kind != abstractdomain.KindPossiblyNaN {
		t.Fatalf(`ReadElementAccess(handle["value"]).Kind = %v, want KindPossiblyNaN`, got.Kind)
	}
	if got.Grade != abstractdomain.TrustLibrary {
		t.Errorf(`ReadElementAccess(handle["value"]).Grade = %q, want %q — the bracketed read is the same Reference the dotted read names`, got.Grade, abstractdomain.TrustLibrary)
	}
}
