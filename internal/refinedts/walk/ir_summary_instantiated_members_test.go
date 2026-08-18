// Pins for the INSTANTIATED reading a type reference carrying TYPE
// ARGUMENTS takes (ir_summary_instantiated_members.go): `p: Box<number>`
// resolves through the checker's own instantiation to one leaf per data
// property, instead of namedTypeMembersOf's former refusal. The
// boundary negatives pin what the arm must NOT expand: a shape whose
// body calls a skipped method member (the use-scan fallback), a class
// instantiation, and an array-like instantiation (the array route owns
// those).

package walk

import (
	"fmt"
	"strings"
	"testing"
)

// TestKernelSummaryDirect_ATypeArgumentAnnotationExpandsThroughTheInstantiation
// is the capability pin: `p: Box<number>` where `interface Box<T> { lo:
// number; hi: T; tag: string }` expands to three leaves — lo NUMBER by
// its own annotation, hi NUMBER by the INSTANTIATION (T := number, which
// only the checker's answer spells), tag STRING — and the body reading
// two number members lowers complete. Before the instantiated arm this
// parameter never expanded (namedTypeMembersOf refused the reference
// over its type arguments) and the body could not lower whole.
func TestKernelSummaryDirect_ATypeArgumentAnnotationExpandsThroughTheInstantiation(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ctx, p := namedTypeCtx(t,
		"interface Box<T> { lo: number; hi: T; tag: string }\n"+
			"function f(p: Box<number>) { return p.lo + p.hi; }\n")
	declaration := namedTypeFunction(t, p, "f")
	entries, ok := SummaryParameterEntriesIn(ctx, declaration.Parameters()[0])
	if !ok {
		t.Fatalf("SummaryParameterEntriesIn(p: Box<number>) declined — the instantiated reference did not expand")
	}
	if len(entries) != 3 {
		t.Fatalf("len(entries) = %d, want 3 (p.lo, p.hi, p.tag): %+v", len(entries), entries)
	}
	byName := map[string]bodySlot{}
	for _, entry := range entries {
		byName[entry.Name] = entry
	}
	lo, hasLo := byName["p.lo"]
	hi, hasHi := byName["p.hi"]
	tag, hasTag := byName["p.tag"]
	if !hasLo || !hasHi || !hasTag {
		t.Fatalf("entry names = %+v, want p.lo, p.hi and p.tag", byName)
	}
	if lo.Sort != BindingKindNumber || lo.TypeofTag != TypeofTagNumber {
		t.Errorf("p.lo sort = %v/%v, want number/number", lo.Sort, lo.TypeofTag)
	}
	// hi's own annotation is `T`; only the instantiation says number
	if hi.Sort != BindingKindNumber || hi.TypeofTag != TypeofTagNumber {
		t.Errorf("p.hi sort = %v/%v, want number/number — the instantiated argument's own sort", hi.Sort, hi.TypeofTag)
	}
	if tag.Sort != BindingKindString || tag.TypeofTag != TypeofTagString {
		t.Errorf("p.tag sort = %v/%v, want string/string", tag.Sort, tag.TypeofTag)
	}
	RelowerSummaryBody(ctx, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(declaration)
	if !recorded {
		t.Fatalf("no outcome recorded — the lowering ran and must report a fate")
	}
	if outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q), want complete — two number-sorted leaves serve `p.lo + p.hi`", outcome, construct)
	}
}

// TestRecordParamMembersIn_AnOptionalInstantiatedMemberWearsMayBeAbsent
// pins the member-list detail: an OPTIONAL member of the instantiation
// (`lo?: number`) contributes its leaf number-sorted wearing
// MayBeAbsent — the optionality's undefined is stripped before the sort
// is read, exactly the division the syntax route makes (the absence
// rides the entry state, never the sort) — and a member instantiated to
// a string type argument takes the string sort.
func TestRecordParamMembersIn_AnOptionalInstantiatedMemberWearsMayBeAbsent(t *testing.T) {
	ctx, p := namedTypeCtx(t,
		"interface Box<T> { lo?: number; hi: T }\n"+
			"function f(p: Box<string>) { return p.hi; }\n")
	declaration := namedTypeFunction(t, p, "f")
	members, expanded := recordParamMembersIn(ctx, declaration.Parameters()[0])
	if !expanded {
		t.Fatalf("recordParamMembersIn(p: Box<string>) declined — the instantiated reference did not expand")
	}
	byKey := map[string]recordParamMember{}
	for _, member := range members {
		byKey[member.Key] = member
	}
	lo, hasLo := byKey["lo"]
	hi, hasHi := byKey["hi"]
	if !hasLo || !hasHi {
		t.Fatalf("member keys = %+v, want lo and hi", byKey)
	}
	if !lo.MayBeAbsent {
		t.Errorf("lo.MayBeAbsent = false, want true — the member was declared optional")
	}
	if lo.Sort != BindingKindNumber || lo.TypeofTag != TypeofTagNumber {
		t.Errorf("lo sort = %v/%v, want number/number — optionality's undefined is stripped before sorting", lo.Sort, lo.TypeofTag)
	}
	if hi.MayBeAbsent {
		t.Errorf("hi.MayBeAbsent = true, want false — the member is required")
	}
	if hi.Sort != BindingKindString || hi.TypeofTag != TypeofTagString {
		t.Errorf("hi sort = %v/%v, want string/string — the instantiated argument's own sort", hi.Sort, hi.TypeofTag)
	}
}

// TestRecordParamMembersIn_ACalledMethodMemberKeepsTheWholeNameSlot is
// the use-scan fallback boundary: the instantiation carries a data
// property (extra) beside a METHOD the body CALLS (getState). A method
// contributes no leaf, so under the expansion `api.getState()` is an
// interior path standing as a CALLEE, which the use scan classifies
// HANDED-OVER — every leaf Written and joined to the havoc vector,
// porous at best — where this very body over the un-expanded single
// slot lowers COMPLETE (observed before the arm was wired). The arm
// therefore scans the owning body first and keeps the whole-name slot
// unless the whole-name uses are plain reads.
func TestRecordParamMembersIn_ACalledMethodMemberKeepsTheWholeNameSlot(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ctx, p := namedTypeCtx(t,
		"interface Api<S> { getState(): S; extra: number }\n"+
			"function g(api: Api<{ x: number }>) { const s = api.getState(); return s; }\n")
	declaration := namedTypeFunction(t, p, "g")
	if _, expanded := recordParamMembersIn(ctx, declaration.Parameters()[0]); expanded {
		t.Fatalf("api expanded — the use-scan fallback must keep the whole-name slot for a body that calls a skipped method member")
	}
	RelowerSummaryBody(ctx, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(declaration)
	if !recorded {
		t.Fatalf("no outcome recorded — the lowering ran and must report a fate")
	}
	t.Logf("called-method body: outcome=%q construct=%q", outcome, construct)
	if construct == "a whole-record parameter use" {
		t.Errorf("construct = %q — the expansion reached the use scan it was meant to fall back from", construct)
	}
}

// TestRecordParamMembersIn_AClassInstantiationDeclines mirrors the
// syntax route's class refusal on the instantiated arm: an instance
// carries methods, accessors and private state the flattening cannot
// hold, whichever way the members are read.
func TestRecordParamMembersIn_AClassInstantiationDeclines(t *testing.T) {
	ctx, p := namedTypeCtx(t,
		"class C<T> { x: number = 1; y: T | undefined = undefined }\n"+
			"function h(c: C<number>) { return c.x; }\n")
	declaration := namedTypeFunction(t, p, "h")
	if _, expanded := recordParamMembersIn(ctx, declaration.Parameters()[0]); expanded {
		t.Errorf("a class instantiation expanded — the class refusal must hold on the instantiated arm")
	}
}

// TestRecordParamMembersIn_AnArrayLikeInstantiationDeclines keeps the
// record and array routes disjoint: `Array<number>` is a reference
// carrying a type argument, but its instantiation is array-like and the
// array-parameter route owns it — expanding `length` as a record leaf
// here would put one annotation on two slot families.
func TestRecordParamMembersIn_AnArrayLikeInstantiationDeclines(t *testing.T) {
	ctx, p := namedTypeCtx(t,
		"function k(xs: Array<number>) { return xs.length; }\n")
	declaration := namedTypeFunction(t, p, "k")
	if _, expanded := recordParamMembersIn(ctx, declaration.Parameters()[0]); expanded {
		t.Errorf("an array-like instantiation expanded as a record — the array route owns this annotation")
	}
}

// TestRecordParamMembersIn_AWideInstantiationExpandsInFull pins the
// no-budget behavior: capability is never refused for cost, so an
// instantiation spelling many data members expands every one of them —
// the parameter's entry vector carries whatever width the interface
// declares, and cost shows at the wall, never as a decline here. The
// member count (70) sits past the removed instantiatedMemberBudget (64),
// so this fixture is the one that would have refused under the old cap.
func TestRecordParamMembersIn_AWideInstantiationExpandsInFull(t *testing.T) {
	const memberCount = 70
	var b strings.Builder
	b.WriteString("interface Big<T> { first: T;\n")
	for i := 0; i < memberCount; i++ {
		fmt.Fprintf(&b, "  m%d: number;\n", i)
	}
	b.WriteString("}\n")
	b.WriteString("function w(p: Big<number>) { return p.first; }\n")
	ctx, p := namedTypeCtx(t, b.String())
	declaration := namedTypeFunction(t, p, "w")
	members, expanded := recordParamMembersIn(ctx, declaration.Parameters()[0])
	if !expanded {
		t.Fatalf("a wide instantiation declined — capability must never be refused for cost")
	}
	if len(members) != memberCount+1 {
		t.Errorf("len(members) = %d, want %d (first plus every mN)", len(members), memberCount+1)
	}
}

// TestRecordParamMembersIn_ANestedInstantiatedMemberExpands pins the
// LANDED widening (nestedMemberLeavesOf's TypeReferenceNode case, this
// wave): recharts' RechartsRootState shape — `state: RechartsRootState`
// where `RechartsRootState` is a plain type LITERAL alias (no arguments
// of its own — declaredTypeMembersOf's existing alias arm already
// expands it), and one of its OWN members (`options:
// ReturnType<typeof optionsReducer>`) carries type arguments — a NESTED
// position, not the alias's own entry position. nestedMemberLeavesOf
// (ir_summary_record_member_reading.go) now calls
// instantiatedReferenceMembersOf for a nested member carrying type
// arguments, the same instantiated reading namedTypeMembersOf takes at
// the entry position, so "options" expands into its own leaves
// ("options.tag" string, "options.count" number) instead of falling
// back to a single unknown-sorted "options" leaf.
func TestRecordParamMembersIn_ANestedInstantiatedMemberExpands(t *testing.T) {
	ctx, p := namedTypeCtx(t,
		"function optionsReducer(): { tag: string; count: number } { return { tag: 'x', count: 1 } }\n"+
			"type RootState = { options: ReturnType<typeof optionsReducer>; other: number };\n"+
			"function f(state: RootState) { return state.options.tag; }\n")
	declaration := namedTypeFunction(t, p, "f")
	members, expanded := recordParamMembersIn(ctx, declaration.Parameters()[0])
	if !expanded {
		t.Fatalf("RootState did not expand at all — the alias-of-a-literal arm should still answer its own two top members")
	}
	if len(members) != 3 {
		t.Fatalf("len(members) = %d, want 3 (other, options.tag, options.count): %+v", len(members), members)
	}
	byPath := map[string]recordParamMember{}
	for _, member := range members {
		byPath[strings.Join(member.Path, " ")] = member
	}
	other, hasOther := byPath["other"]
	tag, hasTag := byPath["options tag"]
	count, hasCount := byPath["options count"]
	if !hasOther || !hasTag || !hasCount {
		t.Fatalf("member paths = %+v, want other, options tag and options count", byPath)
	}
	if other.Sort != BindingKindNumber {
		t.Errorf("other.Sort = %v, want number", other.Sort)
	}
	if tag.Sort != BindingKindString || tag.TypeofTag != TypeofTagString {
		t.Errorf("options.tag sort = %v/%v, want string/string — the instantiation's own leaf", tag.Sort, tag.TypeofTag)
	}
	if count.Sort != BindingKindNumber || count.TypeofTag != TypeofTagNumber {
		t.Errorf("options.count sort = %v/%v, want number/number — the instantiation's own leaf", count.Sort, count.TypeofTag)
	}
	if tag.SlotName != "state.options.tag" {
		t.Errorf("options.tag SlotName = %q, want state.options.tag", tag.SlotName)
	}
	if count.SlotName != "state.options.count" {
		t.Errorf("options.count SlotName = %q, want state.options.count", count.SlotName)
	}
}

// TestRecordParamMembersIn_WideNestedInstantiatedMembersExpandInFull pins
// the no-budget behavior at the HOLDER level: a holder with EIGHT
// members, each `ReturnType<typeof reducerN>` carrying five leaves of
// its own — forty leaves total — expands every one of them. This is
// RechartsRootState's own shape (sixteen `ReturnType<typeof reducer>`
// members on recharts' corpus): capability is never refused for cost,
// so nested-instantiated members append their expanded leaves
// unconditionally, exactly like a plain type-literal or array-pair
// nested member always has. The construct string "a body past the slot
// budget" no longer exists anywhere in this tree — a wide body attempts
// real lowering and its fate is read off SummaryOutcomeOf, not
// pre-declined here.
func TestRecordParamMembersIn_WideNestedInstantiatedMembersExpandInFull(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	var b strings.Builder
	for i := 0; i < 8; i++ {
		fmt.Fprintf(&b, "function reducer%d(): { a%d: number; b%d: number; c%d: number; d%d: number; e%d: number } { return { a%d: 1, b%d: 1, c%d: 1, d%d: 1, e%d: 1 } }\n",
			i, i, i, i, i, i, i, i, i, i, i)
	}
	b.WriteString("type RootState = {\n")
	for i := 0; i < 8; i++ {
		fmt.Fprintf(&b, "  slice%d: ReturnType<typeof reducer%d>;\n", i, i)
	}
	b.WriteString("};\n")
	b.WriteString("function f(state: RootState) { return state.slice0; }\n")
	ctx, p := namedTypeCtx(t, b.String())
	declaration := namedTypeFunction(t, p, "f")
	members, expanded := recordParamMembersIn(ctx, declaration.Parameters()[0])
	if !expanded {
		t.Fatalf("RootState did not expand at all — the top-level alias-of-a-literal arm should still answer its own eight members")
	}
	if len(members) != 40 {
		t.Fatalf("len(members) = %d, want 40 (eight slices, five leaves each): %+v", len(members), members)
	}
	byPath := map[string]recordParamMember{}
	for _, member := range members {
		byPath[strings.Join(member.Path, " ")] = member
	}
	for i := 0; i < 8; i++ {
		slice := fmt.Sprintf("slice%d", i)
		for _, leaf := range []string{"a", "b", "c", "d", "e"} {
			path := fmt.Sprintf("%s %s%d", slice, leaf, i)
			member, has := byPath[path]
			if !has {
				t.Fatalf("member paths = %+v, want a nested leaf %q — every slice's five leaves must expand", byPath, path)
			}
			if member.Sort != BindingKindNumber || member.TypeofTag != TypeofTagNumber {
				t.Errorf("%s sort = %v/%v, want number/number — the reducer's own return leaf", path, member.Sort, member.TypeofTag)
			}
		}
	}
	RelowerSummaryBody(ctx, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(declaration)
	if !recorded {
		t.Fatalf("no outcome recorded — the lowering ran and must report a fate")
	}
	t.Logf("outcome=%q construct=%q", outcome, construct)
	if construct == "a body past the slot budget" {
		t.Errorf("construct = %q — this string must never appear; the budget and its decline are gone", construct)
	}
}

// TestRecordParamMembersIn_AnAliasOfAGenericReferenceExpands pins
// construct 2: `type A = Box<number>` — a type ALIAS whose right side is
// itself a type REFERENCE carrying type arguments. declaredTypeMembersOf's
// alias-of-reference arm (ir_summary_named_type_members.go) recurses
// through namedTypeMembersOf's own two readings, but its own guard
// (`if innerReference.TypeArguments != nil { return nil, false }`)
// refused before reaching instantiatedReferenceMembersOf, so a parameter
// behind such an alias never expanded even though the direct-arguments
// form (`p: Box<number>` written straight on the parameter) already does
// (TestKernelSummaryDirect_ATypeArgumentAnnotationExpandsThroughTheInstantiation).
func TestRecordParamMembersIn_AnAliasOfAGenericReferenceExpands(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ctx, p := namedTypeCtx(t,
		"interface Box<T> { lo: number; hi: T; tag: string }\n"+
			"type NumberBox = Box<number>;\n"+
			"function f(p: NumberBox) { return p.lo + p.hi; }\n")
	declaration := namedTypeFunction(t, p, "f")
	entries, ok := SummaryParameterEntriesIn(ctx, declaration.Parameters()[0])
	if !ok {
		t.Fatalf("SummaryParameterEntriesIn(p: NumberBox) declined — the alias-of-a-generic-reference did not expand")
	}
	if len(entries) != 3 {
		t.Fatalf("len(entries) = %d, want 3 (p.lo, p.hi, p.tag): %+v", len(entries), entries)
	}
	byName := map[string]bodySlot{}
	for _, entry := range entries {
		byName[entry.Name] = entry
	}
	lo, hasLo := byName["p.lo"]
	hi, hasHi := byName["p.hi"]
	_, hasTag := byName["p.tag"]
	if !hasLo || !hasHi || !hasTag {
		t.Fatalf("entry names = %+v, want p.lo, p.hi and p.tag", byName)
	}
	if lo.Sort != BindingKindNumber || lo.TypeofTag != TypeofTagNumber {
		t.Errorf("p.lo sort = %v/%v, want number/number", lo.Sort, lo.TypeofTag)
	}
	if hi.Sort != BindingKindNumber || hi.TypeofTag != TypeofTagNumber {
		t.Errorf("p.hi sort = %v/%v, want number/number — the instantiated argument's own sort", hi.Sort, hi.TypeofTag)
	}
	RelowerSummaryBody(ctx, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(declaration)
	if !recorded {
		t.Fatalf("no outcome recorded — the lowering ran and must report a fate")
	}
	if outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q), want complete — two number-sorted leaves serve `p.lo + p.hi`", outcome, construct)
	}
}

// TestRecordParamMembersIn_AParameterizedAliasOfAGenericReferenceStillDeclines
// keeps the existing guard: an alias that is ITSELF parameterized
// (`type Wrapped<T> = Box<T>`) has no instantiation of its own at the
// alias link — a caller's `Wrapped<number>` never resolves back through
// this reader at all (the alias's OWN type parameters are a fact about
// the alias declaration, invisible to a parameter typed plain `Wrapped`
// with no arguments) — so the existing `len(parameterNames) > 0` guard
// must keep refusing rather than substituting the alias's own type
// parameter as if it were resolved.
func TestRecordParamMembersIn_AParameterizedAliasOfAGenericReferenceStillDeclines(t *testing.T) {
	ctx, p := namedTypeCtx(t,
		"interface Box<T> { lo: number; hi: T }\n"+
			"type Wrapped<T> = Box<T>;\n"+
			"function f(p: Wrapped) { return 1; }\n")
	declaration := namedTypeFunction(t, p, "f")
	if _, expanded := recordParamMembersIn(ctx, declaration.Parameters()[0]); expanded {
		t.Errorf("a parameterized alias of a generic reference expanded — its own type parameter has no instantiation at this link")
	}
}

// TestDeclaredTypeMembersOf_AnAliasOfAGenericReferenceAtAHeritageLinkExpandsInIsolation
// pins the FIX: declaredTypeMembersOf's own alias-of-reference arm
// (ir_summary_named_type_members.go) now routes an args-bearing alias
// target through instantiatedReferenceMembersOf instead of declining —
// called directly on "Child" the way heritageMembersOf calls it (the
// SAME call this test used to observe declining before the fix), the
// heritage parent NumberBox (= Box<number>) now expands and Child's own
// members merge over it.
func TestDeclaredTypeMembersOf_AnAliasOfAGenericReferenceAtAHeritageLinkExpandsInIsolation(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ctx, p := namedTypeCtx(t,
		"interface Box<T> { lo: number; hi: T; tag: string }\n"+
			"type NumberBox = Box<number>;\n"+
			"interface Child extends NumberBox { extra: boolean }\n"+
			"function useIt(x: Child) { return x; }\n")
	fn := namedTypeFunction(t, p, "useIt")
	paramType := fn.AsFunctionDeclaration().Parameters.Nodes[0].AsParameterDeclaration().Type
	typeName := paramType.AsTypeReferenceNode().TypeName
	members, ok := declaredTypeMembersOf(ctx, "x", typeName, nil, true)
	if !ok {
		t.Fatalf("declaredTypeMembersOf(Child) declined in isolation — the alias-of-reference arm should now route NumberBox through the instantiated reading")
	}
	byKey := map[string]recordParamMember{}
	for _, member := range members {
		byKey[member.Key] = member
	}
	if _, hasLo := byKey["lo"]; !hasLo {
		t.Errorf("member keys = %+v, want \"lo\" from the inherited NumberBox=Box<number> members", byKey)
	}
	if _, hasHi := byKey["hi"]; !hasHi {
		t.Errorf("member keys = %+v, want \"hi\" from the inherited NumberBox=Box<number> members", byKey)
	}
	if _, hasExtra := byKey["extra"]; !hasExtra {
		t.Errorf("member keys = %+v, want \"extra\" from Child's own members", byKey)
	}
}

// TestSummaryParameterEntriesIn_AnAliasOfAGenericReferenceAtAHeritageLinkExpandsThroughTheWholeTypeFallback
// pins the end-to-end behavior at the PARAMETER position: `p: Child`
// (Child extends the alias-of-instantiation NumberBox) expands and
// serves complete, not because the heritage walk resolved NumberBox
// (the isolation pin above shows it still declines), but because
// recordParamMembersIn's top-level fallback re-tries Child's OWN
// reference — carrying no arguments — through
// instantiatedReferenceMembersOf, whose checker-authority member list
// already includes every inherited property.
func TestSummaryParameterEntriesIn_AnAliasOfAGenericReferenceAtAHeritageLinkExpandsThroughTheWholeTypeFallback(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ctx, p := namedTypeCtx(t,
		"interface Box<T> { lo: number; hi: T; tag: string }\n"+
			"type NumberBox = Box<number>;\n"+
			"interface Child extends NumberBox { extra: boolean }\n"+
			"function f(p: Child) { return p.lo + p.hi; }\n")
	declaration := namedTypeFunction(t, p, "f")
	entries, ok := SummaryParameterEntriesIn(ctx, declaration.Parameters()[0])
	if !ok {
		t.Fatalf("SummaryParameterEntriesIn(p: Child) declined — the top-level whole-reference fallback did not expand it")
	}
	byName := map[string]bodySlot{}
	for _, entry := range entries {
		byName[entry.Name] = entry
	}
	lo, hasLo := byName["p.lo"]
	hi, hasHi := byName["p.hi"]
	if !hasLo || !hasHi {
		t.Fatalf("entry names = %+v, want p.lo and p.hi from the inherited NumberBox members", byName)
	}
	if lo.Sort != BindingKindNumber || hi.Sort != BindingKindNumber {
		t.Errorf("p.lo/p.hi sorts = %v/%v, want number/number", lo.Sort, hi.Sort)
	}
	RelowerSummaryBody(ctx, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(declaration)
	if !recorded {
		t.Fatalf("no outcome recorded — the lowering ran and must report a fate")
	}
	if outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q), want complete", outcome, construct)
	}
}

// TestRecordParamMembersIn_AnAliasOfAnIntersectionWithAGenericReferenceSideExpands
// pins construct 2 at the ONLY position intersectionMembersOf is ever
// reached from: a type ALIAS whose own target is an intersection
// (`type Combo = Extra & NumberBox`) — namedTypeMembersOf has no
// IsIntersectionTypeNode arm of its own, so a plain intersection written
// straight on a parameter's annotation never reaches intersectionMembersOf
// at all; only declaredTypeMembersOf's alias-of-intersection arm
// (ir_summary_named_type_members.go, `ast.IsIntersectionTypeNode(asAlias.Type)`)
// calls it. Its type-reference-side arm
// (ir_summary_composite_type_members.go) only refuses a side that
// carries arguments ON ITS OWN SPELLING (`Extra & Box<number>` already
// worked, refused before ever reaching declaredTypeMembersOf) — a side
// that is a bare alias NAME recurses straight into
// declaredTypeMembersOf(ctx, holder, "NumberBox", visiting, false),
// which is exactly the alias-of-reference arm this fix widens. Unlike
// the heritage case, there is no whole-reference fallback here: `p`'s
// OWN annotation is a bare reference to "Combo", which resolves through
// the alias-of-intersection arm rather than instantiatedReferenceMembersOf
// directly.
func TestRecordParamMembersIn_AnAliasOfAnIntersectionWithAGenericReferenceSideExpands(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ctx, p := namedTypeCtx(t,
		"interface Box<T> { lo: number; hi: T; tag: string }\n"+
			"type NumberBox = Box<number>;\n"+
			"interface Extra { extra: boolean }\n"+
			"type Combo = Extra & NumberBox;\n"+
			"function f(p: Combo) { return p.lo + p.hi; }\n")
	declaration := namedTypeFunction(t, p, "f")
	entries, ok := SummaryParameterEntriesIn(ctx, declaration.Parameters()[0])
	if !ok {
		t.Fatalf("SummaryParameterEntriesIn(p: Combo) declined — the intersection side's alias-of-a-generic-reference did not expand")
	}
	byName := map[string]bodySlot{}
	for _, entry := range entries {
		byName[entry.Name] = entry
	}
	lo, hasLo := byName["p.lo"]
	hi, hasHi := byName["p.hi"]
	if !hasLo || !hasHi {
		t.Fatalf("entry names = %+v, want p.lo and p.hi from the NumberBox side", byName)
	}
	if lo.Sort != BindingKindNumber || hi.Sort != BindingKindNumber {
		t.Errorf("p.lo/p.hi sorts = %v/%v, want number/number", lo.Sort, hi.Sort)
	}
	RelowerSummaryBody(ctx, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(declaration)
	if !recorded {
		t.Fatalf("no outcome recorded — the lowering ran and must report a fate")
	}
	if outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q), want complete", outcome, construct)
	}
}
