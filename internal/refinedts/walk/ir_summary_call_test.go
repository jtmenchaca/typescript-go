// The call statement: a site whose callee has a compiled summary
// lowers to IrStatementCall rather than inlining the callee's body, and
// the table the body carries names each callee once.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/parser"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
)

func TestSummaryTableBuilder_ACalleeIsTabledOnceAndReusedAfterwards(t *testing.T) {
	builder := &SummaryTableBuilder{}
	first := &ast.Node{}
	second := &ast.Node{}
	if index := builder.CalleeIndex(first, kernelbridge.SummaryBlob("one")); index != 0 {
		t.Errorf("first callee index = %d, want 0", index)
	}
	if index := builder.CalleeIndex(second, kernelbridge.SummaryBlob("two")); index != 1 {
		t.Errorf("second callee index = %d, want 1", index)
	}
	if index := builder.CalleeIndex(first, kernelbridge.SummaryBlob("one")); index != 0 {
		t.Errorf("the first callee's second ask = %d, want 0 — a callee is tabled once", index)
	}
	if len(builder.Blobs) != 2 {
		t.Errorf("len(table) = %d, want 2", len(builder.Blobs))
	}
	if builder.Blobs[0] != "one" || builder.Blobs[1] != "two" {
		t.Errorf("table = %v, want [one two]", builder.Blobs)
	}
}

// summaryCallParse parses a source whose statements the lowering reads
// directly — no checker involved.
func summaryCallParse(t *testing.T, source string) []*ast.Node {
	t.Helper()
	opts := ast.SourceFileParseOptions{FileName: "/call.ts", Path: "/call.ts"}
	file := parser.ParseSourceFile(opts, source, core.ScriptKindTS)
	return file.Statements.Nodes
}

func TestSummaryCallStatement_WithoutAFlowContextTheCallStatementRouteDeclinesAndTheHavocFloorServes(t *testing.T) {
	// no registry to ask, so no CALL STATEMENT can be built; the door's
	// third tier still lowers the site as havoc
	context := &LoweringContext{
		Bindings:     []string{"x"},
		Sorts:        []BindingKind{BindingKindNumber},
		Typeofs:      []TypeofTag{TypeofTagNumber},
		SummaryTable: &SummaryTableBuilder{},
	}
	statements := summaryCallParse(t, `x = g(1);`)
	if _, ok := summaryCallStatement(context, callOfStatement(t, statements[0]), 0); ok {
		t.Errorf("a call statement was built without a flow context — there is no registry to answer for the callee")
	}
	lowered, ok := SummaryCallStatementOf(context, statements[0])
	if !ok {
		t.Fatalf("the door declined whole — the opaque tier lowers an unresolvable callee")
	}
	if len(lowered) != 1 || lowered[0].Kind != kernelbridge.IrStatementAssign ||
		lowered[0].Target != 0 || lowered[0].Effect.Kind != kernelbridge.LoopEffectUnknown {
		t.Errorf("lowered = %+v, want one `x := unknown` — the havoc floor's whole claim", lowered)
	}
}

func TestSummaryCallStatement_WithoutATableTheCallStatementRouteDeclines(t *testing.T) {
	// the table is where a callee's blob is indexed from; with none, the
	// call has no Callee field to name
	context := &LoweringContext{
		Bindings: []string{"x"},
		Sorts:    []BindingKind{BindingKindNumber},
		Typeofs:  []TypeofTag{TypeofTagNumber},
		Flow:     &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}},
	}
	statements := summaryCallParse(t, `x = g(1);`)
	if _, ok := summaryCallStatement(context, callOfStatement(t, statements[0]), 0); ok {
		t.Errorf("a call statement was built without a table — its Callee field would index nothing")
	}
}

// callOfStatement is the call expression a `x = f(…)` statement stands
// on — what the tier tests hand to the routes directly.
func callOfStatement(t *testing.T, statement *ast.Node) *ast.Node {
	t.Helper()
	e := Unwrapped(statement.AsExpressionStatement().Expression)
	if ast.IsCallExpression(e) {
		return e
	}
	return Unwrapped(e.AsBinaryExpression().Right)
}

/* ── record arguments ────────────────────────────────────────────── */

// recordParameterOf parses a one-parameter declaration and answers the
// members its annotation expands to.
func recordParameterOf(t *testing.T, source string) []recordParamMember {
	t.Helper()
	opts := ast.SourceFileParseOptions{FileName: "/p.ts", Path: "/p.ts"}
	file := parser.ParseSourceFile(opts, source, core.ScriptKindTS)
	declaration := file.Statements.Nodes[0]
	members, ok := recordParamMembersOf(declaration.Parameters()[0])
	if !ok {
		t.Fatalf("the parameter of %q did not expand", source)
	}
	return members
}

// firstExpressionOf is the expression a one-statement source spells.
func firstExpressionOf(t *testing.T, source string) *ast.Node {
	t.Helper()
	statements := summaryCallParse(t, source)
	return Unwrapped(statements[0].AsExpressionStatement().Expression)
}

func TestRecordArgumentEffects_AnObjectLiteralArgumentLowersEachMemberByName(t *testing.T) {
	members := recordParameterOf(t, "function g(p: { lo: number, hi: number }) { return p.lo; }")
	context := &LoweringContext{
		Bindings: []string{"n"},
		Sorts:    []BindingKind{BindingKindNumber},
		Typeofs:  []TypeofTag{TypeofTagNumber},
	}
	// the keys are spelled in the OTHER order — the effects must still
	// come out in the parameter's member order
	effects, ok := recordArgumentEffects(context, members, firstExpressionOf(t, `({ hi: n, lo: 4 });`))
	if !ok {
		t.Fatalf("an object literal of exactly the members declined")
	}
	if len(effects) != 2 {
		t.Fatalf("len(effects) = %d, want 2", len(effects))
	}
	// effect 0 fills "p.lo" — the literal 4
	if effects[0].Kind != kernelbridge.LoopEffectConst {
		t.Errorf("effect 0 kind = %v, want the constant 4 that lo was given", effects[0].Kind)
	}
	// effect 1 fills "p.hi" — a var of n's slot
	if effects[1].Kind != kernelbridge.LoopEffectVar || effects[1].Index != 0 {
		t.Errorf("effect 1 = %+v, want a var of slot 0 (n)", effects[1])
	}
}

func TestRecordArgumentEffects_AFlattenedRecordLocalArgumentLowersEachLeafSlotsVar(t *testing.T) {
	members := recordParameterOf(t, "function g(p: { lo: number, hi: number }) { return p.lo; }")
	context := &LoweringContext{
		Bindings: []string{"q.hi", "q.lo"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber},
		Typeofs:  []TypeofTag{TypeofTagNumber, TypeofTagNumber},
	}
	effects, ok := recordArgumentEffects(context, members, firstExpressionOf(t, `(q);`))
	if !ok {
		t.Fatalf("a flattened record local of exactly these leaves declined")
	}
	// member order, not slot order: lo first, though its slot is 1
	if len(effects) != 2 {
		t.Fatalf("len(effects) = %d, want 2", len(effects))
	}
	if effects[0].Kind != kernelbridge.LoopEffectVar || effects[0].Index != 1 {
		t.Errorf("effect 0 = %+v, want a var of q.lo's slot 1", effects[0])
	}
	if effects[1].Kind != kernelbridge.LoopEffectVar || effects[1].Index != 0 {
		t.Errorf("effect 1 = %+v, want a var of q.hi's slot 0", effects[1])
	}
}

func TestRecordArgumentEffects_TheWrongShapedArgumentsDecline(t *testing.T) {
	members := recordParameterOf(t, "function g(p: { lo: number, hi: number }) { return p.lo; }")
	context := &LoweringContext{
		Bindings: []string{"n", "r.lo"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber},
		Typeofs:  []TypeofTag{TypeofTagNumber, TypeofTagNumber},
	}
	sources := map[string]string{
		"a literal missing a member": `({ lo: 1 });`,
		"a literal with an extra":    `({ lo: 1, hi: 2, mid: 3 });`,
		"a literal with a spread":    `({ ...n, lo: 1, hi: 2 });`,
		"a shorthand row":            `({ lo, hi });`,
		"a computed key":             `({ [n]: 1, hi: 2 });`,
		"a scalar local":             `(n);`,
		"a record of other leaves":   `(r);`,
		"a call result":              `(h(1));`,
	}
	for name, source := range sources {
		if _, ok := recordArgumentEffects(context, members, firstExpressionOf(t, source)); ok {
			t.Errorf("%s filled the leaf entries — only an exact literal or an exact flattened local may", name)
		}
	}
}

func TestSummaryCallStatement_ANonCallStatementDeclines(t *testing.T) {
	context := &LoweringContext{
		Bindings:     []string{"x", "y"},
		Sorts:        []BindingKind{BindingKindNumber, BindingKindNumber},
		Typeofs:      []TypeofTag{TypeofTagNumber, TypeofTagNumber},
		Flow:         &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}},
		SummaryTable: &SummaryTableBuilder{},
	}
	statements := summaryCallParse(t, `x = y + 1;`)
	if _, ok := SummaryCallStatementOf(context, statements[0]); ok {
		t.Errorf("an ordinary assignment took the call route")
	}
}

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
	if args[0].Kind != kernelbridge.LoopEffectVar || args[0].Index != 1 {
		t.Errorf("entry 0 = %+v, want a var of the caller's this.count slot 1", args[0])
	}
	if args[1].Kind != kernelbridge.LoopEffectVar || args[1].Index != 2 {
		t.Errorf("entry 1 = %+v, want a var of the caller's this.limit slot 2", args[1])
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
	if args[0].Kind != kernelbridge.LoopEffectVar || args[0].Index != 0 {
		t.Errorf("entry 0 = %+v, want a var of wrapper.metatype's slot 0", args[0])
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
	if args[0].Kind != kernelbridge.LoopEffectVar || args[0].Index != 0 {
		t.Errorf("entry 0 = %+v, want a var of this.injector.depth's slot 0", args[0])
	}
	if args[1].Kind != kernelbridge.LoopEffectVar || args[1].Index != 1 {
		t.Errorf("entry 1 = %+v, want a var of this.injector.name's slot 1", args[1])
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
	if args[0].Kind != kernelbridge.LoopEffectVar || args[0].Index != 0 {
		t.Errorf("entry 0 = %+v, want a var of this.count's slot 0", args[0])
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

/* ── end to end, through the kernel ──────────────────────────────── */

// methodNamed is the method `text` of the first class declaration in a
// checker-backed program.
func methodNamed(t *testing.T, p *program.CheckerProgram, text string) *ast.Node {
	t.Helper()
	for _, statement := range p.Entry.Statements.Nodes {
		if !ast.IsClassDeclaration(statement) {
			continue
		}
		for _, member := range statement.ClassLikeData().Members.Nodes {
			if !ast.IsMethodDeclaration(member) {
				continue
			}
			name := member.Name()
			if name != nil && ast.IsIdentifier(name) && name.Text() == text {
				return member
			}
		}
	}
	t.Fatalf("no method named %s", text)
	return nil
}

func TestSummaryCallStatement_AWrittenFieldExitRidesRetsBackIntoTheCallersSlotThroughTheKernel(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	// `bump` READS and WRITES this.count, so its own layout gives that
	// field an entry the census marks Written. A caller that calls it on
	// `this` fills the entry from its own this.count slot, and the exit
	// rides back into that same slot.
	p := entryEnvTestProgram(t,
		"class Counter {\n"+
			"  count: number = 0;\n"+
			"  bump(): number { this.count = this.count + 1; return this.count; }\n"+
			"  run(): number { this.bump(); return this.count; }\n"+
			"}\n")
	ctx := &FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}
	bump := methodNamed(t, p, "bump")
	// the callee resolves only through a REGISTERED contract — the same
	// row the production registry holds, which is what ContractOf reads
	bumpSymbol := p.Checker.GetSymbolAtLocation(bump.Name())
	if bumpSymbol == nil {
		t.Fatalf("bump's declaration has no symbol to register a contract under")
	}
	ctx.Contracts[bumpSymbol] = &FunctionContract{Declaration: bump}

	// the callee's OWN LAYOUT is what the call site reads — one row per
	// this-field, Written taken from the census's write set
	bundle := thisBundleOf(ctx, bump)
	if !bundle.Expanded {
		t.Fatalf("the callee's this bundle did not expand — its read fields are the entries")
	}
	countEntry, foundEntry := bundleRowOf(bundleRowsOf(bump, bundle), "this.count")
	if !foundEntry {
		t.Fatalf("the callee's layout gave this.count no bundle row")
	}
	if !countEntry.Written {
		t.Fatalf("the callee's this.count row is not Written although the body assigns it")
	}

	// the CALLER's slots, spelled as the layout spells a this bundle. The
	// site resolves them BY SPELLING, so this is the caller a lowered
	// `run` presents to the door.
	context := &LoweringContext{
		Bindings:      []string{"this.count", "#done", "#ret"},
		Sorts:         []BindingKind{BindingKindNumber, BindingKindNumber, BindingKindUnknown},
		Typeofs:       []TypeofTag{TypeofTagNumber, TypeofTagNumber, TypeofTagNone},
		Narrow:        kernel.Narrow,
		Result:        &LoweringResult{Done: 1, Ret: 2},
		Flow:          ctx,
		SummaryTable:  &SummaryTableBuilder{},
		ResolveCallee: func(callee *ast.Node) *ast.Node { return bump },
	}
	call := callInMethodBody(t, p, "run")
	shape := LoweredSummary{
		SlotCount:     len(bundle.Entries) + 2,
		BundleEntries: bundleRowsOf(bump, bundle),
	}
	args := make([]kernelbridge.LoopEffect, shape.SlotCount)
	for index := range args {
		args[index] = kernelbridge.AbsentConst()
	}
	rets := make([]int, shape.SlotCount)
	for index := range rets {
		rets[index] = -1
	}
	if !bundleRetsAndArgs(context, call, shape, args, rets) {
		t.Fatalf("the threading declined at a `this.bump()` site")
	}
	if countEntry.Index >= len(args) {
		t.Fatalf("the call carries %d entries, too few for the callee's entry %d", len(args), countEntry.Index)
	}
	filled := args[countEntry.Index]
	if filled.Kind != kernelbridge.LoopEffectVar || filled.Index != 0 {
		t.Errorf("the callee's this.count entry = %+v, want a var of the caller's own this.count slot 0", filled)
	}
	if rets[countEntry.Index] != 0 {
		t.Errorf("rets[%d] = %d, want the caller's this.count slot 0 — the write must ride back",
			countEntry.Index, rets[countEntry.Index])
	}

	// and the DOOR: until the callee's own `this.x = e` statements lower
	// (the layout's other half), the site takes the opaque tier — which
	// must still havoc the receiver's bundle rather than leave the
	// caller's this.count standing across a call that writes it
	lowered, ok := SummaryCallOrHavoc(context, call, -1)
	if !ok {
		t.Fatalf("the call site declined whole")
	}
	if lowered[0].Kind == kernelbridge.IrStatementCall {
		// the callee lowers now: the door's own statement must carry the
		// same threading the direct call above produced
		if lowered[0].Args[countEntry.Index].Kind != kernelbridge.LoopEffectVar ||
			lowered[0].Args[countEntry.Index].Index != 0 {
			t.Errorf("the door's call entry = %+v, want the caller's this.count slot 0", lowered[0].Args[countEntry.Index])
		}
		if lowered[0].Rets[countEntry.Index] != 0 {
			t.Errorf("the door's rets[%d] = %d, want 0", countEntry.Index, lowered[0].Rets[countEntry.Index])
		}
		return
	}
	havocked := false
	for _, statement := range lowered {
		if statement.Kind == kernelbridge.IrStatementAssign && statement.Target == 0 &&
			statement.Effect.Kind == kernelbridge.LoopEffectUnknown {
			havocked = true
		}
	}
	if !havocked {
		t.Errorf("the opaque tier left this.count standing across a call that writes it: %+v", lowered)
	}
}

// bundleRowsOf is the bundle rows a method's this bundle contributes:
// one per read field, indexed after the declared parameters, Written
// taken from the census's own write set — the layout's own arithmetic,
// restated here only because the test reads the callee side directly.
func bundleRowsOf(method *ast.Node, bundle thisBundleLayout) []BundleEntry {
	rows := make([]BundleEntry, 0, len(bundle.Entries))
	for index, entry := range bundle.Entries {
		_, written := bundle.Written[entry.Name]
		rows = append(rows, BundleEntry{
			Path:    entry.Name,
			Index:   len(method.Parameters()) + index,
			Written: written,
		})
	}
	return rows
}

// bundleRowOf is one bundle row by its slot spelling.
func bundleRowOf(entries []BundleEntry, spelled string) (BundleEntry, bool) {
	for _, entry := range entries {
		if entry.Path == spelled {
			return entry, true
		}
	}
	return BundleEntry{}, false
}

// callInMethodBody is the first call expression in the named method's
// body — the site the door is asked to lower.
func callInMethodBody(t *testing.T, p *program.CheckerProgram, method string) *ast.Node {
	t.Helper()
	var found *ast.Node
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if found != nil || node == nil {
			return true
		}
		if ast.IsCallExpression(node) {
			found = node
			return true
		}
		node.ForEachChild(visit)
		return false
	}
	visit(methodNamed(t, p, method).Body())
	if found == nil {
		t.Fatalf("no call expression in %s's body", method)
	}
	return found
}
