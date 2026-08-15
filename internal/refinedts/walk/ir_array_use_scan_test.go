// Tests for the use-scan additions in ir_array_use_scan.go: the six
// non-callback Array-method forms (indexOf, lastIndexOf, includes,
// at, join, pop, shift) admitted into the flattened recognizer
// alongside push/index/length/for-of.
package walk

import "testing"

func TestArrayUseScan_IndexOfAdmitsTheArray(t *testing.T) {
	declaration := summaryDeclarationOf(t,
		"function f(n: number) { const a = [1, 2]; return a.indexOf(n); }")
	body := declaration.Body()
	locals, ok := CollectLocals(body)
	if !ok {
		t.Fatalf("CollectLocals ok = false, want the array local collected")
	}
	if flattened := ArrayLocalsOf(body, locals.Locals, nil); len(flattened) != 1 {
		t.Errorf("ArrayLocalsOf found %d flattened arrays, want 1 — indexOf should not decline the array", len(flattened))
	}
}

func TestArrayUseScan_LastIndexOfAdmitsTheArray(t *testing.T) {
	declaration := summaryDeclarationOf(t,
		"function f(n: number) { const a = [1, 2]; return a.lastIndexOf(n); }")
	body := declaration.Body()
	locals, ok := CollectLocals(body)
	if !ok {
		t.Fatalf("CollectLocals ok = false, want the array local collected")
	}
	if flattened := ArrayLocalsOf(body, locals.Locals, nil); len(flattened) != 1 {
		t.Errorf("ArrayLocalsOf found %d flattened arrays, want 1", len(flattened))
	}
}

func TestArrayUseScan_IncludesAdmitsTheArray(t *testing.T) {
	declaration := summaryDeclarationOf(t,
		"function f(n: number) { const a = [1, 2]; return a.includes(n); }")
	body := declaration.Body()
	locals, ok := CollectLocals(body)
	if !ok {
		t.Fatalf("CollectLocals ok = false, want the array local collected")
	}
	if flattened := ArrayLocalsOf(body, locals.Locals, nil); len(flattened) != 1 {
		t.Errorf("ArrayLocalsOf found %d flattened arrays, want 1", len(flattened))
	}
}

func TestArrayUseScan_AtAdmitsTheArray(t *testing.T) {
	declaration := summaryDeclarationOf(t,
		"function f(n: number) { const a = [1, 2]; return a.at(n); }")
	body := declaration.Body()
	locals, ok := CollectLocals(body)
	if !ok {
		t.Fatalf("CollectLocals ok = false, want the array local collected")
	}
	if flattened := ArrayLocalsOf(body, locals.Locals, nil); len(flattened) != 1 {
		t.Errorf("ArrayLocalsOf found %d flattened arrays, want 1", len(flattened))
	}
}

func TestArrayUseScan_JoinAdmitsTheArray(t *testing.T) {
	declaration := summaryDeclarationOf(t,
		`function f(s: string) { const a = [1, 2]; return a.join(","); }`)
	body := declaration.Body()
	locals, ok := CollectLocals(body)
	if !ok {
		t.Fatalf("CollectLocals ok = false, want the array local collected")
	}
	if flattened := ArrayLocalsOf(body, locals.Locals, nil); len(flattened) != 1 {
		t.Errorf("ArrayLocalsOf found %d flattened arrays, want 1", len(flattened))
	}
}

func TestArrayUseScan_PopAdmitsTheArray(t *testing.T) {
	declaration := summaryDeclarationOf(t,
		"function f(n: number) { const a = [1, 2]; a.pop(); return a.length; }")
	body := declaration.Body()
	locals, ok := CollectLocals(body)
	if !ok {
		t.Fatalf("CollectLocals ok = false, want the array local collected")
	}
	if flattened := ArrayLocalsOf(body, locals.Locals, nil); len(flattened) != 1 {
		t.Errorf("ArrayLocalsOf found %d flattened arrays, want 1 — pop is now a landed shrink, not a decline", len(flattened))
	}
}

func TestArrayUseScan_ShiftAdmitsTheArray(t *testing.T) {
	declaration := summaryDeclarationOf(t,
		"function f(n: number) { const a = [1, 2]; a.shift(); return a.length; }")
	body := declaration.Body()
	locals, ok := CollectLocals(body)
	if !ok {
		t.Fatalf("CollectLocals ok = false, want the array local collected")
	}
	if flattened := ArrayLocalsOf(body, locals.Locals, nil); len(flattened) != 1 {
		t.Errorf("ArrayLocalsOf found %d flattened arrays, want 1", len(flattened))
	}
}

func TestArrayUseScan_APopWithAnArgumentStillDeclines(t *testing.T) {
	declaration := summaryDeclarationOf(t,
		"function f(n: number) { const a = [1, 2]; a.pop(n); return a.length; }")
	body := declaration.Body()
	locals, ok := CollectLocals(body)
	if !ok {
		t.Fatalf("CollectLocals ok = false, want the array local collected")
	}
	if flattened := ArrayLocalsOf(body, locals.Locals, nil); len(flattened) != 0 {
		t.Errorf("ArrayLocalsOf found %d flattened arrays, want 0 — pop takes no argument", len(flattened))
	}
}

func TestArrayUseScan_AnIndexOfWithASecondFromIndexArgumentDeclines(t *testing.T) {
	declaration := summaryDeclarationOf(t,
		"function f(n: number) { const a = [1, 2]; return a.indexOf(n, 1); }")
	body := declaration.Body()
	locals, ok := CollectLocals(body)
	if !ok {
		t.Fatalf("CollectLocals ok = false, want the array local collected")
	}
	if flattened := ArrayLocalsOf(body, locals.Locals, nil); len(flattened) != 0 {
		t.Errorf("ArrayLocalsOf found %d flattened arrays, want 0 — the two-argument form is not read", len(flattened))
	}
}

func TestArrayUseScan_AnArgumentThatMentionsTheArrayStillDeclines(t *testing.T) {
	declaration := summaryDeclarationOf(t,
		"function f() { const a = [1, 2]; return a.includes(a as unknown as number); }")
	body := declaration.Body()
	locals, ok := CollectLocals(body)
	if !ok {
		t.Fatalf("CollectLocals ok = false, want the array local collected")
	}
	if flattened := ArrayLocalsOf(body, locals.Locals, nil); len(flattened) != 0 {
		t.Errorf("ArrayLocalsOf found %d flattened arrays, want 0 — the argument reads a as a whole value", len(flattened))
	}
}
