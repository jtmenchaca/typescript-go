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
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
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
	entry, returnCases, omission := ExportFunctionFact(ctx, contract)
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
	if len(row.ElementCases) != 1 {
		t.Fatalf("len(entry[0].ElementCases) = %d, want 1", len(row.ElementCases))
	}
	if row.ElementCases[0].Sort != CaseSortNumber {
		t.Errorf("entry[0].ElementCases[0].Sort = %q, want %q", row.ElementCases[0].Sort, CaseSortNumber)
	}
	if len(returnCases) != 1 {
		t.Fatalf("len(returnCases) = %d, want 1", len(returnCases))
	}
	if returnCases[0].Sort != CaseSortNumber {
		t.Errorf("returnCases[0].Sort = %q, want %q", returnCases[0].Sort, CaseSortNumber)
	}
	if len(returnCases[0].Set.Forms) == 0 {
		t.Errorf("returnCases[0].Set carries no forms — takeTriple's derived return should be a faithful set (xs.length)")
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

// TestExportFunctionFact_ADeclaredBooleanParameterExportsTheWholeSortFloorCase
// pins Item 2: a `z.boolean()`-declared parameter, reached through
// `z.infer<typeof zFlag>` exactly as every other declared-parameter
// case in this file is, must emit {"sort":"boolean"} in its entry
// row — not the {"sort":"number","set":{0,1}} reading caseOfSet gives
// every other two-member set, since chain_root_constructor.go's
// "boolean" root and type_node_aliases.go's z.infer threading now
// carry the KindTag "boolean" all the way from the schema constant to
// foreignEntryRowOf.
//
// The function returns a literal `true` rather than echoing its own
// parameter: DerivedReturnOf binds a declared parameter's in-body
// value through AbstractValueOfDeclared/setKindTagOf
// (walk/declared_value.go), which reads only "bigint"/"symbol" and
// answers abstractdomain.SetKindTagNone for "boolean" — carrying the
// return-side floor read (FaithfulReturnCases' own
// KindValues{PrimitiveBoolean} check) through an identity body is a
// SEPARATE gap outside foreignEntryRowOf/caseOfSet, the two functions
// this item names; a literal return keeps this test scoped to the
// entry row alone.
func TestExportFunctionFact_ADeclaredBooleanParameterExportsTheWholeSortFloorCase(t *testing.T) {
	kernel := parseVocabKernel(t)
	source := `import * as z from "/surface/z.ts";
const zFlag = z.boolean();
function alwaysTrue(flag: z.infer<typeof zFlag>): boolean { return true; }
`
	_, ctx, contract := factExportContractOf(t, kernel, source, "alwaysTrue")
	entry, returnCases, omission := ExportFunctionFact(ctx, contract)
	if omission != "" {
		t.Fatalf("ExportFunctionFact declined: %s", omission)
	}
	if len(entry) != 1 {
		t.Fatalf("len(entry) = %d, want 1", len(entry))
	}
	row := entry[0]
	if row.IsSequence {
		t.Fatalf("entry[0].IsSequence = true, want false — a declared boolean is a scalar")
	}
	if len(row.Cases) != 1 {
		t.Fatalf("len(entry[0].Cases) = %d, want 1: %+v", len(row.Cases), row.Cases)
	}
	if row.Cases[0].Sort != CaseSortBoolean {
		t.Errorf("entry[0].Cases[0].Sort = %q, want %q — a declared z.boolean() parameter must read as the whole-sort boolean case, not a {0,1} number case", row.Cases[0].Sort, CaseSortBoolean)
	}
	if len(row.Cases[0].Set.Forms) != 0 {
		t.Errorf("entry[0].Cases[0].Set = %+v, want no forms — a boolean case carries no set", row.Cases[0].Set)
	}
	if len(returnCases) != 1 {
		t.Fatalf("len(returnCases) = %d, want 1", len(returnCases))
	}
	if returnCases[0].Sort != CaseSortBoolean {
		t.Errorf("returnCases[0].Sort = %q, want %q — a literal-true return should read as the whole-sort boolean case (FaithfulReturnCases' own existing floor)", returnCases[0].Sort, CaseSortBoolean)
	}
}

// TestDerivedReturnOf_ALengthReadAnswersASetFaithfulReturnCasesAccepts
// pins the call-site-free derivation on the simplest body: `return
// xs.length` with no caller in view. DerivedReturnOf must answer a
// value FaithfulReturnCases accepts (a set_of_known reading), never an
// object/unknown refusal, and never require a call site to seed xs.
func TestDerivedReturnOf_ALengthReadAnswersASetFaithfulReturnCasesAccepts(t *testing.T) {
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
	cases, sentence := FaithfulReturnCases(derived)
	if sentence != "" {
		t.Fatalf("FaithfulReturnCases declined: %s", sentence)
	}
	if len(cases) != 1 {
		t.Fatalf("len(cases) = %d, want 1", len(cases))
	}
	if cases[0].Sort != CaseSortNumber {
		t.Errorf("cases[0].Sort = %q, want %q", cases[0].Sort, CaseSortNumber)
	}
	if len(cases[0].Set.Forms) == 0 {
		t.Errorf("FaithfulReturnCases answered an empty set for xs.length")
	}
}

// TestDerivedReturnOf_ANullableTernaryReturnEmitsTheInnerCasePlusNull
// pins the RULED schema's own rule: a DeclaredPossiblyUndefined-shaped
// derived return (here, a ternary whose one branch is null) emits the
// inner faithful case PLUS {"sort":"null"} appended — the vocabulary
// that used to have no reading at all (returnKindWords' "a
// possibly-absent value" omission).
func TestDerivedReturnOf_ANullableTernaryReturnEmitsTheInnerCasePlusNull(t *testing.T) {
	kernel := parseVocabKernel(t)
	source := `import * as z from "/surface/z.ts";
const zAge = z.number().int().min(0).max(120);
function clampToTen(age: z.infer<typeof zAge>): number | null { return age > 100 ? null : age; }
`
	_, ctx, contract := factExportContractOf(t, kernel, source, "clampToTen")
	derived, ok := DerivedReturnOf(ctx, contract)
	if !ok {
		t.Fatalf("DerivedReturnOf declined — want a call-site-free derivation off the declared parameter alone")
	}
	if derived.Kind != abstractdomain.KindPossiblyUndefined {
		t.Fatalf("derived.Kind = %v, want KindPossiblyUndefined", derived.Kind)
	}
	cases, sentence := FaithfulReturnCases(derived)
	if sentence != "" {
		t.Fatalf("FaithfulReturnCases declined: %s", sentence)
	}
	if len(cases) != 2 {
		t.Fatalf("len(cases) = %d, want 2 (the inner number case plus null): %+v", len(cases), cases)
	}
	if cases[0].Sort != CaseSortNumber {
		t.Errorf("cases[0].Sort = %q, want %q", cases[0].Sort, CaseSortNumber)
	}
	if len(cases[0].Set.Forms) == 0 {
		t.Errorf("cases[0].Set carries no forms — the inner case should be a faithful set")
	}
	if cases[1].Sort != CaseSortNull {
		t.Errorf("cases[1].Sort = %q, want %q — the null case must be LAST, appended after the inner case", cases[1].Sort, CaseSortNull)
	}
}

// TestDerivedReturnOf_AClampedArithmeticBodyAnswersASetFaithfulReturnCasesAccepts
// pins the derivation on a body that computes rather than merely
// reads a length: Math.min clamps a number()-declared parameter's
// window, and the clamp must still answer a faithful set with no
// call site in view.
func TestDerivedReturnOf_AClampedArithmeticBodyAnswersASetFaithfulReturnCasesAccepts(t *testing.T) {
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
	cases, sentence := FaithfulReturnCases(derived)
	if sentence != "" {
		t.Fatalf("FaithfulReturnCases declined: %s", sentence)
	}
	if len(cases) != 1 {
		t.Fatalf("len(cases) = %d, want 1", len(cases))
	}
	if len(cases[0].Set.Forms) == 0 {
		t.Errorf("FaithfulReturnCases answered an empty set for Math.min(age, 10)")
	}
}

// TestDerivedReturnOf_AStringPassthroughEmitsOneStringCase pins the
// RULED schema's string case: a z.string()-declared parameter passed
// straight through derives a KindSet whose forms are sequence-shaped
// (Star(Codepoints)), and FaithfulReturnCases reads that shape as a
// "string" case (caseOfSet's StatesSequence test) — the full kernel
// wire set grammar reused verbatim, never a private encoding.
func TestDerivedReturnOf_AStringPassthroughEmitsOneStringCase(t *testing.T) {
	kernel := parseVocabKernel(t)
	source := `import * as z from "/surface/z.ts";
const zName = z.string();
function echoName(name: z.infer<typeof zName>): string { return name; }
`
	_, ctx, contract := factExportContractOf(t, kernel, source, "echoName")
	derived, ok := DerivedReturnOf(ctx, contract)
	if !ok {
		t.Fatalf("DerivedReturnOf declined — want a call-site-free derivation off the declared parameter alone")
	}
	cases, sentence := FaithfulReturnCases(derived)
	if sentence != "" {
		t.Fatalf("FaithfulReturnCases declined: %s", sentence)
	}
	if len(cases) != 1 {
		t.Fatalf("len(cases) = %d, want 1", len(cases))
	}
	if cases[0].Sort != CaseSortString {
		t.Errorf("cases[0].Sort = %q, want %q", cases[0].Sort, CaseSortString)
	}
	if len(cases[0].Set.Forms) == 0 {
		t.Errorf("cases[0].Set carries no forms — a string passthrough should be a faithful set")
	}
}

// TestDerivedReturnOf_ABooleanLiteralReturnEmitsTheWholeSortFloorCase
// pins the boolean whole-sort floor: a boolean-literal derived
// KindValues{PrimitiveBoolean} return emits {"sort":"boolean"} rather
// than falling through to SetOfKnown's own {0,1} numeric reading —
// FaithfulReturnCases checks value.KindTag == PrimitiveBoolean BEFORE
// calling SetOfKnown, since SetOfKnown itself does not carry the tag
// through. (A comparison against a RANGE-typed parameter, e.g. `age
// === 18` against a min/max-bounded age, derives Unknown rather than a
// decided boolean here — this checker's comparison transfer decides
// only exact operands, so the literal-return body is the reachable
// path this test pins; a body deriving a decided comparison result
// would read identically once the comparison transfer decides one.)
func TestDerivedReturnOf_ABooleanLiteralReturnEmitsTheWholeSortFloorCase(t *testing.T) {
	kernel := parseVocabKernel(t)
	source := `import * as z from "/surface/z.ts";
const zAge = z.number().int().min(0).max(120);
function isAdult(age: z.infer<typeof zAge>): boolean { return true; }
`
	_, ctx, contract := factExportContractOf(t, kernel, source, "isAdult")
	derived, ok := DerivedReturnOf(ctx, contract)
	if !ok {
		t.Fatalf("DerivedReturnOf declined — want a call-site-free derivation off the declared parameter alone")
	}
	cases, sentence := FaithfulReturnCases(derived)
	if sentence != "" {
		t.Fatalf("FaithfulReturnCases declined: %s", sentence)
	}
	if len(cases) != 1 {
		t.Fatalf("len(cases) = %d, want 1: %+v", len(cases), cases)
	}
	if cases[0].Sort != CaseSortBoolean {
		t.Errorf("cases[0].Sort = %q, want %q", cases[0].Sort, CaseSortBoolean)
	}
	if len(cases[0].Set.Forms) != 0 {
		t.Errorf("cases[0].Set = %+v, want no forms — a boolean case carries no set", cases[0].Set)
	}
}

// TestDerivedReturnOf_AnObjectLiteralReturnEmitsTheObjectCaseWithMembers
// pins Item 1's exporter half: a body returning a plain object literal
// derives a KindObject AbstractValue, and FaithfulReturnCases must emit
// {"sort":"object","members":{...},"closed":bool} through objectCaseOf
// rather than refusing (returnKindWords' old "an object" omission) —
// each member's own cases list recursed through the same
// FaithfulReturnCases call, and Closed carrying the literal's own
// Complete fact (no spread, every key statically known) unchanged.
func TestDerivedReturnOf_AnObjectLiteralReturnEmitsTheObjectCaseWithMembers(t *testing.T) {
	kernel := parseVocabKernel(t)
	source := `import * as z from "/surface/z.ts";
const zAge = z.number().int().min(0).max(120);
function wrapAge(age: z.infer<typeof zAge>) { return { ok: true, value: age }; }
`
	_, ctx, contract := factExportContractOf(t, kernel, source, "wrapAge")
	derived, ok := DerivedReturnOf(ctx, contract)
	if !ok {
		t.Fatalf("DerivedReturnOf declined — want a call-site-free derivation off the declared parameter alone")
	}
	if derived.Kind != abstractdomain.KindObject {
		t.Fatalf("derived.Kind = %v, want KindObject", derived.Kind)
	}
	cases, sentence := FaithfulReturnCases(derived)
	if sentence != "" {
		t.Fatalf("FaithfulReturnCases declined: %s", sentence)
	}
	if len(cases) != 1 {
		t.Fatalf("len(cases) = %d, want 1: %+v", len(cases), cases)
	}
	c := cases[0]
	if c.Sort != CaseSortObject {
		t.Fatalf("cases[0].Sort = %q, want %q", c.Sort, CaseSortObject)
	}
	if !c.Closed {
		t.Errorf("cases[0].Closed = false, want true — a plain object literal with no spread states its exact key set")
	}
	if len(c.Members) != 2 {
		t.Fatalf("len(cases[0].Members) = %d, want 2: %+v", len(c.Members), c.Members)
	}
	okCases, hasOk := c.Members["ok"]
	if !hasOk {
		t.Fatalf("cases[0].Members carries no 'ok' key: %+v", c.Members)
	}
	if len(okCases) != 1 || okCases[0].Sort != CaseSortBoolean {
		t.Errorf("cases[0].Members[\"ok\"] = %+v, want one boolean case", okCases)
	}
	valueCases, hasValue := c.Members["value"]
	if !hasValue {
		t.Fatalf("cases[0].Members carries no 'value' key: %+v", c.Members)
	}
	if len(valueCases) != 1 || valueCases[0].Sort != CaseSortNumber {
		t.Errorf("cases[0].Members[\"value\"] = %+v, want one number case", valueCases)
	}
	if len(valueCases[0].Set.Forms) == 0 {
		t.Errorf("cases[0].Members[\"value\"][0].Set carries no forms — the age parameter's own window should still be a faithful set")
	}
}

// TestDerivedReturnOf_AStringLiteralUnionIndexedReturnEmitsAStringCase
// pins the fix for caseOfSet's string/number misread: a body indexing
// a string-literal array by a bounded parameter
// (`["ok", "warn", "error"][code]`) derives a join over three known
// strings, whose top-level form is a Union of sequence-shaped
// branches rather than a bare sequence form — StatesSequence alone
// misses this shape, and only SequenceShaped's recursive reading
// catches it. Array index access derives KindPossiblyUndefined (an
// out-of-bounds index reads as absent), so the RULED schema's own
// possibly-absent rule appends {"sort":"null"} after the inner case —
// the inner case is what this test pins: Sort == CaseSortString,
// never CaseSortNumber.
func TestDerivedReturnOf_AStringLiteralUnionIndexedReturnEmitsAStringCase(t *testing.T) {
	kernel := parseVocabKernel(t)
	source := `import * as z from "/surface/z.ts";
const zStatus = z.union([z.literal("ok"), z.literal("warn"), z.literal("error")]);
type Status = z.infer<typeof zStatus>;
function makeStatus(code: number): Status {
  const words: Status[] = ["ok", "warn", "error"];
  return words[code];
}
`
	_, ctx, contract := factExportContractOf(t, kernel, source, "makeStatus")
	entry, returnCases, omission := ExportFunctionFact(ctx, contract)
	if omission != "" {
		t.Fatalf("ExportFunctionFact declined: %s", omission)
	}
	if len(entry) != 1 {
		t.Fatalf("len(entry) = %d, want 1", len(entry))
	}
	if len(returnCases) != 2 {
		t.Fatalf("len(returnCases) = %d, want 2 (the string case plus the array-index's own possibly-absent null case): %+v", len(returnCases), returnCases)
	}
	if returnCases[0].Sort != CaseSortString {
		t.Errorf("returnCases[0].Sort = %q, want %q — a string-literal-union return must read as a string case, not a number case", returnCases[0].Sort, CaseSortString)
	}
	if len(returnCases[0].Set.Forms) == 0 {
		t.Errorf("returnCases[0].Set carries no forms — the derived return should still be a faithful set")
	}
	if returnCases[1].Sort != CaseSortNull {
		t.Errorf("returnCases[1].Sort = %q, want %q — the array index's own possibly-absent case must be last", returnCases[1].Sort, CaseSortNull)
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
			ElementCases:  []Case{{Sort: CaseSortNumber, Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{1}))}},
			LengthAtLeast: 3,
		},
	}
	returnCases := []Case{{Sort: CaseSortNumber, Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{1}))}}
	said := ProvenanceSaidOf(entry, returnCases)
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
