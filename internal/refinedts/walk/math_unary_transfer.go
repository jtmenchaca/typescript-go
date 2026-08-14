// from evaluation/math_unary_transfer.ts
//
// Unary Math.* kernel questions: floor/ceil/round/trunc/abs, the
// exp/log family, trig, and sqrt. (AbstractValue{}, false) when the
// name is not one of those — the caller continues its switch.

package walk

import (
	"math"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

var unaryMathOpWire = map[string]kernelbridge.TransferQuestionOp{
	"floor": kernelbridge.TransferOpFloor,
	"ceil":  kernelbridge.TransferOpCeil,
	"round": kernelbridge.TransferOpRound,
	"trunc": kernelbridge.TransferOpTrunc,
	"abs":   kernelbridge.TransferOpAbs,
	"sqrt":  kernelbridge.TransferOpSqrt,
	"exp":   kernelbridge.TransferOpExp,
	"log":   kernelbridge.TransferOpLog,
	"log2":  kernelbridge.TransferOpLog2,
	"log10": kernelbridge.TransferOpLog10,
	"expm1": kernelbridge.TransferOpExpm1,
	"log1p": kernelbridge.TransferOpLog1p,
	"cbrt":  kernelbridge.TransferOpCbrt,
	"sin":   kernelbridge.TransferOpSin,
	"cos":   kernelbridge.TransferOpCos,
	"tan":   kernelbridge.TransferOpTan,
	"sinh":  kernelbridge.TransferOpSinh,
	"cosh":  kernelbridge.TransferOpCosh,
	"tanh":  kernelbridge.TransferOpTanh,
	"atan":  kernelbridge.TransferOpAtan,
	"asin":  kernelbridge.TransferOpAsin,
	"atanh": kernelbridge.TransferOpAtanh,
	"asinh": kernelbridge.TransferOpAsinh,
	"acosh": kernelbridge.TransferOpAcosh,
}

func unaryTransfer(op string, args []abstractdomain.AbstractValue, operandTrustLevel abstractdomain.TrustLevel) abstractdomain.AbstractValue {
	if len(args) == 0 {
		return silence.Residue()
	}
	kernel := currentTransferKernel()
	if kernel == nil {
		return silence.Residue()
	}
	A, ok := SetOfKnownForTransfer(args[0])
	if !ok {
		return silence.Residue()
	}
	return abstractdomain.AtTrustLevel(
		KnownOfAnswer(kernel.Transfer(kernelbridge.TransferQuestion{Op: unaryMathOpWire[op], A: A})),
		operandTrustLevel,
	)
}

// UnaryMathImage is unaryMathImage in the TS source: the unary Math.*
// kernel image. (AbstractValue{}, false) = not a unary name this file
// reads — the caller continues.
func UnaryMathImage(name string, args []abstractdomain.AbstractValue, operandTrustLevel abstractdomain.TrustLevel) (abstractdomain.AbstractValue, bool) {
	switch name {
	case "floor", "ceil", "round", "trunc", "abs":
		return unaryTransfer(name, args, operandTrustLevel), true
	case "exp", "expm1", "cbrt", "sin", "cos", "tan", "sinh", "cosh", "tanh",
		"atan", "asin", "atanh", "asinh", "acosh":
		// the kernel's tight enclosure under the k-ulp assumption —
		// total on the reals, so no gate; the window's authority is
		// the transcription-plus-assumption tier, never the operands'
		return abstractdomain.AtTrustLevel(unaryTransfer(name, args, operandTrustLevel), abstractdomain.TrustSpec), true
	case "log1p":
		// real only past −1: the shifted-log window; the corners ride
		// the kernel's own pinned rows
		if len(args) == 0 {
			return silence.Residue(), true
		}
		A, ok := SetOfKnownForTransfer(args[0])
		if !ok {
			return silence.Residue(), true
		}
		pastMinusOne := false
		for _, f := range A.Forms {
			if (f.Form == refinementsets.FormAtLeast && f.A > -1) ||
				(f.Form == refinementsets.FormAbove && f.A >= -1) ||
				(f.Form == refinementsets.FormOneOf && allGreaterThan(f.W, -1)) {
				pastMinusOne = true
				break
			}
		}
		if pastMinusOne {
			return abstractdomain.AtTrustLevel(unaryTransfer("log1p", args, operandTrustLevel), abstractdomain.TrustSpec), true
		}
		// the sign is not pinned: the operand straddles −1, so the answer
		// is the shifted-log image of its provable past-−1 part alongside
		// the NaN the rest reaches (sec-math.log1p: NaN below −1)
		if split, ok := domainSplitImage("log1p", A, -1, true, operandTrustLevel); ok {
			return split, true
		}
		return silence.Residue(), true
	case "log", "log2", "log10":
		// the log window is real only over POSITIVE operands — zero
		// reaches −∞ (pinned kernel-side) and a negative is NaN, so
		// only a provably positive window routes; exact corners ride
		// through the kernel's own pinned rows
		if len(args) == 0 {
			return silence.Residue(), true
		}
		A, ok := SetOfKnownForTransfer(args[0])
		if !ok {
			return silence.Residue(), true
		}
		positive := false
		for _, f := range A.Forms {
			if (f.Form == refinementsets.FormAtLeast && f.A > 0) ||
				(f.Form == refinementsets.FormAbove && f.A >= 0) ||
				(f.Form == refinementsets.FormOneOf && allGreaterThan(f.W, 0)) {
				positive = true
				break
			}
		}
		if positive {
			return abstractdomain.AtTrustLevel(unaryTransfer(name, args, operandTrustLevel), abstractdomain.TrustSpec), true
		}
		// the sign is not pinned: the answer is the log image of the
		// operand's provably POSITIVE part alongside the NaN its negative
		// part reaches. Zero is clipped away with a STRICT floor — the
		// kernel's log family is positive-operands-only (transferLogWith:
		// "zero reaches −∞ and a negative operand is NaN, carried
		// reader-side"), so 0 must not enter the window it is asked.
		if split, ok := domainSplitImage(name, A, 0, true, operandTrustLevel); ok {
			return split, true
		}
		return silence.Residue(), true
	case "sqrt":
		// EXACTLY specified — 𝔽(√ℝ(n)) (vendored spec, sec-math.sqrt)
		// is the correctly-rounded root, which is MONOTONE in n: the
		// image of a nonnegative window is exactly the window of its
		// endpoint roots, each the host's own IEEE row. Negative runs
		// give NaN, which the reader's nanOr split carries; only a
		// provably nonnegative operand answers here.
		if len(args) == 0 {
			return silence.Residue(), true
		}
		A, ok := SetOfKnownForTransfer(args[0])
		if !ok {
			return silence.Residue(), true
		}
		// a SINGLETON set is the exact host row — spec-exact
		if len(A.Forms) == 1 && A.Forms[0].Form == refinementsets.FormOneOf && len(A.Forms[0].W) == 1 {
			root := math.Sqrt(A.Forms[0].W[0])
			grade := abstractdomain.MinTrustLevel(operandTrustLevel, abstractdomain.TrustSpec)
			if math.IsNaN(root) {
				return abstractdomain.AtTrustLevel(abstractdomain.NaNValue, grade), true
			}
			return abstractdomain.KnownValues([]float64{root}, abstractdomain.PrimitiveNumber, grade), true
		}
		window := RangeOfSet(A)
		if window == nil {
			return silence.Residue(), true
		}
		if window.Hi < 0 {
			return abstractdomain.AtTrustLevel(abstractdomain.NaNValue, operandTrustLevel), true
		}
		if window.Lo < 0 {
			// the window straddles zero: the nonnegative part's roots are
			// spec-exact and the negative part is NaN (sec-math.sqrt), so
			// the answer is the clipped image wrapped possibly-NaN — the
			// same split math_builtin_models.go's own sqrt-over-a-set
			// branch takes. Zero IS in sqrt's domain, so the floor here is
			// NOT strict.
			if split, ok := domainSplitImage("sqrt", A, 0, false, operandTrustLevel); ok {
				return split, true
			}
			return silence.Residue(), true
		}
		return abstractdomain.KnownSet(
			refinementsets.MakeRefinedSet(refinementsets.AtLeast(math.Sqrt(window.Lo)), refinementsets.AtMost(math.Sqrt(window.Hi))),
			nil,
			abstractdomain.MinTrustLevel(operandTrustLevel, abstractdomain.TrustSpec),
			abstractdomain.SetKindTagNone,
		), true
	default:
		return abstractdomain.AbstractValue{}, false
	}
}

// domainSplitImage answers a partial-domain unary Math call whose
// operand straddles the edge of that domain: the real image of the part
// that IS in the domain, wrapped possibly-NaN because the rest of the
// operand reaches NaN. This is the split math_builtin_models.go's own
// sqrt-over-a-set branch already takes, in the same construction —
// PossiblyNaN over the clipped image, graded at the operand's own level
// no better than spec.
//
// floor is the domain edge; strictFloor says the edge itself is OUTSIDE
// the domain (the logs, whose 0 reaches −∞ rather than a real, and
// log1p's −1), false where the edge is admitted (sqrt's 0).
//
// (AbstractValue{}, false) — the caller keeps its decline — when the
// operand poses no window, when the in-domain part is provably EMPTY
// (every value is past the edge, so nothing real survives the clip), or
// when the kernel declines the clipped question.
func domainSplitImage(
	op string,
	A refinementsets.RefinedSet,
	floor float64,
	strictFloor bool,
	operandTrustLevel abstractdomain.TrustLevel,
) (abstractdomain.AbstractValue, bool) {
	kernel := currentTransferKernel()
	if kernel == nil {
		return abstractdomain.AbstractValue{}, false
	}
	// nothing to split without a readable window, and nothing to answer
	// when the whole operand sits past the edge — the in-domain part is
	// empty, so the decline stands
	window := RangeOfSet(A)
	if window == nil {
		return abstractdomain.AbstractValue{}, false
	}
	if window.Hi < floor || (strictFloor && window.Hi == floor) {
		return abstractdomain.AbstractValue{}, false
	}
	edge := refinementsets.AtLeast(floor)
	if strictFloor {
		edge = refinementsets.Above(floor)
	}
	clipped := refinementsets.MakeRefinedSet(
		append(append([]refinementsets.Refinement{}, A.Forms...), edge)...,
	)
	image := KnownOfAnswer(kernel.Transfer(kernelbridge.TransferQuestion{
		Op: unaryMathOpWire[op],
		A:  clipped,
	}))
	// an unknown or all-NaN image says the clipped question itself was
	// declined — no real half to speak for, so the caller's decline stands
	if image.Kind == abstractdomain.KindUnknown || image.Kind == abstractdomain.KindNaN {
		return abstractdomain.AbstractValue{}, false
	}
	grade := abstractdomain.MinTrustLevel(operandTrustLevel, abstractdomain.TrustSpec)
	return abstractdomain.AtTrustLevel(abstractdomain.PossiblyNaN(image), grade), true
}

func allGreaterThan(values []float64, floor float64) bool {
	for _, v := range values {
		if !(v > floor) {
			return false
		}
	}
	return true
}
