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

func TestArrayUseScan_ConcatWithAPlainArgumentAdmitsTheArray(t *testing.T) {
	declaration := summaryDeclarationOf(t,
		"function f(x: number) { const a = [1, 2]; return a.concat(x); }")
	body := declaration.Body()
	locals, ok := CollectLocals(body)
	if !ok {
		t.Fatalf("CollectLocals ok = false, want the array local collected")
	}
	if flattened := ArrayLocalsOf(body, locals.Locals, nil); len(flattened) != 1 {
		t.Errorf("ArrayLocalsOf found %d flattened arrays, want 1 — concat is now a recognized read-form, not a decline", len(flattened))
	}
}

func TestArrayUseScan_ConcatWithNoArgumentStillAdmitsTheArray(t *testing.T) {
	declaration := summaryDeclarationOf(t,
		"function f() { const a = [1, 2]; return a.concat(); }")
	body := declaration.Body()
	locals, ok := CollectLocals(body)
	if !ok {
		t.Fatalf("CollectLocals ok = false, want the array local collected")
	}
	if flattened := ArrayLocalsOf(body, locals.Locals, nil); len(flattened) != 1 {
		t.Errorf("ArrayLocalsOf found %d flattened arrays, want 1 — a.concat() is a legal zero-argument copy", len(flattened))
	}
}

func TestArrayUseScan_ConcatWithASpreadArgumentDeclines(t *testing.T) {
	declaration := summaryDeclarationOf(t,
		"function f(xs: number[]) { const a = [1, 2]; return a.concat(...xs); }")
	body := declaration.Body()
	locals, ok := CollectLocals(body)
	if !ok {
		t.Fatalf("CollectLocals ok = false, want the array local collected")
	}
	if flattened := ArrayLocalsOf(body, locals.Locals, nil); len(flattened) != 0 {
		t.Errorf("ArrayLocalsOf found %d flattened arrays, want 0 — a spread argument names no fixed count", len(flattened))
	}
}

func TestArrayUseScan_ConcatOfAnArrayParameterAccumulatorAdmitsTheParameter(t *testing.T) {
	// the Text.tsx reduce shape the brief names: `xs.reduce((acc: number[],
	// x) => acc.concat(x), [])` — `acc` is an ARRAY-TYPED PARAMETER of the
	// inner arrow, and its one use is `acc.concat(x)`. ArrayParameterOf
	// (ir_array_parameters.go) gates on usesAreAllArrayFormsFrom exactly as
	// ArrayLocalsOf does for a declared local; this pins the parameter path
	// directly rather than through the outer reduce lowering (a sibling's
	// territory).
	declaration := summaryDeclarationOf(t,
		"function g(acc: number[], x: number) { return acc.concat(x); }")
	parameter := declaration.Parameters()[0]
	if _, ok := ArrayParameterOf(nil, nil, declaration.Body(), parameter); !ok {
		t.Errorf("ArrayParameterOf(acc) declined — concat should no longer block the array-parameter recognizer")
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
