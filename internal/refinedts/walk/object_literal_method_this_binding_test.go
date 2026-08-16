// The rows this file pins: b-body-expressions.ts:431
// (literalWritingMethod) and h-object-literal-members.ts:245
// (nonInertClosureWritingMember, its OWN in-set leg — the fixture
// runs the identical shape twice under two names). Both moved from a
// wrong FIRE to UNDETERMINED on the judge: `person.bump()` writes
// `this.age = this.age + 1` off the read entry (40 -> 41), and
// `person.age` afterward must read exactly 41 — an IN-SET value that
// must stay silent, not merely "no diagnostic."
//
// The bug this pins: EvaluateExpression's own KindThisKeyword arm
// (evaluate_expression.go) only read env.Get("this") when
// dataflowfacts.EnclosingThisClass or EnclosingThisParameterFunction
// recognized the site — neither ever fires for an OBJECT-LITERAL
// method's body, so a bare `this` INSIDE bump()'s own body (the
// receiver expression ReadObjectKeyAccess evaluates for `this.age`)
// answered silence.Residue() regardless of the binding
// ObjectLiteralMethodWalkCall (method_this_writes.go) had just set in
// its synthetic callEnv via `callEnv.Set("this", receiver)`. The read
// side of `this.age = this.age + 1` computed Unknown + 1 = Unknown,
// which folded back onto person's "age" key as Unknown — silently
// UNDETERMINED, never the exact 41, even though the write side
// (assignment_operators.go's ThisWriteSink capture) and the fold-back
// (setObjectKey) were both already correct.
//
// The fix: dataflowfacts.EnclosingThisObjectLiteralMethod
// (access_paths.go) is EnclosingThisClass's climb re-rooted at an
// object-literal method instead of a class one, and
// evaluate_expression.go's KindThisKeyword arm now also reads
// env.Get("this") when it answers non-nil — a third recognized shape
// beside the class and this-parameter ones, matching exactly the
// bodies ObjectLiteralMethodWalkCall binds "this" for.
//
// Runs the WHOLE function body through AnalyzeFunction (not a bare
// evaluateExpression of a hand-picked return alone) so person.bump()
// actually executes and every reader in the real call path
// (EvaluateCallExpression -> InlineContractBody ->
// ObjectLiteralMethodWalkCall -> the method's own AnalyzeStatements
// walk -> the fold-back -> the outer read) runs exactly as the
// fixture exercises it. The function under test RETURNS person.age
// (through ReturnSink) so the test asserts the EXACT bound value, not
// merely the absence of a diagnostic — a value that stayed Unknown
// would still pass every diagnostic-only check against Age's wide
// [0,120] window, so only an exact-scalar assertion catches this
// regression. Skips (never a faked pass) when the native kernel dylib
// is absent, per super_and_array_ctor_test.go's canonical recipe.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
)

// objectLiteralMethodThisBindingSource mirrors the fixture's own
// literalWritingMethod row verbatim (b-body-expressions.ts:431), with
// its trailing `void ok;` return replaced by `return person.age;` so
// AnalyzeFunction's ReturnSink carries the exact post-call value out —
// the fixture itself never returns `ok`, since the row's whole point
// is the SILENT assignability check at the `const ok: Age = …` line,
// which this test reproduces separately through diagnostics.
const objectLiteralMethodThisBindingSource = "function literalWritingMethod(): number {\n" +
	"  const person = {\n" +
	"    age: 40,\n" +
	"    bump(): void {\n" +
	"      this.age = this.age + 1;\n" +
	"    },\n" +
	"  };\n" +
	"  person.bump();\n" +
	"  const ok: number = person.age;\n" +
	"  return ok;\n" +
	"}\n" +
	"function literalSpoilMethod(): number {\n" +
	"  const outlaw = {\n" +
	"    age: 40,\n" +
	"    spoil(): void {\n" +
	"      this.age = 200;\n" +
	"    },\n" +
	"  };\n" +
	"  outlaw.spoil();\n" +
	"  return outlaw.age;\n" +
	"}\n"

// objectLiteralMethodThisBindingRun drives fnName's WHOLE body through
// AnalyzeFunction, wired for both diagnostics and the returned value —
// the full call path the fixture takes, not a hand-picked expression
// read.
func objectLiteralMethodThisBindingRun(t *testing.T, fnName string) (returned abstractdomain.AbstractValue, diagnostics []assignability.RefinementDiagnostic) {
	t.Helper()
	kernel := superArrayLoadKernel(t)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, objectLiteralMethodThisBindingSource)
	ctx := superArrayContracts(t, p)
	ctx.Kernel = kernel

	fn := entryEnvFunctionNamed(t, p, fnName)
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
	if len(sink) == 0 {
		t.Fatalf("%s: AnalyzeFunction's ReturnSink caught nothing — the return statement never ran", fnName)
	}
	returned = sink[0]
	for _, v := range sink[1:] {
		returned = abstractdomain.JoinKnown(returned, v)
	}
	return returned, diagnostics
}

// TestObjectLiteralMethodThisBinding_TheCalledWriteReadsBackExactly41
// is the row's own in-set leg, driven through the FULL call path:
// AnalyzeFunction walks literalWritingMethod, EvaluateCallExpression
// runs person.bump() through InlineContractBody's
// ObjectLiteralMethodWalkCall try, that call's own AnalyzeStatements
// walks `this.age = this.age + 1` with "this" bound to person, and
// the fold-back lands the exact written value (41) on person's own
// "age" key — asserted here as an EXACT scalar, not merely a silent
// diagnostic list, so a regression that leaves the value Unknown
// (still silent against Age's wide window) cannot hide.
func TestObjectLiteralMethodThisBinding_TheCalledWriteReadsBackExactly41(t *testing.T) {
	returned, diagnostics := objectLiteralMethodThisBindingRun(t, "literalWritingMethod")
	for _, d := range diagnostics {
		if d.Code == 7001 || d.Code == 7002 {
			t.Errorf("literalWritingMethod fired code %d: %s — person.bump()'s write (40 -> 41) was not read back exactly", d.Code, d.MessageText)
		}
	}
	if returned.Kind != abstractdomain.KindValues || len(returned.Values) != 1 || returned.Values[0] != 41 {
		spelled, _ := abstractdomain.FormatAbstractValue(returned)
		t.Errorf("person.age after person.bump() = %q, want the exact scalar 41", spelled)
	}
}

// TestObjectLiteralMethodThisBinding_TheOutOfSetSpoilStillReadsBack200
// is the marked twin, run through the same full call path: outlaw.age
// after outlaw.spoil() must read exactly 200, and — separately from
// this test's own exact-value assertion — the fixture's own
// @refinedts-expect-error line still owes a fire, pinned already by
// object_literal_method_call_ordering_test.go's
// TheOutOfSetSpoilStillFires. Pinned again here beside the new fix so
// a regression touching only the this-keyword read (never exercised
// by a constant literal write) cannot silently break this shape while
// the constant-write twin still passes.
func TestObjectLiteralMethodThisBinding_TheOutOfSetSpoilStillReadsBack200(t *testing.T) {
	returned, _ := objectLiteralMethodThisBindingRun(t, "literalSpoilMethod")
	if returned.Kind != abstractdomain.KindValues || len(returned.Values) != 1 || returned.Values[0] != 200 {
		spelled, _ := abstractdomain.FormatAbstractValue(returned)
		t.Errorf("outlaw.age after outlaw.spoil() = %q, want the exact scalar 200", spelled)
	}
}

// TestEnclosingThisObjectLiteralMethod_RecognizesTheMethodsOwnBody is
// the unit-level pin for the new dataflowfacts helper directly: `this`
// inside bump()'s own body resolves to the METHOD DECLARATION, the
// same node ObjectLiteralMethodWalkCall walks — never nil, and never
// the surrounding function's own FunctionDeclaration.
func TestEnclosingThisObjectLiteralMethod_RecognizesTheMethodsOwnBody(t *testing.T) {
	p := entryEnvTestProgram(t, objectLiteralMethodThisBindingSource)
	fn := entryEnvFunctionNamed(t, p, "literalWritingMethod")
	thisKeyword := superArrayFirstNode(t, fn.Body(), "this keyword", func(node *ast.Node) bool {
		return node.Kind == ast.KindThisKeyword
	})
	method := dataflowfacts.EnclosingThisObjectLiteralMethod(thisKeyword)
	if method == nil || !ast.IsMethodDeclaration(method) {
		t.Fatalf("EnclosingThisObjectLiteralMethod answered a non-method node")
	}
	name := method.Name()
	if name == nil || !ast.IsIdentifier(name) || name.Text() != "bump" {
		t.Errorf("EnclosingThisObjectLiteralMethod resolved a method other than bump")
	}
}

// TestEnclosingThisObjectLiteralMethod_NilOutsideAnyMethod is the
// negative control: a bare top-level `this` (the enclosing function
// declares no this-parameter, and there is no class) answers nil —
// the same silence.Residue() floor the existing this-keyword arm
// already served before this fix, kept for every shape besides the
// three recognized ones.
func TestEnclosingThisObjectLiteralMethod_NilOutsideAnyMethod(t *testing.T) {
	p := entryEnvTestProgram(t, "function f(): unknown {\n  return this;\n}\n")
	fn := entryEnvFunctionNamed(t, p, "f")
	thisKeyword := superArrayFirstNode(t, fn.Body(), "this keyword", func(node *ast.Node) bool {
		return node.Kind == ast.KindThisKeyword
	})
	if method := dataflowfacts.EnclosingThisObjectLiteralMethod(thisKeyword); method != nil {
		t.Errorf("EnclosingThisObjectLiteralMethod answered non-nil for a bare top-level this")
	}
}
