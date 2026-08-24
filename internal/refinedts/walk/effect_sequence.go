// split from effect_expression.go — the sequence (string) half of the grammar

package walk

import (
	"math"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/jsnum"
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
		// s.repeat(n) over a NON-exact sequence-readable receiver rides the
		// kernel's drawn-from row unconditionally: the closure holds for
		// every receiver and every non-negative, finite count, so the
		// count itself is not read here (see LoopOpRepeatElem's doc). A
		// negative or infinite count throws rather than returns
		// (sec-string.prototype.repeat step 3/4), so this reader gates on
		// the argument being a plain, non-negative numeric literal, and
		// declines every other shape to the havoc floor rather than
		// assert past a throw.
		if repeated, ok := repeatElemEffect(context, call); ok {
			return repeated, true
		}
		// s.padStart(n, pad) / s.padEnd(n, pad) over a receiver the kernel
		// reads as a REPETITION shape: the union-alphabet claim
		// LoopOpPadUnion states. The fill text must be exactly known here
		// (padUnionEffect reads it as a sequence effect) since the kernel
		// needs its scalar SET as an operand, not merely its sort; the
		// target length n is not read at all -- the row states no
		// ceiling.
		if padded, ok := padUnionEffect(context, call); ok {
			return padded, true
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
	// `String(x)` called AS A FUNCTION (never `new String(x)`, which this
	// syntax cannot spell — a `new` node never reaches this reader) is
	// exactly ToString(x) (sec-string-constructor-string-value,
	// specifications/javascript/spec.html: "Let string be ? ToString(value)." with
	// NewTarget undefined). ToString is TOTAL — no throw completion — over
	// every operand sort this walk can name a slot for: a Number
	// (Number::toString), a String (identity, ToString step 1), and the
	// walk's own number sort ALSO carries every boolean (ToString steps
	// 5-6, "true"/"false" exactly). It throws only for a Symbol
	// (sec-tostring step 2) and, for an Object, runs the object's own
	// ToPrimitive chain (step 10) — neither of which this walk ever tracks
	// as a number- or string-sorted slot, so an operand this reader can
	// already lower through EffectOf or sequenceEffectOf is, by
	// construction, never one of those two refused shapes.
	//
	// The CLAIM stops at the sort, exactly as templateSpanEffect's own
	// number-substitution widening does (see that function's doc): no
	// digit-shape transfer exists on this grammar's wire to compute the
	// exact decimal text of a runtime number, so a number/boolean operand
	// widens to the string root C* (known sort, unknown value) rather than
	// an exact tuple. A STRING operand is the one case ToString is the
	// identity (step 1), so it rides through unchanged and exact.
	if stringOp, ok := stringConstructorCallOf(head); ok {
		if operandEffect, operandOk := sequenceEffectOf(context, stringOp, true /*inSequence*/); operandOk {
			// the operand is ALREADY a string — ToString(value) for a
			// String value returns value unchanged (sec-tostring step 1)
			return operandEffect, true
		}
		if _, operandOk := EffectOf(context, stringOp); operandOk {
			// the operand is number- or boolean-sorted — ToString's own
			// Number::toString / "true"/"false" rows apply, and every one
			// of them is a String; the sort is all this grammar can carry
			return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectConst, Set: refinementsets.Strings}, true
		}
		// `null`/`undefined`: ToString answers the exact literal word
		// (sec-tostring steps 3-4), read directly rather than through
		// either sort reader above, since neither EffectOf nor
		// sequenceEffectOf has a slot reading for the absent keywords
		if IsAbsentKeyword(stringOp) {
			word := "undefined"
			if Unwrapped(stringOp).Kind == ast.KindNullKeyword {
				word = "null"
			}
			return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectConst, Set: refinementsets.StringTuple(word)}, true
		}
		// a Symbol operand THROWS (step 2) and an Object operand runs its
		// own ToPrimitive/toString/valueOf chain (step 10) — neither is a
		// spec-total read, so the call keeps the decline it had before
		// this arm existed, exactly as an ungated operand shape always
		// did.
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
	return templateSpanOpaqueCallEffect(context, span)
}

// templateSpanOpaqueCallEffect is the LAST reading of a CALL
// substitution — `${getClipPathId(stackId, index)}` whose callee no
// stronger route served (the sequence route's HoistCallEffect needs a
// COMPLETE blob). The call hoists to a temp through the same statement
// door every statement-position call takes (HoistOpaqueCallTemp), its
// statements emitted ahead of the statement holding the template, and
// the span contributes the temp's read.
//
// THE GATES, each load-bearing:
//
//   - the SORT is read before anything allocates: only a call whose
//     resolved return type spells string or number rides. ToString is
//     total on both (sec-tostring, specifications/javascript/spec.html); an
//     unknown-sorted result could be a Symbol, whose ToString throws,
//     and keeps the decline the span always had.
//   - a RESOLVABLE callee's blob writes PRECISE values, and running it
//     ahead of the statement is a reordering — the same ordering gate
//     HoistCallEffect wears (hoistingIsOrderSafe) must prove no slot
//     the statement reads around the call is one the call writes.
//   - a BODY-LOCAL CLOSURE callee is refused outright: its served
//     statement (ClosureCallStatementOf) writes exact capture exits,
//     and with no resolvable declaration the ordering gate has no
//     write set to measure — so the reorder cannot be proved safe.
//
// An UNRESOLVABLE callee past those gates reaches only tiers whose
// writes are widenings — the imported-hook and receiver recognizers,
// the model tiers' joins, the opaque havoc's unknowns — and a widening
// emitted early can only weaken what an earlier span reads, never
// falsify it.
//
// A string-sorted temp contributes its own read (the callee's ret
// out-state verbatim — a serving tier's value, or the havoc floor's
// unknown). A number-sorted one contributes the sort-only string root,
// the same ToString widening the plain number span above takes.
func templateSpanOpaqueCallEffect(context *LoweringContext, span *ast.Node) (kernelbridge.LoopEffect, bool) {
	head := Unwrapped(span)
	if operand, isAwait := AwaitedOperandOf(head); isAwait {
		head = Unwrapped(operand)
	}
	if head == nil || !ast.IsCallExpression(head) {
		return kernelbridge.LoopEffect{}, false
	}
	sort, _ := ResolvedExpressionSort(hoistCheckerOf(context), Unwrapped(span))
	if sort != BindingKindString && sort != BindingKindNumber {
		return kernelbridge.LoopEffect{}, false
	}
	if _, isLocalClosure := localClosureOf(context, head.AsCallExpression().Expression); isLocalClosure {
		return kernelbridge.LoopEffect{}, false
	}
	if callee := summaryCalleeOf(context, head); callee != nil && !hoistingIsOrderSafe(context, head, callee) {
		return kernelbridge.LoopEffect{}, false
	}
	temp, hoisted := HoistOpaqueCallTemp(context, span)
	if !hoisted {
		return kernelbridge.LoopEffect{}, false
	}
	if temp < len(context.Sorts) && context.Sorts[temp] == BindingKindString {
		return varEffect(temp), true
	}
	return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectConst, Set: refinementsets.Strings}, true
}

// stringConstructorCallOf answers the ONE argument of a bare `String(x)`
// call — the global `String` identifier called directly, never through a
// property access (`foo.String(x)` is a different callee) and never as
// `new String(x)` (a NewExpression is a different AST kind this reader's
// caller never reaches). Exactly one argument: `String()` (zero
// arguments) and a rest/spread call are both left alone, since neither is
// the one-argument ToString row the spec clause states.
func stringConstructorCallOf(head *ast.Node) (*ast.Node, bool) {
	if !ast.IsCallExpression(head) {
		return nil, false
	}
	call := head.AsCallExpression()
	if call.QuestionDotToken != nil || !ast.IsIdentifier(call.Expression) || call.Expression.Text() != "String" {
		return nil, false
	}
	if call.Arguments == nil || len(call.Arguments.Nodes) != 1 {
		return nil, false
	}
	if ast.IsSpreadElement(call.Arguments.Nodes[0]) {
		return nil, false
	}
	return call.Arguments.Nodes[0], true
}

// repeatElemEffect reads `s.repeat(n)` over a sequence-readable receiver
// and answers the kernel's LoopOpRepeatElem row — the drawn-from closure
// with the length ceiling dropped entirely (see that op's own doc).
//
// The COUNT is read only far enough to establish that the call cannot
// throw: sec-string.prototype.repeat steps 3-4 raise a RangeError for a
// negative count or +Infinity, so this reader admits only a plain,
// non-negative integer LITERAL for n — the one shape provably clear of
// both throwing rows without evaluating anything. Any other count
// expression (a variable, an arithmetic expression, a negative or
// non-integer literal) declines to the havoc floor rather than assert
// a claim across an unproven throw.
func repeatElemEffect(context *LoweringContext, call *ast.CallExpression) (kernelbridge.LoopEffect, bool) {
	if call.QuestionDotToken != nil || !ast.IsPropertyAccessExpression(call.Expression) {
		return kernelbridge.LoopEffect{}, false
	}
	access := call.Expression.AsPropertyAccessExpression()
	if access.QuestionDotToken != nil || !ast.IsIdentifier(access.Name()) || access.Name().Text() != "repeat" {
		return kernelbridge.LoopEffect{}, false
	}
	if call.Arguments == nil || len(call.Arguments.Nodes) != 1 {
		return kernelbridge.LoopEffect{}, false
	}
	countNode := Unwrapped(call.Arguments.Nodes[0])
	if countNode == nil || !ast.IsNumericLiteral(countNode) {
		return kernelbridge.LoopEffect{}, false
	}
	count := float64(jsnum.FromString(countNode.AsNumericLiteral().Text))
	if count < 0 || count != math.Trunc(count) || math.IsInf(count, 0) {
		return kernelbridge.LoopEffect{}, false
	}
	receiver, receiverOk := sequenceEffectOf(context, access.Expression, true /*inSequence*/)
	if !receiverOk {
		return kernelbridge.LoopEffect{}, false
	}
	return kernelbridge.LoopEffect{
		Kind: kernelbridge.LoopEffectSeqUnary,
		Op:   kernelbridge.LoopOpRepeatElem,
		A:    &receiver,
	}, true
}

// padUnionEffect reads `s.padStart(n, pad)` / `s.padEnd(n, pad)` over a
// sequence-readable receiver and answers the kernel's LoopOpPadUnion
// row: the union-alphabet claim over the receiver and the fill text,
// with the floor kept and the ceiling left unstated (see that op's own
// doc).
//
// The FILL TEXT must be EXACTLY known here — the kernel needs its
// scalar SET as an operand (PadSet), not merely its sort, the same
// reason LoopOpReplaceUnionSafe's ReplSet is read syntactically rather
// than through a slot. A one-argument call omits pad, whose default is
// the single space " " (sec-stringpad step 1: "If fillString is
// undefined, set fillString to the String value consisting solely of
// the code unit 0x0020"). THE TARGET LENGTH n is not read at all: the
// row states no ceiling, so nothing about n changes the claim, and the
// gate the kernel itself decides (padUnionForm matches only a Repeat
// receiver shape) is the only premise this row needs.
func padUnionEffect(context *LoweringContext, call *ast.CallExpression) (kernelbridge.LoopEffect, bool) {
	if call.QuestionDotToken != nil || !ast.IsPropertyAccessExpression(call.Expression) {
		return kernelbridge.LoopEffect{}, false
	}
	access := call.Expression.AsPropertyAccessExpression()
	if access.QuestionDotToken != nil || !ast.IsIdentifier(access.Name()) {
		return kernelbridge.LoopEffect{}, false
	}
	method := access.Name().Text()
	if method != "padStart" && method != "padEnd" {
		return kernelbridge.LoopEffect{}, false
	}
	if call.Arguments == nil || len(call.Arguments.Nodes) < 1 || len(call.Arguments.Nodes) > 2 {
		return kernelbridge.LoopEffect{}, false
	}
	fill := " "
	if len(call.Arguments.Nodes) == 2 {
		exact, ok := exactSyntacticStringOf(call.Arguments.Nodes[1])
		if !ok {
			return kernelbridge.LoopEffect{}, false
		}
		fill = exact
	}
	receiver, receiverOk := sequenceEffectOf(context, access.Expression, true /*inSequence*/)
	if !receiverOk {
		return kernelbridge.LoopEffect{}, false
	}
	padSet := refinementsets.MakeRefinedSet(refinementsets.OneOf(refinementsets.CodepointsOf(fill)))
	return kernelbridge.LoopEffect{
		Kind:   kernelbridge.LoopEffectSeqUnary,
		Op:     kernelbridge.LoopOpPadUnion,
		A:      &receiver,
		PadSet: padSet,
	}, true
}
