// Pins objectReturnMemberStatements / arrayReturnMemberStatements
// (lowering_to_kernel_ir_return_members.go): a returned literal whose
// SHAPE allocates member slots (returnedLiteralShape's inertValue gate
// passes — no call, no write, anywhere in the literal) but whose ONE
// member's VALUE the effect grammar still cannot spell — an untracked
// global's property read, which PathSlotIndexOf/ArrayLengthSlotOf have
// no slot for.
//
// Before this pin's fix, the unspellable member silently took
// `unknownEffect` with no NoteFirstHavoc call, so the body reported
// SummaryComplete over a key it never determined — the same
// fabrication class the return route's own retired member-read arm was
// pulled for (lowering_to_kernel_ir_return.go's comment on the arm).
// kernel_summaries.go's serving rule reads a recorded SummaryComplete
// as license to hand summaryMemberResult's rebuilt object straight to
// a caller, so an unnoted decline here would have served a fabricated
// exact value for `b`.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
)

func TestReturnObjectLiteral_UnspellableMemberValueStaysPorous(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
		declare const g: { field: number };
		export function f(): { a: number; b: number } {
			return { a: 1, b: g.field };
		}
	`)
	fn := entryEnvFunctionNamed(t, p, "f")
	_, ok := RelowerSummaryBody(&FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}, fn)
	outcome, construct, recorded := SummaryOutcomeOf(fn)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	if !ok {
		t.Fatalf("the body declined outright — want a lowering that succeeds porous, not a decline")
	}
	if outcome == SummaryComplete {
		t.Errorf("outcome = %q (construct %q), want SummaryPorous — member `b` reads an untracked global's field, which the effect grammar cannot spell; a COMPLETE outcome here fabricates that member's value", outcome, construct)
	}
	if construct == "" {
		t.Errorf("construct = %q, want a name naming the unspellable member", construct)
	}
}

// TestReturnObjectLiteral_EveryMemberSpellableStaysComplete is the
// positive twin: every member's value IS in the effect grammar
// (a literal, a copy of a tracked parameter), so the body reports
// SummaryComplete exactly as before this pin's fix — the fix must not
// widen porous over a body that already determined every member.
func TestReturnObjectLiteral_EveryMemberSpellableStaysComplete(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
		export function f(n: number): { a: number; b: number } {
			return { a: 1, b: n };
		}
	`)
	fn := entryEnvFunctionNamed(t, p, "f")
	_, ok := RelowerSummaryBody(&FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}, fn)
	outcome, construct, recorded := SummaryOutcomeOf(fn)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	if !ok || outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q, ok %v), want SummaryComplete — both members are spellable", outcome, construct, ok)
	}
}

// TestReturnArrayLiteral_UnspellableElementStaysPorous mirrors the
// object pin for arrayReturnMemberStatements' elementJoinAssignments:
// an array literal whose elements are all inertValue-safe but whose
// join still needs a member the effect grammar cannot spell.
func TestReturnArrayLiteral_UnspellableElementStaysPorous(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
		declare const g: { field: number };
		export function f(): number[] {
			return [1, g.field];
		}
	`)
	fn := entryEnvFunctionNamed(t, p, "f")
	_, ok := RelowerSummaryBody(&FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}, fn)
	outcome, construct, recorded := SummaryOutcomeOf(fn)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	if !ok {
		t.Fatalf("the body declined outright — want a lowering that succeeds porous, not a decline")
	}
	if outcome == SummaryComplete {
		t.Errorf("outcome = %q (construct %q), want SummaryPorous — the second element reads an untracked global's field, which the effect grammar cannot spell; a COMPLETE outcome here fabricates the joined element", outcome, construct)
	}
}

// ── a returned object's member whose VALUE is a bare CALL ───────────────
//
// objectLiteralMembersAdmitShape (ir_summary_returned_shape.go) widened
// the layout gate: a property whose value is a bare call no longer
// refuses the whole shape outright, on the theory that
// objectReturnMemberStatements can still WRITE that one member — either
// by hoisting the call (RhsEffect -> EffectOf -> HoistCallEffect, for a
// callee with a COMPLETE summary blob) or by declining that one member
// honestly (NoteFirstHavoc, porous). Both pins below prove the writer
// keeps that promise: a call the hoist CAN serve determines the member,
// and a call it cannot (no resolvable declaration, so no blob to hoist)
// still reports porous rather than fabricating the member's value.

// TestReturnObjectLiteral_UnresolvableCallMemberStaysPorous is the call
// member's own unspellable-value pin, mirroring
// TestReturnObjectLiteral_UnspellableMemberValueStaysPorous above: `g` is
// an AMBIENT declaration, so ContractOf/ResolveCallee has no declaration
// to resolve it to and HoistCallEffect's blob gate never opens — the
// member takes unknown and NoteFirstHavoc names it, exactly the same
// honesty the untracked-global-read pin already proves for a non-call
// value.
func TestReturnObjectLiteral_UnresolvableCallMemberStaysPorous(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
		declare function g(): number;
		export function f(): { a: number; b: number } {
			return { a: 1, b: g() };
		}
	`)
	fn := entryEnvFunctionNamed(t, p, "f")
	_, ok := RelowerSummaryBody(&FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}, fn)
	outcome, construct, recorded := SummaryOutcomeOf(fn)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	if !ok {
		t.Fatalf("the body declined outright — want a lowering that succeeds porous, not a decline")
	}
	if outcome == SummaryComplete {
		t.Errorf("outcome = %q (construct %q), want SummaryPorous — member `b` calls an ambient declaration with no body to hoist; a COMPLETE outcome here fabricates that member's value", outcome, construct)
	}
	if construct == "" {
		t.Errorf("construct = %q, want a name naming the unspellable member", construct)
	}
}

// TestReturnObjectLiteral_ResolvableCallMemberDeterminesAndStaysComplete
// is the positive twin: `g`'s declaration IS in view and its own body
// lowers to a COMPLETE summary blob, so HoistCallEffect's blob gate
// opens, the call hoists ahead of the return, and the member reads the
// hoisted temp — determining the member rather than merely declining it
// honestly. The whole body then reports SummaryComplete, same as the
// plain-literal positive twin above.
//
// `g`'s contract must be REGISTERED under its symbol before relowering —
// ContractBySymbol (contract_lookup.go) resolves a callee only through
// ctx.Contracts[symbol]; it does not independently discover a plain
// sibling function declaration. An empty Contracts map (the shape every
// OTHER pin in this file uses, since none of them calls anything) leaves
// ResolveCallee nil for `g`, HoistCallEffect's `callee == nil` gate
// declines, and the member falls to the same honest
// NoteFirstHavoc/porous floor the unresolvable-callee pin above proves —
// which is what this test caught before this registration was added.
// TestReturnTemplateOverServedCall_CompletesThroughTheSequenceHoist
// (ir_return_template_hoist_pin_test.go) is the sibling pin this
// registration pattern is copied from.
func TestReturnObjectLiteral_ResolvableCallMemberDeterminesAndStaysComplete(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
		function g(): number { return 1; }
		export function f(): { a: number; b: number } {
			return { a: 1, b: g() };
		}
	`)
	ctx := &FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}
	g := entryEnvFunctionNamed(t, p, "g")
	gName := g.AsFunctionDeclaration().Name()
	gSymbol := p.Checker.GetSymbolAtLocation(gName)
	if gSymbol == nil {
		t.Fatalf("no symbol for g")
	}
	ctx.Contracts[gSymbol] = &FunctionContract{Declaration: g}
	if _, blobOk := LowerSummaryBody(ctx, g); !blobOk {
		t.Fatalf("g's body did not compile a blob — the premise (a servable callee) is gone")
	}
	if outcome, construct, _ := SummaryOutcomeOf(g); outcome != SummaryComplete {
		t.Fatalf("g: outcome=%q construct=%q, want complete", outcome, construct)
	}
	fn := entryEnvFunctionNamed(t, p, "f")
	_, ok := RelowerSummaryBody(ctx, fn)
	outcome, construct, recorded := SummaryOutcomeOf(fn)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	if !ok || outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q, ok %v), want SummaryComplete — g has a resolvable, complete-summary body, so member `b` hoists and determines", outcome, construct, ok)
	}
}
