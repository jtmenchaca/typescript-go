// The with-statement reach: the body of `with (o) { … }` is WALKED
// and its checked positions JUDGED — a return of a with-scoped name
// against a stated result set fires the alert, because nothing about
// that name is provable inside the scope — while tracked names the
// body mentions are forgotten across the block and names it never
// mentions ride through untouched.
//
// Companion to with_statement.go. No TS twin: the TS source routes a
// with statement to the unmodeled catch-all, which never walks the
// body (the tsc-vscode syntax-coverage fixture a-statements.ts pins
// the fixed behavior at its withStatement function).

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
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/tsoptions"
	"github.com/microsoft/typescript-go/internal/vfs/vfstest"
)

// withReachTestProgram is PORT.md's canonical program-from-source
// recipe (entry_env_test.go's entryEnvTestProgram), kept local so
// this file stands alone.
func withReachTestProgram(t *testing.T, entrySource string) *program.CheckerProgram {
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

// withReachStatementOf finds the top-level function named text and
// hands back the first statement of its body — the `with` under test.
func withReachStatementOf(t *testing.T, p *program.CheckerProgram, text string) *ast.Node {
	t.Helper()
	for _, statement := range p.Entry.Statements.Nodes {
		if !ast.IsFunctionDeclaration(statement) {
			continue
		}
		name := statement.AsFunctionDeclaration().Name()
		if name == nil || !ast.IsIdentifier(name) || name.Text() != text {
			continue
		}
		body := statement.Body()
		if body == nil {
			t.Fatalf("function %s has no body", text)
		}
		statements := body.AsBlock().Statements.Nodes
		if len(statements) == 0 {
			t.Fatalf("function %s has an empty body", text)
		}
		first := statements[0]
		if !ast.IsWithStatement(first) {
			t.Fatalf("function %s's first statement is not a with statement", text)
		}
		return first
	}
	t.Fatalf("no function named %s", text)
	return nil
}

// withReachContext is the walk context the tests run under: a real
// checker program, an alias store, and a diagnostic sink — no kernel
// (the with route asks it nothing: a with-scoped read is residue, and
// residue against a stated set is the alert, decided host-side).
func withReachContext(p *program.CheckerProgram, sink *[]assignability.RefinementDiagnostic) *FlowContext {
	return &FlowContext{
		P:         p,
		Contracts: map[*ast.Symbol]*FunctionContract{},
		Aliases:   dataflowfacts.NewAliasClasses(),
		Declared:  map[string]*annotations.DeclaredRefinement{},
		Report: func(d assignability.RefinementDiagnostic) {
			*sink = append(*sink, d)
		},
	}
}

// withReachStatedWindow is a stated result set that adds more than
// the number sort's ground — the shape a refined return type states.
func withReachStatedWindow(lo float64, hi float64) *annotations.DeclaredRefinement {
	set := refinementsets.MakeRefinedSet(refinementsets.AtLeast(lo), refinementsets.AtMost(hi))
	return &annotations.DeclaredRefinement{Kind: annotations.DeclaredSet, Set: &set}
}

// A return inside the with body is judged: the returned name may
// resolve through the with scope, nothing about it is provable, and
// the stated result set fires. The return also ENDS the walk — the
// old catch-all swallowed it and walked on.
func TestWithStatement_AReturnInsideTheBodyIsJudgedAndFires(t *testing.T) {
	p := withReachTestProgram(t,
		"function f(obj: { age: number }): number {\n"+
			"  with (obj) {\n"+
			"    return age;\n"+
			"  }\n"+
			"  return 0;\n"+
			"}\n")
	var diagnostics []assignability.RefinementDiagnostic
	ctx := withReachContext(p, &diagnostics)
	exits := AnalyzeStatement(ctx, NewEnv(), withReachStatementOf(t, p, "f"), withReachStatedWindow(0, 150))
	if !exits {
		t.Errorf("AnalyzeStatement(with{return age}) exits = false, want true — the body's return ends the list")
	}
	fired := false
	for _, d := range diagnostics {
		if d.Code == 7001 || d.Code == 7002 {
			fired = true
		}
	}
	if !fired {
		t.Errorf("no diagnostic fired for `return age` inside the with body; got %d diagnostics", len(diagnostics))
	}
}

// A tracked name the body only READS is forgotten across the block:
// the read may have resolved to the scope object's property, so the
// old fact does not survive.
func TestWithStatement_AMentionedTrackedNameIsForgotten(t *testing.T) {
	p := withReachTestProgram(t,
		"function g(obj: { age: number }, x: number): number {\n"+
			"  with (obj) {\n"+
			"    x;\n"+
			"  }\n"+
			"  return x;\n"+
			"}\n")
	var diagnostics []assignability.RefinementDiagnostic
	ctx := withReachContext(p, &diagnostics)
	env := NewEnv()
	env.Set("x", abstractdomain.KnownValues([]float64{42}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved))
	exits := AnalyzeStatement(ctx, env, withReachStatementOf(t, p, "g"), nil)
	if exits {
		t.Errorf("AnalyzeStatement(with{x}) exits = true, want false — the body falls through")
	}
	held, ok := env.Get("x")
	if !ok {
		t.Fatalf("env lost the binding x entirely, want it held as unknown")
	}
	if held.Kind != abstractdomain.KindUnknown {
		t.Errorf("env[x].Kind after the with = %v, want KindUnknown — the mention forgets the fact", held.Kind)
	}
}

// A tracked name the body never mentions keeps its fact: `with`
// resolves only the names its body spells, so an unmentioned binding
// rides through exactly.
func TestWithStatement_AnUnmentionedTrackedNameRidesThrough(t *testing.T) {
	p := withReachTestProgram(t,
		"function h(obj: { age: number }, x: number): number {\n"+
			"  with (obj) {\n"+
			"    obj;\n"+
			"  }\n"+
			"  return x;\n"+
			"}\n")
	var diagnostics []assignability.RefinementDiagnostic
	ctx := withReachContext(p, &diagnostics)
	env := NewEnv()
	env.Set("x", abstractdomain.KnownValues([]float64{42}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved))
	AnalyzeStatement(ctx, env, withReachStatementOf(t, p, "h"), nil)
	held, ok := env.Get("x")
	if !ok {
		t.Fatalf("env lost the binding x entirely, want its value kept")
	}
	if held.Kind != abstractdomain.KindValues || len(held.Values) != 1 || held.Values[0] != 42 {
		formatted, _ := abstractdomain.FormatAbstractValue(held)
		t.Errorf("env[x] after the with = %q, want the exact 42 kept — h's body never mentions x", formatted)
	}
}

// A write inside the body does not survive as a fact: `x = 5` may
// have landed on the scope object's property instead of the binding,
// so after the block x is unknown — never 5.
func TestWithStatement_AWriteInsideTheBodyIsNotTrusted(t *testing.T) {
	p := withReachTestProgram(t,
		"function w(obj: { age: number }, x: number): number {\n"+
			"  with (obj) {\n"+
			"    x = 5;\n"+
			"  }\n"+
			"  return x;\n"+
			"}\n")
	var diagnostics []assignability.RefinementDiagnostic
	ctx := withReachContext(p, &diagnostics)
	env := NewEnv()
	env.Set("x", abstractdomain.KnownValues([]float64{42}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved))
	AnalyzeStatement(ctx, env, withReachStatementOf(t, p, "w"), nil)
	held, ok := env.Get("x")
	if !ok {
		t.Fatalf("env lost the binding x entirely, want it held as unknown")
	}
	if held.Kind == abstractdomain.KindValues {
		formatted, _ := abstractdomain.FormatAbstractValue(held)
		t.Errorf("env[x] after the with = %q, want unknown — the write may have landed on obj instead", formatted)
	}
}
