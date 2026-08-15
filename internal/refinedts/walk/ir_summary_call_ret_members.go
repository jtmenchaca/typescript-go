// split from ir_summary_call.go — where a constructed instance's fields and a returned value's members land

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

/* ── the constructed instance's fields ───────────────────────────── */

// targetNameOf spells the caller slot a call's value lands in, so the
// field threading below can look for that name's own leaves. A target of
// -1, or one past the binding vector, spells nothing.
func targetNameOf(context *LoweringContext, target int) string {
	if context == nil || target < 0 || target >= len(context.Bindings) {
		return ""
	}
	return context.Bindings[target]
}

// constructorFieldRets writes the CONSTRUCTOR's this-field exits into the
// caller's own slots for the fresh instance's fields.
//
// A `new X()` runs a constructor whose summary already carries one
// this-entry per field the body touches, with Written marking the ones it
// assigns (the same BundleEntries an ordinary method rides with — nothing
// in the layout special-cases a constructor out of that machinery). Those
// entries entered ABSENT, which is exactly a field before its initializer
// runs, and their EXITS hold what the constructor left in them.
//
// The caller can read those exits wherever it holds a slot for the same
// field of the same instance: `const c = new C()` with the caller's own
// "c.count" flattened is the shape, and the row's out then lands on that
// slot. Where the caller flattened nothing the rows stay -1 and the
// instance is the unknown it has always been — dropping them is sound for
// the reason the receiver threading gives: nothing lowered can read a
// spelling the caller has no slot for.
//
// This is the ONE difference from an ordinary call's write-back, and it
// is a difference in direction only: an ordinary call maps a written
// field back onto a slot the caller filled on the way IN, while a fresh
// instance's fields were never filled by anyone — the entries stayed
// absent — so these rows carry values OUT of a constructor into slots the
// caller had no value for. The exits are the constructor's own writes
// either way, and summarize_eq covers the whole exit row.
func constructorFieldRets(
	context *LoweringContext,
	call *ast.Node,
	targetName string,
	statement kernelbridge.IrStatement,
) kernelbridge.IrStatement {
	if context == nil || targetName == "" || statement.Rets == nil {
		return statement
	}
	callee := summaryCalleeOf(context, call)
	if callee == nil {
		return statement
	}
	calleeShape, known := LowerSummaryBody(context.Flow, callee)
	if !known {
		return statement
	}
	for _, entry := range calleeShape.BundleEntries {
		if !entry.Written {
			// a field the constructor only READS holds whatever it entered
			// with, which for a fresh instance is absent — writing that back
			// would claim the field IS undefined, and the class's own
			// initializers may have set it outside this body's sight
			continue
		}
		field, isThis := thisFieldNameOf(entry.Path)
		if !isThis {
			continue
		}
		slot, held := slotIndexOfName(context, targetName+"."+field)
		if !held {
			continue
		}
		if entry.Index < 0 || entry.Index >= len(statement.Rets) {
			continue
		}
		statement.Rets[entry.Index] = slot
	}
	return statement
}

/* ── the returned value's members ────────────────────────────────── */

// threadRetMemberRets decides where a member-carrying return's exits
// land in the CALLER.
//
// The callee's RetMembers name one out-slot per member of the value it
// returns. A caller writing that value into one scalar slot has no place
// for them — an object is not a scalar, and the target holds the same
// unknown it always held — so the rows stay -1 and the exits are dropped.
// Dropping is sound and not weaker than declining: nothing the caller
// lowered can read a member of a value it has no name for, so no
// knowledge survives the call for the dropped rows to falsify.
//
// The rows become READABLE at the call sites that DO hold names for the
// members: `const { a, b } = f()`, where the caller flattened `a` and `b`
// as its own locals. That threading is the destructure route's to make —
// it knows which local each key binds to — and it reads this same
// RetMembers list, so the layout's answer about which slot is which
// member is the one answer all three seams walk.
//
// (false) never today: every shape this route meets is either threaded or
// left at -1, and there is no member layout that makes the call itself
// unlowerable. The flag rides so the caller's three threadings read the
// same way.
func threadRetMemberRets(
	context *LoweringContext,
	calleeShape LoweredSummary,
	target int,
	rets []int,
) bool {
	if calleeShape.RetShape == RetShapeNone || len(calleeShape.RetMembers) == 0 {
		return true
	}
	for _, member := range calleeShape.RetMembers {
		if member.Index < 0 || member.Index >= len(rets) {
			// the layout and this vector disagree about how many slots the
			// callee has — the call declines rather than writing an exit into
			// a position nothing laid out
			return false
		}
		// a caller slot spelled "<target's name>.<member>" is what a
		// destructured or flattened target would hold. The statement route's
		// target is an index, not a name, so nothing is spelled here and the
		// row is dropped; the direct-apply route serves these sites.
		_ = target
		rets[member.Index] = -1
	}
	return true
}
