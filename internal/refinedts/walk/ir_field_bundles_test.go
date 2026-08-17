// The field census: which declared fields a class or an annotation
// contributes, how each one is sorted, and what one body does with the
// bundle it stands for.
//
// Everything the class side answers reads syntax alone, so it probes on
// parsed sources with no checker. The annotation side splits: a type
// literal spells its own members and probes the same way, while a type
// REFERENCE needs a checker to resolve the name — the walk tests build
// no program, so those cases are pinned at their nil-tolerant decline
// (which is the behaviour the lowering depends on: no checker means the
// bundle declines, never a crash).
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/parser"
)

// bundleParse parses a source and answers its top-level statements —
// the census reads syntax, so no checker is involved.
func bundleParse(t *testing.T, source string) []*ast.Node {
	t.Helper()
	opts := ast.SourceFileParseOptions{FileName: "/bundle.ts", Path: "/bundle.ts"}
	file := parser.ParseSourceFile(opts, source, core.ScriptKindTS)
	return file.Statements.Nodes
}

// bundleClassOf parses a source whose first statement is a class and
// answers the fields ClassFieldsOf reads off it.
func bundleClassOf(t *testing.T, source string) []BundleField {
	t.Helper()
	statements := bundleParse(t, source)
	fields, ok := ClassFieldsOf(nil, statements[0])
	if !ok {
		t.Fatalf("ClassFieldsOf declined %q — it is a class declaration", source)
	}
	return fields
}

// bundleMethodBodyOf parses a class and answers the body of its first
// METHOD — the field declarations that come before it are the bundle the
// census is about, so the method is rarely member zero.
func bundleMethodBodyOf(t *testing.T, source string) *ast.Node {
	t.Helper()
	statements := bundleParse(t, source)
	for _, member := range statements[0].AsClassDeclaration().Members.Nodes {
		if !ast.IsMethodDeclaration(member) {
			continue
		}
		body := member.Body()
		if body == nil {
			t.Fatalf("the first method of %q has no body", source)
		}
		return body
	}
	t.Fatalf("no method declaration in %q", source)
	return nil
}

// bundleFunctionBodyOf parses a function declaration and answers its
// body block — the named-receiver cases read a parameter, not `this`.
func bundleFunctionBodyOf(t *testing.T, source string) *ast.Node {
	t.Helper()
	statements := bundleParse(t, source)
	body := statements[0].Body()
	if body == nil {
		t.Fatalf("the function of %q has no body", source)
	}
	return body
}

// fieldNames is the spelled names of a field list, in its own order.
func fieldNames(fields []BundleField) []string {
	names := make([]string, len(fields))
	for index, field := range fields {
		names[index] = field.Name
	}
	return names
}

// sameNames compares a field list's names against what a case expects.
func sameNames(fields []BundleField, want []string) bool {
	if len(fields) != len(want) {
		return false
	}
	for index, name := range want {
		if fields[index].Name != name {
			return false
		}
	}
	return true
}

/* ── the declared field set ──────────────────────────────────────── */

func TestFieldBundles_AClassContributesItsDeclaredInstanceFieldsInOrder(t *testing.T) {
	fields := bundleClassOf(t, `
		class Injector {
			container: number;
			depth: number;
			label: string;
		}
	`)
	if !sameNames(fields, []string{"container", "depth", "label"}) {
		t.Fatalf("fields = %v, want [container depth label] in declaration order", fieldNames(fields))
	}
}

func TestFieldBundles_AFieldIsSortedByItsOwnAnnotation(t *testing.T) {
	fields := bundleClassOf(t, `
		class Shape {
			count: number;
			flag: boolean;
			name: string;
			held: Injector;
		}
	`)
	wantSorts := []BindingKind{BindingKindNumber, BindingKindNumber, BindingKindString, BindingKindUnknown}
	wantTags := []TypeofTag{TypeofTagNumber, TypeofTagBoolean, TypeofTagString, TypeofTagNone}
	if len(fields) != 4 {
		t.Fatalf("fields = %v, want four", fieldNames(fields))
	}
	for index, field := range fields {
		if field.Sort != wantSorts[index] {
			t.Errorf("%s sort = %q, want %q", field.Name, field.Sort, wantSorts[index])
		}
		if field.TypeofTag != wantTags[index] {
			t.Errorf("%s typeof = %q, want %q", field.Name, field.TypeofTag, wantTags[index])
		}
	}
}

func TestFieldBundles_AFieldWithNoReadableAnnotationStillContributes(t *testing.T) {
	// the census REPORTS shape — an unsortable field is an unknown-sorted
	// slot, not a reason to answer nothing
	fields := bundleClassOf(t, `
		class Wrapper {
			metatype;
			instance: Record<string, number>;
			depth: number;
		}
	`)
	if !sameNames(fields, []string{"metatype", "instance", "depth"}) {
		t.Fatalf("fields = %v, want all three — an unsortable field still reports", fieldNames(fields))
	}
	if fields[0].Sort != BindingKindUnknown || fields[1].Sort != BindingKindUnknown {
		t.Errorf("sorts = %q/%q, want unknown for both", fields[0].Sort, fields[1].Sort)
	}
	if fields[2].Sort != BindingKindNumber {
		t.Errorf("depth sort = %q, want number — a readable field beside unreadable ones still sorts", fields[2].Sort)
	}
}

func TestFieldBundles_StaticMembersAccessorsAndMethodsAreNotFields(t *testing.T) {
	// a static belongs to the constructor object; a method resolves as a
	// CALL through its own summary; an accessor with a body runs code
	fields := bundleClassOf(t, `
		class Module {
			static registry: number;
			depth: number;
			get size(): number { return this.depth; }
			set size(v: number) { this.depth = v; }
			load(): number { return this.depth; }
			constructor() { this.depth = 0; }
		}
	`)
	if !sameNames(fields, []string{"depth"}) {
		t.Fatalf("fields = %v, want [depth] only", fieldNames(fields))
	}
}

func TestFieldBundles_AComputedFieldNameSpellsNoSlotAndIsSkipped(t *testing.T) {
	// nothing spells the slot, so the field is not in the set; the class
	// still reports the fields it CAN spell rather than declining
	fields := bundleClassOf(t, `
		class Bag {
			[key]: number;
			depth: number;
		}
	`)
	if !sameNames(fields, []string{"depth"}) {
		t.Fatalf("fields = %v, want [depth] — a computed name spells no slot", fieldNames(fields))
	}
}

func TestFieldBundles_ANonClassNodeIsTheOnlyDeclineOfTheClassSide(t *testing.T) {
	statements := bundleParse(t, `function f(): number { return 1; }`)
	if _, ok := ClassFieldsOf(nil, statements[0]); ok {
		t.Errorf("ClassFieldsOf accepted a function declaration — only a class-like node has instance fields")
	}
	if _, ok := ClassFieldsOf(nil, nil); ok {
		t.Errorf("ClassFieldsOf accepted nil")
	}
}

func TestFieldBundles_AClassExpressionReadsTheSameAsADeclaration(t *testing.T) {
	statements := bundleParse(t, `const C = class { depth: number; label: string; };`)
	initializer := statements[0].AsVariableStatement().DeclarationList.
		AsVariableDeclarationList().Declarations.Nodes[0].
		AsVariableDeclaration().Initializer
	fields, ok := ClassFieldsOf(nil, initializer)
	if !ok {
		t.Fatalf("ClassFieldsOf declined a class expression")
	}
	if !sameNames(fields, []string{"depth", "label"}) {
		t.Fatalf("fields = %v, want [depth label]", fieldNames(fields))
	}
}

func TestFieldBundles_FieldsRespellUnderTheReceiverTheBodyReadsThemBy(t *testing.T) {
	fields := bundleClassOf(t, `class Injector { container: number; depth: number; }`)
	underThis := BundleFieldsAs("this", fields)
	if underThis[0].SlotName != "this.container" || underThis[1].SlotName != "this.depth" {
		t.Fatalf("slot names = %q/%q, want this.container/this.depth", underThis[0].SlotName, underThis[1].SlotName)
	}
	underName := BundleFieldsAs("wrapper", fields)
	if underName[0].SlotName != "wrapper.container" {
		t.Fatalf("slot name = %q, want wrapper.container", underName[0].SlotName)
	}
	// the re-spelling carries the sorts across untouched
	if underName[0].Sort != fields[0].Sort || underName[0].TypeofTag != fields[0].TypeofTag {
		t.Errorf("the re-spelling moved a sort — %q/%q vs %q/%q",
			underName[0].Sort, underName[0].TypeofTag, fields[0].Sort, fields[0].TypeofTag)
	}
}

/* ── the annotation side ─────────────────────────────────────────── */

// bundleParameterTypeOf is the type annotation of a function's first
// parameter.
func bundleParameterTypeOf(t *testing.T, source string) *ast.Node {
	t.Helper()
	statements := bundleParse(t, source)
	return statements[0].Parameters()[0].AsParameterDeclaration().Type
}

func TestFieldBundles_ATypeLiteralAnnotationSpellsItsOwnMembersWithoutAChecker(t *testing.T) {
	typeNode := bundleParameterTypeOf(t, `function f(p: { lo: number, hi: string, held: Date }): number { return p.lo; }`)
	fields, ok := BundleTypeFieldsOf(nil, typeNode)
	if !ok {
		t.Fatalf("BundleTypeFieldsOf declined a type literal — it spells its own members")
	}
	if !sameNames(fields, []string{"lo", "hi", "held"}) {
		t.Fatalf("fields = %v, want [lo hi held]", fieldNames(fields))
	}
	if fields[0].Sort != BindingKindNumber || fields[1].Sort != BindingKindString || fields[2].Sort != BindingKindUnknown {
		t.Errorf("sorts = %q/%q/%q, want number/string/unknown", fields[0].Sort, fields[1].Sort, fields[2].Sort)
	}
}

func TestFieldBundles_AMethodSignatureInATypeLiteralIsNotAField(t *testing.T) {
	typeNode := bundleParameterTypeOf(t, `function f(p: { lo: number, load(): number }): number { return p.lo; }`)
	fields, ok := BundleTypeFieldsOf(nil, typeNode)
	if !ok {
		t.Fatalf("BundleTypeFieldsOf declined a type literal")
	}
	if !sameNames(fields, []string{"lo"}) {
		t.Fatalf("fields = %v, want [lo] — a method signature resolves as a call, not a slot", fieldNames(fields))
	}
}

func TestFieldBundles_AnOptionalMemberContributesWearingAnUnknownSort(t *testing.T) {
	// absence is a value the annotation's own sort does not cover; the
	// field still reports, so a body reading it does not spell a missing slot
	typeNode := bundleParameterTypeOf(t, `function f(p: { lo?: number, hi: number }): number { return p.hi; }`)
	fields, ok := BundleTypeFieldsOf(nil, typeNode)
	if !ok {
		t.Fatalf("BundleTypeFieldsOf declined a type literal with an optional member")
	}
	if !sameNames(fields, []string{"lo", "hi"}) {
		t.Fatalf("fields = %v, want [lo hi]", fieldNames(fields))
	}
	if fields[0].Sort != BindingKindUnknown {
		t.Errorf("lo? sort = %q, want unknown — absence is not in the number sort", fields[0].Sort)
	}
	if fields[1].Sort != BindingKindNumber {
		t.Errorf("hi sort = %q, want number", fields[1].Sort)
	}
}

func TestFieldBundles_ATypeReferenceWithoutACheckerDeclines(t *testing.T) {
	// the name needs symbolAt to reach its declaration; with no checker
	// there is nothing to resolve through, and the bundle declines rather
	// than crashing — the nil-tolerance the lowering depends on
	typeNode := bundleParameterTypeOf(t, `function f(wrapper: InstanceWrapper): number { return wrapper.depth; }`)
	if _, ok := BundleTypeFieldsOf(nil, typeNode); ok {
		t.Errorf("a type reference resolved with no flow context — there is no checker to resolve the name")
	}
	if _, ok := BundleTypeFieldsOf(&FlowContext{}, typeNode); ok {
		t.Errorf("a type reference resolved with no program")
	}
}

func TestFieldBundles_AnAnnotationThatIsNeitherLiteralNorReferenceDeclines(t *testing.T) {
	for _, source := range []string{
		`function f(p: number): number { return p; }`,
		`function f(p: { lo: number } | { hi: number }): number { return 1; }`,
		`function f(p: number[]): number { return 1; }`,
	} {
		typeNode := bundleParameterTypeOf(t, source)
		if _, ok := BundleTypeFieldsOf(nil, typeNode); ok {
			t.Errorf("%q's annotation answered a field set — it spells no members", source)
		}
	}
	if _, ok := BundleTypeFieldsOf(nil, nil); ok {
		t.Errorf("a nil annotation answered a field set")
	}
}

/* ── the per-body use report ─────────────────────────────────────── */

// thisCensusOf is the census of a class's FIRST method body against that
// class's own fields, read through `this`.
func thisCensusOf(t *testing.T, source string) FieldCensus {
	t.Helper()
	statements := bundleParse(t, source)
	fields, ok := ClassFieldsOf(nil, statements[0])
	if !ok {
		t.Fatalf("ClassFieldsOf declined %q", source)
	}
	return FieldCensusOf(bundleMethodBodyOf(t, source), "this", BundleFieldsAs("this", fields))
}

// namedCensusOf is the census of a function body against a field set
// spelled by hand, read through a named receiver.
func namedCensusOf(t *testing.T, source string, receiverName string, names []string) FieldCensus {
	t.Helper()
	fields := make([]BundleField, len(names))
	for index, name := range names {
		fields[index] = BundleField{
			Name:      name,
			SlotName:  receiverName + "." + name,
			Sort:      BindingKindNumber,
			TypeofTag: TypeofTagNumber,
		}
	}
	return FieldCensusOf(bundleFunctionBodyOf(t, source), receiverName, fields)
}

func TestFieldBundles_ReadFieldsReportInDeclarationOrderNotMentionOrder(t *testing.T) {
	// the layout and the call sites build their slot vectors from this
	// order, so it must be the declaration's, never the body's
	census := thisCensusOf(t, `
		class Injector {
			container: number;
			depth: number;
			label: string;
			load(): number { return this.depth + this.container; }
		}
	`)
	if !sameNames(census.Reads, []string{"container", "depth"}) {
		t.Fatalf("reads = %v, want [container depth] in declaration order", fieldNames(census.Reads))
	}
	if len(census.Writes) != 0 {
		t.Errorf("writes = %v, want none", fieldNames(census.Writes))
	}
	if census.Escapes || census.Computed {
		t.Errorf("escapes = %v, computed = %v, want both false — every mention is a declared field read", census.Escapes, census.Computed)
	}
}

func TestFieldBundles_AReadCarriesTheFieldsOwnSlotNameAndSort(t *testing.T) {
	census := thisCensusOf(t, `
		class Injector {
			container: string;
			load(): number { return this.container.length; }
		}
	`)
	if len(census.Reads) != 1 {
		t.Fatalf("reads = %v, want one", fieldNames(census.Reads))
	}
	if census.Reads[0].SlotName != "this.container" {
		t.Errorf("slot name = %q, want this.container", census.Reads[0].SlotName)
	}
	if census.Reads[0].Sort != BindingKindString {
		t.Errorf("sort = %q, want string — the read carries the declaration's own sort", census.Reads[0].Sort)
	}
}

func TestFieldBundles_APlainAssignmentIsAWriteAndNotARead(t *testing.T) {
	census := thisCensusOf(t, `
		class Injector {
			depth: number;
			load(): void { this.depth = 4; }
		}
	`)
	if !sameNames(census.Writes, []string{"depth"}) {
		t.Fatalf("writes = %v, want [depth]", fieldNames(census.Writes))
	}
	if len(census.Reads) != 0 {
		t.Errorf("reads = %v, want none — an assignment target stores, it does not read", fieldNames(census.Reads))
	}
}

func TestFieldBundles_ACompoundWriteAndAnUpdateAreBothAWriteAndARead(t *testing.T) {
	for _, body := range []string{"this.depth += 1;", "this.depth++;", "++this.depth;", "this.depth--;"} {
		census := thisCensusOf(t, `
			class Injector {
				depth: number;
				load(): void { `+body+` }
			}
		`)
		if !sameNames(census.Writes, []string{"depth"}) {
			t.Errorf("%q writes = %v, want [depth]", body, fieldNames(census.Writes))
		}
		if !sameNames(census.Reads, []string{"depth"}) {
			t.Errorf("%q reads = %v, want [depth] — the old value is read before the new is stored", body, fieldNames(census.Reads))
		}
	}
}

func TestFieldBundles_ADeleteIsAWriteThatDoesNotRead(t *testing.T) {
	census := thisCensusOf(t, `
		class Injector {
			depth: number;
			load(): void { delete this.depth; }
		}
	`)
	if !sameNames(census.Writes, []string{"depth"}) {
		t.Fatalf("writes = %v, want [depth]", fieldNames(census.Writes))
	}
	if len(census.Reads) != 0 {
		t.Errorf("reads = %v, want none", fieldNames(census.Reads))
	}
}

func TestFieldBundles_AWriteOnlyFieldAppearsInWritesAloneAndAReadWriteInBoth(t *testing.T) {
	census := thisCensusOf(t, `
		class Injector {
			container: number;
			depth: number;
			label: number;
			load(): number { this.container = 1; this.depth += 2; return this.label; }
		}
	`)
	if !sameNames(census.Writes, []string{"container", "depth"}) {
		t.Fatalf("writes = %v, want [container depth]", fieldNames(census.Writes))
	}
	if !sameNames(census.Reads, []string{"depth", "label"}) {
		t.Fatalf("reads = %v, want [depth label]", fieldNames(census.Reads))
	}
}

func TestFieldBundles_AMethodCallOnAFieldCountsTheFieldReadAndDoesNotEscape(t *testing.T) {
	// `this.injector.load(…)` is a read of injector followed by a call —
	// the method resolves through its own summary, not through a slot
	census := thisCensusOf(t, `
		class Module {
			injector: number;
			depth: number;
			load(): number { return this.injector.load(this.depth); }
		}
	`)
	if !sameNames(census.Reads, []string{"injector", "depth"}) {
		t.Fatalf("reads = %v, want [injector depth]", fieldNames(census.Reads))
	}
	if census.Escapes {
		t.Errorf("escapes = true, want false — a method-call receiver is a field read")
	}
}

func TestFieldBundles_ADirectMethodCallOnTheReceiverIsNotAnEscape(t *testing.T) {
	// `this.helper()` names a method, which is not in the field set; the
	// call resolves through ContractBySymbol, so the receiver has not left
	// the lowering's sight
	census := thisCensusOf(t, `
		class Module {
			depth: number;
			load(): number { return this.helper() + this.depth; }
			helper(): number { return 1; }
		}
	`)
	if !sameNames(census.Reads, []string{"depth"}) {
		t.Fatalf("reads = %v, want [depth]", fieldNames(census.Reads))
	}
	if census.Escapes {
		t.Errorf("escapes = true, want false — a method name is a call, not a missing slot")
	}
}

func TestFieldBundles_AComputedMemberOnTheReceiverIsReportedComputed(t *testing.T) {
	census := thisCensusOf(t, `
		class Bag {
			depth: number;
			load(k: string): number { return this[k]; }
		}
	`)
	if !census.Computed {
		t.Errorf("computed = false, want true — this[k] names no field")
	}
	if census.Escapes {
		t.Errorf("escapes = true, want false — the declaration still bounds which slots could be meant")
	}
}

func TestFieldBundles_AComputedWriteOnTheReceiverIsAlsoComputed(t *testing.T) {
	census := thisCensusOf(t, `
		class Bag {
			depth: number;
			load(k: string): void { this[k] = 1; }
		}
	`)
	if !census.Computed {
		t.Errorf("computed = false, want true — this[k] = 1 moves an unnamed slot")
	}
	if len(census.Writes) != 0 {
		t.Errorf("writes = %v, want none — nothing names WHICH field moved", fieldNames(census.Writes))
	}
}

func TestFieldBundles_ABareMentionOfTheReceiverEscapes(t *testing.T) {
	for _, body := range []string{
		"return f(this);",
		"const alias = this; return alias.depth;",
		"xs.push(this); return this.depth;",
	} {
		census := thisCensusOf(t, `
			class Module {
				depth: number;
				load(): number { `+body+` }
			}
		`)
		if !census.Escapes {
			t.Errorf("%q escapes = false, want true — the receiver went where the lowering cannot see", body)
		}
	}
}

func TestFieldBundles_AnUndeclaredMemberDefersToItsConsumer(t *testing.T) {
	// no slot holds `missing` — the spelling a GET ACCESSOR is read by.
	// The census reports the name (AccessorReads) instead of escaping;
	// Believable refuses every consumer without the accessor fold, and
	// the fold itself (thisBundleOf) turns an unresolvable name back
	// into the escape — the same refusal, ruled at the right seam.
	census := thisCensusOf(t, `
		class Module {
			depth: number;
			load(): number { return this.missing; }
		}
	`)
	if census.Escapes {
		t.Errorf("escapes = true, want the deferral — the ruling is the consumer's")
	}
	if !contains(census.AccessorReads, "missing") {
		t.Errorf("AccessorReads = %v, want it to name missing", census.AccessorReads)
	}
	if census.Believable() {
		t.Errorf("a deferred accessor read answered believable")
	}
	if len(census.Reads) != 0 {
		t.Errorf("reads = %v, want none — an undeclared member is not a field read", fieldNames(census.Reads))
	}
}

func TestFieldBundles_AnOptionalChainOnTheReceiverEscapes(t *testing.T) {
	census := thisCensusOf(t, `
		class Module {
			depth: number;
			load(): number { return this?.depth; }
		}
	`)
	if !census.Escapes {
		t.Errorf("escapes = false, want true — an optional step admits an absent receiver, which no slot spells")
	}
}

func TestFieldBundles_AReadOnlyArrowCaptureIsARead(t *testing.T) {
	// an arrow keeps the enclosing `this` and runs at a time this scan
	// cannot place — but a READ moves nothing, so a capture whose every
	// receiver mention is a declared-field read is admitted as reads
	census := thisCensusOf(t, `
		class Module {
			depth: number;
			load(): number { return xs.map(x => x + this.depth).length; }
		}
	`)
	if census.Escapes {
		t.Errorf("escapes = true, want false — the arrow only reads depth")
	}
	if !sameNames(census.Reads, []string{"depth"}) {
		t.Errorf("reads = %v, want [depth]", fieldNames(census.Reads))
	}
}

func TestFieldBundles_AWritingArrowCaptureStillEscapes(t *testing.T) {
	// a capture that WRITES a field may store at any later time — that
	// is exactly what the escape guards
	census := thisCensusOf(t, `
		class Module {
			depth: number;
			load(): number { xs.forEach(x => { this.depth = x; }); return 1; }
		}
	`)
	if !census.Escapes {
		t.Errorf("escapes = false, want true — the arrow stores into depth")
	}
}

func TestFieldBundles_ANestedFunctionRebindsThisSoItsOwnThisIsNotThisBundle(t *testing.T) {
	// a function expression's `this` denotes some other object entirely
	census := thisCensusOf(t, `
		class Module {
			depth: number;
			load(): number { const g = function () { return this.depth; }; return this.depth; }
		}
	`)
	if census.Escapes {
		t.Errorf("escapes = true, want false — a function expression rebinds this")
	}
	if !sameNames(census.Reads, []string{"depth"}) {
		t.Fatalf("reads = %v, want [depth] — the method's own read still counts", fieldNames(census.Reads))
	}
}

/* ── a named receiver ────────────────────────────────────────────── */

func TestFieldBundles_ANamedReceiverReadsWritesAndEscapesTheSameWayThisDoes(t *testing.T) {
	census := namedCensusOf(t,
		`function f(wrapper: InstanceWrapper): number { wrapper.instance = 1; return wrapper.metatype; }`,
		"wrapper", []string{"metatype", "instance"})
	if !sameNames(census.Reads, []string{"metatype"}) {
		t.Fatalf("reads = %v, want [metatype]", fieldNames(census.Reads))
	}
	if !sameNames(census.Writes, []string{"instance"}) {
		t.Fatalf("writes = %v, want [instance]", fieldNames(census.Writes))
	}
	if census.Escapes || census.Computed {
		t.Errorf("escapes = %v, computed = %v, want both false", census.Escapes, census.Computed)
	}
}

func TestFieldBundles_ANamedReceiverInANestedFunctionFollowsTheReadOnlyRule(t *testing.T) {
	// unlike `this`, a closed-over NAME is still the same object inside
	// any nested form — and the same admission applies: a closure whose
	// every mention is a declared-field READ contributes reads, while
	// one that writes keeps the escape
	census := namedCensusOf(t,
		`function f(wrapper: InstanceWrapper): number { const g = function () { return wrapper.metatype; }; return 1; }`,
		"wrapper", []string{"metatype"})
	if census.Escapes {
		t.Errorf("escapes = true, want false — the closure only reads metatype")
	}
	if !sameNames(census.Reads, []string{"metatype"}) {
		t.Errorf("reads = %v, want [metatype]", fieldNames(census.Reads))
	}
	writing := namedCensusOf(t,
		`function f(wrapper: InstanceWrapper): number { const g = function () { wrapper.metatype = 1; }; return 1; }`,
		"wrapper", []string{"metatype"})
	if !writing.Escapes {
		t.Errorf("a writing closure did not escape: %+v", writing)
	}
}

func TestFieldBundles_AThisCensusIgnoresANameThatIsNotTheReceiver(t *testing.T) {
	// a same-named field on some OTHER object is not this bundle's read
	census := namedCensusOf(t,
		`function f(wrapper: InstanceWrapper, other: InstanceWrapper): number { return other.metatype + wrapper.metatype; }`,
		"wrapper", []string{"metatype"})
	if !sameNames(census.Reads, []string{"metatype"}) {
		t.Fatalf("reads = %v, want [metatype] once", fieldNames(census.Reads))
	}
	if census.Escapes {
		t.Errorf("escapes = true, want false — `other` is a different object entirely")
	}
}

func TestFieldBundles_APropertyNamedLikeTheReceiverIsAStepNotAMention(t *testing.T) {
	// the `wrapper` in `holder.wrapper` is a step name, not the object
	census := namedCensusOf(t,
		`function f(wrapper: InstanceWrapper): number { return holder.wrapper + wrapper.metatype; }`,
		"wrapper", []string{"metatype"})
	if census.Escapes {
		t.Errorf("escapes = true, want false — holder.wrapper names a step, not this receiver")
	}
	if !sameNames(census.Reads, []string{"metatype"}) {
		t.Fatalf("reads = %v, want [metatype]", fieldNames(census.Reads))
	}
}

func TestFieldBundles_AnEmptyBodyOrNoFieldsReportsNothing(t *testing.T) {
	census := namedCensusOf(t, `function f(wrapper: InstanceWrapper): number { return 1; }`, "wrapper", []string{"metatype"})
	if len(census.Reads) != 0 || len(census.Writes) != 0 || census.Escapes || census.Computed {
		t.Errorf("census = %+v, want an empty report", census)
	}
	if empty := FieldCensusOf(nil, "this", nil); empty.Escapes || empty.Computed || len(empty.Reads) != 0 {
		t.Errorf("a nil body reported %+v, want an empty report", empty)
	}
}
