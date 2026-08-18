// A GROUNDED computed member read/write (`o['a']`, `o["a"]`) against a
// flattened record local, admitted to the SAME slot a dotted read
// (`o.a`) already resolves to.
//
// PathSlotIndexOf/propertyPathReading (ir_object_slots_slot_index.go,
// ir_object_slots.go) only ever walk PropertyAccessExpression steps —
// an ElementAccessExpression never reaches that spelling, however
// grounded its key is, so `o['a']` never found the slot `o.a` occupies.
// This file is the narrow admission: a string-literal (or no-substitution
// template) key, on an IDENTIFIER receiver, resolves to the identical
// spelled slot name `PathSlotIndexOf` would answer for the one-step
// dotted form — the two syntaxes name the same runtime property
// (sec-topropertykey: ToPropertyKey of a String argument is that string
// unchanged), so admitting the computed spelling here widens no claim
// the dotted read does not already make.
//
// A DYNAMIC key (a variable, a call result, a template with a
// substitution) is not this file's business and keeps declining —
// grounding is the whole point of the gate, and this file never guesses
// a key.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
)

// groundedComputedKeyOf reads a computed member's key as a grounded
// string: a string literal or a no-substitution template spells its own
// text directly. Anything else (an identifier, a call, a template with a
// substitution, a numeric key — ToPropertyKey of a number needs the
// source-spelling rule object_literal.go/element_access.go already
// apply on the interpreter side, not duplicated here) is not grounded.
func groundedComputedKeyOf(key *ast.Node) (string, bool) {
	head := Unwrapped(key)
	if head == nil {
		return "", false
	}
	if ast.IsStringLiteral(head) {
		return head.AsStringLiteral().Text, true
	}
	if ast.IsNoSubstitutionTemplateLiteral(head) {
		return head.Text(), true
	}
	return "", false
}

// GroundedComputedMemberSlotOf resolves `receiver['key']` /
// `receiver["key"]` to the slot a dotted `receiver.key` read on the same
// flattened record local would resolve to. (0, false) for anything not
// admitted: a non-identifier receiver, an optional chain (`o?.['a']` —
// the same "not a plain path step" refusal PathSlotIndexOf's own
// dotted reading gives `o?.a`), a dynamic key, or a receiver/key pair
// that simply has no slot under that spelling (an untracked local, a
// key the record's own flattening never named).
func GroundedComputedMemberSlotOf(context *LoweringContext, node *ast.Node) (int, bool) {
	head := Unwrapped(node)
	if head == nil || !ast.IsElementAccessExpression(head) {
		return 0, false
	}
	elem := head.AsElementAccessExpression()
	if elem.QuestionDotToken != nil {
		return 0, false
	}
	receiver := Unwrapped(elem.Expression)
	if receiver == nil || !ast.IsIdentifier(receiver) {
		return 0, false
	}
	key, groundedOk := groundedComputedKeyOf(elem.ArgumentExpression)
	if !groundedOk {
		return 0, false
	}
	return slotIndexOfName(context, receiver.Text()+"."+key)
}
