// split from ir_accessor_calls_test.go — read-modify-write: the compound and the update

package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
)

/* ── read-modify-write: the compound and the update ──────────────── */

// accessorStatementIn is the FIRST statement of the named method's body
// — the node the statement door is asked to read.
func accessorStatementIn(t *testing.T, p *program.CheckerProgram, method string) *ast.Node {
	t.Helper()
	body := methodNamed(t, p, method).Body()
	statements := body.AsBlock().Statements.Nodes
	if len(statements) == 0 {
		t.Fatalf("%s's body has no statements", method)
	}
	return statements[0]
}

// compoundAccessorSource is loweringAccessorSource's class with a
// read-modify-write in every shape the routes claim, each in its own
// method so a case reads exactly one. The accessor bodies touch no
// `this` for the same reason loweringAccessorSource's do: the layout
// gives an accessor no this-bundle, so an accessor that reads a field
// has no blob for these routes to call.
const compoundAccessorSource = "class Box {\n" +
	"  store: number = 0;\n" +
	"  n: number = 0;\n" +
	"  get value(): number { return 7; }\n" +
	"  set value(v: number) { const held = v + 1; }\n" +
	"  plus(): void { this.value += 2; }\n" +
	"  times(): void { this.value *= 2; }\n" +
	"  step(): void { this.value++; }\n" +
	"  back(): void { --this.value; }\n" +
	"  orElse(): void { this.value ||= 2; }\n" +
	"}\n"

// compoundContext is the caller every read-modify-write case presents:
// the class's own slots, a table, a growable slot vector, and the hoist
// permission both halves are gated on.
func compoundContext(ctx *FlowContext) *LoweringContext {
	return accessorLoweringContext(ctx,
		[]string{"this.store", "this.n"},
		[]BindingKind{BindingKindNumber, BindingKindNumber})
}

// compoundStatements is the getter's hoisted call followed by whatever
// the route returned — the order the statement dispatch itself emits
// them in (TakeHoisted flushes the hoists ahead of the route's own
// statements), reassembled here so a case can read the whole
// read-modify-write as one sequence.
func compoundStatements(
	context *LoweringContext,
	written []kernelbridge.IrStatement,
) []kernelbridge.IrStatement {
	out := append([]kernelbridge.IrStatement{}, context.Hoisted...)
	return append(out, written...)
}

func TestAccessorCalls_ACompoundThroughASetterIsAGetterCallTheArithmeticAndASetterCall(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ctx, p := accessorCtx(t, compoundAccessorSource)
	registerAccessorContracts(t, ctx, p)
	context := compoundContext(ctx)
	before := len(context.Bindings)
	written, ok := SetterWriteOf(context, accessorStatementIn(t, p, "plus"))
	if !ok {
		t.Fatalf("`this.value += 2` declined — both accessors have summarizable bodies")
	}
	// the run the language performs: the getter's call, then the setter's
	statements := compoundStatements(context, written)
	if len(statements) != 2 {
		t.Fatalf("the compound lowered to %d statements, want the getter's call then the setter's", len(statements))
	}
	for index, statement := range statements {
		if statement.Kind != kernelbridge.IrStatementCall {
			t.Fatalf("statement %d is %v, want a call — both accessors run bodies", index, statement.Kind)
		}
	}
	// the getter's read landed in exactly one fresh temp
	if len(context.Bindings) != before+1 {
		t.Fatalf("the slot vector grew by %d, want the getter's one temp", len(context.Bindings)-before)
	}
	temp := before
	// the setter's entry 0 is the ARITHMETIC over that temp — the
	// read-modify-write, not the right side alone
	value := statements[1].Args[0]
	if value.Kind != kernelbridge.LoopEffectBinary || value.Op != kernelbridge.LoopOpAdd {
		t.Fatalf("the setter's entry 0 = %+v, want an add — `+=` reads before it writes", value)
	}
	if value.A == nil || value.A.Kind != kernelbridge.LoopEffectVar || value.A.Index != temp {
		t.Errorf("the arithmetic's left = %+v, want a var of the getter's temp %d", value.A, temp)
	}
	if value.B == nil || value.B.Kind != kernelbridge.LoopEffectConst {
		t.Errorf("the arithmetic's right = %+v, want the lowered operand `2`", value.B)
	}
}

func TestAccessorCalls_EachArithmeticCompoundCarriesItsOwnOperator(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	for _, held := range []struct {
		method string
		op     kernelbridge.LoopEffectOp
	}{
		{"plus", kernelbridge.LoopOpAdd},
		{"times", kernelbridge.LoopOpMul},
	} {
		ctx, p := accessorCtx(t, compoundAccessorSource)
		registerAccessorContracts(t, ctx, p)
		context := compoundContext(ctx)
		written, ok := SetterWriteOf(context, accessorStatementIn(t, p, held.method))
		if !ok {
			t.Fatalf("%s's compound declined", held.method)
		}
		value := written[0].Args[0]
		if value.Kind != kernelbridge.LoopEffectBinary || value.Op != held.op {
			t.Errorf("%s's entry 0 = %+v, want the %q the operator spells", held.method, value, held.op)
		}
	}
}

func TestAccessorCalls_AnUpdateThroughASetterIsTheCompoundWithTheConstantOne(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	// prefix and postfix lower the SAME: in a statement position nothing
	// reads the expression's value, and the effect is identical
	for _, held := range []struct {
		method string
		op     kernelbridge.LoopEffectOp
	}{
		{"step", kernelbridge.LoopOpAdd},
		{"back", kernelbridge.LoopOpSub},
	} {
		ctx, p := accessorCtx(t, compoundAccessorSource)
		registerAccessorContracts(t, ctx, p)
		context := compoundContext(ctx)
		before := len(context.Bindings)
		written, ok := SetterWriteOf(context, accessorStatementIn(t, p, held.method))
		if !ok {
			t.Fatalf("%s's update declined — both accessors have summarizable bodies", held.method)
		}
		statements := compoundStatements(context, written)
		if len(statements) != 2 {
			t.Fatalf("%s lowered to %d statements, want the getter's call then the setter's",
				held.method, len(statements))
		}
		value := statements[1].Args[0]
		if value.Kind != kernelbridge.LoopEffectBinary || value.Op != held.op {
			t.Fatalf("%s's entry 0 = %+v, want a %q against the constant one", held.method, value, held.op)
		}
		if value.A == nil || value.A.Kind != kernelbridge.LoopEffectVar || value.A.Index != before {
			t.Errorf("%s's arithmetic reads %+v, want the getter's temp %d", held.method, value.A, before)
		}
		if value.B == nil || value.B.Kind != kernelbridge.LoopEffectConst {
			t.Errorf("%s's step = %+v, want the constant one", held.method, value.B)
		}
	}
}

func TestAccessorCalls_APlainWriteStillLowersToTheSetterCallAlone(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ctx, p := accessorCtx(t, loweringAccessorSource)
	registerAccessorContracts(t, ctx, p)
	context := compoundContext(ctx)
	// `this.value = 3` reads nothing back: the plain write is one call,
	// and the compound route must not have changed it
	written, ok := SetterWriteOf(context, accessorStatementIn(t, p, "run"))
	if !ok {
		t.Fatalf("the plain setter write declined")
	}
	if len(written) != 1 || len(context.Hoisted) != 0 {
		t.Fatalf("the plain write lowered to %d statements with %d hoists, want one call and no read",
			len(written), len(context.Hoisted))
	}
	if written[0].Args[0].Kind != kernelbridge.LoopEffectConst {
		t.Errorf("entry 0 = %+v, want the right side's own constant — a plain write reads nothing back",
			written[0].Args[0])
	}
}

func TestAccessorCalls_AShortCircuitingCompoundDeclinesAndIsNamed(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ctx, p := accessorCtx(t, compoundAccessorSource)
	registerAccessorContracts(t, ctx, p)
	context := compoundContext(ctx)
	// `this.value ||= 2` runs the setter on SOME runs and not others, and
	// one call statement claims it ran on every one
	if _, ok := SetterWriteOf(context, accessorStatementIn(t, p, "orElse")); ok {
		t.Errorf("a short-circuiting compound lowered to a call the run may never make")
	}
	if len(context.Hoisted) != 0 {
		t.Errorf("a declined compound left %+v behind in Hoisted", context.Hoisted)
	}
	if name := DeclinedConstructOf(context); name == "" {
		t.Errorf("the decline named no construct — a refusal must be named, not silent")
	}
}

func TestAccessorCalls_ACompoundThroughAGetOnlyOrSetOnlyPropertyDeclines(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	// a get-only property: the write is a runtime error or a silent no-op.
	// a set-only property: the read answers a property with no getter,
	// whose arithmetic is NaN. Neither is a claim these routes make.
	for name, source := range map[string]string{
		"a get-only property": "class Box {\n" +
			"  get value(): number { return 7; }\n" +
			"  plus(): void { this.value += 2; }\n" +
			"}\n",
		"a set-only property": "class Box {\n" +
			"  set value(v: number) { const held = v + 1; }\n" +
			"  plus(): void { this.value += 2; }\n" +
			"}\n",
	} {
		ctx, p := accessorCtx(t, source)
		registerAccessorContracts(t, ctx, p)
		context := compoundContext(ctx)
		if _, ok := SetterWriteOf(context, accessorStatementIn(t, p, "plus")); ok {
			t.Errorf("%s's compound lowered — the read-modify-write wants BOTH halves", name)
		}
		if len(context.Hoisted) != 0 {
			t.Errorf("%s's declined compound left %+v behind in Hoisted", name, context.Hoisted)
		}
	}
}

func TestAccessorCalls_ACompoundDeclinesWithoutHoistRoom(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ctx, p := accessorCtx(t, compoundAccessorSource)
	registerAccessorContracts(t, ctx, p)
	// the getter's half is gated on CanHoist, and the compound cannot
	// run a read the IR has nowhere to spell
	context := compoundContext(ctx)
	context.CanHoist = false
	if _, ok := SetterWriteOf(context, accessorStatementIn(t, p, "plus")); ok {
		t.Errorf("a compound lowered where no statement position exists for its read")
	}
	if len(context.Hoisted) != 0 {
		t.Errorf("a declined compound appended %+v to Hoisted", context.Hoisted)
	}
}

func TestAccessorCalls_ACompoundOverAPlainFieldIsNotThisRoutes(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ctx, p := accessorCtx(t, "class Box {\n"+
		"  store: number = 0;\n"+
		"  plus(): void { this.store += 2; }\n"+
		"  step(): void { this.store++; }\n"+
		"}\n")
	registerAccessorContracts(t, ctx, p)
	for _, method := range []string{"plus", "step"} {
		context := compoundContext(ctx)
		if _, ok := SetterWriteOf(context, accessorStatementIn(t, p, method)); ok {
			t.Errorf("%s's compound over a plain FIELD took the accessor route — it is a slot, "+
				"and the slot assignment owns it", method)
		}
	}
}
