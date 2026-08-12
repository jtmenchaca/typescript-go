// Interface tests for type reading: both adapters and the join.
// A form the join misses is a false unknown.

package typereading

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
// in-memory single-file program with its checker. Unlike the TS
// source's CheckerProgram, there is no separate host/surface layer to
// build -- the checker IS the host, in-process (PORT.md's adapter
// rule), so this helper builds the plain tsgo compiler.Program and
// hands back its checker and entry file directly.
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

// parameterNamed is parameterNamed in the TS test.
func parameterNamed(t *testing.T, p testProgram, text string) *ast.Node {
	t.Helper()
	var found *ast.Node
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if found != nil {
			return true
		}
		if ast.IsParameterDeclaration(node) && ast.IsIdentifier(node.Name()) && node.Name().AsIdentifier().Text == text {
			found = node
			return true
		}
		node.ForEachChild(visit)
		return found != nil
	}
	p.entry.AsNode().ForEachChild(visit)
	if found == nil {
		t.Fatalf("no parameter %s", text)
	}
	return found
}

func hostAt(p testProgram, token *ast.Node) (abstractdomain.AbstractValue, bool) {
	return ReadHostType(p.checker, p.checker.GetTypeAtLocation(token), token, 0)
}

func TestReadHostType_NumberArrayIsAStarOfNumbersWithNaNOnTheElements(t *testing.T) {
	p := programFromSource(t, "export function f(xs: number[]): void { console.log(xs); }\n")
	worn, ok := hostAt(p, identifierIn(t, p, "xs", 1))
	if !ok {
		t.Fatalf("expected a value")
	}
	if worn.Kind != abstractdomain.KindSet {
		t.Fatalf("expected a set, got %v", worn.Kind)
	}
	if worn.Set.Forms[0].Form != "star" {
		t.Errorf("Set.Forms[0].Form = %v, want star", worn.Set.Forms[0].Form)
	}
	if !worn.NaNElements {
		t.Errorf("NaNElements = false, want true")
	}
}

func TestReadHostType_StringArrayIsAStarOfStringsNoNaNMark(t *testing.T) {
	p := programFromSource(t, "export function f(xs: string[]): void { console.log(xs); }\n")
	worn, ok := hostAt(p, identifierIn(t, p, "xs", 1))
	if !ok || worn.Kind != abstractdomain.KindSet {
		t.Fatalf("expected a set")
	}
	if worn.Set.Forms[0].Form != "star" {
		t.Errorf("Set.Forms[0].Form = %v, want star", worn.Set.Forms[0].Form)
	}
	if worn.NaNElements {
		t.Errorf("NaNElements = true, want false")
	}
}

func TestReadHostType_AnArrayArmNoLongerDissolvesItsUnion(t *testing.T) {
	p := programFromSource(t, "export function f(v: string | number[]): void { console.log(v); }\n")
	worn, ok := hostAt(p, identifierIn(t, p, "v", 1))
	if !ok {
		t.Fatalf("expected a value")
	}
	if worn.Kind != abstractdomain.KindKindUnion {
		t.Errorf("Kind = %v, want kindUnion", worn.Kind)
	}
}

func TestReadHostType_ATemplateLiteralTypeIsItsConcatenation(t *testing.T) {
	p := programFromSource(t, "export function f(t: `px-${string}`): void { console.log(t); }\n")
	worn, ok := hostAt(p, identifierIn(t, p, "t", 1))
	if !ok || worn.Kind != abstractdomain.KindSet {
		t.Fatalf("expected a set")
	}
	if worn.Set.Forms[0].Form != "concatenation" {
		t.Errorf("Set.Forms[0].Form = %v, want concatenation", worn.Set.Forms[0].Form)
	}
}

func TestReadHostType_ATypeParameterReadsItsConstraint(t *testing.T) {
	p := programFromSource(t, "export function f<T extends string>(x: T): void { console.log(x); }\n")
	worn, ok := hostAt(p, identifierIn(t, p, "x", 1))
	if !ok {
		t.Fatalf("expected a value")
	}
	if worn.Kind != abstractdomain.KindSet {
		t.Errorf("Kind = %v, want set", worn.Kind)
	}
}

func TestReadHostType_AnUnconstrainedTypeParameterClaimsNothing(t *testing.T) {
	p := programFromSource(t, "export function f<T>(x: T): void { console.log(x); }\n")
	if _, ok := hostAt(p, identifierIn(t, p, "x", 1)); ok {
		t.Errorf("expected no value")
	}
}

func TestReadTypeNode_BooleanArrayIsAStarOfTheTwoCodes(t *testing.T) {
	p := programFromSource(t, "export function f(xs: boolean[]): void { console.log(xs); }\n")
	parameter := parameterNamed(t, p, "xs")
	typeNode := parameter.AsParameterDeclaration().Type
	if typeNode == nil {
		t.Fatalf("expected a type node")
	}
	worn, ok := ReadTypeNode(p.checker, typeNode, parameter.Name(), 0)
	if !ok || worn.Kind != abstractdomain.KindSet {
		t.Fatalf("expected a set")
	}
	if worn.Set.Forms[0].Form != "star" {
		t.Errorf("Set.Forms[0].Form = %v, want star", worn.Set.Forms[0].Form)
	}
	formatted, ok := abstractdomain.FormatAbstractValue(worn)
	if !ok || formatted != "{each 0 | 1}" {
		t.Errorf("FormatAbstractValue = %q, %v, want %q, true", formatted, ok, "{each 0 | 1}")
	}
}

func TestReadDeclaredType_ObjectWithArrayBooleanFillsTheKeySyntaxSkipped(t *testing.T) {
	p := programFromSource(t, "export function f(o: { name: string; flags: Array<boolean> }): void { console.log(o); }\n")
	parameter := parameterNamed(t, p, "o")
	typeNode := parameter.AsParameterDeclaration().Type
	if typeNode == nil {
		t.Fatalf("expected a type node")
	}
	token := parameter.Name()
	syntaxOnly, syntaxOk := ReadTypeNode(p.checker, typeNode, token, 0)
	host, hostOk := ReadHostType(p.checker, p.checker.GetTypeAtLocation(token), token, 0)
	joined, joinedOk := ReadDeclaredType(p.checker, typeNode, token)
	if !syntaxOk || syntaxOnly.Kind != abstractdomain.KindObject {
		t.Fatalf("expected syntaxOnly to be an object")
	}
	if formatted, ok := abstractdomain.FormatAbstractValue(syntaxOnly); !ok || formatted != "{name: string}" {
		t.Errorf("FormatAbstractValue(syntaxOnly) = %q, %v, want %q, true", formatted, ok, "{name: string}")
	}
	if !hostOk {
		t.Fatalf("expected host to be a value")
	}
	if formatted, ok := abstractdomain.FormatAbstractValue(host); !ok || formatted != "{name: string, flags: each 0 | 1}" {
		t.Errorf("FormatAbstractValue(host) = %q, %v, want %q, true", formatted, ok, "{name: string, flags: each 0 | 1}")
	}
	if !joinedOk {
		t.Fatalf("expected joined to be a value")
	}
	if formatted, ok := abstractdomain.FormatAbstractValue(joined); !ok || formatted != "{name: string, flags: each 0 | 1}" {
		t.Errorf("FormatAbstractValue(joined) = %q, %v, want %q, true", formatted, ok, "{name: string, flags: each 0 | 1}")
	}
}

func TestReadDeclaredType_ArrayStringFallsThroughToHostStar(t *testing.T) {
	p := programFromSource(t, "export function f(xs: Array<string>): void { console.log(xs); }\n")
	parameter := parameterNamed(t, p, "xs")
	typeNode := parameter.AsParameterDeclaration().Type
	if typeNode == nil {
		t.Fatalf("expected a type node")
	}
	token := parameter.Name()
	_, syntaxOk := ReadTypeNode(p.checker, typeNode, token, 0)
	if syntaxOk {
		t.Errorf("expected syntax-only reading to hold nothing")
	}
	joined, joinedOk := ReadDeclaredType(p.checker, typeNode, token)
	if !joinedOk || joined.Kind != abstractdomain.KindSet {
		t.Fatalf("expected a set")
	}
	if joined.Set.Forms[0].Form != "star" {
		t.Errorf("Set.Forms[0].Form = %v, want star", joined.Set.Forms[0].Form)
	}
}

func TestGate_OneCopyThroughANamedConstLaundersNothing(t *testing.T) {
	p := programFromSource(
		t,
		"const raw = JSON.parse(\"0\");\n"+
			"const b: number = raw;\n"+
			"const a = b;\n"+
			"console.log(a);\n",
	)
	aDeclaration := identifierIn(t, p, "a", 0).Parent
	if !UncheckedDeclaration(p.checker, aDeclaration, 0) {
		t.Errorf("UncheckedDeclaration(a) = false, want true")
	}
	bDeclaration := identifierIn(t, p, "b", 0).Parent
	if !UncheckedDeclaration(p.checker, bDeclaration, 0) {
		t.Errorf("UncheckedDeclaration(b) = false, want true")
	}
}

func TestGate_ACheckedChainOfCopiesStaysSeedable(t *testing.T) {
	p := programFromSource(
		t,
		"const b: number = 1;\n"+
			"const a = b;\n"+
			"console.log(a);\n",
	)
	aDeclaration := identifierIn(t, p, "a", 0).Parent
	if UncheckedDeclaration(p.checker, aDeclaration, 0) {
		t.Errorf("UncheckedDeclaration(a) = true, want false")
	}
}
