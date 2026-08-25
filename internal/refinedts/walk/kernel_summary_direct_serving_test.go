// The widened summary route's SERVING RULE (a complete or porous body
// only hands its answer to a caller when the answer determines
// something, and a porous body never compiles at all) and the
// COLLECTION's own local-skip rules (a nested arrow/function/rest/
// binding-pattern element that has no leaf in this body's own slot
// vector is SKIPPED, never declined). See kernel_summary_direct_test.go's
// header for the sibling map; this file owns summaryRetIsTop,
// silenceValue, summaryCollectedNames, hasName, and summaryRetExit.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

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
	outcome, _, _ := SummaryOutcomeOf(nil, declaration)
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
	outcome, construct, recorded := SummaryOutcomeOf(nil, declaration)
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
	outcome, construct, recorded := SummaryOutcomeOf(nil, declaration)
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
	outcome, construct, recorded := SummaryOutcomeOf(nil, declaration)
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
	outcome, construct, _ := SummaryOutcomeOf(nil, declaration)
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
		outcome, construct, _ := SummaryOutcomeOf(nil, declaration)
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
	outcome, construct, _ := SummaryOutcomeOf(nil, declaration)
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
		outcome, construct, _ := SummaryOutcomeOf(nil, declaration)
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
		outcome, construct, _ := SummaryOutcomeOf(nil, declaration)
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
	outcome, construct, _ := SummaryOutcomeOf(nil, declaration)
	t.Logf("array-member binding pattern: outcome=%q construct=%q", outcome, construct)
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
		_, construct, _ := SummaryOutcomeOf(nil, declaration)
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
		_, construct, _ := SummaryOutcomeOf(nil, declaration)
		t.Fatalf("a body declaring a nested arrow declined at %q — the arrow is skipped, not declined", construct)
	}
	outcome, construct, recorded := SummaryOutcomeOf(nil, declaration)
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
		_, construct, _ := SummaryOutcomeOf(nil, declaration)
		t.Fatalf("an array binding pattern DECLINED the body at %q — its names are collected, the pattern lowers", construct)
	}
	outcome, construct, _ := SummaryOutcomeOf(nil, declaration)
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
