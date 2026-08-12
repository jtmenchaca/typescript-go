// from evaluation/index_operators.ts
//
// Indexed writes on a tracked name, and the index-order test used by
// sorted-sequence subtraction. An exact tuple with a pinned in-range
// index takes the write in place; a key-typed index over a tracked
// object writes exactly one of its candidate keys.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/jsnum"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// ReadIndexedWrite is readIndexedWrite in the TS source: a write
// through an INDEX on a tracked name — or (AbstractValue{}, false)
// where the left side is not that form.
func ReadIndexedWrite(ctx *FlowContext, env Env, e *ast.Node) (abstractdomain.AbstractValue, bool) {
	bin := e.AsBinaryExpression()
	if bin.OperatorToken.Kind != ast.KindEqualsToken || !ast.IsElementAccessExpression(bin.Left) {
		return abstractdomain.AbstractValue{}, false
	}
	elem := bin.Left.AsElementAccessExpression()
	if !ast.IsIdentifier(elem.Expression) {
		return abstractdomain.AbstractValue{}, false
	}
	name := elem.Expression.Text()
	if _, ok := env[name]; !ok {
		return abstractdomain.AbstractValue{}, false
	}
	receiver, ok := env[name]
	if !ok {
		receiver = silence.Residue()
	}
	index := evaluateExpression(ctx, env, elem.ArgumentExpression)
	value := evaluateExpression(ctx, env, bin.Right)
	// a declared sequence's element set is an invariant: judge the
	// written value against it before the tracked value moves
	WriteElement(ctx, bin.Left, value, bin.Right)
	if receiver.Kind == abstractdomain.KindValues && receiver.KindTag == abstractdomain.PrimitiveArray &&
		index.Kind == abstractdomain.KindValues && len(index.Values) == 1 &&
		value.Kind == abstractdomain.KindValues && len(value.Values) == 1 &&
		value.KindTag == abstractdomain.PrimitiveNumber &&
		isInteger(index.Values[0]) &&
		index.Values[0] >= 0 && int(index.Values[0]) < len(receiver.Values) {
		next := append([]float64{}, receiver.Values...)
		next[int(index.Values[0])] = value.Values[0]
		dataflowfacts.UpdateTracked(ctx.Aliases, env, name, abstractdomain.KnownValues(next, abstractdomain.PrimitiveArray, abstractdomain.TrustProved))
		return value, true
	}
	if receiver.Kind == abstractdomain.KindObject {
		argument := elem.ArgumentExpression
		var literal string
		hasLiteral := false
		if ast.IsStringLiteral(argument) || ast.IsNoSubstitutionTemplateLiteral(argument) {
			literal, hasLiteral = argument.Text(), true
		}
		if hasLiteral {
			_, hasKey := objectKeyIndex(receiver, literal)
			if hasKey {
				keys := setObjectKey(receiver.Keys, literal, value)
				dataflowfacts.UpdateTracked(ctx.Aliases, env, name, abstractdomain.KnownObject(keys, nil, false, abstractdomain.TrustProved, false))
				return value, true
			}
		}
		indexType := ctx.P.Checker.GetTypeAtLocation(argument)
		var candidates []*checker.Type
		if indexType.IsUnion() {
			candidates = indexType.Types()
		} else {
			candidates = []*checker.Type{indexType}
		}
		var written []string
		readable := len(candidates) > 0
		for _, candidate := range candidates {
			if candidate.IsStringLiteral() {
				lit, _ := candidate.AsLiteralType().Value().(string)
				written = append(written, lit)
			} else {
				readable = false
			}
		}
		if readable {
			allPresent := true
			for _, key := range written {
				if _, hasKey := objectKeyIndex(receiver, key); !hasKey {
					allPresent = false
					break
				}
			}
			if allPresent {
				keys := append([]abstractdomain.ObjectKey{}, receiver.Keys...)
				for _, key := range written {
					idx, _ := objectKeyIndex(receiver, key)
					keys[idx] = abstractdomain.ObjectKey{Name: key, Value: abstractdomain.JoinKnown(keys[idx].Value, value)}
				}
				dataflowfacts.UpdateTracked(ctx.Aliases, env, name, abstractdomain.KnownObject(keys, nil, false, abstractdomain.TrustProved, false))
				return value, true
			}
		}
	}
	ctx.Aliases.Havoc(env, name)
	return value, true
}

func objectKeyIndex(receiver abstractdomain.AbstractValue, name string) (int, bool) {
	for i, key := range receiver.Keys {
		if key.Name == name {
			return i, true
		}
	}
	return 0, false
}

func setObjectKey(keys []abstractdomain.ObjectKey, name string, value abstractdomain.AbstractValue) []abstractdomain.ObjectKey {
	out := append([]abstractdomain.ObjectKey{}, keys...)
	for i, key := range out {
		if key.Name == name {
			out[i] = abstractdomain.ObjectKey{Name: name, Value: value}
			return out
		}
	}
	return append(out, abstractdomain.ObjectKey{Name: name, Value: value})
}

// IndexAtLeast is indexAtLeast in the TS source: does the LEFT index
// provably sit at or above the RIGHT one? By literals, by a ledger
// row over their places, or by their windows (left's floor at or
// above right's ceiling). Effect-free sides only — a decision must
// not re-run an effect.
func indexAtLeast(ctx *FlowContext, env Env, left, right *ast.Node) bool {
	if ast.IsNumericLiteral(left) && ast.IsNumericLiteral(right) {
		return float64(jsnum.FromString(left.Text())) >= float64(jsnum.FromString(right.Text()))
	}
	// as place + offset: i ≥ i − 1 outright; across places, a ledger
	// row L − R ≥ b decides when b covers the offsets' difference
	// (array indexes sit under 2^32, so the offsets are exact)
	leftSide := dataflowfacts.OffsetPlaceOf(ctx.P.Checker, left)
	var rightSide *dataflowfacts.OffsetPlace
	if leftSide != nil {
		rightSide = dataflowfacts.OffsetPlaceOf(ctx.P.Checker, right)
	}
	if leftSide != nil && rightSide != nil {
		if dataflowfacts.SamePlace(leftSide.Place, rightSide.Place) {
			return leftSide.Offset >= rightSide.Offset
		}
		row := dataflowfacts.DifferenceConstraintFor(ctx.DifferenceConstraints, leftSide.Place, rightSide.Place)
		if row != nil && row.Bound >= float64(rightSide.Offset-leftSide.Offset) {
			return true
		}
	}
	effectFree := func(side *ast.Node) bool {
		return ast.IsNumericLiteral(side) || ast.IsIdentifier(side) ||
			(ast.IsPropertyAccessExpression(side) && ast.IsIdentifier(side.AsPropertyAccessExpression().Expression))
	}
	if !effectFree(left) || !effectFree(right) {
		return false
	}
	leftRange := RangeOfKnown(evaluateExpression(ctx, env, left))
	var rightRange *NumberRange
	if leftRange != nil {
		rightRange = RangeOfKnown(evaluateExpression(ctx, env, right))
	}
	return leftRange != nil && rightRange != nil && leftRange.Lo >= rightRange.Hi
}
