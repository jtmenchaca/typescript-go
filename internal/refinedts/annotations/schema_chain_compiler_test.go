// Interface tests for CompileAnnotation. Ported from
// schema_chain_compiler.test.ts's static-chain cases; the entry/
// program helpers replace programFromSource + the real surface
// module (service/ tier, unported -- see
// annotations_test_helpers_test.go's header).

package annotations

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// compileTopLevel compiles every top-level `const NAME = z...` in
// order, returning the named one's Compiled -- the TS test's
// `compiled` helper, minus the registry threading which
// CompileAnnotation.CompileAnnotation itself needs explicit.
func compileTopLevel(t *testing.T, source string, name string) Compiled {
	t.Helper()
	p := newTestProgram(t, source)
	registry := AnnotationRegistry{}
	var found *Compiled
	for _, statement := range p.program.Entry.Statements.Nodes {
		if !ast.IsVariableStatement(statement) {
			continue
		}
		for _, declaration := range statement.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes {
			varDecl := declaration.AsVariableDeclaration()
			if varDecl.Initializer == nil || !ast.IsIdentifier(varDecl.Name()) {
				continue
			}
			if !RootsInSurface(p.program, varDecl.Initializer) {
				continue
			}
			c := CompileAnnotation(p.program, varDecl.Initializer, registry)
			if IsUnsupported(c) {
				if varDecl.Name().AsIdentifier().Text == name {
					found = &c
				}
				continue
			}
			symbol := p.program.Checker.GetSymbolAtLocation(varDecl.Name())
			if symbol != nil {
				registry[symbol] = c.Annotation
			}
			if varDecl.Name().AsIdentifier().Text == name {
				found = &c
			}
		}
	}
	if found == nil {
		t.Fatalf("no annotation named %s", name)
	}
	return *found
}

func TestCompileAnnotation_NumberChainCompilesToItsForms(t *testing.T) {
	c := compileTopLevel(t, "const X = z.number();\n", "X")
	if IsUnsupported(c) {
		t.Fatalf("unexpected unsupported: %s", c.Unsupported.Unsupported)
	}
	set := derefSet(c.Annotation.Set)
	if len(set.Forms) != 1 || set.Forms[0].Form != refinementsets.FormAtLeast {
		t.Errorf("z.number() forms = %+v, want a single atLeast(-Inf)", set.Forms)
	}
}

func TestCompileAnnotation_SurfaceRulesRefuseAtCompile(t *testing.T) {
	// a refine outside the readable guard language stays a runtime
	// check: the annotation compiles with its base set (positions
	// checked against it alert instead of accepting)
	unread := compileTopLevel(t, "const X = z.number().refine(() => true);\n", "X")
	if IsUnsupported(unread) {
		t.Fatalf("unexpected unsupported: %s", unread.Unsupported.Unsupported)
	}
	plain := compileTopLevel(t, "const X = z.number();\n", "X")
	if !sameSetStructurally(derefSet(unread.Annotation.Set), derefSet(plain.Annotation.Set)) {
		t.Errorf("unread refine's base set diverged from the bare z.number() set")
	}
	if !unread.Annotation.Unread {
		t.Errorf("expected Unread=true on the unparsed refine")
	}
}

func TestCompileAnnotation_StringChainsCompileRepetitionBoundsAndPatterns(t *testing.T) {
	star := compileTopLevel(t, "const X = z.string();\n", "X")
	if IsUnsupported(star) {
		t.Fatalf("unexpected unsupported: %s", star.Unsupported.Unsupported)
	}
	if derefSet(star.Annotation.Set).Forms[0].Form != refinementsets.FormStar {
		t.Errorf("z.string() forms[0] = %v, want star", derefSet(star.Annotation.Set).Forms[0].Form)
	}

	// length bounds are REPETITION on sequence chains -- never
	// atLeast; bounded repetition is the native form
	exact := compileTopLevel(t, "const X = z.string().length(2);\n", "X")
	if IsUnsupported(exact) {
		t.Fatalf("unexpected unsupported: %s", exact.Unsupported.Unsupported)
	}
	if derefSet(exact.Annotation.Set).Forms[0].Form != refinementsets.FormRepeat {
		t.Errorf("z.string().length(2) forms[0] = %v, want repeat", derefSet(exact.Annotation.Set).Forms[0].Form)
	}

	bounded := compileTopLevel(t, "const X = z.string().min(1).max(3);\n", "X")
	if IsUnsupported(bounded) {
		t.Fatalf("unexpected unsupported: %s", bounded.Unsupported.Unsupported)
	}
	if derefSet(bounded.Annotation.Set).Forms[0].Form != refinementsets.FormRepeat {
		t.Errorf("min/max chain forms[0] = %v, want repeat", derefSet(bounded.Annotation.Set).Forms[0].Form)
	}

	pattern := compileTopLevel(t, `const X = z.string().regex(/^[0-9]+$/);`+"\n", "X")
	if IsUnsupported(pattern) {
		t.Fatalf("unexpected unsupported: %s", pattern.Unsupported.Unsupported)
	}
	if got := len(derefSet(pattern.Annotation.Set).Forms); got != 2 {
		t.Errorf("regex chain forms count = %d, want 2", got)
	}

	backrefs := compileTopLevel(t, `const X = z.string().regex(/(a)\1/);`+"\n", "X")
	if !IsUnsupported(backrefs) {
		t.Fatalf("expected backreferences to refuse")
	}
}

func TestCompileAnnotation_TupleNestsRightRestAppendsTheStar(t *testing.T) {
	two := compileTopLevel(t, "const X = z.tuple([z.number().int(), z.number()]);\n", "X")
	if IsUnsupported(two) {
		t.Fatalf("unexpected unsupported: %s", two.Unsupported.Unsupported)
	}
	if derefSet(two.Annotation.Set).Forms[0].Form != refinementsets.FormConcatenation {
		t.Errorf("tuple forms[0] = %v, want concatenation", derefSet(two.Annotation.Set).Forms[0].Form)
	}
	varied := compileTopLevel(t, "const X = z.tuple([z.number().int()]).rest(z.number());\n", "X")
	if IsUnsupported(varied) {
		t.Fatalf("unexpected unsupported: %s", varied.Unsupported.Unsupported)
	}
	top := derefSet(varied.Annotation.Set).Forms[0]
	if top.Form != refinementsets.FormConcatenation {
		t.Fatalf("rest chain top form = %v, want concatenation", top.Form)
	}
	if top.B.Forms[0].Form != refinementsets.FormStar {
		t.Errorf("rest chain tail form = %v, want star", top.B.Forms[0].Form)
	}
}

func TestCompileAnnotation_StringLiteralAndEnumCompileToCodepointTuples(t *testing.T) {
	one := compileTopLevel(t, `const X = z.literal("a");`+"\n", "X")
	if IsUnsupported(one) {
		t.Fatalf("unexpected unsupported: %s", one.Unsupported.Unsupported)
	}
	got := derefSet(one.Annotation.Set).Forms[0]
	if got.Form != refinementsets.FormOneOf || len(got.W) != 1 || got.W[0] != 97 {
		t.Errorf("z.literal(\"a\") forms[0] = %+v, want oneOf([97])", got)
	}
	two := compileTopLevel(t, `const X = z.enum(["a", "b"]);`+"\n", "X")
	if IsUnsupported(two) {
		t.Fatalf("unexpected unsupported: %s", two.Unsupported.Unsupported)
	}
	if derefSet(two.Annotation.Set).Forms[0].Form != refinementsets.FormUnion {
		t.Errorf("z.enum forms[0] = %v, want union", derefSet(two.Annotation.Set).Forms[0].Form)
	}
}
