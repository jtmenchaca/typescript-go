// The widened summary route's NAMED-TYPE parameters: SummaryParameterEntriesIn
// and recordParamMembersIn resolve a parameter annotated with an
// interface, a type alias, or a heritage chain through the checker,
// the same expansion rules kernel_summary_direct_test.go's inline
// type-literal cases pin. See that file's header for the sibling map;
// this file owns namedTypeCtx/namedTypeFunction, the checker-backed
// program helpers every named-type case here builds on.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
)

// namedTypeCtx builds a checker-backed FlowContext over one source, so a
// parameter annotated with a NAMED type has something to resolve
// through. The memos both clear first: the record-member memo is keyed
// on parameter nodes and the outcome store on declaration nodes, and
// each case parses its own fresh program.
func namedTypeCtx(t *testing.T, source string) (*FlowContext, *program.CheckerProgram) {
	t.Helper()
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, source)
	return &FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}, p
}

// namedTypeFunction is the top-level function declaration named `text`
// in a checker-backed program.
func namedTypeFunction(t *testing.T, p *program.CheckerProgram, text string) *ast.Node {
	t.Helper()
	return entryEnvFunctionNamed(t, p, text)
}

func TestSummaryParameterEntriesIn_AnInterfaceTypedParameterExpandsOneEntryPerMember(t *testing.T) {
	ctx, p := namedTypeCtx(t,
		"interface Bounds { lo: number; hi: string; on: boolean }\n"+
			"function f(p: Bounds) { return p.lo; }\n")
	declaration := namedTypeFunction(t, p, "f")
	entries, ok := SummaryParameterEntriesIn(ctx, declaration.Parameters()[0])
	if !ok {
		t.Fatalf("a scalar-membered interface declined — its members are the literal case's members")
	}
	if len(entries) != 3 {
		t.Fatalf("len(entries) = %d, want 3 — one per member", len(entries))
	}
	wantName := []string{"p.lo", "p.hi", "p.on"}
	wantSort := []BindingKind{BindingKindNumber, BindingKindString, BindingKindNumber}
	wantTypeof := []TypeofTag{TypeofTagNumber, TypeofTagString, TypeofTagBoolean}
	for i, entry := range entries {
		if entry.Name != wantName[i] {
			t.Errorf("entry %d name = %q, want %q", i, entry.Name, wantName[i])
		}
		if entry.Sort != wantSort[i] {
			t.Errorf("entry %d sort = %q, want %q", i, entry.Sort, wantSort[i])
		}
		if entry.TypeofTag != wantTypeof[i] {
			t.Errorf("entry %d typeof = %q, want %q", i, entry.TypeofTag, wantTypeof[i])
		}
	}
	// the resolution is remembered under the parameter node, so the
	// ctx-less spelling the call sites take answers the SAME list — that
	// agreement is what keeps the layout and the sites from building
	// different entry vectors
	members, expanded := recordParamMembersOf(declaration.Parameters()[0])
	if !expanded || len(members) != 3 {
		t.Fatalf("the ctx-less reading answered %d members (expanded %v), want the same 3", len(members), expanded)
	}
}

func TestSummaryParameterEntriesIn_ATypeAliasOfALiteralExpands(t *testing.T) {
	ctx, p := namedTypeCtx(t,
		"type Pair = { lo: number, hi: number };\n"+
			"function f(p: Pair) { return p.lo + p.hi; }\n")
	declaration := namedTypeFunction(t, p, "f")
	entries, ok := SummaryParameterEntriesIn(ctx, declaration.Parameters()[0])
	if !ok || len(entries) != 2 {
		t.Fatalf("entries = %v (ok %v), want two — one per alias member", entries, ok)
	}
	if entries[0].Name != "p.lo" || entries[1].Name != "p.hi" {
		t.Errorf("entries = %q/%q, want p.lo/p.hi", entries[0].Name, entries[1].Name)
	}
}

func TestSummaryParameterEntriesIn_TheNamedShapesThatStayWholeName(t *testing.T) {
	// each of these resolves to a declaration whose members are not
	// promised to every entry, so the parameter keeps its single slot.
	// An interface's method and optional member and richer member all
	// EXPAND through a named type exactly as the inline-literal case
	// does (recordParamMembersIn reads a named type by the same member
	// rules) — pinned separately below, not here. A reference CARRYING
	// TYPE ARGUMENTS (`p: Box<number>`) is no longer in this family
	// either: with the arguments applied there is exactly one
	// instantiation, and its members read through the checker's own
	// answer (instantiatedReferenceMembersOf,
	// ir_summary_instantiated_members_test.go's pins).
	sources := map[string]string{
		"a class": "class Point { lo: number = 0; hi: number = 0 }\n" +
			"function f(p: Point) { return 1; }\n",
		"an alias of a union": "type Either = { lo: number } | { hi: number };\n" +
			"function f(p: Either) { return 1; }\n",
		"an empty interface": "interface Bounds { }\n" +
			"function f(p: Bounds) { return 1; }\n",
		"an unresolvable name": "function f(p: Nowhere) { return 1; }\n",
	}
	for name, source := range sources {
		ctx, p := namedTypeCtx(t, source)
		declaration := namedTypeFunction(t, p, "f")
		if _, expanded := recordParamMembersIn(ctx, declaration.Parameters()[0]); expanded {
			t.Errorf("%s expanded — its members are not promised to every entry", name)
		}
		entries, ok := SummaryParameterEntriesIn(ctx, declaration.Parameters()[0])
		if !ok || len(entries) != 1 || entries[0].Name != "p" {
			t.Errorf("%s: entries = %v (ok %v), want the single whole-name entry", name, entries, ok)
		}
	}
}

// TestSummaryParameterEntriesIn_ANamedInterfacesMethodAndOptionalAndRicherMembersExpand
// pins the landed widening through a NAMED type: an interface's method
// is skipped (contributes nothing) while its scalar sibling still
// expands; an optional member contributes wearing MayBeAbsent — the same
// rules scalarMemberListOfIn states for the inline-literal case, read
// off a resolved interface declaration by the same member reader. A
// richer (array-typed) member's own expansion is pinned separately below
// (TestSummaryParameterEntriesIn_ANamedInterfacesRicherMemberExpandsToItsLenElemPair)
// since it no longer fits this test's single-"p.lo"-entry shape.
func TestSummaryParameterEntriesIn_ANamedInterfacesMethodAndOptionalAndRicherMembersExpand(t *testing.T) {
	cases := map[string]struct {
		source string
		sort   BindingKind
	}{
		"a method beside a scalar member": {
			source: "interface Bounds { lo: number; go(): number }\n" +
				"function f(p: Bounds) { return 1; }\n",
			sort: BindingKindNumber,
		},
		"an optional member": {
			source: "interface Bounds { lo?: number }\n" +
				"function f(p: Bounds) { return 1; }\n",
			sort: BindingKindNumber,
		},
	}
	for name, given := range cases {
		ctx, p := namedTypeCtx(t, given.source)
		declaration := namedTypeFunction(t, p, "f")
		entries, ok := SummaryParameterEntriesIn(ctx, declaration.Parameters()[0])
		if !ok || len(entries) != 1 || entries[0].Name != "p.lo" || entries[0].Sort != given.sort {
			t.Errorf("%s: entries = %v (ok %v), want [{p.lo %s}]", name, entries, ok, given.sort)
		}
	}
	// the optional case also carries MayBeAbsent on the resolved member
	ctx, p := namedTypeCtx(t, "interface Bounds { lo?: number }\n"+
		"function f(p: Bounds) { return 1; }\n")
	declaration := namedTypeFunction(t, p, "f")
	members, expanded := recordParamMembersIn(ctx, declaration.Parameters()[0])
	if !expanded || len(members) != 1 || !members[0].MayBeAbsent {
		t.Errorf("members = %v (expanded %v), want the one member wearing MayBeAbsent", members, expanded)
	}
}

// TestSummaryParameterEntriesIn_ANamedInterfacesRicherMemberExpandsToItsLenElemPair
// pins the LANDED widening (nestedMemberLeavesOf's array arm, this wave)
// through a NAMED type: an interface's array-typed member (`lo:
// number[]`) expands to its own "len"/"elem" pair, the same
// arrayElementPairMembers expansion the inline-literal case takes
// (TestSummaryParameterEntries_ARicherMemberExpandsToItsLenElemPair).
func TestSummaryParameterEntriesIn_ANamedInterfacesRicherMemberExpandsToItsLenElemPair(t *testing.T) {
	ctx, p := namedTypeCtx(t, "interface Bounds { lo: number[] }\n"+
		"function f(p: Bounds) { return 1; }\n")
	declaration := namedTypeFunction(t, p, "f")
	entries, ok := SummaryParameterEntriesIn(ctx, declaration.Parameters()[0])
	if !ok || len(entries) != 2 {
		t.Fatalf("entries = %v (ok %v), want [{p.lo.len number} {p.lo.elem number}]", entries, ok)
	}
	byName := map[string]bodySlot{}
	for _, entry := range entries {
		byName[entry.Name] = entry
	}
	length, hasLen := byName["p.lo.len"]
	elem, hasElem := byName["p.lo.elem"]
	if !hasLen || !hasElem {
		t.Fatalf("entry names = %+v, want p.lo.len and p.lo.elem", byName)
	}
	if length.Sort != BindingKindNumber {
		t.Errorf("p.lo.len sort = %v, want number", length.Sort)
	}
	if elem.Sort != BindingKindNumber {
		t.Errorf("p.lo.elem sort = %v, want number — the element type is a scalar number", elem.Sort)
	}
}

func TestSummaryParameterEntriesIn_AnInterfaceWithExtendsExpandsInheritedMembers(t *testing.T) {
	// the heritage walk follows the extends chain: Bounds carries its
	// own hi AND Base's lo, so the parameter expands one entry per
	// inherited-plus-own member
	ctx, p := namedTypeCtx(t,
		"interface Base { lo: number }\n"+
			"interface Bounds extends Base { hi: number }\n"+
			"function f(p: Bounds) { return 1; }\n")
	declaration := namedTypeFunction(t, p, "f")
	members, expanded := recordParamMembersIn(ctx, declaration.Parameters()[0])
	if !expanded {
		t.Fatalf("an interface with extends kept its single slot — the heritage walk did not expand it")
	}
	keys := map[string]bool{}
	for _, member := range members {
		keys[member.Key] = true
	}
	if !keys["lo"] || !keys["hi"] || len(members) != 2 {
		t.Errorf("members = %v, want exactly the inherited lo and the own hi", members)
	}
}

func TestKernelSummaryDirect_AnInterfaceTypedParametersFieldsBuildTheLeafEntries(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ctx, p := namedTypeCtx(t,
		"interface Pair { lo: number; hi: number }\n"+
			"function f(p: Pair) { return p.lo + p.hi; }\n")
	declaration := namedTypeFunction(t, p, "f")
	summary, lowered := RelowerSummaryBody(ctx, declaration)
	if !lowered {
		t.Fatalf("a body reading an interface-typed parameter's members declined")
	}
	if summary.ParamCount != 2 {
		t.Errorf("ParamCount = %d, want 2 — one entry per interface member", summary.ParamCount)
	}
	contract := &FunctionContract{Declaration: declaration}
	argument := recordObject(t, map[string]float64{"lo": 2, "hi": 5}, []string{"lo", "hi"})
	answer, ok := KernelSummaryDirect(ctx, []abstractdomain.AbstractValue{argument}, contract)
	if !ok {
		t.Fatalf("the apply declined — the object's fields spell the leaf entries")
	}
	state, stateOk := StateOfKnown(answer)
	if !stateOk || state.Top {
		t.Fatalf("the answer did not spell as a scalar state: %+v", answer)
	}
	// f({lo:2, hi:5}) = 7; the answer must ADMIT it
	if !kernel.Member(state.Set, []float64{7}) {
		t.Errorf("the summary of f({lo:2,hi:5}) excludes the true value 7: %+v", state.Set)
	}
}

// TestSummaryParameterEntriesIn_AnInterfaceWithANestedMemberExpandsTheTwoStepLeaf
// pins the gap this closed: an interface's OWN members now read through
// the SAME widened reader (interfaceOwnMembersWithCheckerIn) a type
// literal or alias already gets, so a nested member declared directly on
// an interface expands to its two-step leaf exactly as
// `p: { lo: number, inner: { deep: number } }` already does.
func TestSummaryParameterEntriesIn_AnInterfaceWithANestedMemberExpandsTheTwoStepLeaf(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ctx, p := namedTypeCtx(t,
		"interface Box { lo: number; inner: { deep: number } }\n"+
			"function f(p: Box) { return p.inner.deep + p.lo; }\n")
	declaration := namedTypeFunction(t, p, "f")
	entries, entriesOk := SummaryParameterEntriesIn(ctx, declaration.Parameters()[0])
	if !entriesOk || len(entries) != 2 {
		t.Fatalf("entries = %v (ok %v), want two — p.lo and p.inner.deep", entries, entriesOk)
	}
	wantName := []string{"p.lo", "p.inner.deep"}
	for i, entry := range entries {
		if entry.Name != wantName[i] {
			t.Errorf("entry %d name = %q, want %q", i, entry.Name, wantName[i])
		}
		if entry.Sort != BindingKindNumber {
			t.Errorf("entry %d sort = %q, want number", i, entry.Sort)
		}
	}
	// a body reading the two-step member off the interface-typed parameter
	// summarizes exactly, the same end-to-end check the type-literal
	// nested-family test makes
	contract := &FunctionContract{Declaration: declaration}
	inner := abstractdomain.KnownObject(
		[]abstractdomain.ObjectKey{{Name: "deep", Value: exactNumber(t, 2)}}, nil, true, abstractdomain.TrustProved, false)
	argument := abstractdomain.KnownObject(
		[]abstractdomain.ObjectKey{
			{Name: "lo", Value: exactNumber(t, 1)},
			{Name: "inner", Value: inner},
		}, nil, true, abstractdomain.TrustProved, false)
	answer, ok := KernelSummaryDirect(ctx, []abstractdomain.AbstractValue{argument}, contract)
	if !ok {
		t.Fatalf("a body reading an interface's two-step nested member declined")
	}
	state, stateOk := StateOfKnown(answer)
	if !stateOk || state.Top {
		t.Fatalf("the answer did not spell as a scalar state: %+v", answer)
	}
	// f({lo:1, inner:{deep:2}}) = 2+1 = 3; the answer must ADMIT it EXACTLY
	if !kernel.Member(state.Set, []float64{3}) {
		t.Errorf("the summary excludes the true value 3: %+v", state.Set)
	}
}

// TestSummaryParameterEntriesIn_AnInterfaceWithAStableSymbolMemberExpandsTheSymLeaf
// pins the same gap for the OTHER widening a checker unlocks: a computed
// member name resolving to a stable module-level symbol const
// (scalarMemberListWithCheckerIn's #sym: reading) now contributes its
// leaf when the member is declared directly on an interface, not only
// inside a type literal or alias.
func TestSummaryParameterEntriesIn_AnInterfaceWithAStableSymbolMemberExpandsTheSymLeaf(t *testing.T) {
	ctx, p := namedTypeCtx(t,
		"const S = Symbol();\n"+
			"interface Box { lo: number, [S]: number }\n"+
			"function f(p: Box) { return p.lo; }\n")
	declaration := namedTypeFunction(t, p, "f")
	entries, ok := SummaryParameterEntriesIn(ctx, declaration.Parameters()[0])
	if !ok || len(entries) != 2 {
		t.Fatalf("entries = %v (ok %v), want two — p.lo and the #sym: leaf", entries, ok)
	}
	wantName := []string{"p.lo", "p.#sym:S"}
	for i, entry := range entries {
		if entry.Name != wantName[i] {
			t.Errorf("entry %d name = %q, want %q", i, entry.Name, wantName[i])
		}
		if entry.Sort != BindingKindNumber {
			t.Errorf("entry %d sort = %q, want number", i, entry.Sort)
		}
	}
}
