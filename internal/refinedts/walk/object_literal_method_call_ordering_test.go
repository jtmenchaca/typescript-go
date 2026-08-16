// The row this file pins: h-object-literal-members.ts:245
// (nonInertClosureWritingMember). `person.bump()` writes `this.age =
// this.age + 1` off the READ entry (40 -> 41, still inside Age's
// [0,120] window), then `const ok: Age = person.age;` reads the
// result — an IN-SET line that must stay silent.
//
// The bug this pins: InlineContractBody tried the kernel-summary-
// direct route BEFORE the object-literal walk-route fix
// (ObjectLiteralMethodWalkCall). The kernel-summary route serves a
// receiver-writing method call by calling ForgetThrough on the WHOLE
// receiver afterward (SummaryReceiverEffects' "served-call forget") —
// correct for a class instance, whose `this.key` reads answer through
// the class's standing field invariant regardless of the forget, but
// wrong for an object literal, where `person.age` reads the tracked
// KindObject's OWN key and forgetting it destroys the only place the
// exact written value (41) lived. The forgotten `person` then read
// through the RESOLVED TYPE fallback (a bare `number`), producing
// "number, or NaN" — refuted against Age, firing 7001 on an in-set
// line. The fix moves ObjectLiteralMethodWalkCall's own try ahead of
// the kernel-summary route in InlineContractBody, so this call folds
// the write back onto `person` instead of forgetting it.
//
// Runs the WHOLE function body through AnalyzeFunction (not a bare
// evaluateExpression of the return alone) so `person.bump()` actually
// executes before the return is judged — a return-only read would
// never exercise the call-ordering bug this row is about. Skips
// (never a faked pass) when the native kernel dylib is absent, per
// super_and_array_ctor_test.go's canonical recipe.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
)

const objectLiteralMethodOrderingSource = "function nonInertClosureWritingMember(): number {\n" +
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
	"function nonInertClosureWritingMemberOverLeg(): number {\n" +
	"  const outlaw = {\n" +
	"    age: 40,\n" +
	"    spoil(): void {\n" +
	"      this.age = 200;\n" +
	"    },\n" +
	"  };\n" +
	"  outlaw.spoil();\n" +
	"  return outlaw.age;\n" +
	"}\n"

// TestObjectLiteralMethodCallOrdering_TheInSetBumpStaysSilent is the
// row's own in-set leg: person.age after person.bump() is exactly 41,
// inside Age's [0,120] window, so judging it against that window must
// report nothing.
func TestObjectLiteralMethodCallOrdering_TheInSetBumpStaysSilent(t *testing.T) {
	kernel := superArrayLoadKernel(t)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, objectLiteralMethodOrderingSource)
	ctx := superArrayContracts(t, p)
	ctx.Kernel = kernel

	fn := entryEnvFunctionNamed(t, p, "nonInertClosureWritingMember")
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

	for _, d := range diagnostics {
		if d.Code == 7001 || d.Code == 7002 {
			t.Errorf("nonInertClosureWritingMember fired code %d: %s — person.bump()'s write (40 -> 41) was forgotten instead of threaded back onto person.age", d.Code, d.MessageText)
		}
	}
}

// TestObjectLiteralMethodCallOrdering_TheOutOfSetSpoilStillFires is the
// marked twin already fixed by the constant-write path
// (method_this_knowledge_test.go's TheConstantWriteTwinStillLandsExactly);
// pinned again here beside the ordering fix so a regression that only
// shows on the READ-then-write shape (bump) cannot silently also break
// the WRITE-only shape (spoil) without a test noticing on this file too.
func TestObjectLiteralMethodCallOrdering_TheOutOfSetSpoilStillFires(t *testing.T) {
	kernel := superArrayLoadKernel(t)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, objectLiteralMethodOrderingSource)
	ctx := superArrayContracts(t, p)
	ctx.Kernel = kernel

	fn := entryEnvFunctionNamed(t, p, "nonInertClosureWritingMemberOverLeg")
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
		t.Errorf("nonInertClosureWritingMemberOverLeg did not fire for the out-of-set 200 written by outlaw.spoil()")
	}
}
