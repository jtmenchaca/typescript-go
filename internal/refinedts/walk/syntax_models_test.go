// from evaluation/syntax_models.test.ts

package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/bundled"
	"github.com/microsoft/typescript-go/internal/compiler"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
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
	ast.KindTaggedTemplateExpression,
	ast.KindSuperKeyword,
}

var declinedRows = []struct {
	kind        ast.Kind
	said        string
	unsupported bool
}{
	{ast.KindNewExpression, "new builds a value the walk does not model", true},
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

// TestNullKeywordEvaluatesToExactlyNull pins the producer split (KindNull
// vs KindUndef): a bare `null` literal in an expression position is the
// runtime null value, not the undefined value — evaluateExpression's own
// ast.KindNullKeyword arm answers abstractdomain.Null.
func TestNullKeywordEvaluatesToExactlyNull(t *testing.T) {
	p := entryEnvTestProgram(t, "null;")
	statements := p.Entry.Statements.Nodes
	if len(statements) != 1 || !ast.IsExpressionStatement(statements[0]) {
		t.Fatalf("expected one expression statement, got %d statements", len(statements))
	}
	ctx := superArrayContracts(t, p)
	value := evaluateExpression(ctx, NewEnv(), statements[0].AsExpressionStatement().Expression)
	if value.Kind != abstractdomain.KindNull {
		spelled, _ := abstractdomain.FormatAbstractValue(value)
		t.Errorf("evaluateExpression(null) Kind = %v (%q), want KindNull", value.Kind, spelled)
	}
}

// TestLiteralKnownNullEvaluatesToExactlyNull is TestNullKeywordEvaluates
// ToExactlyNull's twin for the OTHER null-literal reader: a `const x =
// null;` declarator is read by literalKnown (type_seed_answer.go), the
// last-reader path LiteralConstClaim/TypeSeedAnswer fall back to at a
// silent exit — it must answer the same exact null value, not undefined.
func TestLiteralKnownNullEvaluatesToExactlyNull(t *testing.T) {
	p := entryEnvTestProgram(t, "const x = null;")
	statements := p.Entry.Statements.Nodes
	if len(statements) != 1 || !ast.IsVariableStatement(statements[0]) {
		t.Fatalf("expected one variable statement, got %d statements", len(statements))
	}
	declarations := statements[0].AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes
	if len(declarations) != 1 {
		t.Fatalf("expected one declarator, got %d", len(declarations))
	}
	initializer := declarations[0].AsVariableDeclaration().Initializer
	value, ok := literalKnown(p, initializer, 0)
	if !ok {
		t.Fatalf("literalKnown(null) ok = false, want true")
	}
	if value.Kind != abstractdomain.KindNull {
		spelled, _ := abstractdomain.FormatAbstractValue(value)
		t.Errorf("literalKnown(null) Kind = %v (%q), want KindNull", value.Kind, spelled)
	}
}
