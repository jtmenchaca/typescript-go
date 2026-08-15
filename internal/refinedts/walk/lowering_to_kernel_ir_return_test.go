// split from lowering_to_kernel_ir_test.go — the opaque return

package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

/* ── the opaque return ───────────────────────────────────────────── */

// The three conversions below are the wave's coverage work: a return
// whose VALUE no reading lowered, a throw that leaves the body, and
// control that cannot leave the statement being stood in for. Each
// keeps the body's route where it used to cost the body its lowering.

// loweringResultContext is a FUNCTION BODY context: two slots for the
// body's own names and the (done, ret) pair the return routes write.
func loweringResultContext(names []string, sorts []BindingKind) *LoweringContext {
	bindings := append(append([]string{}, names...), "#done", "#ret")
	kinds := append(append([]BindingKind{}, sorts...), BindingKindNumber, BindingKindUnknown)
	tags := make([]TypeofTag, len(bindings))
	for index := range tags {
		tags[index] = TypeofTagNumber
	}
	tags[len(tags)-1] = TypeofTagNone
	return &LoweringContext{
		Bindings: bindings,
		Sorts:    kinds,
		Typeofs:  tags,
		Result:   &LoweringResult{Done: len(names), Ret: len(names) + 1},
	}
}

func TestLoweringToKernelIR_AReturnNoReadingLoweredKeepsItsControlAndLosesOnlyItsValue(t *testing.T) {
	// every route for the VALUE declined — no effect, no await form, no
	// inlinable callee, no guard shape. The control flow is still exact:
	// `#ret := unknown` then the done raise, the readable return's own
	// two statements with the value part standing in.
	// A write-and-call-free literal return is READ (no havoc); a literal
	// whose construction RUNS CODE still havocs.
	context := loweringResultContext([]string{"x"}, []BindingKind{BindingKindNumber})
	stmts, ok := LowerStatements(context, loweringParse(t, "return { lo: f() };"))
	if !ok {
		t.Fatalf("an opaque return declined — its control flow is exact")
	}
	if len(stmts) != 2 {
		t.Fatalf("len(stmts) = %d, want 2 — the ret write and the raise: %+v", len(stmts), stmts)
	}
	if stmts[0].Kind != kernelbridge.IrStatementAssign || stmts[0].Target != context.Result.Ret ||
		stmts[0].Effect.Kind != kernelbridge.LoopEffectUnknown {
		t.Errorf("stmts[0] = %+v, want `assign #ret unknown` — never absent, which would claim undefined", stmts[0])
	}
	if stmts[1].Kind != kernelbridge.IrStatementAssign || stmts[1].Target != context.Result.Done {
		t.Errorf("stmts[1] = %+v, want the done raise", stmts[1])
	}
	if !RaisesDone(stmts, context.Result.Done) {
		t.Errorf("RaisesDone = false, want true — the block ends at this return")
	}
	if context.FirstHavoc != "return (object literal)" {
		t.Errorf("FirstHavoc = %q, want %q", context.FirstHavoc, "return (object literal)")
	}
}

func TestLoweringToKernelIR_AnOpaqueReturnNamesTheReturnedExpressionsOwnSyntax(t *testing.T) {
	cases := []struct {
		source string
		want   string
	}{
		{"return { lo: f() };", "return (object literal)"},
		{"return this.x.y(q);", "return (call this.x.y)"},
		{"return [f()];", "return (array literal)"},
		{"return new Thing();", "return (new)"},
		{"return tag`raw`;", "return (tagged template tag)"},
	}
	for _, held := range cases {
		context := loweringResultContext([]string{"x"}, []BindingKind{BindingKindNumber})
		if _, ok := LowerStatements(context, loweringParse(t, held.source)); !ok {
			t.Errorf("%q declined", held.source)
			continue
		}
		if context.FirstHavoc != held.want {
			t.Errorf("%q named %q, want %q", held.source, context.FirstHavoc, held.want)
		}
	}
}

func TestLoweringToKernelIR_StatementsAfterAnOpaqueReturningArmStillGateOnTheDoneFlag(t *testing.T) {
	// the continuation discipline is the readable return's, unchanged: an
	// arm that may have returned puts the block's remainder under the
	// flag's falsity. The opaque return raises the same flag, so it reads
	// exactly the same way.
	context := loweringResultContext([]string{"x"}, []BindingKind{BindingKindNumber})
	stmts, ok := LowerStatements(context, loweringParse(t, `
		if (x === 0) { return { lo: 1 }; }
		x = 2;
	`))
	if !ok {
		t.Fatalf("the body declined — the opaque return keeps its route")
	}
	if len(stmts) != 2 {
		t.Fatalf("len(stmts) = %d, want 2 — the branch and the gated remainder: %+v", len(stmts), stmts)
	}
	if stmts[0].Kind != kernelbridge.IrStatementBranch {
		t.Errorf("stmts[0].Kind = %v, want a branch", stmts[0].Kind)
	}
	gate := stmts[1]
	if gate.Kind != kernelbridge.IrStatementBranch || gate.On != context.Result.Done ||
		gate.Test != kernelbridge.IrTestTruthyNum {
		t.Fatalf("stmts[1] = %+v, want the remainder gated on the done flag", gate)
	}
	if len(gate.Then) != 0 {
		t.Errorf("the flag-up arm runs %d statements, want none — the body returned", len(gate.Then))
	}
	if len(gate.Else) != 1 || gate.Else[0].Target != 0 {
		t.Errorf("the flag-down arm = %+v, want the one later assignment", gate.Else)
	}
}
