// String membership tests and indexOf/lastIndexOf comparisons,
// lowered to the kernel's inSet leaf.

package narrowing

import (
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// StringTestLeaf is stringTestLeaf in the TS source: a string-membership
// test on this place, lowered to the kernel's inSet leaf: `/re/.test(x)`
// with a flagless regex literal whose pattern the format grammar
// compiles, or `x.includes("s")` / `x.startsWith("s")` / `x.endsWith("s")`
// with one literal argument. Gated to places tsc types a string —
// `.test` coerces its argument (ToString) and the methods live on other
// receivers too, so the sort obligation is discharged here. A flag or a
// position argument changes what the test decides, so either refuses.
func StringTestLeaf(c *checker.Checker, e *ast.Node, place dataflowfacts.TrackedPlace, isTracked func(name string) bool) kernelbridge.NarrowTree {
	call := e.AsCallExpression()
	callee := call.Expression
	if !ast.IsPropertyAccessExpression(callee) {
		return Other
	}
	propAccess := callee.AsPropertyAccessExpression()
	method := propAccess.Name().Text()
	stringPlace := func(expr *ast.Node) bool {
		tested := dataflowfacts.TrackedPlaceOf(expr, isTracked)
		if tested == nil || !dataflowfacts.SameTrackedPlace(*tested, place) {
			return false
		}
		t := c.GetTypeAtLocation(expr)
		return (t.Flags() & checker.TypeFlagsStringLike) != 0
	}
	if method == "test" && ast.IsRegularExpressionLiteral(propAccess.Expression) &&
		call.Arguments != nil && len(call.Arguments.Nodes) == 1 && stringPlace(call.Arguments.Nodes[0]) {
		text := propAccess.Expression.Text()
		lastSlash := strings.LastIndex(text, "/")
		compiled := refinementsets.FormatGrammar(text[1:lastSlash], text[lastSlash+1:])
		if !compiled.Ok {
			return Other
		}
		return kernelbridge.NarrowTree{Kind: kernelbridge.NarrowKindInSet, Set: compiled.Set}
	}
	if (method == "includes" || method == "startsWith" || method == "endsWith") &&
		call.Arguments != nil && len(call.Arguments.Nodes) == 1 && stringPlace(propAccess.Expression) {
		s := dataflowfacts.StringLiteralOf(call.Arguments.Nodes[0])
		if s == nil {
			return Other
		}
		var set refinementsets.RefinedSet
		switch method {
		case "includes":
			set = refinementsets.IncludesSet(*s)
		case "startsWith":
			set = refinementsets.StartsWithSet(*s)
		default:
			set = refinementsets.EndsWithSet(*s)
		}
		return kernelbridge.NarrowTree{Kind: kernelbridge.NarrowKindInSet, Set: set}
	}
	return Other
}

// indexOfCallOf reads a call to indexOf/lastIndexOf with exactly one
// argument, or nil.
func indexOfCallOf(e *ast.Node) *ast.Node {
	if !ast.IsCallExpression(e) {
		return nil
	}
	call := e.AsCallExpression()
	if !ast.IsPropertyAccessExpression(call.Expression) {
		return nil
	}
	name := call.Expression.AsPropertyAccessExpression().Name().Text()
	if (name != "indexOf" && name != "lastIndexOf") || call.Arguments == nil || len(call.Arguments.Nodes) != 1 {
		return nil
	}
	return e
}

// IndexOfComparisonLeaf is indexOfComparisonLeaf in the TS source:
// `x.indexOf("s") !== -1` and kin read as the containment test they
// formatAt: indexOf answers ≥ 0 exactly when the receiver contains the
// argument (sec-string.prototype.indexof), and `indexOf === 0` exactly
// when the receiver starts with it. Only a literal argument and a
// string-typed receiver place lower; anything else claims nothing.
// (nil, false) stands in for the TS source's null.
func IndexOfComparisonLeaf(
	c *checker.Checker,
	left *ast.Node,
	op ast.Kind,
	right *ast.Node,
	place dataflowfacts.TrackedPlace,
	isTracked func(name string) bool,
) (kernelbridge.NarrowTree, bool) {
	call := indexOfCallOf(left)
	k, kOk := LiteralOf(right)
	kind := op
	if call == nil {
		call = indexOfCallOf(right)
		k, kOk = LiteralOf(left)
		kind = mirrorOp(op)
	}
	if call == nil || !kOk {
		return kernelbridge.NarrowTree{}, false
	}
	access := call.AsCallExpression().Expression
	accessExpr := access.AsPropertyAccessExpression()
	tested := dataflowfacts.TrackedPlaceOf(accessExpr.Expression, isTracked)
	if tested == nil || !dataflowfacts.SameTrackedPlace(*tested, place) {
		return kernelbridge.NarrowTree{}, false
	}
	if (c.GetTypeAtLocation(accessExpr.Expression).Flags() & checker.TypeFlagsStringLike) == 0 {
		return kernelbridge.NarrowTree{}, false
	}
	s := dataflowfacts.StringLiteralOf(call.AsCallExpression().Arguments.Nodes[0])
	if s == nil {
		return kernelbridge.NarrowTree{}, false
	}
	containsSet := refinementsets.IncludesSet(*s)
	contains := kernelbridge.NarrowTree{Kind: kernelbridge.NarrowKindInSet, Set: containsSet}
	refuted := kernelbridge.NarrowTree{Kind: kernelbridge.NarrowKindNot, A: &contains}
	if kind == ast.KindGreaterThanEqualsToken && k == 0 {
		return contains, true
	}
	if kind == ast.KindGreaterThanToken && k == -1 {
		return contains, true
	}
	if kind == ast.KindExclamationEqualsEqualsToken && k == -1 {
		return contains, true
	}
	if kind == ast.KindLessThanToken && k == 0 {
		return refuted, true
	}
	if kind == ast.KindEqualsEqualsEqualsToken && k == -1 {
		return refuted, true
	}
	if accessExpr.Name().Text() == "indexOf" && kind == ast.KindEqualsEqualsEqualsToken && k == 0 {
		return kernelbridge.NarrowTree{Kind: kernelbridge.NarrowKindInSet, Set: refinementsets.StartsWithSet(*s)}, true
	}
	return kernelbridge.NarrowTree{}, false
}

// StringTestReason is stringTestReason in the TS source: decline
// sentences for string tests and indexOf comparisons (finding 9).
// indexOf shares the comparison coverage row.
func StringTestReason(e *ast.Node, readElsewhere GuardReadElsewhere) (said string, unsupported bool, ok bool) {
	if ast.IsCallExpression(e) && ast.IsPropertyAccessExpression(e.AsCallExpression().Expression) {
		method := e.AsCallExpression().Expression.AsPropertyAccessExpression().Name().Text()
		if method == "test" || method == "includes" || method == "startsWith" || method == "endsWith" {
			return "a call as a guard is not read — deciding it needs the " +
				"callee's body", true, true
		}
		return "", false, false
	}
	if ast.IsBinaryExpression(e) {
		bin := e.AsBinaryExpression()
		if IsComparisonOperator(bin.OperatorToken.Kind) &&
			(indexOfCallOf(bin.Left) != nil || indexOfCallOf(bin.Right) != nil) {
			if readElsewhere == GuardReadRelation {
				return "a comparison between two changing values narrows no " +
					"set — the order ledger holds it as a difference row", false, true
			}
			return "a comparison between two changing values the order " +
				"ledger cannot hold — a side is unstable or unreadable", true, true
		}
	}
	return "", false, false
}
