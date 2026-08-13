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
	case kernelbridge.LoopEffectVar, kernelbridge.LoopEffectConst,
		kernelbridge.LoopEffectConstState, kernelbridge.LoopEffectUnknown:
		return 1
	case kernelbridge.LoopEffectUnary, kernelbridge.LoopEffectOrAbsent:
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
	case kernelbridge.LoopEffectUnary, kernelbridge.LoopEffectOrAbsent:
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
	case kernelbridge.LoopEffectUnary, kernelbridge.LoopEffectOrAbsent:
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

// MapForOfLowering is a for-of over a flattened Map or Set, lowered as
// the ordinary loop exactly the way ArrayForOfLowering lowers an array's
// — the per-pass binding effects are slot vars, the rest of the body
// folds through FoldBody, and NO numeric head bounds the trip count (the
// collection's size is not related to any binding by the flattening), so
// every cond and after entry stays nil and the solver certifies whatever
// the body's effects support.
//
// The recognized heads:
//
//   - `for (const v of s)` and `for (const v of m.values())` — the
//     binding takes the value slot's var each pass.
//   - `for (const k of m.keys())` — the key slot's var.
//   - `for (const [k, v] of m)` and `of m.entries()` — an array pattern
//     of EXACTLY two plain identifiers, k taking the key slot and v the
//     value slot.
//
// Declines where the binding is not one of those shapes, where the
// collection is not flattened, or where the body leaves the fold's
// grammar.
func MapForOfLowering(context *LoweringContext, statement *ast.Node) (kernelbridge.IrStatement, bool) {
	if !ast.IsForOfStatement(statement) {
		return kernelbridge.IrStatement{}, false
	}
	forOf := statement.AsForInOrOfStatement()
	// `for await (… of …)` awaits each element — the value the binding
	// takes is the awaited one, which the value slot does not hold
	if forOf.AwaitModifier != nil {
		return kernelbridge.IrStatement{}, false
	}
	if forOf.Initializer == nil || !ast.IsVariableDeclarationList(forOf.Initializer) {
		return kernelbridge.IrStatement{}, false
	}
	declarations := forOf.Initializer.AsVariableDeclarationList().Declarations.Nodes
	if len(declarations) != 1 {
		return kernelbridge.IrStatement{}, false
	}
	bindingName := declarations[0].AsVariableDeclaration().Name()
	// the fold starts every binding at its own var; the loop's own
	// bindings then take their per-pass slot reads
	current := make([]kernelbridge.LoopEffect, len(context.Bindings))
	for index := range current {
		current[index] = varEffect(index)
	}
	// per-pass writes, collected first so the two shapes share one path
	type perPass struct{ target, source int }
	var passes []perPass
	if ast.IsIdentifier(bindingName) {
		slot, pairIterated, ok := MapIterationSlotOf(context, forOf.Expression)
		if !ok || pairIterated {
			return kernelbridge.IrStatement{}, false
		}
		target, found := slotIndexOfName(context, bindingName.Text())
		if !found {
			return kernelbridge.IrStatement{}, false
		}
		passes = append(passes, perPass{target: target, source: slot})
	} else if ast.IsArrayBindingPattern(bindingName) {
		keysSlot, valsSlot, ok := MapEntrySlotsOf(context, forOf.Expression)
		if !ok {
			return kernelbridge.IrStatement{}, false
		}
		elements := bindingName.AsBindingPattern().Elements.Nodes
		if len(elements) != 2 {
			return kernelbridge.IrStatement{}, false
		}
		sources := []int{keysSlot, valsSlot}
		for index, element := range elements {
			binding := element.AsBindingElement()
			if binding.DotDotDotToken != nil || binding.Initializer != nil ||
				binding.PropertyName != nil || !ast.IsIdentifier(binding.Name()) {
				return kernelbridge.IrStatement{}, false
			}
			target, found := slotIndexOfName(context, binding.Name().Text())
			if !found {
				return kernelbridge.IrStatement{}, false
			}
			passes = append(passes, perPass{target: target, source: sources[index]})
		}
	} else {
		return kernelbridge.IrStatement{}, false
	}
	for _, pass := range passes {
		if pass.target >= len(current) || pass.source >= len(current) {
			return kernelbridge.IrStatement{}, false
		}
		current[pass.target] = varEffect(pass.source)
	}
	if !FoldBody(context, StatementsOf(forOf.Statement), current) {
		return kernelbridge.IrStatement{}, false
	}
	written := make([]bool, len(current))
	for index, effect := range current {
		written[index] = !(effect.Kind == kernelbridge.LoopEffectVar && effect.Index == index)
	}
	return kernelbridge.IrStatement{
		Kind:    kernelbridge.IrStatementLoop,
		Written: written,
		Cond:    make([]*refinementsets.RefinedSet, len(current)),
		After:   make([]*refinementsets.RefinedSet, len(current)),
		Body:    current,
	}, true
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
