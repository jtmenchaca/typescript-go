// split from ir_accessor_calls_test.go — the setter in expression position

package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
)

/* ── the setter in expression position ───────────────────────────── */

// expressionSetterSource puts a setter write where its VALUE is read:
// as a call argument, and as the right side of another assignment.
const expressionSetterSource = "class Box {\n" +
	"  store: number = 0;\n" +
	"  n: number = 0;\n" +
	"  get value(): number { return 7; }\n" +
	"  set value(v: number) { const held = v + 1; }\n" +
	"  take(x: number): number { return x; }\n" +
	"  passed(): void { this.take(this.value = 3); }\n" +
	"  held(): void { const y = (this.value = 3); }\n" +
	"}\n"

// setterAssignmentIn is the `this.value = 3` binary expression inside
// the named method — the sub-expression the value route is asked to
// read, reached without the statement door seeing it.
func setterAssignmentIn(t *testing.T, p *program.CheckerProgram, method string) *ast.Node {
	t.Helper()
	var found *ast.Node
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if found != nil || node == nil {
			return true
		}
		if ast.IsBinaryExpression(node) &&
			node.AsBinaryExpression().OperatorToken.Kind == ast.KindEqualsToken {
			found = node
			return true
		}
		node.ForEachChild(visit)
		return false
	}
	visit(methodNamed(t, p, method).Body())
	if found == nil {
		t.Fatalf("no assignment expression in %s's body", method)
	}
	return found
}

func TestAccessorCalls_ASetterInExpressionPositionHoistsItsCallAndAnswersTheRightSide(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	for _, method := range []string{"passed", "held"} {
		ctx, p := accessorCtx(t, expressionSetterSource)
		registerAccessorContracts(t, ctx, p)
		context := compoundContext(ctx)
		value, ok := SetterAssignmentEffect(context, setterAssignmentIn(t, p, method))
		if !ok {
			t.Fatalf("`this.value = 3` in %s's expression position declined", method)
		}
		// the setter's call rides out through Hoisted, which the statement
		// route flushes ahead of its own statements
		if len(context.Hoisted) != 1 {
			t.Fatalf("%s hoisted %d statements, want exactly the setter's call", method, len(context.Hoisted))
		}
		if context.Hoisted[0].Kind != kernelbridge.IrStatementCall {
			t.Fatalf("%s hoisted %v, want a call — the setter runs a body", method, context.Hoisted[0].Kind)
		}
		// the expression's VALUE is the right side by the language's own
		// rule, not whatever the setter stored
		if value.Kind != kernelbridge.LoopEffectConst {
			t.Errorf("%s's value = %+v, want the right side's own constant `3`", method, value)
		}
		if entry := context.Hoisted[0].Args[0]; entry.Kind != kernelbridge.LoopEffectConst {
			t.Errorf("%s's entry 0 = %+v, want the same lowered right side", method, entry)
		}
	}
}

func TestAccessorCalls_ASetterInExpressionPositionWithNoStatementPositionIsNamed(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ctx, p := accessorCtx(t, expressionSetterSource)
	registerAccessorContracts(t, ctx, p)
	context := compoundContext(ctx)
	// CanHoist false: a loop head, a branch test — nowhere to put the
	// call, and a call on a path the IR does not spell is a WRONG answer
	context.CanHoist = false
	if _, ok := SetterAssignmentEffect(context, setterAssignmentIn(t, p, "passed")); ok {
		t.Errorf("a setter in expression position lowered where no statement position exists")
	}
	if len(context.Hoisted) != 0 {
		t.Errorf("a declined expression-position setter left %+v behind", context.Hoisted)
	}
	if name := DeclinedConstructOf(context); name != "a setter in expression position" {
		t.Errorf("the decline named %q, want \"a setter in expression position\" — "+
			"the refusal is named, not silent", name)
	}
}

func TestAccessorCalls_APlainFieldWriteIsNotAnExpressionPositionSetter(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ctx, p := accessorCtx(t, "class Box {\n"+
		"  store: number = 0;\n"+
		"  take(x: number): number { return x; }\n"+
		"  passed(): void { this.take(this.store = 3); }\n"+
		"}\n")
	registerAccessorContracts(t, ctx, p)
	context := compoundContext(ctx)
	if _, ok := SetterAssignmentEffect(context, setterAssignmentIn(t, p, "passed")); ok {
		t.Errorf("a plain field write took the setter route — it is a slot, and the slot routes own it")
	}
	if name := DeclinedConstructOf(context); name != "" {
		t.Errorf("a plain field write named the decline %q — it is not this route's shape at all", name)
	}
}
