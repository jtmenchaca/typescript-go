// from control_flow/loop_accumulation.ts
//
// Solve one accumulation — the loop engine over a single value:
// iterate, widen, certify, tighten. Used by reduce and kin.

package walk

import (
	"math"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

const accumulationIterations = 3

// SolveAccumulation solves one accumulation — the loop engine over a
// single value: iterate the step over the exact join; widen what
// refuses to stabilize (last-two-iterates rule); the kernel certifies
// (the initial value inside the candidate, one step from it back
// inside); one narrowing round recovers precision. Unknown when
// nothing certifies — never a guess. `reduce` and kin ride this.
func SolveAccumulation(
	ctx *FlowContext,
	initial abstractdomain.AbstractValue,
	step func(abstractdomain.AbstractValue) abstractdomain.AbstractValue,
) abstractdomain.AbstractValue {
	candidate := initial
	iterates := []abstractdomain.AbstractValue{candidate}
	for i := 0; i < accumulationIterations; i++ {
		joined := abstractdomain.JoinKnown(candidate, step(candidate))
		if abstractdomain.SameKnown(joined, candidate) {
			return candidate
		}
		candidate = joined
		iterates = append(iterates, candidate)
	}
	ranges := make([]*NumberRange, len(iterates))
	for i, iterate := range iterates {
		ranges[i] = RangeOfKnown(iterate)
		if ranges[i] == nil {
			return silence.Residue()
		}
	}
	last := len(ranges) - 1
	lo := math.Inf(-1)
	if ranges[last].Lo == ranges[last-1].Lo {
		lo = ranges[last].Lo
	}
	hi := math.Inf(1)
	if ranges[last].Hi == ranges[last-1].Hi {
		hi = ranges[last].Hi
	}
	int_ := true
	for _, r := range ranges {
		if !r.Int {
			int_ = false
			break
		}
	}
	if math.IsInf(lo, -1) && math.IsInf(hi, 1) && !int_ {
		return silence.Residue()
	}
	forms := []refinementsets.Refinement{
		refinementsets.AtLeast(lo),
		refinementsets.AtMost(hi),
	}
	if int_ {
		forms = append(forms, refinementsets.Integer)
	}
	widened := abstractdomain.KnownSet(
		refinementsets.MakeRefinedSet(forms...), nil,
		abstractdomain.TrustProved, "")
	if widened.Kind != abstractdomain.KindSet {
		return silence.Residue()
	}
	if !CertifiedInvariant(ctx, initial, step(widened), widened.Set) {
		return silence.Residue()
	}
	// one narrowing round, re-certified
	tightened := abstractdomain.JoinKnown(initial, step(widened))
	if tightened.Kind == abstractdomain.KindSet &&
		!abstractdomain.SameKnown(tightened, widened) &&
		CertifiedInvariant(ctx, initial, step(tightened), tightened.Set) {
		return tightened
	}
	return widened
}
