// Pins the import edge's symbol identity: an imported exported const
// resolves to the SAME symbol the exporting declaration carries — the
// exports-table symbol (binder.declareModuleMember), not the exporting
// file's locals twin that merely points to it through ExportSymbol.
// Registries (annotations.FileFacts) key by declaration.Symbol(), so a
// locals-twin answer misses every registry for every imported name —
// the A2.edge.json imported-schema decline.
package nameresolution

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/bundled"
	"github.com/microsoft/typescript-go/internal/compiler"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/tsoptions"
	"github.com/microsoft/typescript-go/internal/vfs/vfstest"
)

func twoFileProgram(t *testing.T, helperSource, mainSource string) (*compiler.Program, *ast.SourceFile, *ast.SourceFile) {
	t.Helper()
	fs := vfstest.FromMap(map[string]string{
		"/helper.ts": helperSource,
		"/main.ts":   mainSource,
		"/tsconfig.json": `{
			"compilerOptions": {},
			"files": ["main.ts", "helper.ts"]
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
	return compilerProgram, compilerProgram.GetSourceFile("/main.ts"), compilerProgram.GetSourceFile("/helper.ts")
}

// firstIdentifierOutsideImports finds the first identifier with the
// given text whose enclosing statement is not an import declaration.
func firstIdentifierOutsideImports(t *testing.T, file *ast.SourceFile, text string) *ast.Node {
	t.Helper()
	var found *ast.Node
	var visit func(node *ast.Node)
	visit = func(node *ast.Node) {
		if found != nil {
			return
		}
		if ast.IsIdentifier(node) && node.Text() == text &&
			ast.FindAncestorKind(node, ast.KindImportDeclaration) == nil {
			found = node
			return
		}
		node.ForEachChild(func(child *ast.Node) bool {
			visit(child)
			return found != nil
		})
	}
	visit(file.AsNode())
	if found == nil {
		t.Fatalf("no identifier %q outside imports in %s", text, file.FileName())
	}
	return found
}

func TestDeclarationSymbolOf_AnImportedConstAnswersTheDeclarationsOwnSymbol(t *testing.T) {
	compilerProgram, mainFile, helperFile := twoFileProgram(t,
		"export const limit = 5;\n",
		"import { limit } from \"./helper.ts\";\nconst doubled = limit + limit;\nvoid doubled;\n",
	)
	use := firstIdentifierOutsideImports(t, mainFile, "limit")
	resolved := DeclarationSymbolOf(compilerProgram, use)
	if resolved == nil {
		t.Fatalf("DeclarationSymbolOf answered nil for the imported name")
	}
	var declared *ast.Symbol
	for _, statement := range helperFile.Statements.Nodes {
		if !ast.IsVariableStatement(statement) {
			continue
		}
		for _, declaration := range statement.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes {
			if declaration.AsVariableDeclaration().Name().Text() == "limit" {
				declared = declaration.Symbol()
			}
		}
	}
	if declared == nil {
		t.Fatalf("no binder symbol on the helper declaration")
	}
	if resolved != declared {
		t.Errorf("resolved %p, want the declaration's own symbol %p", resolved, declared)
	}
}
