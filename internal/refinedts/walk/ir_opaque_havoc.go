// The sound floor for a statement no route reads: HAVOC, not decline.
//
// Before this file, a statement no lowering route recognized declined
// the WHOLE body, and the body kept nothing. That is the wrong trade in
// the slot world: a statement the lowering cannot read still cannot do
// anything except move slots, and there is a finite, enumerable set of
// slots it can move. Writing `unknown` into each of those and keeping
// every other slot's knowledge is sound, and it keeps the body's route.
//
// WHAT A STATEMENT CAN AFFECT, in the slot world:
//
//	(a) the tracked SCALAR slots it assigns — an assignment target, a
//	    compound, a `++`/`--` step, a destructuring target with a slot;
//	(b) every LEAF slot of any FLATTENED local the statement MENTIONS
//	    anywhere. A mention hands the object to code the lowering cannot
//	    see, and unseen code may move any leaf: a record's leaves, an
//	    array's length and element, a collection's size, values and keys,
//	    a promise-held local's inner;
//	(c) the names it DECLARES — `const x = <unreadable>` finds x's slot
//	    and havocs it.
//
// Module state, global state, and `this` state are not in the slot
// world at all, so they need nothing: no slot holds them, and no lowered
// read can answer from them.
//
// A havoc is `assign slot unknown` and nothing else. It is never a
// guessed set — the point of the floor is that it claims NOTHING about
// what the statement did, only about which slots it could have touched.
// A slot set the enumeration cannot bound is a DECLINE, not a wider
// havoc, because "which slots" is the one thing this route must be
// right about.
//
// The impossibilities, each a genuine one:
//
//   - `with (o) { … }` — every free name in the block may resolve into
//     o's properties, so which names the block writes is not a syntactic
//     question at all;
//   - a bare `eval(…)` call — the same, for arbitrary code;
//   - a break or continue that LEAVES the enumerated statement — a
//     labelled one whose label is declared outside it, or a bare one
//     with no enclosing switch or loop inside it. Control then goes
//     somewhere the havoc's own statement position does not reach. A
//     CONTAINED break or continue is admitted: it cannot leave the
//     statement, and the havoc of the whole statement already covers
//     every path through it (containedTransfer below);
//   - a `return` — a havoc writes slots and then falls through, but a
//     return raises the done flag and stops the block. Havocking a
//     statement that contains one would drop the raise, and a later
//     return would then overwrite the result slot the swallowed one
//     wrote — a WRONG answer about the returned value, not a weak one;
//   - a `throw` — see throwCarryingStatement below for the reasoning.
//
// A statement whose own route is the OPAQUE RETURN or the escaping
// THROW never reaches this floor at all: the lowering has an exact
// control-flow shape for each (lowering_to_kernel_ir.go), and this
// floor's refusal of both is what routes them there.

package walk

import (
	"sort"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// OpaqueHavocStatements is the LAST resort of the statement dispatch: a
// statement every route declined lowers as one `assign slot unknown` per
// slot it could possibly have written, deduplicated and in slot order.
//
// (nil, false) only where the slot set cannot be enumerated at all —
// never as "this statement looked hard". A false answer here is what
// still declines the body.
//
// The FIRST havocked construct's spelling is recorded into the lowering
// context (FirstHavoc), which the body-level owner reads at the end to
// report the body's outcome. Recording is first-wins and this route
// never reads it back.
func OpaqueHavocStatements(context *LoweringContext, statement *ast.Node) ([]kernelbridge.IrStatement, bool) {
	if context == nil || statement == nil {
		return nil, false
	}
	if !havocEnumerable(statement) {
		return nil, false
	}
	slots, ok := havocSlotsOfStatement(context, statement)
	if !ok {
		return nil, false
	}
	NoteFirstHavoc(context, havocConstructName(statement))
	return havocAssignments(slots), true
}

// havocAssignments turns a collected slot set into the statements that
// write them: one `assign slot unknown` each, deduplicated, in SLOT
// ORDER — so the same statement in the same context always lowers to the
// same IR, whatever order the enumeration walked the syntax in.
func havocAssignments(slots map[int]struct{}) []kernelbridge.IrStatement {
	ordered := make([]int, 0, len(slots))
	for slot := range slots {
		ordered = append(ordered, slot)
	}
	sort.Ints(ordered)
	out := make([]kernelbridge.IrStatement, 0, len(ordered))
	for _, slot := range ordered {
		out = append(out, kernelbridge.IrStatement{
			Kind:   kernelbridge.IrStatementAssign,
			Target: slot,
			Effect: unknownEffect,
		})
	}
	return out
}
