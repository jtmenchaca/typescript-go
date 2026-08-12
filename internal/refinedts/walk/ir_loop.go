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

// LoopHead is the {on, cond, after} shape loopHeadOf returns.
type LoopHead struct {
	On    int
	Cond  *refinementsets.RefinedSet
	After *refinementsets.RefinedSet
}

// LoopHeadOf is loopHeadOf in the TS source: a while head `binding
// <cmp> literal`, lowered through the kernel's narrowing question
// into the condition's truth and falsity sets.
func LoopHeadOf(context *LoweringContext, condition *ast.Node) (LoopHead, bool) {
	if !ast.IsBinaryExpression(condition) {
		return LoopHead{}, false
	}
	bin := condition.AsBinaryExpression()
	op, hasOp := CmpOps[bin.OperatorToken.Kind]
	on, onOk := NumberIndexOf(context, bin.Left)
	k, kOk := NumberOf(bin.Right)
	if !hasOp || !onOk || !kOk {
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

// EffectNodes is effectNodes in the TS source: effect size, for the
// substitution budget.
func EffectNodes(e kernelbridge.LoopEffect) int {
	switch e.Kind {
	case kernelbridge.LoopEffectVar, kernelbridge.LoopEffectConst, kernelbridge.LoopEffectUnknown:
		return 1
	case kernelbridge.LoopEffectUnary:
		return 1 + EffectNodes(*e.A)
	case kernelbridge.LoopEffectBinary, kernelbridge.LoopEffectJoin:
		return 1 + EffectNodes(*e.A) + EffectNodes(*e.B)
	}
	panic("EffectNodes: unreached kind")
}

// SubstituteVars is substituteVars in the TS source: every `var i`
// replaced by binding i's CURRENT effect — how a sequential read
// becomes the parallel form the solver speaks.
func SubstituteVars(e kernelbridge.LoopEffect, current []kernelbridge.LoopEffect) kernelbridge.LoopEffect {
	switch e.Kind {
	case kernelbridge.LoopEffectVar:
		if e.Index >= 0 && e.Index < len(current) {
			return current[e.Index]
		}
		return e
	case kernelbridge.LoopEffectConst, kernelbridge.LoopEffectUnknown:
		return e
	case kernelbridge.LoopEffectUnary:
		out := e
		a := SubstituteVars(*e.A, current)
		out.A = &a
		return out
	case kernelbridge.LoopEffectBinary, kernelbridge.LoopEffectJoin:
		out := e
		a := SubstituteVars(*e.A, current)
		b := SubstituteVars(*e.B, current)
		out.A = &a
		out.B = &b
		return out
	}
	panic("SubstituteVars: unreached kind")
}

// EffectNodeBudget is EFFECT_NODE_BUDGET in the TS source: the
// pass-entry substitution budget — past this, the composed effect
// stops paying for itself and the reading declines.
const EffectNodeBudget = 96

// FoldBody is foldBody in the TS source: a statement run folded into
// the per-binding effects — assignments substitute-and-set (later
// statements read earlier writes), an if/else folds both arms and
// JOINS them per binding — the effect grammar's own control join,
// condition dropped (both arms admitted, sound). Anything else, or a
// composed effect past the budget, ends the reading.
func FoldBody(context *LoweringContext, statements []*ast.Node, current []kernelbridge.LoopEffect) bool {
	for _, s := range statements {
		if assignment, ok := AssignmentOf(context, s); ok {
			composed := SubstituteVars(assignment.Effect, current)
			if EffectNodes(composed) > EffectNodeBudget {
				return false
			}
			current[assignment.Target] = composed
			continue
		}
		if ast.IsIfStatement(s) {
			ifStmt := s.AsIfStatement()
			thenSide := append([]kernelbridge.LoopEffect{}, current...)
			elseSide := append([]kernelbridge.LoopEffect{}, current...)
			if !FoldBody(context, StatementsOf(ifStmt.ThenStatement), thenSide) {
				return false
			}
			if !FoldBody(context, StatementsOf(ifStmt.ElseStatement), elseSide) {
				return false
			}
			for i := range current {
				if effectsEqual(thenSide[i], elseSide[i]) {
					current[i] = thenSide[i]
					continue
				}
				a, b := thenSide[i], elseSide[i]
				joined := kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectJoin, A: &a, B: &b}
				if EffectNodes(joined) > EffectNodeBudget {
					return false
				}
				current[i] = joined
			}
			continue
		}
		return false
	}
	return true
}

// effectsEqual is the Go stand-in for the TS source's `===` identity
// test on two LoopEffect values in the "unchanged" fast path
// (thenSide[i] === elseSide[i] when foldBody left the same
// substituted value on both arms, e.g. a binding neither arm wrote).
// The TS source compares object identity, which in Go's per-call
// SubstituteVars means "same Kind and same Index/Set/Op" for a leaf,
// or recursively for a composed effect — the values ARE structurally
// identical whenever the TS identity check would have held, since
// each unwritten binding threads the exact same current[i] value
// through both recursive FoldBody calls.
func effectsEqual(a, b kernelbridge.LoopEffect) bool {
	if a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case kernelbridge.LoopEffectVar:
		return a.Index == b.Index
	case kernelbridge.LoopEffectConst:
		return setsEqualForFold(a.Set, b.Set)
	case kernelbridge.LoopEffectUnknown:
		return true
	case kernelbridge.LoopEffectUnary:
		return a.Op == b.Op && effectsEqual(*a.A, *b.A)
	case kernelbridge.LoopEffectBinary, kernelbridge.LoopEffectJoin:
		return a.Op == b.Op && effectsEqual(*a.A, *b.A) && effectsEqual(*a.B, *b.B)
	}
	return false
}

func setsEqualForFold(a, b refinementsets.RefinedSet) bool {
	return kernelbridge.EncodeSet(a) == kernelbridge.EncodeSet(b)
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
		case kernelbridge.IrStatementBranch:
			if RaisesDone(s.Then, done) || RaisesDone(s.Else, done) {
				return true
			}
		}
	}
	return false
}

// LoopStatement is loopStatement in the TS source: the IR loop
// statement from a prepared head and body.
func LoopStatement(context *LoweringContext, head LoopHead, body LoopBody) kernelbridge.IrStatement {
	cond := make([]*refinementsets.RefinedSet, len(context.Bindings))
	after := make([]*refinementsets.RefinedSet, len(context.Bindings))
	for i := range context.Bindings {
		if i == head.On {
			cond[i] = head.Cond
			after[i] = head.After
		}
	}
	return kernelbridge.IrStatement{
		Kind:    kernelbridge.IrStatementLoop,
		Written: body.Written,
		Cond:    cond,
		After:   after,
		Body:    body.Effects,
	}
}
