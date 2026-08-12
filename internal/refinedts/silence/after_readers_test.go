// afterReaders: seed fills silence only; cuts and unchecked do not.

package silence

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/bundled"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/compiler"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/tsoptions"
	"github.com/microsoft/typescript-go/internal/vfs/vfstest"
)

// testProgram stands in for the TS test's programFromSource: an
// in-memory single-file program with its checker. There is no
// separate host/surface layer to build — the checker IS the host,
// in-process (PORT.md's adapter rule) — so this helper builds the
// plain tsgo compiler.Program and hands back its checker and entry
// file directly. Mirrors typereading/read_type_test.go's helper of
// the same shape.
type testProgram struct {
	checker *checker.Checker
	entry   *ast.SourceFile
}

func programFromSource(t *testing.T, source string) testProgram {
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
		t.Fatalf("parsing tsconfig.json: %v", errors)
	}
	p := compiler.NewProgram(compiler.ProgramOptions{Config: parsed, Host: host})
	p.BindSourceFiles()
	c, done := p.GetTypeChecker(t.Context())
	t.Cleanup(done)
	entry := p.GetSourceFile("/main.ts")
	if entry == nil {
		t.Fatalf("no entry source file")
	}
	return testProgram{checker: c, entry: entry}
}

// identifierIn is identifierIn in the TS test: the which-th identifier
// named text, walked in source order.
func identifierIn(t *testing.T, p testProgram, text string, which int) *ast.Node {
	t.Helper()
	seen := 0
	var found *ast.Node
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if found != nil {
			return true
		}
		if ast.IsIdentifier(node) && node.AsIdentifier().Text == text {
			if seen == which {
				found = node
				return true
			}
			seen++
		}
		node.ForEachChild(visit)
		return found != nil
	}
	p.entry.AsNode().ForEachChild(visit)
	if found == nil {
		t.Fatalf("no identifier named %s", text)
	}
	return found
}

func TestAfterReaders_APlainUnknownAtABooleanArrayNameBecomesTheStar(t *testing.T) {
	p := programFromSource(t, "export function f(xs: boolean[]): void { console.log(xs); }\n")
	token := identifierIn(t, p, "xs", 1)
	worn := AfterReaders(abstractdomain.Unknown, p.checker, token, "")
	formatted, ok := abstractdomain.FormatAbstractValue(worn)
	if !ok {
		t.Fatalf("expected a formattable value")
	}
	if formatted != "{each 0 | 1}" {
		t.Fatalf("expected {each 0 | 1}, got %s", formatted)
	}
}

func TestAfterReaders_ACutIsNotSeeded(t *testing.T) {
	p := programFromSource(t, "export function f(xs: boolean[]): void { console.log(xs); }\n")
	token := identifierIn(t, p, "xs", 1)
	worn := AfterReaders(abstractdomain.Unknown, p.checker, token, RoleCut)
	if worn.Kind != abstractdomain.KindUnknown {
		t.Fatalf("expected unknown, got %v", worn.Kind)
	}
	if worn.Opaque {
		t.Fatalf("expected not opaque")
	}
}

func TestAfterReaders_OpaqueStaysOpaque(t *testing.T) {
	p := programFromSource(t, "export function f(xs: boolean[]): void { console.log(xs); }\n")
	token := identifierIn(t, p, "xs", 1)
	worn := AfterReaders(abstractdomain.Opaque, p.checker, token, "")
	if worn.Kind != abstractdomain.KindUnknown {
		t.Fatalf("expected unknown, got %v", worn.Kind)
	}
	if !worn.Opaque {
		t.Fatalf("expected opaque")
	}
}

func TestSeededBinding_HeldKnowledgeOutranksTheType(t *testing.T) {
	p := programFromSource(t, "const s: string = \"hi\";\nconsole.log(s);\n")
	token := identifierIn(t, p, "s", 0)
	held := abstractdomain.KnownValues([]float64{104, 105}, abstractdomain.PrimitiveString, abstractdomain.TrustProved)
	worn := SeededBinding(p.checker, held, token)
	if worn.Kind != held.Kind || worn.KindTag != held.KindTag || len(worn.Values) != len(held.Values) {
		t.Fatalf("expected held to pass through unchanged, got %+v", worn)
	}
	for i := range held.Values {
		if worn.Values[i] != held.Values[i] {
			t.Fatalf("expected held to pass through unchanged, got %+v", worn)
		}
	}
}
