// split from ir_array_slots.go — what the element slot is WORTH: the
// sort it wears and the typeof evidence it carries

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
)

// ArrayElementSort is a flattened array's ELEMENT sort, read from the
// literal's own elements: every element string-shaped by syntax makes a
// string element slot; anything else the lowering reads numerically. An
// EMPTY literal has no element to read, so its sort is unknown until a
// push writes one — and the number sort is the one the pushes and the
// index reads speak, so an empty literal takes it too.
//
// A PARAMETER array has no elements at all — its values come from the
// caller — so it answers the sort read from its declared type instead.
func ArrayElementSort(local ArrayLocal) BindingKind {
	if local.statesElementSort() {
		return local.DeclaredElementSort
	}
	if len(local.Elements) == 0 {
		return BindingKindNumber
	}
	for _, element := range local.Elements {
		if !SpelledSequenceShape(element) {
			return BindingKindNumber
		}
	}
	return BindingKindString
}

// ArrayElementTypeof is a flattened array's element typeof evidence,
// from the literal's syntax alone. Only an all-same reading claims
// anything; a mixed or unreadable literal claims nothing, and typeof
// tests on the element slot then decline.
//
// A PARAMETER array's evidence follows its declared element sort: a
// number-sorted element type is `typeof x === "number"` for every
// element the caller can pass, a string-sorted one likewise. An unknown
// sort claims nothing, which is what a union or an unread type is worth.
func ArrayElementTypeof(local ArrayLocal) TypeofTag {
	if local.statesElementSort() {
		switch local.DeclaredElementSort {
		case BindingKindNumber:
			return TypeofTagNumber
		case BindingKindString:
			return TypeofTagString
		}
		return TypeofTagNone
	}
	if len(local.Elements) == 0 {
		return TypeofTagNone
	}
	var held TypeofTag
	for index, element := range local.Elements {
		e := Unwrapped(element)
		var tag TypeofTag
		switch {
		case SpelledSequenceShape(e):
			tag = TypeofTagString
		case e.Kind == ast.KindTrueKeyword || e.Kind == ast.KindFalseKeyword:
			tag = TypeofTagBoolean
		default:
			if _, isNumber := NumberOf(e); ast.IsNumericLiteral(e) || isNumber {
				tag = TypeofTagNumber
			} else {
				return TypeofTagNone
			}
		}
		if index == 0 {
			held = tag
			continue
		}
		if tag != held {
			return TypeofTagNone
		}
	}
	return held
}
