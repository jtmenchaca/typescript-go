// Pins the class-field value determinations: a constructor's
// parameter properties fill the instance, a `#`-named initializer is a
// key, `const C = class { … }` constructs as a declaration does, the
// public-field seal grants and refuses standing invariants, the
// summary-call receiver reads a constructed instance, and a method
// call's receiver rides the replay key.
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

// classFieldValuesProgram follows entry_env_test.go's canonical
// program recipe.
func classFieldValuesProgram(t *testing.T, entrySource string) *program.CheckerProgram {
	t.Helper()
	fs := vfstest.FromMap(map[string]string{
		"/main.ts": entrySource,
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
	return &program.CheckerProgram{
		Program: compilerProgram,
		Checker: c,
		Entry:   entry,
	}
}

// classFieldValuesContext is the walk context the tests run under: a
// real checker program and inert sinks — no kernel (every kernel route
// declines and the walk's own reading answers).
func classFieldValuesContext(p *program.CheckerProgram) *FlowContext {
	return &FlowContext{
		P:         p,
		Contracts: map[*ast.Symbol]*FunctionContract{},
		Aliases:   dataflowfacts.NewAliasClasses(),
		Declared:  map[string]*annotations.DeclaredRefinement{},
		Report:    func(assignability.RefinementDiagnostic) {},
	}
}

// classFieldValuesFirstNode finds the first node the predicate admits,
// in source order.
func classFieldValuesFirstNode(t *testing.T, p *program.CheckerProgram, wanted string, admits func(node *ast.Node) bool) *ast.Node {
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

// classFieldValuesClassNamed finds the class declaration named text.
func classFieldValuesClassNamed(t *testing.T, p *program.CheckerProgram, text string) *ast.Node {
	t.Helper()
	return classFieldValuesFirstNode(t, p, "class named "+text, func(node *ast.Node) bool {
		if !ast.IsClassDeclaration(node) {
			return false
		}
		name := node.Name()
		return name != nil && ast.IsIdentifier(name) && name.Text() == text
	})
}

// classFieldValuesKey reads one named key off an object-kind value.
func classFieldValuesKey(t *testing.T, held abstractdomain.AbstractValue, name string) abstractdomain.AbstractValue {
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

// classFieldValuesExactNumber asserts a value is exactly the one
// number.
func classFieldValuesExactNumber(t *testing.T, held abstractdomain.AbstractValue, want float64) {
	t.Helper()
	if held.Kind != abstractdomain.KindValues {
		t.Fatalf("held.Kind = %v, want KindValues", held.Kind)
	}
	if len(held.Values) != 1 || held.Values[0] != want {
		t.Errorf("held.Values = %v, want [%v]", held.Values, want)
	}
}

func classFieldValuesNumber(v float64) abstractdomain.AbstractValue {
	return abstractdomain.KnownValues([]float64{v}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
}

/* ── ConstructedInstance ─────────────────────────────────────────── */

func TestConstructedInstance_AParameterPropertyFillsTheField(t *testing.T) {
	p := classFieldValuesProgram(t,
		"class Person {\n"+
			"  constructor(readonly age: number) {}\n"+
			"}\n"+
			"void Person;\n")
	declaration := classFieldValuesClassNamed(t, p, "Person")
	ctx := classFieldValuesContext(p)
	instance := ConstructedInstance(ctx, declaration, []abstractdomain.AbstractValue{classFieldValuesNumber(40)})
	classFieldValuesExactNumber(t, classFieldValuesKey(t, instance, "age"), 40)
}

func TestConstructedInstance_TheOutOfSetArgumentReachesTheFieldToo(t *testing.T) {
	p := classFieldValuesProgram(t,
		"class Person {\n"+
			"  constructor(readonly age: number) {}\n"+
			"}\n"+
			"void Person;\n")
	declaration := classFieldValuesClassNamed(t, p, "Person")
	ctx := classFieldValuesContext(p)
	instance := ConstructedInstance(ctx, declaration, []abstractdomain.AbstractValue{classFieldValuesNumber(200)})
	classFieldValuesExactNumber(t, classFieldValuesKey(t, instance, "age"), 200)
}

func TestConstructedInstance_APrivateNameInitializerIsAKey(t *testing.T) {
	p := classFieldValuesProgram(t,
		"class Person {\n"+
			"  #age = 40;\n"+
			"  years(): number {\n"+
			"    return this.#age;\n"+
			"  }\n"+
			"}\n"+
			"void Person;\n")
	declaration := classFieldValuesClassNamed(t, p, "Person")
	ctx := classFieldValuesContext(p)
	instance := ConstructedInstance(ctx, declaration, nil)
	classFieldValuesExactNumber(t, classFieldValuesKey(t, instance, "#age"), 40)
}

/* ── the const class expression ──────────────────────────────────── */

func TestEvaluateNewExpression_AConstClassExpressionResolves(t *testing.T) {
	p := classFieldValuesProgram(t,
		"const Person = class {\n"+
			"  age = 40;\n"+
			"};\n"+
			"const instance = new Person();\n"+
			"void instance.age;\n")
	newExpr := classFieldValuesFirstNode(t, p, "new expression", ast.IsNewExpression)
	ctx := classFieldValuesContext(p)
	held := EvaluateNewExpression(ctx, NewEnv(), newExpr)
	if held == nil {
		t.Fatalf("EvaluateNewExpression answered nil for the const-bound class expression")
	}
	classFieldValuesExactNumber(t, classFieldValuesKey(t, *held, "age"), 40)
}

/* ── the public-field seal ───────────────────────────────────────── */

func TestFieldInvariants_ASealedPublicFieldKeepsItsInitializer(t *testing.T) {
	p := classFieldValuesProgram(t,
		"class ThisPerson {\n"+
			"  age = 40;\n"+
			"  years(): number {\n"+
			"    return this.age;\n"+
			"  }\n"+
			"}\n"+
			"const ok = new ThisPerson().years();\n"+
			"void ok;\n")
	declaration := classFieldValuesClassNamed(t, p, "ThisPerson")
	ctx := classFieldValuesContext(p)
	invariants := FieldInvariantsOf(ctx, declaration)
	held, has := invariants["age"]
	if !has {
		t.Fatalf("no invariant for the sealed public field age")
	}
	classFieldValuesExactNumber(t, held, 40)
}

func TestFieldInvariants_AClassReferencedAsAValueLosesThePublicInvariant(t *testing.T) {
	// `void OverPerson` reads the class VALUE — a holder the seal
	// cannot follow, so the public field keeps no invariant and its
	// reads stay opaque
	p := classFieldValuesProgram(t,
		"class OverPerson {\n"+
			"  age = 200;\n"+
			"  years(): number {\n"+
			"    return this.age;\n"+
			"  }\n"+
			"}\n"+
			"void OverPerson;\n")
	declaration := classFieldValuesClassNamed(t, p, "OverPerson")
	ctx := classFieldValuesContext(p)
	invariants := FieldInvariantsOf(ctx, declaration)
	if _, has := invariants["age"]; has {
		t.Errorf("a class handed out as a value kept a public-field invariant")
	}
}

func TestFieldInvariants_ANonThisWriteVetoesThePublicField(t *testing.T) {
	p := classFieldValuesProgram(t,
		"class Boxed {\n"+
			"  age = 40;\n"+
			"}\n"+
			"const box = new Boxed();\n"+
			"box.age = 7;\n")
	declaration := classFieldValuesClassNamed(t, p, "Boxed")
	ctx := classFieldValuesContext(p)
	invariants := FieldInvariantsOf(ctx, declaration)
	if _, has := invariants["age"]; has {
		t.Errorf("a field the file writes outside `this` kept its invariant")
	}
}

func TestFieldInvariants_AnExportedClassLosesThePublicInvariant(t *testing.T) {
	p := classFieldValuesProgram(t,
		"export class Shared {\n"+
			"  age = 40;\n"+
			"}\n"+
			"void new Shared().age;\n")
	declaration := classFieldValuesClassNamed(t, p, "Shared")
	ctx := classFieldValuesContext(p)
	invariants := FieldInvariantsOf(ctx, declaration)
	if _, has := invariants["age"]; has {
		t.Errorf("an exported class kept a public-field invariant")
	}
}

func TestFieldInvariants_AnInstancePassedAsAnArgumentLosesTheSeal(t *testing.T) {
	p := classFieldValuesProgram(t,
		"class Carried {\n"+
			"  age = 40;\n"+
			"}\n"+
			"declare function keep(carried: Carried): void;\n"+
			"const carried = new Carried();\n"+
			"keep(carried);\n")
	declaration := classFieldValuesClassNamed(t, p, "Carried")
	ctx := classFieldValuesContext(p)
	invariants := FieldInvariantsOf(ctx, declaration)
	if _, has := invariants["age"]; has {
		t.Errorf("an instance handed to a callee kept a public-field invariant")
	}
}

func TestFieldInvariants_AMethodExtractionLosesTheSeal(t *testing.T) {
	// `instance.years` without the call may later run with a receiver
	// this class's text never met
	p := classFieldValuesProgram(t,
		"class Extracted {\n"+
			"  age = 40;\n"+
			"  years(): number {\n"+
			"    return this.age;\n"+
			"  }\n"+
			"}\n"+
			"const instance = new Extracted();\n"+
			"const reader = instance.years;\n"+
			"void reader;\n")
	declaration := classFieldValuesClassNamed(t, p, "Extracted")
	ctx := classFieldValuesContext(p)
	invariants := FieldInvariantsOf(ctx, declaration)
	if _, has := invariants["age"]; has {
		t.Errorf("an extracted method left the public-field invariant standing")
	}
}

/* ── the summary-call receiver ───────────────────────────────────── */

func TestSummaryCallReceiver_AConstructedReceiverAnswersTheInstance(t *testing.T) {
	p := classFieldValuesProgram(t,
		"class Person {\n"+
			"  #age = 40;\n"+
			"  years(): number {\n"+
			"    return this.#age;\n"+
			"  }\n"+
			"}\n"+
			"const ok = new Person().years();\n"+
			"void ok;\n")
	call := classFieldValuesFirstNode(t, p, "method call on a construction", func(node *ast.Node) bool {
		return ast.IsCallExpression(node) &&
			ast.IsPropertyAccessExpression(node.AsCallExpression().Expression)
	})
	ctx := classFieldValuesContext(p)
	held := SummaryCallReceiver(ctx, NewEnv(), call)
	classFieldValuesExactNumber(t, classFieldValuesKey(t, held, "#age"), 40)
}

func TestSummaryCallReceiver_AWritingArgumentKeepsTheResidue(t *testing.T) {
	// `new Person(n++).years()` — re-deriving the receiver would run
	// the increment twice, so the reading declines and the residue
	// stands
	p := classFieldValuesProgram(t,
		"class Person {\n"+
			"  constructor(readonly age: number) {}\n"+
			"  years(): number {\n"+
			"    return this.age;\n"+
			"  }\n"+
			"}\n"+
			"let n = 40;\n"+
			"const ok = new Person(n++).years();\n"+
			"void ok;\n")
	call := classFieldValuesFirstNode(t, p, "method call on a construction", func(node *ast.Node) bool {
		return ast.IsCallExpression(node) &&
			ast.IsPropertyAccessExpression(node.AsCallExpression().Expression)
	})
	ctx := classFieldValuesContext(p)
	held := SummaryCallReceiver(ctx, NewEnv(), call)
	if held.Kind != abstractdomain.KindUnknown {
		t.Errorf("held.Kind = %v, want KindUnknown (residue) for an effectful argument", held.Kind)
	}
}

/* ── the replay key ──────────────────────────────────────────────── */

func TestInlineMemoKey_TheReceiverRidesTheKey(t *testing.T) {
	p := classFieldValuesProgram(t,
		"class Sealed {\n"+
			"  #age: number;\n"+
			"  constructor(age: number) {\n"+
			"    this.#age = age;\n"+
			"  }\n"+
			"  years(): number {\n"+
			"    return this.#age;\n"+
			"  }\n"+
			"}\n"+
			"const ok = new Sealed(40).years();\n"+
			"void ok;\n")
	call := classFieldValuesFirstNode(t, p, "method call on a construction", func(node *ast.Node) bool {
		return ast.IsCallExpression(node) &&
			ast.IsPropertyAccessExpression(node.AsCallExpression().Expression)
	})
	method := classFieldValuesFirstNode(t, p, "years method", func(node *ast.Node) bool {
		if !ast.IsMethodDeclaration(node) {
			return false
		}
		name := node.Name()
		return name != nil && ast.IsIdentifier(name) && name.Text() == "years"
	})
	calleeName := call.AsCallExpression().Expression.AsPropertyAccessExpression().Name()
	ctx := classFieldValuesContext(p)
	contract := &FunctionContract{Declaration: method}
	effective := EffectiveArguments{Exact: true}
	receiverOf := func(age float64) abstractdomain.AbstractValue {
		return abstractdomain.KnownObject(
			[]abstractdomain.ObjectKey{{Name: "#age", Value: classFieldValuesNumber(age)}},
			nil, false, abstractdomain.TrustProved, false)
	}
	keyForty := computeInlineMemoKey(ctx, NewEnv(), call, contract, calleeName, effective, receiverOf(40))
	keyTwoHundred := computeInlineMemoKey(ctx, NewEnv(), call, contract, calleeName, effective, receiverOf(200))
	if keyForty == "" || keyTwoHundred == "" {
		t.Fatalf("a spellable receiver refused a key: %q, %q", keyForty, keyTwoHundred)
	}
	if keyForty == keyTwoHundred {
		t.Errorf("two receivers holding different field values spelled one replay key: %q", keyForty)
	}
}
