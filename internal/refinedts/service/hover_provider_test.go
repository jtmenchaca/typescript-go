// The hover seam over a live program: TokenAt's deepest-node walk and
// FormatRefinementAt's stated-annotation path (no kernel required —
// the stated path is facts + formatting).

package service

import (
	"context"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
)

func TestTokenAtDeepestNode(t *testing.T) {
	source := "const answer = 42;\n"
	built, err := ProgramFromSource(source, testSurfaceDir)
	if err != nil {
		t.Fatalf("ProgramFromSource: %v", err)
	}
	defer built.Done()

	at := strings.Index(source, "answer")
	token := TokenAt(built.Entry, at)
	if token == nil {
		t.Fatal("expected a token at the binding name")
	}
	if !ast.IsIdentifier(token) || token.Text() != "answer" {
		t.Fatalf("expected the identifier `answer`, got kind %v", token.Kind)
	}
	// half-open: the position AT the end of the file answers nil
	if TokenAt(built.Entry, len(source)) != nil {
		t.Fatal("a position past the end must answer nil")
	}
}

func TestFormatRefinementAtStatedAnnotation(t *testing.T) {
	source := "import * as z from \"/surface/z.ts\";\n" +
		"const zPct = z.number().min(0).max(100);\n"
	built, err := ProgramFromSource(source, testSurfaceDir)
	if err != nil {
		t.Fatalf("ProgramFromSource: %v", err)
	}
	defer built.Done()

	at := strings.Index(source, "zPct")
	spelled, _, ok := FormatRefinementAt(context.Background(), built.Program, "/main.ts", at, []string{SurfacePath})
	if !ok {
		t.Fatal("expected the stated annotation to spell at the binding name")
	}
	if !strings.Contains(spelled, "100") {
		t.Fatalf("expected the stated window in the spelling, got %q", spelled)
	}
}

func TestFormatRefinementAtNonName(t *testing.T) {
	source := "const answer = 42;\n"
	built, err := ProgramFromSource(source, testSurfaceDir)
	if err != nil {
		t.Fatalf("ProgramFromSource: %v", err)
	}
	defer built.Done()

	// hovering the `=` punctuation: not a name, nothing spelled
	at := strings.Index(source, "=")
	if spelled, _, ok := FormatRefinementAt(context.Background(), built.Program, "/main.ts", at, []string{SurfacePath}); ok {
		t.Fatalf("expected no spelling on punctuation, got %q", spelled)
	}
}
