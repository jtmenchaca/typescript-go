// Pins four i-more-expressions.ts rows the syntax-coverage judge fired
// wrongly on: an array-typed parameter, a defaulted record parameter,
// an array-binding-pattern parameter, and a rest identifier parameter —
// each a plain-typed call whose in-set argument must read back exactly,
// not the wide "number, or NaN" a TOP-fed kernel-summary entry answers.
//
// Reuses super_and_array_ctor_test.go's harness (superArrayContracts,
// superArrayLoadKernel, superArrayCallIn, superArrayExactScalar) — same
// package, same program-from-source recipe, so a passing row here and a
// passing row there cannot silently disagree about how a call resolves.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
)

/* ── the array-typed parameter's kernel-summary TOP fix ──────────── */

// arrayParameterFixtureSource mirrors arrayTypedParameter /
// arrayTypedParameterRefines: an identifier parameter typed number[],
// read by index — the shape summaryEntryStates fills TOP for both its
// "ages.len"/"ages.elem" entries, discarding an exact caller argument
// unless EXACT tells applySummary to decline that one serving.
const arrayParameterFixtureSource = "function arrayTypedParameter(ages: number[]): number {\n" +
	"  return ages[0];\n" +
	"}\n" +
	"function good(): number { return arrayTypedParameter([40, 41]); }\n" +
	"function over(): number { return arrayTypedParameter([200, 201]); }\n"

func TestApplySummary_AnArrayTypedParameterExactCallReadsThroughToTheArgument(t *testing.T) {
	kernel := superArrayLoadKernel(t)
	ClearResolvedRecordMembers()
	ClearResolvedArrayParameters()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, arrayParameterFixtureSource)
	ctx := superArrayContracts(t, p)
	ctx.Kernel = kernel

	// the unmarked fixture row: ages[0] on [40, 41] answers exactly 40,
	// not the wide "number, or NaN" a TOP-fed kernel entry would leave
	goodCall := superArrayCallIn(t, p, "good")
	goodValue := evaluateExpression(ctx, NewEnv(), goodCall)
	superArrayExactScalar(t, kernel, goodValue, 40, "arrayTypedParameter([40, 41])")

	// the marked twin: ages[0] on [200, 201] answers exactly 200 — out
	// of Age, so the expect-error row still fires, now on the true value
	overCall := superArrayCallIn(t, p, "over")
	overValue := evaluateExpression(ctx, NewEnv(), overCall)
	superArrayExactScalar(t, kernel, overValue, 200, "arrayTypedParameter([200, 201])")
}

func TestDeclarationHasArrayParameter_TrueForAFlattenedArrayParameterFalseOtherwise(t *testing.T) {
	kernel := superArrayLoadKernel(t)
	ClearResolvedArrayParameters()
	p := entryEnvTestProgram(t, arrayParameterFixtureSource)
	ctx := superArrayContracts(t, p)
	ctx.Kernel = kernel
	arrayFn := entryEnvFunctionNamed(t, p, "arrayTypedParameter")
	if !declarationHasArrayParameter(ctx, arrayFn) {
		t.Errorf("declarationHasArrayParameter = false for arrayTypedParameter(ages: number[]), want true")
	}
	plainFn := entryEnvFunctionNamed(t, p, "good")
	if declarationHasArrayParameter(ctx, plainFn) {
		t.Errorf("declarationHasArrayParameter = true for good(), which takes no parameters at all")
	}
}

/* ── the defaulted record parameter's missing default fix ────────── */

// defaultedRecordFixtureSource mirrors defaultedRecordParameter /
// defaultedRecordParameterRefines: a record-typed parameter whose
// default is an object literal — recordParamMembersIn refuses that
// shape outright (a defaulted record needs the default applied
// member-wise, which no kernel route spells), so the kernel lowering
// declines the whole body and the walk-based recovery is the only
// route left; ParameterKnown alone never evaluates a parameter's own
// default, so the recovery needs ParameterArgumentOrDefault to reach
// the exact 18.
const defaultedRecordFixtureSource = "function defaultedRecordParameter(person: { age: number } = { age: 18 }): number {\n" +
	"  return person.age;\n" +
	"}\n" +
	"function good(): number { return defaultedRecordParameter(); }\n" +
	"function over(): number { return defaultedRecordParameter({ age: 200 }); }\n"

func TestRecoverPure_ADefaultedRecordParameterMissingArgumentTakesTheDefault(t *testing.T) {
	kernel := superArrayLoadKernel(t)
	ClearResolvedRecordMembers()
	ClearResolvedArrayParameters()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, defaultedRecordFixtureSource)
	ctx := superArrayContracts(t, p)
	ctx.Kernel = kernel

	// the unmarked fixture row: defaultedRecordParameter() with NO
	// argument reads person.age off the DEFAULT {age: 18} — exactly 18,
	// not an unbound or opaque "number, or NaN"
	goodCall := superArrayCallIn(t, p, "good")
	goodValue := evaluateExpression(ctx, NewEnv(), goodCall)
	superArrayExactScalar(t, kernel, goodValue, 18, "defaultedRecordParameter()")

	// the marked twin: an explicit argument ignores the default entirely
	overCall := superArrayCallIn(t, p, "over")
	overValue := evaluateExpression(ctx, NewEnv(), overCall)
	superArrayExactScalar(t, kernel, overValue, 200, "defaultedRecordParameter({ age: 200 })")
}

func TestParameterArgumentOrDefault_EvaluatesTheDefaultOnlyWhenTheArgumentIsGenuinelyMissing(t *testing.T) {
	p := entryEnvTestProgram(t, defaultedRecordFixtureSource)
	ctx := superArrayContracts(t, p)
	fn := entryEnvFunctionNamed(t, p, "defaultedRecordParameter")
	parameter := fn.AsFunctionDeclaration().Parameters.Nodes[0]

	// no argument at all: the default {age: 18} runs
	missing := ParameterArgumentOrDefault(ctx, parameter, 0, EffectiveArguments{Exact: true})
	if missing.Kind != abstractdomain.KindObject {
		t.Fatalf("ParameterArgumentOrDefault (missing) Kind = %v, want KindObject from the default literal", missing.Kind)
	}

	// an unread spread ahead of this position: the call MAY still be
	// sending it, so the default must not run — ParameterKnown's own
	// residue stands
	inexact := ParameterArgumentOrDefault(ctx, parameter, 0, EffectiveArguments{Exact: false})
	if inexact.Kind == abstractdomain.KindObject {
		t.Errorf("ParameterArgumentOrDefault ran the default under an inexact (spread-uncertain) call")
	}
}

/* ── the array-binding-pattern parameter's exact leaf ─────────────── */

// arrayPatternFixtureSource mirrors arrayPatternParameterEntries /
// arrayPatternParameterRefines: `[age]: number[]` binds its one leaf
// through BindInlineParameter/ReadDestructuring, which must read the
// caller's own array argument exactly rather than leaving `age`
// unbound or widened.
const arrayPatternFixtureSource = "function arrayPatternParameterEntries([age]: number[]): number {\n" +
	"  return age;\n" +
	"}\n" +
	"function good(): number { return arrayPatternParameterEntries([40, 41]); }\n" +
	"function over(): number { return arrayPatternParameterEntries([200, 201]); }\n"

func TestEvaluateCallExpression_AnArrayPatternParameterLeafReadsThroughToTheArgument(t *testing.T) {
	kernel := superArrayLoadKernel(t)
	ClearResolvedRecordMembers()
	ClearResolvedArrayParameters()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, arrayPatternFixtureSource)
	ctx := superArrayContracts(t, p)
	ctx.Kernel = kernel

	goodCall := superArrayCallIn(t, p, "good")
	goodValue := evaluateExpression(ctx, NewEnv(), goodCall)
	superArrayExactScalar(t, kernel, goodValue, 40, "arrayPatternParameterEntries([40, 41])")

	overCall := superArrayCallIn(t, p, "over")
	overValue := evaluateExpression(ctx, NewEnv(), overCall)
	superArrayExactScalar(t, kernel, overValue, 200, "arrayPatternParameterEntries([200, 201])")
}

/* ── the rest identifier parameter's exact element read ───────────── */

// restParameterFixtureSource mirrors restIdentifierParameter /
// restIdentifierParameterRefines: `...ages: number[]` binds the
// COLLECTED argument list, and `ages[0]` on it must read the caller's
// first extra argument exactly.
const restParameterFixtureSource = "function restIdentifierParameter(...ages: number[]): number {\n" +
	"  return ages[0];\n" +
	"}\n" +
	"function good(): number { return restIdentifierParameter(40, 41); }\n" +
	"function over(): number { return restIdentifierParameter(200, 201); }\n"

func TestEvaluateCallExpression_ARestParameterElementReadsThroughToTheFirstArgument(t *testing.T) {
	kernel := superArrayLoadKernel(t)
	ClearResolvedRecordMembers()
	ClearResolvedArrayParameters()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, restParameterFixtureSource)
	ctx := superArrayContracts(t, p)
	ctx.Kernel = kernel

	goodCall := superArrayCallIn(t, p, "good")
	goodValue := evaluateExpression(ctx, NewEnv(), goodCall)
	superArrayExactScalar(t, kernel, goodValue, 40, "restIdentifierParameter(40, 41)")

	overCall := superArrayCallIn(t, p, "over")
	overValue := evaluateExpression(ctx, NewEnv(), overCall)
	superArrayExactScalar(t, kernel, overValue, 200, "restIdentifierParameter(200, 201)")
}
