// Interface test for the zod library-adapter path end to end through
// CompileAnnotation: a virtual "zod" package at the exact node_modules
// path DeclaresZod recognizes, exercising the adapter's own root
// vocabulary (z.number() -> the finite doubles, not R-bar) rather
// than the checker's surface stand-in.

package annotations

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/bundled"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/compiler"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/tsoptions"
	"github.com/microsoft/typescript-go/internal/vfs/vfstest"
)

const zodPackageStandIn = `
export const z = {
  number: (): any => 0,
  string: (): any => 0,
  coerce: { number: (): any => 0 },
};
`

func newZodAdapterTestProgram(t *testing.T, entrySource string) (*program.CheckerProgram, *checker.Checker) {
	t.Helper()
	fs := vfstest.FromMap(map[string]string{
		"/main.ts":                     `import { z } from "zod";` + "\n" + entrySource,
		"/node_modules/zod/index.d.ts": zodPackageStandIn,
		"/tsconfig.json": `{
			"compilerOptions": {},
			"files": ["main.ts"]
		}`,
	}, false /*useCaseSensitiveFileNames*/)
	fs = bundled.WrapFS(fs)
	host := compiler.NewCompilerHost("/", fs, bundled.LibPath(), nil, nil, nil)
	parsed, errors := tsoptions.GetParsedCommandLineOfConfigFile("/tsconfig.json", &core.CompilerOptions{}, nil, host, nil)
	if len(errors) != 0 {
		t.Fatalf("parsing tsconfig.json: %v", errors)
	}
	compilerProgram := compiler.NewProgram(compiler.ProgramOptions{Config: parsed, Host: host})
	compilerProgram.BindSourceFiles()
	c, done := compilerProgram.GetTypeChecker(t.Context())
	t.Cleanup(done)
	entry := compilerProgram.GetSourceFile("/main.ts")
	if entry == nil {
		t.Fatalf("no entry source file")
	}
	p := &program.CheckerProgram{
		Program: compilerProgram,
		Checker: c,
		Entry:   entry,
		// no SurfacePaths: this program has no checker-surface import
		// at all, only the zod package -- the adapter path must fire
		// on its own, independent of resolvesToSurface
		SurfacePaths: map[string]bool{},
	}
	return p, c
}

func TestZodAdapter_NumberRootIsTheFiniteDoublesNotRBar(t *testing.T) {
	p, c := newZodAdapterTestProgram(t, "const X = z.number();\n")
	var initializer *ast.Node
	for _, statement := range p.Entry.Statements.Nodes {
		if !ast.IsVariableStatement(statement) {
			continue
		}
		for _, declaration := range statement.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes {
			varDecl := declaration.AsVariableDeclaration()
			if ast.IsIdentifier(varDecl.Name()) && varDecl.Name().AsIdentifier().Text == "X" {
				initializer = varDecl.Initializer
			}
		}
	}
	if initializer == nil {
		t.Fatalf("no const X")
	}
	_ = c
	compiled := CompileAnnotation(p, initializer, AnnotationRegistry{})
	if IsUnsupported(compiled) {
		t.Fatalf("unexpected unsupported: %s", compiled.Unsupported.Unsupported)
	}
	if compiled.Annotation.LibraryAdapter != "zod" {
		t.Errorf("LibraryAdapter = %q, want zod", compiled.Annotation.LibraryAdapter)
	}
	set := derefSet(compiled.Annotation.Set)
	// the adapter's OWN root (chain_vocabulary.go's ZodRoots["number"])
	// states above(-Inf), below(Inf) -- the FINITE doubles -- not the
	// surface's atLeast(-Inf) spelling of R-bar itself
	if len(set.Forms) != 2 || set.Forms[0].Form != refinementsets.FormAbove || set.Forms[1].Form != refinementsets.FormBelow {
		t.Errorf("zod z.number() forms = %+v, want [above(-Inf), below(Inf)]", set.Forms)
	}
}
