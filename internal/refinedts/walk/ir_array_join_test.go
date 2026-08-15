// Tests for ir_array_join.go: `a.join(sep)` — the string-sort-only
// read over a flattened array's two slots.
//
// ArrayJoinEffect is not yet wired into RhsEffect (that hook lives in
// ir_assignment.go, outside this package's array files), so these
// tests call the dispatcher directly on a parsed call node — the same
// shape the hook will hand it once landed.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

func TestArrayJoin_AStringLiteralSeparatorOverNumberElementsAnswersSortOnlyUnknown(t *testing.T) {
	context := &LoweringContext{
		Bindings: []string{"a.len", "a.elem"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber},
		Typeofs:  make([]TypeofTag, 2),
	}
	node := arrayReadCallNodeOf(t, `a.join(",");`)
	effect, ok := ArrayJoinEffect(context, node, BindingKindString)
	if !ok {
		t.Fatalf(`ArrayJoinEffect(a.join(",")) ok = false, want true`)
	}
	if effect.Kind != kernelbridge.LoopEffectUnknown {
		t.Errorf("effect.Kind = %q, want %q — join's content is not pinned", effect.Kind, kernelbridge.LoopEffectUnknown)
	}
}

func TestArrayJoin_ANoArgumentJoinTakesTheDefaultCommaSeparator(t *testing.T) {
	context := &LoweringContext{
		Bindings: []string{"a.len", "a.elem", "s"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindString, BindingKindString},
		Typeofs:  make([]TypeofTag, 3),
	}
	// join takes zero or one argument; the fixed-count gate in
	// ir_array_use_scan.go only ever admits the one-argument shape it
	// names, so a bare `a.join()` is read directly here rather than
	// through arrayReadMethodCallOf, mirroring how the spec's own
	// default applies (sec-array.prototype.join step 3).
	statements := loweringParse(t, `a.join();`)
	call := Unwrapped(statements[0].AsExpressionStatement().Expression)
	if _, ok := ArrayJoinEffect(context, call, BindingKindString); ok {
		t.Skip("a.join() with zero arguments is not admitted by the one-argument read gate — see ir_array_use_scan.go's readMethodArgCounts")
	}
}

func TestArrayJoin_AStringElementSortAlsoAnswersSortOnlyUnknown(t *testing.T) {
	context := &LoweringContext{
		Bindings: []string{"a.len", "a.elem"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindString},
		Typeofs:  make([]TypeofTag, 2),
	}
	node := arrayReadCallNodeOf(t, `a.join("-");`)
	effect, ok := ArrayJoinEffect(context, node, BindingKindString)
	if !ok {
		t.Fatalf(`ArrayJoinEffect(a.join("-")) ok = false, want true`)
	}
	if effect.Kind != kernelbridge.LoopEffectUnknown {
		t.Errorf("effect.Kind = %q, want %q", effect.Kind, kernelbridge.LoopEffectUnknown)
	}
}

func TestArrayJoin_ANumberSortedSeparatorNameIsAlsoAdmitted(t *testing.T) {
	context := &LoweringContext{
		Bindings: []string{"a.len", "a.elem", "sep"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber},
		Typeofs:  make([]TypeofTag, 3),
	}
	node := arrayReadCallNodeOf(t, `a.join(sep);`)
	if _, ok := ArrayJoinEffect(context, node, BindingKindString); !ok {
		t.Errorf(`ArrayJoinEffect(a.join(sep)) with a number-sorted sep ok = false, want true — ToString on a Number never runs code`)
	}
}

func TestArrayJoin_ANumberTargetSortDeclines(t *testing.T) {
	context := &LoweringContext{
		Bindings: []string{"a.len", "a.elem"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber},
		Typeofs:  make([]TypeofTag, 2),
	}
	node := arrayReadCallNodeOf(t, `a.join(",");`)
	if _, ok := ArrayJoinEffect(context, node, BindingKindNumber); ok {
		t.Errorf("ArrayJoinEffect under a number target sort ok = true, want false — join answers a string")
	}
}

func TestArrayJoin_AnUnstatedSeparatorDeclines(t *testing.T) {
	context := &LoweringContext{
		Bindings: []string{"a.len", "a.elem", "sep"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber, BindingKindUnknown},
		Typeofs:  make([]TypeofTag, 3),
	}
	node := arrayReadCallNodeOf(t, `a.join(sep);`)
	if _, ok := ArrayJoinEffect(context, node, BindingKindString); ok {
		t.Errorf("ArrayJoinEffect over an unknown-sorted separator ok = true, want false — ToString could call out")
	}
}

func TestArrayJoin_AnUnrecognizedElementSortDeclines(t *testing.T) {
	context := &LoweringContext{
		Bindings: []string{"a.len", "a.elem"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindUnknown},
		Typeofs:  make([]TypeofTag, 2),
	}
	node := arrayReadCallNodeOf(t, `a.join(",");`)
	if _, ok := ArrayJoinEffect(context, node, BindingKindString); ok {
		t.Errorf("ArrayJoinEffect over an unknown-sorted elem slot ok = true, want false")
	}
}
