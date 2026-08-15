// split from ir_accessor_calls_test.go — the receiver path and the temp's own reading

package walk

import (
	"testing"
)

/* ── the receiver path ───────────────────────────────────────────── */

func TestAccessorCalls_TheReceiverPathIsTheAccessMinusItsAccessorStep(t *testing.T) {
	ctx, p := accessorCtx(t, "class Box {\n"+
		"  store: number = 0;\n"+
		"  get value(): number { return this.store; }\n"+
		"  run(): number { return this.value; }\n"+
		"}\n")
	_ = ctx
	path, ok := accessorReceiverPathOf(accessorAccessIn(t, p, "run", "value"))
	if !ok || path != "this" {
		t.Errorf("accessorReceiverPathOf(`this.value`) = %q, %v, want \"this\"", path, ok)
	}
}

func TestAccessorCalls_AChainedReceiverSpellsTheWholePrefix(t *testing.T) {
	_, p := accessorCtx(t, "class Inner {\n"+
		"  store: number = 0;\n"+
		"  get value(): number { return this.store; }\n"+
		"}\n"+
		"class Box {\n"+
		"  holder: Inner = new Inner();\n"+
		"  run(): number { return this.holder.value; }\n"+
		"}\n")
	path, ok := accessorReceiverPathOf(accessorAccessIn(t, p, "run", "value"))
	if !ok || path != "this.holder" {
		t.Errorf("accessorReceiverPathOf(`this.holder.value`) = %q, %v, want \"this.holder\"", path, ok)
	}
}

/* ── the temp's own reading ──────────────────────────────────────── */

func TestAccessorCalls_TheTempWearsTheGettersDeclaredReturnSort(t *testing.T) {
	_, p := accessorCtx(t, "class Box {\n"+
		"  a: number = 0;\n"+
		"  s: string = \"\";\n"+
		"  get num(): number { return this.a; }\n"+
		"  get word(): string { return this.s; }\n"+
		"  get plain() { return this.a; }\n"+
		"  run(): number { return this.num; }\n"+
		"}\n")
	context := &LoweringContext{}
	for _, held := range []struct {
		name string
		sort BindingKind
		tag  TypeofTag
	}{
		{"num", BindingKindNumber, TypeofTagNumber},
		{"word", BindingKindString, TypeofTagString},
		// an unannotated getter promises nothing about what comes back
		{"plain", BindingKindUnknown, TypeofTagNone},
	} {
		getter := accessorDeclaredIn(t, p, held.name, false)
		if sort := context.AccessorSortOf(getter); sort != held.sort {
			t.Errorf("%s's temp sort = %q, want %q — the declaration's own annotation", held.name, sort, held.sort)
		}
		if tag := context.AccessorTypeofOf(getter); tag != held.tag {
			t.Errorf("%s's temp typeof = %q, want %q", held.name, tag, held.tag)
		}
	}
}

func TestAccessorCalls_ANonGetterHasNoReturnEvidence(t *testing.T) {
	_, p := accessorCtx(t, accessorSource)
	context := &LoweringContext{}
	setter := accessorDeclaredIn(t, p, "value", true)
	if sort := context.AccessorSortOf(setter); sort != BindingKindUnknown {
		t.Errorf("a setter answered the sort %q, want unknown — it returns nothing", sort)
	}
	if sort := context.AccessorSortOf(nil); sort != BindingKindUnknown {
		t.Errorf("a nil declaration answered the sort %q, want unknown", sort)
	}
}
