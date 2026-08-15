// split from ir_opaque_havoc_test.go — the opaque call

package walk

import (
	"testing"
)

/* ── the opaque call ─────────────────────────────────────────────── */

func TestOpaqueCallHavoc_AnUnresolvableCalleeHavocsItsTargetAndTheLeavesItWasHanded(t *testing.T) {
	context := &LoweringContext{
		Bindings: []string{"out", "p.lo", "p.hi", "n"},
		Sorts: []BindingKind{
			BindingKindNumber, BindingKindNumber, BindingKindNumber, BindingKindNumber,
		},
		Typeofs: []TypeofTag{
			TypeofTagNumber, TypeofTagNumber, TypeofTagNumber, TypeofTagNumber,
		},
	}
	statements := havocParse(t, "out = fetchish(p, n);")
	call := Unwrapped(statements[0].AsExpressionStatement().Expression).AsBinaryExpression().Right
	lowered, ok := OpaqueCallHavoc(context, Unwrapped(call), 0)
	if !ok {
		t.Fatalf("an unresolvable call declined")
	}
	targets := havocTargets(t, lowered)
	// the mentioned leaves first, the call's own value last
	want := []int{1, 2, 0}
	if len(targets) != len(want) {
		t.Fatalf("targets = %v, want %v — p's leaves and the target slot", targets, want)
	}
	for index, slot := range want {
		if targets[index] != slot {
			t.Errorf("targets = %v, want %v", targets, want)
			break
		}
	}
}

func TestOpaqueCallHavoc_ABareCallStillHavocsTheObjectsItWasHanded(t *testing.T) {
	context := &LoweringContext{
		Bindings: []string{"xs.len", "xs.elem"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber},
		Typeofs:  []TypeofTag{TypeofTagNumber, TypeofTagNumber},
	}
	statements := havocParse(t, "consume(xs);")
	call := Unwrapped(statements[0].AsExpressionStatement().Expression)
	lowered, ok := OpaqueCallHavoc(context, call, -1)
	if !ok {
		t.Fatalf("a bare unresolvable call declined")
	}
	targets := havocTargets(t, lowered)
	if len(targets) != 2 || targets[0] != 0 || targets[1] != 1 {
		t.Errorf("targets = %v, want [0 1] — the array's two slots; nothing reads the value", targets)
	}
}

func TestOpaqueCallHavoc_AReceiverThatIsAFlattenedLocalIsHavockedToo(t *testing.T) {
	// `p.y.then(cb)` hands p out through the RECEIVER, not an argument
	context := &LoweringContext{
		Bindings: []string{"p.a", "p.b"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber},
		Typeofs:  []TypeofTag{TypeofTagNumber, TypeofTagNumber},
	}
	statements := havocParse(t, "p.a.then(cb);")
	call := Unwrapped(statements[0].AsExpressionStatement().Expression)
	lowered, ok := OpaqueCallHavoc(context, call, -1)
	if !ok {
		t.Fatalf("a call on a flattened local's member declined")
	}
	targets := havocTargets(t, lowered)
	if len(targets) != 2 || targets[0] != 0 || targets[1] != 1 {
		t.Errorf("targets = %v, want [0 1] — the receiver's leaves", targets)
	}
}

func TestOpaqueHavoc_ACallbackThatWritesAnOuterSlotHavocsIt(t *testing.T) {
	// the callee may CALL the callback, and the callback writes this
	// body's slot. Stopping the scan at the function boundary would leave
	// `total`'s knowledge standing across a write the walk never saw.
	context := &LoweringContext{
		Bindings: []string{"total", "xs.len", "xs.elem", "untouched"},
		Sorts: []BindingKind{
			BindingKindNumber, BindingKindNumber, BindingKindNumber, BindingKindNumber,
		},
		Typeofs: []TypeofTag{
			TypeofTagNumber, TypeofTagNumber, TypeofTagNumber, TypeofTagNumber,
		},
	}
	statements := havocParse(t, "xs.forEachish(v => { total **= v; });")
	lowered, ok := OpaqueHavocStatements(context, statements[0])
	if !ok {
		t.Fatalf("a callback-taking call declined")
	}
	targets := havocTargets(t, lowered)
	want := []int{0, 1, 2}
	if len(targets) != len(want) {
		t.Fatalf("targets = %v, want %v — total (written inside the callback) and xs's two slots", targets, want)
	}
	for index, slot := range want {
		if targets[index] != slot {
			t.Errorf("targets = %v, want %v", targets, want)
			break
		}
	}
	for _, target := range targets {
		if target == 3 {
			t.Errorf("the untouched slot 3 was havocked")
		}
	}
}

func TestOpaqueCallHavoc_ACallbackArgumentsWritesRideThroughTheCallRouteToo(t *testing.T) {
	context := &LoweringContext{
		Bindings: []string{"seen", "p.a"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber},
		Typeofs:  []TypeofTag{TypeofTagNumber, TypeofTagNumber},
	}
	statements := havocParse(t, "mystery(p, () => { seen **= 1; });")
	call := Unwrapped(statements[0].AsExpressionStatement().Expression)
	lowered, ok := OpaqueCallHavoc(context, call, -1)
	if !ok {
		t.Fatalf("a call with a writing callback argument declined")
	}
	targets := havocTargets(t, lowered)
	if len(targets) != 2 || targets[0] != 0 || targets[1] != 1 {
		t.Errorf("targets = %v, want [0 1] — the callback's write and p's leaf", targets)
	}
}

func TestOpaqueHavoc_ACallbacksOwnReturnDoesNotDeclineTheStatement(t *testing.T) {
	// a return inside a CALLBACK returns from the callback, not from this
	// body, so it is not the control transfer the impossibility scan
	// refuses
	context := &LoweringContext{
		Bindings: []string{"xs.len", "xs.elem"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber},
		Typeofs:  []TypeofTag{TypeofTagNumber, TypeofTagNumber},
	}
	statements := havocParse(t, "xs.mapish(function (v) { return v + 1; });")
	lowered, ok := OpaqueHavocStatements(context, statements[0])
	if !ok {
		t.Fatalf("a callback containing a return declined — the return is the callback's, not the body's")
	}
	targets := havocTargets(t, lowered)
	if len(targets) != 2 || targets[0] != 0 || targets[1] != 1 {
		t.Errorf("targets = %v, want [0 1] — xs's two slots", targets)
	}
}

func TestOpaqueHavoc_ThisBodysOwnReturnDeclines(t *testing.T) {
	// a havoc writes slots and falls through; a return raises the done
	// flag and stops the block. Swallowing one would leave the flag down
	// and every later statement walked as if it ran.
	context := &LoweringContext{
		Bindings: []string{"x", "#done", "#ret"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber, BindingKindUnknown},
		Typeofs:  []TypeofTag{TypeofTagNumber, TypeofTagNumber, TypeofTagNone},
		Result:   &LoweringResult{Done: 1, Ret: 2},
	}
	statements := havocParse(t, "try { return weird`x`; } finally { }")
	if _, ok := OpaqueHavocStatements(context, statements[0]); ok {
		t.Errorf("a statement carrying this body's own return havocked — the raise would be dropped")
	}
}
