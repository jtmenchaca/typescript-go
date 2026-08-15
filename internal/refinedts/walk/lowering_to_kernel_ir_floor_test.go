// split from lowering_to_kernel_ir_test.go — contained control and the floor

package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

/* ── contained control no longer costs the body ──────────────────── */

func TestLoweringToKernelIR_ASwitchTheChainDeclinesHavocsInsteadOfDecliningTheBody(t *testing.T) {
	// the switch's discriminant is untracked, so the equality chain
	// declines. Its bare breaks cannot LEAVE the switch, so the floor
	// admits it: the whole switch havocs what it could have written and
	// the body keeps its route.
	context := &LoweringContext{
		Bindings: []string{"x", "keep"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber},
		Typeofs:  []TypeofTag{TypeofTagNumber, TypeofTagNumber},
	}
	stmts, ok := LowerStatements(context, loweringParse(t, `
		keep = 3;
		switch (free) { case 1: x = 1; break; default: x = 2; }
	`))
	if !ok {
		t.Fatalf("a switch with a contained break declined the body — the break cannot leave it")
	}
	if len(stmts) != 2 {
		t.Fatalf("len(stmts) = %d, want 2 — the readable write and the switch's one havoc: %+v", len(stmts), stmts)
	}
	if stmts[1].Kind != kernelbridge.IrStatementAssign || stmts[1].Target != 0 ||
		stmts[1].Effect.Kind != kernelbridge.LoopEffectUnknown {
		t.Errorf("stmts[1] = %+v, want `assign 0 unknown` — x alone; keep is untouched", stmts[1])
	}
	if context.FirstHavoc != "switch" {
		t.Errorf("FirstHavoc = %q, want %q", context.FirstHavoc, "switch")
	}
}

func TestLoweringToKernelIR_ALoopCarryingABareBreakHavocsInsteadOfDecliningTheBody(t *testing.T) {
	context := &LoweringContext{
		Bindings: []string{"x"},
		Sorts:    []BindingKind{BindingKindNumber},
		Typeofs:  []TypeofTag{TypeofTagNumber},
	}
	// a for-in has no reading in the SOLVER's loop form at all, and its
	// body's break stays inside. The statement-bodied loop takes it
	// (ir_loop_stmts.go): the body's own statements ride under a loop
	// that reads no condition, and the kernel havocs the body's write
	// set — which is `x` — leaving every other slot alone.
	//
	// Before that form existed this body was the havoc FLOOR's, and the
	// floor's answer was one `assign x unknown` naming "for-in". The
	// exit state for `x` is the same either way (the loop wrote it under
	// an unbounded trip count, so nothing about it survives); what
	// changed is that the slots the loop merely MENTIONS are no longer
	// havocked with it, and the body keeps a real loop rather than a
	// stand-in.
	stmts, ok := LowerStatements(context, loweringParse(t, `
		for (const k in o) { x = 1; break; }
	`))
	if !ok {
		t.Fatalf("a for-in carrying a bare break declined — the break cannot leave it")
	}
	if len(stmts) != 1 || stmts[0].Kind != kernelbridge.IrStatementLoopStmts {
		t.Fatalf("stmts = %+v, want one statement-bodied loop", stmts)
	}
	// the body is the loop's whole carrying field, and the write to x is
	// in it — the effect-bodied loop's fields stay empty
	if len(stmts[0].Stmts) == 0 {
		t.Errorf("the loop carries no statements — the body's write to x must ride inside it")
	}
	if stmts[0].Written != nil || stmts[0].Cond != nil || stmts[0].Body != nil {
		t.Errorf("the loop carries an effect-bodied loop's fields: %+v", stmts[0])
	}
	// the `break` itself has no reading, so it takes the floor INSIDE the
	// body and names itself — the body is porous, and it says so
	if context.FirstHavoc == "" {
		t.Errorf("nothing was named — the break has no reading and must name its own stand-in")
	}
}

func TestLoweringToKernelIR_ABreakThatCrossesOutOfTheHavockedStatementStillDeclines(t *testing.T) {
	context := &LoweringContext{
		Bindings: []string{"x"},
		Sorts:    []BindingKind{BindingKindNumber},
		Typeofs:  []TypeofTag{TypeofTagNumber},
	}
	// the labelled break leaves the WHILE the floor would stand in for:
	// the transfer goes to a label outside it, so the havoc's writes
	// would sit on a path control had already left
	source := loweringParse(t, `outer: while (c) { for (const k in o) { break outer; } }`)
	inner := source[0].AsLabeledStatement().Statement.
		AsWhileStatement().Statement.AsBlock().Statements.Nodes
	if _, ok := LowerStatements(context, inner); ok {
		t.Errorf("a labelled break crossing out lowered — the transfer leaves the statement")
	}
	if named := DeclinedConstructOf(context); named != "labeled break crossing out" {
		t.Errorf("the decline named %q, want %q", named, "labeled break crossing out")
	}
}

func TestLoweringToKernelIR_ABodyThatLoweredOwesNoDeclineNameEvenWhereAnArmDeclined(t *testing.T) {
	// the switch's arm carries a return with no result slot, so the arm's
	// own nested lowering declines — and the havoc floor then stands in
	// for the whole switch. The BODY lowered, so it owes no decline name;
	// only the outermost run's answer decides that.
	context := &LoweringContext{
		Bindings: []string{"x"},
		Sorts:    []BindingKind{BindingKindNumber},
		Typeofs:  []TypeofTag{TypeofTagNumber},
	}
	if _, ok := LowerStatements(context, loweringParse(t, `
		switch (free) { case 1: x = 1; break; }
	`)); !ok {
		t.Fatalf("the body declined — the switch's contained break havocs")
	}
	if named := DeclinedConstructOf(context); named != "" {
		t.Errorf("a body that lowered left the decline name %q, want none", named)
	}
}

func TestLoweringToKernelIR_TheDeclineNameIsReadOnceAndCleared(t *testing.T) {
	context := &LoweringContext{
		Bindings: []string{"x"},
		Sorts:    []BindingKind{BindingKindNumber},
		Typeofs:  []TypeofTag{TypeofTagNumber},
	}
	if _, ok := LowerStatements(context, loweringParse(t, `with (o) { x = 1; }`)); ok {
		t.Fatalf("a with statement lowered — its written-slot set is not a syntactic question")
	}
	if named := DeclinedConstructOf(context); named != "with statement" {
		t.Fatalf("the decline named %q, want %q", named, "with statement")
	}
	if named := DeclinedConstructOf(context); named != "" {
		t.Errorf("a second read answered %q, want empty — one run, one name", named)
	}
}
