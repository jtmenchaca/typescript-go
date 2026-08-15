// split from ir_call_hoist.go — the ordering gate

package walk

import (
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
)

/* ── the ordering gate ───────────────────────────────────────────── */

// hoistingIsOrderSafe is the soundness rule this whole file turns on.
//
// THE PROBLEM. JavaScript evaluates an expression left to right, and every
// side effect lands at the position where its subexpression sits. Hoisting
// moves the call's effects to BEFORE the whole statement. For
//
//	total = count + this.bump();
//
// the real run reads `count` FIRST and then runs bump; the hoisted run
// runs bump first and then reads `count`. If bump writes `count`, the two
// runs disagree — the hoisted lowering would read the POST-call value
// where the real one read the pre-call value. That is not a weaker claim,
// it is a WRONG one, so it must not be lowered.
//
// THE RULE. The hoist is admitted only where the call's WRITE SET is
// disjoint from every OTHER slot the statement mentions. Both sides are
// enumerated, never guessed:
//
//   - the call's write set is exactly what its call statement writes,
//     which is what Rets names: the temp (fresh by construction, so it can
//     collide with nothing) plus the caller slots a WRITTEN bundle entry
//     rides back into. Those come from the callee's own LoweredSummary
//     BundleEntries — the census's Written rows, resolved to caller slots
//     by the same receiver-path spelling bundleRetsAndArgs resolves them
//     by, so the gate and the statement builder can never disagree about
//     which slots a call writes.
//
//   - the statement's slot mentions are havocSlotsOfStatement's — the ONE
//     enumerator the havoc floor uses. It over-approximates (a mention is
//     any identifier occurrence, in any position), which is the safe
//     direction here: the gate may refuse a hoist that would have been
//     fine, and refusing costs only precision, since the statement then
//     takes its ordinary route and the havoc floor after it.
//
// WHY THE CALL'S OWN SUBTREE IS EXCLUDED. The statement's mention set
// includes the call's own arguments and receiver, and those are slots the
// call statement itself reads — reading them at the hoist position is
// exactly what the real run does, since the call's own operands are
// evaluated at the call's own position. So the mentions inside the call
// being hoisted are subtracted before the intersection; only what the
// statement mentions AROUND the call is a reordering risk.
//
// WHAT THE RULE DOES NOT COVER, and does not need to. A callee that writes
// through anything but a bundle write-back — module state, a global, an
// object the caller never flattened — writes nothing this walk carries, so
// no slot's knowledge can be falsified by it. That is the same fact the
// opaque-call floor rests on, and the same fact that lets a scalar
// argument pass by value.
//
// UNRESOLVABLE ENUMERATION. Where havocSlotsOfStatement cannot bound the
// statement's mentions the gate REFUSES: a set it could not enumerate is
// not a set it can prove disjoint from anything.
func hoistingIsOrderSafe(context *LoweringContext, call *ast.Node, callee *ast.Node) bool {
	// with no statement in hand there is nothing to be ordered against, and
	// the gate cannot be checked — so it refuses
	if context.HoistStatement == nil {
		return false
	}
	writes := hoistedCallWriteSlots(context, call, callee)
	if len(writes) == 0 {
		// the call writes nothing the caller carries: no reordering of the
		// statement's reads can observe it
		return true
	}
	// the enumerability refusal rides on the full-statement scan; the
	// intersection itself runs against the mentions OUTSIDE the call's
	// own subtree. Exempting by slot identity is wrong: the receiver
	// path `this` expands to every field, so a call writing this.count
	// would exempt the very slot the statement reads around it. A
	// mention is safe only when every occurrence sits INSIDE the call —
	// then the whole call moves with its own operands and nothing
	// observes the order.
	if _, enumerable := statementSlotMentions(context, context.HoistStatement); !enumerable {
		return false
	}
	outside := slotMentionsOutside(context, context.HoistStatement, call)
	for slot := range writes {
		if _, mentioned := outside[slot]; mentioned {
			return false
		}
	}
	return true
}

// slotMentionsOutside is the widened mention scan with the hoisted
// call's OWN subtree excluded: identifiers and dotted paths, each
// contributing its slot and its bundle leaves, everywhere in the
// statement EXCEPT under the call being hoisted. What remains is
// exactly what the statement reads or writes around the call — the
// only occurrences a reorder could be observed through.
func slotMentionsOutside(context *LoweringContext, statement *ast.Node, exclude *ast.Node) map[int]struct{} {
	slots := map[int]struct{}{}
	noteName := func(name string) {
		if slot, held := slotIndexOfName(context, name); held {
			slots[slot] = struct{}{}
		}
		for _, slot := range flattenedSlotsUnder(context, name) {
			slots[slot] = struct{}{}
		}
	}
	var visit func(current *ast.Node) bool
	visit = func(current *ast.Node) bool {
		if current == exclude {
			return false
		}
		if ast.IsPropertyAccessExpression(current) {
			if path, spelled := dottedPathOf(current); spelled {
				noteName(path)
			}
		}
		if ast.IsIdentifier(current) {
			noteName(current.Text())
		}
		current.ForEachChild(visit)
		return false
	}
	visit(statement)
	return slots
}

// (hoistedCallOwnReads lived here: an exemption set for the ordering
// gate, keyed by SLOT identity. It exempted the receiver path's whole
// bundle, so a call writing this.count exempted the very slot the
// statement read around it. The gate now excludes the call's SUBTREE
// from the mention scan instead — slotMentionsOutside — which is the
// occurrence-level reading the exemption was reaching for.)

// statementSlotMentions is every slot a piece of syntax touches, for the
// ordering gate: the havoc enumerator's own set, WIDENED by the dotted
// paths it structurally cannot see.
//
// The widening is not an improvement on the enumerator — it is the same
// gap receiverBundleHavocSlots exists for, reached from another direction.
// havocSlotsOfStatement finds a mentioned local by IDENTIFIER, and a
// bundle slot spelled "this.count" has no identifier at its root: `this`
// is a keyword, so `this.count + this.bump()` walks that enumerator and
// contributes NOTHING for this.count. For the havoc floor that gap is
// covered at the call site; for the ordering gate it would be a hole in
// the one thing the gate must be right about, since the gate's whole job
// is to notice that the statement reads a slot the call writes.
//
// So every PROPERTY ACCESS PATH in the syntax is spelled out (dottedPathOf
// — the same reader the receiver threading uses) and its slot, where the
// caller has one, joins the set. Over-approximating is the safe direction:
// a mention the gate adds can only cause it to REFUSE a hoist, and a
// refused hoist costs precision, never soundness.
func statementSlotMentions(context *LoweringContext, node *ast.Node) (map[int]struct{}, bool) {
	slots, enumerable := havocSlotsOfStatement(context, node)
	if !enumerable {
		return nil, false
	}
	var visit func(current *ast.Node) bool
	visit = func(current *ast.Node) bool {
		if ast.IsPropertyAccessExpression(current) {
			if path, spelled := dottedPathOf(current); spelled {
				if slot, held := slotIndexOfName(context, path); held {
					slots[slot] = struct{}{}
				}
				// a path that names a flattened BUNDLE rather than a leaf
				// contributes every leaf under it
				for _, slot := range flattenedSlotsUnder(context, path) {
					slots[slot] = struct{}{}
				}
			}
		}
		if current.Kind == ast.KindThisKeyword {
			for _, slot := range flattenedSlotsUnder(context, "this") {
				slots[slot] = struct{}{}
			}
		}
		current.ForEachChild(visit)
		return false
	}
	visit(node)
	return slots, true
}

// hoistedCallWriteSlots is the caller slots a hoisted call STATEMENT
// writes, other than its own fresh temp: one per WRITTEN this-field bundle
// entry the receiver's path resolves to a caller slot.
//
// The reading is bundleRetsAndArgs' own, restated here because the gate
// must answer BEFORE the statement is built (a refusal must leave no temp
// and no statement behind). The two readings share receiverPathOf and
// slotIndexOfName, so a drift between them would have to be a drift in
// those, not between two spellings of the same rule.
//
// A callee whose lowering is not readable answers the EMPTY set together
// with a refusal upstream: summaryCallStatement would decline such a
// callee too, so the hoist never gets that far.
func hoistedCallWriteSlots(context *LoweringContext, call *ast.Node, callee *ast.Node) map[int]struct{} {
	writes := map[int]struct{}{}
	shape, known := LowerSummaryBody(context.Flow, callee)
	if !known {
		return writes
	}
	receiverPath, pathOk := receiverPathOf(call)
	if !pathOk {
		// no receiver path: the callee has no this-entries to write back
		// through, or the statement builder will decline the site outright
		return writes
	}
	for _, entry := range shape.BundleEntries {
		if !entry.Written || !strings.HasPrefix(entry.Path, "this.") {
			continue
		}
		field := strings.TrimPrefix(entry.Path, "this.")
		if slot, held := slotIndexOfName(context, receiverPath+"."+field); held {
			writes[slot] = struct{}{}
		}
	}
	return writes
}
