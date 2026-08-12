package dataflowfacts

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
)

// TestLiteralSpelling ports the canonicalization gates_places.test.ts
// exercises through gateKeyOf — LiteralSpelling is the ported half
// (gateKeyOf itself is blocked; see path_conditions.go's banner).
func TestLiteralSpelling(t *testing.T) {
	_, file := checkerFor(t, `
"up";
5;
-5;
true;
false;
`+"`tpl`;\n"+`
1 + 1;
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
		want string
	}{
		{"a string literal", 0, "s:up"},
		{"a numeric literal", 1, "n:5"},
		{"a negated numeric literal", 2, "n:-5"},
		{"true", 3, "b:true"},
		{"false", 4, "b:false"},
		{"a no-substitution template literal", 5, "s:tpl"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := LiteralSpelling(exprs[c.i])
			if got == nil {
				t.Fatalf("expected a spelling")
			}
			if *got != c.want {
				t.Errorf("got %q, want %q", *got, c.want)
			}
		})
	}

	t.Run("a non-literal expression has no spelling", func(t *testing.T) {
		if got := LiteralSpelling(exprs[6]); got != nil {
			t.Errorf("expected nil, got %q", *got)
		}
	})
}
