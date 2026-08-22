// The widened summary route (KernelSummaryDirect — this port's S5
// completion): a body whose only writes hit locals summarizes
// kernel-side without the effect-free pre-scan, and the declines that
// keep the route honest. Skipped (never a faked pass) when the native
// kernel dylib is absent, the same gate kernel_delegation_test uses.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/parser"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
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
		outcome, construct, _ := SummaryOutcomeOf(declaration)
		t.Fatalf("a handed-over record no longer lowers at all (%q / %q) — the hand-over serving is the point", outcome, construct)
	}
	outcome, construct, recorded := SummaryOutcomeOf(declaration)
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
		outcome, construct, _ := SummaryOutcomeOf(declaration)
		t.Fatalf("a declared-leaf write declined (%q / %q)", outcome, construct)
	}
	if outcome, _, recorded := SummaryOutcomeOf(declaration); !recorded || outcome != SummaryComplete {
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
	if outcome, construct, _ := SummaryOutcomeOf(declaration); outcome != SummaryDeclined || construct != "a whole-record parameter use" {
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
		outcome, construct, _ := SummaryOutcomeOf(declaration)
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
		outcome, construct, _ := SummaryOutcomeOf(declaration)
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

/* ── named-type parameters ───────────────────────────────────────── */

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

/* ── the per-body outcome ────────────────────────────────────────── */

func TestKernelSummaryDirect_ACompleteBodyRecordsComplete(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	declaration := summaryDeclarationOf(t, "function f(n: number) { return n + 1; }")
	if _, ok := RelowerSummaryBody(&FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration); !ok {
		t.Fatalf("a plain arithmetic body declined")
	}
	outcome, construct, recorded := SummaryOutcomeOf(declaration)
	if !recorded {
		t.Fatalf("no outcome recorded — the lowering is the one place the fate is known")
	}
	if outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q), want complete", outcome, construct)
	}
	if construct != "" {
		t.Errorf("a complete body named %q — it has nothing to name", construct)
	}
}

func TestKernelSummaryDirect_ADecliningBodyRecordsTheConstructItRefused(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	// a generator: the call's value is an iterator, which no slot spells
	declaration := summaryDeclarationOf(t, "function* f(n: number) { yield n + 1; }")
	if _, ok := RelowerSummaryBody(&FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration); ok {
		t.Fatalf("a generator body lowered")
	}
	outcome, construct, recorded := SummaryOutcomeOf(declaration)
	if !recorded {
		t.Fatalf("no outcome recorded for a declined body")
	}
	if outcome != SummaryDeclined {
		t.Errorf("outcome = %q, want declined", outcome)
	}
	if construct != "a generator body" {
		t.Errorf("construct = %q, want the construct it refused named", construct)
	}
}

func TestKernelSummaryDirect_ARestParameterLowersAsOneUnknownEntry(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	// a rest parameter binds an ARRAY — always defined, its contents
	// unspellable: one unknown-sorted entry, and the body reads whole
	declaration := summaryDeclarationOf(t, "function f(...rest: number[]) { return 1; }")
	if _, ok := RelowerSummaryBody(&FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration); !ok {
		t.Fatalf("a rest-parameter body declined — one unknown entry spells it")
	}
	outcome, construct, _ := SummaryOutcomeOf(declaration)
	if outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q), want complete", outcome, construct)
	}
}

func TestKernelSummaryDirect_ABindingPatternParameterLowersAsItsBoundNames(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	// `{ lo }` binds ONE local from the argument object's `lo` member: one
	// ordinary scalar entry under the BOUND name, filled at the call sites
	// from the member the entry's Key names
	declaration := summaryDeclarationOf(t, "function f({ lo }: { lo: number }) { return lo; }")
	lowered, ok := RelowerSummaryBody(&FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration)
	if !ok {
		outcome, construct, _ := SummaryOutcomeOf(declaration)
		t.Fatalf("a binding-pattern parameter declined (%q / %q) — its bound names are ordinary entries", outcome, construct)
	}
	if lowered.ParamCount != 1 {
		t.Errorf("ParamCount = %d, want 1 — the one bound name", lowered.ParamCount)
	}
	entries, entriesOk := SummaryParameterEntries(declaration.Parameters()[0])
	if !entriesOk || len(entries) != 1 {
		t.Fatalf("entries = %v (ok %v), want the one bound name", entries, entriesOk)
	}
	if entries[0].Name != "lo" || entries[0].Key != "lo" {
		t.Errorf("entry = %+v, want name lo filled from member lo", entries[0])
	}
	outcome, construct, _ := SummaryOutcomeOf(declaration)
	if outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q), want complete", outcome, construct)
	}
}

func TestKernelSummaryDirect_ARenamedBindingPatternElementFillsFromItsOwnMember(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	// `{ lo: low }` binds `low` from the member `lo` — the slot wears the
	// BOUND name and the Key names the member the call sites read
	declaration := summaryDeclarationOf(t, "function f({ lo: low }: { lo: number, hi: number }) { return low; }")
	if _, ok := RelowerSummaryBody(&FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration); !ok {
		outcome, construct, _ := SummaryOutcomeOf(declaration)
		t.Fatalf("a renamed binding-pattern element declined (%q / %q)", outcome, construct)
	}
	entries, entriesOk := SummaryParameterEntries(declaration.Parameters()[0])
	if !entriesOk || len(entries) != 1 {
		t.Fatalf("entries = %v (ok %v), want the one bound name", entries, entriesOk)
	}
	if entries[0].Name != "low" || entries[0].Key != "lo" {
		t.Errorf("entry = %+v, want name low filled from member lo", entries[0])
	}
}

// TestKernelSummaryDirect_ABindingPatternOverAnArrayMemberTakesATopEntry
// pins the CURRENT shape: `lo`'s own annotation (`number[]`) now expands
// through nestedMemberLeavesOf's array arm to nested "len"/"elem" leaves
// (this wave) rather than answering a single unknown-sorted "lo" leaf —
// so "lo" has no depth-1 row, and the binding-pattern element binds the
// nestedRoots TOP entry instead (ir_summary_parameter_entries.go's
// nestedRoots arm): reads of `lo` answer nothing, which is exactly what
// is known, the same result the old unknown-sorted leaf produced, reached
// by a different route now that the member itself expanded.
func TestKernelSummaryDirect_ABindingPatternOverAnArrayMemberTakesATopEntry(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	declaration := summaryDeclarationOf(t, "function f({ lo }: { lo: number[] }) { return 1; }")
	lowered, ok := RelowerSummaryBody(&FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration)
	if !ok {
		outcome, construct, _ := SummaryOutcomeOf(declaration)
		t.Fatalf("a binding pattern over an array-typed member declined (%q / %q)", outcome, construct)
	}
	if lowered.ParamCount != 1 {
		t.Errorf("ParamCount = %d, want 1 — the one bound name", lowered.ParamCount)
	}
	entries, entriesOk := SummaryParameterEntries(declaration.Parameters()[0])
	if !entriesOk || len(entries) != 1 {
		t.Fatalf("entries = %v (ok %v), want the one bound name", entries, entriesOk)
	}
	if entries[0].Name != "lo" || entries[0].Key != "" || entries[0].Sort != BindingKindUnknown || entries[0].TypeofTag != TypeofTagNone || !entries[0].TopEntry {
		t.Errorf("entry = %+v, want name lo, no Key, sort unknown, typeof none, TopEntry true — lo's member expanded to nested leaves with no depth-1 row", entries[0])
	}
	outcome, construct, _ := SummaryOutcomeOf(declaration)
	t.Logf("array-member binding pattern: outcome=%q construct=%q", outcome, construct)
}

/* ── the `this` bundle ───────────────────────────────────────────── */

// bundleMethodOf parses a source whose first statement is a class and
// answers the method named `text`. The parent link a bundle reads the
// enclosing class through is what the parser sets, so the node comes
// from a real parse rather than being assembled.
func bundleMethodOf(t *testing.T, source string, text string) *ast.Node {
	t.Helper()
	opts := ast.SourceFileParseOptions{FileName: "/m.ts", Path: "/m.ts"}
	file := parser.ParseSourceFile(opts, source, core.ScriptKindTS)
	for _, statement := range file.Statements.Nodes {
		if !ast.IsClassDeclaration(statement) {
			continue
		}
		for _, member := range statement.AsClassDeclaration().Members.Nodes {
			if !ast.IsMethodDeclaration(member) {
				continue
			}
			name := member.Name()
			if name != nil && ast.IsIdentifier(name) && name.Text() == text {
				return member
			}
		}
	}
	t.Fatalf("no method named %s in %q", text, source)
	return nil
}

// bundleEntryNamed is the BundleEntries row spelled under path.
func bundleEntryNamed(summary LoweredSummary, path string) (BundleEntry, bool) {
	for _, entry := range summary.BundleEntries {
		if entry.Path == path {
			return entry, true
		}
	}
	return BundleEntry{}, false
}

// NOTE ON WHAT THESE PROBE. The layout below builds "this.a"-spelled
// entry slots, and the apply side fills them. What does NOT yet resolve
// is the body's own READ of `this.a`: both slot resolvers reject a
// ThisKeyword root — SpelledNameOf (tracked_bindings.go) requires an
// identifier receiver, and propertyPathOf (ir_object_slots.go) requires
// an identifier at the root of the chain. So a method body reading a
// this-field still declines its statement today, and the layout cases
// probe the layout function itself while the apply cases drive the
// entries through the summary the layout produced.

func TestKernelSummaryDirect_AMethodsReadThisFieldsBecomeEntriesAfterTheParameters(t *testing.T) {
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	// a, b are READ and become entries; c is declared and never mentioned,
	// so it takes no entry — only read fields expand
	declaration := bundleMethodOf(t, `
		class Holder {
			a: number;
			b: number;
			c: number;
			load(n: number): number { return this.a + this.b + n; }
		}
	`, "load")
	bundle := thisBundleOf(nil, declaration)
	if !bundle.Expanded {
		t.Fatalf("the bundle did not expand — a, b are read and every mention is a declared field")
	}
	if bundle.Escaped {
		t.Errorf("Escaped = true, want false — nothing carries the receiver out of sight")
	}
	if len(bundle.Entries) != 2 {
		t.Fatalf("entries = %+v, want two — this.a and this.b", bundle.Entries)
	}
	wantName := []string{"this.a", "this.b"}
	for index, entry := range bundle.Entries {
		if entry.Name != wantName[index] {
			t.Errorf("entry %d name = %q, want %q", index, entry.Name, wantName[index])
		}
		if entry.Sort != BindingKindNumber {
			t.Errorf("entry %d sort = %q, want number — the field's own annotation", index, entry.Sort)
		}
	}
	if len(bundle.Written) != 0 {
		t.Errorf("Written = %v, want none — the body only reads", bundle.Written)
	}
}

func TestKernelSummaryDirect_AWrittenThisFieldIsNamedInTheBundleRows(t *testing.T) {
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	// depth is read AND written (a compound write reads the old value), so
	// it carries an entry and the row names it written — that flag is what
	// the call sites map back out through rets
	declaration := bundleMethodOf(t, `
		class Holder {
			depth: number;
			label: number;
			load(): number { this.depth += 1; return this.label; }
		}
	`, "load")
	bundle := thisBundleOf(nil, declaration)
	if !bundle.Expanded {
		t.Fatalf("the bundle did not expand")
	}
	if _, written := bundle.Written["this.depth"]; !written {
		t.Errorf("this.depth is not named written — the body moves it")
	}
	if _, written := bundle.Written["this.label"]; written {
		t.Errorf("this.label is named written — the body only reads it")
	}
}

func TestKernelSummaryDirect_TheBundleRowsRideOutOnTheSummaryWithTheirSlotIndices(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	// the body's own reads do not resolve yet (see the note above), so it
	// lowers by way of the havoc floor — but the LAYOUT still runs, and the
	// rows it built ride out on the summary, which is what the call sites
	// read to map written fields back
	declaration := bundleMethodOf(t, `
		class Holder {
			a: number;
			b: number;
			load(n: number): number { this.a; this.b = 1; return n; }
		}
	`, "load")
	summary, ok := RelowerSummaryBody(&FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration)
	if !ok {
		_, construct, _ := SummaryOutcomeOf(declaration)
		t.Fatalf("the body declined at %q", construct)
	}
	// one declared parameter, then BOTH this-fields: a is read, and the
	// write-only b takes a row too — the row is what its write-back
	// rides (the caller's own b slot kept a stale value across the call
	// when the write had no row; thisBundleOf's write-only-entry comment)
	if summary.ParamCount != 3 {
		t.Fatalf("ParamCount = %d, want 3 — the declared parameter plus the read AND the written this-entries", summary.ParamCount)
	}
	entry, has := bundleEntryNamed(summary, "this.a")
	if !has {
		t.Fatalf("no bundle row for this.a — BundleEntries = %+v", summary.BundleEntries)
	}
	if entry.Index != 1 {
		t.Errorf("this.a index = %d, want 1 — the this-entries lay out AFTER the declared parameters", entry.Index)
	}
	if entry.Written {
		t.Errorf("this.a Written = true, want false — only b is written")
	}
	written, hasWritten := bundleEntryNamed(summary, "this.b")
	if !hasWritten {
		t.Fatalf("no bundle row for the write-only this.b — BundleEntries = %+v", summary.BundleEntries)
	}
	if written.Index != 2 {
		t.Errorf("this.b index = %d, want 2 — after the read entry", written.Index)
	}
	if !written.Written {
		t.Errorf("this.b Written = false, want true — the body stores into it")
	}
}

func TestKernelSummaryDirect_AnEscapingReceiverDeclinesTheExpansionAndGoesPorous(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	// the receiver is handed to a callee the lowering cannot follow, so
	// its slots could move through a name the census never saw. The body
	// still lowers — the this-reads hit the opaque floor — and the outcome
	// is POROUS naming the escape.
	declaration := bundleMethodOf(t, `
		class Holder {
			depth: number;
			load(n: number): number { hand(this); return n; }
		}
	`, "load")
	// the layout refuses the EXPANSION, and says why
	bundle := thisBundleOf(nil, declaration)
	if bundle.Expanded {
		t.Errorf("an escaped bundle expanded — its slots may move through a name the census never saw")
	}
	if !bundle.Escaped {
		t.Fatalf("Escaped = false, want true — `hand(this)` carries the receiver out of sight")
	}
	if len(bundle.Entries) != 0 {
		t.Errorf("entries = %+v, want none", bundle.Entries)
	}
	// and the BODY still lowers, porous, naming the escape
	summary, ok := RelowerSummaryBody(&FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration)
	if !ok {
		_, construct, _ := SummaryOutcomeOf(declaration)
		t.Fatalf("an escaping receiver DECLINED the body at %q — the expansion declines, the body still lowers", construct)
	}
	if len(summary.BundleEntries) != 0 {
		t.Errorf("BundleEntries = %+v, want none — an escaped bundle does not expand", summary.BundleEntries)
	}
	outcome, construct, recorded := SummaryOutcomeOf(declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	if outcome != SummaryPorous {
		t.Errorf("outcome = %q, want porous — the receiver left the lowering's sight", outcome)
	}
	if construct != "this escapes" {
		t.Errorf("construct = %q, want %q — the earliest reason the body stopped being read whole", construct, "this escapes")
	}
}

func TestKernelSummaryDirect_ANonMethodHasNoThisBundle(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	declaration := summaryDeclarationOf(t, "function f(n: number) { return n + 1; }")
	summary, ok := RelowerSummaryBody(&FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration)
	if !ok {
		t.Fatalf("a plain function declined")
	}
	if len(summary.BundleEntries) != 0 {
		t.Errorf("BundleEntries = %+v, want none — only a method has a this bundle", summary.BundleEntries)
	}
	if summary.ParamCount != 1 {
		t.Errorf("ParamCount = %d, want 1", summary.ParamCount)
	}
}

/* ── the apply side fills the this-entries ───────────────────────── */

// thisEntryProbe lowers a method and answers the entry states one
// receiver produces for it — the apply side's own filling, end to end
// from a real declaration's layout.
func thisEntryProbe(
	t *testing.T,
	declaration *ast.Node,
	argKnowns []abstractdomain.AbstractValue,
	receiver abstractdomain.AbstractValue,
) (LoweredSummary, []kernelbridge.KnownStateWire) {
	t.Helper()
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	summary, ok := RelowerSummaryBody(ctx, declaration)
	if !ok {
		_, construct, _ := SummaryOutcomeOf(declaration)
		t.Fatalf("the body declined at %q", construct)
	}
	states, statesOk := summaryEntryStates(ctx, declaration, summary, argKnowns, receiver)
	if !statesOk {
		t.Fatalf("the entry states declined")
	}
	return summary, states
}

func TestKernelSummaryDirect_AReceiversFieldKnowledgeFillsTheThisEntries(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	declaration := bundleMethodOf(t, `
		class Holder {
			a: number;
			b: number;
			load(n: number): number { this.a; this.b; return n; }
		}
	`, "load")
	receiver := recordObject(t, map[string]float64{"a": 2, "b": 5}, []string{"a", "b"})
	summary, states := thisEntryProbe(t, declaration, []abstractdomain.AbstractValue{exactNumber(t, 9)}, receiver)
	if len(states) != summary.ParamCount {
		t.Fatalf("len(states) = %d, want ParamCount %d — one state per entry", len(states), summary.ParamCount)
	}
	// each this-entry took the receiver's own knowledge of that field
	for _, held := range []struct {
		path  string
		value float64
	}{{"this.a", 2}, {"this.b", 5}} {
		entry, has := bundleEntryNamed(summary, held.path)
		if !has {
			t.Errorf("no bundle row for %q", held.path)
			continue
		}
		state := states[entry.Index]
		if state.Top || state.Undef || state.Null {
			t.Errorf("%q entered top=%v undef=%v null=%v, want the receiver's own field state", held.path, state.Top, state.Undef, state.Null)
			continue
		}
		if !kernel.Member(state.Set, []float64{held.value}) {
			t.Errorf("%q entered a set excluding the receiver's %v: %+v", held.path, held.value, state.Set)
		}
	}
}

func TestKernelSummaryDirect_AReceiverWithoutTheFieldFillsThatEntryTop(t *testing.T) {
	SetEngineKernel(kernelDelegationLoadKernel(t))
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	declaration := bundleMethodOf(t, `
		class Holder {
			a: number;
			b: number;
			load(n: number): number { this.a; this.b; return n; }
		}
	`, "load")
	// b is not among the receiver's keys: that entry fills TOP, never
	// absent — an unknown field is unknown, and absent would claim the
	// field is undefined
	receiver := recordObject(t, map[string]float64{"a": 2}, []string{"a"})
	summary, states := thisEntryProbe(t, declaration, []abstractdomain.AbstractValue{exactNumber(t, 9)}, receiver)
	known, hasKnown := bundleEntryNamed(summary, "this.a")
	missing, hasMissing := bundleEntryNamed(summary, "this.b")
	if !hasKnown || !hasMissing {
		t.Fatalf("BundleEntries = %+v, want rows for this.a and this.b", summary.BundleEntries)
	}
	if states[known.Index].Top {
		t.Errorf("this.a entered TOP — the receiver names it")
	}
	if !states[missing.Index].Top {
		t.Errorf("this.b entered top=false, want TOP — the receiver does not name it")
	}
	if states[missing.Index].Undef || states[missing.Index].Null {
		t.Errorf("this.b entered ABSENT — absent would claim the field is undefined, which no receiver said")
	}
}

func TestKernelSummaryDirect_ANonObjectReceiverFillsEveryThisEntryTop(t *testing.T) {
	SetEngineKernel(kernelDelegationLoadKernel(t))
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	declaration := bundleMethodOf(t, `
		class Holder {
			a: number;
			b: number;
			load(n: number): number { this.a; this.b; return n; }
		}
	`, "load")
	// silence names no fields at all; every this-entry fills TOP, which
	// the entry quantifier already covers — sound, less precise
	summary, states := thisEntryProbe(t,
		declaration, []abstractdomain.AbstractValue{exactNumber(t, 1)}, unknownReceiver())
	for _, entry := range summary.BundleEntries {
		if !states[entry.Index].Top {
			t.Errorf("%q entered top=false on an unknown receiver, want TOP", entry.Path)
		}
		if states[entry.Index].Undef || states[entry.Index].Null {
			t.Errorf("%q entered ABSENT on an unknown receiver, want TOP", entry.Path)
		}
	}
	// the DECLARED parameter is untouched by the receiver rule — it still
	// takes its own argument's knowledge
	if states[0].Top {
		t.Errorf("the declared parameter entered TOP — the receiver rule reached past the this-entries")
	}
}

/* ── the serving rule ────────────────────────────────────────────── */

func TestKernelSummaryDirect_ACompleteBlobDeclinesOnATopRet(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	// The body lowers whole — every statement read, nothing havocked — so
	// its outcome is complete. Its return leans on an UNKNOWN-sorted
	// parameter, so the ret comes back TOP, and a complete body that
	// determines NOTHING declines: serving silence would take the call
	// away from the inline walk, which reads this call's own arguments and
	// can determine a value the compiled blob cannot. Completeness alone
	// does not earn the serve; determining something does.
	declaration := summaryDeclarationOf(t, "function f(x) { return x; }")
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	if _, ok := RelowerSummaryBody(ctx, declaration); !ok {
		t.Fatalf("the body declined — it should lower whole")
	}
	outcome, _, _ := SummaryOutcomeOf(declaration)
	if outcome != SummaryComplete {
		t.Fatalf("outcome = %q, want complete — this case is about what a COMPLETE body does", outcome)
	}
	// the ret really does come back TOP for this call — otherwise the case
	// would pass on the ordinary path and prove nothing about the rule
	if !summaryRetIsTop(t, ctx, declaration, []abstractdomain.AbstractValue{silenceValue()}) {
		t.Fatalf("this call's ret is not TOP — the case would not exercise the serving rule")
	}
	contract := &FunctionContract{Declaration: declaration}
	if _, ok := KernelSummaryDirect(ctx, []abstractdomain.AbstractValue{silenceValue()}, contract); ok {
		t.Errorf("a complete blob served a TOP ret — an unknown ret is never served, whatever produced it")
	}
}

// summaryRetIsTop answers whether a call's ret slot really comes back
// TOP — what makes a serving-rule case exercise the rule rather than the
// ordinary path.
func summaryRetIsTop(
	t *testing.T,
	ctx *FlowContext,
	declaration *ast.Node,
	argKnowns []abstractdomain.AbstractValue,
) bool {
	t.Helper()
	summary, ok := LowerSummaryBody(ctx, declaration)
	if !ok {
		t.Fatalf("the body declined")
	}
	blob, hasBlob := SummaryBlobFor(ctx, declaration)
	if !hasBlob {
		t.Fatalf("no blob compiled")
	}
	states, statesOk := summaryEntryStates(ctx, declaration, summary, argKnowns, unknownReceiver())
	if !statesOk {
		t.Fatalf("the entry states declined")
	}
	for len(states) < summary.SlotCount {
		states = append(states, absentState)
	}
	states[summary.DoneIndex] = doneDownState
	exits, exitsOk := kernelbridge.AskApplySummary(blob, states)
	if !exitsOk || summary.RetIndex >= len(exits) {
		t.Fatalf("the apply declined")
	}
	return exits[summary.RetIndex].Top
}

func TestKernelSummaryDirect_APorousBodyIsNeverCompiledAndNeverServes(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	// an unresolvable call HAVOCS rather than declines, so the body lowers
	// POROUS — and a porous answer may be weaker than the inline walk's,
	// so it never serves. Since it never serves, the registry does not
	// pay to compile it either: the lowering runs (the outcome and its
	// construct are recorded), the kernel compile is skipped, and every
	// call takes the inline walk.
	declaration := summaryDeclarationOf(t, "function f(x) { const y = mystery(x); return y; }")
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	if _, ok := RelowerSummaryBody(ctx, declaration); !ok {
		t.Fatalf("the body declined — an opaque call havocs, it does not decline")
	}
	outcome, construct, recorded := SummaryOutcomeOf(declaration)
	if !recorded || outcome != SummaryPorous {
		t.Fatalf("outcome = %q (recorded %v), want porous — the lowering still runs and reports", outcome, recorded)
	}
	if construct == "" {
		t.Errorf("a porous body named no construct — the histogram is the work queue")
	}
	if _, built := SummaryBlobFor(ctx, declaration); built {
		t.Errorf("a porous body compiled a blob — it can never serve, so the compile is pure cost")
	}
	contract := &FunctionContract{Declaration: declaration}
	if _, ok := KernelSummaryDirect(ctx, []abstractdomain.AbstractValue{silenceValue()}, contract); ok {
		t.Errorf("a porous body served — its answer may be weaker than the inline walk's")
	}
}

// silenceValue is an argument nothing is known about — what makes a
// body's return come back TOP.
func silenceValue() abstractdomain.AbstractValue {
	return unknownReceiver()
}

/* ── the collection SKIPS what it cannot lay out ─────────────────── */

// summaryCollectedNames is the slot names a body's LOCALS lay out. The
// lowering's own slot vector is not carried out on LoweredSummary, so
// this re-runs the collection and the layout over the body — the same
// two functions lowerSummaryBodyReporting calls, so the names agree.
func summaryCollectedNames(t *testing.T, declaration *ast.Node) []string {
	t.Helper()
	body := declaration.Body()
	locals, patterns, ok := collectSummaryLocals(body)
	if !ok {
		t.Fatalf("collectSummaryLocals declined — the collection skips, it does not decline")
	}
	var names []string
	for _, slot := range localSlotsOf(nil, body, locals, patterns, map[string]struct{}{}) {
		names = append(names, slot.Name)
	}
	return names
}

// hasName is whether a spelled slot name is among a collected list.
func hasName(names []string, wanted string) bool {
	for _, name := range names {
		if name == wanted {
			return true
		}
	}
	return false
}

// summaryRetExit drives the KERNEL over one call's entry states and
// answers the ret slot's exit — the compiled program's own answer,
// before the serving rule rules on whether the route hands it to a
// caller.
//
// The POROUS bodies below need this: applySummary declines a porous blob
// outright (it may be weaker than the inline walk), so asking
// KernelSummaryDirect would say nothing about whether the statements
// AROUND the havocked one were read. The exit state does say it.
func summaryRetExit(
	t *testing.T,
	ctx *FlowContext,
	declaration *ast.Node,
	argKnowns []abstractdomain.AbstractValue,
) kernelbridge.KnownStateWire {
	t.Helper()
	summary, ok := LowerSummaryBody(ctx, declaration)
	if !ok {
		_, construct, _ := SummaryOutcomeOf(declaration)
		t.Fatalf("the body declined at %q", construct)
	}
	blob, hasBlob := SummaryBlobFor(ctx, declaration)
	if !hasBlob {
		t.Fatalf("no blob compiled")
	}
	states, statesOk := summaryEntryStates(ctx, declaration, summary, argKnowns, unknownReceiver())
	if !statesOk {
		t.Fatalf("the entry states declined")
	}
	for len(states) < summary.SlotCount {
		states = append(states, absentState)
	}
	states[summary.DoneIndex] = doneDownState
	exits, exitsOk := kernelbridge.AskApplySummary(blob, states)
	if !exitsOk || summary.RetIndex >= len(exits) {
		t.Fatalf("the apply declined")
	}
	return exits[summary.RetIndex]
}

func TestKernelSummaryDirect_ANestedArrowIsSkippedNotDeclined(t *testing.T) {
	// `cb` is this body's own local and takes a slot; `inner`, declared
	// INSIDE the arrow, is the arrow's and takes none — collecting it
	// would lay out a slot for a name no statement of this body can read
	declaration := summaryDeclarationOf(t,
		"function f(n: number) { let total = n; const cb = (x: number) => { const inner = x + 1; return inner; }; return total; }")
	names := summaryCollectedNames(t, declaration)
	if !hasName(names, "total") {
		t.Errorf("names = %v, want the enclosing body's `total` collected", names)
	}
	if !hasName(names, "cb") {
		t.Errorf("names = %v, want the bound name `cb` collected — the floor havocs its slot", names)
	}
	if hasName(names, "inner") {
		t.Errorf("names = %v, must NOT hold `inner` — it is the arrow's local, not this body's", names)
	}
}

func TestKernelSummaryDirect_ANestedFunctionDeclarationIsSkipped(t *testing.T) {
	// a function DECLARATION is not a variable declaration, so the name
	// `helper` takes no slot at all — and hoisting is therefore
	// unobservable: nothing lowered can read a name with no slot
	declaration := summaryDeclarationOf(t,
		"function f(n: number) { let total = n; function helper(x: number) { const held = x; return held; } return total; }")
	names := summaryCollectedNames(t, declaration)
	if !hasName(names, "total") {
		t.Errorf("names = %v, want `total` collected", names)
	}
	if hasName(names, "held") {
		t.Errorf("names = %v, must NOT hold `held` — it is helper's local", names)
	}
}

func TestKernelSummaryDirect_ABodyDeclaringANestedArrowLowersPorouslyAndKeepsItsOtherStatements(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	// the arrow's DECLARATION statement is served by the havoc floor —
	// `cb`'s slot takes unknown — and the two arithmetic statements
	// around it are read exactly as they would be without it. Before the
	// skip rule the nested arrow declined this whole body.
	declaration := summaryDeclarationOf(t,
		"function f(n: number) { let s = n + 1; const cb = (x: number) => x + 1; s = s + 1; return s; }")
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	if _, ok := RelowerSummaryBody(ctx, declaration); !ok {
		_, construct, _ := SummaryOutcomeOf(declaration)
		t.Fatalf("a body declaring a nested arrow declined at %q — the arrow is skipped, not declined", construct)
	}
	outcome, construct, recorded := SummaryOutcomeOf(declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	// the closure touches no tracked state — creating it runs nothing,
	// `cb` takes unknown by the function-valued declaration's own rule,
	// and the body is read whole
	if outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q), want complete — an inert closure declaration is read, not floored", outcome, construct)
	}
	// THE OTHER STATEMENTS' KNOWLEDGE SURVIVES. A porous body is never
	// compiled (it can never serve), so the property is read off the
	// LOWERED IR walked directly: the two arithmetic statements are in
	// the statement list, the arrow's declaration is one unknown assign
	// between them, and walking that program from n=2 answers 4.
	lowered, loweredOk := RelowerSummaryBody(ctx, declaration)
	if !loweredOk {
		t.Fatalf("the body declined on the second lowering")
	}
	entries := make([]kernelbridge.KnownStateWire, lowered.SlotCount)
	for i := range entries {
		entries[i] = absentState
	}
	entries[0] = kernelbridge.KnownStateWire{
		Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{2})),
	}
	entries[lowered.DoneIndex] = doneDownState
	exits := kernel.Walk(entries, lowered.Stmts)
	if lowered.RetIndex >= len(exits) {
		t.Fatalf("the walk answered %d states, want more than %d", len(exits), lowered.RetIndex)
	}
	exit := exits[lowered.RetIndex]
	if exit.Top {
		t.Fatalf("the ret exit is TOP — the arithmetic around the arrow was lost")
	}
	if !kernel.Member(exit.Set, []float64{4}) {
		t.Errorf("the ret exit excludes the true value 4: %+v", exit.Set)
	}
}

func TestKernelSummaryDirect_AnArrayBindingPatternLowersWithItsNamesUnknown(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	// `const [a, b] = xs` names two slots. The pattern route reads the
	// statement whole: the bound values have no spelling, so both names
	// take `unknown` BY THE PATTERN'S OWN RULE — the body is read, not
	// floored, and records complete.
	declaration := summaryDeclarationOf(t,
		"function f(n: number) { let s = n + 1; const [a, b] = xs; return s; }")
	names := summaryCollectedNames(t, declaration)
	if !hasName(names, "a") || !hasName(names, "b") {
		t.Fatalf("names = %v, want both bound names collected — each element name is one slot", names)
	}
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	if _, ok := RelowerSummaryBody(ctx, declaration); !ok {
		_, construct, _ := SummaryOutcomeOf(declaration)
		t.Fatalf("an array binding pattern DECLINED the body at %q — its names are collected, the pattern lowers", construct)
	}
	outcome, construct, _ := SummaryOutcomeOf(declaration)
	if outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q), want complete — unknown names are what is true of them, not a hole", outcome, construct)
	}
	// the statement BEFORE the pattern kept its knowledge: f(2) returns 3,
	// read off the lowered IR walked directly.
	lowered, loweredOk := RelowerSummaryBody(ctx, declaration)
	if !loweredOk {
		t.Fatalf("the body declined on the second lowering")
	}
	entries := make([]kernelbridge.KnownStateWire, lowered.SlotCount)
	for i := range entries {
		entries[i] = absentState
	}
	entries[0] = kernelbridge.KnownStateWire{
		Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{2})),
	}
	entries[lowered.DoneIndex] = doneDownState
	exits := kernel.Walk(entries, lowered.Stmts)
	if lowered.RetIndex >= len(exits) {
		t.Fatalf("the walk answered %d states, want more than %d", len(exits), lowered.RetIndex)
	}
	exit := exits[lowered.RetIndex]
	if exit.Top {
		t.Fatalf("the ret exit is TOP — the statement before the pattern was lost")
	}
	if !kernel.Member(exit.Set, []float64{3}) {
		t.Errorf("the ret exit excludes 3: %+v", exit.Set)
	}
}

func TestKernelSummaryDirect_AnArrayPatternsRestAndNestedElementsAreSkipped(t *testing.T) {
	// `head` is a plain identifier and takes a slot; `rest` holds the
	// REMAINDER, which is an array rather than a scalar the vector
	// carries, and `[deep]` binds one level down that no leaf spells —
	// both are skipped, and the statement's havoc still covers whatever
	// slots those names have (none here)
	declaration := summaryDeclarationOf(t,
		"function f(n: number) { const [head, [deep], ...rest] = xs; return n; }")
	names := summaryCollectedNames(t, declaration)
	if !hasName(names, "head") {
		t.Errorf("names = %v, want the plain identifier `head` collected", names)
	}
	if hasName(names, "rest") {
		t.Errorf("names = %v, must NOT hold `rest` — a rest element is not a scalar slot", names)
	}
	if hasName(names, "deep") {
		t.Errorf("names = %v, must NOT hold `deep` — a nested pattern binds a level no leaf spells", names)
	}
}

func TestKernelSummaryDirect_AnArrayPatternsDefaultTakesAnOrdinaryUnknownSlot(t *testing.T) {
	// a DEFAULT is covered by the same havoc: the floor's `unknown` is
	// the whole value claim for that name, and it admits the default as
	// readily as the element
	declaration := summaryDeclarationOf(t,
		"function f(n: number) { const [a = 1, b] = xs; return n; }")
	body := declaration.Body()
	locals, patterns, ok := collectSummaryLocals(body)
	if !ok {
		t.Fatalf("a defaulted array element declined the collection")
	}
	slots := localSlotsOf(nil, body, locals, patterns, map[string]struct{}{})
	for _, wanted := range []string{"a", "b"} {
		found := false
		for _, slot := range slots {
			if slot.Name != wanted {
				continue
			}
			found = true
			if slot.Sort != BindingKindUnknown {
				t.Errorf("%q sort = %q, want unknown — no leaf distinguishes an array position", wanted, slot.Sort)
			}
		}
		if !found {
			t.Errorf("slots = %+v, want a slot named %q", slots, wanted)
		}
	}
}

func TestKernelSummaryDirect_AnObjectPatternsNamesStillWearTheirLeafSorts(t *testing.T) {
	// the object-pattern reading is UNCHANGED by the array widening: its
	// names still read the record's leaves and wear those sorts
	declaration := summaryDeclarationOf(t,
		"function f(n: number) { const p = { lo: 1, hi: 2 }; const { lo, hi } = p; return lo + hi; }")
	body := declaration.Body()
	locals, patterns, ok := collectSummaryLocals(body)
	if !ok {
		t.Fatalf("collectSummaryLocals declined")
	}
	slots := localSlotsOf(nil, body, locals, patterns, map[string]struct{}{})
	for _, wanted := range []string{"lo", "hi"} {
		found := false
		for _, slot := range slots {
			if slot.Name != wanted {
				continue
			}
			found = true
			if slot.Sort != BindingKindNumber {
				t.Errorf("%q sort = %q, want number — the record leaf's own sort", wanted, slot.Sort)
			}
		}
		if !found {
			t.Errorf("slots = %+v, want a slot named %q", slots, wanted)
		}
	}
}

func TestKernelSummaryDirect_ARecognizedMapCallbackSiteIsUnchangedByTheSkip(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	// `ys = xs.map(x => x + 1)` still takes the CALLBACK route — the
	// route reads the arrow NODE at the call site and never asks the
	// collection about it, so skipping the arrow changed nothing here.
	// (The slot layout is built directly, as every callback test does: an
	// array whose uses include `.map` does not flatten from a whole body,
	// which is ir_array_slots.go's rule, not this one's.)
	context := &LoweringContext{
		Bindings:     []string{"xs.len", "xs.elem", "ys.len", "ys.elem"},
		Sorts:        []BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber, BindingKindUnknown},
		Typeofs:      make([]TypeofTag, 4),
		Narrow:       kernel.Narrow,
		Flow:         &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}},
		SummaryTable: &SummaryTableBuilder{},
	}
	statements := loweringParse(t, `ys = xs.map(x => x + 1);`)
	stmts, ok := SummaryCallbackStatementOf(context, statements[0])
	if !ok {
		t.Fatalf("the recognized map site declined — the callback route is unchanged by the skip")
	}
	if len(stmts) != 2 || stmts[1].Kind != kernelbridge.IrStatementCall {
		t.Fatalf("stmts = %+v, want the length copy then the CALL statement", stmts)
	}
	// and the site never havocked: a complete-eligible lowering
	if context.FirstHavoc != "" {
		t.Errorf("FirstHavoc = %q, want empty — the callback route reads the whole site", context.FirstHavoc)
	}
}

func TestKernelSummaryDirect_AnArrowAtACallSiteIsNotCollectedAsThisBodysLocal(t *testing.T) {
	// the other half of the same fact: the collection walks straight past
	// the arrow argument, so `x` (its parameter) and any local it declares
	// take no slot here — the arrow's own summary lays those out
	declaration := summaryDeclarationOf(t,
		"function f(n: number) { let total = n; const ys = xs.map(x => { const bump = x + 1; return bump; }); return total; }")
	names := summaryCollectedNames(t, declaration)
	if !hasName(names, "total") || !hasName(names, "ys") {
		t.Errorf("names = %v, want this body's own `total` and `ys`", names)
	}
	if hasName(names, "bump") {
		t.Errorf("names = %v, must NOT hold `bump` — it is the arrow's local", names)
	}
}

/* ── nested member families (Row 1) ──────────────────────────────── */

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
