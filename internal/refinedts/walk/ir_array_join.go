// split from ir_array_slots.go — `a.join(sep)`: the string-sort-only
// read over a flattened array's two slots.
//
// join's result is never absent and never thrown for an ordinary array
// receiver and a stated-string separator
// (sec-array.prototype.join steps 1-8: every step is a String
// operation over a length the receiver states and a separator ToString
// already reduced to a String; nothing in the algorithm returns
// undefined/null or raises). What the CONTENT is depends on every
// element's own ToString, which the elem slot's weak join does not
// pin — so the claim stops at the sort: join answers a string, and
// nothing about which one.
package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// ArrayJoinEffect is `a.join(sep)` under a STRING-sorted target: sort-
// only unknown, gated on the separator being a stated string (a
// literal, or absent — sec-array.prototype.join step 3, `undefined`
// reads as the default "," separator, itself a String) and the
// element sort being string- or number-sorted. Both gates rest on the
// same clause: sec-array.prototype.join step 7.a.i's `? ToString(_element_)`
// and step 3's `? ToString(_separator_)` both run sec-tostring, whose
// String and Number rows (steps 1, 7) return without invoking
// ToPrimitive — the one row that could call a user `toString` /
// `Symbol.toPrimitive`. So a string- or number-sorted operand can
// never make join throw or run code, which is what lets the read stop
// at the sort: every step 1-8 either walks the length or converts a
// String/Number, so the result is always a String
// (never absent, never thrown) — but WHICH string depends on the
// elem slot's weak join, which pins no content. A separator that is
// not stated — a variable of unknown sort, a template with a call
// inside, anything the parser did not pin to String or Number —
// declines: the algorithm still answers a String, but this reader
// only answers where it can name the premise it used, and an unread
// separator's shape is not one.
func ArrayJoinEffect(context *LoweringContext, node *ast.Node, targetSort BindingKind) (kernelbridge.LoopEffect, bool) {
	if targetSort != BindingKindString {
		return kernelbridge.LoopEffect{}, false
	}
	receiver, receiverOk := arrayReadReceiverOf(node)
	if !receiverOk {
		return kernelbridge.LoopEffect{}, false
	}
	method, argument, isRead := arrayReadMethodCallOf(node, receiver)
	if !isRead || method != "join" {
		return kernelbridge.LoopEffect{}, false
	}
	_, elemSlot, ok := arraySlotsOf(context, receiver)
	if !ok {
		return kernelbridge.LoopEffect{}, false
	}
	elemSort := context.Sorts[elemSlot]
	if elemSort != BindingKindString && elemSort != BindingKindNumber {
		return kernelbridge.LoopEffect{}, false
	}
	if !joinSeparatorIsStatedString(context, argument) {
		return kernelbridge.LoopEffect{}, false
	}
	return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectUnknown}, true
}

// joinSeparatorIsStatedString is whether a join argument is a shape
// ToString converts WITHOUT RUNNING CODE: absent (the default ","
// separator), a string literal, or a NAME already tracked under the
// string or number sort. ToString on a String or a Number argument
// never calls out (sec-tostring), which is exactly what those two
// sorts pin; anything else — an object-typed name, an arbitrary
// expression writeAndCallFree alone would pass — could still carry a
// user `toString`/`Symbol.toPrimitive` ToString invokes, so the gate
// stops at a sort the two-slot flattening already vouches for rather
// than at mere syntax. The separator's OWN value is never read here
// either way; join's claim stops at the sort.
func joinSeparatorIsStatedString(context *LoweringContext, argument *ast.Node) bool {
	if argument == nil {
		return true
	}
	head := Unwrapped(argument)
	if ast.IsStringLiteral(head) || ast.IsNoSubstitutionTemplateLiteral(head) {
		return true
	}
	if ast.IsNumericLiteral(head) {
		return true
	}
	if name, spelled := SpelledNameOf(head); spelled {
		if slot, found := slotIndexOfName(context, name); found {
			sort := context.Sorts[slot]
			return sort == BindingKindString || sort == BindingKindNumber
		}
	}
	return false
}
