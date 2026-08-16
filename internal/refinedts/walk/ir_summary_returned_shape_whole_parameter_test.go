// Pins q-decline-names.ts's wholeRecordParameterCoverage row: a callee
// that reads and returns its own record parameter WHOLE
// (`function wholeRecordUse(person: { age: number }): { age: number } {
// return person; }`) must carry the argument's own field value through
// the call, so `wholeRecordUse({ age: 40 }).age` reads exactly 40 at the
// call site — not the unknown a bare "return an identifier" shape
// answered before returnedWholeParameterMembers existed
// (ir_summary_returned_shape.go).
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
)

// wholeRecordUseSource mirrors the fixture's own two functions: the
// callee returns its parameter unchanged, leaf for leaf, and the caller
// reads one leaf off the call's result.
const wholeRecordUseSource = "function wholeRecordUse(person: { age: number }): { age: number } {\n" +
	"  return person;\n" +
	"}\n" +
	"function f(): number {\n" +
	"  return wholeRecordUse({ age: 40 }).age;\n" +
	"}\n" +
	"function g(): number {\n" +
	"  return wholeRecordUse({ age: 200 }).age;\n" +
	"}\n"

// registerWholeRecordUseContract puts wholeRecordUse's own declaration in
// the contract registry, the same minimal row hoistCalleeProgram
// (ir_call_hoist_test.go) and the accessor tests' registerAccessorContracts
// register theirs under — a bare {Declaration: fn} is a complete contract;
// Params/Result stay nil since this row asks about the value the BODY
// carries through, not a stated annotation on either end.
func registerWholeRecordUseContract(t *testing.T, ctx *FlowContext, p *program.CheckerProgram) {
	t.Helper()
	fn := entryEnvFunctionNamed(t, p, "wholeRecordUse")
	symbol := p.Checker.GetSymbolAtLocation(fn.AsFunctionDeclaration().Name())
	if symbol == nil {
		t.Fatalf("wholeRecordUse has no symbol to register a contract under")
	}
	ctx.Contracts[symbol] = &FunctionContract{Declaration: fn}
}

func TestWholeRecordParameter_ACalleeReturningItsParameterWholeCarriesTheFieldThrough(t *testing.T) {
	kernel := compoundAssignLoadKernel(t)
	p := entryEnvTestProgram(t, wholeRecordUseSource)
	var diagnostics []assignability.RefinementDiagnostic
	ctx := compoundAssignContext(p, kernel, &diagnostics, nil)
	registerWholeRecordUseContract(t, ctx, p)
	env := NewEnv()
	statements := compoundAssignFunctionStatements(t, p, "f")
	// `return wholeRecordUse({ age: 40 }).age;` — read the call's own
	// result directly, the same expression evaluateExpression runs for
	// the fixture's `const ok: Age = wholeRecordUse({ age: 40 }).age;`
	returned := statements[0].AsReturnStatement().Expression
	value := evaluateExpression(ctx, env, returned)
	formatted, formatOk := abstractdomain.FormatAbstractValue(value)
	if !formatOk || formatted != "40" {
		t.Fatalf("wholeRecordUse({ age: 40 }).age = %q, %v, want exactly \"40\" — the argument's field must ride through the whole-record return", formatted, formatOk)
	}
	if len(diagnostics) != 0 {
		t.Errorf("wholeRecordUse({ age: 40 }).age raised %d diagnostics, want 0: %+v", len(diagnostics), diagnostics)
	}
}

// The marked row: wholeRecordUse({ age: 200 }).age carries 200 back
// through the same whole-record return — past Age's [0, 120] ceiling —
// so a judged read must fire exactly as a direct `person.age` read
// would.
func TestWholeRecordParameter_TheSameWholeRecordReturnCarriesAnOutOfSetFieldThrough(t *testing.T) {
	kernel := compoundAssignLoadKernel(t)
	p := entryEnvTestProgram(t, wholeRecordUseSource)
	var diagnostics []assignability.RefinementDiagnostic
	ctx := compoundAssignContext(p, kernel, &diagnostics, nil)
	registerWholeRecordUseContract(t, ctx, p)
	env := NewEnv()
	statements := compoundAssignFunctionStatements(t, p, "g")
	returned := statements[0].AsReturnStatement().Expression
	value := evaluateExpression(ctx, env, returned)
	formatted, formatOk := abstractdomain.FormatAbstractValue(value)
	if !formatOk || formatted != "200" {
		t.Fatalf("wholeRecordUse({ age: 200 }).age = %q, %v, want exactly \"200\"", formatted, formatOk)
	}
	// judged directly against Age's own window, the same check the
	// fixture's `return wholeRecordUse({ age: 200 }).age;` runs under
	window := compoundAssignAgeWindow()
	CheckAssignability(ctx, value, *window, statements[0], "a returned value", nil)
	if len(diagnostics) == 0 {
		t.Errorf("wholeRecordUse({ age: 200 }).age (200, past Age's 120 ceiling) raised no diagnostic, want one")
	}
}

// returnedWholeParameterMembers itself: one bundle entry per depth-1
// leaf of the SAME identifier every return names, aliased by index
// rather than allocated fresh — the unit-level pin beside the two
// end-to-end cases above.
func TestReturnedWholeParameterMembers_OneLeafAliasesTheParametersOwnEntry(t *testing.T) {
	p := entryEnvTestProgram(t, wholeRecordUseSource)
	fn := entryEnvFunctionNamed(t, p, "wholeRecordUse")
	body := fn.AsFunctionDeclaration().Body
	bundleEntries := []BundleEntry{{Path: "person.age", Index: 3, Written: false}}
	members, shape := returnedWholeParameterMembers(body, bundleEntries)
	if shape != RetShapeObject {
		t.Fatalf("shape = %v, want RetShapeObject", shape)
	}
	if len(members) != 1 || members[0].Name != "age" || members[0].Index != 3 {
		t.Errorf("members = %+v, want one row {Name: \"age\", Index: 3}", members)
	}
}

// A body returning two DIFFERENT identifiers on two paths has no single
// parameter's leaves to alias — the same "one shape for the whole body"
// rule returnedLiteralShape enforces for its own object/array cases.
func TestReturnedWholeParameterMembers_TwoDifferentReturnedNamesDeclineTheAlias(t *testing.T) {
	p := entryEnvTestProgram(t,
		"function pick(a: { age: number }, b: { age: number }, cond: boolean): { age: number } {\n"+
			"  if (cond) { return a; }\n"+
			"  return b;\n"+
			"}\n")
	fn := entryEnvFunctionNamed(t, p, "pick")
	body := fn.AsFunctionDeclaration().Body
	bundleEntries := []BundleEntry{
		{Path: "a.age", Index: 3, Written: false},
		{Path: "b.age", Index: 4, Written: false},
	}
	_, shape := returnedWholeParameterMembers(body, bundleEntries)
	if shape != RetShapeNone {
		t.Errorf("shape = %v, want RetShapeNone — two different returned names share no single leaf list", shape)
	}
}
