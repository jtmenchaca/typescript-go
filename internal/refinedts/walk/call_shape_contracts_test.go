// Four call/contract shapes the syntax-coverage fixture's §E rows
// name: an overload group's implementation answering calls past its
// body-less signatures, Function.prototype.call binding a contracted
// callee's own `this` parameter, a rest parameter filled as an exact
// tuple by an exact call site, and a generator return type spelled
// through a type alias (plus the yield expression's own N-position
// value). Each test walks the real pipeline — CompileContractFileFacts
// then AnalyzeFunction — the same door e-class-and-function.ts's own
// rows run through, so a pinned row here is the mechanism the fixture
// exercises, not a description of it.
//
// Reuses yieldContractOf / yieldContractKernel (yield_contract_test.go,
// same package) for the checker+binder harness and the native-kernel
// gate — every kernel-backed case here skips the same way theirs does
// when the dylib is absent.

package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
)

// ── overload group: the implementation's summary answers the call ──

// TestOverloadGroup_TheImplementationRegistersLastAndAnswersTheCall
// pins the contract-compile half directly: pickYears' two body-less
// overload signatures and its implementation all share one symbol
// (TypeScript merges an overload group), and CompileContractFileFacts
// registers each declaration it visits in source order — so the
// implementation, visited last, is the one contracts[symbol] holds.
// The registered Declaration must be the THIRD pickYears node (the one
// with a body): an overload signature's Declaration.Body() is nil,
// and every call/summary route declines outright on a nil body
// (kernel_summaries.go's summaryLowerable, inline_contract_body.go's
// own nil-body guard).
func TestOverloadGroup_TheImplementationRegistersLastAndAnswersTheCall(t *testing.T) {
	source := "function pickYears(age: number): number;\n" +
		"function pickYears(age: number, extra: number): number;\n" +
		"function pickYears(age: number, extra?: number): number {\n" +
		"  return age + (extra ?? 0);\n" +
		"}\n"
	contract, _, _ := yieldContractOf(t, source, "pickYears")
	if contract.Declaration.Body() == nil {
		t.Fatalf("the registered pickYears contract carries a body-less declaration — an overload signature outran the implementation")
	}
	params := contract.Declaration.Parameters()
	if len(params) != 2 {
		t.Fatalf("the registered pickYears declaration has %d parameters, want 2 (the implementation's own arity, `age, extra?`)", len(params))
	}
}

// TestOverloadGroup_ACallWithinTheImplementationsArityDeterminesAValue
// pins the walk half: pickYears(40) — one argument, filling the
// REQUIRED age parameter and leaving the optional extra absent —
// determines exactly 40 through the implementation's own body
// (`age + (extra ?? 0)`, extra ?? 0 folding to 0 when extra is
// undefined). A caller resolving to a body-less overload signature
// instead would see Body() == nil and every route would decline,
// leaving the call's value unknown — which this pins against.
func TestOverloadGroup_ACallWithinTheImplementationsArityDeterminesAValue(t *testing.T) {
	kernel := yieldContractKernel(t)
	source := "function pickYears(age: number): number;\n" +
		"function pickYears(age: number, extra: number): number;\n" +
		"function pickYears(age: number, extra?: number): number {\n" +
		"  return age + (extra ?? 0);\n" +
		"}\n" +
		"function caller(): number {\n" +
		"  return pickYears(40);\n" +
		"}\n"
	contract, ctx, diagnostics := yieldContractOf(t, source, "caller")
	ctx.Kernel = kernel
	// EngineKernelHeld() stays UNSEATED here, deliberately: with it seated,
	// InlineContractBody's kernel-summary route (kernelSummaryDirect)
	// tries first and SERVES — effect_expression.go's own logicalTokens
	// comment says why the served answer is only a SOUND SUPERSET for
	// `??`/`&&`/`||` ("the JOIN of both operands' sets… a superset on the
	// arm the short-circuit picked, never an exclusion"), so `extra ?? 0`
	// serves `extra`'s wide number ground joined with 0 instead of the
	// exact 0 the walk route computes knowing `extra` is absent. This
	// call needs no kernel-summary serving to determine pickYears(40)'s
	// exact value — TransferBinary folds two exact KindValues arithmetic
	// operands without asking the kernel at all — so leaving the global
	// unseated (yieldContractKernel's own SetEngineKernel call is undone
	// here, on purpose) lets the exact walk-route answer through
	// unshadowed, which is what this test pins.
	SetEngineKernel(nil)
	t.Cleanup(func() { SetEngineKernel(kernel) })

	var returned abstractdomain.AbstractValue
	sink := []abstractdomain.AbstractValue{}
	ctx.ReturnSink = &sink
	AnalyzeFunction(ctx, contract, nil)
	if len(sink) == 0 {
		t.Fatalf("caller's body recorded no return value")
	}
	returned = JoinSinkSummarized(sink)
	if returned.Kind != abstractdomain.KindValues {
		t.Fatalf("pickYears(40) determined %+v, want an exact KindValues(40) — the implementation's own body answering", returned)
	}
	if len(returned.Values) != 1 || returned.Values[0] != 40 {
		t.Errorf("pickYears(40) determined %v, want exactly [40]", returned.Values)
	}
	if len(*diagnostics) != 0 {
		t.Errorf("an in-arity call to the implementation reported %d diagnostics, want 0: %+v", len(*diagnostics), *diagnostics)
	}
}

// ── Function.prototype.call: the first argument binds `this` ───────

// TestThisParameterCall_TheFirstArgumentBindsTheDeclaredThisParameter
// pins ThisParameterCallResult directly: withThis(this: {age:number})
// returns this.age, and withThis.call({age: 40}) must read that
// receiver's own `age` key — sec-function.prototype.call's own
// binding (the first argument becomes [[Call]]'s thisArgument).
func TestThisParameterCall_TheFirstArgumentBindsTheDeclaredThisParameter(t *testing.T) {
	kernel := yieldContractKernel(t)
	source := "function withThis(this: { age: number }): number {\n" +
		"  return this.age;\n" +
		"}\n" +
		"function caller(): number {\n" +
		"  return withThis.call({ age: 40 });\n" +
		"}\n"
	_, ctx, _ := yieldContractOf(t, source, "caller")
	ctx.Kernel = kernel
	SetEngineKernel(kernel)

	callerFn := entryEnvFunctionNamed(t, ctx.P, "caller")
	returnStatement := callerFn.AsFunctionDeclaration().Body.AsBlock().Statements.Nodes[0]
	callExpr := returnStatement.AsReturnStatement().Expression

	env := NewEnv()
	result := ThisParameterCallResult(ctx, env, callExpr)
	if result == nil {
		t.Fatalf("ThisParameterCallResult answered nil for withThis.call({age: 40}) — the shape did not match")
	}
	if result.Kind != abstractdomain.KindValues {
		t.Fatalf("withThis.call({age: 40}) determined %+v, want an exact KindValues(40)", *result)
	}
	if len(result.Values) != 1 || result.Values[0] != 40 {
		t.Errorf("withThis.call({age: 40}) determined %v, want exactly [40]", result.Values)
	}
}

// TestThisParameterCall_APlainThisReadOutsideACallStillDeclines pins
// the OTHER half of the ThisKeyword arm's widening: a this-parameter
// function's body called through .call reads its bound this, but the
// SAME body reached any other way (no caller binding in env) still
// declines — EnclosingThisParameterFunction recognizes the site, but
// env.Get("this") finds nothing, so the read falls to silence exactly
// as it did before this recognizer existed.
func TestThisParameterCall_APlainThisReadOutsideACallStillDeclines(t *testing.T) {
	kernel := yieldContractKernel(t)
	source := "function withThis(this: { age: number }): number {\n" +
		"  return this.age;\n" +
		"}\n"
	contract, ctx, _ := yieldContractOf(t, source, "withThis")
	ctx.Kernel = kernel
	SetEngineKernel(kernel)

	var sink []abstractdomain.AbstractValue
	ctx.ReturnSink = &sink
	AnalyzeFunction(ctx, contract, nil)
	if len(sink) == 0 {
		t.Fatalf("withThis's own body recorded no return value")
	}
	returned := JoinSinkSummarized(sink)
	if returned.Kind != abstractdomain.KindUnknown {
		t.Errorf("this.age read with no caller binding determined %+v, want KindUnknown (undetermined, not a wrong value)", returned)
	}
}

// ── rest parameter: an exact call site fills an exact tuple ────────

// TestRestParameter_AnExactCallSiteFillsTheRestParameterExactly pins
// the walk half: firstAge(...ages: number[]) returning ages[0], called
// as firstAge(40, 41) — two exact literal arguments — must determine
// exactly 40. Before the applySummary EXACT-serving fix
// (kernel_summaries.go), a rest parameter's kernel-summary entry state
// is always TOP (summaryEntryStates' own rule, since the compiled
// program is reused across every call and cannot carry one call's
// length), and the COMPLETE-body serving rule handed that TOP ret back
// as the call's answer before recoverPureBody's walk-based recovery
// (ParameterKnown's own exact list) ever ran.
func TestRestParameter_AnExactCallSiteFillsTheRestParameterExactly(t *testing.T) {
	kernel := yieldContractKernel(t)
	source := "function firstAge(...ages: number[]): number {\n" +
		"  return ages[0];\n" +
		"}\n" +
		"function caller(): number {\n" +
		"  return firstAge(40, 41);\n" +
		"}\n"
	contract, ctx, diagnostics := yieldContractOf(t, source, "caller")
	ctx.Kernel = kernel
	SetEngineKernel(kernel)

	var sink []abstractdomain.AbstractValue
	ctx.ReturnSink = &sink
	AnalyzeFunction(ctx, contract, nil)
	if len(sink) == 0 {
		t.Fatalf("caller's body recorded no return value")
	}
	returned := JoinSinkSummarized(sink)
	if returned.Kind != abstractdomain.KindValues {
		t.Fatalf("firstAge(40, 41) determined %+v, want an exact KindValues(40) — the exact call site's own rest tuple", returned)
	}
	if len(returned.Values) != 1 || returned.Values[0] != 40 {
		t.Errorf("firstAge(40, 41) determined %v, want exactly [40]", returned.Values)
	}
	if len(*diagnostics) != 0 {
		t.Errorf("an exact rest-parameter call reported %d diagnostics, want 0: %+v", len(*diagnostics), *diagnostics)
	}
}

// ── generator return type through an alias, and the N position ─────

// TestGeneratorAlias_AReturnTypeSpelledThroughAnAliasStillCompilesYAndR
// pins aliasedTypeReference: `type G = Generator<10 | 40, 10 | 40,
// unknown>; function* f(): G { ... }` must compile the SAME Y and R
// positions a direct `Generator<...>` spelling would — before this
// fix, generatorStatedPositions' own name test read the alias name
// `G` directly (never `Generator`), so both positions compiled nil
// and the contract never grounded.
func TestGeneratorAlias_AReturnTypeSpelledThroughAnAliasStillCompilesYAndR(t *testing.T) {
	source := "type G = Generator<10 | 40, 10 | 40, unknown>;\n" +
		"function* judged(): G {\n" +
		"  yield 40;\n" +
		"  return 40;\n" +
		"}\n"
	contract, _, _ := yieldContractOf(t, source, "judged")
	if contract.Yield == nil {
		t.Fatalf("a generator return type spelled through an alias compiled no yield position")
	}
	if contract.Yield.Kind != annotations.DeclaredSet {
		t.Errorf("the aliased yield position compiled as kind %v, want a DeclaredSet", contract.Yield.Kind)
	}
	if contract.Result == nil {
		t.Fatalf("a generator return type spelled through an alias compiled no result position")
	}
	if !contract.Grounded {
		t.Errorf("a generator contract reached through an alias did not ground")
	}
}

// TestGeneratorAlias_AYieldExpressionReadAsAValueWearsTheStatedNPosition
// pins the N-position half: Generator<Age's set, …, 10 | 40>'s THIRD
// argument states what the yield EXPRESSION ITSELF reads as (what the
// caller's next(v) sends back) — `const v = yield 40;` must evaluate
// the yield expression to that stated set, through
// AbstractValueOfDeclared(*ctx.YieldResumeStated), the same
// stated-refinement-to-value idiom BindEntryEnv seeds a parameter's
// entry value with.
func TestGeneratorAlias_AYieldExpressionReadAsAValueWearsTheStatedNPosition(t *testing.T) {
	kernel := yieldContractKernel(t)
	source := "function* resumed(): Generator<number, number, 10 | 40> {\n" +
		"  const v = yield 1;\n" +
		"  return v;\n" +
		"}\n"
	contract, ctx, _ := yieldContractOf(t, source, "resumed")
	ctx.Kernel = kernel
	SetEngineKernel(kernel)

	if contract.YieldResume == nil {
		t.Fatalf("Generator<number, number, 10 | 40>'s third argument compiled no resume position")
	}
	if contract.YieldResume.Kind != annotations.DeclaredSet {
		t.Errorf("the resume position compiled as kind %v, want a DeclaredSet", contract.YieldResume.Kind)
	}

	var sink []abstractdomain.AbstractValue
	ctx.ReturnSink = &sink
	AnalyzeFunction(ctx, contract, nil)
	if len(sink) == 0 {
		t.Fatalf("resumed's body recorded no return value")
	}
	returned := JoinSinkSummarized(sink)
	// `v` is bound from the yield EXPRESSION's own value, which
	// AbstractValueOfDeclared(*ctx.YieldResumeStated) seeds as the
	// stated 10 | 40 set — not a wide "number", and not undetermined
	if returned.Kind != abstractdomain.KindSet && returned.Kind != abstractdomain.KindValues {
		t.Fatalf("`return v` (v bound from `yield 1`'s own value) determined %+v, want the stated 10 | 40 resume position, not KindUnknown", returned)
	}
}

// TestGeneratorAlias_AYieldExpressionWithNoStatedNPositionStillDeclines
// pins the decline half: a generator whose Generator<...> states only
// Y and R (a two-argument spelling, N unstated) reads a `yield e`
// expression's own value as undetermined — the one syntax table's
// decline still applies wherever ctx.YieldResumeStated is nil.
func TestGeneratorAlias_AYieldExpressionWithNoStatedNPositionStillDeclines(t *testing.T) {
	kernel := yieldContractKernel(t)
	source := "function* judged(): Generator<10 | 40, 10 | 40> {\n" +
		"  const v = yield 40;\n" +
		"  return v;\n" +
		"}\n"
	contract, ctx, _ := yieldContractOf(t, source, "judged")
	ctx.Kernel = kernel
	SetEngineKernel(kernel)
	if contract.YieldResume != nil {
		t.Fatalf("a two-argument Generator<Y, R> compiled a resume position — the third argument was never written")
	}

	env := NewEnv()
	ctx.YieldResumeStated = nil
	body := contract.Declaration.Body()
	firstStatement := body.AsBlock().Statements.Nodes[0]
	yieldExpr := firstStatement.AsVariableStatement().DeclarationList.AsVariableDeclarationList().
		Declarations.Nodes[0].AsVariableDeclaration().Initializer
	if yieldExpr.Kind != ast.KindYieldExpression {
		t.Fatalf("the first statement's initializer is %v, want KindYieldExpression", yieldExpr.Kind)
	}
	answer := evaluateExpression(ctx, env, yieldExpr)
	if answer.Kind != abstractdomain.KindUnknown {
		t.Errorf("a yield expression's value with no stated N position determined %+v, want KindUnknown", answer)
	}
}
