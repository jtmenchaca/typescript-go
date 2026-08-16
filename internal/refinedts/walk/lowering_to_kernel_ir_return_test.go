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

// TestLoweringToKernelIR_AFieldReadNoSlotResolvesHavocsRatherThanStayingInert
// pins the syntax-wave divergence (AGENT-BRIEF.md, i-more-expressions.ts's
// privateAccessorSlotLaidOut and b-body-expressions.ts's thisFieldRead):
// `return this.#age;` (a private-named step propertyPathOf/SpelledNameOf
// cannot spell) and `return this.age;` (a step this minimal, bundle-less
// context has no slot for either) both used to take THE INERT RETURN
// branch — writeAndCallFree(head) is true for a plain property read, so
// the ret slot wrote unknown WITHOUT calling NoteFirstHavoc. A body that
// lowers with no havoc name reads as COMPLETE (SummaryOutcomeOf), and
// applySummary's serving rule serves a COMPLETE body's TOP ret
// unconditionally — so a call through this route answered unknown as a
// genuine, served claim instead of declining to the walk route, which
// reads the field correctly (a class field invariant, an accessor's own
// backing field). The fix: a property/element read is no longer treated
// as inert — it falls to THE OPAQUE RETURN below, which notes the havoc,
// so the summary reads POROUS and applySummary declines instead of
// serving a lost read as if it were a real answer.
func TestLoweringToKernelIR_AFieldReadNoSlotResolvesHavocsRatherThanStayingInert(t *testing.T) {
	cases := []string{
		"return this.#age;",
		"return this.age;",
		"return this.a.b;",
	}
	for _, source := range cases {
		context := loweringResultContext(nil, nil)
		stmts, ok := LowerStatements(context, loweringParse(t, source))
		if !ok {
			t.Errorf("%q declined outright — its control flow is exact even where its value is not", source)
			continue
		}
		if context.FirstHavoc == "" {
			t.Errorf("%q lowered with FirstHavoc = \"\" (COMPLETE) — want a havoc name, since no slot named this field's own read", source)
		}
		if !RaisesDone(stmts, context.Result.Done) {
			t.Errorf("%q: RaisesDone = false, want true — the block ends at this return", source)
		}
	}
}

// TestLoweringToKernelIR_ABareThisReturnStaysInert is the regression
// guard beside the fix above: `return this;` itself — no property or
// element step at all — carries no scalar value on ANY route (a bare
// instance reference), and ReturnsSelf/ReturnsReceiver is the standing
// mechanism that already covers its aliasing implications at the call
// sites. It must keep answering COMPLETE (no havoc name), exactly as
// before — the fix narrows THE INERT RETURN to exclude a real field or
// element read, not every value-free expression.
func TestLoweringToKernelIR_ABareThisReturnStaysInert(t *testing.T) {
	context := loweringResultContext(nil, nil)
	if _, ok := LowerStatements(context, loweringParse(t, "return this;")); !ok {
		t.Fatalf("`return this;` declined — a bare this always lowers")
	}
	if context.FirstHavoc != "" {
		t.Errorf("FirstHavoc = %q, want \"\" — a bare `this` carries no field read to lose", context.FirstHavoc)
	}
}
