// Record locals flattened into scalar slots: a body keeping a
// fixed-shape object in a local summarizes through the kernel walk,
// and the uses that would observe the object AS an object decline.
// Skipped (never a faked pass) when the native kernel dylib is absent,
// the same gate the sibling summary tests use.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
)

func TestObjectSlots_AFixedShapeRecordLocalFlattensIntoPerKeySlotsAndSummarizes(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	// p has no slot of its own: "p.lo" and "p.hi" do. The declaration
	// lowers as two assignments, the loop writes p.lo, and the return
	// reads it — every step through the ordinary scalar grammar.
	declaration := summaryDeclarationOf(t,
		"function f(n: number) { const p = { lo: 0, hi: n }; let i = 0; while (i < 3) { p.lo = p.lo + 1; i = i + 1; } return p.lo; }")
	contract := &FunctionContract{Declaration: declaration}
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	answer, ok := KernelSummaryDirect(ctx, []abstractdomain.AbstractValue{exactNumber(t, 7)}, contract)
	if !ok {
		t.Fatalf("KernelSummaryDirect ok = false, want a summarized answer for a flattened record local")
	}
	state, stateOk := StateOfKnown(answer)
	if !stateOk || state.Top {
		t.Fatalf("summarized answer did not spell as a scalar state: %+v", answer)
	}
	// p.lo counts 0 → 3 across three trips; the kernel's loop answer
	// must ADMIT 3 (a widened invariant is sound, an answer excluding
	// the true value is not)
	if !kernel.Member(state.Set, []float64{3}) {
		t.Errorf("summary of f(7) excludes the true value 3 for p.lo: %+v", state.Set)
	}
}

func TestObjectSlots_AnAliasedRecordLocalDeclines(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	// `const q = p` reads the whole record; after flattening there is no
	// one value for q to hold, so the recognizer must refuse p its slot
	// family — and the `p.lo` reads then find no slot and decline.
	declaration := summaryDeclarationOf(t,
		"function f(n: number) { const p = { lo: 0, hi: n }; const q = p; return p.lo; }")
	contract := &FunctionContract{Declaration: declaration}
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	if _, ok := KernelSummaryDirect(ctx, []abstractdomain.AbstractValue{exactNumber(t, 7)}, contract); ok {
		t.Errorf("an aliased record local summarized — the alias reads an object the flattening does not build")
	}
}

func TestObjectSlots_ASpreadInTheRecordLiteralDeclines(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	// a spread row names no single key, so the literal has no flat key
	// set to become slots
	declaration := summaryDeclarationOf(t,
		"function f(o: { lo: number }, n: number) { const p = { ...o, hi: n }; return p.hi; }")
	contract := &FunctionContract{Declaration: declaration}
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	if _, ok := KernelSummaryDirect(ctx, []abstractdomain.AbstractValue{exactNumber(t, 1), exactNumber(t, 7)}, contract); ok {
		t.Errorf("a spread record literal summarized — its key set is not the literal's own rows")
	}
}

func TestObjectSlots_TheRecognizerAdmitsAPlainRecordAndNamesItsSlots(t *testing.T) {
	// the recognizer alone — no kernel needed, it reads only syntax
	declaration := summaryDeclarationOf(t,
		"function f(n: number) { const p = { lo: 0, hi: n }; p.lo = p.lo + 1; return p.hi; }")
	body := declaration.Body()
	locals, ok := CollectLocals(body)
	if !ok {
		t.Fatalf("CollectLocals ok = false, want the record local collected")
	}
	flattened := ObjectLocalsOf(body, locals.Locals)
	if len(flattened) != 1 {
		t.Fatalf("ObjectLocalsOf found %d flattened locals, want 1", len(flattened))
	}
	for _, local := range flattened {
		if local.Name != "p" {
			t.Errorf("flattened local name = %q, want p", local.Name)
		}
		if len(local.Keys) != 2 {
			t.Fatalf("flattened local has %d keys, want 2", len(local.Keys))
		}
		if local.Keys[0].SlotName != "p.lo" || local.Keys[1].SlotName != "p.hi" {
			t.Errorf("slot names = %q, %q, want p.lo, p.hi", local.Keys[0].SlotName, local.Keys[1].SlotName)
		}
		if got := ObjectLocalKeySort(local.Keys[0]); got != BindingKindNumber {
			t.Errorf("sort of p.lo = %q, want number", got)
		}
		if got := ObjectLocalKeyTypeof(local.Keys[0]); got != TypeofTagNumber {
			t.Errorf("typeof of p.lo = %q, want number", got)
		}
	}
}

func TestObjectSlots_TheRecognizerDeclinesAKeyTheLiteralNeverDeclared(t *testing.T) {
	// `p.extra = 1` would need a slot the literal never named
	declaration := summaryDeclarationOf(t,
		"function f(n: number) { const p = { lo: 0 }; p.extra = n; return p.lo; }")
	body := declaration.Body()
	locals, ok := CollectLocals(body)
	if !ok {
		t.Fatalf("CollectLocals ok = false, want the record local collected")
	}
	if flattened := ObjectLocalsOf(body, locals.Locals); len(flattened) != 0 {
		t.Errorf("a later-added key flattened — its write would land in no slot")
	}
}

func TestObjectSlots_TheRecognizerDeclinesARecordPassedWhole(t *testing.T) {
	// `g(p)` hands the record to a callee that could read any key or
	// keep the reference
	declaration := summaryDeclarationOf(t,
		"function f(n: number) { const p = { lo: 0, hi: n }; g(p); return p.lo; }")
	body := declaration.Body()
	locals, ok := CollectLocals(body)
	if !ok {
		t.Fatalf("CollectLocals ok = false, want the record local collected")
	}
	if flattened := ObjectLocalsOf(body, locals.Locals); len(flattened) != 0 {
		t.Errorf("a record passed as an argument flattened — the callee observes an object the flattening does not build")
	}
}

func TestObjectSlots_TheRecognizerFlattensANestedRecordByLeafPath(t *testing.T) {
	// a nested object row contributes its own leaves under the row's key:
	// `{ lo: 0, inner: { deep: n } }` names "p.lo" and "p.inner.deep"
	declaration := summaryDeclarationOf(t,
		"function f(n: number) { const p = { lo: 0, inner: { deep: n } }; return p.inner.deep; }")
	body := declaration.Body()
	locals, ok := CollectLocals(body)
	if !ok {
		t.Fatalf("CollectLocals ok = false, want the record local collected")
	}
	flattened := ObjectLocalsOf(body, locals.Locals)
	if len(flattened) != 1 {
		t.Fatalf("ObjectLocalsOf found %d flattened locals, want 1", len(flattened))
	}
	for _, local := range flattened {
		if len(local.Keys) != 2 {
			t.Fatalf("flattened local has %d leaves, want 2", len(local.Keys))
		}
		if local.Keys[0].SlotName != "p.lo" {
			t.Errorf("first leaf slot = %q, want p.lo", local.Keys[0].SlotName)
		}
		if local.Keys[1].SlotName != "p.inner.deep" {
			t.Errorf("second leaf slot = %q, want p.inner.deep", local.Keys[1].SlotName)
		}
	}
}

func TestObjectSlots_TheRecognizerDeclinesAReadOfAnUndeclaredNestedLeaf(t *testing.T) {
	// `p.inner.missing` names no leaf the literal gave a slot
	declaration := summaryDeclarationOf(t,
		"function f(n: number) { const p = { inner: { deep: n } }; return p.inner.missing; }")
	body := declaration.Body()
	locals, ok := CollectLocals(body)
	if !ok {
		t.Fatalf("CollectLocals ok = false, want the record local collected")
	}
	if flattened := ObjectLocalsOf(body, locals.Locals); len(flattened) != 0 {
		t.Errorf("a read of an undeclared nested leaf flattened — its read lands in no slot")
	}
}

func TestObjectSlots_TheRecognizerDeclinesAReadOfAnInteriorNode(t *testing.T) {
	// `p.inner` is a whole record after flattening, not one scalar leaf
	declaration := summaryDeclarationOf(t,
		"function f(n: number) { const p = { inner: { deep: n } }; const q = p.inner; return q.deep; }")
	body := declaration.Body()
	locals, ok := CollectLocals(body)
	if !ok {
		t.Fatalf("CollectLocals ok = false, want the record local collected")
	}
	if flattened := ObjectLocalsOf(body, locals.Locals); len(flattened) != 0 {
		t.Errorf("a read of an interior node flattened — it names no one scalar")
	}
}

func TestObjectSlots_TheRecognizerDeclinesAKeyReadingTheRecordItDeclares(t *testing.T) {
	// `{ lo: 0, hi: p.lo }` reads a slot the declaration has not written
	// yet; flattened, that read would see the absent entry state
	declaration := summaryDeclarationOf(t,
		"function f(n: number) { const p = { lo: n, hi: p.lo }; return p.hi; }")
	body := declaration.Body()
	locals, ok := CollectLocals(body)
	if !ok {
		t.Fatalf("CollectLocals ok = false, want the record local collected")
	}
	if flattened := ObjectLocalsOf(body, locals.Locals); len(flattened) != 0 {
		t.Errorf("a self-reading record literal flattened — its second key would read an unwritten slot")
	}
}
