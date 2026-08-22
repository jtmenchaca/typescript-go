// The sequence-concatenation WIDENING bound: past a named nesting
// depth, a set built by repeated string derivation (a loop body's
// `.replace()`/`+` accumulating across iterations, or a fixpoint join
// re-deriving the same candidate round after round) widens to the
// coarser sound form instead of growing further.
//
// MEASURED, the crash this guards against: tmp/recharts-src/src/util/
// ReduceCSSCalc.ts's evaluateExpression feeds calculateParentheses's
// while-loop output through a SECOND calculateArithmetic call, whose
// own while-loop reassigns its string across repeated regex-driven
// `.replace()` calls. The minimized reproducer
// (/private/tmp/killer_slice10.ts) shows the kernel's seqSubset ask
// growing by two (integer, union) pairs in its RHS star's alphabet on
// each successive ask (2, then 4, then 6) before the third ask never
// returns -- the kernel's own derivative-based deciders (refined-lean/
// refined_sets/automata.lean's Concatenation case: `if A.nullable then
// mkUnion (mkConcat (A.deriv v) B) (B.deriv v) else ...`) grow the
// term on every nullable-left derivative step with no canonicalization
// collapsing the repeated substructure, so a moderately-nested
// Concatenation/Star tree the walk keeps handing back turns a bounded
// question into an unbounded one on the kernel side.
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
// the kernel spends unbounded time on it -- the trace shows the
// pathological ask's operands already past this depth by the third
// round of growth.
package abstractdomain

const sequenceConcatenationWidenBound = 64
