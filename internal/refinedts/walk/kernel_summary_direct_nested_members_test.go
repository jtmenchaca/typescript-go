// The widened summary route's NESTED MEMBER FAMILIES (Row 1): a
// parameter member whose own annotation is itself a type literal, an
// optional parent composing MayBeAbsent down to its child, and the
// cycle guard a self-referential alias pair needs to terminate. See
// kernel_summary_direct_test.go's header for the sibling map.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
)

// TestKernelSummaryDirect_ABodyReadingATwoStepNestedMemberSummarizesAndAnswersExactly
// pins the landed NESTED FAMILY widening end to end: a body reading
// `p.inner.deep + p.lo` over `p: { lo: number, inner: { deep: number } }`
// summarizes, and the answer is exact over a fully-known argument.
func TestKernelSummaryDirect_ABodyReadingATwoStepNestedMemberSummarizesAndAnswersExactly(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	declaration := summaryDeclarationOf(t,
		"function f(p: { lo: number, inner: { deep: number } }) { return p.inner.deep + p.lo; }")
	entries, entriesOk := SummaryParameterEntries(declaration.Parameters()[0])
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
	contract := &FunctionContract{Declaration: declaration}
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	inner := abstractdomain.KnownObject(
		[]abstractdomain.ObjectKey{{Name: "deep", Value: exactNumber(t, 2)}}, nil, true, abstractdomain.TrustProved, false)
	argument := abstractdomain.KnownObject(
		[]abstractdomain.ObjectKey{
			{Name: "lo", Value: exactNumber(t, 1)},
			{Name: "inner", Value: inner},
		}, nil, true, abstractdomain.TrustProved, false)
	answer, ok := KernelSummaryDirect(ctx, []abstractdomain.AbstractValue{argument}, contract)
	if !ok {
		t.Fatalf("a body reading a two-step nested member declined")
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

// TestSummaryParameterEntries_AnOptionalParentMakesTheNestedChildEntryAbsentAware
// pins the absence-composition rule: an optional `inner?:` makes its
// nested child possibly absent even though the child's OWN annotation
// (`deep: number`) is required — the weaker promise composes down the
// path, and the child's entry carries MayBeAbsent for it.
func TestSummaryParameterEntries_AnOptionalParentMakesTheNestedChildEntryAbsentAware(t *testing.T) {
	declaration := summaryDeclarationOf(t, "function f(p: { inner?: { deep: number } }) { return 1; }")
	members, expanded := recordParamMembersOf(declaration.Parameters()[0])
	if !expanded || len(members) != 1 {
		t.Fatalf("members = %v (expanded %v), want the one nested child leaf", members, expanded)
	}
	// Key is the member's OWN name — the last Path segment; Path is the
	// full identity from the holder down
	if members[0].Key != "deep" {
		t.Errorf("members[0].Key = %q, want %q", members[0].Key, "deep")
	}
	wantPath := []string{"inner", "deep"}
	if len(members[0].Path) != len(wantPath) || members[0].Path[0] != wantPath[0] || members[0].Path[1] != wantPath[1] {
		t.Errorf("members[0].Path = %v, want %v", members[0].Path, wantPath)
	}
	if !members[0].MayBeAbsent {
		t.Errorf("members[0].MayBeAbsent = false, want true — the optional parent composes down to the child")
	}
}

// TestSummaryParameterEntries_ACyclicAliasPairTerminatesWithoutHanging
// pins the cycle guard nestedMemberLeavesOf's type-reference recursion
// carries: `type A = { b: B }; type B = { a: A }` given straight as a
// parameter's own annotation must not hang the reader. The parameter
// itself is `p: A`, so `b`'s own nested family resolves through B, which
// resolves back to A — the declaration already on the walk's path — and
// the revisit answers false, falling back to `b`'s single unknown-sorted
// leaf rather than recursing forever; `a`'s own scalar sibling `lo`
// still expands. This exercises the ALIAS declaration's own visiting
// path (scalarMemberListWithCheckerIn's recursion off a type-alias
// literal); an INTERFACE pair's own members resolve through
// interfaceOwnMembersWithCheckerIn, threaded with the SAME guard from
// declaredTypeMembersOf, and reach the identical recursion — see
// TestSummaryParameterEntriesIn_AnInterfaceWithANestedMemberExpandsTheTwoStepLeaf.
func TestSummaryParameterEntries_ACyclicAliasPairTerminatesWithoutHanging(t *testing.T) {
	ctx, p := namedTypeCtx(t,
		"type B = { a: A };\n"+
			"type A = { lo: number, b: B };\n"+
			"function f(p: A) { return p.lo; }\n")
	declaration := namedTypeFunction(t, p, "f")
	entries, ok := SummaryParameterEntriesIn(ctx, declaration.Parameters()[0])
	if !ok {
		t.Fatalf("the cyclic-alias parameter declined outright — want at least p.lo served")
	}
	// termination is the fact this test exists to pin: b's own family
	// stops at ONE leaf (its cyclic member falls back to unknown-sorted,
	// never expanded into a's own lo again), and lo itself still expands
	wantName := []string{"p.lo", "p.b.a"}
	if len(entries) != 2 {
		t.Fatalf("entries = %v, want two — p.lo and b's single cyclic-fallback leaf p.b.a", entries)
	}
	for i, entry := range entries {
		if entry.Name != wantName[i] {
			t.Errorf("entry %d name = %q, want %q", i, entry.Name, wantName[i])
		}
	}
	if entries[1].Sort != BindingKindUnknown {
		t.Errorf("entries[1].Sort = %q, want unknown — the cyclic member falls back rather than recursing forever", entries[1].Sort)
	}
}

// TestKernelSummaryDirect_AScalarAtTheInteriorStepDeclinesTheCall pins
// the interior three-way rule: an argument whose "inner" field is a
// definite SCALAR (never an object) can never carry "inner.deep", so
// the call declines rather than TOPping past a value the caller's own
// argument rules out.
func TestKernelSummaryDirect_AScalarAtTheInteriorStepDeclinesTheCall(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	declaration := summaryDeclarationOf(t,
		"function f(p: { inner: { deep: number } }) { return p.inner.deep; }")
	contract := &FunctionContract{Declaration: declaration}
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	// p's own field "inner" is a definite number, not an object — the
	// interior step can never carry "deep"
	argument := abstractdomain.KnownObject(
		[]abstractdomain.ObjectKey{{Name: "inner", Value: exactNumber(t, 5)}}, nil, true, abstractdomain.TrustProved, false)
	if _, ok := KernelSummaryDirect(ctx, []abstractdomain.AbstractValue{argument}, contract); ok {
		t.Errorf("a scalar at the interior step summarized — it can never carry the nested member")
	}
}
