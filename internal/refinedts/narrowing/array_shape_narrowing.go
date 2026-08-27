// Array.isArray and a literal-array `.includes` as structural
// shape guards.

package narrowing

import (
	"strconv"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/tracing"
)

// resolvesToDefaultLib is resolvesToDefaultLib in the TS source
// (service/program_resolution.ts): does this identifier resolve to a
// declaration in a DEFAULT library file (the global Number, Math, …)?
func resolvesToDefaultLib(c *checker.Checker, node *ast.Node) bool {
	tracing.CountBy("host.symbolAtLocation", 1)
	tracing.CountBy("host.symbolInDefaultLib", 1)
	return c.SymbolInDefaultLib(c.GetSymbolAtLocation(node))
}

// ArrayShapeLeaf is arrayShapeLeaf in the TS source: Array.isArray, or
// a literal-list membership test in either of its two spellings
// (`[…].includes(x)`, `new Set([…]).has(x)`) — (BranchNarrowings{},
// false) when the call is none of those, so the caller can try the next
// recognizer.
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
					WhenTrue: []Narrowed{
						// the BRAND on the tested place itself: held, the
						// value IS an Array exotic object, so a kind union
						// keeps only the arms one could be
						// (Narrowed.SequenceBrand). Without this row the
						// guard stated only the length, and a value declared
						// `T[] | number` kept its number arm through the
						// test — an element read off it then had no sequence
						// to index and pinned no position.
						{
							Binding:       tested.Binding,
							Path:          append([]string{}, tested.Path...),
							SequenceBrand: true,
						},
						{
							Binding:  tested.Binding,
							Path:     append(append([]string{}, tested.Path...), "length"),
							Shape:    lengthShape,
							HasShape: true,
						},
					},
				}, true
			}
			return BranchNarrowings{}, false
		}
	}
	// `[1, 2, 3].includes(x)` held TRUE pins x into the literal set
	// (sec-array.prototype.includes answers SameValueZero
	// membership) — numbers as one-of words, strings as the union of
	// their exact tuples (the same spelling z.enum compiles to);
	// held FALSE is answered by the kernel's Or-of-Eq/EqSeq fold
	// through leafTreeOf/includesMembershipTree — the real-line
	// complement of the same members, weak, wherever the fold applies
	if includes, ok := includesReceiverOf(c, call); ok {
		members, words, readable := includesMembersOf(includes)
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
	return BranchNarrowings{}, false
}

// includesReceiverOf reads the ARRAY LITERAL whose members a membership
// test asks about — (nil, false) when e is not such a call at all.
//
// Two spellings ask the same question of the same literal list, and
// both are read here so the whenTrue pin and the kernel whenFalse fold
// treat them identically.
//
//   - `[1, 2].includes(x)` — the literal is the receiver
//     (sec-array.prototype.includes).
//   - `new Set([1, 2]).has(x)` — the literal is the Set constructor's
//     argument. A freshly built Set holds exactly the members the
//     literal spells (sec-set-iterable adds each item of the iterable),
//     and `has` answers membership over that same list
//     (sec-set.prototype.has: SameValue against each [[SetData]] entry,
//     after CanonicalizeKeyedCollectionKey folds -0 to +0 — which is
//     SameValueZero, the same equality `includes` uses). The Set is
//     built at the test and dropped after it, so nothing can have
//     changed its members between construction and question.
func includesReceiverOf(c *checker.Checker, call *ast.CallExpression) (*ast.Node, bool) {
	if !ast.IsPropertyAccessExpression(call.Expression) {
		return nil, false
	}
	propAccess := call.Expression.AsPropertyAccessExpression()
	if call.Arguments == nil || len(call.Arguments.Nodes) != 1 {
		return nil, false
	}
	if propAccess.Name().Text() == "includes" && ast.IsArrayLiteralExpression(propAccess.Expression) {
		return propAccess.Expression, true
	}
	if propAccess.Name().Text() == "has" && ast.IsNewExpression(propAccess.Expression) {
		newExpr := propAccess.Expression.AsNewExpression()
		// the built-in Set, never a class of the same name that a file
		// declares for itself — a user Set's `has` decides membership by
		// whatever its own body does
		if !ast.IsIdentifier(newExpr.Expression) || newExpr.Expression.Text() != "Set" ||
			!resolvesToDefaultLib(c, newExpr.Expression) {
			return nil, false
		}
		if newExpr.Arguments == nil || len(newExpr.Arguments.Nodes) != 1 ||
			!ast.IsArrayLiteralExpression(newExpr.Arguments.Nodes[0]) {
			return nil, false
		}
		return newExpr.Arguments.Nodes[0], true
	}
	return nil, false
}

// includesMembersOf reads a literal array's elements as one sort of
// member — numbers (with sign) in members, strings in words, readable
// false when any element is neither (a computed value, an identifier
// such as the NaN global, a mixed sort). Shared by the whenTrue pin
// above and the kernel whenFalse fold below, so both decline exactly
// the same member lists.
func includesMembersOf(receiver *ast.Node) (members []float64, words []string, readable bool) {
	readable = true
	elements := receiver.AsArrayLiteralExpression().Elements
	if elements == nil {
		return nil, nil, readable
	}
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
	return members, words, readable
}

// IncludesMembershipTree is includesMembershipTree in the TS source: a
// literal-list membership test — `[...].includes(x)` or
// `new Set([...]).has(x)` — lowered to the kernel's own Or-of-Eq/EqSeq
// tree on the PLACE x — the same recognition includesReceiverOf /
// includesMembersOf use for the whenTrue pin above, folded so the
// general ask loop (condition_analysis.go) can also answer whenFalse:
// the kernel's real-line complement of the same members, weak.
// Other (declines) on every list the whenTrue path also declines:
// mixed sorts, a non-literal element, an empty list — and where the
// call does not test the given place at all.
func IncludesMembershipTree(
	c *checker.Checker,
	e *ast.Node,
	place dataflowfacts.TrackedPlace,
	isTracked func(name string) bool,
) kernelbridge.NarrowTree {
	if !ast.IsCallExpression(e) {
		return Other
	}
	call := e.AsCallExpression()
	receiver, ok := includesReceiverOf(c, call)
	if !ok {
		return Other
	}
	tested := dataflowfacts.TrackedPlaceOfWith(c, call.Arguments.Nodes[0], isTracked)
	if tested == nil || !dataflowfacts.SameTrackedPlace(*tested, place) {
		return Other
	}
	members, words, readable := includesMembersOf(receiver)
	if !readable {
		return Other
	}
	if len(members) > 0 && len(words) == 0 {
		return orFold(len(members), func(i int) kernelbridge.NarrowTree {
			return kernelbridge.NarrowTree{Kind: kernelbridge.NarrowKindEq, K: members[i]}
		})
	}
	if len(words) > 0 && len(members) == 0 {
		return orFold(len(words), func(i int) kernelbridge.NarrowTree {
			return kernelbridge.NarrowTree{Kind: kernelbridge.NarrowKindEqSeq, Points: refinementsets.CodepointsOf(words[i])}
		})
	}
	return Other
}

// orFold folds n ≥ 1 leaves into a right-associated Or tree — the
// same shape a `x === 1 || x === 2 || …` condition lowers to.
func orFold(n int, leafAt func(i int) kernelbridge.NarrowTree) kernelbridge.NarrowTree {
	tree := leafAt(n - 1)
	for i := n - 2; i >= 0; i-- {
		a := leafAt(i)
		b := tree
		tree = kernelbridge.NarrowTree{Kind: kernelbridge.NarrowKindOr, A: &a, B: &b}
	}
	return tree
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
	// HELD on a tracked place the test IS read — ArrayShapeLeaf emits
	// the brand row and the length row. This sentence is what remains:
	// the argument is not a place this walk tracks, or the test is
	// REFUTED, which proves only "not an Array" and names no arm to
	// drop, since the tuple layer does not tell a string from an array.
	return "the Array.isArray test narrows nothing here — its argument " +
		"is not a tracked place, or the test is refuted, which names no arm to drop", true, true
}
