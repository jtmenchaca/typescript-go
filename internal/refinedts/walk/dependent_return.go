// from assignability/dependent_return.ts
//
// A RETURN against a parameter-dependent bound (`Nat & z.Gte<"n">`):
// the claim is per-run — the returned value relates to what the
// parameter held on that same run — so set-against-set alone cannot
// judge it. Identity, sum, constant, and the order ledger, in that
// order; anything else the reading cannot prove alerts.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/jsnum"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
)

var dependsWords = map[string]string{
	"ge": "≥",
	"gt": ">",
	"le": "≤",
	"lt": "<",
}

// unwrapExpression peels parenthesized, `as`, and non-null wrappers.
func unwrapExpression(e *ast.Node) *ast.Node {
	for {
		switch {
		case ast.IsParenthesizedExpression(e):
			e = e.AsParenthesizedExpression().Expression
		case ast.IsAsExpression(e):
			e = e.AsAsExpression().Expression
		case ast.IsNonNullExpression(e):
			e = e.AsNonNullExpression().Expression
		default:
			return e
		}
	}
}

// CheckDependentReturn is checkDependentReturn in the TS source.
func CheckDependentReturn(
	ctx *FlowContext,
	env Env,
	expression *ast.Node,
	known abstractdomain.AbstractValue,
	stated annotations.DeclaredRefinement,
) {
	settled := stated
	if stated.Kind == annotations.DeclaredPossiblyUndefined {
		settled = *stated.Inner
	}
	if settled.Kind != annotations.DeclaredSet {
		return
	}
	for _, dep := range settled.ElementDepends {
		CheckElementBoundReturn(ctx, expression, DependentRelation{Op: dep.Op, Param: dep.Param})
	}
	for _, dep := range settled.Depends {
		CheckOneDependentReturn(ctx, env, expression, known, DependentRelation{Op: dep.Op, Param: dep.Param})
	}
}

// comparisonOfKind maps a binary operator's ast.Kind to its "ge" |
// "gt" | "le" | "lt" tag, or ("", false) for anything else — the TS
// source's CMP partial record.
func comparisonOfKind(kind ast.Kind) (string, bool) {
	switch kind {
	case ast.KindGreaterThanEqualsToken:
		return "ge", true
	case ast.KindGreaterThanToken:
		return "gt", true
	case ast.KindLessThanEqualsToken:
		return "le", true
	case ast.KindLessThanToken:
		return "lt", true
	}
	return "", false
}

var mirrorOp = map[string]string{"ge": "le", "gt": "lt", "le": "ge", "lt": "gt"}
var impliesOp = map[string][]string{
	"lt": {"lt"},
	"le": {"lt", "le"},
	"gt": {"gt"},
	"ge": {"gt", "ge"},
}

// CheckElementBoundReturn is checkElementBoundReturn in the TS
// source: an array statement whose EVERY element wears a dependent
// bound (`Array<number & z.Lt<"a">>`), returned from a `.filter`:
// the kept elements provably satisfy the callback's comparison, so
// implication decides — `e < a` proves every element `< a`; the
// non-strict `e <= a` against the strict claim keeps the equal
// element and refutes. Anything else the reading cannot prove
// alerts, as every stated claim does.
func CheckElementBoundReturn(
	ctx *FlowContext,
	expression *ast.Node,
	dep DependentRelation,
) {
	op, param := dep.Op, dep.Param
	head := unwrapExpression(expression)
	var predicate string
	hasPredicate := false
	if ast.IsCallExpression(head) {
		call := head.AsCallExpression()
		if ast.IsPropertyAccessExpression(call.Expression) &&
			call.Expression.AsPropertyAccessExpression().Name().Text() == "filter" &&
			len(call.Arguments.Nodes) >= 1 {
			callback := unwrapExpression(call.Arguments.Nodes[0])
			if (ast.IsArrowFunction(callback) || ast.IsFunctionExpression(callback)) &&
				len(callback.Parameters()) >= 1 && ast.IsIdentifier(callback.Parameters()[0].Name()) {
				elementName := callback.Parameters()[0].Name().Text()
				var body *ast.Node
				callbackBody := callback.Body()
				if callbackBody != nil && !ast.IsBlock(callbackBody) {
					body = unwrapExpression(callbackBody)
				}
				if body != nil && ast.IsBinaryExpression(body) {
					bin := body.AsBinaryExpression()
					if cmp, cmpOk := comparisonOfKind(bin.OperatorToken.Kind); cmpOk {
						left := unwrapExpression(bin.Left)
						right := unwrapExpression(bin.Right)
						if ast.IsIdentifier(left) && left.Text() == elementName &&
							ast.IsIdentifier(right) && right.Text() == param {
							predicate = cmp
							hasPredicate = true
						} else if ast.IsIdentifier(left) && left.Text() == param &&
							ast.IsIdentifier(right) && right.Text() == elementName {
							predicate = mirrorOp[cmp]
							hasPredicate = true
						}
					}
				}
			}
		}
	}
	if hasPredicate {
		for _, implied := range impliesOp[op] {
			if implied == predicate {
				return
			}
		}
		weaker := (op == "lt" && predicate == "le") || (op == "gt" && predicate == "ge")
		if weaker {
			ctx.Report(assignability.At(
				expression,
				7001,
				"the kept elements satisfy '"+dependsWords[predicate]+" "+
					param+"', which is not assignable to every element "+
					"'"+dependsWords[op]+" "+param+"' — the equal element "+
					"survives the filter",
			))
			return
		}
	}
	ctx.Report(assignability.At(
		expression,
		7002,
		"an element bound '"+dependsWords[op]+" "+param+"' could not be "+
			"proved — "+assignability.AlertText,
	))
}

// CheckOneDependentReturn is checkOneDependentReturn in the TS
// source.
func CheckOneDependentReturn(
	ctx *FlowContext,
	env Env,
	expression *ast.Node,
	known abstractdomain.AbstractValue,
	dep DependentRelation,
) {
	op, param := dep.Op, dep.Param
	// a dotted name reaches into an object parameter — the entry rows
	// carry those between parameters; a return against one declines
	if containsDot(param) {
		return
	}
	head := unwrapExpression(expression)
	if ast.IsIdentifier(head) && head.Text() == param {
		if op == "ge" || op == "le" {
			return
		}
		comparative := "less"
		if op == "gt" {
			comparative = "greater"
		}
		ctx.Report(assignability.At(
			expression,
			7001,
			"a returned value equal to '"+param+"' is never strictly "+
				comparative+" than '"+param+"'",
		))
		return
	}
	nanFree := func(k *abstractdomain.AbstractValue) bool {
		return k != nil && k.Kind != abstractdomain.KindNaN && k.Kind != abstractdomain.KindPossiblyNaN &&
			k.Kind != abstractdomain.KindPossiblyUndefined && k.Kind != abstractdomain.KindUndef
	}
	if ast.IsBinaryExpression(head) && head.AsBinaryExpression().OperatorToken.Kind == ast.KindPlusToken &&
		(op == "ge" || op == "le") {
		bin := head.AsBinaryExpression()
		left := unwrapExpression(bin.Left)
		right := unwrapExpression(bin.Right)
		var other *ast.Node
		if ast.IsIdentifier(left) && left.Text() == param {
			other = right
		} else if ast.IsIdentifier(right) && right.Text() == param {
			other = left
		}
		if other != nil {
			var otherKnown *abstractdomain.AbstractValue
			if ast.IsIdentifier(other) {
				if v, ok := env.Get(other.Text()); ok {
					otherKnown = &v
				}
			} else if ast.IsNumericLiteral(other) {
				v := float64(jsnum.FromString(other.Text()))
				known := abstractdomain.KnownValues([]float64{v}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
				otherKnown = &known
			}
			if nanFree(otherKnown) {
				rangeOf := RangeOfKnown(*otherKnown)
				if rangeOf != nil {
					if op == "ge" && rangeOf.Lo >= 0 {
						return
					}
					if op == "le" && rangeOf.Hi <= 0 {
						return
					}
				}
			}
		}
	}
	// the ORDER LEDGER: a live row (or the kernel's composition of
	// rows) relating the returned place to the parameter proves the
	// per-run claim — `return x` after a refuted `x <= min` guard,
	// `return max` under `max: z.Gte<"min">`'s own entry row
	{
		var fn *ast.Node
		for cursor := expression.Parent; cursor != nil; cursor = cursor.Parent {
			if ast.IsFunctionLike(cursor) {
				fn = cursor
				break
			}
		}
		var paramSymbol *ast.Symbol
		if fn != nil {
			for _, p := range fn.Parameters() {
				if name := p.Name(); name != nil && ast.IsIdentifier(name) && name.Text() == param {
					paramSymbol = ctx.P.Checker.GetSymbolAtLocation(name)
					break
				}
			}
		}
		returnedPlace := dataflowfacts.PlaceKeyOf(ctx.P.Checker, head)
		if paramSymbol != nil && returnedPlace != nil {
			paramPlace := dataflowfacts.PlaceKey{Base: paramSymbol, Path: "", BaseName: param}
			rows := ctx.DifferenceConstraints
			var proved bool
			switch op {
			case "ge":
				proved = dataflowfacts.ConstraintsImply(ctx.Kernel, rows, *returnedPlace, paramPlace, 0, false)
			case "gt":
				proved = dataflowfacts.ConstraintsImply(ctx.Kernel, rows, *returnedPlace, paramPlace, 0, true)
			case "le":
				proved = dataflowfacts.ConstraintsImply(ctx.Kernel, rows, paramPlace, *returnedPlace, 0, false)
			default: // "lt"
				proved = dataflowfacts.ConstraintsImply(ctx.Kernel, rows, paramPlace, *returnedPlace, 0, true)
			}
			if proved {
				return
			}
		}
	}
	bound := dependsWords[op] + " " + param
	sibling, hasSibling := env.Get(param)
	var siblingPtr *abstractdomain.AbstractValue
	if hasSibling {
		siblingPtr = &sibling
	}
	var siblingRange *NumberRange
	if nanFree(siblingPtr) {
		siblingRange = RangeOfKnown(sibling)
	}
	// a CONSTANT return is independent of the parameter by
	// construction: a sibling state admitting a breaking value refutes
	if known.Kind == abstractdomain.KindValues && known.KindTag == abstractdomain.PrimitiveNumber &&
		len(known.Values) == 1 && siblingRange != nil {
		c := known.Values[0]
		var broken bool
		switch op {
		case "ge":
			broken = siblingRange.Hi > c
		case "gt":
			broken = siblingRange.Hi >= c
		case "le":
			broken = siblingRange.Lo < c
		default: // "lt"
			broken = siblingRange.Lo <= c
		}
		if !broken {
			return
		}
		var admits string
		if op == "ge" || op == "gt" {
			admits = "values up to " + formatJSNumberLocal(siblingRange.Hi)
		} else {
			admits = "values down to " + formatJSNumberLocal(siblingRange.Lo)
		}
		ctx.Report(assignability.At(
			expression,
			7001,
			"a returned value of '"+formatJSNumberLocal(c)+"' is not assignable to '"+bound+"' — "+
				"'"+param+"' admits "+admits+" at this return",
		))
		return
	}
	// set-against-set still PROVES the uncorrelated direction: the
	// returned floor clearing the sibling ceiling holds pairwise
	var returnedRange *NumberRange
	if nanFree(&known) {
		returnedRange = RangeOfKnown(known)
	}
	if returnedRange != nil && siblingRange != nil {
		switch op {
		case "ge":
			if returnedRange.Lo >= siblingRange.Hi {
				return
			}
		case "gt":
			if returnedRange.Lo > siblingRange.Hi {
				return
			}
		case "le":
			if returnedRange.Hi <= siblingRange.Lo {
				return
			}
		case "lt":
			if returnedRange.Hi < siblingRange.Lo {
				return
			}
		}
	}
	ctx.Report(assignability.At(
		expression,
		7002,
		"a returned value could not be proved '"+bound+"' — "+assignability.AlertText,
	))
}

func containsDot(s string) bool {
	for _, c := range s {
		if c == '.' {
			return true
		}
	}
	return false
}
