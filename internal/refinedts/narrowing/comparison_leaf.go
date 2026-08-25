// One comparison as a narrowing leaf: literals, mirrored operators,
// modulo-zero, and the cmpSet window when the other side is known.

package narrowing

import (
	"math"
	"strconv"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

var comparisonOps = map[ast.Kind]struct{}{
	ast.KindLessThanToken:                {},
	ast.KindLessThanEqualsToken:          {},
	ast.KindGreaterThanToken:             {},
	ast.KindGreaterThanEqualsToken:       {},
	ast.KindEqualsEqualsToken:            {},
	ast.KindEqualsEqualsEqualsToken:      {},
	ast.KindExclamationEqualsToken:       {},
	ast.KindExclamationEqualsEqualsToken: {},
}

// IsComparisonOperator is isComparisonOperator in the TS source.
func IsComparisonOperator(op ast.Kind) bool {
	_, ok := comparisonOps[op]
	return ok
}

// LiteralOf is literalOf in the TS source: a numeric side of a
// comparison: a literal or its negation. (0, false) where the
// expression is not a numeric side — mirroring the TS source's null.
func LiteralOf(e *ast.Node) (float64, bool) {
	if ast.IsNumericLiteral(e) {
		n, err := strconv.ParseFloat(e.Text(), 64)
		if err != nil {
			return 0, false
		}
		return n, true
	}
	if ast.IsPrefixUnaryExpression(e) {
		unary := e.AsPrefixUnaryExpression()
		if unary.Operator == ast.KindMinusToken && ast.IsNumericLiteral(unary.Operand) {
			n, err := strconv.ParseFloat(unary.Operand.Text(), 64)
			if err != nil {
				return 0, false
			}
			return -n, true
		}
	}
	if ast.IsIdentifier(e) && e.Text() == "Infinity" {
		return math.Inf(1), true
	}
	return 0, false
}

// ModuloSide is moduloSide in the TS source: `x % k === 0` — read the
// remainder side, when it is one on this place with a positive finite
// divisor. (0, false) stands in for the TS source's null. The checker
// is what resolves a const-bound index in the remainder's place.
func ModuloSide(c *checker.Checker, side *ast.Node, place dataflowfacts.TrackedPlace, isTracked func(name string) bool) (float64, bool) {
	if !ast.IsBinaryExpression(side) || side.AsBinaryExpression().OperatorToken.Kind != ast.KindPercentToken {
		return 0, false
	}
	bin := side.AsBinaryExpression()
	tested := dataflowfacts.TrackedPlaceOfWith(c, bin.Left, isTracked)
	k, kOk := LiteralOf(bin.Right)
	if tested == nil || !dataflowfacts.SameTrackedPlace(*tested, place) ||
		!kOk || math.IsInf(k, 0) || math.IsNaN(k) || k <= 0 {
		return 0, false
	}
	return k, true
}

// MathSignSide reads `Math.sign(x)` when its argument is this place —
// the sign-comparison recognizer, `ModuloSide`'s own shape for the one
// other library call an equality against a literal genuinely narrows.
func MathSignSide(c *checker.Checker, side *ast.Node, place dataflowfacts.TrackedPlace, isTracked func(name string) bool) bool {
	bare := Peeled(side)
	if !ast.IsCallExpression(bare) {
		return false
	}
	call := bare.AsCallExpression()
	if !ast.IsPropertyAccessExpression(call.Expression) {
		return false
	}
	pa := call.Expression.AsPropertyAccessExpression()
	if !ast.IsIdentifier(pa.Expression) || pa.Expression.Text() != "Math" || pa.Name().Text() != "sign" {
		return false
	}
	if call.Arguments == nil || len(call.Arguments.Nodes) != 1 {
		return false
	}
	tested := dataflowfacts.TrackedPlaceOfWith(c, call.Arguments.Nodes[0], isTracked)
	return tested != nil && dataflowfacts.SameTrackedPlace(*tested, place)
}

// ComparisonLeaf is comparisonLeaf in the TS source: one comparison as
// a leaf ON THE GIVEN PLACE — everything else is the `other` leaf,
// which claims nothing in either direction.
func ComparisonLeaf(
	c *checker.Checker,
	left *ast.Node,
	op ast.Kind,
	right *ast.Node,
	place dataflowfacts.TrackedPlace,
	isTracked func(name string) bool,
	sideBounds SideBounds,
) kernelbridge.NarrowTree {
	isEquals := op == ast.KindEqualsEqualsEqualsToken
	isNotEquals := op == ast.KindExclamationEqualsEqualsToken
	negated := func(t kernelbridge.NarrowTree) kernelbridge.NarrowTree {
		return kernelbridge.NarrowTree{Kind: kernelbridge.NarrowKindNot, A: &t}
	}

	onPlace := func(e *ast.Node) bool {
		tested := dataflowfacts.TrackedPlaceOfWith(c, e, isTracked)
		return tested != nil && dataflowfacts.SameTrackedPlace(*tested, place)
	}

	if isEquals || isNotEquals {
		// an absence test is definedness, not a set claim
		if AbsentLiteral(c, left) || AbsentLiteral(c, right) {
			return Other
		}
		// `n !== n` IS the NaN test: SameValue aside, strict equality of a
		// place with itself fails exactly when the value is NaN
		// (sec-strict-equality-comparison) — so the self-comparison reads
		// as the isNaN leaf, `!==` held meaning NaN and `===` held real
		if onPlace(left) && onPlace(right) {
			nan := kernelbridge.NarrowTree{Kind: kernelbridge.NarrowKindIsNaN}
			if isNotEquals {
				return nan
			}
			return negated(nan)
		}
		// a string equality on this place
		leftWord := dataflowfacts.StringLiteralOf(left)
		rightWord := dataflowfacts.StringLiteralOf(right)
		var stringSide *string
		if leftWord != nil {
			stringSide = leftWord
		} else {
			stringSide = rightWord
		}
		if stringSide != nil && (onPlace(left) || onPlace(right)) {
			// equality with the EMPTY string on a string-typed place is the
			// emptiness test: `s === ""` ⟺ ¬(𝑙𝑒𝑛 ≥ 1), so the refuted side
			// proves the one-or-more window outright
			if *stringSide == "" {
				stringTyped := false
				for _, side := range []*ast.Node{left, right} {
					if onPlace(side) && (c.GetTypeAtLocation(side).Flags()&checker.TypeFlagsStringLike) != 0 {
						stringTyped = true
						break
					}
				}
				if stringTyped {
					nonEmpty := kernelbridge.NarrowTree{
						Kind: kernelbridge.NarrowKindInSet,
						Set:  refinementsets.Repetition(refinementsets.Codepoints, 1, nil),
					}
					if isEquals {
						return negated(nonEmpty)
					}
					return nonEmpty
				}
			}
			leaf := kernelbridge.NarrowTree{Kind: kernelbridge.NarrowKindEqSeq, Points: refinementsets.CodepointsOf(*stringSide)}
			if isEquals {
				return leaf
			}
			return negated(leaf)
		}
		// the exact-remainder test: x % k === 0 / !== 0
		modulo, moduloOk := ModuloSide(c, left, place, isTracked)
		if !moduloOk {
			modulo, moduloOk = ModuloSide(c, right, place, isTracked)
		}
		leftLiteral, leftLiteralOk := LiteralOf(left)
		rightLiteral, rightLiteralOk := LiteralOf(right)
		// the sign test: Math.sign(x) === k lowers to the sign's own
		// comparison on the EXISTING proven leaves — === 1 is x > 0
		// (sign(NaN) is NaN and never 1, so truth proves realness, the
		// strong Gt claim); === -1 is x < 0; === 0 is x ∈ {0, −0} (the
		// float equality conflates the zeros exactly as the ground
		// does). Any other literal never comes out of Math.sign — the
		// test proves nothing HERE (deciding the branch dead is the
		// dead-guard machinery's job, not a narrowing claim).
		// sec-math.sign.
		if MathSignSide(c, left, place, isTracked) || MathSignSide(c, right, place, isTracked) {
			k, kOk := rightLiteral, rightLiteralOk
			if !kOk {
				k, kOk = leftLiteral, leftLiteralOk
			}
			if kOk {
				var leaf kernelbridge.NarrowTree
				switch k {
				case 1:
					leaf = kernelbridge.NarrowTree{Kind: kernelbridge.NarrowKindCmp, Op: kernelbridge.NarrowOpGt, K: 0}
				case -1:
					leaf = kernelbridge.NarrowTree{Kind: kernelbridge.NarrowKindCmp, Op: kernelbridge.NarrowOpLt, K: 0}
				case 0:
					leaf = kernelbridge.NarrowTree{Kind: kernelbridge.NarrowKindEq, K: 0}
				default:
					return Other
				}
				if isEquals {
					return leaf
				}
				return negated(leaf)
			}
		}
		zeroSide := (leftLiteralOk && leftLiteral == 0) || (rightLiteralOk && rightLiteral == 0)
		if moduloOk && zeroSide {
			leaf := kernelbridge.NarrowTree{Kind: kernelbridge.NarrowKindModZero, D: modulo}
			if isEquals {
				return leaf
			}
			return negated(leaf)
		}
	}

	// loose equality against a numeric literal, on a place tsc types a
	// number: both sides numbers means IsLooselyEqual IS the strict
	// comparison (sec-islooselyequal step 1), so the test lowers the
	// same — any other sort would coerce, and refuses
	if op == ast.KindEqualsEqualsToken || op == ast.KindExclamationEqualsToken {
		var placeSide *ast.Node
		if onPlace(left) {
			placeSide = left
		} else if onPlace(right) {
			placeSide = right
		}
		if placeSide == nil || (c.GetTypeAtLocation(placeSide).Flags()&checker.TypeFlagsNumberLike) == 0 {
			return Other
		}
		if op == ast.KindEqualsEqualsToken {
			op = ast.KindEqualsEqualsEqualsToken
		} else {
			op = ast.KindExclamationEqualsEqualsToken
		}
	}
	// normalize to place-on-the-left; mirror the operator when the
	// literal is on the left (k >= x means x <= k)
	var k float64
	kind := op
	rightLiteral, rightLiteralOk := LiteralOf(right)
	leftLiteral, leftLiteralOk := LiteralOf(left)
	mirrored := mirrorOp(op)
	setOpOf := func(k ast.Kind) (kernelbridge.NarrowCmpOp, bool) {
		switch k {
		case ast.KindGreaterThanEqualsToken:
			return kernelbridge.NarrowOpGe, true
		case ast.KindGreaterThanToken:
			return kernelbridge.NarrowOpGt, true
		case ast.KindLessThanEqualsToken:
			return kernelbridge.NarrowOpLe, true
		case ast.KindLessThanToken:
			return kernelbridge.NarrowOpLt, true
		default:
			return "", false
		}
	}
	if onPlace(left) && rightLiteralOk {
		k = rightLiteral
	} else if onPlace(right) && leftLiteralOk {
		k = leftLiteral
		kind = mirrored
	} else if sideBounds != nil {
		var setOp kernelbridge.NarrowCmpOp
		var setOpOk bool
		var side *ast.Node
		if onPlace(left) && !onPlace(right) {
			setOp, setOpOk = setOpOf(op)
			side = right
		} else if onPlace(right) && !onPlace(left) {
			setOp, setOpOk = setOpOf(mirrored)
			side = left
		}
		if !setOpOk || side == nil {
			return Other
		}
		window, windowOk := sideBounds(side)
		if !windowOk {
			return Other
		}
		return kernelbridge.NarrowTree{Kind: kernelbridge.NarrowKindCmpSet, Op: setOp, Lo: window.Lo, Hi: window.Hi}
	} else {
		return Other
	}
	if math.IsNaN(k) {
		return Other
	}

	switch kind {
	case ast.KindGreaterThanEqualsToken:
		return kernelbridge.NarrowTree{Kind: kernelbridge.NarrowKindCmp, Op: kernelbridge.NarrowOpGe, K: k}
	case ast.KindGreaterThanToken:
		return kernelbridge.NarrowTree{Kind: kernelbridge.NarrowKindCmp, Op: kernelbridge.NarrowOpGt, K: k}
	case ast.KindLessThanEqualsToken:
		return kernelbridge.NarrowTree{Kind: kernelbridge.NarrowKindCmp, Op: kernelbridge.NarrowOpLe, K: k}
	case ast.KindLessThanToken:
		return kernelbridge.NarrowTree{Kind: kernelbridge.NarrowKindCmp, Op: kernelbridge.NarrowOpLt, K: k}
	case ast.KindEqualsEqualsEqualsToken:
		return kernelbridge.NarrowTree{Kind: kernelbridge.NarrowKindEq, K: k}
	case ast.KindExclamationEqualsEqualsToken:
		return negated(kernelbridge.NarrowTree{Kind: kernelbridge.NarrowKindEq, K: k})
	default:
		return Other
	}
}

// mirrorOp mirrors the operator when the literal is on the left
// (k >= x means x <= k).
func mirrorOp(op ast.Kind) ast.Kind {
	switch op {
	case ast.KindGreaterThanEqualsToken:
		return ast.KindLessThanEqualsToken
	case ast.KindGreaterThanToken:
		return ast.KindLessThanToken
	case ast.KindLessThanEqualsToken:
		return ast.KindGreaterThanEqualsToken
	case ast.KindLessThanToken:
		return ast.KindGreaterThanToken
	default:
		return op
	}
}

// ComparisonReason is comparisonReason in the TS source: decline
// sentences for comparison ops (finding 9). Absence equalities share
// this coverage row — guardReason historically classifies them with
// COMPARISON_OPS.
func ComparisonReason(e *ast.Node, readElsewhere GuardReadElsewhere) (said string, unsupported bool, ok bool) {
	if !ast.IsBinaryExpression(e) || !IsComparisonOperator(e.AsBinaryExpression().OperatorToken.Kind) {
		return "", false, false
	}
	// the ORDER LEDGER may hold what set-narrowing cannot: when the
	// caller recorded difference rows from this very condition, the
	// guard was read — as a relation, not a set
	if readElsewhere == GuardReadRelation {
		return "a comparison between two changing values narrows no " +
			"set — the order ledger holds it as a difference row", false, true
	}
	return "a comparison between two changing values the order " +
		"ledger cannot hold — a side is unstable or unreadable", true, true
}
