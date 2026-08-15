// split from ir_opaque_havoc.go — the impossibilities

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
)

/* ── the impossibilities ─────────────────────────────────────────── */

// havocEnumerable is whether the statement's written-slot set is a
// syntactic question at all. Each false below is a genuine enumeration
// impossibility, not a difficulty:
//
//   - `with (o) …`: a free name inside the block may be o's property or
//     the enclosing binding, and only the runtime object decides. The set
//     of slots the block writes is therefore not readable from syntax.
//   - `eval(…)` called as a BARE name: the code it runs is a value, and
//     that code may write any binding in scope. (A `o.eval(…)` member
//     call is an ordinary method call — only the direct call has the
//     scope-piercing semantics, so only it is refused here.)
//   - a break or continue that LEAVES this statement: the transfer goes
//     to a structure outside the subtree being enumerated, so the exit
//     the kernel walks to is not the exit the run reaches. The whole
//     rule is containment, and containedTransfer decides it — a break
//     whose switch or loop sits INSIDE the statement is admitted,
//     because it cannot leave and the havoc of the whole statement
//     already covers every path through it.
//   - a `return`: the havoc's own shape is "write these slots, then carry
//     on", and a return does not carry on — it writes the result slot,
//     raises the done flag, and ends the block. A havoc that swallowed
//     one would leave the flag down, every statement after the havocked
//     one would be walked as if it had run, and a later return would
//     overwrite the result slot the swallowed one wrote: a WRONG answer
//     about the returned value. Raising the flag instead is not
//     available either: the havoc cannot say WHICH of its paths
//     returned, so the flag would be raised on all of them. (A `return`
//     in STATEMENT position never arrives here — the lowering's own
//     opaque-return route holds the exact control shape for it.)
//   - a `throw`: throwCarryingStatement.
//
// A nested FUNCTION's own returns, throws and transfers are its
// business, not this statement's, so the scan stops at a function
// boundary — an unreadable statement holding a callback is enumerable
// exactly as one holding any other expression is.
func havocEnumerable(statement *ast.Node) bool {
	enumerable := true
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if !enumerable {
			return true
		}
		switch {
		case node.Kind == ast.KindWithStatement:
			enumerable = false
			return true
		case ast.IsBreakStatement(node), ast.IsContinueStatement(node):
			if !containedTransfer(node, statement) {
				enumerable = false
			}
			return true
		case ast.IsReturnStatement(node):
			enumerable = false
			return true
		case throwCarryingStatement(node):
			enumerable = false
			return true
		case isBareEvalCall(node):
			enumerable = false
			return true
		}
		// a nested function's transfers leave IT, not this statement
		if ast.IsFunctionLike(node) || ast.IsClassLike(node) {
			return false
		}
		node.ForEachChild(visit)
		return false
	}
	visit(statement)
	return enumerable
}

// containedTransfer is whether a `break` or `continue` stays INSIDE the
// statement being enumerated. This is the whole rule, and it is the one
// the refusal is actually about:
//
// The havoc stands where the statement stood and writes every slot the
// statement could have written. A transfer that stays inside the
// statement is a path THROUGH it — the run leaves the statement at the
// statement's own exit, which is exactly where the havoc's writes sit,
// and the slot enumeration already unioned every block the transfer
// could have skipped or repeated. So the havoc covers it.
//
// A transfer that LEAVES the statement is the refusal: the run departs
// from somewhere in the middle for a target outside, and the kernel
// walks straight on past the havoc as though the statement had run to
// completion. The exit would then claim the havoc's writes happened on
// a path control had already left.
//
// Two shapes leave:
//
//   - a BARE break or continue with no enclosing switch or loop inside
//     the subtree — its target is an enclosing loop or switch of the
//     BODY, outside what is being havocked;
//   - a LABELLED break or continue whose labelled statement is not
//     inside the subtree — same thing, reached by name.
//
// Both walks are syntactic: from the transfer up through Parent to the
// subtree root. A parent chain that does not pass through the root at
// all (a caller handing this a detached node) answers false — the safe
// direction, since the refusal only ever costs coverage.
func containedTransfer(transfer *ast.Node, root *ast.Node) bool {
	if transfer == nil || root == nil {
		return false
	}
	label := transferLabelOf(transfer)
	isContinue := ast.IsContinueStatement(transfer)
	for node := transfer.Parent; node != nil; node = node.Parent {
		if label != "" {
			// the labelled statement this transfer names, found inside the
			// subtree: the target is contained
			if ast.IsLabeledStatement(node) &&
				node.AsLabeledStatement().Label.Text() == label {
				return true
			}
		} else if breakTargetOf(node, isContinue) {
			// the nearest switch or loop this bare transfer leaves, found
			// inside the subtree
			return true
		}
		if node == root {
			// the subtree ended before any target did
			return false
		}
		// a nested function boundary means this transfer was never this
		// statement's to begin with; the scan above stops at one, so
		// reaching it here is a caller passing an inner node directly
		if ast.IsFunctionLike(node) || ast.IsClassLike(node) {
			return false
		}
	}
	return false
}

// transferLabelOf is the label a `break`/`continue` names, or "" for a
// bare one.
func transferLabelOf(transfer *ast.Node) string {
	if ast.IsBreakStatement(transfer) {
		if label := transfer.AsBreakStatement().Label; label != nil {
			return label.Text()
		}
		return ""
	}
	if ast.IsContinueStatement(transfer) {
		if label := transfer.AsContinueStatement().Label; label != nil {
			return label.Text()
		}
		return ""
	}
	return ""
}

// breakTargetOf is whether a node is what a BARE transfer leaves: a
// `continue` leaves the nearest loop only, a `break` leaves the nearest
// loop OR switch — the language's own rule.
func breakTargetOf(node *ast.Node, isContinue bool) bool {
	switch {
	case ast.IsForStatement(node), ast.IsForInStatement(node),
		ast.IsForOfStatement(node), ast.IsWhileStatement(node),
		ast.IsDoStatement(node):
		return true
	case ast.IsSwitchStatement(node):
		return !isContinue
	}
	return false
}

// isBareEvalCall is `eval(…)` called through the bare name — the direct
// call whose code runs in the CALLER's scope and may write any binding
// there. `o.eval(x)` is an ordinary method call on some object and is
// not this.
func isBareEvalCall(node *ast.Node) bool {
	if !ast.IsCallExpression(node) {
		return false
	}
	callee := Unwrapped(node.AsCallExpression().Expression)
	return ast.IsIdentifier(callee) && callee.Text() == "eval"
}

// throwCarryingStatement is the THROW half of the impossibility test
// above, kept as its own named predicate because the DECISION it encodes
// needs its reasoning written down. The reasoning, and where it fails to
// close:
//
// The tempting lowering is "raise the done flag with the result slot
// left alone" — a throw ends the body's run, which is what the flag
// spells. Two things stop it.
//
// First, the flag is what the apply route reads to decide whether a
// path FELL OFF: `allReturned` is "the done flag cannot still be zero",
// and a body that is not allReturned has its answer wrapped
// PossiblyUndefined. Raising the flag on a throwing path therefore
// SUPPRESSES that wrap. For a body like `if (c) throw e; return 1;` the
// route would then claim "this call returns a value in {1}, never
// undefined" — which happens to hold for every run that completes, since
// a throwing run returns nothing at all. So the claim about the RETURN
// is defensible.
//
// Second, and this is what does not close: a throw inside a `try` does
// not end the body — it transfers to the `catch`, whose statements then
// run. Raising the done flag makes every later statement in the block
// conditional on the flag being down, so the catch's own writes become
// invisible to the walk. The caller would then read the state as it
// stood BEFORE the catch ran, which is not a weaker claim but a WRONG
// one. Separating "a throw that leaves the body" from "a throw the
// enclosing try catches" is exactly the structural reading the flag
// cannot spell, and there is no second flag here to spell it with.
//
// So a throw is refused HERE: a `throw` anywhere in a statement this
// floor is standing in for declines it.
//
// WHAT CLOSED SINCE. The second objection is about the TRY, and it is
// structural — so read the structure. A `throw` in STATEMENT position
// whose parent chain up to the lowered body's root passes through no
// `try` cannot transfer to a catch of THIS body: it leaves the body
// outright. For that throw the first paragraph's reasoning is the whole
// story, and it closes — a throwing run returns NOTHING, so no claim
// about the returned outcome can be wrong about it. The lowering gives
// it the exact shape of a return of nothing:
//
//	#ret := AbsentConst()   (a run that threw returned no value)
//	#done := {1}            (and the block ends here)
//
// which is weaker than the truth (the run produced no outcome at all,
// not an absent one) and never stronger. That route lives in
// lowering_to_kernel_ir.go; a throw INSIDE a try keeps this decline,
// under the catch-invisibility reasoning above, and the report names it
// "throw inside try" rather than as a category.
//
// This predicate is unchanged: the floor still refuses every throw it
// meets, because a havoc STANDS IN FOR a statement and a statement
// standing in for a throw would have to spell the transfer, which is
// what it cannot do. Only the statement-position route above it
// changed.
//
// (The subtree walk is havocEnumerable's, which calls this at every
// node; this only rules on the node in front of it.)
func throwCarryingStatement(node *ast.Node) bool {
	return ast.IsThrowStatement(node)
}
