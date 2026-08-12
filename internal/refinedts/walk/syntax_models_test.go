// from evaluation/syntax_models.test.ts

package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/bundled"
	"github.com/microsoft/typescript-go/internal/compiler"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/tsoptions"
	"github.com/microsoft/typescript-go/internal/vfs/vfstest"
)

var modeledKinds = []ast.Kind{
	ast.KindParenthesizedExpression,
	ast.KindAwaitExpression,
	ast.KindSatisfiesExpression,
	ast.KindAsExpression,
	ast.KindNonNullExpression,
	ast.KindTypeAssertionExpression,
	ast.KindNumericLiteral,
	ast.KindTrueKeyword,
	ast.KindFalseKeyword,
	ast.KindStringLiteral,
	ast.KindNoSubstitutionTemplateLiteral,
	ast.KindTemplateExpression,
	ast.KindPrefixUnaryExpression,
	ast.KindPostfixUnaryExpression,
	ast.KindIdentifier,
	ast.KindNullKeyword,
	ast.KindVoidExpression,
	ast.KindTypeOfExpression,
	ast.KindDeleteExpression,
	ast.KindArrayLiteralExpression,
	ast.KindObjectLiteralExpression,
	ast.KindConditionalExpression,
	ast.KindBinaryExpression,
	ast.KindCallExpression,
	ast.KindPropertyAccessExpression,
	ast.KindElementAccessExpression,
}

var declinedRows = []struct {
	kind        ast.Kind
	said        string
	unsupported bool
}{
	{ast.KindNewExpression, "new builds a value the walk does not model", true},
	{ast.KindTaggedTemplateExpression, "a tagged template calls its tag, which the walk does not run", true},
	{ast.KindClassExpression, "a class value is not modeled", true},
	{ast.KindMetaProperty, "import.meta is the host's value, not the program's", true},
	{ast.KindYieldExpression, "what a yield resumes with comes from the caller — not read", true},
	{ast.KindArrowFunction, "a function — read at its calls", false},
	{ast.KindFunctionExpression, "a function — read at its calls", false},
}

func TestSyntaxModelsIsOneRowPerKind(t *testing.T) {
	if len(SyntaxModels) != len(modeledKinds)+len(declinedRows) {
		t.Fatalf("SyntaxModels has %d rows, want %d", len(SyntaxModels), len(modeledKinds)+len(declinedRows))
	}
}

func TestEveryModeledSyntaxKindRowIsModeled(t *testing.T) {
	for _, kind := range modeledKinds {
		row, ok := SyntaxModels[kind]
		if !ok || !row.Modeled {
			t.Fatalf("SyntaxModels[%v] = %+v, ok=%v, want modeled", kind, row, ok)
		}
	}
}

func TestEveryDeclinedSyntaxKindRowCarriesItsSentence(t *testing.T) {
	for _, want := range declinedRows {
		row, ok := SyntaxModels[want.kind]
		if !ok || row.Modeled || row.Said != want.said || row.Unsupported != want.unsupported {
			t.Fatalf("SyntaxModels[%v] = %+v, ok=%v, want {Said:%q Unsupported:%v}", want.kind, row, ok, want.said, want.unsupported)
		}
	}
}

func TestTypeReadsSilentResultCoversJoinsOnly(t *testing.T) {
	fs := vfstest.FromMap(map[string]string{
		"/main.ts":       "a ? b : c; a && b; a || b; a ?? b; f(); x.y;",
		"/tsconfig.json": `{"compilerOptions": {}, "files": ["main.ts"]}`,
	}, false /*useCaseSensitiveFileNames*/)
	fs = bundled.WrapFS(fs)
	host := compiler.NewCompilerHost("/", fs, bundled.LibPath(), nil, nil, nil)
	parsed, errors := tsoptions.GetParsedCommandLineOfConfigFile("/tsconfig.json", &core.CompilerOptions{}, nil, host, nil)
	if len(errors) != 0 {
		t.Fatalf("parsing tsconfig.json: %v", errors)
	}
	p := compiler.NewProgram(compiler.ProgramOptions{Config: parsed, Host: host})
	p.BindSourceFiles()
	entry := p.GetSourceFile("/main.ts")
	if entry == nil {
		t.Fatalf("no entry source file")
	}
	statements := entry.AsSourceFile().Statements.Nodes
	exprOf := func(i int) *ast.Node {
		return statements[i].AsExpressionStatement().Expression
	}
	cond := exprOf(0)
	and := exprOf(1)
	or := exprOf(2)
	qq := exprOf(3)
	call := exprOf(4)
	prop := exprOf(5)
	if !TypeReadsSilentResult(cond) {
		t.Fatal("typeReadsSilentResult(cond) should be true")
	}
	if !TypeReadsSilentResult(and) {
		t.Fatal("typeReadsSilentResult(and) should be true")
	}
	if !TypeReadsSilentResult(or) {
		t.Fatal("typeReadsSilentResult(or) should be true")
	}
	if !TypeReadsSilentResult(qq) {
		t.Fatal("typeReadsSilentResult(qq) should be true")
	}
	if TypeReadsSilentResult(call) {
		t.Fatal("typeReadsSilentResult(call) should be false")
	}
	if TypeReadsSilentResult(prop) {
		t.Fatal("typeReadsSilentResult(prop) should be false")
	}
}
