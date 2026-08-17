// The CLASS-TYPED PARAMETER bundle: which fields a class contributes
// once its constructor parameter properties are counted, what a
// class-annotated parameter's slots are spelled as, what one body does
// with them, and what the entries enter holding at a call.
//
// The parameter-property extraction reads SYNTAX alone, so those cases
// probe on a parsed source with no checker. The annotation side needs a
// checker to resolve the class name, so it builds a program through
// entryEnvTestProgram (the walk tests' canonical recipe) — and the
// nil-checker decline is pinned too, since the lowering depends on it.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

/* ── helpers ─────────────────────────────────────────────────────── */

// bundleParamClassOf parses a source whose first statement is a class
// and answers that class node.
func bundleParamClassOf(t *testing.T, source string) *ast.Node {
	t.Helper()
	statements := bundleParse(t, source)
	if len(statements) == 0 || !ast.IsClassLike(statements[0]) {
		t.Fatalf("no class declaration parsed from %q", source)
	}
	return statements[0]
}

// bundleParamNamesOf spells a field list as "<name>:<slot>:<sort>" rows,
// which is what every ordering and respelling case below compares.
func bundleParamNamesOf(fields []BundleField) []string {
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		out = append(out, field.Name+":"+field.SlotName+":"+string(field.Sort))
	}
	return out
}

func bundleParamRowsEqual(t *testing.T, got []string, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("field rows = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Errorf("field row %d = %q, want %q", index, got[index], want[index])
		}
	}
}

// bundleParamFunctionParameter finds a top-level function by name in a
// checked program and answers its parameter at `at`, plus its body.
func bundleParamFunctionParameter(
	t *testing.T,
	p *program.CheckerProgram,
	functionName string,
	at int,
) (parameter *ast.Node, body *ast.Node) {
	t.Helper()
	for _, statement := range p.Entry.Statements.Nodes {
		if !ast.IsFunctionDeclaration(statement) {
			continue
		}
		name := statement.AsFunctionDeclaration().Name()
		if name == nil || !ast.IsIdentifier(name) || name.Text() != functionName {
			continue
		}
		parameters := statement.Parameters()
		if at >= len(parameters) {
			t.Fatalf("function %s has %d parameters, wanted index %d", functionName, len(parameters), at)
		}
		return parameters[at], statement.Body()
	}
	t.Fatalf("no function named %s", functionName)
	return nil, nil
}

// bundleParamObject builds the argument a class-typed parameter's
// entries are filled from: one key per name, each an exact number.
func bundleParamObject(t *testing.T, order []string, values map[string]float64) abstractdomain.AbstractValue {
	t.Helper()
	keys := make([]abstractdomain.ObjectKey, 0, len(order))
	for _, name := range order {
		keys = append(keys, abstractdomain.ObjectKey{
			Name:  name,
			Value: abstractdomain.KnownValues([]float64{values[name]}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved),
		})
	}
	return abstractdomain.KnownObject(keys, nil, true, abstractdomain.TrustProved, false)
}

/* ── 1. constructor parameter properties ─────────────────────────── */

func TestBundleParams_AConstructorParameterPropertyIsAFieldWearingItsAnnotationsSort(t *testing.T) {
	classLike := bundleParamClassOf(t,
		"class Wrapper {\n"+
			"  constructor(private readonly count: number, public label: string, protected on: boolean) {}\n"+
			"}\n")
	fields := ConstructorParameterFields(classLike)
	// the SlotName comes back BARE, exactly as ClassFieldsOf spells its
	// own answer — the caller re-spells the union under one receiver
	bundleParamRowsEqual(t, bundleParamNamesOf(fields), []string{
		"count:count:" + string(BindingKindNumber),
		"label:label:" + string(BindingKindString),
		// a boolean rides the NUMBER sort — annotationSort's own rule, which
		// is what ClassFieldsOf applies to a declared property
		"on:on:" + string(BindingKindNumber),
	})
	if fields[2].TypeofTag != TypeofTagBoolean {
		t.Errorf("the boolean field's typeof = %q, want %q", fields[2].TypeofTag, TypeofTagBoolean)
	}
	if fields[0].TypeofTag != TypeofTagNumber || fields[1].TypeofTag != TypeofTagString {
		t.Errorf("the number/string fields' typeof = %q/%q", fields[0].TypeofTag, fields[1].TypeofTag)
	}
}

func TestBundleParams_APlainConstructorParameterDeclaresNoField(t *testing.T) {
	classLike := bundleParamClassOf(t,
		"class Wrapper {\n"+
			"  constructor(n: number, private kept: number) {}\n"+
			"}\n")
	fields := ConstructorParameterFields(classLike)
	// `n` carries no accessibility or readonly modifier, so it is a
	// parameter and nothing else — no instance holds it
	bundleParamRowsEqual(t, bundleParamNamesOf(fields), []string{
		"kept:kept:" + string(BindingKindNumber),
	})
}

func TestBundleParams_AParameterPropertyWithAnUnreadableAnnotationStillContributes(t *testing.T) {
	classLike := bundleParamClassOf(t,
		"class Wrapper {\n"+
			"  constructor(private readonly host: Container, private readonly n: number) {}\n"+
			"}\n")
	fields := ConstructorParameterFields(classLike)
	// the census REPORTS shape; a field nothing can sort wears the unknown
	// sort rather than dropping out and leaving a body's read unslotted
	bundleParamRowsEqual(t, bundleParamNamesOf(fields), []string{
		"host:host:" + string(BindingKindUnknown),
		"n:n:" + string(BindingKindNumber),
	})
	if fields[0].TypeofTag != TypeofTagNone {
		t.Errorf("an unsortable field's typeof = %q, want %q", fields[0].TypeofTag, TypeofTagNone)
	}
}

func TestBundleParams_AClassWithNoConstructorContributesNoParameterFields(t *testing.T) {
	classLike := bundleParamClassOf(t, "class Wrapper { a: number = 0; b: string = \"\"; }\n")
	if fields := ConstructorParameterFields(classLike); len(fields) != 0 {
		t.Errorf("ConstructorParameterFields = %v for a class with no constructor, want none", bundleParamNamesOf(fields))
	}
}

func TestBundleParams_ConstructorParameterFieldsDeclinesANonClassNode(t *testing.T) {
	statements := bundleParse(t, "function f(private_: number) { return private_; }\n")
	if fields := ConstructorParameterFields(statements[0]); fields != nil {
		t.Errorf("ConstructorParameterFields read a function declaration: %v", bundleParamNamesOf(fields))
	}
	if fields := ConstructorParameterFields(nil); fields != nil {
		t.Errorf("ConstructorParameterFields read nil: %v", bundleParamNamesOf(fields))
	}
}

func TestBundleParams_ClassBundleFieldsUnionsDeclaredMembersThenParameterProperties(t *testing.T) {
	classLike := bundleParamClassOf(t,
		"class Wrapper {\n"+
			"  declared: number = 0;\n"+
			"  static ignored: number = 0;\n"+
			"  constructor(private readonly injected: string) {}\n"+
			"  method(): number { return 1; }\n"+
			"}\n")
	fields, ok := ClassBundleFields(nil, classLike)
	if !ok {
		t.Fatalf("ClassBundleFields declined a class declaration")
	}
	// declared members first in declaration order, then the constructor
	// parameter properties in parameter order; a STATIC member and a
	// METHOD are neither
	bundleParamRowsEqual(t, bundleParamNamesOf(fields), []string{
		"declared:declared:" + string(BindingKindNumber),
		"injected:injected:" + string(BindingKindString),
	})
}

/* ── 2. the class-typed parameter bundle ─────────────────────────── */

func TestBundleParams_AClassTypedParameterRespellsTheClassFieldsUnderItsOwnName(t *testing.T) {
	p := entryEnvTestProgram(t,
		"class InstanceWrapper {\n"+
			"  metatype: number = 0;\n"+
			"  constructor(private readonly token: string) {}\n"+
			"}\n"+
			"export function f(wrapper: InstanceWrapper) { return wrapper.metatype; }\n")
	ctx := &FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}
	parameter, _ := bundleParamFunctionParameter(t, p, "f", 0)
	name, fields, ok := BundleParamFieldsOf(ctx, parameter)
	if !ok {
		t.Fatalf("BundleParamFieldsOf declined a class-typed parameter")
	}
	if name != "wrapper" {
		t.Errorf("holder = %q, want %q", name, "wrapper")
	}
	bundleParamRowsEqual(t, bundleParamNamesOf(fields), []string{
		"metatype:wrapper.metatype:" + string(BindingKindNumber),
		"token:wrapper.token:" + string(BindingKindString),
	})
}

func TestBundleParams_AnExtendsClassAnswersItsOwnDeclaredAndParameterFieldsOnly(t *testing.T) {
	p := entryEnvTestProgram(t,
		"class Base { inherited: number = 0; }\n"+
			"class Module extends Base {\n"+
			"  own: number = 0;\n"+
			"  constructor(private readonly token: string) { super(); }\n"+
			"}\n"+
			"export function f(m: Module) { return m.own; }\n")
	ctx := &FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}
	parameter, _ := bundleParamFunctionParameter(t, p, "f", 0)
	name, fields, ok := BundleParamFieldsOf(ctx, parameter)
	if !ok {
		t.Fatalf("a class with heritage declined — a class's OWN fields are still sound to read")
	}
	if name != "m" {
		t.Errorf("holder = %q, want %q", name, "m")
	}
	// `inherited` is declared in a declaration this reading never visits,
	// so it is simply NOT in the list — a body reading it finds no slot
	// and falls to the opaque floor
	bundleParamRowsEqual(t, bundleParamNamesOf(fields), []string{
		"own:m.own:" + string(BindingKindNumber),
		"token:m.token:" + string(BindingKindString),
	})
}

func TestBundleParams_AnInterfaceTypedParameterIsNotThisRoutesShape(t *testing.T) {
	p := entryEnvTestProgram(t,
		"interface ContextId { id: number }\n"+
			"export function f(c: ContextId) { return c.id; }\n")
	ctx := &FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}
	parameter, _ := bundleParamFunctionParameter(t, p, "f", 0)
	if _, _, ok := BundleParamFieldsOf(ctx, parameter); ok {
		t.Errorf("BundleParamFieldsOf read an interface — BundleTypeFieldsOf owns that shape")
	}
}

func TestBundleParams_AGenericClassAndAGenericReferenceBothDecline(t *testing.T) {
	p := entryEnvTestProgram(t,
		"class Box<T> { held: number = 0; }\n"+
			"class Plain { held: number = 0; }\n"+
			"export function f(b: Box<number>) { return b.held; }\n"+
			"export function g(b: Box) { return b.held; }\n")
	ctx := &FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}
	withArguments, _ := bundleParamFunctionParameter(t, p, "f", 0)
	if _, _, ok := BundleParamFieldsOf(ctx, withArguments); ok {
		t.Errorf("a reference carrying type arguments expanded — its fields depend on what was applied")
	}
	bare, _ := bundleParamFunctionParameter(t, p, "g", 0)
	if _, _, ok := BundleParamFieldsOf(ctx, bare); ok {
		t.Errorf("a class carrying type parameters expanded — its annotations are not any instance's own")
	}
}

func TestBundleParams_ANilContextDeclinesRatherThanCrashing(t *testing.T) {
	statements := bundleParse(t, "function f(wrapper: InstanceWrapper) { return wrapper.metatype; }\n")
	parameter := statements[0].Parameters()[0]
	if _, _, ok := BundleParamFieldsOf(nil, parameter); ok {
		t.Errorf("BundleParamFieldsOf resolved with no context")
	}
	if _, _, ok := BundleParamFieldsOf(&FlowContext{}, parameter); ok {
		t.Errorf("BundleParamFieldsOf resolved with no program")
	}
}

func TestBundleParams_AScalarAndAnUnannotatedParameterAreNoBundle(t *testing.T) {
	p := entryEnvTestProgram(t,
		"export function f(n: number, u) { return n; }\n")
	ctx := &FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}
	scalar, _ := bundleParamFunctionParameter(t, p, "f", 0)
	if _, _, ok := BundleParamFieldsOf(ctx, scalar); ok {
		t.Errorf("a `number` parameter expanded as a bundle")
	}
	unannotated, _ := bundleParamFunctionParameter(t, p, "f", 1)
	if _, _, ok := BundleParamFieldsOf(ctx, unannotated); ok {
		t.Errorf("an unannotated parameter expanded as a bundle")
	}
}

/* ── 3. the census composition ───────────────────────────────────── */

func TestBundleParams_TheCensusReportsReadsAndWritesInTheFieldSetsOwnOrder(t *testing.T) {
	p := entryEnvTestProgram(t,
		"class InstanceWrapper {\n"+
			"  metatype: number = 0;\n"+
			"  instance: number = 0;\n"+
			"  unused: number = 0;\n"+
			"  constructor(private readonly token: string) {}\n"+
			"}\n"+
			"export function f(wrapper: InstanceWrapper) {\n"+
			"  wrapper.instance = wrapper.metatype + 1;\n"+
			"  return wrapper.token;\n"+
			"}\n")
	ctx := &FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}
	parameter, body := bundleParamFunctionParameter(t, p, "f", 0)
	name, census, fields, ok := BundleParamCensus(ctx, body, parameter)
	if !ok {
		t.Fatalf("BundleParamCensus declined a class-typed parameter")
	}
	if name != "wrapper" {
		t.Errorf("holder = %q, want %q", name, "wrapper")
	}
	// the WHOLE field set comes back, not just the read ones
	bundleParamRowsEqual(t, bundleParamNamesOf(fields), []string{
		"metatype:wrapper.metatype:" + string(BindingKindNumber),
		"instance:wrapper.instance:" + string(BindingKindNumber),
		"unused:wrapper.unused:" + string(BindingKindNumber),
		"token:wrapper.token:" + string(BindingKindString),
	})
	// declaration order, never the body's mention order: metatype is read
	// after `instance` is written but comes first in the class
	bundleParamRowsEqual(t, bundleParamNamesOf(census.Reads), []string{
		"metatype:wrapper.metatype:" + string(BindingKindNumber),
		"token:wrapper.token:" + string(BindingKindString),
	})
	bundleParamRowsEqual(t, bundleParamNamesOf(census.Writes), []string{
		"instance:wrapper.instance:" + string(BindingKindNumber),
	})
	if census.Computed || census.Escapes {
		t.Errorf("census flags computed=%v escapes=%v, want both false", census.Computed, census.Escapes)
	}
}

func TestBundleParams_TheCensusReportsAnEscapingParameterBundle(t *testing.T) {
	p := entryEnvTestProgram(t,
		"class InstanceWrapper { metatype: number = 0; }\n"+
			"declare function sink(w: InstanceWrapper): void;\n"+
			"export function f(wrapper: InstanceWrapper) {\n"+
			"  sink(wrapper);\n"+
			"  return wrapper.metatype;\n"+
			"}\n")
	ctx := &FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}
	parameter, body := bundleParamFunctionParameter(t, p, "f", 0)
	_, census, _, ok := BundleParamCensus(ctx, body, parameter)
	if !ok {
		t.Fatalf("BundleParamCensus declined a class-typed parameter")
	}
	if !census.Escapes {
		t.Errorf("a bare mention of the bundle did not report an escape")
	}
}

func TestBundleParams_TheCensusDeclinesWhereTheBundleDoes(t *testing.T) {
	p := entryEnvTestProgram(t,
		"class InstanceWrapper { metatype: number = 0; }\n"+
			"export function f(n: number) { return n; }\n"+
			"export function g(wrapper: InstanceWrapper) { return wrapper.metatype; }\n")
	ctx := &FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}
	scalar, body := bundleParamFunctionParameter(t, p, "f", 0)
	if _, _, _, ok := BundleParamCensus(ctx, body, scalar); ok {
		t.Errorf("BundleParamCensus answered for a scalar parameter")
	}
	bundled, _ := bundleParamFunctionParameter(t, p, "g", 0)
	if _, _, _, ok := BundleParamCensus(ctx, nil, bundled); ok {
		t.Errorf("BundleParamCensus answered with no body to scan")
	}
}

/* ── 4. the entry states ─────────────────────────────────────────── */

func TestBundleParams_EntryStatesReadEachFieldOffAnObjectArgument(t *testing.T) {
	read := []BundleField{
		{Name: "metatype", SlotName: "wrapper.metatype", Sort: BindingKindNumber, TypeofTag: TypeofTagNumber},
		{Name: "instance", SlotName: "wrapper.instance", Sort: BindingKindNumber, TypeofTag: TypeofTagNumber},
	}
	argument := bundleParamObject(t, []string{"metatype", "instance"}, map[string]float64{
		"metatype": 3,
		"instance": 7,
	})
	states, ok := BundleParamEntryStates(argument, read)
	if !ok {
		t.Fatalf("BundleParamEntryStates declined an object argument")
	}
	if len(states) != 2 {
		t.Fatalf("len(states) = %d, want 2 — one per read field", len(states))
	}
	for index, state := range states {
		if state.Top {
			t.Fatalf("entry %d entered TOP although the argument names its field", index)
		}
	}
	// each entry holds its OWN field's value, in the read list's order —
	// entry 0 is metatype's 3 and entry 1 is instance's 7, never swapped
	if got, ok := bundleParamExactOf(states[0]); !ok || got != 3 {
		t.Errorf("entry 0 = %+v, want the exact value 3", states[0].Set)
	}
	if got, ok := bundleParamExactOf(states[1]); !ok || got != 7 {
		t.Errorf("entry 1 = %+v, want the exact value 7", states[1].Set)
	}
}

// bundleParamExactOf reads the one value a wire state's set holds, where
// it holds exactly one — what an entry filled from an exact-valued field
// spells.
func bundleParamExactOf(state kernelbridge.KnownStateWire) (float64, bool) {
	if state.Top || state.Undef || state.Null || state.Nan {
		return 0, false
	}
	if len(state.Set.Forms) != 1 || state.Set.Forms[0].Form != refinementsets.FormOneOf {
		return 0, false
	}
	if len(state.Set.Forms[0].W) != 1 {
		return 0, false
	}
	return state.Set.Forms[0].W[0], true
}

func TestBundleParams_AFieldTheArgumentDoesNotNameEntersTopNotAbsent(t *testing.T) {
	read := []BundleField{
		{Name: "metatype", SlotName: "wrapper.metatype", Sort: BindingKindNumber, TypeofTag: TypeofTagNumber},
		{Name: "missing", SlotName: "wrapper.missing", Sort: BindingKindNumber, TypeofTag: TypeofTagNumber},
	}
	argument := bundleParamObject(t, []string{"metatype"}, map[string]float64{"metatype": 3})
	states, ok := BundleParamEntryStates(argument, read)
	if !ok {
		t.Fatalf("BundleParamEntryStates declined an object argument missing a field")
	}
	if states[0].Top {
		t.Errorf("the NAMED field entered TOP")
	}
	if !states[1].Top {
		t.Errorf("the unnamed field entered %+v, want {Top:true}", states[1])
	}
	if states[1].Undef || states[1].Null {
		t.Errorf("the unnamed field entered ABSENT — no caller claimed the field is undefined")
	}
}

func TestBundleParams_ANonObjectArgumentTopsEveryEntryRatherThanKillingTheCall(t *testing.T) {
	read := []BundleField{
		{Name: "metatype", SlotName: "wrapper.metatype", Sort: BindingKindNumber, TypeofTag: TypeofTagNumber},
		{Name: "instance", SlotName: "wrapper.instance", Sort: BindingKindNumber, TypeofTag: TypeofTagNumber},
	}
	// a class instance the caller knows nothing about is the ROUTINE case:
	// declining here would kill nearly every call whose argument is an
	// ordinary instance, which is the whole reason the bundle exists
	for name, argument := range map[string]abstractdomain.AbstractValue{
		"silence": abstractdomain.AbstractValue{},
		"a number": abstractdomain.KnownValues([]float64{1},
			abstractdomain.PrimitiveNumber, abstractdomain.TrustProved),
	} {
		states, ok := BundleParamEntryStates(argument, read)
		if !ok {
			t.Fatalf("BundleParamEntryStates declined %s — a non-object argument tops, never declines", name)
		}
		if len(states) != 2 {
			t.Fatalf("len(states) = %d for %s, want 2", len(states), name)
		}
		for index, state := range states {
			if !state.Top {
				t.Errorf("%s: entry %d = %+v, want {Top:true}", name, index, state)
			}
			if state.Undef || state.Null {
				t.Errorf("%s: entry %d entered ABSENT", name, index)
			}
		}
	}
}

func TestBundleParams_AnEmptyReadListHasNoEntriesToFill(t *testing.T) {
	argument := bundleParamObject(t, []string{"metatype"}, map[string]float64{"metatype": 3})
	if states, ok := BundleParamEntryStates(argument, nil); ok || states != nil {
		t.Errorf("BundleParamEntryStates answered %v/%v for an empty read list", states, ok)
	}
}

func TestBundleParams_TheSlotSpellingReadsBackApartUnderItsHolder(t *testing.T) {
	if field, ok := BundleParamFieldNameOf("wrapper.metatype", "wrapper"); !ok || field != "metatype" {
		t.Errorf("BundleParamFieldNameOf(wrapper.metatype, wrapper) = %q/%v, want metatype/true", field, ok)
	}
	if _, ok := BundleParamFieldNameOf("this.count", "wrapper"); ok {
		t.Errorf("a `this` row read as a wrapper row")
	}
	if _, ok := BundleParamFieldNameOf("wrapper.", "wrapper"); ok {
		t.Errorf("an empty field name read as a row")
	}
	if _, ok := BundleParamFieldNameOf("wrapper", "wrapper"); ok {
		t.Errorf("the holder's own name read as a field row")
	}
}
