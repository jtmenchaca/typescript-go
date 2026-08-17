// Pins for ArrayParameterOf / arrayParameterElementSort — the
// array-typed PARAMETER recognizer and the sort it reads off the
// declared element type.
package walk

import (
	"testing"
)

// TestCenterYShapedRecordParameter_PremiseCheck pins the AGENT-BRIEF's
// flagged premise: Sankey's `centerY = (node: SankeyNode) => node.y +
// node.dy / 2` is a RECORD parameter (one interface-typed object), not
// an array parameter — SankeyNode carries scalar number members (y,
// dy) alongside `value: any` and `number[]` members. The doc on
// recordParamMembersIn says a member whose own annotation is not
// number/boolean/string contributes its leaf unknown-sorted rather
// than refusing the whole parameter, so the record expansion should
// already serve a body that only ever reads the two scalar members.
//
// summaryDeclarationOf (kernel_summary_direct_test.go) only ever parses
// a BARE source with no checker/binder and requires the function to be
// the source's FIRST statement — an interface declaration ahead of the
// function fails that requirement outright, and recordParamMembersIn's
// named-type branch (namedTypeMembersOf) needs a checker to resolve
// "SankeyNode" to its interface members regardless. namedTypeCtx /
// namedTypeFunction (same file) is the established program-backed
// harness for exactly this shape — see
// TestSummaryParameterEntriesIn_AnInterfaceTypedParameterExpandsOneEntryPerMember.
func TestCenterYShapedRecordParameter_PremiseCheck(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearSummaryOutcomes()
	source := `
interface SankeyNode {
  dx: number;
  dy: number;
  name: string;
  value: any;
  x: number;
  y: number;
  depth: number;
  targetNodes: number[];
  targetLinks: number[];
  sourceNodes: number[];
  sourceLinks: number[];
}
function centerY(node: SankeyNode) { return node.y + node.dy / 2; }
`
	ctx, p := namedTypeCtx(t, source)
	declaration := namedTypeFunction(t, p, "centerY")
	RelowerSummaryBody(ctx, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(declaration)
	if !recorded {
		t.Fatalf("no outcome recorded — the lowering ran and must report a fate")
	}
	t.Logf("centerY-shaped body: outcome=%q construct=%q", outcome, construct)
	if outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q), want complete — the record expansion should serve two scalar members", outcome, construct)
	}
}

// TestArrayOfRecordsElementMemberRead_PremiseCheck was the FIRST pin in
// AGENT-BRIEF's records-as-array-elements dependency order, taken BEFORE
// the build below landed: an element access straight into one member, no
// intermediate local. Its premise (ArrayParameterOf declines an
// object-element array outright) was already refuted before this agent's
// build started — ArrayParameterOf admits `xs` (the use-scan never
// inspected the ELEMENT'S shape, only whether every occurrence of `xs`
// itself sits in a recognized array form, and `xs[i]` always did). What
// stayed porous until this pass was PathSlotIndexOf's own reading of
// `xs[i].a` — no arm resolved a PropertyAccessExpression over an
// ElementAccessExpression receiver to any slot (arrayElementMemberLeafOf,
// ir_object_slots_slot_index.go, closes it). Kept as a NAMED outcome pin
// now that the capability serves, rather than a premise log.
func TestArrayOfRecordsElementMemberRead_PremiseCheck(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearSummaryOutcomes()
	source := "function f(xs: { a: number, b: number }[], i: number) { return xs[i].a; }\n"
	ctx, p := namedTypeCtx(t, source)
	declaration := namedTypeFunction(t, p, "f")
	parameter := declaration.Parameters()[0]
	if _, isArray := ArrayParameterOf(ctx, checkerOf(ctx), declaration.Body(), parameter); !isArray {
		t.Fatalf("ArrayParameterOf(xs) declined — the array-parameter flattening itself regressed")
	}
	RelowerSummaryBody(ctx, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(declaration)
	if !recorded {
		t.Fatalf("no outcome recorded — the lowering ran and must report a fate")
	}
	if outcome != SummaryComplete {
		t.Errorf("xs[i].a body: outcome=%q construct=%q, want complete — the element-member read (step 3) should serve `return xs[i].a`", outcome, construct)
	}
}

// TestArrayParameterOf_ElementMembersExpandForARecordElement pins step 1
// of the records-as-array-elements build: an array-typed parameter whose
// ELEMENT is a record (a syntactic type literal here) expands
// ElementMembers to one leaf per property, spelled under the elem slot
// ("xs.elem.a", "xs.elem.b") — the vocabulary PathSlotIndexOf (step 3)
// and the array-parameter entry layout (step 2) both read.
func TestArrayParameterOf_ElementMembersExpandForARecordElement(t *testing.T) {
	ClearResolvedArrayParameters()
	ClearResolvedRecordMembers()
	source := "function f(xs: { a: number, b: number }[], i: number) { return xs[i].a; }\n"
	ctx, p := namedTypeCtx(t, source)
	declaration := namedTypeFunction(t, p, "f")
	parameter := declaration.Parameters()[0]
	local, isArray := ArrayParameterOf(ctx, checkerOf(ctx), declaration.Body(), parameter)
	if !isArray {
		t.Fatalf("ArrayParameterOf(xs) declined — the array-parameter flattening itself regressed")
	}
	if len(local.ElementMembers) != 2 {
		t.Fatalf("ElementMembers = %d members, want 2 (a, b): %+v", len(local.ElementMembers), local.ElementMembers)
	}
	bySlot := map[string]recordParamMember{}
	for _, member := range local.ElementMembers {
		bySlot[member.SlotName] = member
	}
	a, hasA := bySlot["xs.elem.a"]
	b, hasB := bySlot["xs.elem.b"]
	if !hasA || !hasB {
		t.Fatalf("ElementMembers slot names = %+v, want xs.elem.a and xs.elem.b", bySlot)
	}
	if a.Sort != BindingKindNumber || b.Sort != BindingKindNumber {
		t.Errorf("member sorts = a:%v b:%v, want both BindingKindNumber", a.Sort, b.Sort)
	}
}

// TestSummaryParameterEntries_RecordArrayWidensPastTwo pins the CALL-SITE
// hazard AGENT-BRIEF's step 2 flags by name: "the layout seam and the
// call-site seam must read one memoized answer." SummaryParameterEntries
// (this agent's territory, ir_summary_parameter_entries.go) now answers
// 1+len(ElementMembers) entries for a record-array parameter — verified
// here — but ir_summary_call_statement.go's own array-parameter branch
// (summaryCallStatement, NOT this agent's file) still unconditionally
// pushes exactly TWO effects into `args` regardless of how many `entries`
// SummaryParameterEntries just answered for the SAME parameter one line
// above it (ir_summary_call_statement.go:110 calls SummaryParameterEntries,
// then its array branch at :160 ignores the answer's length). A record-
// array parameter FOLLOWED BY ANY OTHER PARAMETER in the same declaration
// would therefore have every later parameter's call-site argument effects
// land at the wrong callee slot index once this widening is live for any
// summaryCallStatement-routed call — not merely porous, actively
// misaligned. This pin documents the width mismatch directly (no kernel
// needed): fix belongs in ir_summary_call_statement.go's array branch,
// widening its two-effect push to 1+len(entries)-1 TOP/unknown effects
// mirroring kernel_summaries.go's summaryEntryStates fix in this same
// pass, reading local.ElementMembers off the SAME arrayParamSlotsIn memo.
func TestSummaryParameterEntries_RecordArrayWidensPastTwo(t *testing.T) {
	ClearResolvedArrayParameters()
	ClearResolvedRecordMembers()
	source := "function g(xs: { a: number, b: number }[], i: number) { return xs[i].a; }\n"
	ctx, p := namedTypeCtx(t, source)
	declaration := namedTypeFunction(t, p, "g")
	parameter := declaration.Parameters()[0]
	// force the memo so the ctx-less SummaryParameterEntries (what
	// ir_summary_call_statement.go:110 actually calls) sees the resolved
	// ElementMembers exactly as the checker-backed layout call already did
	if _, flattened := arrayParamSlotsIn(ctx, parameter); !flattened {
		t.Fatalf("arrayParamSlotsIn(xs) declined — the array-parameter flattening itself regressed")
	}
	entries, ok := SummaryParameterEntries(parameter)
	if !ok {
		t.Fatalf("SummaryParameterEntries(xs) declined")
	}
	if len(entries) != 3 {
		t.Fatalf("len(entries) = %d, want 3 (xs.len, xs.elem.a, xs.elem.b) — SummaryParameterEntries no longer answers what this test predicts; re-check ir_summary_call_statement.go's array branch against the CURRENT width before assuming the hazard below still applies", len(entries))
	}
	t.Logf("SummaryParameterEntries(xs) = %d entries; ir_summary_call_statement.go's array branch (summaryCallStatement, line ~160) still pushes a HARDCODED two effects for this parameter — a sibling fix there must widen to len(entries) or every argument effect after xs misaligns by %d slots", len(entries), len(entries)-2)
}
