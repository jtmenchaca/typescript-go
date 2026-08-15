// split from ir_await_test.go — the return route's probes: the identity
// read into the result slot and the async question behind the bare form.

package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

func TestAwaitReturn_ReturnAwaitOfATrackedScalarWritesTheResultSlotAndRaisesTheFlag(t *testing.T) {
	context := awaitScalarContext()
	statements := awaitParse(t, `function f() { return await s; }`)
	body := statements[0].AsFunctionDeclaration().Body.AsBlock().Statements.Nodes
	raise := kernelbridge.IrStatement{Kind: kernelbridge.IrStatementAssign, Target: context.Result.Done}
	lowered, ok := AwaitReturnStatements(context, body[0].AsReturnStatement().Expression, raise)
	if !ok {
		t.Fatalf("AwaitReturnStatements(return await s) ok = false, want true")
	}
	if len(lowered) != 2 {
		t.Fatalf("len(lowered) = %d, want 2 (the ret write and the raise)", len(lowered))
	}
	if lowered[0].Target != context.Result.Ret {
		t.Errorf("lowered[0].Target = %d, want %d (#ret)", lowered[0].Target, context.Result.Ret)
	}
	if lowered[0].Effect.Kind != kernelbridge.LoopEffectVar || lowered[0].Effect.Index != 1 {
		t.Errorf("the ret write is not the identity read of s: %+v", lowered[0].Effect)
	}
	if lowered[1].Target != context.Result.Done {
		t.Errorf("lowered[1] is not the raise: target = %d, want %d", lowered[1].Target, context.Result.Done)
	}
}

func TestAwaitReturn_ReturnAwaitOfPromiseRejectWritesAThrownExit(t *testing.T) {
	// ECMA-262 sec-promise.reject: reason is passed straight to
	// [[Reject]] with no thenable check, so the rejection propagates
	// before the return ever hands back a value — the sound answer is
	// the same #ret := thrown / #done := {1} shape lowerThrowStatement
	// writes for `throw e`, not the identity read
	context := awaitScalarContext()
	statements := awaitParse(t, `function f() { return await Promise.reject(s); }`)
	body := statements[0].AsFunctionDeclaration().Body.AsBlock().Statements.Nodes
	raise := kernelbridge.IrStatement{
		Kind:   kernelbridge.IrStatementAssign,
		Target: context.Result.Done,
		Effect: kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectConst},
	}
	lowered, ok := AwaitReturnStatements(context, body[0].AsReturnStatement().Expression, raise)
	if !ok {
		t.Fatalf("AwaitReturnStatements(return await Promise.reject(s)) ok = false, want true")
	}
	if len(lowered) != 2 {
		t.Fatalf("len(lowered) = %d, want 2 (the thrown ret write and the raise): %+v", len(lowered), lowered)
	}
	if lowered[0].Target != context.Result.Ret || lowered[0].Effect.Kind != kernelbridge.LoopEffectThrown {
		t.Errorf("lowered[0] = %+v, want `assign #ret thrown`", lowered[0])
	}
	if lowered[1].Target != context.Result.Done {
		t.Errorf("lowered[1] is not the caller's own raise: target = %d, want %d", lowered[1].Target, context.Result.Done)
	}
}

func TestAwaitReturn_ReturnAwaitOfPromiseRejectInsideAnUncoveredTryDeclines(t *testing.T) {
	// a reject inside a try this body's own try route does not cover
	// keeps the decline, the same law an explicit `throw` obeys
	// (ThrowReachesATry / ThrowCoveredByItsTry, lowering_to_kernel_ir_throw.go)
	context := awaitScalarContext()
	statements := awaitParse(t, `
		function f() {
			try {
				return await Promise.reject(s);
			} finally {
				cleanup();
			}
		}
	`)
	fn := statements[0].AsFunctionDeclaration().Body.AsBlock().Statements.Nodes[0]
	tryReturn := fn.AsTryStatement().TryBlock.AsBlock().Statements.Nodes[0]
	raise := kernelbridge.IrStatement{Kind: kernelbridge.IrStatementAssign, Target: context.Result.Done}
	if _, ok := AwaitReturnStatements(context, tryReturn.AsReturnStatement().Expression, raise); ok {
		t.Errorf("a Promise.reject return inside a finally-bearing try lowered — the try route does not cover it")
	}
}

func TestAwaitReturn_WithoutAResultSlotThereIsNothingToReturnInto(t *testing.T) {
	context := awaitScalarContext()
	context.Result = nil
	statements := awaitParse(t, `function f() { return await s; }`)
	body := statements[0].AsFunctionDeclaration().Body.AsBlock().Statements.Nodes
	if _, ok := AwaitReturnStatements(context, body[0].AsReturnStatement().Expression, kernelbridge.IrStatement{}); ok {
		t.Errorf("a return lowered with no result slot")
	}
}

func TestInsideAsyncDeclaration_TheBareReturnFormIsAdmittedOnlyFromAnAsyncBody(t *testing.T) {
	// `return f(…)` from an async body ADOPTS the promise, so it settles
	// exactly as `return await f(…)` does; from a plain body it does not
	statements := awaitParse(t, `
		async function a() { return f(1); }
		function b() { return f(1); }
	`)
	asyncReturn := statements[0].AsFunctionDeclaration().Body.AsBlock().Statements.Nodes[0]
	plainReturn := statements[1].AsFunctionDeclaration().Body.AsBlock().Statements.Nodes[0]
	if !insideAsyncDeclaration(asyncReturn.AsReturnStatement().Expression) {
		t.Errorf("a return inside `async function a` read as not async")
	}
	if insideAsyncDeclaration(plainReturn.AsReturnStatement().Expression) {
		t.Errorf("a return inside a plain function read as async")
	}
}

func TestInsideAsyncDeclaration_AnAsyncMethodAndAnAsyncArrowBothCount(t *testing.T) {
	statements := awaitParse(t, `
		class C { async m() { return f(1); } }
		const g = async () => { return f(1); };
	`)
	method := statements[0].AsClassDeclaration().Members.Nodes[0]
	methodReturn := method.AsMethodDeclaration().Body.AsBlock().Statements.Nodes[0]
	if !insideAsyncDeclaration(methodReturn.AsReturnStatement().Expression) {
		t.Errorf("a return inside an async method read as not async")
	}
	arrow := statements[1].AsVariableStatement().DeclarationList.AsVariableDeclarationList().
		Declarations.Nodes[0].AsVariableDeclaration().Initializer
	arrowReturn := arrow.AsArrowFunction().Body.AsBlock().Statements.Nodes[0]
	if !insideAsyncDeclaration(arrowReturn.AsReturnStatement().Expression) {
		t.Errorf("a return inside an async arrow read as not async")
	}
}
