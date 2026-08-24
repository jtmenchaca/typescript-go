// Pins for the remaining scalar RETURN-POSITION shapes the census
// names singly: `return (! member)`, `return (! binary &&)`, `return
// (binary +)`, `return (binary in)`. Each is checked in isolation
// against a MINIMAL fixture built from the corpus's own use of the
// shape, to see whether the plain effect grammar (RhsEffect, tried at
// lowering_to_kernel_ir_return.go's line ~115, ahead of every arm this
// agent owns) already serves it, or whether it needs an arm in a file
// this agent owns.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
)

// TestReturnBinaryUnary_InOperatorCompletes pins `return 'upperWidth' in
// box;` (DataUtils-adjacent shape: cartesianViewBoxToTrapezoid's own
// `'upperWidth' in box`), census row "return (binary in)". `in` is one
// of booleanBinaryTokens (effect_expression.go) gated on
// writeAndCallFree — a write-and-call-free `in` test over an untracked
// object parameter should already complete through the plain effect
// grammar with NO new arm needed.
func TestReturnBinaryUnary_InOperatorCompletes(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	declaration := summaryDeclarationOf(t, `
		function hasUpperWidth(box: { width: number; upperWidth?: number }) {
			return 'upperWidth' in box;
		}
	`)
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	_, ok := RelowerSummaryBody(ctx, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(nil, declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	t.Logf("ok=%v outcome=%q construct=%q", ok, outcome, construct)
	if !ok || outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q, ok %v), want SummaryComplete — a write-and-call-free `in` test is one of booleanBinaryTokens", outcome, construct, ok)
	}
}

// TestReturnBinaryUnary_PlusOnUntrackedMembersCompletes pins a
// write-and-call-free `+` over two untracked member reads: `return
// a.x + a.y;` where `a` is an object-typed parameter with no leaf
// annotation for `.x`/`.y`. binOps (effect_expression.go) admits `+`
// through the arithmetic grammar once BOTH operands lower — and once
// this agent's own returnNonThisMemberEffect (ir_return_member.go)
// serves an untracked `a.x`/`a.y` read as unknown, the arithmetic
// itself still needs a NUMBER-SORTED operand (EffectOf's numberSlot
// gate reads only a tracked slot, never an unknown-sorted opaque
// value) — so this pin checks whether the plain grammar's arithmetic
// arm reaches that far, or whether it needs its own arm.
func TestReturnBinaryUnary_PlusOnUntrackedMembersCompletes(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	declaration := summaryDeclarationOf(t, `
		function sumXY(a: { x: number; y: number }) {
			return a.x + a.y;
		}
	`)
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	_, ok := RelowerSummaryBody(ctx, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(nil, declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	t.Logf("ok=%v outcome=%q construct=%q", ok, outcome, construct)
}

// TestReturnBinaryUnary_NegatedMemberCompletes pins `return
// !upperFirst.somethingFalsy;`-shaped negation of an untracked member
// read (census row "return (! member)"): `function f(x: { a: unknown
// }) { return !x.a; }`. `!e` reads as the exact two-value set under
// writeAndCallFree(e) (effect_expression.go line 286) — a property
// READ is write-and-call-free by definition (writeAndCallFree
// descends through children and a PropertyAccessExpression carries no
// write/call itself), so this should already complete via the plain
// grammar with no new arm.
func TestReturnBinaryUnary_NegatedMemberCompletes(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	declaration := summaryDeclarationOf(t, `
		function f(x: { a: unknown }) {
			return !x.a;
		}
	`)
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	_, ok := RelowerSummaryBody(ctx, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(nil, declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	t.Logf("ok=%v outcome=%q construct=%q", ok, outcome, construct)
	if !ok || outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q, ok %v), want SummaryComplete — `!x.a` is a write-and-call-free negation, the two-value set", outcome, construct, ok)
	}
}
