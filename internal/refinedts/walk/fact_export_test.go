// Unit tests for fact_export.go: ExportFunctionFact's entry/omission
// split and DerivedReturnOf's call-site-free derivation, built the
// same way e_class_and_function_accessor_rows_test.go and
// parse_and_chain_vocabulary_test.go build a real checker-backed
// program — CompileAnnotationFileFacts then CompileContractFileFacts
// against the vendored z.ts surface stand-in, never a hand-built
// DeclaredRefinement standing in for what a real annotation compiles
// to.

package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// factExportContractOf compiles source through the annotation and
// contract passes (eRowRun's own order: annotations first, so a
// return type reading `z.infer<typeof …>` resolves) and answers the
// named function's own compiled contract plus a ready FlowContext.
func factExportContractOf(t *testing.T, kernel *kernelbridge.RefinedTSKernel, source string, name string) (*program.CheckerProgram, *FlowContext, *FunctionContract) {
	t.Helper()
	p := parseVocabProgram(t, source)
	registry := annotations.AnnotationRegistry{}
	objects := annotations.ObjectRegistry{}
	annotations.CompileAnnotationFileFacts(p, p.Entry,
		annotations.AnnotationFileFactsMerged{Registry: registry, Objects: objects},
		kernel, false, func(assignability.RefinementDiagnostic) {})
	merged := map[*ast.Symbol]*FunctionContract{}
	contracts := CompileContractFileFacts(p, p.Entry, registry, objects, merged, false, func(assignability.RefinementDiagnostic) {})
	fn := parseVocabFunctionNamed(t, p, name)
	symbol := p.Checker.GetSymbolAtLocation(fn.Name())
	if symbol == nil {
		t.Fatalf("the checker resolved no symbol for %s", name)
	}
	contract, ok := contracts[symbol]
	if !ok {
		t.Fatalf("CompileContractFileFacts registered no contract for %s", name)
	}
	ctx := &FlowContext{
		P:         p,
		Registry:  registry,
		Objects:   objects,
		Contracts: contracts,
		Report:    func(assignability.RefinementDiagnostic) {},
		Aliases:   dataflowfacts.NewAliasClasses(),
		Declared:  map[string]*annotations.DeclaredRefinement{},
		Kernel:    kernel,
	}
	return p, ctx, contract
}

// TestExportFunctionFact_AOneParamBoundedArrayExportsASequenceRowWithTheFloor
// pins the sequence split: `z.array(z.number()).min(3)` reads back
// through AsRepetition as a repetition window, and the entry row
// carries that window's own Lo as LengthAtLeast — never a
// re-derived or hand-computed floor.
func TestExportFunctionFact_AOneParamBoundedArrayExportsASequenceRowWithTheFloor(t *testing.T) {
	kernel := parseVocabKernel(t)
	source := `import * as z from "/surface/z.ts";
const zTriple = z.array(z.number()).min(3);
function takeTriple(xs: z.infer<typeof zTriple>): number { return xs.length; }
`
	_, ctx, contract := factExportContractOf(t, kernel, source, "takeTriple")
	entry, returnSet, omission := ExportFunctionFact(ctx, contract)
	if omission != "" {
		t.Fatalf("ExportFunctionFact declined: %s", omission)
	}
	if len(entry) != 1 {
		t.Fatalf("len(entry) = %d, want 1", len(entry))
	}
	row := entry[0]
	if row.Name != "xs" {
		t.Errorf("entry[0].Name = %q, want %q", row.Name, "xs")
	}
	if !row.IsSequence {
		t.Fatalf("entry[0].IsSequence = false, want true — z.array(...).min(3) should read as a sequence")
	}
	if row.LengthAtLeast != 3 {
		t.Errorf("entry[0].LengthAtLeast = %d, want 3", row.LengthAtLeast)
	}
	if len(returnSet.Forms) == 0 {
		t.Errorf("returnSet carries no forms — takeTriple's derived return should be a faithful set (xs.length)")
	}
}

// TestExportFunctionFact_ATwoParamFunctionIsOmittedByName pins the
// symmetric refusal: the consumer already declines any function with
// more than one entry, so the exporter refuses before reading either
// parameter's shape, and the sentence names the reason (not a
// specific parameter, since neither was reached).
func TestExportFunctionFact_ATwoParamFunctionIsOmittedByName(t *testing.T) {
	kernel := parseVocabKernel(t)
	source := `import * as z from "/surface/z.ts";
const zTriple = z.array(z.number()).min(3);
function takeTwo(xs: z.infer<typeof zTriple>, ys: z.infer<typeof zTriple>): number { return xs.length; }
`
	_, ctx, contract := factExportContractOf(t, kernel, source, "takeTwo")
	entry, _, omission := ExportFunctionFact(ctx, contract)
	if omission == "" {
		t.Fatalf("ExportFunctionFact did not decline a two-parameter function")
	}
	if entry != nil {
		t.Errorf("entry = %+v, want nil alongside a non-empty omission", entry)
	}
	wantSubstring := "more than one parameter"
	if !containsSubstring(omission, wantSubstring) {
		t.Errorf("omission = %q, want it to name %q", omission, wantSubstring)
	}
}

// TestExportFunctionFact_ARestParamIsOmittedByName pins the rest-
// parameter refusal: a `...rest` parameter states no fixed entry
// shape, and the sentence names the parameter.
func TestExportFunctionFact_ARestParamIsOmittedByName(t *testing.T) {
	kernel := parseVocabKernel(t)
	source := `import * as z from "/surface/z.ts";
function takeRest(...rest: number[]): number { return rest.length; }
`
	_, ctx, contract := factExportContractOf(t, kernel, source, "takeRest")
	entry, _, omission := ExportFunctionFact(ctx, contract)
	if omission == "" {
		t.Fatalf("ExportFunctionFact did not decline a rest parameter")
	}
	if entry != nil {
		t.Errorf("entry = %+v, want nil alongside a non-empty omission", entry)
	}
	if !containsSubstring(omission, "rest") {
		t.Errorf("omission = %q, want it to name the rest parameter", omission)
	}
	if !containsSubstring(omission, "rest'") && !containsSubstring(omission, "'rest") {
		t.Errorf("omission = %q, want it to name the parameter by its own bound name", omission)
	}
}

// TestDerivedReturnOf_ALengthReadAnswersASetFaithfulReturnSetAccepts
// pins the call-site-free derivation on the simplest body: `return
// xs.length` with no caller in view. DerivedReturnOf must answer a
// value FaithfulReturnSet accepts (a set_of_known reading), never an
// object/unknown refusal, and never require a call site to seed xs.
func TestDerivedReturnOf_ALengthReadAnswersASetFaithfulReturnSetAccepts(t *testing.T) {
	kernel := parseVocabKernel(t)
	source := `import * as z from "/surface/z.ts";
const zTriple = z.array(z.number()).min(3);
function takeTriple(xs: z.infer<typeof zTriple>): number { return xs.length; }
`
	_, ctx, contract := factExportContractOf(t, kernel, source, "takeTriple")
	derived, ok := DerivedReturnOf(ctx, contract)
	if !ok {
		t.Fatalf("DerivedReturnOf declined — want a call-site-free derivation off the declared parameter alone")
	}
	set, sentence := FaithfulReturnSet(derived)
	if sentence != "" {
		t.Fatalf("FaithfulReturnSet declined: %s", sentence)
	}
	if len(set.Forms) == 0 {
		t.Errorf("FaithfulReturnSet answered an empty set for xs.length")
	}
}

// TestDerivedReturnOf_AClampedArithmeticBodyAnswersASetFaithfulReturnSetAccepts
// pins the derivation on a body that computes rather than merely
// reads a length: Math.min clamps a number()-declared parameter's
// window, and the clamp must still answer a faithful set with no
// call site in view.
func TestDerivedReturnOf_AClampedArithmeticBodyAnswersASetFaithfulReturnSetAccepts(t *testing.T) {
	kernel := parseVocabKernel(t)
	source := `import * as z from "/surface/z.ts";
const zAge = z.number().int().min(0).max(120);
function clampToTen(age: z.infer<typeof zAge>): number { return Math.min(age, 10); }
`
	_, ctx, contract := factExportContractOf(t, kernel, source, "clampToTen")
	derived, ok := DerivedReturnOf(ctx, contract)
	if !ok {
		t.Fatalf("DerivedReturnOf declined — want a call-site-free derivation off the declared parameter alone")
	}
	set, sentence := FaithfulReturnSet(derived)
	if sentence != "" {
		t.Fatalf("FaithfulReturnSet declined: %s", sentence)
	}
	if len(set.Forms) == 0 {
		t.Errorf("FaithfulReturnSet answered an empty set for Math.min(age, 10)")
	}
}

// TestProvenanceSaidOf_RendersTheEntryAndReturnThroughFormatForDiagnostics
// pins ProvenanceSaidOf's shape against a hand-built entry/return pair
// (no program needed — this function only formats what it is handed).
func TestProvenanceSaidOf_RendersTheEntryAndReturnThroughFormatForDiagnostics(t *testing.T) {
	entry := []ForeignEntryRow{
		{
			Name:          "xs",
			IsSequence:    true,
			Element:       refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{1})),
			LengthAtLeast: 3,
		},
	}
	returnSet := refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{1}))
	said := ProvenanceSaidOf(entry, returnSet)
	if !containsSubstring(said, "given 'xs'") {
		t.Errorf("said = %q, want it to open with the entry row's own name", said)
	}
	if !containsSubstring(said, "length is at least 3") {
		t.Errorf("said = %q, want it to name the length floor", said)
	}
	if !containsSubstring(said, "this body's returns derive") {
		t.Errorf("said = %q, want the fixed return clause", said)
	}
}

// containsSubstring is strings.Contains, spelled locally so this test
// file's import list stays exactly what its assertions need.
func containsSubstring(haystack string, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
