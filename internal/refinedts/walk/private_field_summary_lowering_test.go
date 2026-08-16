// Pins the private-field gap in the kernel-summary lowering
// (AGENT-BRIEF.md's syntax-wave facts, the Sealed/privateFieldThroughConstructor
// row from e-class-and-function.ts:140): a `this.#field` read inside a
// method carried NO slot at all in the summary route, even though the
// SAME field reads exactly through ConstructedInstance and ClassMethodWalkCall
// on the walk route.
//
// The mechanism: three readers recognized a property step's own name
// only when `ast.IsIdentifier(access.Name())` held — SpelledNameOf
// (tracked_bindings.go), propertyPathReading (ir_object_slots.go), and
// the field census's fieldAccessOf (ir_field_bundles_scan.go). A
// PrivateIdentifier name (`#age`) fails all three checks, so:
//
//   - the CENSUS (fieldAccessOf) never recognized `this.#age` as a
//     field read/write/call at all — the AST walk fell through to
//     visitBareMention, which marks the WHOLE bundle Escaped (every
//     other field of the class loses its slot too, not just #age);
//   - even where a slot existed, the STATEMENT lowering (SpelledNameOf,
//     propertyPathReading) could not resolve `this.#age` back to it,
//     so the read fell to the opaque-return havoc.
//
// A method whose only `this` reads are private fields therefore always
// lowered POROUS (Escaped) and applySummary correctly declined to
// serve it — sound, but it meant the kernel-loaded judge always paid
// the walk-route fallback for a private-field method, and any place
// that fallback disagreed with the summary route's own bookkeeping
// (the "may be undefined" row) inherited the divergence. The fix
// widens all three checks to accept ast.IsPrivateIdentifier alongside
// ast.IsIdentifier, mirroring fieldMemberName/thisFieldStoreNameOf
// (ir_field_bundles_capture_writes.go), which already did.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
)

// TestSpelledNameOf_APrivateFieldStepSpellsUnderTheHashName pins the
// read-side fix directly: `this.#age` must spell "this.#age", not
// decline — the same shape `this.age` already spelled.
func TestSpelledNameOf_APrivateFieldStepSpellsUnderTheHashName(t *testing.T) {
	declaration := bundleMethodOf(t, `
		class Sealed {
			#age: number;
			years(): number { return this.#age; }
		}
	`, "years")
	body := declaration.Body()
	if body == nil || !ast.IsBlock(body) || len(body.AsBlock().Statements.Nodes) == 0 {
		t.Fatalf("years() carries no block body")
	}
	returnStatement := body.AsBlock().Statements.Nodes[0]
	if !ast.IsReturnStatement(returnStatement) {
		t.Fatalf("years()'s first statement is not a return: %+v", returnStatement)
	}
	expression := returnStatement.AsReturnStatement().Expression
	spelled, ok := SpelledNameOf(expression)
	if !ok {
		t.Fatalf("SpelledNameOf(this.#age) declined — want \"this.#age\"")
	}
	if spelled != "this.#age" {
		t.Errorf("SpelledNameOf(this.#age) = %q, want \"this.#age\"", spelled)
	}
}

// TestThisBundleOf_APrivateFieldReadExpandsRatherThanEscaping pins the
// census-side fix: a method whose ONLY `this` mention is a private
// field read must expand the bundle with one entry for it, not mark
// the whole receiver Escaped (visitBareMention's fallback when
// fieldAccessOf declines).
func TestThisBundleOf_APrivateFieldReadExpandsRatherThanEscaping(t *testing.T) {
	declaration := bundleMethodOf(t, `
		class Sealed {
			#age: number;
			years(): number { return this.#age; }
		}
	`, "years")
	bundle := thisBundleOf(nil, declaration)
	if bundle.Escaped {
		t.Fatalf("Escaped = true, want false — this.#age is a declared field read, not a bare mention")
	}
	if !bundle.Expanded {
		t.Fatalf("Expanded = false, want true — #age is read and declared")
	}
	if len(bundle.Entries) != 1 || bundle.Entries[0].Name != "this.#age" {
		t.Fatalf("entries = %+v, want exactly one row named this.#age", bundle.Entries)
	}
	if bundle.Entries[0].Sort != BindingKindNumber {
		t.Errorf("entry sort = %q, want number — #age: number", bundle.Entries[0].Sort)
	}
}

// TestLowerSummaryBody_APrivateFieldReadMethodLowersComplete pins the
// end-to-end consequence: with the census and the statement lowering
// both recognizing the private field, years()'s body must lower
// COMPLETE (no havoc name), not porous — the state applySummary's own
// serving rule (kernel_summaries.go) gates on.
func TestLowerSummaryBody_APrivateFieldReadMethodLowersComplete(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	declaration := bundleMethodOf(t, `
		class Sealed {
			#age: number;
			years(): number { return this.#age; }
		}
	`, "years")
	summary, ok := RelowerSummaryBody(&FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration)
	if !ok {
		_, construct, _ := SummaryOutcomeOf(declaration)
		t.Fatalf("years() declined to lower at %q — want it to lower", construct)
	}
	outcome, construct, recorded := SummaryOutcomeOf(declaration)
	if !recorded {
		t.Fatalf("no outcome recorded for years()")
	}
	if outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q), want complete — the private field now carries a slot the statement lowering resolves", outcome, construct)
	}
	entry, has := bundleEntryNamed(summary, "this.#age")
	if !has {
		t.Fatalf("no bundle row for this.#age — BundleEntries = %+v", summary.BundleEntries)
	}
	if entry.Written {
		t.Errorf("this.#age Written = true, want false — years() only reads it")
	}
}

// TestApplySummary_APrivateFieldThroughConstructorServesTheExactWrite
// is the full end-to-end pin for the fixture row itself
// (e-class-and-function.ts:140, privateFieldThroughConstructor):
// `new Sealed(40).years()` must serve the exact constructor write
// through applySummary now that years()'s summary is complete, and
// the marked twin (200) must carry the out-of-set value through the
// same route — never a possibly-undefined wrapper, which is what the
// judge reported before this fix (the porous decline previously fell
// through to the walk route; this test pins the SUMMARY route's own
// answer now that it serves).
func TestApplySummary_APrivateFieldThroughConstructorServesTheExactWrite(t *testing.T) {
	kernel := superArrayLoadKernel(t)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	source := "class Sealed {\n" +
		"  #age: number;\n" +
		"  constructor(age: number) {\n" +
		"    this.#age = age;\n" +
		"  }\n" +
		"  years(): number {\n" +
		"    return this.#age;\n" +
		"  }\n" +
		"}\n" +
		"function good(): number { return new Sealed(40).years(); }\n" +
		"function over(): number { return new Sealed(200).years(); }\n"
	p := entryEnvTestProgram(t, source)
	ctx := superArrayContracts(t, p)
	ctx.Kernel = kernel

	yearsMethod := superArrayClassMethod(t, p, "Sealed", "years")

	forArg := func(age float64) abstractdomain.AbstractValue {
		receiver := abstractdomain.KnownObject(
			[]abstractdomain.ObjectKey{{Name: "#age", Value: abstractdomain.KnownValues([]float64{age}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)}},
			nil, false, abstractdomain.TrustProved, false)
		return receiver
	}

	goodAnswer, goodOk := KernelSummaryDirectOn(ctx, nil, &FunctionContract{Declaration: yearsMethod}, forArg(40))
	if !goodOk {
		_, construct, _ := SummaryOutcomeOf(yearsMethod)
		t.Fatalf("KernelSummaryDirectOn declined for the 40 receiver at %q — want it to serve", construct)
	}
	superArrayExactScalar(t, kernel, goodAnswer, 40, "Sealed(40).years() via applySummary")

	overAnswer, overOk := KernelSummaryDirectOn(ctx, nil, &FunctionContract{Declaration: yearsMethod}, forArg(200))
	if !overOk {
		t.Fatalf("KernelSummaryDirectOn declined for the 200 receiver — want it to serve")
	}
	superArrayExactScalar(t, kernel, overAnswer, 200, "Sealed(200).years() via applySummary")
}
