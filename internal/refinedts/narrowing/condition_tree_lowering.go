// Program → NarrowTree: the condition as one place's narrowing tree,
// its !, &&, || structure with recognized tests on this place at the
// leaves and `other` everywhere else. Split from
// condition_analysis.ts per the v2 tree.

package narrowing

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/conditiontree"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// TreeOf is treeOf in the TS source: the condition as ONE place's
// narrowing tree: its !, &&, || structure with recognized tests on this
// place at the leaves and `other` everywhere else.
func TreeOf(
	c *checker.Checker,
	condition *ast.Node,
	place dataflowfacts.TrackedPlace,
	isTracked func(name string) bool,
	sideBounds SideBounds,
	depth int,
) kernelbridge.NarrowTree {
	// the connectives come from the SHARED condition tree
	// (condition_tree.ts) — the kernel channel stopped walking them
	// itself (finding 16), so a De Morgan subtlety cannot exist here
	// and be absent from the row and gate emitters. The shared tree
	// pushes `!` onto leaf polarity; a negated leaf comes back as the
	// kernel's own `not` node around the recognized test.
	var fold func(t conditiontree.ConditionTree) kernelbridge.NarrowTree
	fold = func(t conditiontree.ConditionTree) kernelbridge.NarrowTree {
		if t.Kind == conditiontree.ConditionTreeAnd || t.Kind == conditiontree.ConditionTreeOr {
			a := fold(*t.A)
			b := fold(*t.B)
			kind := kernelbridge.NarrowKindAnd
			if t.Kind == conditiontree.ConditionTreeOr {
				kind = kernelbridge.NarrowKindOr
			}
			return kernelbridge.NarrowTree{Kind: kind, A: &a, B: &b}
		}
		leaf := leafTreeOf(c, t.Test, place, isTracked, sideBounds, depth)
		if t.Negated {
			return kernelbridge.NarrowTree{Kind: kernelbridge.NarrowKindNot, A: &leaf}
		}
		return leaf
	}
	return fold(conditiontree.ConditionTreeOf(condition, false))
}

// leafTreeOf is leafTreeOf in the TS source: one LEAF recognized on the
// given place — every leaf test the recognizers read, with `await`
// peeled here (the shared tree does not look through it). Peeling can
// expose connective structure the shared tree could not see; that
// recurses through the fold with the leaf's own polarity kept.
func leafTreeOf(
	c *checker.Checker,
	test *ast.Node,
	place dataflowfacts.TrackedPlace,
	isTracked func(name string) bool,
	sideBounds SideBounds,
	depth int,
) kernelbridge.NarrowTree {
	e := Peeled(test)
	if e != test {
		return TreeOf(c, e, place, isTracked, sideBounds, depth)
	}
	if ast.IsBinaryExpression(e) {
		bin := e.AsBinaryExpression()
		op := bin.OperatorToken.Kind
		if indexed, ok := IndexOfComparisonLeaf(c, bin.Left, op, bin.Right, place, isTracked); ok {
			return indexed
		}
		return ComparisonLeaf(c, bin.Left, op, bin.Right, place, isTracked, sideBounds)
	}
	if ast.IsCallExpression(e) {
		call := e.AsCallExpression()
		// `Boolean(x)` in test position IS the test of x — a condition
		// applies ToBoolean anyway (sec-toboolean), so the default
		// library's Boolean adds nothing and the argument re-enters the
		// fold whole (connectives inside it included)
		if ast.IsIdentifier(call.Expression) && call.Expression.Text() == "Boolean" &&
			call.Arguments != nil && len(call.Arguments.Nodes) == 1 && resolvesToDefaultLib(c, call.Expression) {
			return TreeOf(c, call.Arguments.Nodes[0], place, isTracked, sideBounds, depth)
		}
		if lifted, ok := liftedPredicateTree(c, e, place, isTracked, depth); ok {
			return lifted
		}
		stringy := StringTestLeaf(c, e, place, isTracked)
		if stringy.Kind != kernelbridge.NarrowKindOther {
			return stringy
		}
		return NumberTestLeaf(c, e, place, isTracked)
	}
	return Other
}

// liftedPredicateTree is liftedPredicateTree in the TS source: a
// PROJECT predicate lifted into the guard: `if (isPositive(x))` narrows
// x by the predicate's own body — a single-parameter function in reach
// whose body is one expression (or one return) over that parameter. A
// narrow tree is place-relative, so the body's tree over its parameter
// IS the tree over the tested place: the argument's value is the
// parameter's value at the call. Tests on the predicate's OTHER names
// read as `other`, which the kernel treats conservatively. Depth-capped:
// predicates lift through predicates three deep, then answer nothing.
func liftedPredicateTree(
	c *checker.Checker,
	e *ast.Node,
	place dataflowfacts.TrackedPlace,
	isTracked func(name string) bool,
	depth int,
) (kernelbridge.NarrowTree, bool) {
	if depth >= 3 {
		return kernelbridge.NarrowTree{}, false
	}
	call := e.AsCallExpression()
	if call.Arguments == nil || len(call.Arguments.Nodes) != 1 {
		return kernelbridge.NarrowTree{}, false
	}
	tested := dataflowfacts.TrackedPlaceOf(call.Arguments.Nodes[0], isTracked)
	if tested == nil || !dataflowfacts.SameTrackedPlace(*tested, place) {
		return kernelbridge.NarrowTree{}, false
	}
	// the tree channel reads a body only PinnedFunctionOf pins — the
	// const and reassignment gates live there, once
	fn := PinnedFunctionOf(c, call.Expression)
	if fn == nil {
		return kernelbridge.NarrowTree{}, false
	}
	parameters := fn.Parameters()
	if len(parameters) != 1 {
		return kernelbridge.NarrowTree{}, false
	}
	parameter := parameters[0].AsParameterDeclaration()
	if !ast.IsIdentifier(parameter.Name()) || parameter.DotDotDotToken != nil || parameter.Initializer != nil {
		return kernelbridge.NarrowTree{}, false
	}
	body := BodyExpressionOf(fn)
	if body == nil {
		return kernelbridge.NarrowTree{}, false
	}
	parameterName := parameter.Name().Text()
	tree := TreeOf(c, body, dataflowfacts.TrackedPlace{Binding: parameterName}, func(name string) bool {
		return name == parameterName
	}, nil, depth+1)
	if !SaysAnything(tree) {
		return kernelbridge.NarrowTree{}, false
	}
	return tree, true
}

// SaysAnything is saysAnything in the TS source: does the tree state
// anything at all? An all-`other` tree asks no question.
func SaysAnything(t kernelbridge.NarrowTree) bool {
	switch t.Kind {
	case kernelbridge.NarrowKindOther:
		return false
	case kernelbridge.NarrowKindNot:
		return SaysAnything(*t.A)
	case kernelbridge.NarrowKindAnd, kernelbridge.NarrowKindOr:
		return SaysAnything(*t.A) || SaysAnything(*t.B)
	default:
		return true
	}
}

// CollectPlaces is collectPlaces in the TS source: the places a
// condition tests.
func CollectPlaces(
	c *checker.Checker,
	condition *ast.Node,
	isTracked func(name string) bool,
	into *[]dataflowfacts.TrackedPlace,
) {
	add := func(candidate *ast.Node) {
		place := dataflowfacts.TrackedPlaceOf(candidate, isTracked)
		if place == nil {
			return
		}
		for _, held := range *into {
			if dataflowfacts.SameTrackedPlace(held, *place) {
				return
			}
		}
		*into = append(*into, *place)
	}
	// the connectives come from the SHARED tree (finding 16) — this
	// walk reads leaves only, `await` peeled per leaf
	for _, leaf := range conditiontree.AllLeaves(conditiontree.ConditionTreeOf(condition, false)) {
		e := Peeled(leaf.Test)
		if e != leaf.Test {
			CollectPlaces(c, e, isTracked, into)
			continue
		}
		if ast.IsBinaryExpression(e) {
			bin := e.AsBinaryExpression()
			// a comparison: either side may be the place; a remainder side
			// tests the place under the %, a method side (indexOf) tests
			// its receiver
			for _, side := range []*ast.Node{bin.Left, bin.Right} {
				add(side)
				if ast.IsBinaryExpression(side) && side.AsBinaryExpression().OperatorToken.Kind == ast.KindPercentToken {
					add(side.AsBinaryExpression().Left)
				}
				if ast.IsCallExpression(side) && ast.IsPropertyAccessExpression(side.AsCallExpression().Expression) {
					add(side.AsCallExpression().Expression.AsPropertyAccessExpression().Expression)
				}
			}
			continue
		}
		if ast.IsCallExpression(e) {
			call := e.AsCallExpression()
			// `Boolean(x)` tests x — the argument recurses whole, so its
			// own connectives and places are collected
			if ast.IsIdentifier(call.Expression) && call.Expression.Text() == "Boolean" &&
				call.Arguments != nil && len(call.Arguments.Nodes) == 1 && resolvesToDefaultLib(c, call.Expression) {
				CollectPlaces(c, call.Arguments.Nodes[0], isTracked, into)
				continue
			}
			if call.Arguments != nil && len(call.Arguments.Nodes) > 0 {
				add(call.Arguments.Nodes[0])
			}
			// a method receiver may be the tested place (`x.includes("s")`)
			if ast.IsPropertyAccessExpression(call.Expression) {
				add(call.Expression.AsPropertyAccessExpression().Expression)
			}
		}
	}
}

// RemapPlaces is remapPlaces in the TS source: carry a body's
// narrowings out to the CALLER's places: a claim on parameter i becomes
// a claim on argument i's place. Claims on anything else (a closure
// name, an argument that is not a place) drop — dropping a narrowing is
// always sound.
func RemapPlaces(ns []Narrowed, parameters []string, argumentPlaces []*dataflowfacts.TrackedPlace) []Narrowed {
	var out []Narrowed
	for _, n := range ns {
		i := -1
		for idx, p := range parameters {
			if p == n.Binding {
				i = idx
				break
			}
		}
		if i == -1 {
			continue
		}
		if i >= len(argumentPlaces) || argumentPlaces[i] == nil {
			continue
		}
		argument := argumentPlaces[i]
		remapped := n
		remapped.Binding = argument.Binding
		remapped.Path = append(append([]string{}, argument.Path...), n.Path...)
		out = append(out, remapped)
	}
	return out
}
