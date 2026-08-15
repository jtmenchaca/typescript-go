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
