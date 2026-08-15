// from control_flow/effect_expression.ts
//
// The EXPRESSION half of the effect grammar, shared by both
// lowerings — the loop solver (loop_effect.ts) and the flow IR
// (lowering_to_kernel_ir.ts): literals through parens/casts, tracked
// reads, negation and unary plus, the five arithmetic operators, the
// Math reads (five unary, min/max), and the ternary as a join (its
// condition must be write-free — both arms are admitted, sound).
// `ReadPlace` answers a spelled tracked (or known) name; `Opaque`
// says what an unmodeled shape becomes — the solver path answers
// unknown for write-free shapes, the IR path declines. (0-value,
// false) means the reading declines.

package walk

import (
	"math"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/jsnum"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

var binOps = map[ast.Kind]kernelbridge.LoopEffectOp{
	ast.KindPlusToken:     kernelbridge.LoopOpAdd,
	ast.KindMinusToken:    kernelbridge.LoopOpSub,
	ast.KindAsteriskToken: kernelbridge.LoopOpMul,
	ast.KindSlashToken:    kernelbridge.LoopOpDiv,
	ast.KindPercentToken:  kernelbridge.LoopOpRem,
	// the bitwise and shift operators: the kernel reads these six op
	// names into LoopOp2 and evaluates them through transferBitwise,
	// which is exact on singleton operands, answers [0, mask] when one
	// side of an AND is a nonnegative singleton, and claims nothing
	// otherwise. No extra operand gate is needed: ToInt32 is defined
	// for every double including the infinities, so a number-sorted
	// operand of any magnitude is a legal input.
	ast.KindBarToken:                               kernelbridge.LoopOpBitOr,
	ast.KindAmpersandToken:                         kernelbridge.LoopOpBitAnd,
	ast.KindCaretToken:                             kernelbridge.LoopOpBitXor,
	ast.KindLessThanLessThanToken:                  kernelbridge.LoopOpShl,
	ast.KindGreaterThanGreaterThanToken:            kernelbridge.LoopOpSar,
	ast.KindGreaterThanGreaterThanGreaterThanToken: kernelbridge.LoopOpShr,
	// `**`: the kernel reads this name into LoopOp2 and evaluates it
	// through transferPow, the same pinned Number::exponentiate rows
	// the transfer wire answers with. Total on every pair of doubles —
	// the spec's own NaN rows are cells transferPow carries — so the
	// operand gate is the number sort alone, like the bitwise forms.
	ast.KindAsteriskAsteriskToken: kernelbridge.LoopOpPow,
}

var mathOps = map[string]kernelbridge.LoopEffectOp{
	"floor": kernelbridge.LoopOpFloor,
	"ceil":  kernelbridge.LoopOpCeil,
	"round": kernelbridge.LoopOpRound,
	"trunc": kernelbridge.LoopOpTrunc,
	"abs":   kernelbridge.LoopOpAbs,
	// the bounded-image four: the kernel answers the interval each
	// clause names, which holds for EVERY conforming engine and needs
	// nothing of the operand. sqrt lands at or above +0; sin and cos
	// inside [-1, 1]; atan inside [-2, 2].
	"sqrt": kernelbridge.LoopOpSqrt,
	"sin":  kernelbridge.LoopOpSin,
	"cos":  kernelbridge.LoopOpCos,
	"atan": kernelbridge.LoopOpAtan,
	// tan's interval is the whole line, so the row bounds nothing. It is
	// here anyway because the alternative is not "no claim" but the
	// kernel's `top`, which admits the absent value and a thrown exit
	// beside every number. The row says the slot holds a NUMBER, possibly
	// NaN, and never either of those (sec-math.tan).
	"tan": kernelbridge.LoopOpTan,
	//
	// EVERY OTHER Math unary — asin, acos, sign, random, fround, cbrt,
	// asinh, atanh, acosh, sinh, cosh, tanh, exp, expm1, log, log2,
	// log10, log1p, hypot, clz32 — stays OFF this table. The loop-effect
	// wire's unary vocabulary (kernelbridge.LoopEffectOp / the kernel's
	// `LoopOp1`, set_functions/loop_solve.lean lines 73-75) is a CLOSED
	// eleven-constructor enum: neg, floor, ceil, round, trunc, abs, sqrt,
	// sin, cos, atan, tan. None of the names above decode on that wire —
	// `loopOp1Of` (boundary/exports.lean) falls through to `none` for
	// every one of them, and there is no generic "claim this interval"
	// constructor to carry a caller-supplied bound; every row is a
	// hand-proved, hand-named Lean constructor. Adding one is a kernel
	// change (a new `LoopOp1` case, a soundness theorem, `lake build`),
	// which this unit does not do.
	//
	// These calls are NOT unsupported everywhere: the general evaluator
	// (evaluateExpression, reached by `unaryTransfer`/`UnaryMathImage` in
	// math_unary_transfer.go and the corner-case handling in
	// math_transfer.go) already carries sound bounded-image or exact
	// rows for every one of them over the separate `TransferQuestion`
	// wire — random -> [0,1) (sec-math.random), sign -> {-1,0,1} (a
	// disjointness read, transferSign), clz32 -> [0,32] integers
	// (transferClz32, exact host computation per sec-math.clz32), and
	// the rest through the kernel's `transferOfOp1`/`transferOfOp2`
	// singleton-and-window rows. That is a DIFFERENT grammar (arbitrary
	// evaluated expressions) from this file's (loop-carried bindings and
	// flow-IR effects), and a call inside a loop body or an assignment
	// RHS this file lowers still floors here — the two wires do not
	// share operands or answers, and this file cannot hand its call off
	// to the other evaluator's kernel question mid-lowering.
}

// mathBinaryOps: the two-argument Math reads the effect wire carries.
// `Math.pow(a, b)` is `a ** b` — the same kernel row — and
// `Math.atan2(y, x)` rides its own interval. Both take EXACTLY two
// arguments; min and max are variadic and fold separately below.
//
// `Math.imul` stays OFF this table for the same reason clz32 stays off
// mathOps: the loop-effect wire's binary vocabulary (`LoopOp2`,
// set_functions/loop_solve.lean lines 89-92) is the closed set add, sub,
// mul, div, rem, min, max, bitOr, bitAnd, bitXor, shl, sar, shr, pow,
// atan2 — no `imul` constructor exists, and the bitwise six are ToInt32
// reinterpretations, not the ToInt32(ToInt32(a)*ToInt32(b) mod 2^32)
// rule imul needs (sec-math.imul). The general evaluator's
// `transferImul` (math_transfer.go) already answers this off the
// separate transfer wire by composing three existing TransferQuestion
// calls (toInt32, mul, toInt32) — a composition this file's Op enum has
// no slot to carry, since a LoopEffect node names exactly one kernel op.
var mathBinaryOps = map[string]kernelbridge.LoopEffectOp{
	"pow":   kernelbridge.LoopOpPow,
	"atan2": kernelbridge.LoopOpAtan2,
}

// mathConstants: the eight Math VALUE properties (sec-value-properties-
// of-the-math-object). Each clause reads "The Number value for X ...
// which is approximately N" — "the Number value for" is the spec's
// defined term (sec-math-object's own note, pointing at
// sec-ecmascript-language-types-number-type) for THE SPECIFIC Number
// closest to the true mathematical constant. That is a fixed, single
// value every conforming engine holds — unlike `sin`/`exp`/etc., whose
// clauses say "an IMPLEMENTATION-APPROXIMATED Number value" and so may
// differ engine to engine. Go's math package constants round to the
// same nearest-float64 the spec's decimal literal names, so each reads
// here as the exact singleton, riding LoopEffectConst — no operator, no
// kernel op name, nothing the closed LoopOp1/LoopOp2 vocabulary needs
// to recognize.
var mathConstants = map[string]float64{
	"E":       math.E,         // sec-math.e
	"LN10":    math.Ln10,      // sec-math.ln10
	"LN2":     math.Ln2,       // sec-math.ln2
	"LOG10E":  math.Log10E,    // sec-math.log10e
	"LOG2E":   math.Log2E,     // sec-math.log2e
	"PI":      math.Pi,        // sec-math.pi
	"SQRT1_2": math.Sqrt(0.5), // sec-math.sqrt1_2 -- "the square root of ½"
	"SQRT2":   math.Sqrt2,     // sec-math.sqrt2
}

// booleanBinaryTokens: every binary operator whose VALUE is exactly
// true or false. The effect claims the two-value set {0,1} — exact as
// a set, reading neither operand — so it is admissible only when
// evaluating the operands cannot move state (writeAndCallFree).
var booleanBinaryTokens = map[ast.Kind]struct{}{
	ast.KindLessThanToken:                {},
	ast.KindGreaterThanToken:             {},
	ast.KindLessThanEqualsToken:          {},
	ast.KindGreaterThanEqualsToken:       {},
	ast.KindEqualsEqualsToken:            {},
	ast.KindEqualsEqualsEqualsToken:      {},
	ast.KindExclamationEqualsToken:       {},
	ast.KindExclamationEqualsEqualsToken: {},
	ast.KindInstanceOfKeyword:            {},
	ast.KindInKeyword:                    {},
}

// logicalTokens: the short-circuit operators. `a && b` evaluates to a
// on a falsy a and to b otherwise, so the JOIN of both operands' sets
// admits every value the expression can take — a superset on the arm
// the short-circuit picked, never an exclusion. Same for || and ??.
var logicalTokens = map[ast.Kind]struct{}{
	ast.KindAmpersandAmpersandToken: {},
	ast.KindBarBarToken:             {},
	ast.KindQuestionQuestionToken:   {},
}

// EffectReader mirrors the destructured `reader` parameter of
// lowerEffectExpression in the TS source.
//
// ReadNode is the Go addition: a DEEP path read (`p.a.b`) that
// SpelledNameOf cannot spell, which nested-record flattening makes an
// ordinary slot read. Tried after ReadPlace declines and before Opaque;
// nil leaves the reading exactly as ReadPlace left it.
//
// HoldsPlace answers whether a spelled name has a SLOT at all, with no
// sort filter. ReadPlace cannot serve that question: it answers only
// where the slot's sort admits the read it is building — EffectOf's
// number gate turns a held string-sorted name into "not readable" — and
// the closure census needs to know which names have state to invalidate,
// which every held slot does whatever its sort. A reader that leaves it
// nil has not said what it holds, and the census reads that as "assume
// everything" (closureWritesTracked's own rule).
type EffectReader struct {
	ReadPlace  func(spelled string) (kernelbridge.LoopEffect, bool)
	ReadNode   func(e *ast.Node) (kernelbridge.LoopEffect, bool)
	Opaque     func(e *ast.Node) (kernelbridge.LoopEffect, bool)
	HoldsPlace func(spelled string) bool
}

// LowerEffectExpression is lowerEffectExpression in the TS source.
func LowerEffectExpression(e *ast.Node, reader EffectReader) (kernelbridge.LoopEffect, bool) {
	if ast.IsParenthesizedExpression(e) || ast.IsAsExpression(e) || ast.IsNonNullExpression(e) ||
		ast.IsTypeAssertion(e) || ast.IsSatisfiesExpression(e) {
		var inner *ast.Node
		switch {
		case ast.IsParenthesizedExpression(e):
			inner = e.AsParenthesizedExpression().Expression
		case ast.IsAsExpression(e):
			inner = e.AsAsExpression().Expression
		case ast.IsNonNullExpression(e):
			inner = e.AsNonNullExpression().Expression
		case ast.IsTypeAssertion(e):
			// `<T>e` erases exactly as `as` does — no runtime check
			inner = e.AsTypeAssertion().Expression
		case ast.IsSatisfiesExpression(e):
			inner = e.AsSatisfiesExpression().Expression
		}
		return LowerEffectExpression(inner, reader)
	}
	if ast.IsNumericLiteral(e) {
		return kernelbridge.LoopEffect{
			Kind: kernelbridge.LoopEffectConst,
			Set:  refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{float64(jsnum.FromString(e.AsNumericLiteral().Text))})),
		}, true
	}
	if e.Kind == ast.KindTrueKeyword {
		return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectConst, Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{1}))}, true
	}
	if e.Kind == ast.KindFalseKeyword {
		return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectConst, Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{0}))}, true
	}
	// a Math VALUE property read -- `Math.PI`, `Math.E`, and the other
	// six -- tried BEFORE the spelled-name and deep-path arms below,
	// since `Math` is a global identifier no reader tracks as a slot and
	// would otherwise fall through to Opaque. Checked ahead of the CALL
	// branch further down because these are property reads, never calls.
	if ast.IsPropertyAccessExpression(e) {
		access := e.AsPropertyAccessExpression()
		if access.QuestionDotToken == nil && ast.IsIdentifier(access.Expression) &&
			access.Expression.Text() == "Math" && ast.IsIdentifier(access.Name()) {
			if value, ok := mathConstants[access.Name().Text()]; ok {
				return kernelbridge.LoopEffect{
					Kind: kernelbridge.LoopEffectConst,
					Set:  refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{value})),
				}, true
			}
		}
	}
	if spelled, ok := SpelledNameOf(e); ok {
		if held, ok := reader.ReadPlace(spelled); ok {
			return held, true
		}
		if reader.ReadNode != nil {
			if held, ok := reader.ReadNode(e); ok {
				return held, true
			}
		}
		return reader.Opaque(e)
	}
	// a DEEP path (`p.a.b`) has no one-step spelling; a flattened nested
	// record gives it a slot all the same
	if ast.IsPropertyAccessExpression(e) && reader.ReadNode != nil {
		if held, ok := reader.ReadNode(e); ok {
			return held, true
		}
	}
	if ast.IsPrefixUnaryExpression(e) {
		unary := e.AsPrefixUnaryExpression()
		if unary.Operator == ast.KindMinusToken {
			if ast.IsNumericLiteral(unary.Operand) {
				return kernelbridge.LoopEffect{
					Kind: kernelbridge.LoopEffectConst,
					Set:  refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{-float64(jsnum.FromString(unary.Operand.AsNumericLiteral().Text))})),
				}, true
			}
			a, ok := LowerEffectExpression(unary.Operand, reader)
			if !ok {
				return kernelbridge.LoopEffect{}, false
			}
			return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectUnary, Op: kernelbridge.LoopOpNeg, A: &a}, true
		}
		if unary.Operator == ast.KindPlusToken {
			return LowerEffectExpression(unary.Operand, reader)
		}
		// `!x` always produces exactly true or false — the two-value set,
		// under the same moves-nothing gate the comparisons wear
		if unary.Operator == ast.KindExclamationToken && writeAndCallFree(unary.Operand) {
			return booleanPairEffect(), true
		}
		return reader.Opaque(e)
	}
	if ast.IsBinaryExpression(e) {
		bin := e.AsBinaryExpression()
		// a COMPARISON (and instanceof/in) always produces exactly true or
		// false: the two-value set is the exact answer, and no operand is
		// read — admitted only where evaluating the operands moves nothing.
		// A gate failure falls to Opaque, exactly what the operator did
		// before this arm existed.
		if _, isBoolean := booleanBinaryTokens[bin.OperatorToken.Kind]; isBoolean && writeAndCallFree(e) {
			return booleanPairEffect(), true
		}
		// a SHORT-CIRCUIT operator's value is one of its operands, so the
		// join of both admits every run. A side that does not lower falls
		// to Opaque — again the operator's old path.
		if _, isLogical := logicalTokens[bin.OperatorToken.Kind]; isLogical {
			a, aOk := LowerEffectExpression(bin.Left, reader)
			b, bOk := LowerEffectExpression(bin.Right, reader)
			if aOk && bOk {
				return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectJoin, A: &a, B: &b}, true
			}
			if held, ok := reader.Opaque(e); ok {
				return held, true
			}
			// a side with no scalar spelling — `radius[i] ?? 0` where the
			// index read finds no element slot — still produces ONE of two
			// operands, and evaluating both moved nothing. Unknown is exactly
			// what a slot can hold of that: the value is unconstrained and no
			// state changed, so the read costs precision here and nothing
			// anywhere else. (Gated on the whole expression moving nothing,
			// the same gate the comparison and literal arms wear; an operand
			// that RUNS something keeps the decline, since its effects need a
			// statement this grammar cannot emit.) An arm holding a CLOSURE
			// that writes a tracked name keeps the decline too — the value
			// escapes with the closure inside it, and no statement here
			// places the later run (closureWritesTracked's own rule).
			if inertValue(e) && !closureWritesTracked(e, reader) {
				return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectUnknown}, true
			}
			return kernelbridge.LoopEffect{}, false
		}
		op, ok := binOps[bin.OperatorToken.Kind]
		if !ok {
			return reader.Opaque(e)
		}
		a, aOk := LowerEffectExpression(bin.Left, reader)
		b, bOk := LowerEffectExpression(bin.Right, reader)
		if !aOk || !bOk {
			return kernelbridge.LoopEffect{}, false
		}
		return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectBinary, Op: op, A: &a, B: &b}, true
	}
	if ast.IsConditionalExpression(e) {
		cond := e.AsConditionalExpression()
		if ContainsWrite(cond.Condition) {
			return kernelbridge.LoopEffect{}, false
		}
		a, aOk := LowerEffectExpression(cond.WhenTrue, reader)
		b, bOk := LowerEffectExpression(cond.WhenFalse, reader)
		if aOk && bOk {
			return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectJoin, A: &a, B: &b}, true
		}
		// an ARM with no scalar spelling: the value is one of the two arms
		// and evaluating the whole thing moved nothing, so unknown is what
		// a slot holds of it — the same reading the short-circuit operators
		// take, which is what a ternary is a spelling of. An arm that RUNS
		// something keeps the decline; its effects need a statement. So
		// does an arm handing over a CLOSURE that writes a tracked name —
		// the later run has no statement position here.
		if inertValue(e) && !closureWritesTracked(e, reader) {
			return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectUnknown}, true
		}
		return kernelbridge.LoopEffect{}, false
	}
	// an OBJECT LITERAL, ARRAY LITERAL, or TEMPLATE whose evaluation
	// moves nothing: the value has no scalar spelling — unknown IS what
	// a slot can hold of it — and building it changed no state, so the
	// unknown claim costs the read and nothing else. A literal whose
	// parts run code keeps the old path (its effects need a statement).
	//
	// A literal carrying FUNCTION VALUES — nest's
	// `{ [APP_GUARD]: guard => this.config.addGlobalGuard(guard) }` —
	// builds them without running them, so it passes the gate above. The
	// second gate is the one those closures need: the literal is the
	// caller's now, and a closure that writes a name this body tracks may
	// run at any later time. Where it writes nothing tracked, the value
	// is inert here and the read stands; where it does, the decline stays
	// and the statement takes the floor, which havocs those names.
	if (ast.IsObjectLiteralExpression(e) || ast.IsArrayLiteralExpression(e) ||
		ast.IsTemplateExpression(e)) && inertValue(e) &&
		!closureWritesTracked(e, reader) {
		return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectUnknown}, true
	}
	if ast.IsCallExpression(e) {
		call := e.AsCallExpression()
		// a PURE BUILTIN read — `Reflect.getMetadata(...)`, `Object.keys(x)`,
		// `Array.isArray(x)`: the callee reads without moving anything, so
		// with write-and-call-free arguments the whole expression moves
		// nothing and its value spells as its contract promises — the
		// two-value set for the predicates, unknown for the rest. The list
		// is curated read-only spec behavior, never guessed.
		if effect, pure := pureBuiltinEffect(call); pure {
			return effect, true
		}
		if ast.IsPropertyAccessExpression(call.Expression) {
			access := call.Expression.AsPropertyAccessExpression()
			if ast.IsIdentifier(access.Expression) && access.Expression.Text() == "Math" {
				name := access.Name().Text()
				if un, ok := mathOps[name]; ok && call.Arguments != nil && len(call.Arguments.Nodes) == 1 {
					a, ok := LowerEffectExpression(call.Arguments.Nodes[0], reader)
					if !ok {
						return kernelbridge.LoopEffect{}, false
					}
					return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectUnary, Op: un, A: &a}, true
				}
				// `Math.pow(a, b)` and `Math.atan2(y, x)` — exactly two
				// arguments, in the order the clause names them. pow is the
				// same kernel row `a ** b` lowers to; atan2 answers its own
				// interval. Any other arity falls through to Opaque, which is
				// what these names did before this arm existed.
				if bin, ok := mathBinaryOps[name]; ok && call.Arguments != nil && len(call.Arguments.Nodes) == 2 {
					a, aOk := LowerEffectExpression(call.Arguments.Nodes[0], reader)
					b, bOk := LowerEffectExpression(call.Arguments.Nodes[1], reader)
					if !aOk || !bOk {
						return kernelbridge.LoopEffect{}, false
					}
					return kernelbridge.LoopEffect{
						Kind: kernelbridge.LoopEffectBinary, Op: bin, A: &a, B: &b,
					}, true
				}
				// `Math.min(a, b, c, ...)` — variadic by the spec, and the
				// wire carries min and max as BINARIES. min is associative,
				// so the n-ary call folds left into nested binaries and
				// means exactly what the call means; the two-argument case
				// is the fold's own first step and lowers identically to
				// what it always did.
				if (name == "min" || name == "max") && call.Arguments != nil && len(call.Arguments.Nodes) >= 2 {
					op := kernelbridge.LoopOpMin
					if name == "max" {
						op = kernelbridge.LoopOpMax
					}
					folded, foldedOk := LowerEffectExpression(call.Arguments.Nodes[0], reader)
					if !foldedOk {
						return kernelbridge.LoopEffect{}, false
					}
					for _, argument := range call.Arguments.Nodes[1:] {
						next, nextOk := LowerEffectExpression(argument, reader)
						if !nextOk {
							return kernelbridge.LoopEffect{}, false
						}
						left, right := folded, next
						folded = kernelbridge.LoopEffect{
							Kind: kernelbridge.LoopEffectBinary, Op: op, A: &left, B: &right,
						}
					}
					return folded, true
				}
				// ONE argument: the fold's own first step never runs, so
				// the call answers exactly ToNumber(x) (sec-math.min /
				// sec-math.max: the loop body runs once over a one-element
				// coerced list and the fold identity is immediately
				// overwritten). Every operand this grammar carries is
				// already number-sorted, so lowering the argument node
				// directly IS that ToNumber read — no separate coercion op
				// exists or is needed on this wire.
				if (name == "min" || name == "max") && call.Arguments != nil && len(call.Arguments.Nodes) == 1 {
					return LowerEffectExpression(call.Arguments.Nodes[0], reader)
				}
				// ZERO arguments: the spec's fold identity itself, read
				// before the loop ever runs — +Infinity for min
				// (sec-math.min step 3, "let lowest be +infinity"),
				// -Infinity for max (sec-math.max step 3, "let highest be
				// -infinity"). Exact constants, no operand needed.
				if name == "min" && call.Arguments != nil && len(call.Arguments.Nodes) == 0 {
					return kernelbridge.LoopEffect{
						Kind: kernelbridge.LoopEffectConst,
						Set:  refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{math.Inf(1)})),
					}, true
				}
				if name == "max" && call.Arguments != nil && len(call.Arguments.Nodes) == 0 {
					return kernelbridge.LoopEffect{
						Kind: kernelbridge.LoopEffectConst,
						Set:  refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{math.Inf(-1)})),
					}, true
				}
			}
		}
		return reader.Opaque(e)
	}
	return reader.Opaque(e)
}
