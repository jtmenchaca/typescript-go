// from control_flow/call_site_bindings.test.ts
//
// Interface tests for CallSiteBindings: exported functions have no
// join, Array.from pins the mapper, a non-exported join wears what
// the sites pass. Callers do not choose between adapters. Skipped
// (never a faked pass) when the native kernel dylib is absent, the
// same gate kernelbridge's own round-trip tests use.

package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/bundled"
	"github.com/microsoft/typescript-go/internal/compiler"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/narrowing"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/tsoptions"
	"github.com/microsoft/typescript-go/internal/vfs/vfstest"
)

// callSiteSurfaceStandIn declares just the root-constructor names the
// transform test needs (mirrors annotations_test_helpers_test.go's
// surfaceStandIn, narrowed to this file's own cases) -- the real
// surface module (surface/z.ts) is not ported (service/ tier).
const callSiteSurfaceStandIn = `
export function number(): any { return 0; }
`

const callSiteSurfacePath = "/z.ts"

// callSiteTestProgram builds a program from the entry source plus the
// surface stand-in, mirroring typereading/read_type_test.go's
// programFromSource pattern (PORT.md's canonical recipe).
func callSiteTestProgram(t *testing.T, entrySource string) *program.CheckerProgram {
	t.Helper()
	fs := vfstest.FromMap(map[string]string{
		"/main.ts":          entrySource,
		callSiteSurfacePath: callSiteSurfaceStandIn,
		"/tsconfig.json": `{
			"compilerOptions": {},
			"files": ["main.ts", "z.ts"]
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
		Program:      compilerProgram,
		Checker:      c,
		Entry:        entry,
		SurfacePaths: map[string]bool{callSiteSurfacePath: true},
	}
}

// callSiteFunctionNamed mirrors the TS test's functionNamed: the
// top-level FunctionDeclaration named text.
func callSiteFunctionNamed(t *testing.T, p *program.CheckerProgram, text string) *ast.Node {
	t.Helper()
	for _, statement := range p.Entry.Statements.Nodes {
		if ast.IsFunctionDeclaration(statement) {
			name := statement.AsFunctionDeclaration().Name()
			if name != nil && ast.IsIdentifier(name) && name.Text() == text {
				return statement
			}
		}
	}
	t.Fatalf("no function named %s", text)
	return nil
}

// callSiteFirstArrow mirrors the TS test's firstArrow: the first
// ArrowFunction found in source order.
func callSiteFirstArrow(t *testing.T, p *program.CheckerProgram) *ast.Node {
	t.Helper()
	var found *ast.Node
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if found != nil {
			return true
		}
		if ast.IsArrowFunction(node) {
			found = node
			return true
		}
		node.ForEachChild(visit)
		return found != nil
	}
	p.Entry.AsNode().ForEachChild(visit)
	if found == nil {
		t.Fatalf("no arrow")
	}
	return found
}

// callSiteCtxOf mirrors the TS test's ctxOf: load the kernel (skip if
// absent) and hand back an empty-registry CallSiteCtx.
func callSiteCtxOf(t *testing.T, p *program.CheckerProgram) CallSiteCtx {
	t.Helper()
	if !kernelbridge.KernelArtifactsPresent(kernelbridge.DylibPath) {
		t.Skip("native kernel dylib absent — build it first")
	}
	kernel, err := kernelbridge.LoadKernel(kernelbridge.DylibPath)
	if err != nil {
		t.Fatalf("LoadKernel: %v", err)
	}
	SetTransferKernel(kernel)
	narrowing.SetNarrowKernel(kernel)
	return CallSiteCtx{
		P:         p,
		Registry:  annotations.AnnotationRegistry{},
		Objects:   annotations.ObjectRegistry{},
		Contracts: map[*ast.Symbol]*FunctionContract{},
		Kernel:    kernel,
	}
}

func TestCallSiteBindings_AnExportedFunctionHasNoJoin(t *testing.T) {
	p := callSiteTestProgram(t, "export function f(n: number) { return n; }\nf(1);\n")
	ctx := callSiteCtxOf(t, p)
	bindings, ok := CallSiteBindings(ctx, callSiteFunctionNamed(t, p, "f"))
	if ok {
		t.Errorf("CallSiteBindings(exported f) = %v, %v, want nil, false", bindings, ok)
	}
}

func TestCallSiteBindings_ArrayFromLengthOnlyMapperPinsAbsentAndIndex(t *testing.T) {
	p := callSiteTestProgram(t, "Array.from({ length: 3 }, (el, i) => i);\n")
	ctx := callSiteCtxOf(t, p)
	bindings, ok := CallSiteBindings(ctx, callSiteFirstArrow(t, p))
	if !ok {
		t.Fatalf("CallSiteBindings(Array.from mapper) ok = false, want true")
	}
	el, hasEl := abstractdomain.FormatAbstractValue(bindings["el"])
	if !hasEl || el != "{absent}" {
		t.Errorf("bindings[el] = %q, %v, want %q, true", el, hasEl, "{absent}")
	}
	i, hasI := abstractdomain.FormatAbstractValue(bindings["i"])
	if !hasI || i != "{integer, 𝑥 ≥ 0}" {
		t.Errorf("bindings[i] = %q, %v, want %q, true", i, hasI, "{integer, 𝑥 ≥ 0}")
	}
}

func TestCallSiteBindings_MapPinsMatchArrayCallbackPins(t *testing.T) {
	p := callSiteTestProgram(t, "const xs = [1, 2]; xs.map((item, i, arr) => item);\n")
	ctx := callSiteCtxOf(t, p)
	fn := callSiteFirstArrow(t, p)
	fromSites, ok := CallSiteBindings(ctx, fn)
	if !ok {
		t.Fatalf("CallSiteBindings(map cb) ok = false, want true")
	}
	fromLaw := ArrayCallbackPins(arrayCallbackPinsParams{
		c:        p.Checker,
		fn:       fn,
		receiver: abstractdomain.KnownValues([]float64{1, 2}, abstractdomain.PrimitiveArray, abstractdomain.TrustProved),
		method:   "map",
	})
	for _, name := range []string{"item", "i", "arr"} {
		fromSitesFormatted, _ := abstractdomain.FormatAbstractValue(fromSites[name])
		fromLawFormatted, _ := abstractdomain.FormatAbstractValue(fromLaw[name])
		if fromSitesFormatted != fromLawFormatted {
			t.Errorf("bindings[%s] = %q, want %q (arrayCallbackPins)", name, fromSitesFormatted, fromLawFormatted)
		}
	}
}

func TestCallSiteBindings_TransformPinsMatchSchemaCallbackPins(t *testing.T) {
	p := callSiteTestProgram(t,
		`import * as z from "./z.ts";`+"\n"+
			"z.number().min(0).transform((n) => n);\n")
	ctx := callSiteCtxOf(t, p)
	fn := callSiteFirstArrow(t, p)
	fromSites, ok := CallSiteBindings(ctx, fn)
	if !ok {
		t.Fatalf("CallSiteBindings(transform cb) ok = false, want true")
	}
	call := fn.Parent
	if !ast.IsCallExpression(call) || !ast.IsPropertyAccessExpression(call.AsCallExpression().Expression) {
		t.Fatalf("expected transform call")
	}
	compiled := annotations.CompileAnnotation(p, call.AsCallExpression().Expression.AsPropertyAccessExpression().Expression, annotations.AnnotationRegistry{})
	if annotations.IsUnsupported(compiled) {
		t.Fatalf("expected compiled receiver")
	}
	fromLaw := SchemaCallbackPins(schemaCallbackPinsParams{c: p.Checker, fn: fn, compiled: *compiled.Annotation})
	fromSitesFormatted, _ := abstractdomain.FormatAbstractValue(fromSites["n"])
	fromLawFormatted, _ := abstractdomain.FormatAbstractValue(fromLaw["n"])
	if fromSitesFormatted != fromLawFormatted {
		t.Errorf("bindings[n] = %q, want %q (schemaCallbackPins)", fromSitesFormatted, fromLawFormatted)
	}
}

func TestCallSiteBindings_ANonExportedFunctionWearsTheJoinOfItsSites(t *testing.T) {
	p := callSiteTestProgram(t, "function f(n) { return n; }\nf(10);\nf(25);\n")
	ctx := callSiteCtxOf(t, p)
	bindings, ok := CallSiteBindings(ctx, callSiteFunctionNamed(t, p, "f"))
	if !ok {
		t.Fatalf("CallSiteBindings(non-exported f) ok = false, want true")
	}
	formatted, hasFormatted := abstractdomain.FormatAbstractValue(bindings["n"])
	if !hasFormatted || formatted != "{10 | 25}" {
		t.Errorf("bindings[n] = %q, %v, want %q, true", formatted, hasFormatted, "{10 | 25}")
	}
}
