// from evaluation/evaluate_operators.ts
//
// The operators, and what they do to what is known. Arithmetic is a
// TRANSFER question to the kernel — the set an operator produces from
// its operands' sets — never arithmetic performed here; the string
// side of `+` concatenates codepoint tuples; the comparisons ask
// membership (comparison.ts). The rest of this file is bookkeeping
// about writes: an assignment is a write, a compound assignment is a
// read and a write, and `++` is both with the order that tells them
// apart.
//
// Short-circuits and the ternary evaluate ONE side where the other is
// decided, which is what makes `x && x.y` read as anything at all.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/typereading"
)

// ReadUnary is readUnary in the TS source: `-x`, `!x`, `~x`, `+x`,
// and the steps `++`/`--` in both positions — or (AbstractValue{},
// false) where the operator is none of them.
func ReadUnary(ctx *FlowContext, env Env, e *ast.Node) (abstractdomain.AbstractValue, bool) {
	if ast.IsPrefixUnaryExpression(e) {
		unary := e.AsPrefixUnaryExpression()
		if unary.Operator == ast.KindMinusToken {
			operand := evaluateExpression(ctx, env, unary.Operand)
			negated := TransferNegate(operand)
			if negated.Kind == abstractdomain.KindUnknown {
				return abstractdomain.UnknownOver([]abstractdomain.AbstractValue{operand}), true
			}
			return negated, true
		}
		// `!x` where ToBoolean of the operand is pinned (a transcribed
		// spec table — the ledger dips to spec); an unpinned operand
		// still answers a BOOLEAN — ToBoolean is total
		// (sec-logical-not-operator)
		if unary.Operator == ast.KindExclamationToken {
			operand := evaluateExpression(ctx, env, unary.Operand)
			verdict, known := abstractdomain.Truthiness(operand)
			if !known {
				return abstractdomain.KnownSet(
					refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{0, 1})),
					nil,
					abstractdomain.TrustSpec,
					abstractdomain.SetKindTagNone,
				), true
			}
			v := float64(1)
			if verdict {
				v = 0
			}
			return abstractdomain.KnownValues(
				[]float64{v},
				abstractdomain.PrimitiveBoolean,
				abstractdomain.MinTrustLevel(abstractdomain.TrustLevelOf(operand), abstractdomain.TrustSpec),
			), true
		}
		// `~x` IS ToInt32's complement — x ^ -1, the bitXor question the
		// kernel already answers
		if unary.Operator == ast.KindTildeToken {
			operand := evaluateExpression(ctx, env, unary.Operand)
			complemented := TransferBitwise(BitXor, operand, abstractdomain.KnownValues([]float64{-1}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved))
			if complemented.Kind == abstractdomain.KindUnknown {
				return abstractdomain.UnknownOver([]abstractdomain.AbstractValue{operand}), true
			}
			return complemented, true
		}
		// `+x` is ToNumber (sec-unary-plus-operator): already-numeric
		// knowledge passes through unchanged, NaN stays NaN. A known
		// string reads through StringToNumber's core grammar (conv.2),
		// kernel-side, exact or correctly rounded — never a decline
		// (StringToNumber is total: unparseable answers NaN). An object
		// operand still needs the ToPrimitive grammar this file does not
		// carry, so it stays unread.
		if unary.Operator == ast.KindPlusToken {
			operand := evaluateExpression(ctx, env, unary.Operand)
			if operand.Kind == abstractdomain.KindNaN {
				return abstractdomain.NaNValue, true
			}
			if operand.Kind == abstractdomain.KindPossiblyNaN {
				return operand, true
			}
			if operand.Kind == abstractdomain.KindValues && operand.KindTag == abstractdomain.PrimitiveNumber {
				return operand, true
			}
			if operand.Kind == abstractdomain.KindSet && operand.SetKindTag == abstractdomain.SetKindTagNone {
				return operand, true
			}
			if operand.Kind == abstractdomain.KindValues && operand.KindTag == abstractdomain.PrimitiveString {
				if parsed, ok := stringToNumberOfKnown(ctx, operand); ok {
					return parsed, true
				}
			}
			return abstractdomain.UnknownOver([]abstractdomain.AbstractValue{operand}), true
		}
	}
	if stepped, ok := ReadStepUnary(ctx, env, e); ok {
		return stepped, true
	}
	return abstractdomain.AbstractValue{}, false
}

// stringToNumberOfKnown asks the kernel's StringToNumber transfer
// (conv.2, TransferOpStringToNumber) for a known string's ToNumber
// image: `js.stringToNumber`'s Lean arm reads the word, parses conv.2's
// core grammar, and answers NaN (unparseable — the grammar excludes
// numeric separators, so `"1_2"` is NaN same as `"abc"`) or the exact
// (or correctly rounded) value through the same nan/values vocabulary
// every other transfer answers in. `ok` is false only where the
// operand does not read as one concrete word (SetOfKnown's refusal —
// not reachable for a PrimitiveString KindValues operand in practice)
// or the kernel itself is absent/refuses, in which case the caller's
// existing decline stands.
func stringToNumberOfKnown(ctx *FlowContext, operand abstractdomain.AbstractValue) (abstractdomain.AbstractValue, bool) {
	if ctx.Kernel == nil {
		return abstractdomain.AbstractValue{}, false
	}
	set, ok := abstractdomain.SetOfKnown(operand)
	if !ok {
		return abstractdomain.AbstractValue{}, false
	}
	var result abstractdomain.AbstractValue
	answered := func() (ok bool) {
		defer func() {
			if r := recover(); r != nil {
				ok = false
			}
		}()
		answer := ctx.Kernel.Transfer(kernelbridge.TransferQuestion{
			Op: kernelbridge.TransferOpStringToNumber,
			A:  set,
		})
		result = abstractdomain.AtTrustLevel(KnownOfAnswer(answer), abstractdomain.TrustLevelOf(operand))
		return true
	}()
	return result, answered
}

// ReadConditional is readConditional in the TS source: `c ? a : b` —
// one side where the condition decides, or where a narrowing makes
// the other impossible; otherwise both sides read under everything
// their side of the condition proves (assume.ts — narrowings, value
// copies, the condition's rows), and join.
func ReadConditional(ctx *FlowContext, env Env, e *ast.Node) abstractdomain.AbstractValue {
	cond := e.AsConditionalExpression()
	conditionKnown := evaluateExpression(ctx, env, cond.Condition)
	verdict, hasVerdict := abstractdomain.Truthiness(conditionKnown)
	assumed := assumeCondition(ctx, env, cond.Condition, AssumeConditionScope{
		WhenTrueScope:  cond.WhenTrue,
		WhenFalseScope: cond.WhenFalse,
		At:             e,
	}, verdict, hasVerdict)
	// a DECIDED or contradicted side never evaluates — the other side
	// IS the value, read under its own narrowings
	if assumed.WhenTrue.Dead && !assumed.WhenFalse.Dead {
		return evaluateExpression(assumed.WhenFalse.Ctx, assumed.WhenFalse.Env, cond.WhenFalse)
	}
	if assumed.WhenFalse.Dead && !assumed.WhenTrue.Dead {
		return evaluateExpression(assumed.WhenTrue.Ctx, assumed.WhenTrue.Env, cond.WhenTrue)
	}
	return abstractdomain.JoinKnown(
		evaluateExpression(assumed.WhenTrue.Ctx, assumed.WhenTrue.Env, cond.WhenTrue),
		evaluateExpression(assumed.WhenFalse.Ctx, assumed.WhenFalse.Env, cond.WhenFalse),
	)
}

// ReadBinary is readBinary in the TS source: every binary operator:
// the assignments and compound assignments, the short-circuits, the
// comparisons, and the arithmetic.
func ReadBinary(ctx *FlowContext, env Env, e *ast.Node) abstractdomain.AbstractValue {
	bin := e.AsBinaryExpression()
	if assigned, ok := ReadAssignment(ctx, env, e); ok {
		return assigned
	}
	if indexed, ok := ReadIndexedWrite(ctx, env, e); ok {
		return indexed
	}
	if indexedCompound, ok := ReadIndexedCompoundWrite(ctx, env, e); ok {
		return indexedCompound
	}
	if forgotten, ok := ReadForgottenAssignment(ctx, env, e); ok {
		return forgotten
	}
	// `&&` / `||`: the right side runs under the left side's verdict,
	// so it evaluates on a NARROWED copy — a checked position inside a
	// conjunction sees every conjunct to its left. Where ToBoolean of
	// the left side is pinned, the expression IS one side: a decided
	// short-circuit skips the never-run side entirely, and a decided
	// fall-through evaluates the right side unconditionally. Only an
	// undecided left keeps the conservative rule (right side walked
	// on the narrowed copy, its writes forgotten, result unknown).
	if bin.OperatorToken.Kind == ast.KindAmpersandAmpersandToken || bin.OperatorToken.Kind == ast.KindBarBarToken {
		isAnd := bin.OperatorToken.Kind == ast.KindAmpersandAmpersandToken
		leftKnown := evaluateExpression(ctx, env, bin.Left)
		// a correlation pass decides its own gate the assumed way
		verdict, hasVerdict := abstractdomain.Truthiness(leftKnown)
		if !hasVerdict {
			assumedV, assumedOk := AssumedVerdict(ctx, ctx.GateAssumptions, bin.Left)
			verdict, hasVerdict = assumedV, assumedOk
		}
		if hasVerdict {
			wantsAnd := isAnd && !verdict
			wantsOr := !isAnd && verdict
			if wantsAnd || wantsOr {
				return leftKnown
			}
			return evaluateExpression(ctx, env, bin.Right)
		}
		// the right side runs where the left HELD (&&) or FAILED (||) —
		// the assume operator, so it also sees the left side's rows,
		// value copies, and length guards
		assumed := assumeCondition(ctx, env, bin.Left, AssumeConditionScope{
			WhenTrueScope:  bin.Right,
			WhenFalseScope: bin.Right,
			At:             e,
		}, verdict, hasVerdict)
		side := assumed.WhenFalse
		if isAnd {
			side = assumed.WhenTrue
		}
		rightKnown := evaluateExpression(side.Ctx, side.Env, bin.Right)
		havocAssigned(ctx, env, bin.Right)
		// a boolean-sorted left has ONE truthy value and one falsy one,
		// so even an undecided short-circuit is a union of two known
		// sides: `flag || x` is true-or-x, `flag && x` false-or-x.
		// The held knowledge decides the sort where it pins one — only
		// an undecided holding asks the host's type
		var heldBoolean bool
		heldBooleanKnown := true
		switch {
		case leftKnown.Kind == abstractdomain.KindValues:
			heldBoolean = leftKnown.KindTag == abstractdomain.PrimitiveBoolean
		case leftKnown.Kind == abstractdomain.KindObject || leftKnown.Kind == abstractdomain.KindList ||
			leftKnown.Kind == abstractdomain.KindArrayHoles ||
			leftKnown.Kind == abstractdomain.KindCollection || leftKnown.Kind == abstractdomain.KindDate ||
			leftKnown.Kind == abstractdomain.KindRegex || leftKnown.Kind == abstractdomain.KindPromise:
			heldBoolean = false
		case leftKnown.Kind == abstractdomain.KindSet:
			// a refinement set is never boolean-sorted UNLESS it spells a
			// subset of {0,1} — the boolean encoding — where only the type
			// can say which sort wrote it
			if leftKnown.SetKindTag != abstractdomain.SetKindTagNone || !isZeroOneOneOf(leftKnown.Set) {
				heldBoolean = false
			} else {
				heldBooleanKnown = false
			}
		default:
			heldBooleanKnown = false
		}
		if !heldBooleanKnown {
			heldBoolean = (typereading.TypeAtLocation(ctx.P.Checker, bin.Left).Flags() & checker.TypeFlagsBooleanLike) != 0
		}
		if heldBoolean {
			v := float64(0)
			if !isAnd {
				v = 1
			}
			return abstractdomain.JoinKnown(abstractdomain.KnownValues([]float64{v}, abstractdomain.PrimitiveBoolean, abstractdomain.TrustProved), rightKnown)
		}
		// an undecided short-circuit still answers the UNION of its two
		// sides: the result IS the left value (on the half that decides)
		// or the right one, so their join over-approximates every run.
		// For `||` an absent or NaN left is falsy and never survives —
		// those wrappers strip before the join.
		leftForJoin := leftKnown
		if !isAnd {
			for leftForJoin.Kind == abstractdomain.KindPossiblyNaN || leftForJoin.Kind == abstractdomain.KindPossiblyUndefined {
				leftForJoin = *leftForJoin.Inner
			}
		}
		joined := abstractdomain.JoinKnown(leftForJoin, rightKnown)
		if joined.Kind != abstractdomain.KindUnknown {
			return joined
		}
		return abstractdomain.UnknownOver([]abstractdomain.AbstractValue{leftKnown, rightKnown})
	}
	// `??`: decided by PRESENCE — a known-absent left side falls
	// through (the right side runs unconditionally), a known-present
	// one short-circuits; a maybe joins its inner value with the
	// conditionally-run right side
	if bin.OperatorToken.Kind == ast.KindQuestionQuestionToken {
		leftKnown := evaluateExpression(ctx, env, bin.Left)
		if leftKnown.Kind == abstractdomain.KindUndef {
			return evaluateExpression(ctx, env, bin.Right)
		}
		switch leftKnown.Kind {
		case abstractdomain.KindValues, abstractdomain.KindObject, abstractdomain.KindNaN, abstractdomain.KindSet, abstractdomain.KindList:
			return leftKnown
		}
		if leftKnown.Kind == abstractdomain.KindPossiblyUndefined {
			rightEnv := env.Clone()
			rightKnown := evaluateExpression(ctx, rightEnv, bin.Right)
			havocAssigned(ctx, env, bin.Right)
			return abstractdomain.JoinKnown(*leftKnown.Inner, rightKnown)
		}
		rightEnv := env.Clone()
		rightKnown := evaluateExpression(ctx, rightEnv, bin.Right)
		havocAssigned(ctx, env, bin.Right)
		return abstractdomain.UnknownOver([]abstractdomain.AbstractValue{leftKnown, rightKnown})
	}
	if inKnown, ok := ReadInKeyword(ctx, env, e); ok {
		return inKnown
	}
	// `a, b` evaluates the left for its effects and answers the right
	// (sec-comma-operator-runtime-semantics-evaluation)
	if bin.OperatorToken.Kind == ast.KindCommaToken {
		evaluateExpression(ctx, env, bin.Left)
		return evaluateExpression(ctx, env, bin.Right)
	}
	if instanceKnown, ok := ReadInstanceOf(ctx, env, e); ok {
		return instanceKnown
	}
	left := evaluateExpression(ctx, env, bin.Left)
	right := evaluateExpression(ctx, env, bin.Right)
	if bitwise, ok := ReadBitwise(left, right, bin.OperatorToken.Kind); ok {
		return bitwise
	}
	if compared, ok := ReadComparison(ctx, e, left, right); ok {
		return compared
	}
	return ReadBinaryArithmetic(ctx, env, e, left, right)
}

// isZeroOneOneOf mirrors the TS source's inline check: exactly one
// oneOf form whose members are all 0 or 1 — the boolean encoding a
// bare refinement set can spell.
func isZeroOneOneOf(set refinementsets.RefinedSet) bool {
	if len(set.Forms) != 1 || set.Forms[0].Form != refinementsets.FormOneOf {
		return false
	}
	for _, w := range set.Forms[0].W {
		if w != 0 && w != 1 {
			return false
		}
	}
	return true
}
