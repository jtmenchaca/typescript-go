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

// compoundBitwiseOperator maps the six bitwise/shift compound tokens
// (`&= |= ^= <<= >>= >>>=`) to the BitwiseOperator TransferBitwise
// reads (bitwise_transfer.go) — compoundOperator's own NumericOperator
// table has no bitwise arm (NumericOperator is the "+ - * / %" union
// only), so a compound bitwise token needs this separate table rather
// than an addition to that one. AssignmentOperator's own grammar row
// (tmp/ecma262/spec.html sec-assignment-operators, `*= /= %= += -= <<=
// >>= >>>= &= ^= |= **=`) lists these beside the arithmetic compounds,
// and ApplyStringOrNumericBinaryOperator (the abstract operation every
// AssignmentOperator's alg step runs) carries the bitwise ops as
// ordinary table entries, so `age &= 0xff` runs the identical
// GetValue/rightRef/PutValue shape `age += 5` does — reading a
// bitwise-shaped op here loses nothing the arithmetic table has.
func compoundBitwiseOperator(kind ast.Kind) (BitwiseOperator, bool) {
	switch kind {
	case ast.KindAmpersandEqualsToken:
		return BitAnd, true
	case ast.KindBarEqualsToken:
		return BitOr, true
	case ast.KindCaretEqualsToken:
		return BitXor, true
	case ast.KindLessThanLessThanEqualsToken:
		return Shl, true
	case ast.KindGreaterThanGreaterThanEqualsToken:
		return Sar, true
	case ast.KindGreaterThanGreaterThanGreaterThanEqualsToken:
		return Shr, true
	default:
		return "", false
	}
}

// compoundResult is the one arithmetic choice every compound-assign
// route makes from a token, a before-value, and a right-value: string
// concatenation for `+=` between string-sorted operands, the numeric
// transfer for the five NumericOperator tokens, the bitwise transfer
// for the six bitwise/shift tokens, TransferPow for `**=`, or silence
// for anything else. leftNode/rightNode are effect-free syntax used
// only to decide string-sortedness (readStringConcatenation's own
// gate) — never re-evaluated.
//
// Shared by the identifier arm, the plain-property arm, and the
// accessor read-modify-write (ir_accessor_calls_read_modify_write.go)
// so the three routes compute the identical value for the identical
// token rather than three hand-kept copies drifting apart.
func compoundResult(ctx *FlowContext, leftNode, rightNode *ast.Node, kind ast.Kind, before, right abstractdomain.AbstractValue) abstractdomain.AbstractValue {
	if kind == ast.KindPlusEqualsToken {
		if concatenated := readStringConcatenation(ctx, leftNode, rightNode, before, right); concatenated != nil {
			return *concatenated
		}
	}
	if op, hasOp := compoundOperator(kind); hasOp {
		return TransferBinary(op, before, right)
	}
	if bitOp, hasBitOp := compoundBitwiseOperator(kind); hasBitOp {
		return TransferBitwise(bitOp, before, right)
	}
	if kind == ast.KindAsteriskAsteriskEqualsToken {
		return TransferPow(before, right)
	}
	return silence.Residue()
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
			if _, ok := env.Get("this"); ok {
				ctx.Aliases.Link(bin.Left.Text(), "this")
			}
		}
		LinkEmbedded(ctx, bin.Left.Text(), bin.Right)
		// a reference read OUT of a tracked holder, or selected from
		// candidates, shares the holder's reference — linked
		if dataflowfacts.ReferenceTyped(ctx.P.Checker, bin.Left) {
			for _, holder := range ProjectionSources(bin.Right, nil) {
				if _, ok := env.Get(holder); ok {
					ctx.Aliases.Link(bin.Left.Text(), holder)
				}
			}
		}
		return value, true
	}
	// `({ a } = x)` / `[a] = xs` — a destructuring ASSIGNMENT pattern,
	// not a declaration. Each bound leaf judges against its own
	// declared type the same way a plain identifier target does
	// (WriteAssignmentPattern reuses WriteBinding per leaf); the
	// pattern's own shape (object vs array literal, nested, rest,
	// default) is read by SlotOf/SlotOfIndex exactly as a declaration
	// pattern's bind does.
	if bin.OperatorToken.Kind == ast.KindEqualsToken &&
		(ast.IsObjectLiteralExpression(bin.Left) || ast.IsArrayLiteralExpression(bin.Left)) {
		value := evaluateExpression(ctx, env, bin.Right)
		WriteAssignmentPattern(ctx, env, bin.Left, value, bin.Right)
		return value, true
	}
	// a `this[S] = value` store under a STABLE symbol const: the value
	// sinks for the field-invariant collection under its #sym: name —
	// the same sink a dotted `this.key = value` feeds — and the held
	// `this` forgets to its invariants, which now carry this field.
	// A symbol key is disjoint from every string key, so the field
	// collection reads the store exactly as it reads a dotted one.
	if bin.OperatorToken.Kind == ast.KindEqualsToken && ast.IsElementAccessExpression(bin.Left) {
		elem := bin.Left.AsElementAccessExpression()
		if elem.Expression.Kind == ast.KindThisKeyword && ctx.ThisWriteSink != nil {
			if fieldName, isSymbolKey := SymbolKeyedFieldName(ctx.P.Checker, bin.Left); isSymbolKey {
				value := evaluateExpression(ctx, env, bin.Right)
				ctx.ThisWriteSink[fieldName] = append(ctx.ThisWriteSink[fieldName], value)
				ForgetThisHeld(ctx, env, e)
				return value, true
			}
		}
	}
	// a PLAIN write through a GET/SET ACCESSOR: the setter body runs
	// with the evaluated right side, exactly as the runtime calls it —
	// checked BEFORE the plain-property arm below, which would otherwise
	// find no "age" key (ConstructedInstance never census-keys an
	// accessor as a plain field) and ADD "age" as a fresh unknown-valued
	// key rather than running the setter (AccessorWalkPlainWrite's own
	// doc comment, ir_accessor_calls_plain_write.go). RESOLUTION is
	// checked before bin.Right evaluates — AccessorSetterTargetOf reads
	// no program state and causes no effect — so a decline here never
	// double-runs bin.Right the way evaluating it speculatively would.
	if bin.OperatorToken.Kind == ast.KindEqualsToken &&
		ast.IsPropertyAccessExpression(bin.Left) &&
		AccessorSetterTargetOf(ctx, env, bin.Left) {
		value := evaluateExpression(ctx, env, bin.Right)
		if next, ok := AccessorWalkPlainWrite(ctx, env, bin.Left, value); ok {
			return next, true
		}
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
	// `a ||= b` / `a &&= b` / `a ??= b` on a tracked identifier: the
	// setter runs (and the right side evaluates) only on the branch the
	// left side's own verdict picks — sec-assignment-operators-runtime-
	// semantics-evaluation's three LogicalAssignment algs (`&&=`: "If
	// ToBoolean(leftValue) is false, return leftValue" then evaluate and
	// PutValue the right side; `||=` the truthy-returns-unchanged dual;
	// `??=`: "If leftValue is neither undefined nor null, return
	// leftValue" then evaluate and write the right side otherwise). A
	// DECIDED left verdict (Truthiness/exact-undef, the same tests
	// ReadBinary's own `&&`/`||`/`??` arms already run) answers the one
	// branch the runtime would actually take — held-unchanged, or the
	// freshly evaluated and written right side — never a join between
	// them, because the unrun branch is not one of the runs the
	// expression can produce. An UNDECIDED verdict falls back to the
	// join both those arms already use for the same reason: the value
	// really is "kept a, or written b", and the join over-approximates
	// whichever the runtime picks.
	// `box.age ||= b` / `box.age &&= b` / `box.age ??= b` through a
	// GET/SET ACCESSOR PAIR: the short-circuit composition of the two
	// facts above — a compound through an accessor runs the getter then
	// the setter, and the logical family's kept branch runs neither.
	// AccessorTargetOf is checked BEFORE bin.Right evaluates, the same
	// resolve-before-evaluate discipline the ordinary accessor-compound
	// arm below follows — AccessorLogicalReadModifyWrite itself decides
	// whether bin.Right ever evaluates, so this call site must not
	// evaluate it first. Tried ahead of the identifier-only arm's own
	// gate (which never matches a property access) and ahead of the
	// plain-property compound arm further down (which would otherwise
	// add "age" as a fresh unknown-valued KEY rather than running the
	// setter — AccessorWalkReadModifyWrite's own doc comment).
	if (bin.OperatorToken.Kind == ast.KindBarBarEqualsToken ||
		bin.OperatorToken.Kind == ast.KindAmpersandAmpersandEqualsToken ||
		bin.OperatorToken.Kind == ast.KindQuestionQuestionEqualsToken) &&
		ast.IsPropertyAccessExpression(bin.Left) &&
		AccessorTargetOf(ctx, env, bin.Left) {
		if next, ok := AccessorLogicalReadModifyWrite(ctx, env, bin.Left, bin.Right, bin.OperatorToken.Kind); ok {
			return next, true
		}
	}
	if (bin.OperatorToken.Kind == ast.KindBarBarEqualsToken ||
		bin.OperatorToken.Kind == ast.KindAmpersandAmpersandEqualsToken ||
		bin.OperatorToken.Kind == ast.KindQuestionQuestionEqualsToken) &&
		ast.IsIdentifier(bin.Left) {
		before, hasBefore := env.Get(bin.Left.Text())
		if !hasBefore {
			before = silence.Residue()
		}
		if bin.OperatorToken.Kind == ast.KindQuestionQuestionEqualsToken {
			// decided by PRESENCE, exactly as ReadBinary's own `??` arm
			// (evaluate_operators.go) decides it — an exact absent left
			// writes the right side outright, an exact present left keeps
			// its own value unwritten and unevaluated, and a maybe joins
			// its present inner with the conditionally-run right
			if before.Kind == abstractdomain.KindUndef {
				right := evaluateExpression(ctx, env, bin.Right)
				WriteBinding(ctx, env, bin.Left.Text(), right, e, "an assigned value")
				return right, true
			}
			switch before.Kind {
			case abstractdomain.KindValues, abstractdomain.KindObject, abstractdomain.KindNaN, abstractdomain.KindSet, abstractdomain.KindList:
				return before, true
			}
			if before.Kind == abstractdomain.KindPossiblyUndefined {
				right := evaluateExpression(ctx, env, bin.Right)
				joined := abstractdomain.JoinKnown(*before.Inner, right)
				WriteBinding(ctx, env, bin.Left.Text(), joined, e, "an assigned value")
				return joined, true
			}
			right := evaluateExpression(ctx, env, bin.Right)
			joined := abstractdomain.UnknownOver([]abstractdomain.AbstractValue{before, right})
			WriteBinding(ctx, env, bin.Left.Text(), joined, e, "an assigned value")
			return joined, true
		}
		// `&&=` / `||=`: decided by TRUTHINESS
		isAnd := bin.OperatorToken.Kind == ast.KindAmpersandAmpersandEqualsToken
		verdict, hasVerdict := abstractdomain.Truthiness(before)
		if hasVerdict {
			wantsAnd := isAnd && !verdict
			wantsOr := !isAnd && verdict
			// the branch that keeps `a` unwritten: the runtime's own
			// GetValue-then-return step, with no PutValue at all — the
			// right side never evaluates
			if wantsAnd || wantsOr {
				return before, true
			}
			right := evaluateExpression(ctx, env, bin.Right)
			WriteBinding(ctx, env, bin.Left.Text(), right, e, "an assigned value")
			return right, true
		}
		// undecided: the join over-approximates both runs the same way
		// ReadBinary's own `&&`/`||` arm does for the bare operator
		right := evaluateExpression(ctx, env, bin.Right)
		joined := abstractdomain.JoinKnown(before, right)
		var next abstractdomain.AbstractValue
		if joined.Kind != abstractdomain.KindUnknown {
			next = joined
		} else {
			next = abstractdomain.UnknownOver([]abstractdomain.AbstractValue{before, right})
		}
		WriteBinding(ctx, env, bin.Left.Text(), next, e, "an assigned value")
		return next, true
	}
	// a compound write through a GET/SET ACCESSOR PAIR runs both bodies —
	// the getter's read, the arithmetic, the setter's write — the same
	// read-modify-write the runtime performs
	// (sec-assignment-operators-runtime-semantics-evaluation's
	// AssignmentOperator alg: GetValue(leftRef) before rightRef, then
	// PutValue). RESOLUTION is checked BEFORE the right side evaluates —
	// AccessorTargetOf reads no program state and causes no effect, so
	// trying it first never double-runs bin.Right the way evaluating it
	// speculatively would. A receiver whose property resolves to an
	// accessor never falls into the plain-property arm below, which
	// would otherwise add "age" as a fresh unknown-valued KEY rather
	// than running the setter (AccessorWalkReadModifyWrite's own doc
	// comment, ir_accessor_calls_read_modify_write.go). The three
	// LogicalAssignment tokens are excluded HERE on purpose — they
	// already matched the arm above this one, which runs the setter only
	// on the branch the getter's own verdict picks.
	if bin.OperatorToken.Kind >= ast.KindFirstCompoundAssignment &&
		bin.OperatorToken.Kind <= ast.KindLastCompoundAssignment &&
		bin.OperatorToken.Kind != ast.KindBarBarEqualsToken &&
		bin.OperatorToken.Kind != ast.KindAmpersandAmpersandEqualsToken &&
		bin.OperatorToken.Kind != ast.KindQuestionQuestionEqualsToken &&
		ast.IsPropertyAccessExpression(bin.Left) &&
		AccessorTargetOf(ctx, env, bin.Left) {
		right := evaluateExpression(ctx, env, bin.Right)
		if next, ok := AccessorWalkReadModifyWrite(ctx, env, bin.Left, bin.Right, bin.OperatorToken.Kind, right); ok {
			return next, true
		}
	}
	// a compound write through a property transfers the same way
	if bin.OperatorToken.Kind >= ast.KindFirstCompoundAssignment &&
		bin.OperatorToken.Kind <= ast.KindLastCompoundAssignment &&
		ast.IsPropertyAccessExpression(bin.Left) {
		right := evaluateExpression(ctx, env, bin.Right)
		before := evaluateExpression(ctx, env, bin.Left)
		next := compoundResult(ctx, bin.Left, bin.Right, bin.OperatorToken.Kind, before, right)
		WriteProperty(ctx, env, bin.Left, next, e)
		return next, true
	}
	// compound assignment transfers where the operator does
	if bin.OperatorToken.Kind >= ast.KindFirstCompoundAssignment &&
		bin.OperatorToken.Kind <= ast.KindLastCompoundAssignment &&
		ast.IsIdentifier(bin.Left) {
		right := evaluateExpression(ctx, env, bin.Right)
		before, ok := env.Get(bin.Left.Text())
		if !ok {
			before = silence.Residue()
		}
		next := compoundResult(ctx, bin.Left, bin.Right, bin.OperatorToken.Kind, before, right)
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
				held, ok := env.Get(unary.Operand.Text())
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
				before, ok := env.Get(unary.Operand.Text())
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
