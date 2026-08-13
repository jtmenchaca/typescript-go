// Await, lowered. The ret-as-inner convention means a summarized async
// callee's ret slot IS the settled value, so `await f(…)` lowers to
// exactly what `f(…)` lowers to — these probes pin the peeling, the
// identity read of a tracked scalar, the promise-held local's
// recognition, the Promise.all statement shape, and the declines.
//
// Parse-only, the same reading ir_summary_call_test.go uses: the
// lowering reads syntax plus the caller's context, and the routes that
// need a compiled blob decline without a flow context — which is what
// several of these assert.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/parser"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// awaitParse parses a source whose statements the lowering reads
// directly — no checker involved. The statements are wrapped in an
// ASYNC function first: at the top level of a script, `await (` parses
// as a call of an identifier named await, not an await expression, so
// the wrapper is what gives the keyword its meaning.
func awaitParse(t *testing.T, source string) []*ast.Node {
	t.Helper()
	opts := ast.SourceFileParseOptions{FileName: "/await.ts", Path: "/await.ts"}
	file := parser.ParseSourceFile(opts,
		"async function __probe() {\n"+source+"\n}", core.ScriptKindTS)
	fn := file.Statements.Nodes[0].AsFunctionDeclaration()
	return fn.Body.AsBlock().Statements.Nodes
}

// awaitScalarContext is a context with two tracked number slots and no
// registry — enough for the identity forms, and a decline for anything
// that needs a callee's blob.
func awaitScalarContext() *LoweringContext {
	return &LoweringContext{
		Bindings: []string{"x", "s", "#done", "#ret"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber, BindingKindUnknown},
		Typeofs:  []TypeofTag{TypeofTagNumber, TypeofTagNumber, TypeofTagNumber, TypeofTagNone},
		Result:   &LoweringResult{Done: 2, Ret: 3},
	}
}

func TestAwaitedOperandOf_ThePeelReachesTheCallThroughParensAndCasts(t *testing.T) {
	statements := awaitParse(t, `
		await f(1);
		await (g(2) as number);
		f(3);
	`)
	first := statements[0].AsExpressionStatement().Expression
	operand, isAwait := AwaitedOperandOf(first)
	if !isAwait {
		t.Fatalf("AwaitedOperandOf(await f(1)) isAwait = false, want true")
	}
	if !ast.IsCallExpression(operand) {
		t.Errorf("the peeled operand of `await f(1)` is not a call expression")
	}

	second := statements[1].AsExpressionStatement().Expression
	castOperand, castIsAwait := AwaitedOperandOf(second)
	if !castIsAwait {
		t.Fatalf("AwaitedOperandOf(await (g(2) as number)) isAwait = false, want true")
	}
	if !ast.IsCallExpression(castOperand) {
		t.Errorf("the peel did not reach through the cast to the call")
	}

	third := statements[2].AsExpressionStatement().Expression
	if _, isAwait := AwaitedOperandOf(third); isAwait {
		t.Errorf("a plain call read as an await")
	}
}

func TestAwaitStatement_AwaitOfATrackedScalarAssignsTheIdentityRead(t *testing.T) {
	// `await s` on a non-promise settles to the value itself, so the
	// right side is exactly the slot's own var
	context := awaitScalarContext()
	statements := awaitParse(t, `x = await s;`)
	lowered, ok := AwaitStatementOf(context, statements[0])
	if !ok {
		t.Fatalf("AwaitStatementOf(x = await s) ok = false, want true")
	}
	if len(lowered) != 1 {
		t.Fatalf("len(lowered) = %d, want 1", len(lowered))
	}
	if lowered[0].Kind != kernelbridge.IrStatementAssign {
		t.Errorf("lowered[0].Kind = %v, want assign", lowered[0].Kind)
	}
	if lowered[0].Target != 0 {
		t.Errorf("lowered[0].Target = %d, want 0 (x)", lowered[0].Target)
	}
	if lowered[0].Effect.Kind != kernelbridge.LoopEffectVar {
		t.Errorf("lowered[0].Effect.Kind = %v, want var", lowered[0].Effect.Kind)
	}
	if lowered[0].Effect.Index != 1 {
		t.Errorf("lowered[0].Effect.Index = %d, want 1 (s)", lowered[0].Effect.Index)
	}
}

func TestAwaitStatement_ABareAwaitOfATrackedScalarContributesNoStatement(t *testing.T) {
	// the read happens and the value is dropped — nothing in the slot
	// vector moves
	context := awaitScalarContext()
	statements := awaitParse(t, `await s;`)
	lowered, ok := AwaitStatementOf(context, statements[0])
	if !ok {
		t.Fatalf("AwaitStatementOf(await s;) ok = false, want true")
	}
	if len(lowered) != 0 {
		t.Errorf("len(lowered) = %d, want 0 — a dropped read moves no slot", len(lowered))
	}
}

func TestAwaitStatement_AwaitOfAnUntrackedNameDeclines(t *testing.T) {
	context := awaitScalarContext()
	statements := awaitParse(t, `x = await q;`)
	if _, ok := AwaitStatementOf(context, statements[0]); ok {
		t.Errorf("an await of an untracked name lowered — there is no slot to read")
	}
}

func TestAwaitStatement_AwaitOfACallWithoutARegistryTakesTheHavocFloor(t *testing.T) {
	// no flow context, so no blob and no cycle — the TOTAL-LOWERING
	// floor serves instead: the assigned form havocs its target, and
	// the bare form (scalar arguments, nothing flattened mentioned)
	// contributes no statement at all
	context := awaitScalarContext()
	statements := awaitParse(t, `x = await f(1);`)
	lowered, ok := AwaitStatementOf(context, statements[0])
	if !ok {
		t.Fatalf("an awaited call without a registry declined, want the havoc floor")
	}
	if len(lowered) != 1 || lowered[0].Kind != kernelbridge.IrStatementAssign ||
		lowered[0].Target != 0 || lowered[0].Effect.Kind != kernelbridge.LoopEffectUnknown {
		t.Errorf("lowered = %+v, want one assign of unknown to x (slot 0)", lowered)
	}
	bare := awaitParse(t, `await f(1);`)
	bareLowered, bareOk := AwaitStatementOf(context, bare[0])
	if !bareOk {
		t.Fatalf("a bare awaited call without a registry declined, want the havoc floor")
	}
	if len(bareLowered) != 0 {
		t.Errorf("len(bareLowered) = %d, want 0 — nothing reads its value and nothing flattened is mentioned", len(bareLowered))
	}
}

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

func TestPromiseAllArray_TheArrayLiteralOfCallsIsRecognizedAndEveryOtherShapeDeclines(t *testing.T) {
	statements := awaitParse(t, `
		await Promise.all([f(1), g(2)]);
		await Promise.all(items);
		await Promise.all([...items]);
		await Promise.race([f(1)]);
		await Promise.all([f(1)], 2);
	`)
	operand, isAwait := AwaitedOperandOf(statements[0].AsExpressionStatement().Expression)
	if !isAwait {
		t.Fatalf("the first statement is not an await")
	}
	elements, recognized := promiseAllArrayOf(operand)
	if !recognized {
		t.Fatalf("promiseAllArrayOf(Promise.all([f(1), g(2)])) recognized = false, want true")
	}
	if len(elements) != 2 {
		t.Errorf("len(elements) = %d, want 2", len(elements))
	}
	for index := 1; index < len(statements); index++ {
		other, ok := AwaitedOperandOf(statements[index].AsExpressionStatement().Expression)
		if !ok {
			t.Fatalf("statement %d is not an await", index)
		}
		if _, recognized := promiseAllArrayOf(other); recognized {
			t.Errorf("statement %d took the Promise.all route — only an array literal of calls does", index)
		}
	}
}

func TestPromiseAllStatement_WithoutARegistryTheCallsTakeTheHavocFloor(t *testing.T) {
	// with no flow context there are no blobs, and each element call
	// takes the total-lowering floor: no target, scalar arguments,
	// nothing flattened mentioned — no statement per call
	context := awaitScalarContext()
	statements := awaitParse(t, `await Promise.all([f(1), g(2)]);`)
	lowered, ok := promiseAllStatementOf(context, statements[0])
	if !ok {
		t.Fatalf("a Promise.all without a registry declined, want the havoc floor per call")
	}
	if len(lowered) != 0 {
		t.Errorf("len(lowered) = %d, want 0 — nothing reads the calls' values", len(lowered))
	}
}

func TestUsesAreAllAwaits_ATotalAwaitReadingIsRecognizedAndAnyOtherUseDeclines(t *testing.T) {
	statements := awaitParse(t, `
		async function total() {
			const p = f(1);
			const a = await p;
			const b = await p;
		}
		async function passed() {
			const p = f(1);
			h(p);
		}
		async function chained() {
			const p = f(1);
			p.then(k);
		}
		async function returned() {
			const p = f(1);
			return p;
		}
		async function caught() {
			const p = f(1);
			p.catch(k);
		}
	`)
	readingOf := func(index int) (body *ast.Node, declaration *ast.Node) {
		body = statements[index].AsFunctionDeclaration().Body
		declaration = body.AsBlock().Statements.Nodes[0].AsVariableStatement().
			DeclarationList.AsVariableDeclarationList().Declarations.Nodes[0]
		return body, declaration
	}
	body, declaration := readingOf(0)
	if !usesAreAllAwaits(body, declaration, "p") {
		t.Errorf("a p used only as `await p` was not recognized")
	}
	// `p.then(cb)` is now a recognized use: the receiver reads through
	// the settled slot the then-lowering serves
	thenBody, thenDeclaration := readingOf(2)
	if !usesAreAllAwaits(thenBody, thenDeclaration, "p") {
		t.Errorf("a p chained with .then was not recognized — the then-lowering serves it")
	}
	declining := []struct {
		Index int
		What  string
	}{
		{1, "passed as an argument"},
		{3, "returned"},
		{4, "chained with .catch"},
	}
	for _, row := range declining {
		body, declaration := readingOf(row.Index)
		if usesAreAllAwaits(body, declaration, "p") {
			t.Errorf("a p %s was recognized — one settled-value slot cannot spell that use", row.What)
		}
	}
}

func TestPromiseLocalDeclaration_WithoutAnAllocateThereIsNoInnerSlotToFlattenInto(t *testing.T) {
	context := awaitScalarContext()
	statements := awaitParse(t, `
		async function total() {
			const p = f(1);
			const a = await p;
		}
	`)
	body := statements[0].AsFunctionDeclaration().Body.AsBlock().Statements.Nodes
	if _, ok := promiseLocalDeclarationOf(context, body[0]); ok {
		t.Errorf("a promise local flattened with no allocate — there is no slot to give it")
	}
}

func TestPromiseInnerSlot_TheHeldSlotIsReadBackByNameAndAnUnheldNameAnswersNothing(t *testing.T) {
	context := awaitScalarContext()
	if _, held := promiseInnerSlotOf(context, "p"); held {
		t.Errorf("an unrecognized name answered a slot")
	}
	holdPromiseInnerSlot(context, "p", 7)
	slot, held := promiseInnerSlotOf(context, "p")
	if !held {
		t.Fatalf("the held slot was not read back")
	}
	if slot != 7 {
		t.Errorf("slot = %d, want 7", slot)
	}
	// and `await p` then reads it as the identity
	statements := awaitParse(t, `x = await p;`)
	lowered, ok := AwaitStatementOf(context, statements[0])
	if !ok {
		t.Fatalf("AwaitStatementOf(x = await p) ok = false, want true")
	}
	if len(lowered) != 1 {
		t.Fatalf("len(lowered) = %d, want 1", len(lowered))
	}
	if lowered[0].Effect.Kind != kernelbridge.LoopEffectVar || lowered[0].Effect.Index != 7 {
		t.Errorf("`await p` did not read the flattened inner slot: %+v", lowered[0].Effect)
	}
}
