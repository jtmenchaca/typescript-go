// Array.isArray and a literal-array `.includes` as structural
// shape guards.

package narrowing

import (
	"strconv"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// resolvesToDefaultLib is resolvesToDefaultLib in the TS source
// (service/program_resolution.ts): does this identifier resolve to a
// declaration in a DEFAULT library file (the global Number, Math, …)?
func resolvesToDefaultLib(c *checker.Checker, node *ast.Node) bool {
	return c.SymbolInDefaultLib(c.GetSymbolAtLocation(node))
}

// ArrayShapeLeaf is arrayShapeLeaf in the TS source: Array.isArray or
// `[…].includes(x)` — (BranchNarrowings{}, false) when the call is
// neither, so the caller can try the next recognizer.
func ArrayShapeLeaf(c *checker.Checker, e *ast.Node, isTracked func(name string) bool) (BranchNarrowings, bool) {
	call := e.AsCallExpression()
	// `Array.isArray(u)` held TRUE proves an Array exotic object,
	// whose `length` is a nonnegative integer under 2^32
	// (sec-array-exotic-objects) — stated on the PLACE u.length, so
	// the guard grounds the length read with no element claim at
	// all. Held FALSE proves nothing this walk states.
	if ast.IsPropertyAccessExpression(call.Expression) {
		propAccess := call.Expression.AsPropertyAccessExpression()
		if ast.IsIdentifier(propAccess.Expression) && propAccess.Expression.Text() == "Array" &&
			propAccess.Name().Text() == "isArray" &&
			call.Arguments != nil && len(call.Arguments.Nodes) == 1 &&
			resolvesToDefaultLib(c, propAccess.Name()) {
			tested := dataflowfacts.TrackedPlaceOfWith(c, call.Arguments.Nodes[0], isTracked)
			if tested != nil {
				lengthShape := abstractdomain.KnownSet(
					refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.AtMost(4294967295), refinementsets.Integer),
					nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone,
				)
				return BranchNarrowings{
					WhenTrue: []Narrowed{{
						Binding:  tested.Binding,
						Path:     append(append([]string{}, tested.Path...), "length"),
						Shape:    lengthShape,
						HasShape: true,
					}},
				}, true
			}
			return BranchNarrowings{}, false
		}
	}
	// `[1, 2, 3].includes(x)` held TRUE pins x into the literal set
	// (sec-array.prototype.includes answers SameValueZero
	// membership) — numbers as one-of words, strings as the union of
	// their exact tuples (the same spelling z.enum compiles to);
	// held FALSE says only "none of them", which no form states —
	// that side stays silent
	if ast.IsPropertyAccessExpression(call.Expression) {
		propAccess := call.Expression.AsPropertyAccessExpression()
		if propAccess.Name().Text() == "includes" &&
			ast.IsArrayLiteralExpression(propAccess.Expression) &&
			call.Arguments != nil && len(call.Arguments.Nodes) == 1 {
			var members []float64
			var words []string
			readable := true
			elements := propAccess.Expression.AsArrayLiteralExpression().Elements
			if elements != nil {
				for _, element := range elements.Nodes {
					if spelledWord := dataflowfacts.StringLiteralOf(element); spelledWord != nil {
						words = append(words, *spelledWord)
						continue
					}
					spelled, spelledOk := numericLiteralOf(element)
					if !spelledOk {
						readable = false
					} else {
						members = append(members, spelled)
					}
				}
			}
			tested := dataflowfacts.TrackedPlaceOfWith(c, call.Arguments.Nodes[0], isTracked)
			// one sort per list: a mixed list pins nothing (the tuple layer
			// holds one sort at a time)
			if readable && tested != nil && len(members) > 0 && len(words) == 0 {
				inSet := Narrowed{
					Binding: tested.Binding,
					Path:    tested.Path,
					Forms:   []refinementsets.Refinement{refinementsets.OneOf(members)},
				}
				return BranchNarrowings{WhenTrue: []Narrowed{inSet}}, true
			}
			if readable && tested != nil && len(words) > 0 && len(members) == 0 {
				set := refinementsets.StringTuple(words[0])
				for _, word := range words[1:] {
					set = refinementsets.MakeRefinedSet(refinementsets.Union(set, refinementsets.StringTuple(word)))
				}
				inWords := Narrowed{
					Binding: tested.Binding,
					Path:    tested.Path,
					Forms:   append([]refinementsets.Refinement{}, set.Forms...),
				}
				return BranchNarrowings{WhenTrue: []Narrowed{inWords}}, true
			}
		}
	}
	return BranchNarrowings{}, false
}

// numericLiteralOf reads a numeric literal, or its negation — the
// TS source's inline ts.isNumericLiteral(element) / prefix-minus check
// in arrayShapeLeaf's includes() branch.
func numericLiteralOf(element *ast.Node) (float64, bool) {
	if ast.IsNumericLiteral(element) {
		n, err := strconv.ParseFloat(element.Text(), 64)
		if err != nil {
			return 0, false
		}
		return n, true
	}
	if ast.IsPrefixUnaryExpression(element) {
		unary := element.AsPrefixUnaryExpression()
		if unary.Operator == ast.KindMinusToken && ast.IsNumericLiteral(unary.Operand) {
			n, err := strconv.ParseFloat(unary.Operand.Text(), 64)
			if err != nil {
				return 0, false
			}
			return -n, true
		}
	}
	return 0, false
}

// ArrayShapeReason is arrayShapeReason in the TS source: decline
// sentences for Array.isArray (finding 9). Literal-array `.includes`
// shares the generic call coverage row.
func ArrayShapeReason(e *ast.Node) (said string, unsupported bool, ok bool) {
	if !ast.IsCallExpression(e) {
		return "", false, false
	}
	call := e.AsCallExpression()
	if !ast.IsPropertyAccessExpression(call.Expression) {
		return "", false, false
	}
	propAccess := call.Expression.AsPropertyAccessExpression()
	if !ast.IsIdentifier(propAccess.Expression) || propAccess.Expression.Text() != "Array" ||
		propAccess.Name().Text() != "isArray" {
		return "", false, false
	}
	// the recognizer does not read this test yet — a kind union
	// keeps every arm through it, held or refuted
	return "an Array.isArray test is not read — a kind union " +
		"keeps every arm through it", true, true
}
