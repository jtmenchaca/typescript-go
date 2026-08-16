// Pins the cross-file construction seam: `new Person(40).age` where
// `Person` is imported from another file reads the exporting file's
// parameter-property constructor exactly as the in-file twin does
// (class_field_values_test.go's TestConstructedInstance_
// AParameterPropertyFillsTheField). Before symbolAt's alias hop landed
// in EvaluateNewExpression's class-lookup branch, GetSymbolAtLocation
// answered the IMPORT SPECIFIER's alias symbol — never the class — so
// every imported `new X(...)` fell through to an opaque instance.
// constructorDeclarationOf (ir_summary_call.go, the summary side) took
// the same alias hop already; this file also pins its const-class-
// expression unwrap across the same import edge.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/bundled"
	"github.com/microsoft/typescript-go/internal/compiler"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/tsoptions"
	"github.com/microsoft/typescript-go/internal/vfs/vfstest"
)

// crossFileConstructedProgram builds a two-file program, entered at
// /main.ts, importing from /helper.ts -- newMultiFileTestProgram's
// pattern (annotations/annotations_test_helpers_test.go) without the
// surface stand-in, since these tests exercise plain classes only.
func crossFileConstructedProgram(t *testing.T, helperSource, mainSource string) *program.CheckerProgram {
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
	c, done := compilerProgram.GetTypeChecker(t.Context())
	t.Cleanup(done)
	entry := compilerProgram.GetSourceFile("/main.ts")
	if entry == nil {
		t.Fatalf("no entry source file")
	}
	return &program.CheckerProgram{
		Program: compilerProgram,
		Checker: c,
		Entry:   entry,
	}
}

// crossFileConstructedContext mirrors class_field_values_test.go's
// classFieldValuesContext: a real checker program, inert sinks, no
// kernel.
func crossFileConstructedContext(p *program.CheckerProgram) *FlowContext {
	return &FlowContext{
		P:         p,
		Contracts: map[*ast.Symbol]*FunctionContract{},
		Aliases:   dataflowfacts.NewAliasClasses(),
		Declared:  map[string]*annotations.DeclaredRefinement{},
		Report:    func(assignability.RefinementDiagnostic) {},
	}
}

// crossFileConstructedFirstNode finds the first node the predicate
// admits, in source order, over the ENTRY file only -- the sink
// expressions under test always sit in /main.ts.
func crossFileConstructedFirstNode(t *testing.T, p *program.CheckerProgram, wanted string, admits func(node *ast.Node) bool) *ast.Node {
	t.Helper()
	var found *ast.Node
	var visit func(node *ast.Node)
	visit = func(node *ast.Node) {
		if found != nil {
			return
		}
		if admits(node) {
			found = node
			return
		}
		node.ForEachChild(func(child *ast.Node) bool {
			visit(child)
			return found != nil
		})
	}
	visit(p.Entry.AsNode())
	if found == nil {
		t.Fatalf("no %s in the entry source", wanted)
	}
	return found
}

func crossFileConstructedKey(t *testing.T, held abstractdomain.AbstractValue, name string) abstractdomain.AbstractValue {
	t.Helper()
	if held.Kind != abstractdomain.KindObject {
		t.Fatalf("held.Kind = %v, want KindObject", held.Kind)
	}
	for _, key := range held.Keys {
		if key.Name == name {
			return key.Value
		}
	}
	t.Fatalf("no key %q among %d keys", name, len(held.Keys))
	return abstractdomain.AbstractValue{}
}

func crossFileConstructedExactNumber(t *testing.T, held abstractdomain.AbstractValue, want float64) {
	t.Helper()
	if held.Kind != abstractdomain.KindValues {
		t.Fatalf("held.Kind = %v, want KindValues", held.Kind)
	}
	if len(held.Values) != 1 || held.Values[0] != want {
		t.Errorf("held.Values = %v, want [%v]", held.Values, want)
	}
}

/* ── EvaluateNewExpression across the import edge ────────────────── */

func TestEvaluateNewExpression_AnImportedClassParameterPropertyFillsTheField(t *testing.T) {
	p := crossFileConstructedProgram(t,
		"export class Person {\n"+
			"  constructor(readonly age: number) {}\n"+
			"}\n",
		"import { Person } from \"./helper.ts\";\n"+
			"const instance = new Person(40);\n"+
			"void instance.age;\n")
	newExpr := crossFileConstructedFirstNode(t, p, "new expression", ast.IsNewExpression)
	ctx := crossFileConstructedContext(p)
	held := EvaluateNewExpression(ctx, NewEnv(), newExpr)
	if held == nil {
		t.Fatalf("EvaluateNewExpression answered nil for the imported class -- the alias symbol's ValueDeclaration is the import specifier, not the class, without symbolAt's hop")
	}
	crossFileConstructedExactNumber(t, crossFileConstructedKey(t, *held, "age"), 40)
}

func TestEvaluateNewExpression_AnImportedClassOutOfSetArgumentReachesTheFieldToo(t *testing.T) {
	p := crossFileConstructedProgram(t,
		"export class Person {\n"+
			"  constructor(readonly age: number) {}\n"+
			"}\n",
		"import { Person } from \"./helper.ts\";\n"+
			"const instance = new Person(200);\n"+
			"void instance.age;\n")
	newExpr := crossFileConstructedFirstNode(t, p, "new expression", ast.IsNewExpression)
	ctx := crossFileConstructedContext(p)
	held := EvaluateNewExpression(ctx, NewEnv(), newExpr)
	if held == nil {
		t.Fatalf("EvaluateNewExpression answered nil for the imported class")
	}
	crossFileConstructedExactNumber(t, crossFileConstructedKey(t, *held, "age"), 200)
}

/* ── constructorDeclarationOf across the import edge ─────────────── */

func TestConstructorDeclarationOf_ResolvesAnImportedClassThroughTheAlias(t *testing.T) {
	p := crossFileConstructedProgram(t,
		"export class Person {\n"+
			"  constructor(readonly age: number) {}\n"+
			"}\n",
		"import { Person } from \"./helper.ts\";\n"+
			"new Person(40);\n")
	newExpr := crossFileConstructedFirstNode(t, p, "new expression", ast.IsNewExpression)
	loweringContext := &LoweringContext{Flow: crossFileConstructedContext(p)}
	declaration := constructorDeclarationOf(loweringContext, newExpr.AsNewExpression().Expression)
	if declaration == nil {
		t.Fatalf("constructorDeclarationOf answered nil for an imported class's constructor")
	}
	if !ast.IsConstructorDeclaration(declaration) {
		t.Errorf("constructorDeclarationOf answered a %v node, want a constructor declaration", declaration.Kind)
	}
}

func TestConstructorDeclarationOf_ResolvesAnImportedConstClassExpression(t *testing.T) {
	p := crossFileConstructedProgram(t,
		"export const Person = class {\n"+
			"  constructor(readonly age: number) {}\n"+
			"};\n",
		"import { Person } from \"./helper.ts\";\n"+
			"new Person(40);\n")
	newExpr := crossFileConstructedFirstNode(t, p, "new expression", ast.IsNewExpression)
	loweringContext := &LoweringContext{Flow: crossFileConstructedContext(p)}
	declaration := constructorDeclarationOf(loweringContext, newExpr.AsNewExpression().Expression)
	if declaration == nil {
		t.Fatalf("constructorDeclarationOf answered nil for an imported const class expression's constructor")
	}
	if !ast.IsConstructorDeclaration(declaration) {
		t.Errorf("constructorDeclarationOf answered a %v node, want a constructor declaration", declaration.Kind)
	}
}
