// Pins for nestedMemberLeavesOf's ARRAY-VALUED member arm
// (ir_summary_record_member_reading.go): a record parameter member
// whose own annotation is an array type (`ticks: number[]`,
// `sourceLinks: number[]`) now expands to the ".len"/".elem" pair under
// the member's own holder, the same vocabulary an array-typed
// PARAMETER's element already wears — Sankey.tsx's relax loops
// (`node.sourceLinks.length`) and axisSelectors.ts's `p.ticks.length`
// callers are this shape.

package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
)

// TestNestedMemberLeavesOf_AScalarArrayMemberExpandsToLenElemPair pins
// the plain scalar-elemented case: `ticks: number[]` on a record
// parameter answers "p.ticks.len" (number) and "p.ticks.elem" (number),
// both wearing ArrayPair, alongside the untouched sibling scalar member.
func TestNestedMemberLeavesOf_AScalarArrayMemberExpandsToLenElemPair(t *testing.T) {
	declaration := summaryDeclarationOf(t,
		"function f(p: { ticks: number[]; kind: string }) { return p.kind; }")
	fn := declaration.AsFunctionDeclaration()
	members, expanded := recordParamMembersOf(fn.Parameters.Nodes[0])
	if !expanded {
		t.Fatalf("p's members declined to expand")
	}
	byPath := map[string]recordParamMember{}
	for _, m := range members {
		byPath[m.SlotName] = m
	}
	lenMember, hasLen := byPath["p.ticks.len"]
	if !hasLen {
		t.Fatalf("members = %+v, want a p.ticks.len leaf", members)
	}
	if lenMember.Sort != BindingKindNumber || !lenMember.ArrayPair {
		t.Errorf("p.ticks.len = %+v, want number-sorted and ArrayPair", lenMember)
	}
	elemMember, hasElem := byPath["p.ticks.elem"]
	if !hasElem {
		t.Fatalf("members = %+v, want a p.ticks.elem leaf", members)
	}
	if elemMember.Sort != BindingKindNumber || !elemMember.ArrayPair {
		t.Errorf("p.ticks.elem = %+v, want number-sorted and ArrayPair", elemMember)
	}
	kindMember, hasKind := byPath["p.kind"]
	if !hasKind || kindMember.Sort != BindingKindString || kindMember.ArrayPair {
		t.Errorf("p.kind = %+v (found %v), want string-sorted and NOT ArrayPair", kindMember, hasKind)
	}
}

// TestNestedMemberLeavesOf_ARecordArrayMemberExpandsElementMembers pins
// the RECORD-elemented case — Sankey.tsx's SankeyNode: `sourceLinks:
// number[]` is the scalar shape a real SankeyNode carries (link INDEX
// numbers, per Sankey.tsx's own LinkDataItemDy indexing), pinned here as
// `links: { weight: number }[]` to also cover a record-elemented member
// array expanding its own nested members under "p.links.elem.<member>".
func TestNestedMemberLeavesOf_ARecordArrayMemberExpandsElementMembers(t *testing.T) {
	declaration := summaryDeclarationOf(t,
		"function f(p: { links: { weight: number }[] }) { return p.links; }")
	fn := declaration.AsFunctionDeclaration()
	members, expanded := recordParamMembersOf(fn.Parameters.Nodes[0])
	if !expanded {
		t.Fatalf("p's members declined to expand")
	}
	byPath := map[string]recordParamMember{}
	for _, m := range members {
		byPath[m.SlotName] = m
	}
	lenMember, hasLen := byPath["p.links.len"]
	if !hasLen || !lenMember.ArrayPair {
		t.Fatalf("members = %+v, want a p.links.len leaf wearing ArrayPair", members)
	}
	weightMember, hasWeight := byPath["p.links.elem.weight"]
	if !hasWeight {
		t.Fatalf("members = %+v, want a p.links.elem.weight leaf", members)
	}
	// weightMember is an ordinary record member NESTED inside the elem
	// slot's own record shape, not the "len"/"elem" pair marker itself —
	// ArrayPair marks only the pair's own two slots (arrayElementPairMembers'
	// own doc), so an ordinary member one level inside "elem" wears it
	// false, same as any other record member would
	if weightMember.Sort != BindingKindNumber || weightMember.ArrayPair {
		t.Errorf("p.links.elem.weight = %+v, want number-sorted and NOT ArrayPair (an ordinary member nested inside the pair)", weightMember)
	}
}

// TestKernelSummaryDirect_ARecordMemberArrayLengthReadLowers pins the
// Sankey.tsx relax-loop shape directly: `node.sourceLinks.length` inside
// a `for`-loop condition over a record-typed local carrying an
// array-valued member. Before this fix, `sourceLinks` contributed no
// nested member at all (nestedMemberLeavesOf declined every array-typed
// annotation), so `.length` read through the interior-path route or the
// whole-parameter refusal; after, it answers off the declared
// "sourceLinks.len" leaf directly.
func TestKernelSummaryDirect_ARecordMemberArrayLengthReadLowers(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	declaration := summaryDeclarationOf(t,
		"function f(node: { sourceLinks: number[]; y: number }): boolean { return node.sourceLinks.length > 0; }")
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	_, ok := RelowerSummaryBody(ctx, declaration)
	outcome, construct, _ := SummaryOutcomeOf(nil, declaration)
	t.Logf("ok=%v outcome=%q construct=%q", ok, outcome, construct)
	if !ok {
		t.Fatalf("f's body declined: outcome=%q construct=%q — a record-member array length read must lower", outcome, construct)
	}
}
