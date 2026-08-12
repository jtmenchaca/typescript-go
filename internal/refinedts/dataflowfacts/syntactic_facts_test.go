package dataflowfacts

import (
	"testing"
)

func TestAssignedNameSetUnfiltered(t *testing.T) {
	_, file := checkerFor(t, `
function f(xs: number[]) {
  xs.push(1);
  helper(xs);
}
`)
	fn := file.Statements.Nodes[0]
	names := AssignedNameSetUnfiltered(fn)
	if _, ok := names["xs"]; !ok {
		t.Errorf("expected xs to be counted (unfiltered), got %v", names)
	}
}

func TestAssignedDirectSetExcludesCallMediatedWrites(t *testing.T) {
	_, file := checkerFor(t, `
function f(xs: number[]) {
  let y = 1;
  y = 2;
  helper(xs);
}
`)
	fn := file.Statements.Nodes[0]
	names := AssignedDirectSet(fn)
	if _, ok := names["y"]; !ok {
		t.Errorf("expected y (a direct assignment) to be counted, got %v", names)
	}
	if _, ok := names["xs"]; ok {
		t.Errorf("expected xs (only call-mediated) to be excluded, got %v", names)
	}
}

func TestAssignedIdentifierNames(t *testing.T) {
	_, file := checkerFor(t, `
function f(o: { k: number }) {
  let y = 1;
  y = 2;
  o.k = 3;
}
`)
	fn := file.Statements.Nodes[0]
	names := AssignedIdentifierNames(fn)
	if _, ok := names["y"]; !ok {
		t.Errorf("expected y to be an identifier-spelled assignment target, got %v", names)
	}
	if _, ok := names["o"]; ok {
		t.Errorf("expected o.k = 3 not to count as an identifier target, got %v", names)
	}
}

func TestDeclaredNameSet(t *testing.T) {
	_, file := checkerFor(t, `
function f() {
  const x = 1;
  const [a, b] = [1, 2];
  function inner() {}
}
`)
	fn := file.Statements.Nodes[0]
	names := DeclaredNameSet(fn)
	for _, want := range []string{"x", "a", "b", "inner"} {
		if _, ok := names[want]; !ok {
			t.Errorf("expected %q to be declared, got %v", want, names)
		}
	}
}

func TestObservedNamesOfIsSortedAndIncludesThis(t *testing.T) {
	_, file := checkerFor(t, `
function f() {
  this;
  y;
  a;
}
`)
	fn := file.Statements.Nodes[0]
	names := ObservedNamesOf(fn)
	found := map[string]bool{}
	for _, name := range names {
		found[name] = true
	}
	if !found["this"] || !found["y"] || !found["a"] {
		t.Errorf("expected this, y, and a to be observed, got %v", names)
	}
	for i := 1; i < len(names); i++ {
		if names[i-1] > names[i] {
			t.Errorf("expected sorted output, got %v", names)
			break
		}
	}
}

func TestCalleeLocalSet(t *testing.T) {
	_, file := checkerFor(t, `
function f(a: number, b: number) {
  const c = a + b;
}
`)
	fn := file.Statements.Nodes[0]
	names := CalleeLocalSet(fn)
	for _, want := range []string{"a", "b", "c"} {
		if _, ok := names[want]; !ok {
			t.Errorf("expected %q to be a callee-local name, got %v", want, names)
		}
	}
}

func TestTargetNames(t *testing.T) {
	_, file := checkerFor(t, `
let x = 1;
let o = { k: 1 };
[x, o.k] = [2, 3];
`)
	assign := file.Statements.Nodes[2].AsExpressionStatement().Expression.AsBinaryExpression()
	into := map[string]struct{}{}
	TargetNames(assign.Left, into)
	if _, ok := into["x"]; !ok {
		t.Errorf("expected x in the destructured target, got %v", into)
	}
	if _, ok := into["o"]; !ok {
		t.Errorf("expected o (the root of o.k) in the destructured target, got %v", into)
	}
}
