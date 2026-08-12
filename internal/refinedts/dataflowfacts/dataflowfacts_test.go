package dataflowfacts

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

// checkerFor builds a real *checker.Checker over one in-memory source
// file — the Go twin of the TS suite's programFromSource, scoped down to
// what this package's tests need (symbol and type resolution). Kept
// local to the test files it serves: PORT.md's "no extracted helper
// files" rule covers the ported package itself, not test scaffolding.
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

// ifConditions collects every `if` statement's tested expression, in
// source order — the TS suite's `p.entry.statements.filter(ts.isIfStatement)`.
func ifConditions(file *ast.SourceFile) []*ast.Node {
	var conditions []*ast.Node
	for _, statement := range file.Statements.Nodes {
		if ast.IsIfStatement(statement) {
			conditions = append(conditions, statement.AsIfStatement().Expression)
		}
	}
	return conditions
}
