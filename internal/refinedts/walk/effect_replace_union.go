// split from effect_expression.go — the replace/replaceAll union row

package walk

import (
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// replaceUnionEffect is `s.replace(pattern, replacement)` and
// `s.replaceAll(...)` lowered to the kernel's union-closure row, or
// declined.
//
// WHAT THE ROW CLAIMS. sec-string.prototype.replace returns the
// string-concatenation of `preceding`, `replacement` and `following`.
// The outer two are substrings of the receiver, so their scalars are
// the receiver's; the middle is GetSubstitution of the replacement
// template, and on the string-pattern path every branch of that
// operation yields either a span of the receiver ("$`", "$&", "$'") or
// text the template itself spells ("$$" -> "$", which a template
// holding "$$" contains; "$n" and "$<...>" fall through to the literal
// _ref_ because _captures_ is "a new empty List" and _namedCaptures_ is
// *undefined* here; and the default row copies one code unit). So every
// result scalar sits in the union of the two alphabets: the ALPHABET
// half of the claim survives every substitution branch, and needs no
// `$` gate.
//
// The LENGTH half does not, and that is why a `$` declines below. "$&"
// expands to the match and "$`"/"$'" to whole spans of the receiver, so
// the output of GetSubstitution is not bounded by the template's own
// length -- "aaa".replace("a", "$'") is longer than the receiver plus
// the template. Only a template with no `$` takes the default row
// every iteration, making its output the template itself.
//
// THE GATES, and why each is here rather than in the kernel:
//
//   - The REPLACEMENT must be exactly known, because its scalars are
//     the union's second half and the kernel cannot guess them. It rides
//     as a OneOf of the code points it spells -- the SET of scalars it
//     may contribute, not the ordered word, since the substitution's
//     position inside the result is not claimed.
//   - The replacement must be ASTRAL-SAFE. allBasicPlane rules out both
//     an astral scalar (whose two code units the code-point reading
//     would not match) and a lone surrogate.
//   - The PATTERN must be an exactly-known string, and astral-safe for
//     split's reason: a well-formed pattern's match begins and ends on a
//     scalar boundary because each of its code units pairs with the same
//     partner inside the receiver, while a pattern that IS a lone
//     surrogate can match half an astral pair and leave `preceding`
//     ending mid-pair. Both premises are about VALUES the kernel never
//     sees, so this side establishes them and the wire name carries them
//     -- exactly LoopOpSplitElemSafe's arrangement, and not
//     LoopOpSliceBmp's, whose premise is the receiver's own set.
//   - A FUNCTION replacement takes no part in the union row: its text is
//     the ToString of a Call (_functionalReplace_ true), so no set holds
//     it and the union has no second half. It earns the FUNCTIONAL-
//     REPLACER row instead (below the union row's gates in the body),
//     gated on its own write set rather than declining outright.
//
// A REGEX pattern keeps the closure -- its matches are still spans of
// the receiver -- and a regex carrying the `u` or `v` flag ALSO earns
// the boundary premise the string-pattern arm gets from code-unit
// pairing, so it is admitted. Under those flags sec-regexpbuiltinexec
// sets _fullUnicode_ true, and then _input_ is StringToCodePoints of
// the receiver, "each element of _input_ is considered to be a
// character": the matcher consumes whole code points, so a match can
// neither begin nor end mid-pair. The two indices agree -- _endIndex_
// is mapped back through GetStringIndex, and both the failure
// re-anchor and the empty-match bump go through AdvanceStringIndex,
// which under _unicode_ true returns _index_ plus the code point's
// [[CodeUnitCount]] (sec-advancestringindex). So `preceding` cannot
// end mid-pair and `following` cannot begin mid-pair; every matched
// span is receiver scalars, which is exactly the premise the
// well-formed string pattern supplies. A regex WITHOUT `u`/`v` keeps
// the old refusal: its matcher walks code units, so a match may split
// an astral pair.
//
// The `$` gate does the rest of the regex's work. Under a regex,
// _captures_ is no longer empty and _namedCaptures_ may be an object,
// so `$1` and `$<name>` read real captures rather than falling through
// to the literal text. Each capture is still a span of the receiver,
// so the ALPHABET half would survive; the LENGTH half would not, for
// the same reason "$&" breaks it. The template holding no `$` at all
// takes none of those branches, and that gate is already unconditional
// below, so nothing further is needed here.
//
// THE CEILING. `replace` rewrites the first match only, so the result is
// at most the receiver plus the replacement: the bump is the
// replacement's scalar count.
//
// `replaceAll` loops every match position, so the injected text
// multiplies by a count no receiver set bounds and the single-match
// budget bounds nothing. Sending bump 0 there would not mean "claim no
// ceiling" -- the wire's bump RAISES whatever ceiling the receiver
// states, so bump 0 claims the receiver's own, which a lengthening
// substitution breaks. What makes bump 0 sound instead is a premise:
// the replacement no longer than the pattern, so no match can lengthen
// the word. That also rules out an EMPTY pattern, which matches at
// every position (_advanceBy_ is max(1, _searchLength_)). A longer
// replacement under replaceAll declines.
//
// THE CEILING IS WHY A MULTI-MATCH REGEX STAYS OUT, even a `u` one
// whose boundaries are sound. A regex reaches this code by two roads
// and both refuse:
//
//   - `replace` with a `g` regex is NOT single-match. Step 3 of
//     sec-string.prototype.replace hands an object searchValue to
//     %Symbol.replace%, and that method reads `g` off the flags and
//     repeats RegExpExec until it returns *null*, setting _done_ only
//     when _global_ is false. So a g-flagged `replace` rewrites every
//     match, exactly as replaceAll does.
//   - `replaceAll` with a regex admits only the `g` form at all: step
//     3.a.ii of sec-string.prototype.replaceall throws a *TypeError*
//     when the flags do not contain "g".
//
// For a string pattern, multi-match rides bump 0 on the premise that
// the replacement is no longer than the pattern. A regex has no such
// premise: the matched span's length is not a property of the pattern
// text this side can measure, and a regex match may be ZERO-WIDTH, so
// the replacement is injected at position after position and the
// result outgrows any bound derived from the receiver. Bump 0 would
// claim the receiver's own ceiling, which is exactly the false claim.
// There is no ceiling-free spelling on the wire to fall back on -- the
// `$` analysis above established that the bump only ever RAISES a
// ceiling the receiver states. So every multi-match regex form
// declines, and the admitted regex row is the single-match one:
// non-global `replace`, whose result is the receiver with one span
// removed and the replacement added, taking the same bump the string
// pattern takes.
//
// WHAT STANDS BEHIND THE UNION ROW: THE SORT ROW. A gate above that
// fails no longer refuses outright -- refusing answers `top`, which
// admits the absent value and a thrown exit beside every word, and
// the spec supports strictly more than that for every shape this
// recognizer admits. LoopOpReplaceSortSafe claims exactly that
// remainder: the result is a STRING, never absent, never NaN, and the
// call never a thrown exit -- no alphabet, no length. STRONG BEATS
// WEAK, so the union row's gates run first and the sort row serves
// only their failures: the multi-match regex forms (`g`, and regex
// `replaceAll` with `g` proven), the `$`-substituting templates, the
// non-`u`/`v` regexes, the astral-unsafe literals, and the
// `replaceAll` whose replacement outgrows its pattern.
//
// A THIRD ROW stands behind a FUNCTION replacement specifically:
// LoopOpReplaceSortThrowSafe. The union and sort rows both need the
// replacement held as a set of scalars, which a function's return value
// never is; what the clause still guarantees on that shape is "a word on
// every completing run, or a thrown exit" -- sec-string.prototype.replace,
// sec-string.prototype.replaceall and sec-regexp.prototype-%symbol.replace%
// each read `_functionalReplace_ := IsCallable(_replaceValue_)` and, where
// true, compute the replacement as `? ToString(? Call(_replaceValue_,
// ...))` -- caller code this side cannot see the body of, so it may throw,
// but every run that DOES complete reaches the same three-piece String
// concatenation the sort row claims a word for. The row is gated on the
// replacer's own write set (ClosureEscapesTrackedWrite, effect_closure_
// writes.go): a SCALAR write set is coverable and is havocked alongside
// the row (ClosureWriteSlots); a write through a FLATTENED capture the
// census cannot spell -- a member write with no slot, or a mention of a
// flattened local -- is not coverable (closureMutatesFlattenedCapture,
// ir_assignment_closure_values.go, the same gate FunctionValuedDeclarationOf
// asks of a closure it hands over) and the refusal stands.
//
// One shape stays refused ahead of every row:
//
//   - `replaceAll` at a regex literal whose flags LACK `g`:
//     sec-string.prototype.replaceall step 2.a.iii throws a TypeError
//     there, so "never a thrown exit" is exactly false and no row
//     holds. The refusal answers `top`, which admits the throw.
func replaceUnionEffect(context *LoweringContext, call *ast.CallExpression) (kernelbridge.LoopEffect, bool) {
	if call.QuestionDotToken != nil || !ast.IsPropertyAccessExpression(call.Expression) {
		return kernelbridge.LoopEffect{}, false
	}
	access := call.Expression.AsPropertyAccessExpression()
	if access.QuestionDotToken != nil || !ast.IsIdentifier(access.Name()) {
		return kernelbridge.LoopEffect{}, false
	}
	method := access.Name().Text()
	if method != "replace" && method != "replaceAll" {
		return kernelbridge.LoopEffect{}, false
	}
	if call.Arguments == nil || len(call.Arguments.Nodes) != 2 {
		return kernelbridge.LoopEffect{}, false
	}
	// the pattern rides one of two arms: an exactly-known string, or a
	// regex literal. Which ROW it earns is decided below -- the union
	// row needs the scalar-boundary premise (a well-formed string
	// pattern, or `u`/`v` on the literal) and a single match; the sort
	// row needs only the shape itself, since either arm keeps every
	// spec path free of throws (see the sort-row section above). A
	// pattern that is neither arm -- a variable, a constructed RegExp --
	// refuses: this side cannot read its value, so neither row's
	// premise is established.
	patternIsRegex := false
	regexFlags := ""
	pattern, patternOk := exactSyntacticStringOf(call.Arguments.Nodes[0])
	if !patternOk {
		regex := Unwrapped(call.Arguments.Nodes[0])
		if regex == nil || !ast.IsRegularExpressionLiteral(regex) {
			return kernelbridge.LoopEffect{}, false
		}
		// the literal's text is /pattern/flags -- read the flag segment
		text := regex.AsRegularExpressionLiteral().Text
		lastSlash := strings.LastIndex(text, "/")
		if lastSlash <= 0 {
			return kernelbridge.LoopEffect{}, false
		}
		regexFlags = text[lastSlash+1:]
		patternIsRegex = true
	}
	// `replaceAll` at a regex whose flags lack `g` THROWS -- step
	// 2.a.iii of sec-string.prototype.replaceall is a TypeError -- so
	// "never a thrown exit" is false and NO row holds. The refusal
	// answers `top`, which admits the throw.
	if patternIsRegex && method == "replaceAll" && !strings.Contains(regexFlags, "g") {
		return kernelbridge.LoopEffect{}, false
	}
	replacement, replacementOk := exactSyntacticStringOf(call.Arguments.Nodes[1])
	if !replacementOk {
		// A FUNCTION replacement earns the FUNCTIONAL-REPLACER row,
		// gated on its write set: the replacer is caller code, invoked
		// mid-operation (? Call, then ? ToString of its result), so it
		// can throw and it can WRITE. `LoopOpReplaceSortThrowSafe`
		// carries exactly the value claim that survives that -- a word
		// on every completing run, or a thrown exit -- and the write
		// half is covered by havocking the replacer's own write set
		// (ClosureEscapesTrackedWrite names the boundary shared with
		// every other escaping-closure call site: the closure runs at a
		// time no statement here places, so every name it writes must
		// be havocked before the claim is trusted).
		//
		// Any OTHER unreadable replacement -- a variable, a computed
		// expression that is not a function literal -- stays refused:
		// this side has no write-set reading for it at all, so the
		// gated claim has no premise to earn.
		replacer := Unwrapped(call.Arguments.Nodes[1])
		if replacer == nil || !ast.IsFunctionLike(replacer) || replacer.Body() == nil {
			return kernelbridge.LoopEffect{}, false
		}
		// a statement stream must exist to hoist the havoc into -- the
		// same first gate HoistCallEffect asks, since an append to
		// context.Hoisted with no stream to flush it is silently lost
		if !context.CanHoist {
			return kernelbridge.LoopEffect{}, false
		}
		// THE UNCOVERABLE SHAPE, refused ahead of the row exactly as
		// FunctionValuedDeclarationOf refuses it: a write through a
		// FLATTENED capture the assigned-name census cannot spell --
		// a member/element write with no slot of its own, or a mention
		// of a flattened local anywhere in the body (closureMutatesFlat
		// tenedCapture, ir_assignment_closure_values.go). ClosureWriteSlots
		// would UNDER-count that move, which is the unsound direction, so
		// the claim earns no row there and the refusal stands.
		if closureMutatesFlattenedCapture(context, replacer.Body()) {
			return kernelbridge.LoopEffect{}, false
		}
		receiver, receiverOk := sequenceEffectOf(context, access.Expression, true /*inSequence*/)
		if !receiverOk {
			return kernelbridge.LoopEffect{}, false
		}
		// THE COVERABLE SHAPE: every SCALAR name the replacer writes is
		// havocked alongside the row -- ClosureEscapesTrackedWrite names
		// the boundary this shares with every other escaping-closure call
		// site, and ClosureWriteSlots is the exact set it resolves to.
		// Both read closureAssignedNames' contract: the node handed in
		// must be the closure ITSELF (`replacer`), not its body alone --
		// closureAssignedNames only starts collecting once its walk finds
		// a function-like node to descend into, so a bare block sees no
		// writes at its own top level.
		if ClosureEscapesTrackedWrite(context, replacer) {
			context.Hoisted = append(context.Hoisted,
				havocAssignments(ClosureWriteSlots(context, replacer))...)
		}
		return kernelbridge.LoopEffect{
			Kind: kernelbridge.LoopEffectSeqUnary,
			Op:   kernelbridge.LoopOpReplaceSortThrowSafe,
			A:    &receiver,
		}, true
	}
	receiver, receiverOk := sequenceEffectOf(context, access.Expression, true /*inSequence*/)
	if !receiverOk {
		return kernelbridge.LoopEffect{}, false
	}
	// THE UNION ROW'S GATES, in order; the first failure falls through
	// to the sort row rather than refusing. Strong beats weak, so the
	// union row is tried first wherever its premises hold.
	strongOk := true
	patternPoints := 0
	if patternOk {
		if allBasicPlane(pattern) {
			patternPoints = len(refinementsets.CodepointsOf(pattern))
		} else {
			// an astral-unsafe pattern can match half a surrogate pair
			// and leave `preceding` ending mid-pair -- no union claim
			strongOk = false
		}
	} else {
		// the boundary premise: only `u`/`v` walks code points
		if !strings.Contains(regexFlags, "u") && !strings.Contains(regexFlags, "v") {
			strongOk = false
		}
		// a global regex rewrites every match, and no receiver-derived
		// ceiling survives a match whose length this side cannot read
		if strings.Contains(regexFlags, "g") || method == "replaceAll" {
			strongOk = false
		}
	}
	if !allBasicPlane(replacement) {
		strongOk = false
	}
	points := refinementsets.CodepointsOf(replacement)
	// THE LENGTH BUDGET IS NOT THE TEMPLATE'S LENGTH WHERE `$` IS
	// PRESENT. The ALPHABET claim survives every GetSubstitution branch
	// (see above), but the LENGTH claim does not: "$&" expands to the
	// match and "$`"/"$'" to whole spans of the receiver, so
	// "aaa".replace("a", "$'") is longer than the receiver plus the
	// template. A template holding no `$` at all takes none of those
	// branches -- every iteration falls to the default row, which copies
	// one code unit -- so its output IS the template and its length IS
	// the template's. That is the only case a finite bump is sound for.
	//
	// A `$` anywhere in the template therefore DECLINES THE UNION ROW,
	// not just the ceiling. There is no "ceiling-free" bump to fall back
	// on: the wire's bump raises whatever ceiling the receiver states,
	// so sending 0 would claim the receiver's own ceiling -- exactly the
	// claim "aaa".replace("a", "$'") breaks. What the shape earns
	// instead is the sort row below.
	if strings.Contains(replacement, "$") {
		strongOk = false
	}
	bump := len(points)
	if method == "replaceAll" && !patternIsRegex {
		// every match may inject, so the single-match budget is no bound
		// at all. What keeps the receiver's own ceiling sound is a
		// substitution that cannot LENGTHEN: with the replacement no
		// longer than the pattern, no match grows the word, so the
		// ceiling rides unraised and the bump is zero. Note this also
		// rules out an EMPTY pattern, which matches at every position
		// (_advanceBy_ is max(1, 0)) and would otherwise inject
		// unboundedly
		if bump > patternPoints {
			strongOk = false
		}
		bump = 0
	}
	if strongOk {
		return kernelbridge.LoopEffect{
			Kind:    kernelbridge.LoopEffectSeqUnary,
			Op:      kernelbridge.LoopOpReplaceUnionSafe,
			A:       &receiver,
			ReplSet: refinementsets.MakeRefinedSet(refinementsets.OneOf(distinctScalars(points))),
			Bump:    bump,
		}, true
	}
	// THE SORT ROW: every union-row failure above lands here, and the
	// shape gates already passed are exactly its premise -- receiver
	// stated a string, pattern an exactly-spelled string or a regex
	// literal, replacement an exactly-spelled string, and `g` proven on
	// a regex `replaceAll`. The kernel answers the root set flagless: a
	// word, never absent, never NaN, never a thrown exit.
	return kernelbridge.LoopEffect{
		Kind: kernelbridge.LoopEffectSeqUnary,
		Op:   kernelbridge.LoopOpReplaceSortSafe,
		A:    &receiver,
	}, true
}

// distinctScalars is a scalar list with duplicates dropped, order kept.
// The replacement's set is a OneOf of the scalars it may contribute, and
// a repeated character contributes nothing a single mention does not.
func distinctScalars(points []float64) []float64 {
	seen := make(map[float64]bool, len(points))
	var out []float64
	for _, p := range points {
		if seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}
