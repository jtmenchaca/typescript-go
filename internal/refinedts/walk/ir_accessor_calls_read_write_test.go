// split from ir_accessor_calls_test.go — the getter read and the setter write

package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
)

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
