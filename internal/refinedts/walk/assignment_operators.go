// from evaluation/assignment_operators.ts
//
// Assignment and compound assignment write through a binding or a
// property; `++`/`--` are the same step with the order that tells
// prefix from postfix. Compound forms transfer through the arithmetic
// operators, then write the result.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

func compoundOperator(kind ast.Kind) (NumericOperator, bool) {
	switch kind {
	case ast.KindPlusEqualsToken:
		return OpAdd, true
	case ast.KindMinusEqualsToken:
		return OpSub, true
	case ast.KindAsteriskEqualsToken:
		return OpMul, true
	case ast.KindSlashEqualsToken:
		return OpDiv, true
	case ast.KindPercentEqualsToken:
		return OpRem, true
	default:
		return "", false
	}
}

// ReadAssignment is readAssignment in the TS source: simple and
// compound writes through a name or a property — or (AbstractValue{},
// false) where the operator is not one of those forms.
func ReadAssignment(ctx *FlowContext, env Env, e *ast.Node) (abstractdomain.AbstractValue, bool) {
	bin := e.AsBinaryExpression()
	if bin.OperatorToken.Kind == ast.KindEqualsToken && ast.IsIdentifier(bin.Left) {
		value := evaluateExpression(ctx, env, bin.Right)
		// checked AT the right side: its contextual type is the
		// declared binding's type, which carries the admitted sorts
		WriteBinding(ctx, env, bin.Left.Text(), value, bin.Right, "an assigned value")
		// binding a reference to another name: the two now share it —
		// directly, or embedded inside a literal. `x = this` links the
		// tracked instance the same way.
		if ast.IsIdentifier(bin.Right) && dataflowfacts.ReferenceTyped(ctx.P.Checker, bin.Right) {
			ctx.Aliases.Link(bin.Left.Text(), bin.Right.Text())
		}
		if bin.Right.Kind == ast.KindThisKeyword {
			if _, ok := env["this"]; ok {
				ctx.Aliases.Link(bin.Left.Text(), "this")
			}
		}
		LinkEmbedded(ctx, bin.Left.Text(), bin.Right)
		// a reference read OUT of a tracked holder, or selected from
		// candidates, shares the holder's reference — linked
		if dataflowfacts.ReferenceTyped(ctx.P.Checker, bin.Left) {
			for _, holder := range ProjectionSources(bin.Right, nil) {
				if _, ok := env[holder]; ok {
					ctx.Aliases.Link(bin.Left.Text(), holder)
				}
			}
		}
		return value, true
	}
	// a write THROUGH a property: `obj.key = v`. The object's facts
	// are only as good as its keys, so the key takes the new value —
	// and where the path is not a plain `name.key`, the whole object
	// (and every name sharing it) forgets.
	if bin.OperatorToken.Kind == ast.KindEqualsToken && ast.IsPropertyAccessExpression(bin.Left) {
		pa := bin.Left.AsPropertyAccessExpression()
		value := evaluateExpression(ctx, env, bin.Right)
		// a `this.key = value` write sinks for the field-invariant
		// collection (fields.ts)
		if pa.Expression.Kind == ast.KindThisKeyword && ctx.ThisWriteSink != nil {
			held, ok := ctx.ThisWriteSink[pa.Name().Text()]
			if !ok {
				ctx.ThisWriteSink[pa.Name().Text()] = []abstractdomain.AbstractValue{value}
			} else {
				ctx.ThisWriteSink[pa.Name().Text()] = append(held, value)
			}
		}
		// storing a tracked reference UNDER A KEY aliases the holder
		// with the stored name
		if ast.IsIdentifier(pa.Expression) && ast.IsIdentifier(bin.Right) && dataflowfacts.ReferenceTyped(ctx.P.Checker, bin.Right) {
			ctx.Aliases.Link(pa.Expression.Text(), bin.Right.Text())
		}
		WriteProperty(ctx, env, bin.Left, value, bin.Right)
		return value, true
	}
	// a compound write through a property transfers the same way
	if bin.OperatorToken.Kind >= ast.KindFirstCompoundAssignment &&
		bin.OperatorToken.Kind <= ast.KindLastCompoundAssignment &&
		ast.IsPropertyAccessExpression(bin.Left) {
		right := evaluateExpression(ctx, env, bin.Right)
		op, hasOp := compoundOperator(bin.OperatorToken.Kind)
		before := evaluateExpression(ctx, env, bin.Left)
		var next abstractdomain.AbstractValue
		switch {
		case hasOp:
			next = TransferBinary(op, before, right)
		case bin.OperatorToken.Kind == ast.KindAsteriskAsteriskEqualsToken:
			next = TransferPow(before, right)
		default:
			next = silence.Residue()
		}
		WriteProperty(ctx, env, bin.Left, next, e)
		return next, true
	}
	// compound assignment transfers where the operator does
	if bin.OperatorToken.Kind >= ast.KindFirstCompoundAssignment &&
		bin.OperatorToken.Kind <= ast.KindLastCompoundAssignment &&
		ast.IsIdentifier(bin.Left) {
		right := evaluateExpression(ctx, env, bin.Right)
		op, hasOp := compoundOperator(bin.OperatorToken.Kind)
		before, ok := env[bin.Left.Text()]
		if !ok {
			before = silence.Residue()
		}
		var next abstractdomain.AbstractValue
		switch {
		case hasOp:
			next = TransferBinary(op, before, right)
		case bin.OperatorToken.Kind == ast.KindAsteriskAsteriskEqualsToken:
			next = TransferPow(before, right)
		default:
			next = silence.Residue()
		}
		WriteBinding(ctx, env, bin.Left.Text(), next, e, "an assigned value")
		return next, true
	}
	return abstractdomain.AbstractValue{}, false
}

// ReadForgottenAssignment is readForgottenAssignment in the TS
// source: a write through an index, or any other assignment target
// the checker cannot place: forget what it may have touched.
func ReadForgottenAssignment(ctx *FlowContext, env Env, e *ast.Node) (abstractdomain.AbstractValue, bool) {
	bin := e.AsBinaryExpression()
	if bin.OperatorToken.Kind >= ast.KindFirstAssignment && bin.OperatorToken.Kind <= ast.KindLastAssignment {
		value := evaluateExpression(ctx, env, bin.Right)
		ForgetThrough(ctx, env, bin.Left)
		return value, true
	}
	return abstractdomain.AbstractValue{}, false
}

// ReadStepUnary is readStepUnary in the TS source: `++`/`--` in both
// positions — or (AbstractValue{}, false) where the operator is
// neither.
func ReadStepUnary(ctx *FlowContext, env Env, e *ast.Node) (abstractdomain.AbstractValue, bool) {
	if ast.IsPrefixUnaryExpression(e) {
		unary := e.AsPrefixUnaryExpression()
		if unary.Operator == ast.KindPlusPlusToken || unary.Operator == ast.KindMinusMinusToken {
			step := OpAdd
			if unary.Operator == ast.KindMinusMinusToken {
				step = OpSub
			}
			if ast.IsIdentifier(unary.Operand) {
				held, ok := env[unary.Operand.Text()]
				if !ok {
					held = silence.Residue()
				}
				stepped := TransferBinary(step, held, abstractdomain.KnownValues([]float64{1}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved))
				WriteBinding(ctx, env, unary.Operand.Text(), stepped, e, "an assigned value")
				return stepped, true
			}
			// ++obj.key steps the key through the same write rule
			if ast.IsPropertyAccessExpression(unary.Operand) {
				stepped := TransferBinary(step, evaluateExpression(ctx, env, unary.Operand), abstractdomain.KnownValues([]float64{1}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved))
				WriteProperty(ctx, env, unary.Operand, stepped, e)
				return stepped, true
			}
			// any other target: forget what it may have touched
			ForgetThrough(ctx, env, unary.Operand)
			return silence.Residue(), true
		}
		return abstractdomain.AbstractValue{}, false
	}
	if ast.IsPostfixUnaryExpression(e) {
		unary := e.AsPostfixUnaryExpression()
		if unary.Operator == ast.KindPlusPlusToken || unary.Operator == ast.KindMinusMinusToken {
			step := OpAdd
			if unary.Operator == ast.KindMinusMinusToken {
				step = OpSub
			}
			if ast.IsIdentifier(unary.Operand) {
				before, ok := env[unary.Operand.Text()]
				if !ok {
					before = silence.Residue()
				}
				WriteBinding(
					ctx, env, unary.Operand.Text(),
					TransferBinary(step, before, abstractdomain.KnownValues([]float64{1}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)),
					e, "an assigned value",
				)
				return before, true
			}
			// obj.key++ steps the key; the expression is the value BEFORE
			if ast.IsPropertyAccessExpression(unary.Operand) {
				before := evaluateExpression(ctx, env, unary.Operand)
				WriteProperty(
					ctx, env, unary.Operand,
					TransferBinary(step, before, abstractdomain.KnownValues([]float64{1}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)),
					e,
				)
				return before, true
			}
			ForgetThrough(ctx, env, unary.Operand)
			return silence.Residue(), true
		}
		return abstractdomain.AbstractValue{}, false
	}
	return abstractdomain.AbstractValue{}, false
}
