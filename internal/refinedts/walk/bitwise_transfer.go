// from evaluation/bitwise_transfer.ts
//
// The bitwise operators — the ToInt32/ToUint32 reinterpretations,
// decided in the kernel (exact on singletons; `x | 0` poses the
// whole toInt32 question, so the certified range rule applies). A
// NaN operand reads as the spec's pinned ToInt32(NaN) = 0.

package walk

import (
	"math"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// BitwiseOperator is the "bitOr" | "bitAnd" | "bitXor" | "shl" | "sar"
// | "shr" union.
type BitwiseOperator string

const (
	BitOr  BitwiseOperator = "bitOr"
	BitAnd BitwiseOperator = "bitAnd"
	BitXor BitwiseOperator = "bitXor"
	Shl    BitwiseOperator = "shl"
	Sar    BitwiseOperator = "sar"
	Shr    BitwiseOperator = "shr"
)

var bitwiseOpWire = map[BitwiseOperator]kernelbridge.TransferQuestionOp{
	BitOr:  kernelbridge.TransferOpBitOr,
	BitAnd: kernelbridge.TransferOpBitAnd,
	BitXor: kernelbridge.TransferOpBitXor,
	Shl:    kernelbridge.TransferOpShl,
	Sar:    kernelbridge.TransferOpSar,
	Shr:    kernelbridge.TransferOpShr,
}

// TransferBitwise is transferBitwise in the TS source: the bitwise
// operators — the ToInt32/ToUint32 reinterpretations, decided in the
// kernel (exact on singletons; `x | 0` poses the whole toInt32
// question, so the certified range rule applies). A NaN operand reads
// as the spec's pinned ToInt32(NaN) = 0.
func TransferBitwise(op BitwiseOperator, rawA, rawB abstractdomain.AbstractValue) abstractdomain.AbstractValue {
	return OrUnknown(func() abstractdomain.AbstractValue { return bitwiseImage(op, rawA, rawB) })
}

func bitwiseImage(op BitwiseOperator, rawA, rawB abstractdomain.AbstractValue) abstractdomain.AbstractValue {
	grade := abstractdomain.DerivedTrustLevel(abstractdomain.TrustProved, rawA, rawB)
	zeroIfNaN := func(k abstractdomain.AbstractValue) abstractdomain.AbstractValue {
		if k.Kind == abstractdomain.KindNaN {
			return abstractdomain.KnownValues([]float64{0}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
		}
		return k
	}
	// the ToInt32/ToUint32 image is pinned for EVERY input — NaN,
	// undefined, and ±∞ all land on 0 (sec-toint32) — so a wrapper
	// strips and an unknown operand poses the universal set: the
	// answer is the operator's full window, determined regardless
	var undressed func(k abstractdomain.AbstractValue) abstractdomain.AbstractValue
	undressed = func(k abstractdomain.AbstractValue) abstractdomain.AbstractValue {
		if k.Kind == abstractdomain.KindPossiblyNaN || k.Kind == abstractdomain.KindPossiblyUndefined {
			return undressed(*k.Inner)
		}
		return k
	}
	universal := func(k abstractdomain.AbstractValue) abstractdomain.AbstractValue {
		if k.Kind == abstractdomain.KindUnknown {
			return abstractdomain.KnownSet(refinementsets.MakeRefinedSet(refinementsets.AtLeast(math.Inf(-1))), nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone)
		}
		return k
	}
	a := universal(undressed(zeroIfNaN(NumericOperand(rawA))))
	b := universal(undressed(zeroIfNaN(NumericOperand(rawB))))
	kernel := currentTransferKernel()
	if kernel == nil {
		return silence.Residue()
	}
	// x | 0 and x ^ 0 (either side) ARE Number::toInt32 of the other
	// side, and x >> 0 / x >>> 0 are the conversions outright
	// (sec-numeric-types-number-bitwiseOR and family: the identity
	// element leaves the converted operand)
	zeroSide := func(k abstractdomain.AbstractValue) bool {
		return k.Kind == abstractdomain.KindValues && len(k.Values) == 1 && k.Values[0] == 0
	}
	if (op == BitOr || op == BitXor) && (zeroSide(a) || zeroSide(b)) {
		other := b
		if zeroSide(b) {
			other = a
		}
		A, ok := SetOfKnownForTransfer(other)
		if !ok {
			return silence.Residue()
		}
		return abstractdomain.AtTrustLevel(
			KnownOfAnswer(kernel.Transfer(kernelbridge.TransferQuestion{Op: kernelbridge.TransferOpInt32Wrap, A: A})),
			grade,
		)
	}
	if op == Sar && zeroSide(b) {
		A, ok := SetOfKnownForTransfer(a)
		if !ok {
			return silence.Residue()
		}
		return abstractdomain.AtTrustLevel(
			KnownOfAnswer(kernel.Transfer(kernelbridge.TransferQuestion{Op: kernelbridge.TransferOpInt32Wrap, A: A})),
			grade,
		)
	}
	A, aOk := SetOfKnownForTransfer(a)
	B, bOk := SetOfKnownForTransfer(b)
	if !aOk || !bOk {
		return silence.Residue()
	}
	direct := KnownOfAnswer(kernel.Transfer(kernelbridge.TransferQuestion{Op: bitwiseOpWire[op], A: A, B: B}))
	if direct.Kind != abstractdomain.KindUnknown {
		return abstractdomain.AtTrustLevel(direct, grade)
	}
	// the op's UNIVERSAL image still answers: `>>>` returns
	// ToUint32's range and every other operator ToInt32's, whatever
	// the operands were (sec-numeric-types-number-unsignedRightShift;
	// sec-toint32 / sec-touint32 pin the windows) — a transcription
	// claim, stated at spec grade
	var window refinementsets.RefinedSet
	if op == Shr {
		window = refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.AtMost(4294967295), refinementsets.Integer)
	} else {
		window = refinementsets.MakeRefinedSet(refinementsets.AtLeast(-2147483648), refinementsets.AtMost(2147483647), refinementsets.Integer)
	}
	return abstractdomain.AtTrustLevel(
		abstractdomain.KnownSet(window, nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone),
		grade,
	)
}
