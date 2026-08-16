// from control_flow/ir_loop.ts
//
// Loop heads and bodies for the flow IR: a comparison condition
// through the kernel's narrowing question, and a body folded into
// one parallel effect per binding.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// LoopHead is the {on, cond, after} shape loopHeadOf returns. A
// TwoSlot head compares On against a second tracked slot rather than
// a constant; nothing constant bounds either side, so Cond and After
// stay nil and the head rides to the kernel as the loop's CondCmp
// instead — Test names which comparison, OnB the other slot, and the
// kernel tightens each slot's exit by the negated comparison's ray
// read off the other slot's flagless exit.
type LoopHead struct {
	On      int
	Cond    *refinementsets.RefinedSet
	After   *refinementsets.RefinedSet
	TwoSlot bool
	OnB     int
	Test    kernelbridge.IrBranchTest
}

// LoopHeadOf is loopHeadOf in the TS source: a while head `binding
// <cmp> literal`, lowered through the kernel's narrowing question
// into the condition's truth and falsity sets. A head comparing two
// tracked number bindings (`i < n`) reads as the two-slot form
// instead: no constant set on either side, the comparison itself
// carried to the kernel.
func LoopHeadOf(context *LoweringContext, condition *ast.Node) (LoopHead, bool) {
	if !ast.IsBinaryExpression(condition) {
		return LoopHead{}, false
	}
	bin := condition.AsBinaryExpression()
	op, hasOp := CmpOps[bin.OperatorToken.Kind]
	on, onOk := NumberIndexOf(context, bin.Left)
	if !hasOp || !onOk {
		return LoopHead{}, false
	}
	k, kOk := NumberOf(bin.Right)
	if !kOk {
		// `i < n`: the right side is another tracked number slot
		onB, onBOk := NumberIndexOf(context, bin.Right)
		if !onBOk {
			return LoopHead{}, false
		}
		return LoopHead{On: on, TwoSlot: true, OnB: onB, Test: irTestOfCmp2(op)}, true
	}
	// a constant-bounded head needs the kernel's narrowing question to
	// split the condition into its true/false sets; a caller that never
	// seated one (Narrow nil) gets an honest decline here, the same
	// shape every other missing-information branch above already
	// returns, never a nil-function call
	if context.Narrow == nil {
		return LoopHead{}, false
	}
	answer := context.Narrow(kernelbridge.NarrowTree{Kind: kernelbridge.NarrowKindCmp, Op: op, K: k})
	head := LoopHead{On: on}
	if answer.WhenTrue != nil {
		set := answer.WhenTrue.Set
		head.Cond = &set
	}
	if answer.WhenFalse != nil {
		set := answer.WhenFalse.Set
		head.After = &set
	}
	return head, true
}

// LoopBody is the {written, effects} shape loopBodyOf returns.
type LoopBody struct {
	Written []bool
	Effects []kernelbridge.LoopEffect
}

// LoopBodyOf is loopBodyOf in the TS source: a loop body as one
// effect per binding, sequential statements folded into the solver's
// parallel form — a for-head's incrementor folds as the body's final
// step.
func LoopBodyOf(context *LoweringContext, body *ast.Node, incrementor *ast.Node) (LoopBody, bool) {
	current := make([]kernelbridge.LoopEffect, len(context.Bindings))
	for index := range context.Bindings {
		current[index] = kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectVar, Index: index}
	}
	if !FoldBody(context, StatementsOf(body), current) {
		return LoopBody{}, false
	}
	if incrementor != nil {
		step, ok := AssignmentOfExpression(context, incrementor)
		if !ok {
			return LoopBody{}, false
		}
		composed := SubstituteVars(step.Effect, current)
		if EffectNodes(composed) > EffectNodeBudget {
			return LoopBody{}, false
		}
		current[step.Target] = composed
	}
	written := make([]bool, len(current))
	for index, e := range current {
		written[index] = !(e.Kind == kernelbridge.LoopEffectVar && e.Index == index)
	}
	return LoopBody{Written: written, Effects: current}, true
}

// RaisesDone is raisesDone in the TS source: does a lowered
// statement list write the done flag anywhere — the test that tells
// a returning arm from a straight one.
func RaisesDone(statements []kernelbridge.IrStatement, done int) bool {
	for _, s := range statements {
		switch s.Kind {
		case kernelbridge.IrStatementAssign:
			if s.Target == done {
				return true
			}
		case kernelbridge.IrStatementBranch, kernelbridge.IrStatementBranchBoth:
			// the opaque branch carries its arms in the same two fields, so
			// a return inside either one raises the flag exactly as a
			// tested branch's does
			if RaisesDone(s.Then, done) || RaisesDone(s.Else, done) {
				return true
			}
		case kernelbridge.IrStatementCall:
			// a call writes only the slots Rets names; the done flag is
			// never among them today, but the test reads the statement
			// rather than assuming it
			for _, target := range s.Rets {
				if target == done {
					return true
				}
			}
		}
	}
	return false
}

// LoopStatement is loopStatement in the TS source: the IR loop
// statement from a prepared head and body. A two-slot head leaves
// every cond and after entry nil — no constant bounds either side —
// and rides in CondCmp instead, which the kernel reads at the exit:
// the loop left, so the head failed, and a failed ordered comparison
// between two real values holds its negation. The solver still
// certifies whatever the body's effects support.
func LoopStatement(context *LoweringContext, head LoopHead, body LoopBody) kernelbridge.IrStatement {
	cond := make([]*refinementsets.RefinedSet, len(context.Bindings))
	after := make([]*refinementsets.RefinedSet, len(context.Bindings))
	for i := range context.Bindings {
		if i == head.On && !head.TwoSlot {
			cond[i] = head.Cond
			after[i] = head.After
		}
	}
	var condCmp *kernelbridge.IrLoopCondCmp
	if head.TwoSlot {
		condCmp = &kernelbridge.IrLoopCondCmp{On: head.On, Test: head.Test, OnB: head.OnB}
	}
	return kernelbridge.IrStatement{
		Kind:    kernelbridge.IrStatementLoop,
		Written: body.Written,
		Cond:    cond,
		After:   after,
		Body:    body.Effects,
		CondCmp: condCmp,
	}
}
