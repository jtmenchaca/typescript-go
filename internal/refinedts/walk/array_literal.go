// from evaluation/array_literal.ts
//
// Array-literal evaluation: one pass builds the item list; all-scalar
// lists collapse to the flat number tuple, anything else stays a LIST
// with element knowledge per slot. A spread flattens an exact
// sequence; an unknown spread loses even the length.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// EvaluateArrayLiteral evaluates an array literal expression.
func EvaluateArrayLiteral(ctx *FlowContext, env Env, e *ast.Node) abstractdomain.AbstractValue {
	lit := e.AsArrayLiteralExpression()
	var items []abstractdomain.AbstractValue
	for _, element := range lit.Elements.Nodes {
		// a spread of an exact sequence flattens its elements in place
		if ast.IsSpreadElement(element) {
			spread := evaluateExpression(ctx, env, element.AsSpreadElement().Expression)
			if spread.Kind == abstractdomain.KindValues && spread.KindTag == abstractdomain.PrimitiveArray {
				for _, v := range spread.Values {
					items = append(items, abstractdomain.KnownValues([]float64{v}, abstractdomain.PrimitiveNumber, abstractdomain.TrustLevelOf(spread)))
				}
				continue
			}
			if spread.Kind == abstractdomain.KindList {
				items = append(items, spread.Items...)
				continue
			}
			// `[...xs]` alone is a COPY: same length window, same
			// elements, same order — the value claim carries whole,
			// measures included
			if len(lit.Elements.Nodes) == 1 && spread.Kind == abstractdomain.KindSet && spread.SetKindTag == abstractdomain.SetKindTagNone {
				return spread
			}
			return silence.Residue() // an unknown spread loses even the length
		}
		items = append(items, evaluateExpression(ctx, env, element))
	}
	flat := true
	for _, item := range items {
		if !(item.Kind == abstractdomain.KindValues && len(item.Values) == 1 && item.KindTag == abstractdomain.PrimitiveNumber) {
			flat = false
			break
		}
	}
	if flat {
		values := make([]float64, len(items))
		floor := abstractdomain.TrustProved
		for i, item := range items {
			values[i] = item.Values[0]
			floor = abstractdomain.MinTrustLevel(floor, abstractdomain.TrustLevelOf(item))
		}
		return abstractdomain.KnownValues(values, abstractdomain.PrimitiveArray, floor)
	}
	return abstractdomain.KnownList(items, abstractdomain.TrustProved)
}
