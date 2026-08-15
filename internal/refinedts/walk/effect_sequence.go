// split from effect_expression.go — the sequence (string) half of the grammar

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// stringSlotEffect is a tracked STRING-sorted read as an effect, or
// (zero, false). Sequence building admits only the string sort: a
// number-sorted operand of `+` is arithmetic, not concatenation, and
// an unknown-sorted one is a reread across sorts, which is never a
// claim.
func stringSlotEffect(context *LoweringContext, e *ast.Node) (kernelbridge.LoopEffect, bool) {
	i, ok := IndexOf(context, e)
	if !ok {
		return kernelbridge.LoopEffect{}, false
	}
	if context.Sorts[i] != BindingKindString {
		return kernelbridge.LoopEffect{}, false
	}
	return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectVar, Index: i}, true
}

// concatOf pairs two operand effects into one concatenation effect.
func concatOf(a, b kernelbridge.LoopEffect) kernelbridge.LoopEffect {
	return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectConcat, A: &a, B: &b}
}

// SequenceEffectOf reads an expression as a SEQUENCE effect — the
// string world's half of the effect grammar, beside the numeric
// LowerEffectExpression:
//
//   - a string literal is its exact tuple;
//   - a tracked string-sorted name is a read;
//   - `a + b` with BOTH sides sequence-readable is their
//     concatenation (a `+` with a number-sorted operand is arithmetic
//     and is not read here — it takes the numeric route as before);
//   - a template literal `a${x}b` is that concatenation spelled out,
//     its literal chunks as exact tuples and its substitutions as
//     sequence reads.
//
// A non-string or untracked part declines the whole reading, exactly
// as it did before this route existed. Parens and casts unwrap first.
//
// A CALL is read only where the syntax already committed the expression
// to being a sequence — inside a `+` chain or a template substitution.
// Handed a bare call as the WHOLE expression this declines, because
// SortOfArg (ir_guard.go) reads a success here as "this argument is
// string-sorted", and a numeric callee's result is not.
func SequenceEffectOf(context *LoweringContext, e *ast.Node) (kernelbridge.LoopEffect, bool) {
	return sequenceEffectOf(context, e, false /*inSequence*/)
}

// sequenceEffectOf is the reading, carrying whether the caller has already
// committed the expression to the sequence world.
func sequenceEffectOf(context *LoweringContext, e *ast.Node, inSequence bool) (kernelbridge.LoopEffect, bool) {
	head := Unwrapped(e)
	if ast.IsStringLiteral(head) {
		return kernelbridge.LoopEffect{
			Kind: kernelbridge.LoopEffectConst,
			Set:  refinementsets.StringTuple(head.AsStringLiteral().Text),
		}, true
	}
	if ast.IsNoSubstitutionTemplateLiteral(head) {
		return kernelbridge.LoopEffect{
			Kind: kernelbridge.LoopEffectConst,
			Set:  refinementsets.StringTuple(head.AsNoSubstitutionTemplateLiteral().Text),
		}, true
	}
	if slot, ok := stringSlotEffect(context, head); ok {
		return slot, true
	}
	// a free (or imported) CONST whose initializer is a string literal —
	// nest's `DEFAULT_METHOD_KEY` behind
	// `this.staticMethodKey ??= DEFAULT_METHOD_KEY as StaticMethodKey`.
	// The declaration is one stable node in the program's shared AST
	// forest and a const's initializer is its value, so the tuple reads
	// directly, exactly as the numeric route's FreeConstEffect reads a
	// numeric const. The cast around it unwrapped above.
	if tuple, ok := freeStringConstEffect(context, head); ok {
		return tuple, true
	}
	// `a[i]` on a string-sorted flattened array is a sequence read — the
	// element slot, or-absent where nothing bounds i
	if slot, ok := ArrayElementSlotOf(context, head); ok && context.Sorts[slot] == BindingKindString {
		return ArrayIndexReadEffect(context, head)
	}
	// `m.get(k)` on a string-sorted collection is the same or-absent
	// read of the values slot
	if slot, ok := MapValueSlotOf(context, head); ok && context.Sorts[slot] == BindingKindString {
		return MapGetReadEffect(context, head)
	}
	if ast.IsBinaryExpression(head) {
		bin := head.AsBinaryExpression()
		if bin.OperatorToken.Kind != ast.KindPlusToken {
			return kernelbridge.LoopEffect{}, false
		}
		// a `+` chain commits BOTH sides to the sequence world, so a call in
		// either operand is a call inside a sequence
		a, aOk := sequenceEffectOf(context, bin.Left, true /*inSequence*/)
		if !aOk {
			return kernelbridge.LoopEffect{}, false
		}
		b, bOk := sequenceEffectOf(context, bin.Right, true /*inSequence*/)
		if !bOk {
			return kernelbridge.LoopEffect{}, false
		}
		return concatOf(a, b), true
	}
	if ast.IsTemplateExpression(head) {
		return templateSequenceOf(context, head)
	}
	// a STRING METHOD whose receiver and arguments are all exactly known —
	// `"a-b".split("-")[0]` is not this, but `"Recharts".slice(0, 3)` is:
	// the whole call computes to one string here in Go, and the exact
	// tuple is what the wire carries. Ahead of the hoist, which would
	// otherwise spend a temp slot and answer unknown for a value that is
	// pinned.
	if exact, ok := exactStringMethodEffect(context, head); ok {
		return exact, true
	}
	// the trims over a NON-exact sequence-readable receiver ride the
	// proved sequence-unary rows: the result's scalars are drawn from
	// the receiver's and it is no longer (sec-trimstring removes by
	// code point, so no surrogate pair splits).
	//
	// slice and the case mappings ride their own GATED rows beside
	// them. Each fails the plain drawn-from premise and each recovers
	// under a premise about the receiver's ALPHABET:
	//
	//   slice cuts at UTF-16 code UNIT positions
	//   (sec-string.prototype.slice), so on an astral-bearing receiver a
	//   cut can fall inside a surrogate pair and mint a lone surrogate.
	//   Where every scalar is in the BMP each is one code unit, so unit
	//   and scalar positions coincide, every cut is at a scalar boundary,
	//   and the piece is a contiguous subsequence -- the trims' row
	//   verbatim.
	//
	//   toUpperCase/toLowerCase REPLACE scalars, so nothing is drawn
	//   from anything. What holds instead is that the result is the
	//   receiver mapped scalar-by-scalar
	//   (sec-string.prototype.tolowercase maps by code point:
	//   StringToCodePoints, then the Default Case Conversion, then
	//   CodePointsToString). Below U+0080 no SpecialCasing row applies,
	//   so the map is one scalar to one scalar and length is preserved
	//   -- which is why the row keeps BOTH repetition bounds where the
	//   drawn-from rows drop the floor.
	//
	// THE GATE IS THE KERNEL'S, not this reader's, and that is the
	// difference from `split`. A split's premise is about the SEPARATOR,
	// a value the kernel never sees, so the adapter establishes it and
	// carries it in the op name. These two premises are about the
	// RECEIVER'S OWN SET, which the kernel holds -- so the name only
	// says which row is meant and the kernel decides the alphabet bound
	// itself (`bmpAlphabetB` / `asciiAlphabetB`, set_functions/walk.lean)
	// and answers `top` on a receiver whose set does not state it. This
	// reader therefore emits the op on syntax alone and never asserts
	// the premise, which is what keeps a set it cannot inspect from
	// becoming a claim it cannot back.
	//
	// `replace` keeps the decline outright: it substitutes caller-chosen
	// text, so neither closure applies under any alphabet.
	//
	// `split`, `indexOf`/`lastIndexOf`, `includes`/`startsWith`/
	// `endsWith`, and `.length` are not declines and are not here: none
	// answers a STRING. A split answers an ARRAY, lowered to the two
	// slots by ir_array_slots.go (its elem slot takes the same
	// drawn-from row under an astral-safe separator gate, its len slot
	// takes unknown); indexOf/lastIndexOf answer a NUMBER and includes/
	// startsWith/endsWith answer a BOOLEAN, both read by the numeric
	// reader's stringIndexOfEffect / stringBooleanSearchEffect
	// (ir_assignment_effect_read.go); `.length` likewise answers a
	// NUMBER, read by that same file's stringLengthEffect. `charAt` DOES
	// answer a string and IS here, riding the slice row below — see its
	// own comment for why no tighter claim is reachable.
	if ast.IsCallExpression(head) {
		call := head.AsCallExpression()
		if ast.IsPropertyAccessExpression(call.Expression) &&
			(call.Arguments == nil || len(call.Arguments.Nodes) == 0) {
			pa := call.Expression.AsPropertyAccessExpression()
			var seqOp kernelbridge.LoopEffectOp
			switch pa.Name().Text() {
			case "trim":
				seqOp = kernelbridge.LoopOpTrim
			case "trimStart", "trimLeft":
				// trimLeft IS trimStart: the spec's Annex B gives it as
				// the SAME function object ("The initial value of the
				// 'trimLeft' property is %String.prototype.trimStart%",
				// annex "String.prototype.trimleft"), not a second
				// implementation, so the two spellings ride one op.
				seqOp = kernelbridge.LoopOpTrimStart
			case "trimEnd", "trimRight":
				// trimRight IS trimEnd for the same Annex B reason
				// (annex "String.prototype.trimright").
				seqOp = kernelbridge.LoopOpTrimEnd
			case "toUpperCase":
				seqOp = kernelbridge.LoopOpUpperAscii
			case "toLowerCase":
				seqOp = kernelbridge.LoopOpLowerAscii
			}
			if seqOp != "" {
				if receiver, ok := sequenceEffectOf(context, pa.Expression, true /*inSequence*/); ok {
					return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectSeqUnary, Op: seqOp, A: &receiver}, true
				}
			}
		}
		// replace/replaceAll at a replacement this side holds EXACTLY.
		// Read the gates at replaceUnionEffect; the row itself is the
		// kernel's union closure over the receiver's alphabet and the
		// replacement's scalars.
		if substitution, ok := replaceUnionEffect(context, call); ok {
			return substitution, true
		}
		// slice carries ARGUMENTS (the cut positions), so it sits apart
		// from the zero-argument methods above. The positions themselves
		// need no reading: the kernel's row holds for EVERY cut, because
		// under the BMP gate every cut is at a scalar boundary and the
		// piece is a subsequence whatever the endpoints were. What the
		// arguments must not do is compute -- a call or an await inside
		// one would have to hoist -- so only plain expressions ride.
		if ast.IsPropertyAccessExpression(call.Expression) {
			pa := call.Expression.AsPropertyAccessExpression()
			if pa.Name().Text() == "slice" && sliceArgumentsArePlain(call) {
				if receiver, ok := sequenceEffectOf(context, pa.Expression, true /*inSequence*/); ok {
					return kernelbridge.LoopEffect{
						Kind: kernelbridge.LoopEffectSeqUnary,
						Op:   kernelbridge.LoopOpSliceBmp,
						A:    &receiver,
					}, true
				}
			}
			// charAt(i) IS a one-argument slice: sec-string.prototype.charat
			// step 7 answers "the substring of string from position to
			// position + 1" (empty where the index is out of range, steps
			// 5-6) — exactly substring(pos, pos+1), the same cut slice
			// performs. Under the BMP gate sliceBmp already wears, every cut
			// lands on a scalar boundary and the piece is a contiguous
			// subsequence of the receiver whatever the endpoints were, which
			// is what makes charAt's result "drawn from the receiver's
			// alphabet, no longer than it" — the exact claim sliceBmp's row
			// carries. This is the SAME kernel row as slice, ridden under
			// the SAME name (LoopOpSliceBmp); charAt gets no tighter
			// [0,1]-length claim than that row states, because no wire op
			// exists to send a length window beside the alphabet one (the
			// gated seqUn family, seqOp1Of, has no such shape) — an ungated
			// or non-BMP receiver stays floored exactly as slice's does.
			if pa.Name().Text() == "charAt" && sliceArgumentsArePlain(call) &&
				call.Arguments != nil && len(call.Arguments.Nodes) == 1 {
				if receiver, ok := sequenceEffectOf(context, pa.Expression, true /*inSequence*/); ok {
					return kernelbridge.LoopEffect{
						Kind: kernelbridge.LoopEffectSeqUnary,
						Op:   kernelbridge.LoopOpSliceBmp,
						A:    &receiver,
					}, true
				}
			}
		}
	}
	// a CALL inside the sequence — `"n=" + this.name()`, a template
	// substitution `${this.name()}` — hoists to a temp-slot call statement
	// ahead of this statement, and the concatenation reads the temp
	// (ir_call_hoist.go). Refused wherever no statement stream exists or
	// the reordering could be observed, and then the reading declines
	// exactly as it did before.
	//
	// The hoist is asked only for a call whose spelling is INSIDE a
	// sequence the syntax already committed to — a bare call handed to this
	// reader on its own is not a sequence, and answering one here would
	// tell SortOfArg (ir_guard.go) that a numeric callee's result is
	// string-sorted, since SortOfArg reads "SequenceEffectOf succeeded" as
	// exactly that claim. So the whole-expression case declines and the
	// numeric reader (EffectOf's Opaque) hoists it instead; the memo means
	// the two never build two statements for one site.
	if inSequence && (ast.IsCallExpression(head) || isAwaitedCallShape(head)) {
		return HoistCallEffect(context, head)
	}
	// a string-sorted GETTER read inside a sequence: the same hoisted
	// call, admitted only where the temp's sort came out string — the
	// same commitment gate the explicit call above wears, for the same
	// SortOfArg reason
	if inSequence && ast.IsPropertyAccessExpression(head) {
		if held, ok := GetterReadEffect(context, head); ok &&
			held.Kind == kernelbridge.LoopEffectVar &&
			held.Index < len(context.Sorts) &&
			context.Sorts[held.Index] == BindingKindString {
			return held, true
		}
	}
	return kernelbridge.LoopEffect{}, false
}

// SpelledSequenceShape is whether an expression is a sequence by its
// own SYNTAX alone — a string literal, any template literal, or a `+`
// chain of those. It reads no names and consults no slot vector, so it
// answers the same in any context: the sort question a caller must
// settle before the callee's slots exist.
func SpelledSequenceShape(e *ast.Node) bool {
	head := Unwrapped(e)
	if ast.IsStringLiteral(head) || ast.IsNoSubstitutionTemplateLiteral(head) ||
		ast.IsTemplateExpression(head) {
		return true
	}
	if ast.IsBinaryExpression(head) {
		bin := head.AsBinaryExpression()
		if bin.OperatorToken.Kind != ast.KindPlusToken {
			return false
		}
		return SpelledSequenceShape(bin.Left) && SpelledSequenceShape(bin.Right)
	}
	return false
}

// templateSequenceOf is `a${x}b${y}c` as a right-nested chain of the
// concatenation effect: the head's literal text, then per span the
// substituted expression's sequence reading followed by that span's
// literal text. An empty literal chunk contributes the empty tuple,
// which concatenates to nothing — kept rather than special-cased, so
// the chain's shape is one rule.
//
// A NUMBER-SORTED substitution — `${n}` — is not a sequence read, but
// it is not a decline either: ToString of a Number always answers a
// String (sec-tostring, the Number case: NaN/±0/finite/±Infinity every
// row is a String), so the span contributes a KNOWN-SORT, UNKNOWN-VALUE
// piece — the string root C* — rather than costing the whole template.
// This is strictly the sort-only claim: the concatenation family can
// carry no more of a ToString result than "some string" without a
// digit-shape transfer this grammar does not have, so the widening
// stops at the sort. templateSpanEffect below is what tries the
// number route once the sequence route has declined.
func templateSequenceOf(context *LoweringContext, head *ast.Node) (kernelbridge.LoopEffect, bool) {
	template := head.AsTemplateExpression()
	if template.TemplateSpans == nil {
		return kernelbridge.LoopEffect{}, false
	}
	parts := []kernelbridge.LoopEffect{{
		Kind: kernelbridge.LoopEffectConst,
		Set:  refinementsets.StringTuple(template.Head.AsTemplateHead().Text),
	}}
	for _, spanNode := range template.TemplateSpans.Nodes {
		span := spanNode.AsTemplateSpan()
		substituted, ok := templateSpanEffect(context, span.Expression)
		if !ok {
			return kernelbridge.LoopEffect{}, false
		}
		parts = append(parts, substituted)
		var text string
		switch {
		case ast.IsTemplateMiddle(span.Literal):
			text = span.Literal.AsTemplateMiddle().Text
		case ast.IsTemplateTail(span.Literal):
			text = span.Literal.AsTemplateTail().Text
		default:
			return kernelbridge.LoopEffect{}, false
		}
		parts = append(parts, kernelbridge.LoopEffect{
			Kind: kernelbridge.LoopEffectConst,
			Set:  refinementsets.StringTuple(text),
		})
	}
	// fold right so the chain nests the way the kernel's Concatenation
	// form does
	out := parts[len(parts)-1]
	for i := len(parts) - 2; i >= 0; i-- {
		out = concatOf(parts[i], out)
	}
	return out, true
}

// templateSpanEffect is one template substitution's contribution to the
// concatenation chain: the ordinary sequence reading where the
// substitution is itself string-sorted (a call there hoists, since the
// substitution sits INSIDE a sequence the template already committed
// to), and — only where that declines — the sort-only string-root piece
// for a NUMBER-sorted substitution, ToString's known sort with an
// unknown value. Anything neither route reads (unknown-sorted,
// object-valued, …) still declines the whole template, exactly as
// before this widening existed.
func templateSpanEffect(context *LoweringContext, span *ast.Node) (kernelbridge.LoopEffect, bool) {
	if substituted, ok := sequenceEffectOf(context, span, true /*inSequence*/); ok {
		return substituted, true
	}
	if _, ok := EffectOf(context, span); ok {
		return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectConst, Set: refinementsets.Strings}, true
	}
	return kernelbridge.LoopEffect{}, false
}
