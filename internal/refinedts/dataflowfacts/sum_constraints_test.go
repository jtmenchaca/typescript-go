package dataflowfacts

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
)

func TestSumPlacesOf(t *testing.T) {
	c, file := checkerFor(t, `
const offset = 0;
const length = 1;
offset + length;
offset + length - 1;
offset + length + 2;
offset;
`)
	var exprs []*ast.Node
	for _, statement := range file.Statements.Nodes {
		if ast.IsExpressionStatement(statement) {
			exprs = append(exprs, statement.AsExpressionStatement().Expression)
		}
	}

	t.Run("a plain two-place sum", func(t *testing.T) {
		got := SumPlacesOf(c, exprs[0])
		if got == nil {
			t.Fatalf("expected sum places")
		}
		if got.Offset != 0 {
			t.Errorf("offset = %v, want 0", got.Offset)
		}
	})

	t.Run("a sum minus a literal", func(t *testing.T) {
		got := SumPlacesOf(c, exprs[1])
		if got == nil {
			t.Fatalf("expected sum places")
		}
		if got.Offset != -1 {
			t.Errorf("offset = %v, want -1", got.Offset)
		}
	})

	t.Run("a sum plus a literal", func(t *testing.T) {
		got := SumPlacesOf(c, exprs[2])
		if got == nil {
			t.Fatalf("expected sum places")
		}
		if got.Offset != 2 {
			t.Errorf("offset = %v, want 2", got.Offset)
		}
	})

	t.Run("a bare place is not a sum", func(t *testing.T) {
		if got := SumPlacesOf(c, exprs[3]); got != nil {
			t.Errorf("expected nil, got %+v", got)
		}
	})
}

func TestSumExitConstraints(t *testing.T) {
	_, file := checkerFor(t, `
while (true) {}
`)
	loop := file.Statements.Nodes[0]
	if _, ok := SumExitConstraintsOf(loop); ok {
		t.Fatalf("expected no rows before NoteSumExitConstraints")
	}
	rows := []SumConstraint{{Offset: 1}}
	NoteSumExitConstraints(loop, rows)
	got, ok := SumExitConstraintsOf(loop)
	if !ok {
		t.Fatalf("expected rows after NoteSumExitConstraints")
	}
	if len(got) != 1 || got[0].Offset != 1 {
		t.Errorf("got %+v", got)
	}
}
