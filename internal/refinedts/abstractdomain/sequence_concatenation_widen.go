// The sequence-concatenation WIDENING bound: past a named nesting
// depth, a set built by repeated string derivation (a loop body's
// `.replace()`/`+` accumulating across iterations, or a fixpoint join
// re-deriving the same candidate round after round) widens to the
// coarser sound form instead of growing further.
//
// THIS IS A PRECISION POLICY, not a crash guard. It once was one: the
// ReduceCSSCalc.ts reproducer (tmp/recharts-src/src/util/
// ReduceCSSCalc.ts's evaluateExpression, minimized at
// /private/tmp/killer_slice10.ts) hung the kernel's seqSubset ask on a
// growing RHS star alphabet, because refined-lean's mkUnion
// (refined_sets/automata.lean) built a fresh Union layer on every
// nullable-left derivative step (`if A.nullable then mkUnion (mkConcat
// (A.deriv v) B) (B.deriv v) else ...`) with no canonicalization
// collapsing a repeated branch, so a moderately-nested Concatenation/
// Star tree the walk kept handing back grew without bound. mkUnion
// now collapses a syntactically-repeated operand to one copy
// (`A.structEq B`, refinements/grammar.lean) before building another
// Union node, so the SAME shape this bound was written against
// terminates in the kernel today -- the bound stays because an
// unwidened join still keeps building a bigger, exact Concatenation/
// Union/Star term every round, and that term's SIZE (not its
// termination) is what this widens away: a large-but-terminating
// candidate is still a costly one to encode, cache-key, and answer
// questions about, and past ordinary program shapes there is no
// precision left to buy.
//
// THE BOUND. 64 is chosen against the g-target corpus's own shapes,
// which must derive EXACTLY, never widened:
//
//   - text_label.ts: z.string().min(3).max(8) over a 3-character
//     literal slice -- a shallow, few-layer Concatenation.
//   - text_timestamp.ts: a template literal interpolating one bounded
//     `padded` value into a 17-character literal suffix
//     ("-01-01T00:00:00Z") -- StringTuple builds one Concatenation
//     layer per character (codepoint_sets.go), so this alone reaches
//     nesting depth 17, plus one more layer for the interpolation
//     itself: 18.
//
// 64 sits far above both (text_timestamp's 18 has more than 3x
// headroom) while still catching the reproducer's growth well before
// the join builds a term expensive to carry further -- the trace
// shows the pathological ask's operands already past this depth by
// the third round of growth.
package abstractdomain

import "github.com/microsoft/typescript-go/internal/refinedts/refinementsets"

const sequenceConcatenationWidenBound = 64

// widenPastConcatenationBound answers the string ground for a join
// whose either operand already carries more sequence-form nodes than
// the bound above, and (false) for every other pair — the join then
// proceeds normally.
//
// JoinKnown calls this FIRST, after its identity check and before any
// arm that constructs. Order is the whole point: an arm that builds a
// term out of these operands — the repetition/list join, the union
// builders, the absorption arms — grows exactly what the bound exists
// to stop growing, so a late check lets the growth happen and never
// fires. The identity case is exempt because it builds nothing: it
// hands back the operand it was given.
//
// Either operand alone is enough. The bound asks whether the join
// would carry a term this large forward, and a too-deep side makes
// that true whichever side it is; both orders answer the same ground,
// so the widening does not depend on argument order.
func widenPastConcatenationBound(a, b AbstractValue, grade TrustLevel) (AbstractValue, bool) {
	for _, side := range [2]AbstractValue{a, b} {
		set, isSet := SetOfKnown(side)
		if !isSet || !refinementsets.StatesSequence(set) {
			continue
		}
		if refinementsets.SequenceNestingDepth(set) > sequenceConcatenationWidenBound {
			return KnownSet(refinementsets.Strings, nil, grade, SetKindTagNone), true
		}
	}
	return AbstractValue{}, false
}
