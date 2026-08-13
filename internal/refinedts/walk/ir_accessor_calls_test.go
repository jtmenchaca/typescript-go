// Accessors as calls: resolving a property access to its get/set
// declarations, the hoisted getter read, the setter's call statement,
// and the declines that keep each honest.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
)

/* ── the recipe ──────────────────────────────────────────────────── */

// accessorAccessIn is the FIRST `<receiver>.<name>` property access
// spelled inside the named method's body — the node the routes are
// asked to read. A checker-backed program, because the resolution goes
// through symbols.
func accessorAccessIn(t *testing.T, p *program.CheckerProgram, method string, name string) *ast.Node {
	t.Helper()
	var found *ast.Node
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if found != nil || node == nil {
			return true
		}
		if ast.IsPropertyAccessExpression(node) {
			accessed := node.AsPropertyAccessExpression().Name()
			if accessed != nil && ast.IsIdentifier(accessed) && accessed.Text() == name {
				found = node
				return true
			}
		}
		node.ForEachChild(visit)
		return false
	}
	visit(methodNamed(t, p, method).Body())
	if found == nil {
		t.Fatalf("no `.%s` property access in %s's body", name, method)
	}
	return found
}

// accessorDeclaredIn is the get or set accessor named `name` on the
// first class of a checker-backed program — the declaration a resolution
// must answer.
func accessorDeclaredIn(t *testing.T, p *program.CheckerProgram, name string, wantSetter bool) *ast.Node {
	t.Helper()
	for _, statement := range p.Entry.Statements.Nodes {
		if !ast.IsClassDeclaration(statement) {
			continue
		}
		for _, member := range statement.ClassLikeData().Members.Nodes {
			isSetter := ast.IsSetAccessorDeclaration(member)
			if !ast.IsGetAccessorDeclaration(member) && !isSetter {
				continue
			}
			if isSetter != wantSetter {
				continue
			}
			memberName := member.Name()
			if memberName != nil && ast.IsIdentifier(memberName) && memberName.Text() == name {
				return member
			}
		}
	}
	t.Fatalf("no accessor named %s (setter %v)", name, wantSetter)
	return nil
}

// accessorCtx is a checker-backed context with an empty contract
// registry, and the memos cleared — each case parses its own program, so
// a remembered expansion or outcome from another case must not survive
// into it.
func accessorCtx(t *testing.T, source string) (*FlowContext, *program.CheckerProgram) {
	t.Helper()
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, source)
	return &FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}, p
}

// accessorSource is the class every resolution case reads: a backing
// field, a get/set pair over it, and a method that reads and writes the
// accessor.
const accessorSource = "class Box {\n" +
	"  store: number = 0;\n" +
	"  get value(): number { return this.store; }\n" +
	"  set value(v: number) { this.store = v; }\n" +
	"  run(): number { this.value = 3; return this.value; }\n" +
	"}\n"

// loweringAccessorSource is the class the CALL-BUILDING cases read: its
// accessor bodies touch no `this` field, so each lowers to a blob today.
//
// WHY THE TWO SOURCES DIFFER, and it is not cosmetic. thisBundleOf
// (ir_summary_body.go) gives a `this` bundle only to a METHOD
// declaration, so an ACCESSOR's body gets no this-entries: its
// `this.store` read finds no slot, and — because the read sits inside a
// `return`, which havocEnumerable refuses — the body DECLINES rather
// than going porous. Until that layout admits accessors, an accessor
// whose body touches `this` has no blob for these routes to call.
//
// That gap is in a file this agent does not own; it is reported rather
// than worked around. The routes themselves are complete: they build the
// call from whatever LoweredSummary the layout produced, so an accessor
// that lowers today exercises every rule, and an accessor that lowers
// once the layout admits `this` will carry its bundle entries through the
// same fill.
const loweringAccessorSource = "class Box {\n" +
	"  store: number = 0;\n" +
	"  get value(): number { return 7; }\n" +
	"  set value(v: number) { const held = v + 1; }\n" +
	"  run(): number { this.value = 3; return this.value; }\n" +
	"}\n"

/* ── resolution ──────────────────────────────────────────────────── */

func TestAccessorCalls_APropertyAccessResolvesToBothAccessorDeclarations(t *testing.T) {
	ctx, p := accessorCtx(t, accessorSource)
	access := accessorAccessIn(t, p, "run", "value")
	getter, setter, ok := AccessorDeclarationsOf(ctx, access)
	if !ok {
		t.Fatalf("`this.value` did not resolve — its symbol declares a get/set pair")
	}
	if getter != accessorDeclaredIn(t, p, "value", false) {
		t.Errorf("the getter answered %p, want the class's own get accessor node", getter)
	}
	if setter != accessorDeclaredIn(t, p, "value", true) {
		t.Errorf("the setter answered %p, want the class's own set accessor node", setter)
	}
}

func TestAccessorCalls_AGetOnlyPropertyAnswersTheGetterAndNoSetter(t *testing.T) {
	ctx, p := accessorCtx(t, "class Box {\n"+
		"  store: number = 0;\n"+
		"  get value(): number { return this.store; }\n"+
		"  run(): number { return this.value; }\n"+
		"}\n")
	getter, setter, ok := AccessorDeclarationsOf(ctx, accessorAccessIn(t, p, "run", "value"))
	if !ok || getter == nil {
		t.Fatalf("a get-only property did not resolve its getter")
	}
	if setter != nil {
		t.Errorf("a get-only property answered a setter — no such declaration exists")
	}
}

func TestAccessorCalls_AnOrdinaryFieldIsNotAnAccessor(t *testing.T) {
	ctx, p := accessorCtx(t, "class Box {\n"+
		"  store: number = 0;\n"+
		"  run(): number { return this.store; }\n"+
		"}\n")
	if _, _, ok := AccessorDeclarationsOf(ctx, accessorAccessIn(t, p, "run", "store")); ok {
		t.Errorf("a plain field resolved as an accessor — it is a SLOT, and the census owns it")
	}
}

func TestAccessorCalls_AnOptionalStepDeclines(t *testing.T) {
	ctx, p := accessorCtx(t, "class Box {\n"+
		"  store: number = 0;\n"+
		"  get value(): number { return this.store; }\n"+
		"  other: Box | undefined;\n"+
		"  run(): number { return this.other?.value ?? 0; }\n"+
		"}\n")
	// `this.other?.value` may read a property of nothing at all, and no
	// call statement stands for the call that may not have happened
	if _, _, ok := AccessorDeclarationsOf(ctx, accessorAccessIn(t, p, "run", "value")); ok {
		t.Errorf("an optional step resolved — no call statement spells a call that may not run")
	}
}

func TestAccessorCalls_ABodylessAccessorDeclines(t *testing.T) {
	ctx, p := accessorCtx(t, "declare class Box {\n"+
		"  get value(): number;\n"+
		"}\n"+
		"class User {\n"+
		"  b: Box = new Box();\n"+
		"  run(): number { return this.b.value; }\n"+
		"}\n")
	if _, _, ok := AccessorDeclarationsOf(ctx, accessorAccessIn(t, p, "run", "value")); ok {
		t.Errorf("a body-less accessor resolved — there is no code to summarize")
	}
}

func TestAccessorCalls_ANilToleranceHoldsWithoutACheckerOrAContext(t *testing.T) {
	_, p := accessorCtx(t, accessorSource)
	access := accessorAccessIn(t, p, "run", "value")
	for name, ctx := range map[string]*FlowContext{
		"a nil context":       nil,
		"no program":          {Contracts: map[*ast.Symbol]*FunctionContract{}},
		"a program no reader": {P: &program.CheckerProgram{}, Contracts: map[*ast.Symbol]*FunctionContract{}},
	} {
		if _, _, ok := AccessorDeclarationsOf(ctx, access); ok {
			t.Errorf("%s resolved an accessor — nothing there can resolve a symbol", name)
		}
	}
}

func TestAccessorCalls_ANonPropertyAccessDeclines(t *testing.T) {
	ctx, p := accessorCtx(t, accessorSource)
	// a call expression, not a property access: nothing here spells a
	// property name for a symbol to carry accessor declarations under
	if _, _, ok := AccessorDeclarationsOf(ctx, methodNamed(t, p, "run")); ok {
		t.Errorf("a method declaration resolved as a property access")
	}
	if _, _, ok := AccessorDeclarationsOf(ctx, nil); ok {
		t.Errorf("a nil node resolved as a property access")
	}
}

/* ── the receiver path ───────────────────────────────────────────── */

func TestAccessorCalls_TheReceiverPathIsTheAccessMinusItsAccessorStep(t *testing.T) {
	ctx, p := accessorCtx(t, "class Box {\n"+
		"  store: number = 0;\n"+
		"  get value(): number { return this.store; }\n"+
		"  run(): number { return this.value; }\n"+
		"}\n")
	_ = ctx
	path, ok := accessorReceiverPathOf(accessorAccessIn(t, p, "run", "value"))
	if !ok || path != "this" {
		t.Errorf("accessorReceiverPathOf(`this.value`) = %q, %v, want \"this\"", path, ok)
	}
}

func TestAccessorCalls_AChainedReceiverSpellsTheWholePrefix(t *testing.T) {
	_, p := accessorCtx(t, "class Inner {\n"+
		"  store: number = 0;\n"+
		"  get value(): number { return this.store; }\n"+
		"}\n"+
		"class Box {\n"+
		"  holder: Inner = new Inner();\n"+
		"  run(): number { return this.holder.value; }\n"+
		"}\n")
	path, ok := accessorReceiverPathOf(accessorAccessIn(t, p, "run", "value"))
	if !ok || path != "this.holder" {
		t.Errorf("accessorReceiverPathOf(`this.holder.value`) = %q, %v, want \"this.holder\"", path, ok)
	}
}

/* ── the getter read ─────────────────────────────────────────────── */

// accessorLoweringContext is the caller a lowered method presents to the
// accessor routes: slots spelled the way the bundle layout spells them,
// a table to index callees in, a growable slot vector, and the hoist
// permission the read is gated on.
func accessorLoweringContext(ctx *FlowContext, bindings []string, sorts []BindingKind) *LoweringContext {
	typeofs := make([]TypeofTag, len(bindings))
	for index, sort := range sorts {
		switch sort {
		case BindingKindNumber:
			typeofs[index] = TypeofTagNumber
		case BindingKindString:
			typeofs[index] = TypeofTagString
		default:
			typeofs[index] = TypeofTagNone
		}
	}
	context := &LoweringContext{
		Bindings:     append([]string{}, bindings...),
		Sorts:        append([]BindingKind{}, sorts...),
		Typeofs:      typeofs,
		Flow:         ctx,
		SummaryTable: &SummaryTableBuilder{},
		CanHoist:     true,
	}
	context.Allocate = func(name string, sort BindingKind, tag TypeofTag) (int, bool) {
		if len(context.Bindings) >= summarySlotBudget {
			return 0, false
		}
		context.Bindings = append(context.Bindings, name)
		context.Sorts = append(context.Sorts, sort)
		context.Typeofs = append(context.Typeofs, tag)
		return len(context.Bindings) - 1, true
	}
	return context
}

// registerAccessorContracts puts the class's accessors in the contract
// registry, the same rows the production registry holds — the resolution
// reads symbols, but the OVERRIDE gate reads this map.
func registerAccessorContracts(t *testing.T, ctx *FlowContext, p *program.CheckerProgram) {
	t.Helper()
	for _, statement := range p.Entry.Statements.Nodes {
		if !ast.IsClassDeclaration(statement) {
			continue
		}
		for _, member := range statement.ClassLikeData().Members.Nodes {
			if !ast.IsGetAccessorDeclaration(member) && !ast.IsSetAccessorDeclaration(member) {
				continue
			}
			name := member.Name()
			if name == nil {
				continue
			}
			symbol := p.Checker.GetSymbolAtLocation(name)
			if symbol == nil {
				continue
			}
			ctx.Contracts[symbol] = &FunctionContract{Declaration: member}
		}
	}
}

func TestAccessorCalls_AGetterReadHoistsItsCallAndAnswersTheTemp(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ctx, p := accessorCtx(t, loweringAccessorSource)
	registerAccessorContracts(t, ctx, p)
	access := accessorAccessIn(t, p, "run", "value")
	context := accessorLoweringContext(ctx,
		[]string{"this.store"}, []BindingKind{BindingKindNumber})
	before := len(context.Bindings)
	effect, ok := GetterReadEffect(context, access)
	if !ok {
		t.Fatalf("a getter-backed read declined — its body is a summarizable call")
	}
	// the value of the read is a var of the TEMP the call's return landed
	// in, which the allocation grew the slot vector by exactly one for
	if effect.Kind != kernelbridge.LoopEffectVar {
		t.Fatalf("the read answered %+v, want a var of the hoisted call's temp", effect)
	}
	if len(context.Bindings) != before+1 {
		t.Errorf("the slot vector grew by %d, want exactly one temp", len(context.Bindings)-before)
	}
	if effect.Index != before {
		t.Errorf("the read answered slot %d, want the freshly allocated %d", effect.Index, before)
	}
	if len(context.Hoisted) != 1 {
		t.Fatalf("Hoisted = %+v, want exactly the one call statement", context.Hoisted)
	}
	statement := context.Hoisted[0]
	if statement.Kind != kernelbridge.IrStatementCall {
		t.Fatalf("the hoisted statement is %v, want a call — the getter runs a body", statement.Kind)
	}
	// the return rides back into the temp, and nothing else does
	landed := 0
	for index, ret := range statement.Rets {
		if ret == effect.Index {
			landed++
		}
		if ret != -1 && ret != effect.Index {
			t.Errorf("rets[%d] = %d, want the temp %d or -1", index, ret, effect.Index)
		}
	}
	if landed != 1 {
		t.Errorf("the temp is written by %d out-states, want exactly the return's one", landed)
	}
}

func TestAccessorCalls_TheGetterCallsThisEntriesFillFromTheReceiverPathsSlots(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ctx, p := accessorCtx(t, loweringAccessorSource)
	registerAccessorContracts(t, ctx, p)
	getter := accessorDeclaredIn(t, p, "value", false)
	shape, lowered := LowerSummaryBody(ctx, getter)
	if !lowered {
		t.Fatalf("the getter's own body declined to lower")
	}
	context := accessorLoweringContext(ctx,
		[]string{"this.store"}, []BindingKind{BindingKindNumber})
	if _, ok := GetterReadEffect(context, accessorAccessIn(t, p, "run", "value")); !ok {
		t.Fatalf("the getter read declined")
	}
	statement := context.Hoisted[0]
	for _, entry := range shape.BundleEntries {
		field, isThis := thisFieldNameOf(entry.Path)
		if !isThis {
			continue
		}
		filled := statement.Args[entry.Index]
		slot, held := slotIndexOfName(context, "this."+field)
		if held {
			if filled.Kind != kernelbridge.LoopEffectVar || filled.Index != slot {
				t.Errorf("entry %q = %+v, want a var of the caller's slot %d", entry.Path, filled, slot)
			}
			continue
		}
		// a field the caller has no slot for is UNKNOWN, never absent —
		// absent would claim the field IS undefined, a claim no caller made
		if filled.Kind != kernelbridge.LoopEffectUnknown {
			t.Errorf("entry %q = %+v, want the UNKNOWN effect", entry.Path, filled)
		}
	}
}

func TestAccessorCalls_AGetterReadDeclinesWithoutHoistRoom(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ctx, p := accessorCtx(t, loweringAccessorSource)
	registerAccessorContracts(t, ctx, p)
	access := accessorAccessIn(t, p, "run", "value")

	// CanHoist false: the read sits where the lowering has nowhere to put
	// a hoisted statement, and a call must never run on a path the IR does
	// not spell
	noHoist := accessorLoweringContext(ctx, []string{"this.store"}, []BindingKind{BindingKindNumber})
	noHoist.CanHoist = false
	if _, ok := GetterReadEffect(noHoist, access); ok {
		t.Errorf("a getter read hoisted where no statement position exists")
	}
	if len(noHoist.Hoisted) != 0 {
		t.Errorf("a declined read appended %+v to Hoisted", noHoist.Hoisted)
	}

	// no Allocate: the temp the return lands in cannot be made
	noAllocate := accessorLoweringContext(ctx, []string{"this.store"}, []BindingKind{BindingKindNumber})
	noAllocate.Allocate = nil
	if _, ok := GetterReadEffect(noAllocate, access); ok {
		t.Errorf("a getter read lowered with no slot to land its return in")
	}

	// no table: the call's Callee field would index nothing
	noTable := accessorLoweringContext(ctx, []string{"this.store"}, []BindingKind{BindingKindNumber})
	noTable.SummaryTable = nil
	if _, ok := GetterReadEffect(noTable, access); ok {
		t.Errorf("a getter read lowered with no table to index its callee in")
	}

	// no Flow: there is no registry to compile the getter's blob through
	noFlow := accessorLoweringContext(ctx, []string{"this.store"}, []BindingKind{BindingKindNumber})
	noFlow.Flow = nil
	if _, ok := GetterReadEffect(noFlow, access); ok {
		t.Errorf("a getter read lowered with no registry to ask for its blob")
	}
}

func TestAccessorCalls_APlainFieldReadIsNotAGetterRead(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ctx, p := accessorCtx(t, "class Box {\n"+
		"  store: number = 0;\n"+
		"  run(): number { return this.store; }\n"+
		"}\n")
	context := accessorLoweringContext(ctx, []string{"this.store"}, []BindingKind{BindingKindNumber})
	if _, ok := GetterReadEffect(context, accessorAccessIn(t, p, "run", "store")); ok {
		t.Errorf("a plain field read took the getter route — it is a slot, and the slot resolvers own it")
	}
}

/* ── the setter write ────────────────────────────────────────────── */

func TestAccessorCalls_ASetterWriteIsOneCallStatementCarryingTheValueAtEntryZero(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ctx, p := accessorCtx(t, loweringAccessorSource)
	registerAccessorContracts(t, ctx, p)
	context := accessorLoweringContext(ctx,
		[]string{"this.store", "n"}, []BindingKind{BindingKindNumber, BindingKindNumber})
	value := varEffect(1)
	statements, ok := SetterWriteStatements(context, accessorAccessIn(t, p, "run", "value"), value)
	if !ok {
		t.Fatalf("a setter-backed write declined — its body is a summarizable call")
	}
	if len(statements) != 1 {
		t.Fatalf("the write lowered to %d statements, want exactly the one call", len(statements))
	}
	statement := statements[0]
	if statement.Kind != kernelbridge.IrStatementCall {
		t.Fatalf("the write lowered to %v, want a call — the setter runs a body", statement.Kind)
	}
	// the setter's ONE declared parameter is entry 0, and it holds the
	// value the caller lowered
	if statement.Args[0].Kind != kernelbridge.LoopEffectVar || statement.Args[0].Index != 1 {
		t.Errorf("entry 0 = %+v, want the caller's own value effect (a var of slot 1)", statement.Args[0])
	}
	// no ret: a setter's value is discarded by the language, so nothing
	// lands in any caller slot from the RETURN
	shape, lowered := LowerSummaryBody(ctx, accessorDeclaredIn(t, p, "value", true))
	if lowered && shape.RetIndex < len(statement.Rets) && statement.Rets[shape.RetIndex] != -1 {
		t.Errorf("rets[%d] = %d, want -1 — a setter's value goes nowhere",
			shape.RetIndex, statement.Rets[shape.RetIndex])
	}
}

// THE FILL RULES, checked directly on the statement builder. The two
// public routes each build their statement through accessorCallStatement,
// and what it does with a bundle row is the wave-4 rule the call sites
// share: a known field is a var, an unknown one is the UNKNOWN effect,
// and a WRITTEN one rides back into the caller's slot. Handing the
// builder a shape lets the rules be read one at a time, the way
// ir_summary_call_test.go's threadedShape reads bundleRetsAndArgs'.
func TestAccessorCalls_TheBuilderFillsKnownUnknownAndWrittenBundleRowsByTheWaveFourRules(t *testing.T) {
	context := accessorLoweringContext(nil,
		[]string{"this.store", "n"}, []BindingKind{BindingKindNumber, BindingKindNumber})
	// entry 0 is the setter's declared parameter; the bundle rows follow
	shape := LoweredSummary{
		SlotCount: 4,
		DoneIndex: 2,
		RetIndex:  3,
		BundleEntries: []BundleEntry{
			{Path: "this.store", Index: 1, Written: true},
			{Path: "this.absent", Index: 2, Written: true},
		},
	}
	value := varEffect(1)
	statement, built := accessorCallStatement(
		context, shape, kernelbridge.SummaryBlob("b"), &ast.Node{}, "this", &value, -1)
	if !built {
		t.Fatalf("the builder declined a well-shaped accessor callee")
	}
	if statement.Args[0].Kind != kernelbridge.LoopEffectVar || statement.Args[0].Index != 1 {
		t.Errorf("entry 0 = %+v, want the value effect the caller lowered", statement.Args[0])
	}
	// a field the caller HAS: a var of that slot, and its write rides back
	if statement.Args[1].Kind != kernelbridge.LoopEffectVar || statement.Args[1].Index != 0 {
		t.Errorf("the known field's entry = %+v, want a var of the caller's this.store slot 0", statement.Args[1])
	}
	if statement.Rets[1] != 0 {
		t.Errorf("rets[1] = %d, want the caller's this.store slot 0 — the setter moves it", statement.Rets[1])
	}
	// a field the caller has NO slot for: UNKNOWN, never absent — absent
	// would claim the field IS undefined, which no caller said. Its write
	// lands nowhere, because nothing lowered can read that spelling.
	if statement.Args[2].Kind != kernelbridge.LoopEffectUnknown {
		t.Errorf("the unknown field's entry = %+v, want the UNKNOWN effect", statement.Args[2])
	}
	if statement.Rets[2] != -1 {
		t.Errorf("rets[2] = %d, want -1 — the caller holds no slot for that field", statement.Rets[2])
	}
	// no ret: a setter's value is discarded by the language
	if statement.Rets[shape.RetIndex] != -1 {
		t.Errorf("rets[%d] = %d, want -1 for a setter", shape.RetIndex, statement.Rets[shape.RetIndex])
	}
}

func TestAccessorCalls_TheBuilderRefusesARecordParameterLeafRow(t *testing.T) {
	// an accessor's parameter is ONE value, never a record, so a layout
	// carrying a non-"this." bundle row is not this site's shape
	context := accessorLoweringContext(nil, []string{"q.lo"}, []BindingKind{BindingKindNumber})
	shape := LoweredSummary{
		SlotCount:     3,
		DoneIndex:     1,
		RetIndex:      2,
		BundleEntries: []BundleEntry{{Path: "p.lo", Index: 0}},
	}
	if _, built := accessorCallStatement(
		context, shape, kernelbridge.SummaryBlob("b"), &ast.Node{}, "this", nil, -1); built {
		t.Errorf("a record-parameter leaf row built an accessor call — the shapes do not match")
	}
}

func TestAccessorCalls_TheBuilderEntersTheDoneFlagDownAndEverythingElseAbsent(t *testing.T) {
	// the padding must match summaryCallStatement's exactly, or a spliced
	// compile would start with the flag already up
	context := accessorLoweringContext(nil, []string{"this.store"}, []BindingKind{BindingKindNumber})
	shape := LoweredSummary{SlotCount: 3, DoneIndex: 1, RetIndex: 2}
	statement, built := accessorCallStatement(
		context, shape, kernelbridge.SummaryBlob("b"), &ast.Node{}, "this", nil, -1)
	if !built {
		t.Fatalf("the builder declined a bundle-free callee")
	}
	if statement.Args[shape.DoneIndex].Kind != kernelbridge.LoopEffectConst {
		t.Errorf("the done flag entered %+v, want the constant {0}", statement.Args[shape.DoneIndex])
	}
	for index, arg := range statement.Args {
		if index == shape.DoneIndex {
			continue
		}
		if arg.Kind != kernelbridge.AbsentConst().Kind {
			t.Errorf("entry %d = %+v, want the absent constant the apply side's padding sends", index, arg)
		}
	}
}

// The LAYOUT GAP, stated as a case so it fails the day it closes and the
// weave list can be re-read. thisBundleOf admits only a METHOD, so an
// accessor whose body touches `this` gets no bundle: the read finds no
// slot, the enclosing `return` refuses the havoc floor, and the body
// declines. Nothing in this file can fix that — the layout is
// ir_summary_body.go's — and until it does, an accessor over a backing
// field has no blob for these routes to call.
func TestAccessorCalls_AnAccessorBodyTouchingThisStillDeclinesAtTheLayout(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ctx, p := accessorCtx(t, accessorSource)
	registerAccessorContracts(t, ctx, p)
	getter := accessorDeclaredIn(t, p, "value", false)
	if _, lowered := LowerSummaryBody(ctx, getter); lowered {
		t.Skipf("the layout now admits an accessor's `this` bundle — re-read the weave list's accessor-layout note")
	}
	outcome, construct, recorded := SummaryOutcomeOf(getter)
	if !recorded || outcome != SummaryDeclined {
		t.Fatalf("outcome = %q (recorded %v), want declined — the this-read finds no slot", outcome, recorded)
	}
	if construct == "" {
		t.Errorf("the decline named no construct")
	}
	// and the route declines with it, rather than building a call against a
	// callee that has no compiled program
	context := accessorLoweringContext(ctx, []string{"this.store"}, []BindingKind{BindingKindNumber})
	if _, ok := GetterReadEffect(context, accessorAccessIn(t, p, "run", "value")); ok {
		t.Errorf("a blob-less getter built a call statement — there is nothing to splice")
	}
}

func TestAccessorCalls_AGetOnlyPropertysWriteDeclines(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ctx, p := accessorCtx(t, "class Box {\n"+
		"  store: number = 0;\n"+
		"  get value(): number { return this.store; }\n"+
		"  run(): number { return this.value; }\n"+
		"}\n")
	registerAccessorContracts(t, ctx, p)
	context := accessorLoweringContext(ctx, []string{"this.store"}, []BindingKind{BindingKindNumber})
	// there is no setter declaration: the write is a runtime error or a
	// silent no-op, and this route claims neither
	if _, ok := SetterWriteStatements(context, accessorAccessIn(t, p, "run", "value"), constNumber(3)); ok {
		t.Errorf("a get-only property's write lowered to a call — no setter body exists to run")
	}
}

func TestAccessorCalls_ASetterWriteDeclinesWithoutHoistRoom(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ctx, p := accessorCtx(t, loweringAccessorSource)
	registerAccessorContracts(t, ctx, p)
	access := accessorAccessIn(t, p, "run", "value")
	context := accessorLoweringContext(ctx, []string{"this.store"}, []BindingKind{BindingKindNumber})
	context.CanHoist = false
	if _, ok := SetterWriteStatements(context, access, constNumber(3)); ok {
		t.Errorf("a setter write lowered where no statement position exists")
	}
}

/* ── the temp's own reading ──────────────────────────────────────── */

func TestAccessorCalls_TheTempWearsTheGettersDeclaredReturnSort(t *testing.T) {
	_, p := accessorCtx(t, "class Box {\n"+
		"  a: number = 0;\n"+
		"  s: string = \"\";\n"+
		"  get num(): number { return this.a; }\n"+
		"  get word(): string { return this.s; }\n"+
		"  get plain() { return this.a; }\n"+
		"  run(): number { return this.num; }\n"+
		"}\n")
	context := &LoweringContext{}
	for _, held := range []struct {
		name string
		sort BindingKind
		tag  TypeofTag
	}{
		{"num", BindingKindNumber, TypeofTagNumber},
		{"word", BindingKindString, TypeofTagString},
		// an unannotated getter promises nothing about what comes back
		{"plain", BindingKindUnknown, TypeofTagNone},
	} {
		getter := accessorDeclaredIn(t, p, held.name, false)
		if sort := context.AccessorSortOf(getter); sort != held.sort {
			t.Errorf("%s's temp sort = %q, want %q — the declaration's own annotation", held.name, sort, held.sort)
		}
		if tag := context.AccessorTypeofOf(getter); tag != held.tag {
			t.Errorf("%s's temp typeof = %q, want %q", held.name, tag, held.tag)
		}
	}
}

func TestAccessorCalls_ANonGetterHasNoReturnEvidence(t *testing.T) {
	_, p := accessorCtx(t, accessorSource)
	context := &LoweringContext{}
	setter := accessorDeclaredIn(t, p, "value", true)
	if sort := context.AccessorSortOf(setter); sort != BindingKindUnknown {
		t.Errorf("a setter answered the sort %q, want unknown — it returns nothing", sort)
	}
	if sort := context.AccessorSortOf(nil); sort != BindingKindUnknown {
		t.Errorf("a nil declaration answered the sort %q, want unknown", sort)
	}
}
