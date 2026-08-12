package narrowing

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/bundled"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/compiler"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/tsoptions"
	"github.com/microsoft/typescript-go/internal/vfs/vfstest"
)

// checkerFor is the test suite's shared program builder — an in-memory
// single-file program with its checker, the same recipe
// typereading/read_type_test.go and dataflowfacts/dataflowfacts_test.go
// use (internal/checker/checker_test.go's canonical form). Unlike the
// TS test's programFromSource, there is no separate host/surface layer
// to build — the checker IS the host, in-process (PORT.md's adapter
// rule).
func checkerFor(t *testing.T, source string) (*checker.Checker, *ast.SourceFile) {
	t.Helper()
	fs := vfstest.FromMap(map[string]string{
		"/main.ts": source,
		"/tsconfig.json": `{
			"compilerOptions": {},
			"files": ["main.ts"]
		}`,
	}, false /*useCaseSensitiveFileNames*/)
	fs = bundled.WrapFS(fs)

	host := compiler.NewCompilerHost("/", fs, bundled.LibPath(), nil, nil, nil)
	parsed, errors := tsoptions.GetParsedCommandLineOfConfigFile("/tsconfig.json", &core.CompilerOptions{}, nil, host, nil)
	if len(errors) != 0 {
		t.Fatalf("parsing tsconfig: %v", errors)
	}
	p := compiler.NewProgram(compiler.ProgramOptions{
		Config: parsed,
		Host:   host,
	})
	p.BindSourceFiles()
	c, done := p.GetTypeChecker(t.Context())
	t.Cleanup(done)
	file := p.GetSourceFile("/main.ts")
	if file == nil {
		t.Fatalf("main.ts did not parse")
	}
	return c, file
}

// firstIfCondition is the tested expression of the first `if` statement
// found in the source file — the TS suite's ofCondition helper, minus
// the narrowing call itself (each test calls that with its own kernel
// gate).
func firstIfCondition(file *ast.SourceFile) *ast.Node {
	var found *ast.Node
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if found != nil {
			return true
		}
		if ast.IsIfStatement(node) {
			found = node.AsIfStatement().Expression
			return true
		}
		node.ForEachChild(visit)
		return found != nil
	}
	file.AsNode().ForEachChild(visit)
	return found
}
