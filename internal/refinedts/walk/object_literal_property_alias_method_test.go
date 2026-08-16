// Pins the object-literal PROPERTY-ALIAS shape: `{ bump: helperFn }`,
// where helperFn is a SEPARATELY DECLARED function (not an inline
// method or arrow), called through the literal (`person.bump()`).
//
// The gap this pins: contract_file_facts.go's collector only registers
// a PropertyAssignment under its OWN symbol when the initializer IS an
// arrow/function expression directly (`{ bump(): void {...} }` or
// `{ bump: () => {...} }`) — a property pointing at an EXISTING name
// registered nothing under the property's own symbol, so ContractOf
// found no contract for `person.bump` at all. The call fell to
// ReadBuiltinCall's method dispatcher (builtin_models.go), which gates
// its whole chain on `ContractOf(...) == nil` and ends at
// readUnmodeledMethod — a SILENT havoc: ForgetThrough erased person's
// entire tracked object, including the "age" key nothing about this
// call actually touched beyond `this.age`, with no diagnostic at all.
//
// The fix is two-part: contract_file_facts.go's new
// "facts.compile.propertyAliases" pass registers the property's own
// symbol as an alias of the target function's contract (mirroring the
// existing const-alias pass), and objectLiteralMethodWalkTarget
// (method_this_writes.go) recognizes a FunctionDeclaration/
// FunctionExpression resolved through such an alias whose CALLEE
// (not the resolved declaration) is a property of an object literal
// (calleePropertyInLiteral) — an ArrowFunction alias is excluded
// because it lexically captures `this` rather than binding to the
// receiver.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
)

const objectLiteralPropertyAliasMethodSource = "function helperBump(): void {\n" +
	"  this.age = this.age + 1;\n" +
	"}\n" +
	"function propertyAliasWritingMember(): number {\n" +
	"  const person = {\n" +
	"    age: 40,\n" +
	"    bump: helperBump,\n" +
	"  };\n" +
	"  person.bump();\n" +
	"  const ok: number = person.age;\n" +
	"  return ok;\n" +
	"}\n" +
	"function helperSpoil(): void {\n" +
	"  this.age = 200;\n" +
	"}\n" +
	"function propertyAliasSpoilingMember(): number {\n" +
	"  const outlaw = {\n" +
	"    age: 40,\n" +
	"    spoil: helperSpoil,\n" +
	"  };\n" +
	"  outlaw.spoil();\n" +
	"  return outlaw.age;\n" +
	"}\n"

// TestObjectLiteralPropertyAliasMethod_ContractResolvesForTheProperty is
// the registration-layer check: ContractOf must find a contract for
// person.bump's callee — before the fix this answered nil, which is
// what routed the call to ReadBuiltinCall's unmodeled-method tail
// instead of InlineContractBody at all.
func TestObjectLiteralPropertyAliasMethod_ContractResolvesForTheProperty(t *testing.T) {
	p := entryEnvTestProgram(t, objectLiteralPropertyAliasMethodSource)
	ctx := superArrayContracts(t, p)
	fn := entryEnvFunctionNamed(t, p, "propertyAliasWritingMember")
	call := superArrayFirstNode(t, fn.Body(), "person.bump() call", func(node *ast.Node) bool {
		if !ast.IsCallExpression(node) {
			return false
		}
		callee := node.AsCallExpression().Expression
		return ast.IsPropertyAccessExpression(callee) && callee.AsPropertyAccessExpression().Name().Text() == "bump"
	})
	calleeExpression := call.AsCallExpression().Expression
	if contract := ContractOf(ctx, calleeExpression); contract == nil {
		t.Fatalf("ContractOf found no contract for person.bump — the property-alias registration pass did not run or did not resolve")
	}
}

// TestObjectLiteralPropertyAliasMethod_TheInSetBumpStaysSilent runs the
// WHOLE function through AnalyzeFunction (so person.bump() actually
// executes before the read) and asserts BOTH that no diagnostic fires
// on the in-set read AND that the read value is the exact scalar 41 —
// an Unknown value left by a silent havoc would also pass a
// diagnostic-only check against a wide window, so the exact-value
// assertion is load-bearing.
func TestObjectLiteralPropertyAliasMethod_TheInSetBumpStaysSilent(t *testing.T) {
	kernel := superArrayLoadKernel(t)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, objectLiteralPropertyAliasMethodSource)
	ctx := superArrayContracts(t, p)
	ctx.Kernel = kernel

	fn := entryEnvFunctionNamed(t, p, "propertyAliasWritingMember")
	var diagnostics []assignability.RefinementDiagnostic
	var sink []abstractdomain.AbstractValue
	bodyCtx := *ctx
	bodyCtx.Report = func(d assignability.RefinementDiagnostic) {
		diagnostics = append(diagnostics, d)
	}
	bodyCtx.ReturnSink = &sink
	contract := &FunctionContract{
		Declaration: fn,
		Result:      withReachStatedWindow(0, 120),
		Grounded:    true,
	}
	AnalyzeFunction(&bodyCtx, contract, nil)

	for _, d := range diagnostics {
		if d.Code == 7001 || d.Code == 7002 {
			t.Errorf("propertyAliasWritingMember fired code %d: %s — person.bump()'s write (40 -> 41) through the ALIASED function was forgotten instead of threaded back onto person.age", d.Code, d.MessageText)
		}
	}
	if len(sink) == 0 {
		t.Fatalf("AnalyzeFunction's ReturnSink caught nothing — the return statement never ran")
	}
	value := sink[0]
	for _, v := range sink[1:] {
		value = abstractdomain.JoinKnown(value, v)
	}
	if value.Kind != abstractdomain.KindValues || len(value.Values) != 1 || value.Values[0] != 41 {
		spelled, _ := abstractdomain.FormatAbstractValue(value)
		t.Errorf("person.age after person.bump() = %q, want the exact scalar 41", spelled)
	}
}

// TestObjectLiteralPropertyAliasMethod_TheOutOfSetSpoilStillFires is the
// marked twin: outlaw.spoil() writes this.age = 200 through the
// ALIASED helper, out of Age's [0,120] window, and the read afterward
// must fire.
func TestObjectLiteralPropertyAliasMethod_TheOutOfSetSpoilStillFires(t *testing.T) {
	kernel := superArrayLoadKernel(t)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, objectLiteralPropertyAliasMethodSource)
	ctx := superArrayContracts(t, p)
	ctx.Kernel = kernel

	fn := entryEnvFunctionNamed(t, p, "propertyAliasSpoilingMember")
	var diagnostics []assignability.RefinementDiagnostic
	bodyCtx := *ctx
	bodyCtx.Report = func(d assignability.RefinementDiagnostic) {
		diagnostics = append(diagnostics, d)
	}
	contract := &FunctionContract{
		Declaration: fn,
		Result:      withReachStatedWindow(0, 120),
		Grounded:    true,
	}
	AnalyzeFunction(&bodyCtx, contract, nil)

	fired := false
	for _, d := range diagnostics {
		if d.Code == 7001 || d.Code == 7002 {
			fired = true
		}
	}
	if !fired {
		t.Errorf("propertyAliasSpoilingMember did not fire for the out-of-set 200 written through the aliased helperSpoil")
	}
}
