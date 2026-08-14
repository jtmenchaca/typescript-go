package dataflowfacts

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
)

// TestPlacesPropertyPathsAndIntegerOffsets ports gates_places.test.ts's
// "places: property paths and integer offsets" — the portion of that
// case not blocked on gateKeyOf (path_conditions.go's banner explains
// the block).
func TestPlacesPropertyPathsAndIntegerOffsets(t *testing.T) {
	c, file := checkerFor(t, `
const o = { k: "x", n: 1 };
const i = 0;
const n = 9;
if (o.k === "x") {}
if (i - 1 < 3) {}
if (i + 2 < 3) {}
if (i < n) {}
`)
	conditions := ifConditions(file)

	property := conditions[0].AsBinaryExpression()
	key := PlaceKeyOf(c, property.Left)
	if key == nil {
		t.Fatalf("expected a place")
	}
	if key.Path != ".k" {
		t.Errorf("path = %q, want %q", key.Path, ".k")
	}
	if key.BaseName != "o" {
		t.Errorf("baseName = %q, want %q", key.BaseName, "o")
	}

	minus := conditions[1].AsBinaryExpression()
	plus := conditions[2].AsBinaryExpression()
	below := OffsetPlaceOf(c, minus.Left)
	above := OffsetPlaceOf(c, plus.Left)
	if below == nil || above == nil {
		t.Fatalf("expected offset places")
	}
	if below.Offset != -1 {
		t.Errorf("below.Offset = %d, want -1", below.Offset)
	}
	if above.Offset != 2 {
		t.Errorf("above.Offset = %d, want 2", above.Offset)
	}
	if below.Place.Base != above.Place.Base {
		t.Errorf("below and above should share the same base symbol")
	}
}

func TestEnclosingThisClass(t *testing.T) {
	t.Run("a method body names its own class", func(t *testing.T) {
		_, file := checkerFor(t, `
class Box {
  method(): void {
    this;
  }
}
`)
		var thisNode *ast.Node
		var visit func(node *ast.Node) bool
		visit = func(node *ast.Node) bool {
			if node.Kind == ast.KindThisKeyword {
				thisNode = node
				return true
			}
			return node.ForEachChild(visit)
		}
		file.AsNode().ForEachChild(visit)
		if thisNode == nil {
			t.Fatalf("expected to find a this-keyword node")
		}
		class := EnclosingThisClass(thisNode)
		if class == nil {
			t.Fatalf("expected an enclosing class")
		}
		if !ast.IsClassDeclaration(class) {
			t.Errorf("expected a class declaration")
		}
	})

	t.Run("a static member has no enclosing this-class", func(t *testing.T) {
		_, file := checkerFor(t, `
class Box {
  static method(): void {
    this;
  }
}
`)
		var thisNode *ast.Node
		var visit func(node *ast.Node) bool
		visit = func(node *ast.Node) bool {
			if node.Kind == ast.KindThisKeyword {
				thisNode = node
				return true
			}
			return node.ForEachChild(visit)
		}
		file.AsNode().ForEachChild(visit)
		if thisNode == nil {
			t.Fatalf("expected to find a this-keyword node")
		}
		class := EnclosingThisClass(thisNode)
		if class != nil {
			t.Errorf("expected no enclosing this-class for a static member")
		}
	})

	t.Run("a function declaration stops the search", func(t *testing.T) {
		_, file := checkerFor(t, `
class Box {
  method(): void {
    function inner(): void {
      this;
    }
  }
}
`)
		var thisNode *ast.Node
		var visit func(node *ast.Node) bool
		visit = func(node *ast.Node) bool {
			if node.Kind == ast.KindThisKeyword {
				thisNode = node
				return true
			}
			return node.ForEachChild(visit)
		}
		file.AsNode().ForEachChild(visit)
		if thisNode == nil {
			t.Fatalf("expected to find a this-keyword node")
		}
		class := EnclosingThisClass(thisNode)
		if class != nil {
			t.Errorf("expected no enclosing this-class through a function declaration")
		}
	})
}

func TestSamePlace(t *testing.T) {
	c, file := checkerFor(t, `
const a = 1;
const b = 2;
a;
a;
b;
`)
	var exprStatements []*ast.Node
	for _, statement := range file.Statements.Nodes {
		if ast.IsExpressionStatement(statement) {
			exprStatements = append(exprStatements, statement.AsExpressionStatement().Expression)
		}
	}
	first := PlaceKeyOf(c, exprStatements[0])
	second := PlaceKeyOf(c, exprStatements[1])
	third := PlaceKeyOf(c, exprStatements[2])
	if first == nil || second == nil || third == nil {
		t.Fatalf("expected places for every identifier")
	}
	if !SamePlace(*first, *second) {
		t.Errorf("two reads of `a` should be the same place")
	}
	if SamePlace(*first, *third) {
		t.Errorf("`a` and `b` should not be the same place")
	}
}

func TestTrackedPlaceOf(t *testing.T) {
	tracked := map[string]bool{"x": true, "this": true}
	isTracked := func(name string) bool { return tracked[name] }

	_, file := checkerFor(t, `
x;
x.a;
x["a"];
x.a.b;
y;
f();
`)
	var exprs []*ast.Node
	for _, statement := range file.Statements.Nodes {
		if ast.IsExpressionStatement(statement) {
			exprs = append(exprs, statement.AsExpressionStatement().Expression)
		}
	}

	t.Run("a bare tracked identifier", func(t *testing.T) {
		place := TrackedPlaceOf(exprs[0], isTracked)
		if place == nil {
			t.Fatalf("expected a tracked place")
		}
		if place.Binding != "x" || len(place.Path) != 0 {
			t.Errorf("got %+v", place)
		}
	})

	t.Run("a property access", func(t *testing.T) {
		place := TrackedPlaceOf(exprs[1], isTracked)
		if place == nil {
			t.Fatalf("expected a tracked place")
		}
		if place.Binding != "x" || len(place.Path) != 1 || place.Path[0] != "a" {
			t.Errorf("got %+v", place)
		}
	})

	t.Run("an element access with a literal key", func(t *testing.T) {
		place := TrackedPlaceOf(exprs[2], isTracked)
		if place == nil {
			t.Fatalf("expected a tracked place")
		}
		if place.Binding != "x" || len(place.Path) != 1 || place.Path[0] != "a" {
			t.Errorf("got %+v", place)
		}
	})

	t.Run("a nested property chain", func(t *testing.T) {
		place := TrackedPlaceOf(exprs[3], isTracked)
		if place == nil {
			t.Fatalf("expected a tracked place")
		}
		if place.Binding != "x" || len(place.Path) != 2 || place.Path[0] != "a" || place.Path[1] != "b" {
			t.Errorf("got %+v", place)
		}
	})

	t.Run("an untracked identifier is not a place", func(t *testing.T) {
		place := TrackedPlaceOf(exprs[4], isTracked)
		if place != nil {
			t.Errorf("expected no tracked place, got %+v", place)
		}
	})

	t.Run("a call expression is not a place", func(t *testing.T) {
		place := TrackedPlaceOf(exprs[5], isTracked)
		if place != nil {
			t.Errorf("expected no tracked place, got %+v", place)
		}
	})
}

func TestSameTrackedPlace(t *testing.T) {
	a := TrackedPlace{Binding: "x", Path: []string{"a", "b"}}
	b := TrackedPlace{Binding: "x", Path: []string{"a", "b"}}
	c := TrackedPlace{Binding: "x", Path: []string{"a"}}
	if !SameTrackedPlace(a, b) {
		t.Errorf("expected equal tracked places to compare equal")
	}
	if SameTrackedPlace(a, c) {
		t.Errorf("expected different-length paths to compare unequal")
	}
}

func TestTrackedPlaceOfIndexSegments(t *testing.T) {
	tracked := map[string]bool{"xs": true}
	isTracked := func(name string) bool { return tracked[name] }

	_, file := checkerFor(t, `
xs[0];
xs[1].a;
xs[i];
xs[-1];
xs[1.5];
xs[01];
xs["[0]"];
`)
	var exprs []*ast.Node
	for _, statement := range file.Statements.Nodes {
		if ast.IsExpressionStatement(statement) {
			exprs = append(exprs, statement.AsExpressionStatement().Expression)
		}
	}

	t.Run("a literal index is one segment", func(t *testing.T) {
		place := TrackedPlaceOf(exprs[0], isTracked)
		if place == nil || place.Binding != "xs" || len(place.Path) != 1 || place.Path[0] != "[0]" {
			t.Errorf("got %+v", place)
		}
	})

	t.Run("an index then a key", func(t *testing.T) {
		place := TrackedPlaceOf(exprs[1], isTracked)
		if place == nil || len(place.Path) != 2 || place.Path[0] != "[1]" || place.Path[1] != "a" {
			t.Errorf("got %+v", place)
		}
	})

	for _, row := range []struct {
		name  string
		index int
	}{
		{"a name index is no place", 2},
		{"a negative index is no place", 3},
		{"a fractional index is no place", 4},
		{"a padded index is no place", 5},
	} {
		t.Run(row.name, func(t *testing.T) {
			if place := TrackedPlaceOf(exprs[row.index], isTracked); place != nil {
				t.Errorf("expected no place, got %+v", place)
			}
		})
	}

	t.Run("a string key spelling brackets stays a key", func(t *testing.T) {
		place := TrackedPlaceOf(exprs[6], isTracked)
		if place == nil || len(place.Path) != 1 || place.Path[0] != "[0]" {
			t.Fatalf("got %+v", place)
		}
		// the segment text is the same; the collision argument is that no
		// object carries a key `[0]` AND is read by an index arm, so the
		// two never name two different slots at one place
		if slot, isIndex := IndexSegmentOf(place.Path[0]); !isIndex || slot != 0 {
			t.Errorf("IndexSegmentOf(%q) = %d, %v", place.Path[0], slot, isIndex)
		}
	})
}

func TestIndexSegmentRoundTrip(t *testing.T) {
	for _, slot := range []int{0, 1, 7, 42, 4294967295} {
		spelled := IndexSegment(slot)
		got, isIndex := IndexSegmentOf(spelled)
		if !isIndex || got != slot {
			t.Errorf("IndexSegmentOf(%q) = %d, %v; want %d, true", spelled, got, isIndex, slot)
		}
	}
	for _, key := range []string{"", "a", "[]", "[00]", "[0x1]", "[-1]", "[ 0]", "0", "[0", "0]"} {
		if slot, isIndex := IndexSegmentOf(key); isIndex {
			t.Errorf("IndexSegmentOf(%q) = %d, true; want a key", key, slot)
		}
	}
}

func TestStringLiteralOf(t *testing.T) {
	_, file := checkerFor(t, "\"a\";\n`b`;\n1;\n")
	var exprs []*ast.Node
	for _, statement := range file.Statements.Nodes {
		if ast.IsExpressionStatement(statement) {
			exprs = append(exprs, statement.AsExpressionStatement().Expression)
		}
	}
	if got := StringLiteralOf(exprs[0]); got == nil || *got != "a" {
		t.Errorf("string literal: got %v, want \"a\"", got)
	}
	if got := StringLiteralOf(exprs[1]); got == nil || *got != "b" {
		t.Errorf("no-substitution template: got %v, want \"b\"", got)
	}
	if got := StringLiteralOf(exprs[2]); got != nil {
		t.Errorf("a numeric literal is not a string literal, got %v", *got)
	}
}
