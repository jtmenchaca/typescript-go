// Pins two of the seven c-reads-and-values.ts §C collection-read rows
// that still fired on in-set values before this unit:
//
//   - arraySort (783) / arrayReverse (795): `sort()` and `reverse()`
//     had NO write-transfer model anywhere in the walk — a bare
//     `ages.sort()` statement evaluated the call (readUnmodeledMethod,
//     the end of the builtin dispatch chain) without updating the
//     tracked array, so `ages[0]` right after read the ORIGINAL
//     unsorted order, undetermined. readArraySortReverseMethods
//     (array_method_models.go) closes both, no-comparator form only —
//     sort's default comparator converts each element to its decimal
//     string and orders by THAT (sec-array.prototype.sort via
//     sec-comparearrayelements), never the numeric order.
//   - objectEntries (1150): `Object.entries(o)[0][1]` — a DOUBLE index
//     off a call result. ElementAccessOf's call-result arm
//     (element_access.go) only matched a receiver that was itself a
//     CallExpression, so the inner `[0]` read the exact pair but the
//     outer `[1]` had no arm to answer through and fell to the
//     type-seeded silence. The gate now also admits a receiver that is
//     itself an ElementAccessExpression, so the outer index recurses
//     through the same arm the inner one used.
//
// Each case runs the real AnalyzeFunction pass — contract compile,
// body walk, result check — the same route the fixture's own
// @refinedts-expect-error rows are judged by, and asserts on the
// reported diagnostics rather than reaching into one internal value:
// an in-set case wants NO diagnostic (silent — the value determined
// and it held the stated set), an out-of-set case wants exactly one
// 7001 refutation (determined and outside it). A stated set is a
// plain number-literal union here (`40 | 41` and kin) rather than the
// zod Age refinement the fixture uses — the same DeclaredSet contract
// route yield_contract_test.go's `normal(): 10 | 40` pins, with no
// import needed for what this unit changed: exact VALUE determination,
// not the refinement machinery.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/narrowing"
)

// collectionLeftoversKernel loads the native kernel the way every
// other kernel-backed walk test in this package does, or skips.
func collectionLeftoversKernel(t *testing.T) *kernelbridge.RefinedTSKernel {
	t.Helper()
	if !kernelbridge.KernelArtifactsPresent(kernelbridge.DylibPath) {
		t.Skip("native kernel dylib absent — build it first")
	}
	kernel, err := kernelbridge.LoadKernel(kernelbridge.DylibPath)
	if err != nil {
		t.Fatalf("LoadKernel: %v", err)
	}
	SetEngineKernel(kernel)
	SetTransferKernel(kernel)
	narrowing.SetNarrowKernel(kernel)
	return kernel
}

// collectionLeftoversContractOf compiles one file's contracts the way
// the real pass 2 does and hands back the named function's contract
// plus a FlowContext whose Report appends to a fresh diagnostics
// slice — yieldContractOf's pattern, reused here so both test files
// build a FlowContext identically.
func collectionLeftoversContractOf(t *testing.T, source string, name string) (*FunctionContract, *FlowContext, *[]assignability.RefinementDiagnostic) {
	t.Helper()
	p := entryEnvTestProgram(t, source)
	registry := annotations.AnnotationRegistry{}
	objects := annotations.ObjectRegistry{}
	merged := map[*ast.Symbol]*FunctionContract{}
	contracts := CompileContractFileFacts(p, p.Entry, registry, objects, merged, false, func(assignability.RefinementDiagnostic) {})
	fn := entryEnvFunctionNamed(t, p, name)
	symbol := p.Checker.GetSymbolAtLocation(fn.Name())
	if symbol == nil {
		t.Fatalf("the checker resolved no symbol for %s", name)
	}
	contract, ok := contracts[symbol]
	if !ok {
		t.Fatalf("CompileContractFileFacts registered no contract for %s", name)
	}
	var diagnostics []assignability.RefinementDiagnostic
	ctx := &FlowContext{
		P:         p,
		Registry:  registry,
		Objects:   objects,
		Contracts: contracts,
		Report: func(d assignability.RefinementDiagnostic) {
			diagnostics = append(diagnostics, d)
		},
		Aliases:  dataflowfacts.NewAliasClasses(),
		Declared: map[string]*annotations.DeclaredRefinement{},
	}
	return contract, ctx, &diagnostics
}

// collectionLeftoversRun runs AnalyzeFunction for one named function
// and hands back the diagnostics it reported.
func collectionLeftoversRun(t *testing.T, kernel *kernelbridge.RefinedTSKernel, source string, name string) []assignability.RefinementDiagnostic {
	t.Helper()
	contract, ctx, diagnostics := collectionLeftoversContractOf(t, source, name)
	ctx.Kernel = kernel
	AnalyzeFunction(ctx, contract, nil)
	return *diagnostics
}

func collectionLeftoversWantSilent(t *testing.T, diagnostics []assignability.RefinementDiagnostic, label string) {
	t.Helper()
	if len(diagnostics) != 0 {
		t.Errorf("%s reported %d diagnostic(s), want none (in-set, determined): %+v", label, len(diagnostics), diagnostics)
	}
}

func collectionLeftoversWantRefuted(t *testing.T, diagnostics []assignability.RefinementDiagnostic, label string) {
	t.Helper()
	if len(diagnostics) != 1 {
		t.Fatalf("%s reported %d diagnostic(s), want exactly one 7001 refutation: %+v", label, len(diagnostics), diagnostics)
	}
	if diagnostics[0].Code != 7001 {
		t.Errorf("%s reported code %d, want 7001 (a determined out-of-set value) — 7002 would mean the value stayed undetermined", label, diagnostics[0].Code)
	}
}

// TestReadArraySortReverseMethods_SortOrdersByDecimalStringNotNumber
// pins arraySort (c-reads-and-values.ts:783/787): `[41, 40].sort()`
// carries the tracked array through to `ages[0]` at its SORTED value,
// string order per CompareArrayElements' default arm.
func TestReadArraySortReverseMethods_SortOrdersByDecimalStringNotNumber(t *testing.T) {
	kernel := collectionLeftoversKernel(t)
	source := "function f(): 40 | 41 {\n" +
		"  const ages = [41, 40];\n" +
		"  ages.sort();\n" +
		"  return ages[0];\n" +
		"}\n" +
		"function g(): 40 | 41 {\n" +
		"  const overs = [201, 200];\n" +
		"  overs.sort();\n" +
		"  return overs[0];\n" +
		"}\n"
	collectionLeftoversWantSilent(t, collectionLeftoversRun(t, kernel, source, "f"), "[41,40].sort(); ages[0] (want 40)")
	collectionLeftoversWantRefuted(t, collectionLeftoversRun(t, kernel, source, "g"), "[201,200].sort(); overs[0] (want 200, out of {40,41})")
}

// TestReadArraySortReverseMethods_SortIsStringOrderEvenAcrossDigitCounts
// is the sharper case CLAUDE.md's task named directly: [9, 10] sorts
// to [10, 9] under the default comparator (the strings "10" < "9"),
// never the numeric order [9, 10] a value-blind port would produce.
func TestReadArraySortReverseMethods_SortIsStringOrderEvenAcrossDigitCounts(t *testing.T) {
	kernel := collectionLeftoversKernel(t)
	source := "function f(): 9 | 10 {\n" +
		"  const ns = [9, 10];\n" +
		"  ns.sort();\n" +
		"  return ns[0];\n" +
		"}\n"
	// ns[0] after sort() is exactly 10 (string order), not 9 (numeric
	// order) — a target excluding 10 must refute, which only holds if
	// the model read the STRING comparison
	collectionLeftoversWantSilent(t, collectionLeftoversRun(t, kernel, source, "f"), "[9,10].sort(); ns[0] (want 10, string order)")
}

// TestReadArraySortReverseMethods_ReverseIsExactInPlace pins
// arrayReverse (c-reads-and-values.ts:795/799): `reverse()` carries
// the tracked array through to `ages[0]` at its reversed value.
func TestReadArraySortReverseMethods_ReverseIsExactInPlace(t *testing.T) {
	kernel := collectionLeftoversKernel(t)
	source := "function f(): 40 | 41 {\n" +
		"  const ages = [40, 41];\n" +
		"  ages.reverse();\n" +
		"  return ages[0];\n" +
		"}\n" +
		"function g(): 40 | 41 {\n" +
		"  const overs = [200, 201];\n" +
		"  overs.reverse();\n" +
		"  return overs[0];\n" +
		"}\n"
	collectionLeftoversWantSilent(t, collectionLeftoversRun(t, kernel, source, "f"), "[40,41].reverse(); ages[0] (want 41)")
	collectionLeftoversWantRefuted(t, collectionLeftoversRun(t, kernel, source, "g"), "[200,201].reverse(); overs[0] (want 201, out of {40,41})")
}

// TestElementAccessOf_ADoubleIndexOffObjectEntriesReadsThePairsSecondSlot
// pins objectEntries (c-reads-and-values.ts:1150/1154):
// `Object.entries(o)[0][1]` reads the value half of the one entry pair
// through TWO chained element-access reads off the same call result.
func TestElementAccessOf_ADoubleIndexOffObjectEntriesReadsThePairsSecondSlot(t *testing.T) {
	kernel := collectionLeftoversKernel(t)
	source := "function f(): 40 {\n" +
		"  const person = { age: 40 };\n" +
		"  return Object.entries(person)[0][1];\n" +
		"}\n" +
		"function g(): 40 {\n" +
		"  const over = { age: 200 };\n" +
		"  return Object.entries(over)[0][1];\n" +
		"}\n"
	collectionLeftoversWantSilent(t, collectionLeftoversRun(t, kernel, source, "f"), "Object.entries({age:40})[0][1] (want 40)")
	collectionLeftoversWantRefuted(t, collectionLeftoversRun(t, kernel, source, "g"), "Object.entries({age:200})[0][1] (want 200, out of {40})")
}
