// split from ir_map_slots.go — the iterated-position resolution
//
// Which slot each pass of a for-of hands the element binding, and the
// (keys, vals) pair a two-name entry pattern takes instead.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
)

// MapIterationSlotOf resolves an iterated expression to the slot each
// pass hands the element binding: `s` and `m.values()` answer the value
// slot, `m.keys()` the key slot. A Map iterated BARE or through
// `entries()` hands a PAIR, which one slot cannot spell — pairIterated
// says so and MapForOfLowering takes the two-name route instead.
func MapIterationSlotOf(context *LoweringContext, iterated *ast.Node) (slot int, pairIterated bool, ok bool) {
	head := Unwrapped(iterated)
	if ast.IsIdentifier(head) {
		_, valsSlot, _, keysOk, found := mapSlotsOf(context, head.Text())
		if !found {
			return 0, false, false
		}
		// a bare Map iterates its ENTRIES; a bare Set iterates its members
		if keysOk {
			return 0, true, true
		}
		return valsSlot, false, true
	}
	access := head
	if !ast.IsCallExpression(access) {
		return 0, false, false
	}
	property := access.AsCallExpression().Expression
	if !ast.IsPropertyAccessExpression(property) {
		return 0, false, false
	}
	receiver := property.AsPropertyAccessExpression().Expression
	if !ast.IsIdentifier(receiver) {
		return 0, false, false
	}
	view, isView := iteratorCallOf(head, receiver.Text())
	if !isView {
		return 0, false, false
	}
	_, valsSlot, keysSlot, keysOk, found := mapSlotsOf(context, receiver.Text())
	if !found {
		return 0, false, false
	}
	switch view {
	case "values":
		return valsSlot, false, true
	case "keys":
		if !keysOk {
			return 0, false, false
		}
		return keysSlot, false, true
	case "entries":
		if !keysOk {
			return 0, false, false
		}
		return 0, true, true
	}
	return 0, false, false
}

// MapEntrySlotsOf is the (keys, vals) slot pair a PAIR iteration hands
// out — `for (const [k, v] of m)` and `of m.entries()`.
func MapEntrySlotsOf(context *LoweringContext, iterated *ast.Node) (keysSlot int, valsSlot int, ok bool) {
	head := Unwrapped(iterated)
	name := ""
	if ast.IsIdentifier(head) {
		name = head.Text()
	} else if ast.IsCallExpression(head) {
		property := head.AsCallExpression().Expression
		if !ast.IsPropertyAccessExpression(property) {
			return 0, 0, false
		}
		receiver := property.AsPropertyAccessExpression().Expression
		if !ast.IsIdentifier(receiver) {
			return 0, 0, false
		}
		view, isView := iteratorCallOf(head, receiver.Text())
		if !isView || view != "entries" {
			return 0, 0, false
		}
		name = receiver.Text()
	}
	if name == "" {
		return 0, 0, false
	}
	_, valsSlot, keysSlot, keysOk, found := mapSlotsOf(context, name)
	if !found || !keysOk {
		return 0, 0, false
	}
	return keysSlot, valsSlot, true
}
