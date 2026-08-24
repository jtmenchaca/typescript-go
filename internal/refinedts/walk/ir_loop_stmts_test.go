// The statement-bodied loop, pinned on three bodies: one whose body
// the SOLVER's fold declines but the statement walk reads, one whose
// iterable nothing flattens, and one whose HEAD moves state and so
// keeps the havoc floor.
//
// The soundness pin is the second half of the first test: after a loop
// nobody bounded, a slot the body WROTE is TOP — any trip count wrote
// it, including none, so nothing about it survives — while a slot the
// body never touched keeps its entry value. That difference is the
// whole reason this form is worth more than the floor, which havocked
// both.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// A while whose body the SOLVER's fold declines — a switch is neither
// an assignment nor an if, which is FoldBody's whole grammar — but
// which the ordinary statement walk reads clean. Before this form the
// body fell to the havoc floor and recorded porous naming "while".
func TestLoopStmts_AWhileWhoseBodyTheFoldDeclinesLowersAndRecordsComplete(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearSummaryOutcomes()
	declaration := summaryDeclarationOf(t,
		"function f(n: number, c: number) { let s = n + 1; "+
			"while (c > 0) { switch (n) { case 1: s = s + 1; break; default: s = s + 2; } } "+
			"return s; }")
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	lowered, loweredOk := RelowerSummaryBody(ctx, declaration)
	if !loweredOk {
		_, construct, _ := SummaryOutcomeOf(nil, declaration)
		t.Fatalf("the body declined at %q — the statement-bodied loop reads it", construct)
	}
	outcome, construct, recorded := SummaryOutcomeOf(nil, declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	if outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q), want complete — every statement of the loop lowers, "+
			"only the fold's grammar declined", outcome, construct)
	}
	loop, held := loopStmtsHolds(lowered.Stmts)
	if !held {
		t.Fatalf("no loopStmts statement in the lowered body — the loop fell to the floor")
	}
	if len(loop.Stmts) == 0 {
		t.Errorf("the loopStmts body is empty — the switch's chain must ride inside it")
	}
	// Written and Body are the EFFECT-bodied loop's fields and stay nil;
	// Cond and After are shared, and `while (c > 0)` reads, so they carry
	// the head for the exit refinement
	if loop.Written != nil || loop.Body != nil {
		t.Errorf("the loopStmts statement carries an effect-bodied loop's fields — "+
			"Written and Body are not its: %+v", loop)
	}
	cSlot := loopStmtsSlotOf(t, declaration, "c")
	if loop.After == nil || cSlot >= len(loop.After) || loop.After[cSlot] == nil {
		t.Errorf("the loopStmts statement carries no falsity set at `c` — "+
			"`while (c > 0)` leaves only when the head fails: %+v", loop.After)
	}
	// THE SOUNDNESS PIN. `s` is written inside the loop, so after a loop
	// nothing bounded its exit is TOP: zero trips and a thousand are both
	// admitted, and no set holds every answer. `n` the loop never writes,
	// so its exit is exactly its entry — that survival is what this form
	// buys over the floor, which havocked every slot the loop mentioned.
	sSlot := loopStmtsSlotOf(t, declaration, "s")
	nSlot := loopStmtsSlotOf(t, declaration, "n")
	exits := loopStmtsWalk(t, kernel, lowered, map[int]float64{nSlot: 2, cSlot: 1})
	if !exits[sSlot].Top {
		t.Errorf("`s`'s exit is not TOP: %+v — the loop writes it under an unbounded trip count, "+
			"so nothing about it survives", exits[sSlot])
	}
	// `c` the loop never writes AND the head reads, so its exit carries
	// the falsity set: the loop left, so `c > 0` failed and `c <= 0`.
	// The entry value 1 satisfies the head, so it cannot be an exit value
	// — the intersection excludes it, which is exactly the refinement
	// this unit adds over the plain havoc.
	if exits[cSlot].Top {
		t.Errorf("`c`'s exit is TOP — the loop never writes it and the head bounds its exit")
	} else if kernel.Member(exits[cSlot].Set, []float64{1}) {
		t.Errorf("`c`'s exit admits 1: %+v — the loop leaves only when `c > 0` fails", exits[cSlot].Set)
	}
	if exits[nSlot].Top {
		t.Fatalf("`n`'s exit is TOP — the loop never writes it, so its entry knowledge must survive")
	}
	if !kernel.Member(exits[nSlot].Set, []float64{2}) {
		t.Errorf("`n`'s exit excludes its entry value 2: %+v", exits[nSlot].Set)
	}
	if kernel.Member(exits[nSlot].Set, []float64{7}) {
		t.Errorf("`n`'s exit admits 7: %+v — the loop wrote nothing into it", exits[nSlot].Set)
	}
}

// A for-of over an iterable nothing flattened. Neither the array nor
// the map route can give the binding a per-pass value, so both decline;
// this form writes the bound name UNKNOWN at the top of every trip and
// walks the body's statements, and the body is read whole.
//
// The body does not READ the bound name. A for-of binding takes a
// whole-name slot with no sort — nothing in the head says what the
// iterated values are — so an arithmetic read of it declines by the
// sort rule and that statement would floor, which is a separate
// question from whether the LOOP lowers. What is pinned here is the
// loop: the binding is written unknown, and the statements around it
// keep their knowledge.
func TestLoopStmts_AForOfOverAnUntrackedIterableLowersWithItsBindingUnknown(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearSummaryOutcomes()
	declaration := summaryDeclarationOf(t,
		"function f(n: number, xs: number[]) { let s = n + 1; "+
			"for (const x of xs) { switch (n) { case 1: s = s + 1; break; default: s = s + 2; } } "+
			"return s; }")
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	lowered, loweredOk := RelowerSummaryBody(ctx, declaration)
	if !loweredOk {
		_, construct, _ := SummaryOutcomeOf(nil, declaration)
		t.Fatalf("the body declined at %q — an untracked iterable is still a readable loop", construct)
	}
	outcome, construct, recorded := SummaryOutcomeOf(nil, declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	if outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q), want complete — an unknown bound name is what is "+
			"true of it, not a hole", outcome, construct)
	}
	loop, held := loopStmtsHolds(lowered.Stmts)
	if !held {
		t.Fatalf("no loopStmts statement in the lowered body — the for-of fell to the floor")
	}
	// the BOUND NAME takes unknown first, ahead of the body that reads it.
	// `xs: number[]` flattens into two entries ("xs.len", "xs.elem" —
	// ArrayParameterOf, since a for-of over the bare name is an admitted
	// array form), so loopStmtsSlotOf's declared-parameter count under-
	// counts entries by one and cannot name x's slot correctly. `x` is
	// the last local the layout lays out — declared after `s`, and
	// nothing follows it before "#done" — so its slot is the one
	// immediately below DoneIndex.
	xSlot := lowered.DoneIndex - 1
	if len(loop.Stmts) == 0 {
		t.Fatalf("the loopStmts body is empty")
	}
	first := loop.Stmts[0]
	if first.Kind != kernelbridge.IrStatementAssign || first.Target != xSlot ||
		first.Effect.Kind != kernelbridge.LoopEffectUnknown {
		t.Errorf("the loop body's first statement is %+v, want `x := unknown` at slot %d — "+
			"the binding takes its per-trip value before the body reads it", first, xSlot)
	}
	// the statement BEFORE the loop kept its knowledge: `s` is written by
	// the loop and so exits TOP, but `n` is not and keeps its entry
	nSlot := loopStmtsSlotOf(t, declaration, "n")
	exits := loopStmtsWalk(t, kernel, lowered, map[int]float64{nSlot: 2})
	if exits[nSlot].Top {
		t.Fatalf("`n`'s exit is TOP — the loop never writes it")
	}
	if !kernel.Member(exits[nSlot].Set, []float64{2}) {
		t.Errorf("`n`'s exit excludes its entry value 2: %+v", exits[nSlot].Set)
	}
}

// A HEAD that moves state keeps the floor. `while (g())` calls on every
// trip, and this form reads no condition at all — walking the loop
// without the call would leave whatever the callee moved behind. So the
// route declines and the whole loop havocs, naming itself "while".
func TestLoopStmts_ACallingHeadStillFallsToTheFloor(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearSummaryOutcomes()
	declaration := summaryDeclarationOf(t,
		"function f(n: number) { let s = n + 1; "+
			"while (g()) { switch (n) { case 1: s = s + 1; break; default: s = s + 2; } } "+
			"return s; }")
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	lowered, loweredOk := RelowerSummaryBody(ctx, declaration)
	if !loweredOk {
		_, construct, _ := SummaryOutcomeOf(nil, declaration)
		t.Fatalf("the body declined at %q — the floor havocs, it does not refuse", construct)
	}
	outcome, construct, recorded := SummaryOutcomeOf(nil, declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	if outcome != SummaryPorous {
		t.Errorf("outcome = %q, want porous — a head that calls is not readable by any loop route", outcome)
	}
	if construct != "while" {
		t.Errorf("construct = %q, want %q — the floor names the syntax it stood in for", construct, "while")
	}
	if _, held := loopStmtsHolds(lowered.Stmts); held {
		t.Errorf("a loopStmts statement was built for a calling head — the head's call would be lost")
	}
}
