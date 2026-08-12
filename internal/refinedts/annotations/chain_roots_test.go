// Ported from chain_roots.test.ts.

package annotations

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
)

func TestRootsInSurface_AShadowingZIsNotTheSurface(t *testing.T) {
	// symbols, not names: a LOCAL `const z = {...}` shadowing the
	// import must not read as the surface
	p := newTestProgramWithSource(t, "const z = { number: () => 0 };\nconst X = z.number();\n")
	for _, statement := range p.program.Entry.Statements.Nodes {
		if !ast.IsVariableStatement(statement) {
			continue
		}
		for _, declaration := range statement.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes {
			varDecl := declaration.AsVariableDeclaration()
			if ast.IsIdentifier(varDecl.Name()) && varDecl.Name().AsIdentifier().Text == "X" {
				if RootsInSurface(p.program, varDecl.Initializer) {
					t.Errorf("RootsInSurface(shadowed z.number()) = true, want false")
				}
			}
		}
	}
}
