package dataflowfacts

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
)

// TestReferenceTyped ports the reference-vs-value distinction
// alias_analysis.test.ts exercises end to end (through the full check
// pipeline, which this directory does not have — service/ and
// kernel_bridge's kernel loader are out of scope here). This tests the
// same distinction directly against ReferenceTyped: array and object
// bindings are reference-typed, a number binding is not.
func TestReferenceTyped(t *testing.T) {
	c, file := checkerFor(t, `
const xs = [1, 2];
const n = 5;
xs;
n;
`)
	var exprs []*ast.Node
	for _, statement := range file.Statements.Nodes {
		if ast.IsExpressionStatement(statement) {
			exprs = append(exprs, statement.AsExpressionStatement().Expression)
		}
	}
	if !ReferenceTyped(c, exprs[0]) {
		t.Errorf("expected an array binding to be reference-typed")
	}
	if ReferenceTyped(c, exprs[1]) {
		t.Errorf("expected a number binding not to be reference-typed")
	}
}

func TestReferenceTypedSyntacticShortcuts(t *testing.T) {
	c, file := checkerFor(t, `
"a";
1;
true;
null;
[1, 2];
({ a: 1 });
(() => {});
`)
	var exprs []*ast.Node
	for _, statement := range file.Statements.Nodes {
		if ast.IsExpressionStatement(statement) {
			exprs = append(exprs, statement.AsExpressionStatement().Expression)
		}
	}
	cases := []struct {
		name string
		i    int
		want bool
	}{
		{"a string literal is not a reference", 0, false},
		{"a numeric literal is not a reference", 1, false},
		{"a boolean literal is not a reference", 2, false},
		{"null is not a reference", 3, false},
		{"an array literal is a reference", 4, true},
		{"an object literal is a reference", 5, true},
		{"an arrow function is a reference", 6, true},
	}
	for _, c2 := range cases {
		t.Run(c2.name, func(t *testing.T) {
			if got := ReferenceTyped(c, exprs[c2.i]); got != c2.want {
				t.Errorf("got %v, want %v", got, c2.want)
			}
		})
	}
}

func TestReadOnlyArrayMethodsAndStringReadMethods(t *testing.T) {
	if _, ok := ReadOnlyArrayMethods["push"]; ok {
		t.Errorf("push mutates — must not be read-only")
	}
	if _, ok := ReadOnlyArrayMethods["map"]; !ok {
		t.Errorf("map is read-only")
	}
	if _, ok := StringReadMethods["split"]; !ok {
		t.Errorf("split is a string read")
	}
	if _, ok := StringReadMethods["push"]; ok {
		t.Errorf("push is not a string method")
	}
}
