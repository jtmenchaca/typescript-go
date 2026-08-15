// split from ir_accessor_calls.go — the receiver, the temp's name, and the temp's sort

package walk

import (
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
)

/* ── the receiver, and the temp's name ───────────────────────────── */

// accessorReceiverPathOf spells the object an accessor was reached
// THROUGH, as the dotted path the caller's slots are named under:
//
//	this.x          → "this"
//	wrapper.x       → "wrapper"
//	this.holder.x   → "this.holder"
//
// It is receiverPathOf's rule on a property access rather than a call:
// the accessed expression MINUS its last step, which is the accessor
// name and never a slot. The declines are the same three, each because
// no slot spelling exists for what was written — a computed step, an
// optional step, and a root that is neither `this` nor an identifier.
func accessorReceiverPathOf(access *ast.Node) (string, bool) {
	if access == nil || !ast.IsPropertyAccessExpression(access) {
		return "", false
	}
	property := access.AsPropertyAccessExpression()
	// AN OPEN GAP, and it is left open deliberately. `o?.x` where x is an
	// accessor is a determinable shape — when the receiver is present the
	// accessor runs, and a conditional call would spell it — but the
	// absence half is the maybe-receiver machinery's, which lives outside
	// this file. Nothing here may claim the call happened, so the path
	// spelling declines and the read keeps the floor. The resolution's own
	// optional gate (AccessorDeclarationsOf) refuses the same shape first;
	// this is the second wall behind it.
	if property.QuestionDotToken != nil {
		return "", false
	}
	return dottedPathOf(Unwrapped(property.Expression))
}

// accessorTempName spells the slot a getter read lands in: the path the
// read named, under a marker no source name can collide with. `#` is the
// same character the base layout's own "#done"/"#ret" wear, and no
// JavaScript identifier or dotted path spells one.
//
// Two reads of the SAME path in one body get two temps, each with this
// same name. That is deliberate and it is sound: a getter runs code, so
// two reads may answer differently, and one shared slot would claim the
// second read gave the first read's value. slotIndexOfName answers the
// FIRST binding of a spelling, so the name is never resolved back to —
// only the index the allocation handed out is used.
func accessorTempName(receiverPath string, access *ast.Node) string {
	name := access.AsPropertyAccessExpression().Name().Text()
	var builder strings.Builder
	builder.WriteString("#get.")
	builder.WriteString(receiverPath)
	builder.WriteString(".")
	builder.WriteString(name)
	return builder.String()
}

/* ── the temp's sort ─────────────────────────────────────────────── */

// AccessorSortOf and AccessorTypeofOf read what a getter's RETURN wears,
// from the declaration's own type annotation — never from any call, the
// same law declaredParamSort states for parameters: a summary quantifies
// over all entries, so only what the annotation itself promises may be
// assumed of the value that comes back.
//
// They are methods on the context rather than free functions because the
// lowering asks them where it asks everything else about a slot, and a
// context that has no reading of its own answers the same unknown a
// missing annotation does.
//
// An unannotated getter's temp is unknown-sorted, which admits only the
// definedness test — the read still lowers, and its value simply says
// nothing more than "some value came back". That is strictly better than
// the floor, which havocs every slot the statement could have touched.
func (context *LoweringContext) AccessorSortOf(getter *ast.Node) BindingKind {
	sort, _ := accessorReturnEvidence(getter)
	return sort
}

// AccessorTypeofOf is the typeof half of the same reading.
func (context *LoweringContext) AccessorTypeofOf(getter *ast.Node) TypeofTag {
	_, tag := accessorReturnEvidence(getter)
	return tag
}

// accessorReturnEvidence reads a getter's declared return annotation as
// a sort and typeof pair, through annotationSort — the ONE annotation
// reading the field census takes, so a getter returning `number` and a
// field declared `number` sort identically.
func accessorReturnEvidence(getter *ast.Node) (BindingKind, TypeofTag) {
	if getter == nil || !ast.IsGetAccessorDeclaration(getter) {
		return BindingKindUnknown, TypeofTagNone
	}
	return annotationSort(getter.AsGetAccessorDeclaration().Type)
}
