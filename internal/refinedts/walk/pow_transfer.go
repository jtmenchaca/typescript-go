// from evaluation/pow_transfer.ts
//
// `**` — ECMA-262's pinned branches and the exact integer-power
// carve-out, decided in the kernel over three-state operands.

package walk

import (
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

func powArgOf(k abstractdomain.AbstractValue) kernelbridge.PowOperandWire {
	if k.Kind == abstractdomain.KindNaN {
		return kernelbridge.PowOperandWire{Kind: kernelbridge.PowOperandNaN}
	}
	set, ok := SetOfKnownForTransfer(k)
	if !ok {
		return kernelbridge.PowOperandWire{Kind: kernelbridge.PowOperandUnknown}
	}
	return kernelbridge.PowOperandWire{Kind: kernelbridge.PowOperandSet, Set: set}
}

// TransferPow is transferPow in the TS source: `**` — ECMA-262's
// pinned branches and the exact integer-power carve-out, decided in
// the kernel over three-state operands.
func TransferPow(base, exponent abstractdomain.AbstractValue) abstractdomain.AbstractValue {
	return OrUnknown(func() abstractdomain.AbstractValue { return powImage(base, exponent) })
}

func powImage(base, exponent abstractdomain.AbstractValue) abstractdomain.AbstractValue {
	kernel := currentTransferKernel()
	if kernel == nil {
		return silence.Residue()
	}
	// a real-base corner window rests on the k-ulp assumption, so the
	// whole route wears the transcription-plus-assumption tier — the
	// same "spec" cap the rest of the enclosure family wears
	return abstractdomain.AtTrustLevel(
		abstractdomain.AtTrustLevel(
			KnownOfAnswer(kernel.Transfer(kernelbridge.TransferQuestion{
				Op:   kernelbridge.TransferOpPow,
				Base: powArgOf(NumericOperand(base)),
				Exp:  powArgOf(NumericOperand(exponent)),
			})),
			abstractdomain.DerivedTrustLevel(abstractdomain.TrustProved, base, exponent),
		),
		abstractdomain.TrustSpec,
	)
}
