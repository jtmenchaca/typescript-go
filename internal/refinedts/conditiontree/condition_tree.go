// from narrowing/condition_tree.ts
//
// A condition with its boolean connectives resolved, ONCE, for
// every emitter that reads guards: parentheses peeled, `!` pushed
// through De Morgan onto the leaves, `&&`/`||` swapped under
// negation. Each leaf is the original syntax with the polarity it
// is tested under — so no emitter re-walks the connective structure
// itself, and a De Morgan subtlety cannot be fixed in one walker
// and stay absent from another.
//
// Pulled into this LEAF package (its own subtree, no imports beyond
// ast) rather than left in package narrowing: dataflowfacts needs
// ConditionTreeOf/ConjunctiveLeaves for the real bodies of
// DifferenceConstraintsOf, NegatedDifferenceConstraintsOf,
// LengthGuardNarrowings, and SumConstraintsOf, but narrowing already
// imports dataflowfacts (TrackedPlace, the leaf recognizers), so
// dataflowfacts importing narrowing back would close a true two-way
// cycle. Both sides import this package instead, per PORT.md's
// leaf-package cycle rule (precedent: annotations/libraryadapters/
// compiledshape) — narrowing's own condition_tree.go was deleted and
// its call sites repointed here at integration
// (walk-integration-punchlist.md item 15).

package conditiontree

import "github.com/microsoft/typescript-go/internal/ast"

// ConditionTreeKind is the tag of a ConditionTree.
type ConditionTreeKind string

const (
	ConditionTreeLeaf ConditionTreeKind = "leaf"
	ConditionTreeAnd  ConditionTreeKind = "and"
	ConditionTreeOr   ConditionTreeKind = "or"
)

// ConditionTree is the TS discriminated union
//
//	{ kind: "leaf"; test; negated } | { kind: "and"; A; B } | { kind: "or"; A; B }
//
// collapsed to one struct with a Kind tag, per the port's convention for
// discriminated unions that carry pure data.
type ConditionTree struct {
	Kind ConditionTreeKind

	// "leaf": the leaf's syntax, parentheses peeled, and whether the
	// leaf is tested REFUTED.
	Test    *ast.Node
	Negated bool

	// "and" / "or": the two sides.
	A *ConditionTree
	B *ConditionTree
}

// ConditionTreeOf is conditionTreeOf in the TS source.
func ConditionTreeOf(e *ast.Node, negated bool) ConditionTree {
	cursor := e
	for ast.IsParenthesizedExpression(cursor) {
		cursor = cursor.AsParenthesizedExpression().Expression
	}
	if ast.IsPrefixUnaryExpression(cursor) &&
		cursor.AsPrefixUnaryExpression().Operator == ast.KindExclamationToken {
		return ConditionTreeOf(cursor.AsPrefixUnaryExpression().Operand, !negated)
	}
	if ast.IsBinaryExpression(cursor) {
		bin := cursor.AsBinaryExpression()
		if bin.OperatorToken.Kind == ast.KindAmpersandAmpersandToken {
			// ¬(a && b) is ¬a || ¬b
			kind := ConditionTreeAnd
			if negated {
				kind = ConditionTreeOr
			}
			a := ConditionTreeOf(bin.Left, negated)
			b := ConditionTreeOf(bin.Right, negated)
			return ConditionTree{Kind: kind, A: &a, B: &b}
		}
		if bin.OperatorToken.Kind == ast.KindBarBarToken {
			// ¬(a || b) is ¬a && ¬b
			kind := ConditionTreeOr
			if negated {
				kind = ConditionTreeAnd
			}
			a := ConditionTreeOf(bin.Left, negated)
			b := ConditionTreeOf(bin.Right, negated)
			return ConditionTree{Kind: kind, A: &a, B: &b}
		}
	}
	return ConditionTree{Kind: ConditionTreeLeaf, Test: cursor, Negated: negated}
}

// ConditionLeaf is one leaf a walk reads: the tested syntax, and whether
// it is tested REFUTED.
type ConditionLeaf struct {
	Test    *ast.Node
	Negated bool
}

// AllLeaves is allLeaves in the TS source: EVERY leaf, whatever the
// connective — what a coverage walk reads: each tested position, held or
// refuted, conjunct or disjunct.
func AllLeaves(tree ConditionTree) []ConditionLeaf {
	var out []ConditionLeaf
	var walk func(t ConditionTree)
	walk = func(t ConditionTree) {
		if t.Kind == ConditionTreeLeaf {
			out = append(out, ConditionLeaf{Test: t.Test, Negated: t.Negated})
			return
		}
		walk(*t.A)
		walk(*t.B)
	}
	walk(tree)
	return out
}

// ConjunctiveLeaves is conjunctiveLeaves in the TS source: every leaf a
// CONJUNCTIVE reading holds: an AND contributes both sides, an OR proves
// neither side alone — the walk the row and gate emitters share.
func ConjunctiveLeaves(tree ConditionTree) []ConditionLeaf {
	var out []ConditionLeaf
	var walk func(t ConditionTree)
	walk = func(t ConditionTree) {
		if t.Kind == ConditionTreeLeaf {
			out = append(out, ConditionLeaf{Test: t.Test, Negated: t.Negated})
			return
		}
		if t.Kind == ConditionTreeAnd {
			walk(*t.A)
			walk(*t.B)
		}
	}
	walk(tree)
	return out
}
