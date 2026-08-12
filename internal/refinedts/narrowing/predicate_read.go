// The shared predicate-reading depth and the unread-guard coverage.
// The refine reader and predicate lifting recurse through each
// other, and one counter bounds the chain wherever it starts. Split
// from condition_analysis.ts per the v2 tree.
//
// BLOCKED: RecordUnreadGuard needs assignability/decline_reasons.ts's
// noteReason (ReasonNote's collector stack) — assignability is a later
// wave (go-port-tracker.md: pending wave 3), outside this directory's
// allowed import set. MentionsTracked has no such dependency and is
// ported in full; RecordUnreadGuard's own reading (splitting a guard
// to its leaves, calling GuardReason per leaf) is written below too,
// with the noteReason sink call dropped and reported at the one call
// site — no note is recorded, but nothing else about the walk is
// stubbed.
package narrowing

import (
	"github.com/microsoft/typescript-go/internal/ast"
)

// predicateDepth is the shared predicate-reading depth: predicate
// bodies may guard through further predicates; the walk is bounded so
// a self-calling predicate reads as far as the bound and honestly no
// further.
var predicateDepth int

// MentionsTracked is mentionsTracked in the TS source: whether the
// condition READS a tracked binding anywhere — a property chain counts
// by its root, a property NAME does not.
func MentionsTracked(node *ast.Node, isTracked func(name string) bool) bool {
	if ast.IsIdentifier(node) {
		return isTracked(node.Text())
	}
	if node.Kind == ast.KindThisKeyword {
		return isTracked("this")
	}
	if ast.IsPropertyAccessExpression(node) {
		return MentionsTracked(node.AsPropertyAccessExpression().Expression, isTracked)
	}
	found := false
	node.ForEachChild(func(child *ast.Node) bool {
		if !found {
			found = MentionsTracked(child, isTracked)
		}
		return false
	})
	return found
}

// RecordUnreadGuard is recordUnreadGuard in the TS source: record the
// unread guard, split to its leaves: a composite under `!`, `&&`, `||`
// records each side that mentions a tracked binding. (A composite where
// SOME leaf read is not recorded at all — the coverage report counts
// conditions that produced nothing, not partial reads.)
//
// BLOCKED (see the file banner): the note this function computes is
// never handed to a collector — noteReason (assignability/
// decline_reasons.ts) is not ported. This still walks the same leaves
// and computes the same (said, unsupported) pairs GuardReason would
// hand noteReason, so a caller that only needs to know WHETHER a guard
// went unread (never mind the recorded sentence) can read the return
// value; nothing is recorded to any coverage collector.
func RecordUnreadGuard(condition *ast.Node, isTracked func(name string) bool, readElsewhere GuardReadElsewhere) {
	// the shared tree resolves the connectives (condition_tree.go); a
	// single-leaf condition records unconditionally, a composite's
	// leaves record where they mention a tracked binding
	tree := ConditionTreeOf(condition, false)
	if tree.Kind == ConditionTreeLeaf {
		GuardReason(tree.Test, readElsewhere)
		return
	}
	for _, leaf := range AllLeaves(tree) {
		if !MentionsTracked(leaf.Test, isTracked) {
			continue
		}
		GuardReason(leaf.Test, readElsewhere)
	}
}

// PredicateReadDepth is predicateReadDepth in the TS source.
func PredicateReadDepth() int {
	return predicateDepth
}

// OpenPredicateRead is openPredicateRead in the TS source.
func OpenPredicateRead() {
	predicateDepth++
}

// ClosePredicateRead is closePredicateRead in the TS source.
func ClosePredicateRead() {
	predicateDepth--
}
