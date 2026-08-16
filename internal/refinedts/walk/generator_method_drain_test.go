// Pins the e-class-and-function.ts §E rows generatorMethod and
// asyncGeneratorMethod:
//
//   - `[...new GenAges().ages()][0]` — a generator METHOD's call result
//     is deliberately OPAQUE (GeneratorCallResult, generator_element.go:
//     the call builds a Generator object, not the body's return value),
//     but EvaluateArrayLiteral's own spread-element gate
//     (array_literal.go) only tried builtinIteratorSequenceOf — the
//     door GeneratorSequenceOf sits behind — when the evaluated element
//     read KindUnknown AND NOT Opaque. An Opaque generator call result
//     never cleared that second half, so the reader that CAN answer the
//     element (by reading the callee's yields off the call node, not
//     off the already-opaque value) never ran. Clearing that gate alone
//     was not the whole fix: GeneratorSequenceOf's own answer is a bare
//     STAR ("these elements, length unstated"), which proves nothing
//     about index 0 being present, and ElementAccessOf's array-literal-
//     receiver arm had no reader at all for a KindSet-shaped receiver —
//     so `[0]` fell through to the type-seeded fallback ("number, or
//     NaN") regardless. Two more pieces close it: GeneratorSequenceOf
//     now carries a proven LOWER BOUND (generatorMinimumYieldCount's
//     leading straight-line yields) as a Repetition rather than a plain
//     Star, EvaluateArrayLiteral returns a solo spread's drained
//     sequence directly rather than folding it through
//     sequenceOfElements (which would rebuild a lo=0 star and lose the
//     bound), and ElementAccessOf reads an index proven under that
//     floor as the exact element, bare — no absence wrapper, since a
//     value this same expression just built cannot have been
//     sparsified by anything in between.
//   - `(await new AsyncGenAges().ages().next()).value` — readIteratorNext
//     already reads an async generator's first yield through
//     GeneratorElementOf, but generatorFirstNextValue (which proves
//     done:false with NO absence on the value, since the FIRST next()
//     of a suspended-start generator cannot be the finishing call) used
//     to decline outright for every async generator, leaving the value
//     wrapped in PossiblyUndefined for lack of that proof.

package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
)

// generatorDrainTestFirstCallExpression finds the first CallExpression
// anywhere in the tree whose callee is a property access named
// wantMethodName — enough to land on `....next()` in a one-statement
// body.
func generatorDrainTestFirstCallExpression(t *testing.T, root *ast.Node, wantMethodName string) *ast.Node {
	t.Helper()
	var found *ast.Node
	var visit func(node *ast.Node)
	visit = func(node *ast.Node) {
		if found != nil {
			return
		}
		if ast.IsCallExpression(node) {
			call := node.AsCallExpression()
			if ast.IsPropertyAccessExpression(call.Expression) {
				if call.Expression.AsPropertyAccessExpression().Name().Text() == wantMethodName {
					found = node
					return
				}
			}
		}
		node.ForEachChild(func(child *ast.Node) bool {
			visit(child)
			return found != nil
		})
	}
	visit(root)
	if found == nil {
		t.Fatalf("no call to .%s( found in the tree", wantMethodName)
	}
	return found
}

// TestGeneratorMethodSpread_TheFirstYieldSurvivesThroughAnOpaqueCallResult
// pins the array-literal half: `[...new GenAges().ages()][0]` must
// determine exactly 40 — GeneratorCallResult's own Opaque reading of
// the call must not block EvaluateArrayLiteral's spread-element gate
// from trying builtinIteratorSequenceOf/GeneratorSequenceOf.
func TestGeneratorMethodSpread_TheFirstYieldSurvivesThroughAnOpaqueCallResult(t *testing.T) {
	kernel := yieldContractKernel(t)
	source := "class GenAges {\n" +
		"  *ages(): Generator<number, void, unknown> {\n" +
		"    yield 40;\n" +
		"  }\n" +
		"}\n" +
		"function drained(): number {\n" +
		"  return [...new GenAges().ages()][0];\n" +
		"}\n"
	p := entryEnvTestProgram(t, source)
	registry := annotations.AnnotationRegistry{}
	objects := annotations.ObjectRegistry{}
	merged := map[*ast.Symbol]*FunctionContract{}
	contracts := CompileContractFileFacts(p, p.Entry, registry, objects, merged, false, func(assignability.RefinementDiagnostic) {})
	fn := entryEnvFunctionNamed(t, p, "drained")
	symbol := p.Checker.GetSymbolAtLocation(fn.Name())
	contract := contracts[symbol]
	if contract == nil {
		t.Fatalf("drained registered no contract")
	}
	ctx := &FlowContext{
		P: p, Kernel: kernel, Registry: registry, Objects: objects, Contracts: contracts,
		Report:   func(assignability.RefinementDiagnostic) {},
		Aliases:  dataflowfacts.NewAliasClasses(),
		Declared: map[string]*annotations.DeclaredRefinement{},
	}
	SetEngineKernel(kernel)

	var sink []abstractdomain.AbstractValue
	ctx.ReturnSink = &sink
	AnalyzeFunction(ctx, contract, nil)
	if len(sink) == 0 {
		t.Fatalf("drained's body recorded no return value")
	}
	returned := JoinSinkSummarized(sink)
	if returned.Kind != abstractdomain.KindValues {
		t.Fatalf("[...new GenAges().ages()][0] determined %+v, want an exact KindValues(40) — the generator's own first yield", returned)
	}
	if len(returned.Values) != 1 || returned.Values[0] != 40 {
		t.Errorf("[...new GenAges().ages()][0] determined %v, want exactly [40]", returned.Values)
	}
}

// TestGeneratorMethodSpread_TheOverRangeTwinStillDeterminesExactly pins
// the OverGenAges twin from the same fixture rows (e-class-and-
// function.ts): `[...new OverGenAges().ages()][0]` yields 200, out of
// Age's 0..120 window. The element-read fix that lets the in-set twin
// go quiet must not blur the out-of-range one into a wider, unproved
// set — it still has to determine the EXACT 200 the body wrote, so the
// fixture's own @refinedts-expect-error still has a determined value to
// fire assignability against.
func TestGeneratorMethodSpread_TheOverRangeTwinStillDeterminesExactly(t *testing.T) {
	kernel := yieldContractKernel(t)
	source := "class OverGenAges {\n" +
		"  *ages(): Generator<number, void, unknown> {\n" +
		"    yield 200;\n" +
		"  }\n" +
		"}\n" +
		"function drained(): number {\n" +
		"  return [...new OverGenAges().ages()][0];\n" +
		"}\n"
	p := entryEnvTestProgram(t, source)
	registry := annotations.AnnotationRegistry{}
	objects := annotations.ObjectRegistry{}
	merged := map[*ast.Symbol]*FunctionContract{}
	contracts := CompileContractFileFacts(p, p.Entry, registry, objects, merged, false, func(assignability.RefinementDiagnostic) {})
	fn := entryEnvFunctionNamed(t, p, "drained")
	symbol := p.Checker.GetSymbolAtLocation(fn.Name())
	contract := contracts[symbol]
	if contract == nil {
		t.Fatalf("drained registered no contract")
	}
	ctx := &FlowContext{
		P: p, Kernel: kernel, Registry: registry, Objects: objects, Contracts: contracts,
		Report:   func(assignability.RefinementDiagnostic) {},
		Aliases:  dataflowfacts.NewAliasClasses(),
		Declared: map[string]*annotations.DeclaredRefinement{},
	}
	SetEngineKernel(kernel)

	var sink []abstractdomain.AbstractValue
	ctx.ReturnSink = &sink
	AnalyzeFunction(ctx, contract, nil)
	if len(sink) == 0 {
		t.Fatalf("drained's body recorded no return value")
	}
	returned := JoinSinkSummarized(sink)
	if returned.Kind != abstractdomain.KindValues {
		t.Fatalf("[...new OverGenAges().ages()][0] determined %+v, want an exact KindValues(200)", returned)
	}
	if len(returned.Values) != 1 || returned.Values[0] != 200 {
		t.Errorf("[...new OverGenAges().ages()][0] determined %v, want exactly [200]", returned.Values)
	}
}

// TestGeneratorMinimumYieldCount pins generatorMinimumYieldCount
// directly: the sound lower bound on a generator's yield count is the
// leading run of plain, top-level `yield e;` expression statements —
// counting stops at the first statement that is not one (a block, an
// if, a loop, a delegated yield*, a yield buried inside a larger
// expression), since nothing past that point is proven to run
// unconditionally.
func TestGeneratorMinimumYieldCount(t *testing.T) {
	cases := []struct {
		name string
		body string
		want int
	}{
		{"single straight-line yield", "{ yield 40; }", 1},
		{"two straight-line yields", "{ yield 40; yield 41; }", 2},
		{"a yield after a conditional stops the count", "{ yield 40; if (true) { yield 41; } yield 42; }", 1},
		{"a leading conditional counts nothing", "{ if (true) { yield 40; } }", 0},
		{"a leading delegation counts nothing", "{ yield* [40]; yield 41; }", 0},
		{"an empty body counts nothing", "{ }", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			source := "function* g(): Generator<number, void, unknown> " + c.body + "\n"
			p := entryEnvTestProgram(t, source)
			fn := entryEnvFunctionNamed(t, p, "g")
			got := generatorMinimumYieldCount(fn)
			if got != c.want {
				t.Errorf("generatorMinimumYieldCount(%q) = %d, want %d", c.body, got, c.want)
			}
		})
	}
}

// TestAsyncGeneratorNext_TheFirstResumeProvesDoneFalseWithNoAbsence
// pins generatorFirstNextValue's async half directly: an async
// generator whose body opens with a plain `yield 40` must answer
// `{value: 40, done: false}` on its first next() — no
// PossiblyUndefined wrapper on value — the same claim the sync case
// already proved (AsyncGeneratorResume runs the same body forward to
// the same CreateIteratorResultObject(value, false), the wrapping
// Promise aside).
func TestAsyncGeneratorNext_TheFirstResumeProvesDoneFalseWithNoAbsence(t *testing.T) {
	kernel := yieldContractKernel(t)
	source := "class AsyncGenAges {\n" +
		"  async *ages(): AsyncGenerator<number, void, unknown> {\n" +
		"    yield 40;\n" +
		"  }\n" +
		"}\n" +
		"function drained() {\n" +
		"  return new AsyncGenAges().ages().next();\n" +
		"}\n"
	p := entryEnvTestProgram(t, source)
	registry := annotations.AnnotationRegistry{}
	objects := annotations.ObjectRegistry{}
	merged := map[*ast.Symbol]*FunctionContract{}
	contracts := CompileContractFileFacts(p, p.Entry, registry, objects, merged, false, func(assignability.RefinementDiagnostic) {})
	ctx := &FlowContext{
		P: p, Kernel: kernel, Registry: registry, Objects: objects, Contracts: contracts,
		Report:   func(assignability.RefinementDiagnostic) {},
		Aliases:  dataflowfacts.NewAliasClasses(),
		Declared: map[string]*annotations.DeclaredRefinement{},
	}
	SetEngineKernel(kernel)

	nextCall := generatorDrainTestFirstCallExpression(t, p.Entry.AsNode(), "next")
	env := NewEnv()
	result := evaluateExpression(ctx, env, nextCall)
	if result.Kind != abstractdomain.KindObject {
		t.Fatalf("....next() determined %+v, want the iterator result record (KindObject)", result)
	}
	var value *abstractdomain.AbstractValue
	var done *abstractdomain.AbstractValue
	for _, key := range result.Keys {
		if key.Name == "value" {
			v := key.Value
			value = &v
		}
		if key.Name == "done" {
			d := key.Value
			done = &d
		}
	}
	if value == nil {
		t.Fatalf("the result record carries no value key")
	}
	if value.Kind == abstractdomain.KindPossiblyUndefined {
		t.Errorf("value wears a PossiblyUndefined wrapper — want the exact 40 with no absence, since the first next() cannot be the finishing call")
	}
	if value.Kind != abstractdomain.KindValues || len(value.Values) != 1 || value.Values[0] != 40 {
		t.Errorf("value = %+v, want an exact KindValues(40)", *value)
	}
	if done == nil {
		t.Fatalf("the result record carries no done key")
	}
	if done.Kind != abstractdomain.KindValues || len(done.Values) != 1 || done.Values[0] != 0 {
		t.Errorf("done = %+v, want an exact false (0)", *done)
	}
}
