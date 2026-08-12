// from control_flow/certified_invariant.ts
//
// One certificate for one binding: entry premise inside the candidate
// AND the step's image back inside — a single kernel question.

package walk

import (
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// CertifiedInvariant is certifiedInvariant in the TS source: one
// certificate for one binding — the entry premise inside the
// candidate AND the step's image back inside — a SINGLE kernel
// question, with the induction principle proved behind it
// (set_functions/invariant.lean `invariant_certifies`). An unknown
// premise, or a refused question, is a no.
//
// The TS source wraps the kernel call in try/catch: a refused
// question throws. The Go RefinedTSKernel.Invariant field already
// answers false on a refusal rather than panicking (see
// kernelbridge/kernel_interface.go's field comment), so no
// defer/recover is needed here — the fallback is built into the
// kernel call itself.
func CertifiedInvariant(
	ctx *FlowContext,
	entry abstractdomain.AbstractValue,
	stepImage abstractdomain.AbstractValue,
	candidate refinementsets.RefinedSet,
) bool {
	premise := func(k abstractdomain.AbstractValue) (kernelbridge.InvariantPremise, bool) {
		switch k.Kind {
		case abstractdomain.KindValues:
			return kernelbridge.InvariantPremise{Kind: kernelbridge.InvariantPremiseValues, Values: k.Values}, true
		case abstractdomain.KindSet:
			return kernelbridge.InvariantPremise{Kind: kernelbridge.InvariantPremiseSet, Set: k.Set}, true
		default:
			return kernelbridge.InvariantPremise{}, false
		}
	}
	entryPremise, entryOk := premise(entry)
	stepPremise, stepOk := premise(stepImage)
	if !entryOk || !stepOk {
		return false
	}
	return ctx.Kernel.Invariant(candidate, entryPremise, stepPremise)
}
