// Pins for the NESTED-ARRAY element layout: an array parameter whose
// element is itself an array exposes element-of-element slots — the
// same ".len"/".elem" pair vocabulary one level down ("xs.elem.len",
// "xs.elem.elem") — and declarations binding such values lower through
// the element-alias route.

package walk

import (
	"testing"
)

func clearNestedArrayMemos() {
	ClearSummaryOutcomes()
	ClearResolvedArrayParameters()
	ClearResolvedRecordMembers()
	ClearElementAliasedLocals()
}

// TestNamedTypeMembersOf_ARecursiveTypeThroughAnArrayMemberTerminates
// pins the cycle guard across the array hop: an interface whose array
// member's element names the interface again (`children: TreeNode[]`)
// must terminate at declaredTypeMembersOf's revisit check. Before the
// `visiting` list rode through arrayElementPairMembers /
// arrayParameterElementMembers / namedTypeMembersOf, this exact shape
// recursed without bound — observed as the 42GB service.test process
// and the TestArraySortBisect_ExactTextAlone timeout panic
// (2026-08-17), whose stack cycles through these three functions.
func TestNamedTypeMembersOf_ARecursiveTypeThroughAnArrayMemberTerminates(t *testing.T) {
	clearNestedArrayMemos()
	source := "interface TreeNode { value: number; children: TreeNode[]; }\n" +
		"function f(n: TreeNode) { return n.value; }\n"
	ctx, p := namedTypeCtx(t, source)
	declaration := namedTypeFunction(t, p, "f")
	parameter := declaration.Parameters()[0]
	annotation := parameter.AsParameterDeclaration().Type
	members, expanded := namedTypeMembersOf(ctx, "n", annotation, nil)
	if !expanded {
		// terminating with a decline is acceptable; looping is the defect
		return
	}
	bySlot := map[string]recordParamMember{}
	for _, member := range members {
		bySlot[member.SlotName] = member
	}
	if _, hasValue := bySlot["n.value"]; !hasValue {
		t.Fatalf("expanded without n.value: %+v", bySlot)
	}
}

// TestArrayParameterOf_NestedArrayElementExpandsThePair pins the layout:
// `xs: number[][]` expands ElementMembers to the inner array's own pair,
// spelled under the elem slot — "xs.elem.len" (number) and
// "xs.elem.elem" (the inner element's sort) — the same way a record
// element expands to "xs.elem.a"/"xs.elem.b" today.
func TestArrayParameterOf_NestedArrayElementExpandsThePair(t *testing.T) {
	clearNestedArrayMemos()
	source := "function f(xs: number[][], i: number, j: number) { return xs[i][j]; }\n"
	ctx, p := namedTypeCtx(t, source)
	declaration := namedTypeFunction(t, p, "f")
	parameter := declaration.Parameters()[0]
	local, isArray := ArrayParameterOf(ctx, checkerOf(ctx), declaration.Body(), parameter)
	if !isArray {
		t.Fatalf("ArrayParameterOf(xs) declined — the array-parameter flattening itself regressed")
	}
	if len(local.ElementMembers) != 2 {
		t.Fatalf("ElementMembers = %d members, want 2 (len, elem): %+v", len(local.ElementMembers), local.ElementMembers)
	}
	bySlot := map[string]recordParamMember{}
	for _, member := range local.ElementMembers {
		bySlot[member.SlotName] = member
	}
	lenMember, hasLen := bySlot["xs.elem.len"]
	elemMember, hasElem := bySlot["xs.elem.elem"]
	if !hasLen || !hasElem {
		t.Fatalf("ElementMembers slot names = %+v, want xs.elem.len and xs.elem.elem", bySlot)
	}
	if lenMember.Sort != BindingKindNumber {
		t.Errorf("len member sort = %v, want BindingKindNumber", lenMember.Sort)
	}
	if elemMember.Sort != BindingKindNumber {
		t.Errorf("elem member sort = %v, want BindingKindNumber (the inner element is number)", elemMember.Sort)
	}
}

// TestArrayParameterOf_NestedArrayRecordElementExpandsLeaves pins the
// record composition one level down: `xs: { y: number }[][]` expands to
// the inner pair's len plus the record's leaves under the inner elem —
// "xs.elem.len" and "xs.elem.elem.y".
func TestArrayParameterOf_NestedArrayRecordElementExpandsLeaves(t *testing.T) {
	clearNestedArrayMemos()
	source := "function f(xs: { y: number }[][], i: number, j: number) { return xs[i][j].y; }\n"
	ctx, p := namedTypeCtx(t, source)
	declaration := namedTypeFunction(t, p, "f")
	parameter := declaration.Parameters()[0]
	local, isArray := ArrayParameterOf(ctx, checkerOf(ctx), declaration.Body(), parameter)
	if !isArray {
		t.Fatalf("ArrayParameterOf(xs) declined — the array-parameter flattening itself regressed")
	}
	bySlot := map[string]recordParamMember{}
	for _, member := range local.ElementMembers {
		bySlot[member.SlotName] = member
	}
	if _, hasLen := bySlot["xs.elem.len"]; !hasLen {
		t.Fatalf("no xs.elem.len member: %+v", bySlot)
	}
	leaf, hasLeaf := bySlot["xs.elem.elem.y"]
	if !hasLeaf {
		t.Fatalf("no xs.elem.elem.y member: %+v", bySlot)
	}
	if leaf.Sort != BindingKindNumber {
		t.Errorf("xs.elem.elem.y sort = %v, want BindingKindNumber", leaf.Sort)
	}
	if len(leaf.Path) != 2 || leaf.Path[0] != "elem" || leaf.Path[1] != "y" {
		t.Errorf("xs.elem.elem.y path = %v, want [elem y]", leaf.Path)
	}
}

// TestNestedArrayElementRead_InnerLengthServes pins the direct read
// spelling: `xs[i].length` over a nested-array parameter resolves to the
// "xs.elem.len" slot (the ".length"→".len" mapping the flattened array's
// own length read already wears), and the body completes.
func TestNestedArrayElementRead_InnerLengthServes(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	clearNestedArrayMemos()
	source := "function f(xs: number[][], i: number) { return xs[i].length; }\n"
	ctx, p := namedTypeCtx(t, source)
	declaration := namedTypeFunction(t, p, "f")
	RelowerSummaryBody(ctx, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(p.Checker, declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	if outcome != SummaryComplete {
		t.Errorf("xs[i].length: outcome=%q construct=%q, want complete", outcome, construct)
	}
}

// TestNestedArrayElementRead_IndexIndexServes pins `xs[i][j]`: the
// element-of-element read answers the "xs.elem.elem" join, or-absent
// (nothing bounds either index), and the body completes.
func TestNestedArrayElementRead_IndexIndexServes(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	clearNestedArrayMemos()
	source := "function f(xs: number[][], i: number, j: number) { return xs[i][j]; }\n"
	ctx, p := namedTypeCtx(t, source)
	declaration := namedTypeFunction(t, p, "f")
	RelowerSummaryBody(ctx, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(p.Checker, declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	if outcome != SummaryComplete {
		t.Errorf("xs[i][j]: outcome=%q construct=%q, want complete", outcome, construct)
	}
}

// TestNestedArrayAlias_RowLengthServes pins the DECLARATION the census
// family names: `const row = xs[i]` over a nested-array parameter is an
// element alias — the statement lowers to nothing, and `row.length`
// resolves to "xs.elem.len".
func TestNestedArrayAlias_RowLengthServes(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	clearNestedArrayMemos()
	source := "function f(xs: number[][], i: number) { const row = xs[i]; return row.length; }\n"
	ctx, p := namedTypeCtx(t, source)
	declaration := namedTypeFunction(t, p, "f")
	RelowerSummaryBody(ctx, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(p.Checker, declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	if outcome != SummaryComplete {
		t.Errorf("const row = xs[i]; return row.length: outcome=%q construct=%q, want complete", outcome, construct)
	}
}

// TestNestedArrayAlias_RowIndexServes pins the alias's index read:
// `row[j]` resolves to the "xs.elem.elem" join, or-absent.
func TestNestedArrayAlias_RowIndexServes(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	clearNestedArrayMemos()
	source := "function f(xs: number[][], i: number, j: number) { const row = xs[i]; return row[j]; }\n"
	ctx, p := namedTypeCtx(t, source)
	declaration := namedTypeFunction(t, p, "f")
	RelowerSummaryBody(ctx, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(p.Checker, declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	if outcome != SummaryComplete {
		t.Errorf("const row = xs[i]; return row[j]: outcome=%q construct=%q, want complete", outcome, construct)
	}
}

// TestNestedArrayAlias_SecondLevelRecordMemberServes pins the composed
// declaration chain the Sankey bodies spell: `const row = xs[i]` then
// `const node = row[j]` over `{ y: number }[][]` — the second alias
// binds the element-of-element record, and `node.y` resolves to
// "xs.elem.elem.y".
func TestNestedArrayAlias_SecondLevelRecordMemberServes(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	clearNestedArrayMemos()
	source := "function f(xs: { y: number }[][], i: number, j: number) { const row = xs[i]; const node = row[j]; return node.y; }\n"
	ctx, p := namedTypeCtx(t, source)
	declaration := namedTypeFunction(t, p, "f")
	RelowerSummaryBody(ctx, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(p.Checker, declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	if outcome != SummaryComplete {
		t.Errorf("const row = xs[i]; const node = row[j]; return node.y: outcome=%q construct=%q, want complete", outcome, construct)
	}
}

// TestElementAlias_NestedRecordMemberDoesNotServeFlat pins a soundness
// rule: `xs: { inner: { deep: number } }[]` expands a NESTED leaf whose
// Path is ["inner","deep"] and whose bare Key is "deep" — and a flat
// read `p.deep` through the alias names an UNDECLARED member (the value
// there is undefined), so it must NOT resolve to the "xs.elem.inner.deep"
// slot. A Key-only match would serve exactly that wrong answer.
func TestElementAlias_NestedRecordMemberDoesNotServeFlat(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	clearNestedArrayMemos()
	source := "function f(xs: { inner: { deep: number } }[], i: number) { const p = xs[i]; return p.deep; }\n"
	ctx, p := namedTypeCtx(t, source)
	declaration := namedTypeFunction(t, p, "f")
	RelowerSummaryBody(ctx, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(p.Checker, declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	if outcome == SummaryComplete {
		t.Errorf("p.deep over {inner:{deep}} element served complete (construct=%q) — `deep` is not a flat member of the element; serving the inner.deep slot is a wrong answer, not a weak one", construct)
	}
}

// TestArrayOfRecords_OuterLengthReadServes pins the outer length read on
// a MEMBER-EXPANDED array parameter: with the scalar "xs.elem" slot
// replaced by member slots, `xs.length` must still resolve to "xs.len".
func TestArrayOfRecords_OuterLengthReadServes(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	clearNestedArrayMemos()
	source := "function f(xs: { a: number }[]) { return xs.length; }\n"
	ctx, p := namedTypeCtx(t, source)
	declaration := namedTypeFunction(t, p, "f")
	RelowerSummaryBody(ctx, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(p.Checker, declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	if outcome != SummaryComplete {
		t.Errorf("xs.length over a member-expanded array: outcome=%q construct=%q, want complete", outcome, construct)
	}
}
