// Two `this`-knowledge lines the syntax-coverage fixture pins at
// b-body-expressions.ts:420 (literalWritingMethod) and :566
// (thisFieldRead's ThisPerson.years):
//
//   - `person.bump()` — bump's body writes `this.age = this.age + 1`
//     off the READ entry, not a literal constant. The call site's
//     bundleRetsAndArgs threading (ir_summary_call_receiver.go) must
//     carry the EXACT written value (41) back onto person.age's own
//     slot, the same way the spoil() constant-write twin already does.
//   - `return this.age` INSIDE ThisPerson.years()'s own body — the
//     class field invariant (InitialThisStateOf) must seed `this.age`
//     for the METHOD'S OWN body walk, not only for outside call sites,
//     so the return checks against Age without firing the undetermined
//     alert.
//
// Both marked twins (outlaw.spoil() writing 200, OverPerson.years()
// reading 200) must keep firing. Follows super_and_array_ctor_test.go's
// canonical program-from-source + kernel-load + exact-scalar recipe;
// skips (never a faked pass) when the native kernel dylib is absent.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
)

/* ── person.bump(): the exact written value rides back ──────────── */

const methodThisKnowledgeLiteralSource = "function literalWritingMethod(): number {\n" +
	"  const person = {\n" +
	"    age: 40,\n" +
	"    bump(): void {\n" +
	"      this.age = this.age + 1;\n" +
	"    },\n" +
	"  };\n" +
	"  person.bump();\n" +
	"  return person.age;\n" +
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

// methodThisKnowledgeLiteralRun drives fnName's WHOLE body through
// AnalyzeFunction so `const person = {...}` (or `outlaw`) and the
// `.bump()`/`.spoil()` call actually run before the return statement
// reads the entry back — evaluateExpression on a hand-picked return
// expression alone, against a fresh NewEnv(), never bound the object
// literal at all, which is a harness gap, not a walk-route one: the
// object literal's own declaration statement never ran.
func methodThisKnowledgeLiteralRun(t *testing.T, ctx *FlowContext, fnName string) abstractdomain.AbstractValue {
	t.Helper()
	fn := entryEnvFunctionNamed(t, ctx.P, fnName)
	var sink []abstractdomain.AbstractValue
	bodyCtx := *ctx
	bodyCtx.ReturnSink = &sink
	contract := &FunctionContract{Declaration: fn, Result: withReachStatedWindow(0, 1000), Grounded: true}
	AnalyzeFunction(&bodyCtx, contract, nil)
	if len(sink) == 0 {
		t.Fatalf("%s: AnalyzeFunction's ReturnSink caught nothing — the return statement never ran", fnName)
	}
	returned := sink[0]
	for _, v := range sink[1:] {
		returned = abstractdomain.JoinKnown(returned, v)
	}
	return returned
}

// TestMethodThisKnowledge_TheCalledWriteBumpsTheExactEntryValueBack is
// the read-and-write twin of the already-passing constant-write case:
// `bump` reads its own entry before writing it, so the ret the call
// site threads back must carry 41, not an unknown or havocked ret.
func TestMethodThisKnowledge_TheCalledWriteBumpsTheExactEntryValueBack(t *testing.T) {
	kernel := superArrayLoadKernel(t)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, methodThisKnowledgeLiteralSource)
	ctx := superArrayContracts(t, p)
	ctx.Kernel = kernel

	value := methodThisKnowledgeLiteralRun(t, ctx, "literalWritingMethod")
	superArrayExactScalar(t, kernel, value, 41, "person.age after person.bump()")
}

// TestMethodThisKnowledge_TheConstantWriteTwinStillLandsExactly pins
// the already-working case beside the new one, so a regression in the
// shared threading shows on both rows at once, not just the new one.
func TestMethodThisKnowledge_TheConstantWriteTwinStillLandsExactly(t *testing.T) {
	kernel := superArrayLoadKernel(t)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, methodThisKnowledgeLiteralSource)
	ctx := superArrayContracts(t, p)
	ctx.Kernel = kernel

	value := methodThisKnowledgeLiteralRun(t, ctx, "literalSpoilMethod")
	superArrayExactScalar(t, kernel, value, 200, "outlaw.age after outlaw.spoil()")
}

/* ── ThisPerson.years(): this.age seeds the method's OWN body walk ── */

const methodThisKnowledgeFieldReadSource = "class ThisPerson {\n" +
	"  age = 40;\n" +
	"  years(): number {\n" +
	"    return this.age;\n" +
	"  }\n" +
	"}\n" +
	"class OverPerson {\n" +
	"  age = 200;\n" +
	"  years(): number {\n" +
	"    return this.age;\n" +
	"  }\n" +
	"}\n"

// methodThisKnowledgeStatedWindow is the Age-shaped stated result the
// fixture checks `years()`'s return against: [0, 120], the zAge bound
// from b-body-expressions.ts.
func methodThisKnowledgeStatedWindow() *annotations.DeclaredRefinement {
	return withReachStatedWindow(0, 120)
}

// TestMethodThisKnowledge_TheOwnBodyWalkSeesTheSeededFieldNeverFires
// is the read half of the two lines: `return this.age` judged INSIDE
// years()'s own body (not at an outside call site) must not fire the
// undetermined alert, and must resolve `this.age` to the class field
// invariant's exact 40 — the seeding AnalyzeFunction's InitialThisStateOf
// call already performs at body entry (analyze_function.go).
func TestMethodThisKnowledge_TheOwnBodyWalkSeesTheSeededFieldNeverFires(t *testing.T) {
	kernel := superArrayLoadKernel(t)
	p := entryEnvTestProgram(t, methodThisKnowledgeFieldReadSource)
	ctx := superArrayContracts(t, p)
	ctx.Kernel = kernel
	years := superArrayClassMethod(t, p, "ThisPerson", "years")

	var diagnostics []assignability.RefinementDiagnostic
	bodyCtx := *ctx
	bodyCtx.Report = func(d assignability.RefinementDiagnostic) {
		diagnostics = append(diagnostics, d)
	}
	contract := &FunctionContract{
		Declaration: years,
		Result:      methodThisKnowledgeStatedWindow(),
		Grounded:    true,
	}
	AnalyzeFunction(&bodyCtx, contract, nil)

	for _, d := range diagnostics {
		if d.Code == 7001 || d.Code == 7002 {
			t.Errorf("ThisPerson.years()'s own body fired code %d: %s — this.age was not seeded from the field invariant", d.Code, d.MessageText)
		}
	}

	env := NewEnv()
	env.Set("this", *InitialThisStateOf(ctx, years))
	returned := superArrayFirstNode(t, years.Body(), "return statement", ast.IsReturnStatement)
	value := evaluateExpression(ctx, env, returned.AsReturnStatement().Expression)
	if value.Kind != abstractdomain.KindValues || len(value.Values) != 1 || value.Values[0] != 40 {
		spelled, _ := abstractdomain.FormatAbstractValue(value)
		t.Errorf("this.age inside years()'s own body = %q, want the exact field invariant 40", spelled)
	}
}

// TestMethodThisKnowledge_TheOverPersonTwinStillFires is the marked
// twin: OverPerson.age is 200, out of the [0,120] window, so the same
// own-body walk must still report the undetermined alert once seeding
// lands — the seeding must not silence a genuinely out-of-set field.
func TestMethodThisKnowledge_TheOverPersonTwinStillFires(t *testing.T) {
	p := entryEnvTestProgram(t, methodThisKnowledgeFieldReadSource)
	ctx := superArrayContracts(t, p)
	years := superArrayClassMethod(t, p, "OverPerson", "years")

	var diagnostics []assignability.RefinementDiagnostic
	bodyCtx := *ctx
	bodyCtx.Report = func(d assignability.RefinementDiagnostic) {
		diagnostics = append(diagnostics, d)
	}
	contract := &FunctionContract{
		Declaration: years,
		Result:      methodThisKnowledgeStatedWindow(),
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
		t.Errorf("OverPerson.years()'s own body did not fire for the out-of-set 200 — got %d diagnostics", len(diagnostics))
	}
}
