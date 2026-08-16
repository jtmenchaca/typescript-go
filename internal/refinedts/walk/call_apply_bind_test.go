// Extends this_parameter_call.go's own template: `.apply(thisArg,
// argsArray)` binding an exact array's items the same way `.call`
// binds its own ...rest, `.bind(thisArg, ...partials)` binding a
// bound function VALUE through a direct call of the bind result and
// through a tracked const, and the write-back epilogue every one of
// the three now owes ThisParameterCallResult's own caller — a written
// this-member or reference parameter lands back on the caller's
// tracked slots rather than leaving them stale.
//
// Reuses yieldContractOf / yieldContractKernel / entryEnvFunctionNamed
// (yield_contract_test.go, call_shape_contracts_test.go, same
// package) for the checker+binder harness and the native-kernel gate.

package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
)

// ── .apply: an exact array's items stand in for .call's ...rest ────

// TestApplyCall_AnArrayLiteralBindsThisAndTheOrdinaryParameters pins
// ApplyCallResult directly: withThis(this: {age:number}, extra:
// number) returns this.age + extra, and withThis.apply({age: 40},
// [2]) must read the receiver's own age (40) AND the array literal's
// own item (2) — sec-function.prototype.apply's own binding
// (argArray read into an argument list, then Call(func, thisArg,
// argList), the same [[Call]] .call's own clause names).
func TestApplyCall_AnArrayLiteralBindsThisAndTheOrdinaryParameters(t *testing.T) {
	kernel := yieldContractKernel(t)
	source := "function withThis(this: { age: number }, extra: number): number {\n" +
		"  return this.age + extra;\n" +
		"}\n" +
		"function caller(): number {\n" +
		"  return withThis.apply({ age: 40 }, [2]);\n" +
		"}\n"
	_, ctx, _ := yieldContractOf(t, source, "caller")
	ctx.Kernel = kernel
	SetEngineKernel(kernel)

	callerFn := entryEnvFunctionNamed(t, ctx.P, "caller")
	returnStatement := callerFn.AsFunctionDeclaration().Body.AsBlock().Statements.Nodes[0]
	callExpr := returnStatement.AsReturnStatement().Expression

	env := NewEnv()
	result := ApplyCallResult(ctx, env, callExpr)
	if result == nil {
		t.Fatalf("ApplyCallResult answered nil for withThis.apply({age: 40}, [2]) — the shape did not match")
	}
	if result.Kind != abstractdomain.KindValues {
		t.Fatalf("withThis.apply({age: 40}, [2]) determined %+v, want an exact KindValues(42)", *result)
	}
	if len(result.Values) != 1 || result.Values[0] != 42 {
		t.Errorf("withThis.apply({age: 40}, [2]) determined %v, want exactly [42]", result.Values)
	}
}

// TestApplyCall_ATrackedConstArrayBindsExactly pins the OTHER exact
// reading ApplyCallResult accepts: a const bound to an array literal
// holds that array's own items at every reachable point, so
// withThis.apply({age: 40}, ages) — ages a const array — must read
// exactly as the literal spelling would.
func TestApplyCall_ATrackedConstArrayBindsExactly(t *testing.T) {
	kernel := yieldContractKernel(t)
	source := "function withThis(this: { age: number }, extra: number): number {\n" +
		"  return this.age + extra;\n" +
		"}\n" +
		"function caller(): number {\n" +
		"  const ages = [2];\n" +
		"  return withThis.apply({ age: 40 }, ages);\n" +
		"}\n"
	_, ctx, _ := yieldContractOf(t, source, "caller")
	ctx.Kernel = kernel
	SetEngineKernel(kernel)

	callerFn := entryEnvFunctionNamed(t, ctx.P, "caller")
	statements := callerFn.AsFunctionDeclaration().Body.AsBlock().Statements.Nodes
	returnStatement := statements[len(statements)-1]
	callExpr := returnStatement.AsReturnStatement().Expression

	// `const ages = [2];` must actually RUN before ApplyCallResult reads
	// `ages` off env — a fresh NewEnv() handed straight to the return
	// expression, skipping the preceding statement, never bound `ages`
	// at all (the same harness gap the object-literal-method-write tests
	// hit): AnalyzeStatements over every statement BEFORE the return
	// walks the const declaration first, so `ages` is tracked exactly
	// the way it is at the real return site.
	env := NewEnv()
	AnalyzeStatements(ctx, env, statements[:len(statements)-1], nil)
	result := ApplyCallResult(ctx, env, callExpr)
	if result == nil {
		t.Fatalf("ApplyCallResult answered nil for withThis.apply({age: 40}, ages) — a tracked const array did not read exactly")
	}
	if result.Kind != abstractdomain.KindValues || len(result.Values) != 1 || result.Values[0] != 42 {
		t.Errorf("withThis.apply({age: 40}, ages) determined %+v, want exactly [42]", *result)
	}
}

// TestApplyCall_AnUnreadableArgsArrayDeclinesToNil pins the decline
// half: withThis.apply({age: 40}, computeArgs()) — a callee's own
// return this walk cannot read as an exact sequence — must answer nil
// so EvaluateCallExpression's own fallback serves the call instead of
// ApplyCallResult claiming an inexact array as if it were exact.
func TestApplyCall_AnUnreadableArgsArrayDeclinesToNil(t *testing.T) {
	kernel := yieldContractKernel(t)
	source := "function withThis(this: { age: number }, extra: number): number {\n" +
		"  return this.age + extra;\n" +
		"}\n" +
		"function computeArgs(): number[] {\n" +
		"  return [1, 2];\n" +
		"}\n" +
		"function caller(): number {\n" +
		"  return withThis.apply({ age: 40 }, computeArgs());\n" +
		"}\n"
	_, ctx, _ := yieldContractOf(t, source, "caller")
	ctx.Kernel = kernel
	SetEngineKernel(kernel)

	callerFn := entryEnvFunctionNamed(t, ctx.P, "caller")
	returnStatement := callerFn.AsFunctionDeclaration().Body.AsBlock().Statements.Nodes[0]
	callExpr := returnStatement.AsReturnStatement().Expression

	env := NewEnv()
	result := ApplyCallResult(ctx, env, callExpr)
	if result != nil {
		t.Errorf("ApplyCallResult answered %+v for an unreadable args array, want nil (decline to the existing fallback)", *result)
	}
}

// ── .bind: a bound function VALUE, called directly or through a const ──

// TestBindCall_ADirectCallOfTheBindResultBindsThisAndThePartial pins
// the direct-call shape: withThis.bind({age: 40}, 2)(3) must read the
// receiver's own age (40), the bind's own partial (2), AND the later
// call's own argument (3) — BoundFunctionCreate's own [[BoundThis]]
// and [[BoundArguments]], concatenated ahead of the later call's own
// argList (sec-bound-function-exotic-objects-call-thisargument-
// argumentslist).
func TestBindCall_ADirectCallOfTheBindResultBindsThisAndThePartial(t *testing.T) {
	kernel := yieldContractKernel(t)
	source := "function withThis(this: { age: number }, a: number, b: number): number {\n" +
		"  return this.age + a + b;\n" +
		"}\n" +
		"function caller(): number {\n" +
		"  return withThis.bind({ age: 40 }, 2)(3);\n" +
		"}\n"
	_, ctx, _ := yieldContractOf(t, source, "caller")
	ctx.Kernel = kernel
	SetEngineKernel(kernel)

	callerFn := entryEnvFunctionNamed(t, ctx.P, "caller")
	returnStatement := callerFn.AsFunctionDeclaration().Body.AsBlock().Statements.Nodes[0]
	callExpr := returnStatement.AsReturnStatement().Expression

	env := NewEnv()
	result := BindCallResult(ctx, env, callExpr)
	if result == nil {
		t.Fatalf("BindCallResult answered nil for withThis.bind({age: 40}, 2)(3) — the direct-call shape did not match")
	}
	if result.Kind != abstractdomain.KindValues || len(result.Values) != 1 || result.Values[0] != 45 {
		t.Errorf("withThis.bind({age: 40}, 2)(3) determined %+v, want exactly [45] (40 + 2 + 3)", *result)
	}
}

// TestBindCall_AConstStoredBindIsCalledLaterByName pins the
// const-stored shape: `const g = withThis.bind({age: 40}); g(5)` must
// read the SAME binding as the direct-call shape, resolved through
// ConstInitializerOf off g's own const declaration.
func TestBindCall_AConstStoredBindIsCalledLaterByName(t *testing.T) {
	kernel := yieldContractKernel(t)
	source := "function withThis(this: { age: number }, extra: number): number {\n" +
		"  return this.age + extra;\n" +
		"}\n" +
		"function caller(): number {\n" +
		"  const g = withThis.bind({ age: 40 });\n" +
		"  return g(5);\n" +
		"}\n"
	_, ctx, _ := yieldContractOf(t, source, "caller")
	ctx.Kernel = kernel
	SetEngineKernel(kernel)

	callerFn := entryEnvFunctionNamed(t, ctx.P, "caller")
	statements := callerFn.AsFunctionDeclaration().Body.AsBlock().Statements.Nodes
	returnStatement := statements[len(statements)-1]
	callExpr := returnStatement.AsReturnStatement().Expression

	env := NewEnv()
	result := BindCallResult(ctx, env, callExpr)
	if result == nil {
		t.Fatalf("BindCallResult answered nil for g(5) — a const-stored .bind result did not resolve")
	}
	if result.Kind != abstractdomain.KindValues || len(result.Values) != 1 || result.Values[0] != 45 {
		t.Errorf("g(5) determined %+v, want exactly [45] (40 + 5)", *result)
	}
}

// TestBindCall_ALetStoredBindDeclinesToNil pins the decline half: a
// bind result stored in a REASSIGNABLE let is not read through — the
// checker cannot see every write to it, so ConstInitializerOf answers
// nothing for it and BindCallResult must answer nil rather than
// trusting a binding that may hold a different function at runtime.
func TestBindCall_ALetStoredBindDeclinesToNil(t *testing.T) {
	kernel := yieldContractKernel(t)
	source := "function withThis(this: { age: number }, extra: number): number {\n" +
		"  return this.age + extra;\n" +
		"}\n" +
		"function caller(): number {\n" +
		"  let g = withThis.bind({ age: 40 });\n" +
		"  return g(5);\n" +
		"}\n"
	_, ctx, _ := yieldContractOf(t, source, "caller")
	ctx.Kernel = kernel
	SetEngineKernel(kernel)

	callerFn := entryEnvFunctionNamed(t, ctx.P, "caller")
	statements := callerFn.AsFunctionDeclaration().Body.AsBlock().Statements.Nodes
	returnStatement := statements[len(statements)-1]
	callExpr := returnStatement.AsReturnStatement().Expression

	env := NewEnv()
	result := BindCallResult(ctx, env, callExpr)
	if result != nil {
		t.Errorf("BindCallResult answered %+v for a let-stored bind, want nil (reassignable — not read through)", *result)
	}
}

// ── the write-back epilogue: this-member and reference-parameter writes ──

// TestThisParameterCall_AThisMemberWriteLandsBackOnTheCallersObject
// pins the epilogue's this-member half: spoil(this: {age: number})
// writes `this.age = 200` and returns nothing. The caller passes a
// tracked const object as thisArg: `spoil.call(person)` — the
// epilogue must write 200 back onto person's own age slot the same
// way InlineContractBody's epilogue would for an ordinary reference
// parameter, so a SECOND read of person.age after the call sees 200,
// not the stale 40.
func TestThisParameterCall_AThisMemberWriteLandsBackOnTheCallersObject(t *testing.T) {
	kernel := yieldContractKernel(t)
	source := "function spoil(this: { age: number }): void {\n" +
		"  this.age = 200;\n" +
		"}\n" +
		"function caller(): number {\n" +
		"  const person = { age: 40 };\n" +
		"  spoil.call(person);\n" +
		"  return person.age;\n" +
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
		t.Fatalf("person.age read after spoil.call(person) determined %+v, want an exact KindValues(200) — the epilogue's this-member write-back", returned)
	}
	if len(returned.Values) != 1 || returned.Values[0] != 200 {
		t.Errorf("person.age read after spoil.call(person) determined %v, want exactly [200] — the STALE 40 means the epilogue never wrote back", returned.Values)
	}
	if len(*diagnostics) != 0 {
		t.Errorf("spoil.call(person) reported %d diagnostics, want 0: %+v", len(*diagnostics), *diagnostics)
	}
}

// TestThisParameterCall_AnUnwrittenThisMemberLeavesTheCallersObjectAlone
// is the epilogue's OTHER half: a this-parameter body that only READS
// (never writes) must leave the caller's object exactly as it was —
// BodyWritesOf's own "this" entry is absent, so the epilogue's guard
// (`bodyWrites["this"]`) never fires, and a second read still sees
// the caller's own original value.
func TestThisParameterCall_AnUnwrittenThisMemberLeavesTheCallersObjectAlone(t *testing.T) {
	kernel := yieldContractKernel(t)
	source := "function withThis(this: { age: number }): number {\n" +
		"  return this.age;\n" +
		"}\n" +
		"function caller(): number {\n" +
		"  const person = { age: 40 };\n" +
		"  withThis.call(person);\n" +
		"  return person.age;\n" +
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
	if returned.Kind != abstractdomain.KindValues || len(returned.Values) != 1 || returned.Values[0] != 40 {
		t.Errorf("person.age read after a read-only withThis.call(person) determined %+v, want exactly [40] (unchanged)", returned)
	}
	if len(*diagnostics) != 0 {
		t.Errorf("withThis.call(person) reported %d diagnostics, want 0: %+v", len(*diagnostics), *diagnostics)
	}
}

// TestThisParameterCall_AReferenceParameterWriteLandsBackOnItsArgument
// pins the epilogue's ORDINARY-parameter half: bumpRecord's OWN this
// parameter is a plain {age: number} (unwritten in this body, so the
// this-member half of the epilogue stays quiet), and its ORDINARY
// parameter `record: {count: number}` is written —
// `record.count = record.count + 1`. After `bumpRecord.call({age:
// 1}, tally)`, a second read of tally.count must see 1, not the stale
// 0 — the same write-back InlineContractBody's own epilogue already
// gives a direct call, applied here through the shifted position
// list .call reads through (restEffective's own node at
// ordinary-parameter position 0).
func TestThisParameterCall_AReferenceParameterWriteLandsBackOnItsArgument(t *testing.T) {
	kernel := yieldContractKernel(t)
	source := "function bumpRecord(this: { age: number }, record: { count: number }): void {\n" +
		"  record.count = record.count + 1;\n" +
		"}\n" +
		"function caller(): number {\n" +
		"  const tally = { count: 0 };\n" +
		"  bumpRecord.call({ age: 1 }, tally);\n" +
		"  return tally.count;\n" +
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
		t.Fatalf("tally.count read after bumpRecord.call({age: 1}, tally) determined %+v, want an exact KindValues(1) — the epilogue's reference-parameter write-back", returned)
	}
	if len(returned.Values) != 1 || returned.Values[0] != 1 {
		t.Errorf("tally.count read after the call determined %v, want exactly [1] — the STALE 0 means the epilogue never wrote back", returned.Values)
	}
	if len(*diagnostics) != 0 {
		t.Errorf("bumpRecord.call({age: 1}, tally) reported %d diagnostics, want 0: %+v", len(*diagnostics), *diagnostics)
	}
}

// ── the e-class-and-function.ts:61 row stays determined ─────────────

// TestThisParameterCall_TheFixtureRowStaysDeterminedAfterTheRefactor
// re-pins call_shape_contracts_test.go's own
// TestThisParameterCall_TheFirstArgumentBindsTheDeclaredThisParameter
// row (e-class-and-function.ts:61's own shape) through
// ThisParameterCallResult directly: the .call recognizer's OWN
// binding must read exactly as it did before this file split
// thisParameterCallBind out into a shared core three recognizers
// call — a refactor regression here would mean .apply/.bind's own
// addition broke .call's already-landed, already-pinned row.
func TestThisParameterCall_TheFixtureRowStaysDeterminedAfterTheRefactor(t *testing.T) {
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
		t.Fatalf("ThisParameterCallResult answered nil for withThis.call({age: 40}) after the .apply/.bind refactor — the e-class-and-function.ts:61 row's own mechanism broke")
	}
	if result.Kind != abstractdomain.KindValues || len(result.Values) != 1 || result.Values[0] != 40 {
		t.Errorf("withThis.call({age: 40}) determined %+v, want exactly [40]", *result)
	}
}
