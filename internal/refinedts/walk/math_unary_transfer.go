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

func allGreaterThan(values []float64, floor float64) bool {
	for _, v := range values {
		if !(v > floor) {
			return false
		}
	}
	return true
}
