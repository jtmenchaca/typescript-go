// from evaluation/binary_arithmetic.ts
//
// Numeric binary operators and the string side of `+`. Arithmetic is
// a TRANSFER question to the kernel; string `+` concatenates codepoint
// tuples or concatenation forms. Ordered subtraction and computed-
// difference windows consult the ledger when places are stable.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/primitives"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
	"github.com/microsoft/typescript-go/internal/scanner"
)

func numericOperatorOf(kind ast.Kind) (NumericOperator, bool) {
	switch kind {
	case ast.KindPlusToken:
		return OpAdd, true
	case ast.KindMinusToken:
		return OpSub, true
	case ast.KindAsteriskToken:
		return OpMul, true
	case ast.KindSlashToken:
		return OpDiv, true
	case ast.KindPercentToken:
		return OpRem, true
	// `**` is not here because it is not one operator to this map:
	// it routes through TransferPow — exact on the spec's pinned
	// branches, refused on the implementation-approximated step.
	default:
		return "", false
	}
}

// ReadBitwise is readBitwise in the TS source: bitwise operators —
// Number::toInt32/toUint32 reinterpretations decided in the kernel.
// Left and right are already evaluated. (AbstractValue{}, false) when
// kind is not one of them.
func ReadBitwise(left, right abstractdomain.AbstractValue, kind ast.Kind) (abstractdomain.AbstractValue, bool) {
	var bitwiseOp BitwiseOperator
	switch kind {
	case ast.KindBarToken:
		bitwiseOp = BitOr
	case ast.KindAmpersandToken:
		bitwiseOp = BitAnd
	case ast.KindCaretToken:
		bitwiseOp = BitXor
	case ast.KindLessThanLessThanToken:
		bitwiseOp = Shl
	case ast.KindGreaterThanGreaterThanToken:
		bitwiseOp = Sar
	case ast.KindGreaterThanGreaterThanGreaterThanToken:
		bitwiseOp = Shr
	default:
		return abstractdomain.AbstractValue{}, false
	}
	shifted := TransferBitwise(bitwiseOp, left, right)
	if shifted.Kind == abstractdomain.KindUnknown {
		return abstractdomain.UnknownOver([]abstractdomain.AbstractValue{left, right}), true
	}
	return shifted, true
}

// ReadBinaryArithmetic is readBinaryArithmetic in the TS source:
// numeric `+`/`-`/`*`/`/`/`%`/`**` and string concatenation — left
// and right already evaluated.
func ReadBinaryArithmetic(ctx *FlowContext, env Env, e *ast.Node, left, right abstractdomain.AbstractValue) abstractdomain.AbstractValue {
	bin := e.AsBinaryExpression()
	// string + is tuple concatenation, exact when both sides are
	// exact; a string/number mix coerces and stays unknown
	if bin.OperatorToken.Kind == ast.KindPlusToken {
		stringSide := func(side *ast.Node) bool {
			return primitives.StringLikeSide(ctx.P.Checker, side)
		}
		leftStringKnown := left.Kind == abstractdomain.KindValues && left.KindTag == abstractdomain.PrimitiveString
		rightStringKnown := right.Kind == abstractdomain.KindValues && right.KindTag == abstractdomain.PrimitiveString
		if stringSide(bin.Left) || stringSide(bin.Right) || leftStringKnown || rightStringKnown {
			// exact only when BOTH sides are string words — a mixed sum
			// coerces the number to its decimal text, which the numeric
			// reading cannot say
			if leftStringKnown && rightStringKnown {
				return abstractdomain.KnownValues(
					append(append([]float64{}, left.Values...), right.Values...),
					abstractdomain.PrimitiveString,
					abstractdomain.MinTrustLevel(abstractdomain.TrustLevelOf(left), abstractdomain.TrustLevelOf(right)),
				)
			}
			// a string sum with SET knowledge on a side is the kernel's own
			// concatenation form: `"/" + rest` provably starts with "/" —
			// the string branch of `+` is the string-concatenation of its
			// operands (sec-applystringornumericbinaryoperator), and the
			// concatenation of two string languages is their concat set
			seqSideOf := func(k abstractdomain.AbstractValue, side *ast.Node) (refinementsets.RefinedSet, bool) {
				if k.Kind == abstractdomain.KindValues && k.KindTag == abstractdomain.PrimitiveString {
					return refinementsets.StringTuple(stringOf(k.Values)), true
				}
				if k.Kind == abstractdomain.KindSet && k.SetKindTag == abstractdomain.SetKindTagNone && stringSide(side) {
					return k.Set, true
				}
				return refinementsets.RefinedSet{}, false
			}
			A, aOk := seqSideOf(left, bin.Left)
			if aOk {
				B, bOk := seqSideOf(right, bin.Right)
				if bOk {
					return abstractdomain.KnownSet(
						refinementsets.MakeRefinedSet(refinementsets.Concatenation(A, B)),
						nil,
						abstractdomain.MinTrustLevel(abstractdomain.MinTrustLevel(abstractdomain.TrustLevelOf(left), abstractdomain.TrustLevelOf(right)), abstractdomain.TrustSpec),
						abstractdomain.SetKindTagNone,
					)
				}
			}
			return abstractdomain.UnknownOver([]abstractdomain.AbstractValue{left, right})
		}
	}
	// `**` transfers exactly on the pinned branches of
	// Number::exponentiate; the implementation-approximated final
	// step stays unknown (the alert downstream)
	if bin.OperatorToken.Kind == ast.KindAsteriskAsteriskToken {
		raised := TransferPow(left, right)
		if raised.Kind == abstractdomain.KindUnknown {
			return abstractdomain.UnknownOver([]abstractdomain.AbstractValue{left, right})
		}
		return raised
	}
	op, hasOp := numericOperatorOf(bin.OperatorToken.Kind)
	if !hasOp {
		// an operator no case above reads — the comma, instanceof — is
		// named silence, counted by the coverage report. The assignment family is
		// excluded: those operators HAVE cases, and an assignment target
		// the cases could not follow is plain unknown, not a missing case.
		assignment := bin.OperatorToken.Kind >= ast.KindFirstAssignment && bin.OperatorToken.Kind <= ast.KindLastAssignment
		if !assignment {
			// tokenToString, because the SyntaxKind reverse map answers
			// alias names for operator tokens (`<` reads FirstBinaryOperator)
			text := scanner.TokenToString(bin.OperatorToken.Kind)
			if text == "" {
				text = bin.OperatorToken.Kind.String()
			}
			assignability.NoteReason(assignability.ReasonNote{
				Site:        "expression",
				Node:        e,
				Said:        "the walk has no case for this operator (" + text + ")",
				Unsupported: true,
			})
		}
		return silence.Residue()
	}
	// `a - b` under a dominating difference row (same stable places,
	// by symbol and path): the ordered subtraction — the kernel
	// floors the difference at the vouched gap instead of letting the
	// correlation-free windows span it
	if op == OpSub {
		leftPlace := dataflowfacts.PlaceKeyOf(ctx.P.Checker, bin.Left)
		var rightPlace *dataflowfacts.PlaceKey
		if leftPlace != nil {
			rightPlace = dataflowfacts.PlaceKeyOf(ctx.P.Checker, bin.Right)
		}
		if leftPlace != nil && rightPlace != nil {
			row := dataflowfacts.DifferenceConstraintFor(ctx.DifferenceConstraints, *leftPlace, *rightPlace)
			if row != nil {
				var ordered *abstractdomain.AbstractValue
				if row.Strict && row.Bound == 0 {
					ordered = TransferOrderedSub(left, right)
				} else {
					ordered = TransferOrderedSubGap(left, right, -row.Bound)
				}
				if ordered != nil {
					return *ordered
				}
			} else if dataflowfacts.ConstraintsImply(ctx.Kernel, ctx.DifferenceConstraints, *leftPlace, *rightPlace, 0, true) {
				// no single row — the linear decider composes the live ones
				// (i < j and j < k floor k − i the way one row would)
				ordered := TransferOrderedSub(left, right)
				if ordered != nil {
					return *ordered
				}
			} else if dataflowfacts.ConstraintsImply(ctx.Kernel, ctx.DifferenceConstraints, *leftPlace, *rightPlace, 0, false) {
				ordered := TransferOrderedSubGap(left, right, 0)
				if ordered != nil {
					return *ordered
				}
			}
		}
	}
	// `xs[j] - xs[i]` on a SORTED-measured sequence with i ≤ j: the
	// parse checked non-decreasing order, and both reads are elements
	// (the in-bounds gates already vouched them defined), so the
	// difference floors at 0 through the kernel's gap subtraction.
	// The index order decides by literals, by a ledger row over the
	// index places, or by the indexes' windows.
	if op == OpSub && ast.IsElementAccessExpression(bin.Left) && ast.IsElementAccessExpression(bin.Right) {
		leftElem := bin.Left.AsElementAccessExpression()
		rightElem := bin.Right.AsElementAccessExpression()
		if ast.IsIdentifier(leftElem.Expression) && ast.IsIdentifier(rightElem.Expression) &&
			leftElem.Expression.Text() == rightElem.Expression.Text() {
			holder, ok := env[leftElem.Expression.Text()]
			if ok && holder.Kind == abstractdomain.KindSet && holder.Measures != nil && holder.Measures.Sorted &&
				indexAtLeast(ctx, env, leftElem.ArgumentExpression, rightElem.ArgumentExpression) {
				ordered := TransferOrderedSubGap(left, right, 0)
				if ordered != nil {
					return *ordered
				}
			}
		}
	}
	transferred := TransferBinary(op, left, right)
	// a COMPUTED-difference row (a guard that tested this very
	// subtraction against an exact number) intersects directly: the
	// places are stable, so this is the same double the guard vouched
	if op == OpSub {
		leftPlace := dataflowfacts.PlaceKeyOf(ctx.P.Checker, bin.Left)
		var rightPlace *dataflowfacts.PlaceKey
		if leftPlace != nil {
			rightPlace = dataflowfacts.PlaceKeyOf(ctx.P.Checker, bin.Right)
		}
		if leftPlace != nil && rightPlace != nil {
			window := dataflowfacts.ComputedWindowFor(ctx.DifferenceConstraints, *leftPlace, *rightPlace)
			if window != nil {
				var forms []refinementsets.Refinement
				if window.Lo != nil {
					if window.Lo.Strict {
						forms = append(forms, refinementsets.Above(window.Lo.Value))
					} else {
						forms = append(forms, refinementsets.AtLeast(window.Lo.Value))
					}
				}
				if window.Hi != nil {
					if window.Hi.Strict {
						forms = append(forms, refinementsets.Below(window.Hi.Value))
					} else {
						forms = append(forms, refinementsets.AtMost(window.Hi.Value))
					}
				}
				if len(forms) > 0 {
					return abstractdomain.NarrowKnown(transferred, forms)
				}
			}
		}
	}
	if transferred.Kind == abstractdomain.KindUnknown {
		return abstractdomain.UnknownOver([]abstractdomain.AbstractValue{left, right})
	}
	return transferred
}
