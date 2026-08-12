package dataflowfacts

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
)

func TestDirectWrites(t *testing.T) {
	_, file := checkerFor(t, `
function f() {
  let x = 1;
  x = 2;
  x++;
  return x;
}
`)
	fn := file.Statements.Nodes[0]
	writes := DirectWrites(fn)
	if _, ok := writes["x"]; !ok {
		t.Errorf("expected x to be a direct write, got %v", writes)
	}
}

func TestReachedWritesIncludesCallArguments(t *testing.T) {
	_, file := checkerFor(t, `
function f(xs: number[]) {
  helper(xs);
}
`)
	fn := file.Statements.Nodes[0]
	writes := ReachedWrites(fn)
	if _, ok := writes["xs"]; !ok {
		t.Errorf("expected xs (handed to a call) to be reached, got %v", writes)
	}
}

func TestStableIn(t *testing.T) {
	t.Run("a bare place with no writes is stable", func(t *testing.T) {
		_, file := checkerFor(t, `
function f() {
  const x = 1;
  if (x > 0) {
    x;
  }
}
`)
		fn := file.Statements.Nodes[0]
		fnDecl := fn.AsFunctionDeclaration()
		ifStmt := fnDecl.Body.AsBlock().Statements.Nodes[1].AsIfStatement()
		symbol := ifStmt.Expression.AsBinaryExpression().Left
		place := PlaceKey{Base: nil, Path: "", BaseName: symbol.Text()}
		if !StableIn(place, []*ast.Node{ifStmt.ThenStatement}, fn) {
			t.Errorf("expected a never-written place to be stable")
		}
	})

	t.Run("a write to the base name breaks stability", func(t *testing.T) {
		_, file := checkerFor(t, `
function f() {
  let x = 1;
  if (x > 0) {
    x = 2;
  }
}
`)
		fn := file.Statements.Nodes[0]
		fnDecl := fn.AsFunctionDeclaration()
		ifStmt := fnDecl.Body.AsBlock().Statements.Nodes[1].AsIfStatement()
		place := PlaceKey{Base: nil, Path: "", BaseName: "x"}
		if StableIn(place, []*ast.Node{ifStmt.ThenStatement}, fn) {
			t.Errorf("expected a write inside the scope to break stability")
		}
	})

	t.Run("arguments poisons every place", func(t *testing.T) {
		_, file := checkerFor(t, `
function f() {
  let x = 1;
  if (x > 0) {
    arguments;
  }
}
`)
		fn := file.Statements.Nodes[0]
		fnDecl := fn.AsFunctionDeclaration()
		ifStmt := fnDecl.Body.AsBlock().Statements.Nodes[1].AsIfStatement()
		place := PlaceKey{Base: nil, Path: "", BaseName: "x"}
		if StableIn(place, []*ast.Node{ifStmt.ThenStatement}, fn) {
			t.Errorf("expected an arguments mention to poison every place")
		}
	})
}
