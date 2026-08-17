// split from ir_summary_call_test.go — receiver threading, the receiver path, and the opaque tier's receiver

package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

/* ── receiver threading ──────────────────────────────────────────── */

// summaryCallOf is the call expression a one-statement source spells.
func summaryCallOf(t *testing.T, source string) *ast.Node {
	t.Helper()
	return callOfStatement(t, summaryCallParse(t, source)[0])
}

// threadedShape is a callee shape carrying this-entries at the given
// indices — what the layout hands the call site, without running a
// lowering. `slots` is the entry count the args vector is sized to.
func threadedShape(slots int, entries []BundleEntry) LoweredSummary {
	return LoweredSummary{SlotCount: slots, BundleEntries: entries}
}

// filledArgs runs the threading over a fresh args/rets pair and answers
// both, so a case reads the fill and the write-back from one call.
func filledArgs(
	t *testing.T,
	context *LoweringContext,
	call *ast.Node,
	shape LoweredSummary,
) ([]kernelbridge.LoopEffect, []int, bool) {
	t.Helper()
	args := make([]kernelbridge.LoopEffect, shape.SlotCount)
	for index := range args {
		args[index] = kernelbridge.AbsentConst()
	}
	rets := make([]int, shape.SlotCount)
	for index := range rets {
		rets[index] = -1
	}
	ok := bundleRetsAndArgs(context, call, shape, args, rets)
	return args, rets, ok
}

func TestSummaryCallStatement_AThisMethodCallFillsTheCalleesThisEntriesFromTheCallersOwnThisSlots(t *testing.T) {
	// `this.bump()` inside a lowered method: the callee's "this.count"
	// entry is the caller's own "this.count" slot
	context := &LoweringContext{
		Bindings: []string{"n", "this.count", "this.limit"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber},
		Typeofs:  []TypeofTag{TypeofTagNumber, TypeofTagNumber, TypeofTagNumber},
	}
	shape := threadedShape(2, []BundleEntry{
		{Path: "this.count", Index: 0},
		{Path: "this.limit", Index: 1},
	})
	args, _, ok := filledArgs(t, context, summaryCallOf(t, `this.bump();`), shape)
	if !ok {
		t.Fatalf("a this-method call declined the threading")
	}
	if args[0].Kind != kernelbridge.LoopEffectVarState || args[0].Index != 1 {
		t.Errorf("entry 0 = %+v, want a whole-state copy of the caller's this.count slot 1", args[0])
	}
	if args[1].Kind != kernelbridge.LoopEffectVarState || args[1].Index != 2 {
		t.Errorf("entry 1 = %+v, want a whole-state copy of the caller's this.limit slot 2", args[1])
	}
}

func TestSummaryCallStatement_ANamedReceiverFillsTheCalleesThisEntriesFromThatNamesSlots(t *testing.T) {
	// `wrapper.get(k)`: the callee's this-fields are the caller's
	// "wrapper.<field>" slots — the same rule, another receiver
	context := &LoweringContext{
		Bindings: []string{"wrapper.metatype", "k"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber},
		Typeofs:  []TypeofTag{TypeofTagNumber, TypeofTagNumber},
	}
	shape := threadedShape(1, []BundleEntry{{Path: "this.metatype", Index: 0}})
	args, _, ok := filledArgs(t, context, summaryCallOf(t, `wrapper.get(k);`), shape)
	if !ok {
		t.Fatalf("a named-receiver method call declined the threading")
	}
	if args[0].Kind != kernelbridge.LoopEffectVarState || args[0].Index != 0 {
		t.Errorf("entry 0 = %+v, want a whole-state copy of wrapper.metatype's slot 0", args[0])
	}
}

func TestSummaryCallStatement_AChainReceiverFillsFromTheFieldsFieldsSlots(t *testing.T) {
	// `this.injector.load(m)`: the receiver PATH is "this.injector", so
	// the callee's this-fields fill from "this.injector.<field>"
	context := &LoweringContext{
		Bindings: []string{"this.injector.depth", "this.injector.name", "m"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindString, BindingKindNumber},
		Typeofs:  []TypeofTag{TypeofTagNumber, TypeofTagString, TypeofTagNumber},
	}
	shape := threadedShape(2, []BundleEntry{
		{Path: "this.depth", Index: 0},
		{Path: "this.name", Index: 1},
	})
	args, _, ok := filledArgs(t, context, summaryCallOf(t, `this.injector.load(m);`), shape)
	if !ok {
		t.Fatalf("a chained receiver declined the threading — one hop is the same prefix rule")
	}
	if args[0].Kind != kernelbridge.LoopEffectVarState || args[0].Index != 0 {
		t.Errorf("entry 0 = %+v, want a whole-state copy of this.injector.depth's slot 0", args[0])
	}
	if args[1].Kind != kernelbridge.LoopEffectVarState || args[1].Index != 1 {
		t.Errorf("entry 1 = %+v, want a whole-state copy of this.injector.name's slot 1", args[1])
	}
}

func TestSummaryCallStatement_AFieldTheCallerHasNoSlotForFillsUnknownAndNeverAbsent(t *testing.T) {
	// the caller knows NOTHING about this.limit — and nothing is what the
	// entry must say. The absent constant would claim the field IS
	// undefined, which is a claim about a value the caller never had.
	context := &LoweringContext{
		Bindings: []string{"this.count"},
		Sorts:    []BindingKind{BindingKindNumber},
		Typeofs:  []TypeofTag{TypeofTagNumber},
	}
	shape := threadedShape(2, []BundleEntry{
		{Path: "this.count", Index: 0},
		{Path: "this.limit", Index: 1},
	})
	args, _, ok := filledArgs(t, context, summaryCallOf(t, `this.bump();`), shape)
	if !ok {
		t.Fatalf("a partially-known bundle declined — an unknown field is fillable")
	}
	if args[0].Kind != kernelbridge.LoopEffectVarState || args[0].Index != 0 {
		t.Errorf("entry 0 = %+v, want a whole-state copy of this.count's slot 0", args[0])
	}
	if args[1].Kind != kernelbridge.LoopEffectUnknown {
		t.Errorf("entry 1 = %+v, want the UNKNOWN effect — absent would claim the field is undefined", args[1])
	}
}

func TestSummaryCallStatement_AWrittenFieldRidesBackThroughRetsWhereTheCallerHasASlot(t *testing.T) {
	context := &LoweringContext{
		Bindings: []string{"this.count"},
		Sorts:    []BindingKind{BindingKindNumber},
		Typeofs:  []TypeofTag{TypeofTagNumber},
	}
	shape := threadedShape(2, []BundleEntry{
		{Path: "this.count", Index: 0, Written: true},
		// written, but the caller holds no slot for it: the write lands
		// nowhere trackable, and nothing lowered can read that spelling
		{Path: "this.limit", Index: 1, Written: true},
	})
	_, rets, ok := filledArgs(t, context, summaryCallOf(t, `this.bump();`), shape)
	if !ok {
		t.Fatalf("a written-field callee declined the threading")
	}
	if rets[0] != 0 {
		t.Errorf("rets[0] = %d, want the caller's this.count slot 0", rets[0])
	}
	if rets[1] != -1 {
		t.Errorf("rets[1] = %d, want -1 — the caller has no slot for this.limit", rets[1])
	}
}

func TestSummaryCallStatement_AnUnwrittenFieldRidesBackNowhere(t *testing.T) {
	// a READ-ONLY entry's exit is the entry state the caller already
	// sent; writing it back would only re-state what the caller knows
	context := &LoweringContext{
		Bindings: []string{"this.count"},
		Sorts:    []BindingKind{BindingKindNumber},
		Typeofs:  []TypeofTag{TypeofTagNumber},
	}
	shape := threadedShape(1, []BundleEntry{{Path: "this.count", Index: 0, Written: false}})
	_, rets, ok := filledArgs(t, context, summaryCallOf(t, `this.bump();`), shape)
	if !ok {
		t.Fatalf("a read-only bundle declined the threading")
	}
	if rets[0] != -1 {
		t.Errorf("rets[0] = %d, want -1 — the body never moved the field", rets[0])
	}
}

func TestSummaryCallStatement_ARecordParameterLeafEntryIsLeftExactlyAsTheArgumentVectorFilledIt(t *testing.T) {
	// the wave-3 mapping is undisturbed: a non-"this." bundle row is a
	// record-parameter leaf the argument walk already filled
	context := &LoweringContext{
		Bindings: []string{"q.lo"},
		Sorts:    []BindingKind{BindingKindNumber},
		Typeofs:  []TypeofTag{TypeofTagNumber},
	}
	shape := threadedShape(2, []BundleEntry{
		{Path: "p.lo", Index: 0},
		{Path: "this.count", Index: 1},
	})
	args := []kernelbridge.LoopEffect{varEffect(0), kernelbridge.AbsentConst()}
	rets := []int{-1, -1}
	if !bundleRetsAndArgs(context, summaryCallOf(t, `this.bump();`), shape, args, rets) {
		t.Fatalf("the threading declined")
	}
	if args[0].Kind != kernelbridge.LoopEffectVar || args[0].Index != 0 {
		t.Errorf("the record leaf's effect = %+v, want the argument walk's own var of slot 0", args[0])
	}
	if rets[0] != -1 {
		t.Errorf("rets[0] = %d, want -1 — a record leaf is never a write-back", rets[0])
	}
}

func TestSummaryCallStatement_AReceiverWithNoSpelledPathDeclinesTheCallStatement(t *testing.T) {
	context := &LoweringContext{
		Bindings: []string{"this.count", "k"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber},
		Typeofs:  []TypeofTag{TypeofTagNumber, TypeofTagNumber},
	}
	shape := threadedShape(1, []BundleEntry{{Path: "this.count", Index: 0}})
	sources := map[string]string{
		"a computed step":  `this.parts[k].load();`,
		"an optional step": `this.injector?.load();`,
		"a call's result":  `pick(k).load();`,
		"a bare callee":    `load(k);`,
	}
	for name, source := range sources {
		if _, _, ok := filledArgs(t, context, summaryCallOf(t, source), shape); ok {
			t.Errorf("%s threaded a this bundle — no slot spelling exists for what it named", name)
		}
	}
}

func TestSummaryCallStatement_ACalleeWithNoThisEntriesNeedsNoReceiverAtAll(t *testing.T) {
	// a plain function's summary carries no this-rows, so a bare `f(1)`
	// site never asks for a receiver path it does not have
	context := &LoweringContext{
		Bindings: []string{"n"},
		Sorts:    []BindingKind{BindingKindNumber},
		Typeofs:  []TypeofTag{TypeofTagNumber},
	}
	shape := threadedShape(1, []BundleEntry{{Path: "p.lo", Index: 0}})
	if _, _, ok := filledArgs(t, context, summaryCallOf(t, `f(n);`), shape); !ok {
		t.Errorf("a bare call declined although the callee has no this-entries to fill")
	}
}

/* ── the receiver path ───────────────────────────────────────────── */

func TestSummaryCallStatement_TheReceiverPathIsTheCalleeExpressionMinusItsMethodStep(t *testing.T) {
	spelled := map[string]string{
		`this.m();`:               "this",
		`wrapper.get(k);`:         "wrapper",
		`this.injector.load(m);`:  "this.injector",
		`a.b.c.d();`:              "a.b.c",
		`(this.injector).load();`: "this.injector",
	}
	for source, want := range spelled {
		got, ok := receiverPathOf(summaryCallOf(t, source))
		if !ok || got != want {
			t.Errorf("receiverPathOf(%q) = %q, %v, want %q", source, got, ok, want)
		}
	}
}

/* ── the opaque tier's receiver ──────────────────────────────────── */

func TestSummaryCallStatement_AThisRootedReceiversBundleSlotsHavocOnTheOpaqueTier(t *testing.T) {
	// the statement enumerator finds a mentioned bundle by IDENTIFIER,
	// and `this` is a keyword — so without this rule an opaque
	// `this.injector.load(m)` left every one of the bundle's slots
	// standing across a call that may write them all
	context := &LoweringContext{
		Bindings: []string{"this.injector.depth", "this.injector.name", "m", "out"},
		Sorts: []BindingKind{
			BindingKindNumber, BindingKindString, BindingKindNumber, BindingKindNumber,
		},
		Typeofs: []TypeofTag{
			TypeofTagNumber, TypeofTagString, TypeofTagNumber, TypeofTagNumber,
		},
	}
	statements := summaryCallParse(t, `out = this.injector.load(m);`)
	lowered, ok := SummaryCallStatementOf(context, statements[0])
	if !ok {
		t.Fatalf("an opaque this-rooted call declined")
	}
	written := map[int]struct{}{}
	for _, statement := range lowered {
		if statement.Kind != kernelbridge.IrStatementAssign ||
			statement.Effect.Kind != kernelbridge.LoopEffectUnknown {
			t.Fatalf("a havoc statement is %+v, want an assign of unknown", statement)
		}
		written[statement.Target] = struct{}{}
	}
	for _, slot := range []int{0, 1, 3} {
		if _, has := written[slot]; !has {
			t.Errorf("slot %d kept its knowledge across an opaque call that may write it (written %v)", slot, written)
		}
	}
	if _, has := written[2]; has {
		t.Errorf("m's slot 2 was havocked — a scalar argument passes by value")
	}
}

func TestSummaryCallStatement_AnIdentifierRootedReceiverIsCoveredWithoutDoubleWriting(t *testing.T) {
	// `wrapper.get(k)` was already covered by the enumerator's mention
	// rule; the receiver reading must not add a second write of the same
	// slot
	context := &LoweringContext{
		Bindings: []string{"wrapper.metatype", "wrapper.instance", "k"},
		Sorts: []BindingKind{
			BindingKindNumber, BindingKindNumber, BindingKindNumber,
		},
		Typeofs: []TypeofTag{
			TypeofTagNumber, TypeofTagNumber, TypeofTagNumber,
		},
	}
	statements := summaryCallParse(t, `wrapper.get(k);`)
	lowered, ok := SummaryCallStatementOf(context, statements[0])
	if !ok {
		t.Fatalf("an opaque named-receiver call declined")
	}
	seen := map[int]int{}
	for _, statement := range lowered {
		seen[statement.Target]++
	}
	if seen[0] != 1 || seen[1] != 1 {
		t.Errorf("the receiver's slots were written %v times each, want once — the two readings agree", seen)
	}
	if len(lowered) != 2 {
		t.Errorf("lowered %d statements, want 2 — the bundle's two slots and nothing else", len(lowered))
	}
}
