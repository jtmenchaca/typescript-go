// split from effect_capture_census.go — the member-write classifier

package walk

import (
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
)

// capturedMemberWrite classifies a WRITE target that steps through a
// name the closure did not bind: a member write on a PATH the
// caller's flattening spells (`p.a = 1`, and `p.a.b = 1` where the
// caller flattened a nested literal) is the object capture's own leaf
// write and is served; a computed step or a `delete` is not.
//
// WHY A DEEP PATH IS A LEAF. The leaf vocabulary was never one step —
// flatKeysOfLiteral recurses into nested literals, so a caller's
// `const p = { a: { b: 1 } }` lays out the slot "p.a.b", and
// leafSlotsUnder hands that back under the path "a.b" with its `p.`
// prefix trimmed and the rest kept whole. The member spelling is
// therefore a PATH below the holder, and matching it against the
// caller's leaves is the same lookup a one-step member takes. What
// used to refuse a deep path was this census spelling members one
// step deep, not the caller having nothing to fill them with.
//
// A path the caller did NOT flatten to that depth still refuses, and
// it refuses in one place: closureCapturesOf's own member lookup,
// which has no slot for a member the caller never laid out and
// declines the whole capture. So this census names what the body
// reads and the caller-side resolution decides whether the leaves
// exist — the same division a one-step member already rides.
//
// `delete p.a` STAYS REFUSED, and the reason is not the path: no leaf
// state spells an ABSENT KEY. A slot holds a value; the vocabulary
// has no word for "this key is no longer there", so a delete moves
// the record's shape rather than a leaf's value and nothing carries
// that back.
func (census *captureCensus) capturedMemberWrite(target *ast.Node, deletes bool) (served bool, refuses bool) {
	head := Unwrapped(target)
	if head == nil {
		return false, false
	}
	if ast.IsElementAccessExpression(head) {
		root := Unwrapped(head.AsElementAccessExpression().Expression)
		if root != nil && ast.IsIdentifier(root) {
			if _, isBound := census.bound[root.Text()]; !isBound {
				return false, true
			}
		}
		return false, false
	}
	if !ast.IsPropertyAccessExpression(head) {
		return false, false
	}
	root, path, pathOk := propertyPathOf(head)
	if !pathOk {
		// an optional or computed step under the write target — the
		// existing step-write reading decides whether it refuses
		return false, capturedStepWrite(target, census.bound)
	}
	if root == "this" {
		// the `this` arm below ends the census on its own
		return false, false
	}
	if _, isBound := census.bound[root]; isBound {
		return false, false
	}
	if deletes {
		// no leaf state spells an absent KEY — a delete moves the
		// record's shape, not a leaf's value
		return false, true
	}
	member := strings.Join(path, ".")
	object := census.objectFor(root)
	object.Written[member] = struct{}{}
	census.noteMember(root, member)
	return true, false
}
