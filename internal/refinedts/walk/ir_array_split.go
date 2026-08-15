// split from ir_array_slots.go — the SPLIT array `const parts =
// s.split(sep)`: its astral-safety gate, its recognizer, and its
// uneven two-slot lowering

package walk

import (
	"unicode/utf8"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// splitSourceOf reads a declaration's initializer as `s.split(sep)`
// with an ASTRAL-SAFE separator, and answers the receiver expression.
//
// Two gates, and both are about surrogate pairs. `String.prototype.
// split` at a string separator cuts the receiver only where the
// separator MATCHES, and a match of a well-formed separator begins and
// ends on a scalar boundary — each of its code units pairs with the
// same partner inside the receiver as it does in the separator. So no
// piece can begin or end mid-pair, and the kernel's drawn-from claim
// holds. The exception is a separator that is ITSELF a lone surrogate:
// `"𝐀".split("\uD835")` matches the high half of an astral pair and
// does split it, minting a lone surrogate the receiver never held.
//
// The gates are therefore: the separator must be a spelled string
// literal (so its code units can be read at all), and it must contain
// no unpaired surrogate. A regular-expression separator, a computed
// separator, a limit argument, and a missing separator all decline —
// each would be a claim with nothing behind it.
func splitSourceOf(declaration *ast.Node) (*ast.Node, bool) {
	if !ast.IsVariableDeclaration(declaration) {
		return nil, false
	}
	initializer := declaration.AsVariableDeclaration().Initializer
	if initializer == nil {
		return nil, false
	}
	head := Unwrapped(initializer)
	if !ast.IsCallExpression(head) {
		return nil, false
	}
	call := head.AsCallExpression()
	if !ast.IsPropertyAccessExpression(call.Expression) {
		return nil, false
	}
	access := call.Expression.AsPropertyAccessExpression()
	if access.QuestionDotToken != nil {
		return nil, false
	}
	if !ast.IsIdentifier(access.Name()) || access.Name().Text() != "split" {
		return nil, false
	}
	// exactly one argument: a `limit` second argument truncates the piece
	// list, which changes no piece's contents but is not a shape this
	// recognizer has read, so it declines rather than guess
	if call.Arguments == nil || len(call.Arguments.Nodes) != 1 {
		return nil, false
	}
	separator := Unwrapped(call.Arguments.Nodes[0])
	if !ast.IsStringLiteral(separator) && !ast.IsNoSubstitutionTemplateLiteral(separator) {
		return nil, false
	}
	if !astralSafeSeparator(separator.Text()) {
		return nil, false
	}
	return access.Expression, true
}

// astralSafeSeparator is whether a separator's text contains no
// UNPAIRED surrogate — the one premise that keeps a split from cutting
// inside an astral pair.
//
// Go strings hold the source text as UTF-8, so a well-formed separator
// decodes with no errors. A lone surrogate cannot be encoded in UTF-8
// at all, and the parser's own escape handling turns `"\uD835"` into
// the replacement rune — so any RuneError in the decoded text is a
// spelling this reader must not vouch for. Declining on a literal
// replacement character (U+FFFD spelled outright) costs a claim on a
// receiver nobody writes, and buys the gate with no engine-specific
// reasoning.
func astralSafeSeparator(text string) bool {
	if text == "" {
		// the empty separator splits between every UTF-16 CODE UNIT, which
		// is exactly the slice-shaped cut: it lands inside an astral pair
		// and mints two lone surrogates
		return false
	}
	for _, r := range text {
		if r == utf8.RuneError {
			return false
		}
		// the surrogate range itself, if it ever reaches here
		if r >= 0xD800 && r <= 0xDFFF {
			return false
		}
	}
	return true
}

// splitArrayLocalOf is the SPLIT recognizer: `const parts =
// s.split(sep)` under the astral-safe gate above. The result is an
// ArrayLocal in every respect — the same two slot spellings, the same
// use scan — so every downstream reader treats it as it treats any
// other flattened array. What differs is where the two slots' values
// come from: the elem slot from the kernel's drawn-from row over the
// receiver, the len slot from nothing at all.
func splitArrayLocalOf(body *ast.Node, declaration *ast.Node) (ArrayLocal, bool) {
	if !ast.IsIdentifier(declaration.AsVariableDeclaration().Name()) {
		return ArrayLocal{}, false
	}
	receiver, isSplit := splitSourceOf(declaration)
	if !isSplit {
		return ArrayLocal{}, false
	}
	name := declaration.AsVariableDeclaration().Name().Text()
	// the receiver may not name the array being declared — at the point
	// the split runs, its own slots have not been written yet
	if mentionsName(receiver, name) {
		return ArrayLocal{}, false
	}
	if !usesAreAllArrayForms(body, declaration, name) {
		return ArrayLocal{}, false
	}
	return ArrayLocal{
		Declaration:  declaration,
		Name:         name,
		LenSlotName:  name + arrayLenSuffix,
		ElemSlotName: name + arrayElemSuffix,
		// no element expressions: the pieces are computed. The sort rides
		// the stated-sort channel instead, and a split's pieces are
		// strings whatever the receiver held
		DeclaredElementSort: BindingKindString,
		SplitReceiver:       receiver,
	}, true
}

// splitDeclarationAssignmentsOf is the SPLIT's lowering: `const parts =
// s.split(sep)` writes the two slots UNEVENLY, and the unevenness is
// the honest part.
//
// The ELEM slot takes the kernel's drawn-from row over the receiver's
// sequence reading. A piece is a contiguous stretch of the receiver, so
// its scalars all occurred there and it is no longer — which is exactly
// what that row claims, and all of what it claims. The gate on the
// separator was applied by the recognizer (splitSourceOf): a match of a
// well-formed separator begins and ends on a scalar boundary, so no
// piece can be cut mid-surrogate-pair.
//
// The LEN slot takes UNKNOWN. How many pieces there are depends on how
// many times the separator occurs in the receiver, which the receiver's
// SET does not state — a set-known string can hold zero occurrences or
// a hundred. Writing any count here, including a floor of one, would be
// a claim with nothing behind it, so the slot claims nothing and every
// `parts.length` read answers unknown.
//
// A receiver with no sequence reading declines outright: without the
// receiver's set there is nothing for the row to draw from.
func splitDeclarationAssignmentsOf(context *LoweringContext, declaration *ast.Node) ([]AssignmentTarget, bool) {
	receiver, isSplit := splitSourceOf(declaration)
	if !isSplit {
		return nil, false
	}
	if !ast.IsIdentifier(declaration.AsVariableDeclaration().Name()) {
		return nil, false
	}
	lenSlot, elemSlot, ok := arraySlotsOf(context, declaration.AsVariableDeclaration().Name().Text())
	if !ok {
		return nil, false
	}
	receiverEffect, receiverOk := sequenceEffectOf(context, receiver, true /*inSequence*/)
	if !receiverOk {
		return nil, false
	}
	return []AssignmentTarget{
		{Target: lenSlot, Effect: kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectUnknown}},
		{Target: elemSlot, Effect: kernelbridge.LoopEffect{
			Kind: kernelbridge.LoopEffectSeqUnary,
			Op:   kernelbridge.LoopOpSplitElemSafe,
			A:    &receiverEffect,
		}},
	}, true
}
