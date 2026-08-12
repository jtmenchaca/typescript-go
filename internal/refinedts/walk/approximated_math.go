// from evaluation/approximated_math.ts
//
// The implementation-approximated Math family. The spec pins only
// CORNERS (steps before the approximation) — those rows answer
// exactly, and a set inside a NaN domain answers NaN, decided as
// kernel SUBSET questions. The approximation step itself pins
// nothing (the `**` precedent: no exact value is claimed for what
// the spec leaves implementation-defined), so everything else is
// unknown. (AbstractValue{}, false) = not a Math name this table reads.

package walk

import (
	"math"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// approximatedRow is one row of the pinned-corners table: an exact
// input, and its output (NaN when hasOutput is false).
type approximatedRow struct {
	input     float64
	output    float64
	hasOutput bool
}

// approximatedEntry is the per-function row set: the pinned corner
// rows (a missing output means NaN) and the input domains whose
// whole image is NaN. Rows are the spec's own steps (21.3.2), nothing
// more.
type approximatedEntry struct {
	pinned     []approximatedRow
	nanDomains []refinementsets.RefinedSet
}

var approximated = map[string]approximatedEntry{
	"cbrt": {
		pinned: []approximatedRow{
			{0, 0, true}, {math.Inf(1), math.Inf(1), true}, {math.Inf(-1), math.Inf(-1), true},
		},
	},
	"exp": {
		pinned: []approximatedRow{
			{0, 1, true}, {math.Inf(1), math.Inf(1), true}, {math.Inf(-1), 0, true},
		},
	},
	"expm1": {
		pinned: []approximatedRow{
			{0, 0, true}, {math.Inf(1), math.Inf(1), true}, {math.Inf(-1), -1, true},
		},
	},
	"log": {
		pinned: []approximatedRow{
			{1, 0, true}, {0, math.Inf(-1), true}, {math.Inf(1), math.Inf(1), true},
		},
		nanDomains: []refinementsets.RefinedSet{refinementsets.MakeRefinedSet(refinementsets.Below(0))},
	},
	"log2": {
		pinned: []approximatedRow{
			{1, 0, true}, {0, math.Inf(-1), true}, {math.Inf(1), math.Inf(1), true},
		},
		nanDomains: []refinementsets.RefinedSet{refinementsets.MakeRefinedSet(refinementsets.Below(0))},
	},
	"log10": {
		pinned: []approximatedRow{
			{1, 0, true}, {0, math.Inf(-1), true}, {math.Inf(1), math.Inf(1), true},
		},
		nanDomains: []refinementsets.RefinedSet{refinementsets.MakeRefinedSet(refinementsets.Below(0))},
	},
	"log1p": {
		pinned: []approximatedRow{
			{0, 0, true}, {-1, math.Inf(-1), true}, {math.Inf(1), math.Inf(1), true},
		},
		nanDomains: []refinementsets.RefinedSet{refinementsets.MakeRefinedSet(refinementsets.Below(-1))},
	},
	"sin": {
		pinned: []approximatedRow{
			{0, 0, true}, {math.Inf(1), 0, false}, {math.Inf(-1), 0, false},
		},
	},
	"cos": {
		pinned: []approximatedRow{
			{0, 1, true}, {math.Inf(1), 0, false}, {math.Inf(-1), 0, false},
		},
	},
	"tan": {
		pinned: []approximatedRow{
			{0, 0, true}, {math.Inf(1), 0, false}, {math.Inf(-1), 0, false},
		},
	},
	"asin": {
		pinned:     []approximatedRow{{0, 0, true}},
		nanDomains: []refinementsets.RefinedSet{refinementsets.MakeRefinedSet(refinementsets.Above(1)), refinementsets.MakeRefinedSet(refinementsets.Below(-1))},
	},
	"acos": {
		pinned:     []approximatedRow{{1, 0, true}},
		nanDomains: []refinementsets.RefinedSet{refinementsets.MakeRefinedSet(refinementsets.Above(1)), refinementsets.MakeRefinedSet(refinementsets.Below(-1))},
	},
	"atan": {
		pinned: []approximatedRow{{0, 0, true}},
	},
	"sinh": {
		pinned: []approximatedRow{
			{0, 0, true}, {math.Inf(1), math.Inf(1), true}, {math.Inf(-1), math.Inf(-1), true},
		},
	},
	"cosh": {
		pinned: []approximatedRow{
			{0, 1, true}, {math.Inf(1), math.Inf(1), true}, {math.Inf(-1), math.Inf(1), true},
		},
	},
	"tanh": {
		pinned: []approximatedRow{
			{0, 0, true}, {math.Inf(1), 1, true}, {math.Inf(-1), -1, true},
		},
	},
	"asinh": {
		pinned: []approximatedRow{
			{0, 0, true}, {math.Inf(1), math.Inf(1), true}, {math.Inf(-1), math.Inf(-1), true},
		},
	},
	"acosh": {
		pinned:     []approximatedRow{{1, 0, true}, {math.Inf(1), math.Inf(1), true}},
		nanDomains: []refinementsets.RefinedSet{refinementsets.MakeRefinedSet(refinementsets.Below(1))},
	},
	"atanh": {
		pinned:     []approximatedRow{{0, 0, true}, {1, math.Inf(1), true}, {-1, math.Inf(-1), true}},
		nanDomains: []refinementsets.RefinedSet{refinementsets.MakeRefinedSet(refinementsets.Above(1)), refinementsets.MakeRefinedSet(refinementsets.Below(-1))},
	},
}

// TransferApproximated is transferApproximated in the TS source.
// (AbstractValue{}, false) when name is not a Math name this table
// reads.
func TransferApproximated(name string, raw abstractdomain.AbstractValue, hasRaw bool) (abstractdomain.AbstractValue, bool) {
	rows, ok := approximated[name]
	if !ok {
		return abstractdomain.AbstractValue{}, false
	}
	kernel := currentTransferKernel()
	if !hasRaw || kernel == nil {
		return silence.Residue(), true
	}
	only := NumericOperand(raw)
	if only.Kind == abstractdomain.KindNaN {
		return abstractdomain.NaNValue, true // all of them: NaN-in, NaN-out
	}
	var single float64
	hasSingle := false
	if only.Kind == abstractdomain.KindValues && len(only.Values) == 1 &&
		only.KindTag != abstractdomain.PrimitiveString && only.KindTag != abstractdomain.PrimitiveArray {
		single, hasSingle = only.Values[0], true
	}
	if hasSingle {
		for _, row := range rows.pinned {
			if single == row.input {
				if !row.hasOutput {
					return abstractdomain.NaNValue, true
				}
				return abstractdomain.KnownValues([]float64{row.output}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved), true
			}
		}
	}
	A, ok := SetOfKnownForTransfer(only)
	if !ok {
		return silence.Residue(), true
	}
	for _, domain := range rows.nanDomains {
		subset, callOk := callScalarSubset(kernel, A, domain)
		if !callOk {
			return silence.Residue(), true
		}
		if subset {
			return abstractdomain.NaNValue, true
		}
	}
	return silence.Residue(), true
}

func callScalarSubset(kernel *kernelbridge.RefinedTSKernel, a, b refinementsets.RefinedSet) (subset bool, ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	return kernel.ScalarSubset(a, b), true
}
