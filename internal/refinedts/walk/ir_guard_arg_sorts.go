// split from ir_guard.go — the argument sort and typeof readings for
// inlined parameter slots

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
)

// SortOfArg is sortOfArg in the TS source: the static sort of an
// argument expression, for an inlined parameter slot.
func SortOfArg(context *LoweringContext, e *ast.Node) BindingKind {
	if i, ok := IndexOf(context, e); ok {
		return context.Sorts[i]
	}
	head := Unwrapped(e)
	if ast.IsStringLiteral(head) {
		return BindingKindString
	}
	// a template or a concatenation of string-sorted parts is a string
	// argument — the same reading RhsEffect gives a string-sorted slot
	if _, ok := SequenceEffectOf(context, e); ok {
		return BindingKindString
	}
	if _, ok := EffectOf(context, e); ok {
		return BindingKindNumber
	}
	return BindingKindUnknown
}

// TypeofOfArg is typeofOfArg in the TS source: the typeof evidence
// an argument expression carries into an inlined parameter slot.
func TypeofOfArg(context *LoweringContext, e *ast.Node) TypeofTag {
	if i, ok := IndexOf(context, e); ok {
		if context.Typeofs != nil && i < len(context.Typeofs) {
			return context.Typeofs[i]
		}
		return TypeofTagNone
	}
	head := Unwrapped(e)
	if ast.IsStringLiteral(head) {
		return TypeofTagString
	}
	if head.Kind == ast.KindTrueKeyword || head.Kind == ast.KindFalseKeyword {
		return TypeofTagBoolean
	}
	if _, ok := SequenceEffectOf(context, e); ok {
		return TypeofTagString
	}
	if _, ok := EffectOf(context, e); ok {
		return TypeofTagNumber
	}
	return TypeofTagNone
}
