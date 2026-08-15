// split from ir_await_test.go — the promise-held local's probes: the use
// scan, the declaration's decline, and the held inner slot.

package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

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

// awaitAllocatingScalarContext is awaitScalarContext with an Allocate
// that grows the vector — what promiseLocalDeclarationOf needs to give
// a new "p.inner" slot a home.
func awaitAllocatingScalarContext() *LoweringContext {
	context := awaitScalarContext()
	context.Allocate = func(name string, sort BindingKind, tag TypeofTag) (int, bool) {
		context.Bindings = append(context.Bindings, name)
		context.Sorts = append(context.Sorts, sort)
		context.Typeofs = append(context.Typeofs, tag)
		return len(context.Bindings) - 1, true
	}
	return context
}

func TestPromiseLocalDeclaration_APromiseResolveInitializerFlattensToTheInnerSlot(t *testing.T) {
	// `const p = Promise.resolve(s)` where s is a tracked scalar: the
	// same gate `await Promise.resolve(s)` uses (RhsEffect admits only
	// shapes CannotBeThenable would clear), carried into the promise-
	// local route so `await p` downstream reads the flattened slot
	context := awaitAllocatingScalarContext()
	statements := awaitParse(t, `
		async function total() {
			const p = Promise.resolve(s);
			const a = await p;
		}
	`)
	body := statements[0].AsFunctionDeclaration().Body.AsBlock().Statements.Nodes
	lowered, ok := promiseLocalDeclarationOf(context, body[0])
	if !ok {
		t.Fatalf("promiseLocalDeclarationOf(const p = Promise.resolve(s)) ok = false, want true")
	}
	if len(lowered) != 1 || lowered[0].Kind != kernelbridge.IrStatementAssign {
		t.Fatalf("lowered = %+v, want one assign into the inner slot", lowered)
	}
	if lowered[0].Effect.Kind != kernelbridge.LoopEffectVar || lowered[0].Effect.Index != 1 {
		t.Errorf("lowered[0].Effect = %+v, want the var read of s (slot 1)", lowered[0].Effect)
	}
	slot, held := promiseInnerSlotOf(context, "p")
	if !held {
		t.Fatalf("promiseInnerSlotOf(p) held = false after a Promise.resolve initializer, want true")
	}
	if lowered[0].Target != slot {
		t.Errorf("lowered[0].Target = %d, want the held inner slot %d", lowered[0].Target, slot)
	}
}

func TestPromiseLocalDeclaration_APromiseResolveOfAnUnreadableArgumentDeclines(t *testing.T) {
	// `Promise.resolve(f())` — the argument is a call, which RhsEffect
	// does not read; the promise-local route declines and the
	// declaration takes whatever route it took before
	context := awaitAllocatingScalarContext()
	statements := awaitParse(t, `
		async function total() {
			const p = Promise.resolve(f());
			const a = await p;
		}
	`)
	body := statements[0].AsFunctionDeclaration().Body.AsBlock().Statements.Nodes
	if _, ok := promiseLocalDeclarationOf(context, body[0]); ok {
		t.Errorf("a Promise.resolve local with an unreadable argument flattened — RhsEffect cannot read a call")
	}
	if _, held := promiseInnerSlotOf(context, "p"); held {
		t.Errorf("a declined Promise.resolve local left a held slot behind")
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
