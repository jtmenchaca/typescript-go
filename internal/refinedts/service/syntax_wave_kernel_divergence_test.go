// Reproduction harness for the KERNEL-LOADED DIVERGENCE cluster
// (AGENT-BRIEF.md's "syntax-wave facts"): five fixture rows the judge
// binary — which loads the kernel dylib and runs Check's full
// service pipeline — answers wrongly, while an isolated walk-package
// test (no full-file contract collection, no walkContractBodies
// scheduling) answers right. Each test here runs Check() itself,
// the same door CheckFile/CheckFiles take, over the SAME multi-
// function file shape the judge sees — so a divergence that only
// shows up once every sibling row's contract is registered
// reproduces here and not in a single-function walk test.
//
// Skipped without the native kernel dylib — these rows are ABOUT the
// kernel-loaded pipeline, so there is nothing to reproduce without it.

package service

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

func requireDylib(t *testing.T) {
	t.Helper()
	if !kernelbridge.KernelArtifactsPresent(kernelbridge.DylibPath) {
		t.Skip("native kernel dylib not built")
	}
}

// TestKernelDivergence_ArraySort_MutatedLocalStillReadsExact reproduces
// row 1: `const ages = [41, 40]; ages.sort(); const good: Age =
// ages[0];` inside a function among several — arraySort's own
// sibling functions register alongside it (KernelSummaryDirect tries
// summarizing arraySort ITSELF, since it is a contracted function in
// the file's contracts map), which the isolated walk test never
// exercises. The walk route (readArraySortReverseMethods) is pinned
// answering [40, 41] exactly.
//
// arraySort's own IN-SET leg (`ages` = [41, 40], sorted read as exactly
// 40) must stay silent; arraySortOver's OUT-OF-SET leg (`overs` =
// [201, 200], sorted read as exactly 200, outside Age's 0..120) now
// that the sort reader works correctly fires its own honest 7001 — the
// reader answering the exact sorted array is what MAKES that leg
// determine a value at all, and 200 genuinely sits outside the
// declared set. Both legs are checked position-anchored (Start offset
// inside each leg's own substring span), the same idiom
// array_sort_bisect_test.go's assertSilentOnSubstring uses, so a
// blanket "no diagnostics at all" assertion — written when the sort
// reader was still broken and neither leg determined anything — does
// not overclaim once the reader is fixed and the marked leg correctly
// speaks.
func TestKernelDivergence_ArraySort_MutatedLocalStillReadsExact(t *testing.T) {
	requireDylib(t)
	source := "import * as z from \"/surface/z.ts\";\n" +
		"const zAge = z.number().int().min(0).max(120);\n" +
		"type Age = z.infer<typeof zAge>;\n" +
		"function arraySort(): Age {\n" +
		"  const ages = [41, 40];\n" +
		"  ages.sort();\n" +
		"  const good: Age = ages[0];\n" +
		"  return good;\n" +
		"}\n" +
		"function arraySortOver(): Age {\n" +
		"  const overs = [201, 200];\n" +
		"  overs.sort();\n" +
		"  return overs[0];\n" +
		"}\n" +
		"void arraySort;\n" +
		"void arraySortOver;\n"
	result := checkFor(t, source)
	assertSilentOnSubstring(t, source, result, "ages.sort();\n  const good: Age = ages[0];",
		"arraySort's own in-set leg (ages[0])")
	overStart, overEnd := byteRange(source, "overs.sort();\n  return overs[0];")
	assertFiresOnSubstring(t, result, "arraySortOver's own out-of-set leg (overs[0])", 7001,
		overStart, overEnd)
}

// TestKernelDivergence_OverloadedCall_ImplementationAnswersOneArg
// reproduces row 2: pickYears carries two overload signatures ahead
// of its implementation, and overloadedCall's own good leg
// (pickYears(40)) must determine 40 through the implementation — the
// walk-package test (call_shape_contracts_test.go) already pins this
// in a single-function, single-contract harness. This test instead
// runs the WHOLE multi-function file through Check(), the door the
// judge takes, to see whether the extra sibling contracts (or the
// KernelSummaryDirect route trying pickYears' OWN lowering before the
// walk-based overload resolution) changes the answer.
func TestKernelDivergence_OverloadedCall_ImplementationAnswersOneArg(t *testing.T) {
	requireDylib(t)
	source := "import * as z from \"/surface/z.ts\";\n" +
		"const zAge = z.number().int().min(0).max(120);\n" +
		"type Age = z.infer<typeof zAge>;\n" +
		"function pickYears(age: number): number;\n" +
		"function pickYears(age: number, extra: number): number;\n" +
		"function pickYears(age: number, extra?: number): number {\n" +
		"  return age + (extra ?? 0);\n" +
		"}\n" +
		"function overloadedCall(): Age {\n" +
		"  const good: Age = pickYears(40);\n" +
		"  return good;\n" +
		"}\n" +
		"void overloadedCall;\n"
	result := checkFor(t, source)
	for _, d := range result.Refinements {
		t.Errorf("overloadedCall's good leg (pickYears(40)) reported %+v — want silence, the implementation's own body determines 40", d)
	}
}

// TestKernelDivergence_RestParameter_ExactCallSiteFillsTheTail
// reproduces row 3: firstAge(...ages: number[]) returning ages[0],
// called as firstAge(40, 41) — the exact-gated decline
// (declarationHasRestParameter) exists specifically so a TOP-fed rest
// entry does not serve over this call's own exact tail. This test
// runs the full multi-function file through Check() to see whether
// the EXACT flag actually reaches applySummary's decline check on
// this route, or whether some other served route (a composed call
// inside a DIFFERENT sibling's own lowering, or the plain
// KernelSummaryDirectOn without Exact) answers first.
func TestKernelDivergence_RestParameter_ExactCallSiteFillsTheTail(t *testing.T) {
	requireDylib(t)
	source := "import * as z from \"/surface/z.ts\";\n" +
		"const zAge = z.number().int().min(0).max(120);\n" +
		"type Age = z.infer<typeof zAge>;\n" +
		"function firstAge(...ages: number[]): number {\n" +
		"  return ages[0];\n" +
		"}\n" +
		"function restParameter(): Age {\n" +
		"  const good: Age = firstAge(40, 41);\n" +
		"  return good;\n" +
		"}\n" +
		"void restParameter;\n"
	result := checkFor(t, source)
	for _, d := range result.Refinements {
		t.Errorf("restParameter's good leg (firstAge(40, 41)) reported %+v — want silence, the exact call site's own tail determines 40", d)
	}
}

// TestKernelDivergence_ThisFieldRead_ClassFieldInvariantAnswers
// reproduces row 4: ThisPerson.age is a plain field initializer (40),
// years() returns this.age directly — the field-invariant machinery
// is pinned working kernel-less; this checks whether the served
// class-method summary route (applySummary/KernelSummaryDirectOn, now
// that the dylib is loaded) answers differently than the field
// invariant read.
func TestKernelDivergence_ThisFieldRead_ClassFieldInvariantAnswers(t *testing.T) {
	requireDylib(t)
	source := "import * as z from \"/surface/z.ts\";\n" +
		"const zAge = z.number().int().min(0).max(120);\n" +
		"type Age = z.infer<typeof zAge>;\n" +
		"class ThisPerson {\n" +
		"  age = 40;\n" +
		"  years(): Age {\n" +
		"    return this.age;\n" +
		"  }\n" +
		"}\n" +
		"function thisFieldRead(): Age {\n" +
		"  const ok: Age = new ThisPerson().years();\n" +
		"  return ok;\n" +
		"}\n" +
		"void thisFieldRead;\n"
	result := checkFor(t, source)
	for _, d := range result.Refinements {
		t.Errorf("thisFieldRead's good leg (new ThisPerson().years()) reported %+v — want silence, the field's own initializer determines 40", d)
	}
}

// TestKernelDivergence_PrivateAccessorRead_GetterOverBackingField
// reproduces row 5: a private `get #age()` accessor over a `#raw`
// backing field, read through `read()`. ConstructedInstance's own
// getter-key pass is pinned working kernel-less; this checks whether
// the served summary route for `read()` (a lowered body that composes
// a call to the accessor) diverges once the dylib is loaded.
func TestKernelDivergence_PrivateAccessorRead_GetterOverBackingField(t *testing.T) {
	requireDylib(t)
	source := "import * as z from \"/surface/z.ts\";\n" +
		"const zAge = z.number().int().min(0).max(120);\n" +
		"type Age = z.infer<typeof zAge>;\n" +
		"class PrivateAccessorHolder {\n" +
		"  #raw = 40;\n" +
		"  get #age(): number {\n" +
		"    return this.#raw;\n" +
		"  }\n" +
		"  read(): number {\n" +
		"    return this.#age;\n" +
		"  }\n" +
		"}\n" +
		"function privateAccessorSlotLaidOut(): Age {\n" +
		"  const good: Age = new PrivateAccessorHolder().read();\n" +
		"  return good;\n" +
		"}\n" +
		"void privateAccessorSlotLaidOut;\n"
	result := checkFor(t, source)
	for _, d := range result.Refinements {
		t.Errorf("privateAccessorSlotLaidOut's good leg reported %+v — want silence, the getter's own backing field determines 40", d)
	}
}

// TestKernelDivergence_ThisFieldRead_ADeleteOnAnUnrelatedTypeDoesNotVetoTheSeal
// reproduces row 1's TRUE trigger — the piece
// TestKernelDivergence_ThisFieldRead_ClassFieldInvariantAnswers above
// does not exercise: b-body-expressions.ts's real 7002 fires only once
// the file ALSO carries deleteExpression's `delete person.age` ahead of
// ThisPerson, on a `{ age?: number }` local with no relation to
// ThisPerson at all. The public-field seal's OutsideWritten set used to
// veto by FIELD NAME alone (walk/class_public_field_seal.go's
// nonThisWrittenFieldNames), so that unrelated delete stripped
// ThisPerson's own `age` invariant class-wide — years()'s OWN body walk
// (not the outside call site) then seeded `this.age` as opaque and fired
// 7002 at `return this.age;`, exactly where AGENT-BRIEF.md's brief
// pins the column. Fixed by reading each write's own receiver TYPE and
// vetoing only a class the receiver's type could actually name.
func TestKernelDivergence_ThisFieldRead_ADeleteOnAnUnrelatedTypeDoesNotVetoTheSeal(t *testing.T) {
	requireDylib(t)
	source := "import * as z from \"/surface/z.ts\";\n" +
		"const zAge = z.number().int().min(0).max(120);\n" +
		"type Age = z.infer<typeof zAge>;\n" +
		"function deleteExpression(): void {\n" +
		"  const person: { age?: number } = { age: 40 };\n" +
		"  delete person.age;\n" +
		"}\n" +
		"class ThisPerson {\n" +
		"  age = 40;\n" +
		"  years(): Age {\n" +
		"    return this.age;\n" +
		"  }\n" +
		"}\n" +
		"function thisFieldRead(): Age {\n" +
		"  const ok: Age = new ThisPerson().years();\n" +
		"  return ok;\n" +
		"}\n" +
		"void deleteExpression;\n" +
		"void thisFieldRead;\n"
	result := checkFor(t, source)
	for _, d := range result.Refinements {
		t.Errorf("years()'s own body (return this.age) reported %+v — want silence, an unrelated delete on a disjoint type must not strip ThisPerson's own field invariant", d)
	}
}

// TestKernelDivergence_PrivateFieldThroughConstructor_YearsAnswersExactly
// reproduces the sixth row (e-class-and-function.ts:140,
// privateFieldThroughConstructor): a class whose ONLY field is
// PRIVATE (`#age`), written once in the constructor and read back
// through a plain method (`years()`). AGENT-BRIEF.md's syntax-wave
// facts trace this to the summary-lowering route: SpelledNameOf
// (tracked_bindings.go), propertyPathReading (ir_object_slots.go),
// and the field census's fieldAccessOf (ir_field_bundles_scan.go)
// all recognized a property step's name only when it was a plain
// IDENTIFIER — a PrivateIdentifier (`#age`) failed every one of them,
// so the census read `this.#age` as a bare, unrecognized mention of
// `this` and marked the WHOLE bundle Escaped, and the statement
// lowering could not resolve the read even where a slot existed. The
// summary route therefore always lowered years() POROUS and
// applySummary correctly declined — but the walk route's own answer
// (ConstructedInstance's exact write) never actually reached this
// call at the SUMMARY layer, and the two routes' bookkeeping is what
// this test checks end to end.
func TestKernelDivergence_PrivateFieldThroughConstructor_YearsAnswersExactly(t *testing.T) {
	requireDylib(t)
	source := "import * as z from \"/surface/z.ts\";\n" +
		"const zAge = z.number().int().min(0).max(120);\n" +
		"type Age = z.infer<typeof zAge>;\n" +
		"class Sealed {\n" +
		"  #age: number;\n" +
		"  constructor(age: number) {\n" +
		"    this.#age = age;\n" +
		"  }\n" +
		"  years(): number {\n" +
		"    return this.#age;\n" +
		"  }\n" +
		"}\n" +
		"function privateFieldThroughConstructor(): Age {\n" +
		"  const good: Age = new Sealed(40).years();\n" +
		"  return good;\n" +
		"}\n" +
		"void privateFieldThroughConstructor;\n"
	result := checkFor(t, source)
	for _, d := range result.Refinements {
		t.Errorf("privateFieldThroughConstructor's good leg (new Sealed(40).years()) reported %+v — want silence, the constructor's own write determines 40", d)
	}
}

// TestKernelDivergence_PrivateFieldReader_BothLegs is the
// b-body-expressions.ts:222 propertyPrivate row, reproduced through
// Check() the way the judge runs it: TWO local classes, each
// declared INSIDE the function (not at module scope, unlike the
// PrivateHolder pair below) — Person answers 40 through its own
// #age initializer, OverPerson answers 200 through the identical
// shape. Both must resolve through the SAME mechanism this file's
// header names (SummaryResultIn's unknownReceiver() TOP-fill, fixed
// by marking a `this` read non-self-contained in scanBody,
// function_summaries.go) — a regression check: this row was silent
// two sweeps ago (served TOP, "number, or NaN", not a determination
// at all) rather than merely wrong-valued.
func TestKernelDivergence_PrivateFieldReader_BothLegs(t *testing.T) {
	requireDylib(t)
	source := "import * as z from \"/surface/z.ts\";\n" +
		"const zAge = z.number().int().min(0).max(120);\n" +
		"type Age = z.infer<typeof zAge>;\n" +
		"function propertyPrivate(): Age {\n" +
		"  class Person {\n" +
		"    #age = 40;\n" +
		"    years(): number {\n" +
		"      return this.#age;\n" +
		"    }\n" +
		"  }\n" +
		"  const ok: Age = new Person().years();\n" +
		"  class OverPerson {\n" +
		"    #age = 200;\n" +
		"    years(): number {\n" +
		"      return this.#age;\n" +
		"    }\n" +
		"  }\n" +
		"  return new OverPerson().years();\n" +
		"}\n" +
		"void propertyPrivate;\n"
	result := checkFor(t, source)
	requireExactlyOneRTS7001(t, result, "propertyPrivate's over leg (new OverPerson().years())")
}

// TestKernelDivergence_PrivateFieldSlotLaidOut_BothLegs is the
// i-more-expressions.ts:105 privateFieldSlotLaidOut row: the SAME
// shape as propertyPrivate above, but PrivateHolder/OverPrivateHolder
// are declared at MODULE scope rather than locally inside the
// function — a regression that was silent two sweeps ago, same root
// cause, checked here so the local-class and module-class shapes
// both have a service-level pin.
func TestKernelDivergence_PrivateFieldSlotLaidOut_BothLegs(t *testing.T) {
	requireDylib(t)
	source := "import * as z from \"/surface/z.ts\";\n" +
		"const zAge = z.number().int().min(0).max(120);\n" +
		"type Age = z.infer<typeof zAge>;\n" +
		"class PrivateHolder {\n" +
		"  #age = 40;\n" +
		"  read(): number {\n" +
		"    return this.#age;\n" +
		"  }\n" +
		"}\n" +
		"class OverPrivateHolder {\n" +
		"  #age = 200;\n" +
		"  read(): number {\n" +
		"    return this.#age;\n" +
		"  }\n" +
		"}\n" +
		"function privateFieldSlotLaidOut(): Age {\n" +
		"  const good: Age = new PrivateHolder().read();\n" +
		"  void good;\n" +
		"  return new OverPrivateHolder().read();\n" +
		"}\n" +
		"void privateFieldSlotLaidOut;\n"
	result := checkFor(t, source)
	requireExactlyOneRTS7001(t, result, "privateFieldSlotLaidOut's over leg (new OverPrivateHolder().read())")
}

// TestKernelDivergence_PrivateFieldThroughConstructor_OverLegFires
// extends TestKernelDivergence_PrivateFieldThroughConstructor_YearsAnswersExactly
// above (which only checks the good leg is silent) with the marked
// twin: `new Sealed(200).years()` must fire RTS7001 — AGENT-BRIEF.md
// records this row as answering "may be 'undefined'" a sweep ago (a
// wrapper, not a bare determination) before landing at "number, or
// NaN" — this pin is what tells the two apart, since both would
// otherwise pass a silence-only check on the good leg alone.
func TestKernelDivergence_PrivateFieldThroughConstructor_OverLegFires(t *testing.T) {
	requireDylib(t)
	source := "import * as z from \"/surface/z.ts\";\n" +
		"const zAge = z.number().int().min(0).max(120);\n" +
		"type Age = z.infer<typeof zAge>;\n" +
		"class Sealed {\n" +
		"  #age: number;\n" +
		"  constructor(age: number) {\n" +
		"    this.#age = age;\n" +
		"  }\n" +
		"  years(): number {\n" +
		"    return this.#age;\n" +
		"  }\n" +
		"}\n" +
		"function privateFieldThroughConstructor(): Age {\n" +
		"  const good: Age = new Sealed(40).years();\n" +
		"  void good;\n" +
		"  return new Sealed(200).years();\n" +
		"}\n" +
		"void privateFieldThroughConstructor;\n"
	result := checkFor(t, source)
	requireExactlyOneRTS7001(t, result, "privateFieldThroughConstructor's over leg (new Sealed(200).years())")
}

// TestKernelDivergence_PrivateAccessorRead_OverLegFires extends
// TestKernelDivergence_PrivateAccessorRead_GetterOverBackingField
// above (good leg only) with the marked twin: a private `get #age()`
// accessor's out-of-set backing value must still fire RTS7002 (the
// accessor route serves through a bundle entry with no declared field
// slot — a shape distinct from a plain PropertyDeclaration field, and
// AGENT-BRIEF.md records this row unchanged for three sweeps).
func TestKernelDivergence_PrivateAccessorRead_OverLegFires(t *testing.T) {
	requireDylib(t)
	source := "import * as z from \"/surface/z.ts\";\n" +
		"const zAge = z.number().int().min(0).max(120);\n" +
		"type Age = z.infer<typeof zAge>;\n" +
		"class PrivateAccessorHolder {\n" +
		"  #raw = 40;\n" +
		"  get #age(): number {\n" +
		"    return this.#raw;\n" +
		"  }\n" +
		"  read(): number {\n" +
		"    return this.#age;\n" +
		"  }\n" +
		"}\n" +
		"class OverPrivateAccessorHolder {\n" +
		"  #raw = 200;\n" +
		"  get #age(): number {\n" +
		"    return this.#raw;\n" +
		"  }\n" +
		"  read(): number {\n" +
		"    return this.#age;\n" +
		"  }\n" +
		"}\n" +
		"function privateAccessorSlotLaidOut(): Age {\n" +
		"  const good: Age = new PrivateAccessorHolder().read();\n" +
		"  void good;\n" +
		"  return new OverPrivateAccessorHolder().read();\n" +
		"}\n" +
		"void privateAccessorSlotLaidOut;\n"
	result := checkFor(t, source)
	requireAtLeastOneDetermination(t, result, "privateAccessorSlotLaidOut's over leg (new OverPrivateAccessorHolder().read())")
}

// TestKernelDivergence_ObjectLiteralMethod_BumpStaysSilentThroughCheck
// is h-object-literal-members.ts:245 (nonInertClosureWritingMember) run
// through Check() itself — the door the judge takes — rather than the
// isolated walk-package test
// (object_literal_method_call_ordering_test.go's
// TestObjectLiteralMethodCallOrdering_TheInSetBumpStaysSilent, which
// calls CompileContractFileFacts/AnalyzeFunction directly on a single
// function with no sibling contracts, no walkContractBodies scheduling,
// and no programFactsCached wrapping). AGENT-BRIEF.md's method_this_writes
// entry records the fix (ObjectLiteralMethodWalkCall tried before the
// kernel-summary route in InlineContractBody) but the entry itself
// names this door untested — this closes that gap: person.bump()
// writes this.age (40 -> 41) and the in-set read afterward must stay
// silent under the FULL service pipeline, not just the walk package's
// own harness.
func TestKernelDivergence_ObjectLiteralMethod_BumpStaysSilentThroughCheck(t *testing.T) {
	requireDylib(t)
	source := "import * as z from \"/surface/z.ts\";\n" +
		"const zAge = z.number().int().min(0).max(120);\n" +
		"type Age = z.infer<typeof zAge>;\n" +
		"function nonInertClosureWritingMember(): Age {\n" +
		"  const person = {\n" +
		"    age: 40,\n" +
		"    bump(): void {\n" +
		"      this.age = this.age + 1;\n" +
		"    },\n" +
		"  };\n" +
		"  person.bump();\n" +
		"  const ok: Age = person.age;\n" +
		"  return ok;\n" +
		"}\n" +
		"void nonInertClosureWritingMember;\n"
	result := checkFor(t, source)
	for _, d := range result.Refinements {
		t.Errorf("nonInertClosureWritingMember's in-set leg (person.age after person.bump()) reported %+v — want silence, person.bump()'s write (40 -> 41) is inside Age's [0,120] window", d)
	}
}

// TestKernelDivergence_ObjectLiteralMethod_SpoilFiresThroughCheck is the
// marked twin, same door: outlaw.spoil() writes this.age = 200, out of
// Age's [0,120] window, and the @refinedts-expect-error line
// (h-object-literal-members.ts:255) owes exactly one RTS7001 through
// the full Check() pipeline.
func TestKernelDivergence_ObjectLiteralMethod_SpoilFiresThroughCheck(t *testing.T) {
	requireDylib(t)
	source := "import * as z from \"/surface/z.ts\";\n" +
		"const zAge = z.number().int().min(0).max(120);\n" +
		"type Age = z.infer<typeof zAge>;\n" +
		"function nonInertClosureWritingMemberOverLeg(): Age {\n" +
		"  const outlaw = {\n" +
		"    age: 40,\n" +
		"    spoil(): void {\n" +
		"      this.age = 200;\n" +
		"    },\n" +
		"  };\n" +
		"  outlaw.spoil();\n" +
		"  return outlaw.age;\n" +
		"}\n" +
		"void nonInertClosureWritingMemberOverLeg;\n"
	result := checkFor(t, source)
	requireExactlyOneRTS7001(t, result, "nonInertClosureWritingMemberOverLeg's over leg (outlaw.age after outlaw.spoil())")
}

// requireExactlyOneRTS7001 asserts the check answered exactly one
// diagnostic and it is RTS7001 — the "determines a value, and the
// value is out of set" verdict a marked @refinedts-expect-error line
// owes.
func requireExactlyOneRTS7001(t *testing.T, result CheckResult, label string) {
	t.Helper()
	if len(result.Refinements) != 1 {
		t.Fatalf("%s: Refinements = %+v, want exactly one diagnostic", label, result.Refinements)
	}
	if result.Refinements[0].Code != 7001 {
		t.Errorf("%s: Code = %d, want 7001", label, result.Refinements[0].Code)
	}
}

// requireAtLeastOneDetermination asserts the check fired SOME
// refinement diagnostic — used where the accessor route may answer
// through a differently-shaped verdict (7001 exact, or 7002's
// possibly-undefined wrapper) but silence on the marked line is
// always wrong.
func requireAtLeastOneDetermination(t *testing.T, result CheckResult, label string) {
	t.Helper()
	if len(result.Refinements) == 0 {
		t.Fatalf("%s: Refinements = %+v, want at least one diagnostic — the marked line must fire", label, result.Refinements)
	}
}

// checkFor runs the exact service door the judge runs (Check, which
// calls setupKernel() and the full runRefinements pipeline) over one
// in-memory source file.
func checkFor(t *testing.T, source string) CheckResult {
	t.Helper()
	result, err := Check(source, testSurfaceDir)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	return result
}
