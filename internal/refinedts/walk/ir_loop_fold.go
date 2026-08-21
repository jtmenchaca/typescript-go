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
	case kernelbridge.LoopEffectVar, kernelbridge.LoopEffectVarState,
		kernelbridge.LoopEffectSquare,
		kernelbridge.LoopEffectConst,
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
	// LoopEffectSquare names "slot Index, squared" — substituting the
	// slot's CURRENT effect in for a plain var swaps the whole effect;
	// a square cannot do that in general, since the wire's `sq` shape
	// only ever takes an index, never an arbitrary operand. Where the
	// current effect is itself a bare var (the common case: no write to
	// the squared name happened since entry, or the write was a plain
	// copy), the square stays exact and cheap over that var's own
	// index. Anywhere else — the slot's current value is already a
	// composed effect — there is no `sq`-shaped wire for "this composed
	// effect, squared", so it falls back to the general product of the
	// substituted effect with itself, the same claim the pre-`sq` mul
	// lowering always gave.
	case kernelbridge.LoopEffectSquare:
		if e.Index >= 0 && e.Index < len(current) {
			substituted := current[e.Index]
			if substituted.Kind == kernelbridge.LoopEffectVar {
				return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectSquare, Index: substituted.Index}
			}
			a, b := substituted, substituted
			return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectBinary, Op: kernelbridge.LoopOpMul, A: &a, B: &b}
		}
		return e
	// LoopEffectVarState never reaches here in practice — every producer
	// of a verbatim copy (destructuring, record reassignment, chained
	// assignment, summary ret threading) builds statements outside
	// AssignmentOf/AssignmentOfExpression, the only readers FoldBody
	// calls — but if one ever does, it is left unchanged rather than
	// substituted as a live binding reference: unlike `var i`, a
	// `varState i` already names a SPECIFIC resolved source slot's whole
	// state, not "whatever this pass currently holds for i".
	case kernelbridge.LoopEffectVarState:
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
	case kernelbridge.LoopEffectVar, kernelbridge.LoopEffectVarState, kernelbridge.LoopEffectSquare:
		return a.Index == b.Index
	case kernelbridge.LoopEffectConst:
		return setsEqualForFold(a.Set, b.Set)
	case kernelbridge.LoopEffectConstState:
		return a.Undef == b.Undef && a.Null == b.Null && a.Nan == b.Nan && setsEqualForFold(a.Set, b.Set)
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
