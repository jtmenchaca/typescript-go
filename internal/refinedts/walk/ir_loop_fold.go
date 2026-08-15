// split from ir_loop.go — effect folding: size, substitution, the
// statement-run fold, and the structural equality it leans on

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// EffectNodes is effectNodes in the TS source: effect size, for the
// substitution budget.
func EffectNodes(e kernelbridge.LoopEffect) int {
	switch e.Kind {
	case kernelbridge.LoopEffectVar, kernelbridge.LoopEffectConst,
		kernelbridge.LoopEffectConstState, kernelbridge.LoopEffectUnknown:
		return 1
	case kernelbridge.LoopEffectUnary, kernelbridge.LoopEffectOrAbsent,
		kernelbridge.LoopEffectSeqUnary, kernelbridge.LoopEffectSeqNum:
		return 1 + EffectNodes(*e.A)
	case kernelbridge.LoopEffectBinary, kernelbridge.LoopEffectConcat,
		kernelbridge.LoopEffectJoin:
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
	case kernelbridge.LoopEffectConst, kernelbridge.LoopEffectConstState,
		kernelbridge.LoopEffectUnknown:
		return e
	case kernelbridge.LoopEffectUnary, kernelbridge.LoopEffectOrAbsent,
		kernelbridge.LoopEffectSeqUnary, kernelbridge.LoopEffectSeqNum:
		out := e
		a := SubstituteVars(*e.A, current)
		out.A = &a
		return out
	case kernelbridge.LoopEffectBinary, kernelbridge.LoopEffectConcat,
		kernelbridge.LoopEffectJoin:
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
	case kernelbridge.LoopEffectConstState:
		return a.Absent == b.Absent && a.Nan == b.Nan && setsEqualForFold(a.Set, b.Set)
	case kernelbridge.LoopEffectUnknown:
		return true
	case kernelbridge.LoopEffectUnary, kernelbridge.LoopEffectOrAbsent,
		kernelbridge.LoopEffectSeqUnary, kernelbridge.LoopEffectSeqNum:
		return a.Op == b.Op && effectsEqual(*a.A, *b.A)
	case kernelbridge.LoopEffectBinary, kernelbridge.LoopEffectConcat,
		kernelbridge.LoopEffectJoin:
		return a.Op == b.Op && effectsEqual(*a.A, *b.A) && effectsEqual(*a.B, *b.B)
	}
	return false
}

func setsEqualForFold(a, b refinementsets.RefinedSet) bool {
	return kernelbridge.EncodeSet(a) == kernelbridge.EncodeSet(b)
}
