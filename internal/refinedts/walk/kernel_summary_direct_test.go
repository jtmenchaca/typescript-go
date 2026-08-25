// The widened summary route (KernelSummaryDirect — this port's S5
// completion): a body whose only writes hit locals summarizes
// kernel-side without the effect-free pre-scan, and the declines that
// keep the route honest. Skipped (never a faked pass) when the native
// kernel dylib is absent, the same gate kernel_delegation_test uses.
//
// This file holds the record-parameter-entry tests: the parameter
// EXPANSION rules (SummaryParameterEntries/recordParamMembersOf) and
// the direct KernelSummaryDirect calls over an inline type-literal
// parameter. Sibling files cover named-type parameters
// (kernel_summary_direct_named_type_test.go), the `this` bundle
// (kernel_summary_direct_this_bundle_test.go), the serving rule and the
// collection's local-skip rules (kernel_summary_direct_serving_test.go),
// and the nested member families
// (kernel_summary_direct_nested_members_test.go). The helpers
// summaryDeclarationOf, exactNumber, and recordObject are defined here
// and used by every sibling.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/parser"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
)

// summaryDeclarationOf parses a throwaway source whose FIRST statement
// is a function declaration and answers that declaration node.
func summaryDeclarationOf(t *testing.T, source string) *ast.Node {
	t.Helper()
	opts := ast.SourceFileParseOptions{FileName: "/s.ts", Path: "/s.ts"}
	file := parser.ParseSourceFile(opts, source, core.ScriptKindTS)
	if len(file.Statements.Nodes) == 0 || !ast.IsFunctionDeclaration(file.Statements.Nodes[0]) {
		t.Fatalf("no function declaration parsed from %q", source)
	}
	return file.Statements.Nodes[0]
}

func exactNumber(t *testing.T, x float64) abstractdomain.AbstractValue {
	t.Helper()
	return abstractdomain.KnownValues([]float64{x}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
}

func TestKernelSummaryDirect_ALocalWritingLoopBodySummarizesWithoutTheEffectFreeGate(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	// scanBody calls this body impure (it assigns s and i), so the
	// effect-free route never reaches it — the direct route must.
	// The while head compares against a LITERAL, so the head's truth
	// and falsity sets narrow the loop (LoopHeadOf); a head comparing
	// two slots lowers too, but claims no narrowing on either side.
	declaration := summaryDeclarationOf(t,
		"function f(n: number) { let s = 0; let i = 0; while (i < 3) { s = s + n; i = i + 1; } return s; }")
	contract := &FunctionContract{Declaration: declaration}
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	answer, ok := KernelSummaryDirect(ctx, []abstractdomain.AbstractValue{exactNumber(t, 2)}, contract)
	if !ok {
		t.Fatalf("KernelSummaryDirect ok = false, want a summarized answer")
	}
	state, stateOk := StateOfKnown(answer)
	if !stateOk || state.Top {
		t.Fatalf("summarized answer did not spell as a scalar state: %+v", answer)
	}
	// f(2) = 2+2+2 = 6; the kernel's loop answer must ADMIT 6 (a
	// widened invariant is sound; an answer excluding 6 is not)
	if !kernel.Member(state.Set, []float64{6}) {
		t.Errorf("summary of f(2) excludes the true value 6: %+v", state.Set)
	}
}

func TestKernelSummaryDirect_APropertyWritingBodyDeclines(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	// `o: { k: number }` EXPANDS to the entry slot "o.k", so a slot for
	// the write's target now exists — and the write must still decline:
	// the caller's own object would move, and a summary carries no effect
	// back out through its entries
	declaration := summaryDeclarationOf(t,
		"function f(o: { k: number }, n: number) { o.k = n; return n; }")
	contract := &FunctionContract{Declaration: declaration}
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	if _, ok := KernelSummaryDirect(ctx, []abstractdomain.AbstractValue{exactNumber(t, 1), exactNumber(t, 2)}, contract); ok {
		t.Errorf("a property-writing body summarized — its effect on the caller's object would be dropped")
	}
}

/* ── record parameters ───────────────────────────────────────────── */

// recordObject builds the object argument an expanded parameter reads:
// one key per name, each holding the exact number given.
func recordObject(t *testing.T, fields map[string]float64, order []string) abstractdomain.AbstractValue {
	t.Helper()
	keys := make([]abstractdomain.ObjectKey, 0, len(order))
	for _, name := range order {
		keys = append(keys, abstractdomain.ObjectKey{Name: name, Value: exactNumber(t, fields[name])})
	}
	return abstractdomain.KnownObject(keys, nil, true, abstractdomain.TrustProved, false)
}

func TestSummaryParameterEntries_ATypeLiteralParameterExpandsOneEntryPerMember(t *testing.T) {
	declaration := summaryDeclarationOf(t,
		"function f(p: { lo: number, hi: string, on: boolean }) { return p.lo; }")
	entries, ok := SummaryParameterEntries(declaration.Parameters()[0])
	if !ok {
		t.Fatalf("a scalar-membered type literal declined the expansion")
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
}

func TestSummaryParameterEntries_AScalarParameterKeepsItsSingleWholeNameEntry(t *testing.T) {
	declaration := summaryDeclarationOf(t, "function f(n: number) { return n; }")
	entries, ok := SummaryParameterEntries(declaration.Parameters()[0])
	if !ok || len(entries) != 1 {
		t.Fatalf("entries = %v (ok %v), want exactly one", entries, ok)
	}
	if entries[0].Name != "n" || entries[0].Sort != BindingKindNumber {
		t.Errorf("scalar entry = %+v, want {n number number}", entries[0])
	}
}

func TestSummaryParameterEntries_TheShapesThatStayWholeNameDeclines(t *testing.T) {
	// each of these keeps the parameter a single unknown-sorted entry —
	// the expansion answers no members for any of them. An optional
	// member, a richer member, and a nested literal all EXPAND now (the
	// accounting-argument widening scalarMemberListOfIn documents) —
	// pinned separately below, not here.
	sources := map[string]string{
		"a class name":       "function f(p: Point) { return 1; }",
		"an interface name":  "function f(p: Shape) { return 1; }",
		"a union":            "function f(p: { lo: number } | { hi: number }) { return 1; }",
		"a method":           "function f(p: { lo(): number }) { return 1; }",
		"an index signature": "function f(p: { [k: string]: number }) { return 1; }",
		"an empty literal":   "function f(p: {}) { return 1; }",
	}
	for name, source := range sources {
		declaration := summaryDeclarationOf(t, source)
		if _, expanded := recordParamMembersOf(declaration.Parameters()[0]); expanded {
			t.Errorf("%s expanded — only a type literal of scalar members may", name)
		}
		entries, ok := SummaryParameterEntries(declaration.Parameters()[0])
		if !ok || len(entries) != 1 || entries[0].Name != "p" {
			t.Errorf("%s: entries = %v (ok %v), want the single whole-name entry", name, entries, ok)
		}
	}
}

// TestSummaryParameterEntries_AnOptionalMemberExpandsWearingMayBeAbsent
// pins the landed widening: an optional member is a WEAKER promise, not
// a missing one, so it contributes its own leaf — sorted by its inner
// annotation — wearing MayBeAbsent, rather than declining the whole
// parameter (scalarMemberListOfIn's doc).
func TestSummaryParameterEntries_AnOptionalMemberExpandsWearingMayBeAbsent(t *testing.T) {
	declaration := summaryDeclarationOf(t, "function f(p: { lo?: number }) { return 1; }")
	entries, ok := SummaryParameterEntries(declaration.Parameters()[0])
	if !ok || len(entries) != 1 || entries[0].Name != "p.lo" || entries[0].Sort != BindingKindNumber {
		t.Fatalf("entries = %v (ok %v), want [{p.lo number}]", entries, ok)
	}
	members, expanded := recordParamMembersOf(declaration.Parameters()[0])
	if !expanded || len(members) != 1 {
		t.Fatalf("members = %v (expanded %v), want the one optional member", members, expanded)
	}
	if !members[0].MayBeAbsent {
		t.Errorf("members[0].MayBeAbsent = false, want true — the member is declared optional")
	}
}

// TestSummaryParameterEntries_AnIndexSignatureIsSkippedBesideAScalarMember
// pins the landed widening: an index signature contributes no leaf and
// kills nothing, so its scalar sibling still expands — the same skip a
// method signature already takes.
func TestSummaryParameterEntries_AnIndexSignatureIsSkippedBesideAScalarMember(t *testing.T) {
	declaration := summaryDeclarationOf(t, "function f(p: { lo: number, [k: string]: number }) { return p.lo; }")
	entries, ok := SummaryParameterEntries(declaration.Parameters()[0])
	if !ok || len(entries) != 1 || entries[0].Name != "p.lo" || entries[0].Sort != BindingKindNumber {
		t.Fatalf("entries = %v (ok %v), want [{p.lo number}]", entries, ok)
	}
}

func TestKernelSummaryDirect_ABodyReadingTheScalarSiblingOfAnIndexSignatureSummarizes(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	declaration := summaryDeclarationOf(t, "function f(p: { lo: number, [k: string]: number }) { return p.lo; }")
	contract := &FunctionContract{Declaration: declaration}
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	argument := recordObject(t, map[string]float64{"lo": 2}, []string{"lo"})
	answer, ok := KernelSummaryDirect(ctx, []abstractdomain.AbstractValue{argument}, contract)
	if !ok {
		t.Fatalf("a body reading the scalar sibling of an index signature declined")
	}
	state, stateOk := StateOfKnown(answer)
	if !stateOk || state.Top {
		t.Fatalf("the answer did not spell as a scalar state: %+v", answer)
	}
	if !kernel.Member(state.Set, []float64{2}) {
		t.Errorf("the summary of f({lo:2}) excludes the true value 2: %+v", state.Set)
	}
}

func TestKernelSummaryDirect_AReadThroughAnIndexSignatureStillDeclines(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	// `p[k]` names no leaf the index signature ever contributes — the
	// skip only frees the scalar sibling, never the indexed read itself
	declaration := summaryDeclarationOf(t,
		"function f(p: { lo: number, [k: string]: number }, k: string) { return p[k]; }")
	contract := &FunctionContract{Declaration: declaration}
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	argument := recordObject(t, map[string]float64{"lo": 2}, []string{"lo"})
	if _, ok := KernelSummaryDirect(ctx, []abstractdomain.AbstractValue{argument, exactNumber(t, 0)}, contract); ok {
		t.Errorf("a read through an index signature summarized — no leaf holds it")
	}
}

// TestSummaryParameterEntriesIn_AComputedStableSymbolMemberExpandsUnderTheSymName
// pins the landed widening: a type element named `[S]: number` under a
// module-level `const S = Symbol()` contributes its leaf under the
// derived `#sym:S` name, beside its scalar sibling `lo`, when the
// reading holds a checker to resolve the const against.
func TestSummaryParameterEntriesIn_AComputedStableSymbolMemberExpandsUnderTheSymName(t *testing.T) {
	ctx, p := namedTypeCtx(t,
		"const S = Symbol();\n"+
			"function f(p: { lo: number, [S]: number }) { return p.lo; }\n")
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

// TestSummaryParameterEntries_AComputedStableSymbolMemberSkipsWithNoChecker
// pins the ctx-less fallback the doc states: with no checker to resolve
// the const against, the computed member SKIPS rather than refusing the
// list — its scalar sibling still expands alone.
func TestSummaryParameterEntries_AComputedStableSymbolMemberSkipsWithNoChecker(t *testing.T) {
	// the ctx-less parse: no checker anywhere to resolve the const
	// against, so the computed member skips even though a checker-backed
	// reading of the very same shape resolves it (the test above)
	declaration := summaryDeclarationOf(t, "function f(p: { lo: number, [S]: number }) { return p.lo; }")
	entries, ok := SummaryParameterEntries(declaration.Parameters()[0])
	if !ok || len(entries) != 1 || entries[0].Name != "p.lo" || entries[0].Sort != BindingKindNumber {
		t.Fatalf("entries = %v (ok %v), want [{p.lo number}] — the computed member skipped", entries, ok)
	}
}

// TestSummaryParameterEntriesIn_ANonStableComputedNameSkipsAndSiblingsExpand
// pins the "any other computed name" branch: a computed name the checker
// cannot pin to a stable symbol const (a plain string-literal computed
// name here) skips exactly like the unresolvable case, and its scalar
// sibling still expands.
func TestSummaryParameterEntriesIn_ANonStableComputedNameSkipsAndSiblingsExpand(t *testing.T) {
	ctx, p := namedTypeCtx(t,
		"function f(p: { lo: number, [\"mid\"]: number }) { return p.lo; }\n")
	declaration := namedTypeFunction(t, p, "f")
	entries, ok := SummaryParameterEntriesIn(ctx, declaration.Parameters()[0])
	if !ok || len(entries) != 1 || entries[0].Name != "p.lo" || entries[0].Sort != BindingKindNumber {
		t.Fatalf("entries = %v (ok %v), want [{p.lo number}] — the non-stable computed member skipped", entries, ok)
	}
}

func TestKernelSummaryDirect_ABodyReadingTheScalarSiblingOfAStableSymbolMemberSummarizes(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ctx, p := namedTypeCtx(t,
		"const S = Symbol();\n"+
			"function f(p: { lo: number, [S]: number }) { return p.lo; }\n")
	declaration := namedTypeFunction(t, p, "f")
	contract := &FunctionContract{Declaration: declaration}
	// the argument must name BOTH declared leaves — p.lo and the
	// #sym:S leaf the computed member now contributes — or the known
	// object is missing a declared member and the call declines
	// (expandedMemberEntryState's three-way rule)
	argument := recordObject(t, map[string]float64{"lo": 2, "#sym:S": 0}, []string{"lo", "#sym:S"})
	answer, ok := KernelSummaryDirect(ctx, []abstractdomain.AbstractValue{argument}, contract)
	if !ok {
		t.Fatalf("a body reading the scalar sibling of a stable-symbol member declined")
	}
	state, stateOk := StateOfKnown(answer)
	if !stateOk || state.Top {
		t.Fatalf("the answer did not spell as a scalar state: %+v", answer)
	}
	if !kernel.Member(state.Set, []float64{2}) {
		t.Errorf("the summary of f({lo:2}) excludes the true value 2: %+v", state.Set)
	}
}

// TestSummaryParameterEntries_ARicherMemberExpandsToItsLenElemPair pins
// the LANDED widening (nestedMemberLeavesOf's array arm, this wave): a
// member whose annotation is an array type (`number[]`) no longer
// contributes a single unknown-sorted leaf — it expands to its own
// "len"/"elem" pair (arrayElementPairMembers), the same two-slot
// vocabulary an array-typed PARAMETER's own element already wears.
func TestSummaryParameterEntries_ARicherMemberExpandsToItsLenElemPair(t *testing.T) {
	declaration := summaryDeclarationOf(t, "function f(p: { lo: number[] }) { return 1; }")
	entries, ok := SummaryParameterEntries(declaration.Parameters()[0])
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

// TestSummaryParameterEntries_ANestedLiteralRecursesIntoTheChildLeaf
// pins the landed NESTED FAMILY widening: a member whose own annotation
// is itself a type literal is no longer a single unknown-sorted leaf —
// it recurses, and the outer member contributes its CHILD's leaves
// under the parent's own key, "p.lo.deep".
func TestSummaryParameterEntries_ANestedLiteralRecursesIntoTheChildLeaf(t *testing.T) {
	declaration := summaryDeclarationOf(t, "function f(p: { lo: { deep: number } }) { return 1; }")
	entries, ok := SummaryParameterEntries(declaration.Parameters()[0])
	if !ok || len(entries) != 1 || entries[0].Name != "p.lo.deep" || entries[0].Sort != BindingKindNumber {
		t.Fatalf("entries = %v (ok %v), want [{p.lo.deep number}]", entries, ok)
	}
}

func TestLowerSummaryBody_ABodyReadingARecordParameterMemberLowersOverTheLeafSlots(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	declaration := summaryDeclarationOf(t,
		"function f(p: { lo: number, hi: number }) { return p.lo + p.hi; }")
	summary, ok := RelowerSummaryBody(&FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration)
	if !ok {
		t.Fatalf("a body reading p.lo / p.hi declined — the leaf slots should carry the reads")
	}
	// ParamCount counts the EXPANDED entries, not the one declared
	// parameter
	if summary.ParamCount != 2 {
		t.Errorf("ParamCount = %d, want 2 — one entry per member", summary.ParamCount)
	}
	// the done flag and the result slot are still the last two of the base
	// vector, so the expansion shifted them without disturbing the
	// convention
	if summary.DoneIndex != summary.RetIndex-1 {
		t.Errorf("DoneIndex %d / RetIndex %d — the flag must sit immediately before the result", summary.DoneIndex, summary.RetIndex)
	}
	if summary.RetIndex >= summary.SlotCount {
		t.Errorf("RetIndex %d outside SlotCount %d", summary.RetIndex, summary.SlotCount)
	}
}

func TestKernelSummaryDirect_ARecordArgumentsFieldsBuildTheLeafEntries(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	declaration := summaryDeclarationOf(t,
		"function f(p: { lo: number, hi: number }) { return p.lo + p.hi; }")
	contract := &FunctionContract{Declaration: declaration}
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	argument := recordObject(t, map[string]float64{"lo": 2, "hi": 5}, []string{"lo", "hi"})
	answer, ok := KernelSummaryDirect(ctx, []abstractdomain.AbstractValue{argument}, contract)
	if !ok {
		t.Fatalf("a record argument declined — its fields spell as scalar entry states")
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

func TestKernelSummaryDirect_ANonObjectArgumentForAnExpandedParameterDeclines(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	declaration := summaryDeclarationOf(t,
		"function f(p: { lo: number, hi: number }) { return p.lo + p.hi; }")
	contract := &FunctionContract{Declaration: declaration}
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	// the entries were laid out expecting the members; a scalar spells
	// none of them
	if _, ok := KernelSummaryDirect(ctx, []abstractdomain.AbstractValue{exactNumber(t, 3)}, contract); ok {
		t.Errorf("a scalar argument filled an expanded parameter's leaf entries")
	}
}

func TestKernelSummaryDirect_AnObjectArgumentMissingADeclaredMemberDeclines(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	declaration := summaryDeclarationOf(t,
		"function f(p: { lo: number, hi: number }) { return p.lo + p.hi; }")
	contract := &FunctionContract{Declaration: declaration}
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	argument := recordObject(t, map[string]float64{"lo": 2}, []string{"lo"})
	if _, ok := KernelSummaryDirect(ctx, []abstractdomain.AbstractValue{argument}, contract); ok {
		t.Errorf("an object without hi filled hi's entry — that leaf has no state to send")
	}
}

func TestKernelSummaryDirect_AWholeRecordParameterUseDeclinesTheBody(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	// `const q = p` stores the record under another name, which the scan
	// does not follow — a write through q would move leaves this body
	// still believes
	declaration := summaryDeclarationOf(t,
		"function f(p: { lo: number }) { const q = p; return q.lo; }")
	contract := &FunctionContract{Declaration: declaration}
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	argument := recordObject(t, map[string]float64{"lo": 2}, []string{"lo"})
	if _, ok := KernelSummaryDirect(ctx, []abstractdomain.AbstractValue{argument}, contract); ok {
		t.Errorf("a whole-p use summarized — the expansion holds no such value")
	}
}

func TestKernelSummaryDirect_ACallArgumentHandOverLowersWithWrittenHavockedLeaves(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	// `g(p)` hands the object over — served now by the havoc-and-
	// write-back reading (recordParameterHandedOver): every leaf goes
	// out Written and joins HandOverHavocNames, and the interior call
	// itself decides completeness — here g is UNRESOLVABLE, so the body
	// lowers POROUS naming the call, never wrongly complete and never
	// the old whole-body decline.
	declaration := summaryDeclarationOf(t,
		"function f(p: { lo: number }) { g(p); return p.lo; }")
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	summary, ok := RelowerSummaryBody(&FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration)
	if !ok {
		outcome, construct, _ := SummaryOutcomeOf(nil, declaration)
		t.Fatalf("a handed-over record no longer lowers at all (%q / %q) — the hand-over serving is the point", outcome, construct)
	}
	outcome, construct, recorded := SummaryOutcomeOf(nil, declaration)
	if !recorded || outcome != SummaryPorous {
		t.Errorf("outcome = %q / construct = %q, want porous — g is unresolvable, so the interior call is the honest hole", outcome, construct)
	}
	row, has := bundleEntryNamed(summary, "p.lo")
	if !has {
		t.Fatalf("no bundle row for p.lo — BundleEntries = %+v", summary.BundleEntries)
	}
	if !row.Written {
		t.Errorf("p.lo Written = false, want true — a handed-over leaf's exit must ride out")
	}
}

func TestKernelSummaryDirect_ADeclaredLeafWriteLowersComplete(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	// `p.lo = 5` writes a DECLARED leaf: the slot takes the assignment,
	// the row goes out Written, and the body is COMPLETE — the
	// class-typed bundle's own treatment, now on the type-literal route
	declaration := summaryDeclarationOf(t,
		"function f(p: { lo: number }): number { p.lo = 5; return p.lo; }")
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	summary, ok := RelowerSummaryBody(&FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration)
	if !ok {
		outcome, construct, _ := SummaryOutcomeOf(nil, declaration)
		t.Fatalf("a declared-leaf write declined (%q / %q)", outcome, construct)
	}
	if outcome, _, recorded := SummaryOutcomeOf(nil, declaration); !recorded || outcome != SummaryComplete {
		t.Errorf("outcome = %q, want complete — every statement lowered", outcome)
	}
	row, has := bundleEntryNamed(summary, "p.lo")
	if !has {
		t.Fatalf("no bundle row for p.lo — BundleEntries = %+v", summary.BundleEntries)
	}
	if !row.Written {
		t.Errorf("p.lo Written = false, want true — the body stores into it")
	}
}

func TestKernelSummaryDirect_AnUndeclaredMemberWriteStillDeclines(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	// `p.mid = 1` writes a member the annotation never declared — no
	// slot holds it, and the refusal stands exactly as before
	declaration := summaryDeclarationOf(t,
		"function f(p: { lo: number }) { p.mid = 1; return p.lo; }")
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	if _, ok := RelowerSummaryBody(&FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration); ok {
		t.Fatalf("an undeclared-member write lowered")
	}
	if outcome, construct, _ := SummaryOutcomeOf(nil, declaration); outcome != SummaryDeclined || construct != "a whole-record parameter use" {
		t.Errorf("outcome = %q / construct = %q, want the standing refusal", outcome, construct)
	}
}

func TestKernelSummaryDirect_ASpreadOfARecordParameterLowersTheBody(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	// `{ ...p, lo: 1 }` COPIES the fields out into a fresh object — the
	// record itself reaches no code that could store into it, so every
	// leaf keeps its value and the body lowers
	declaration := summaryDeclarationOf(t,
		"function f(p: { lo: number, hi: number }) { const q = { ...p, lo: 1 }; return p.hi; }")
	if _, ok := RelowerSummaryBody(&FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration); !ok {
		outcome, construct, _ := SummaryOutcomeOf(nil, declaration)
		t.Fatalf("a spread of a record parameter declined (%q / %q) — a spread reads, it does not store", outcome, construct)
	}
}

func TestKernelSummaryDirect_AReturnOfARecordParameterLowersTheBody(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	// `return p` hands the value out and reads no leaf again; the caller
	// already holds whatever it passed, so nothing here goes stale
	declaration := summaryDeclarationOf(t,
		"function f(p: { lo: number }) { if (p.lo > 0) { return p; } return p; }")
	if _, ok := RelowerSummaryBody(&FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration); !ok {
		outcome, construct, _ := SummaryOutcomeOf(nil, declaration)
		t.Fatalf("a return of a record parameter declined (%q / %q)", outcome, construct)
	}
}

func TestKernelSummaryDirect_AnUndeclaredMemberReadDeclinesTheBody(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	declaration := summaryDeclarationOf(t,
		"function f(p: { lo: number }) { return p.mid; }")
	contract := &FunctionContract{Declaration: declaration}
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	argument := recordObject(t, map[string]float64{"lo": 2}, []string{"lo"})
	if _, ok := KernelSummaryDirect(ctx, []abstractdomain.AbstractValue{argument}, contract); ok {
		t.Errorf("a read of an undeclared member summarized — no slot holds it")
	}
}

func TestKernelSummaryDirect_AClassTypedParameterIsUnchanged(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	// a class name is not a type literal: the parameter keeps its single
	// unknown-sorted slot, and the `p.lo` read finds no slot — exactly
	// today's decline
	declaration := summaryDeclarationOf(t,
		"function f(p: Point) { return p.lo; }")
	contract := &FunctionContract{Declaration: declaration}
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	argument := recordObject(t, map[string]float64{"lo": 2}, []string{"lo"})
	if _, ok := KernelSummaryDirect(ctx, []abstractdomain.AbstractValue{argument}, contract); ok {
		t.Errorf("a class-typed parameter expanded — its instance has methods and aliases the flattening cannot hold")
	}
}

func TestKernelSummaryDirect_AnAsyncBodySummarizesAndAnswersAPromiseOfItsRet(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	// the ret-as-inner convention: the lowered body's #ret holds the
	// SETTLED value (n + 1), and the boundary wraps it — so the body
	// summarizes and the caller's view is a Promise of the return set
	declaration := summaryDeclarationOf(t,
		"async function f(n: number) { return n + 1; }")
	contract := &FunctionContract{Declaration: declaration}
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	answer, ok := KernelSummaryDirect(ctx, []abstractdomain.AbstractValue{exactNumber(t, 1)}, contract)
	if !ok {
		t.Fatalf("an async body declined — the ret-as-inner convention makes it lowerable")
	}
	if answer.Kind != abstractdomain.KindPromise {
		t.Fatalf("async summary answered kind %v, want a Promise wrapper", answer.Kind)
	}
	if answer.Inner == nil {
		t.Fatalf("the Promise carries no inner value")
	}
	state, stateOk := StateOfKnown(*answer.Inner)
	if !stateOk || state.Top {
		t.Fatalf("the promise's inner did not spell as a scalar state: %+v", *answer.Inner)
	}
	// f(1) settles at 2; the inner set must ADMIT it
	if !kernel.Member(state.Set, []float64{2}) {
		t.Errorf("the promise's inner excludes the true settled value 2: %+v", state.Set)
	}
}

func TestKernelSummaryDirect_AGeneratorBodyDeclines(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	// the call's value is an ITERATOR, and no slot spells one — there is
	// no inner value for a boundary wrapper to adopt
	declaration := summaryDeclarationOf(t,
		"function* f(n: number) { yield n + 1; }")
	contract := &FunctionContract{Declaration: declaration}
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	if _, ok := KernelSummaryDirect(ctx, []abstractdomain.AbstractValue{exactNumber(t, 1)}, contract); ok {
		t.Errorf("a generator body summarized — the call's value is an iterator, not the yield set")
	}
}
