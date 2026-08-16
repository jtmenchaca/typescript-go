// from comparison/comparison_decision.ts
//
// One comparison, decided. Every finite row is a MEMBERSHIP question
// to the kernel — `a < b` asks whether a is in the set below b — so
// the answer is proved rather than computed here. The rows the kernel
// cannot be asked are transcribed from the specification and marked
// as such: NaN fails every comparison and passes every disequality,
// absence compares only under `==`, and infinite words order by the
// extended reals rather than by float arithmetic.
//
// comparison/'s one file (compareKnown reads FlowContext) joins
// package walk at integration per PORT.md's "the walk package" note —
// ported here directly since evaluate_operators.ts and
// binary_comparison.ts need it now; the placeholder `comparison`
// package (internal/refinedts/comparison/comparison_decision.go) is
// deleted at integration.

package walk

import (
	"errors"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

var errKernelRefused = errors.New("kernel refused the question")

// ComparisonOp is the "lt" | "gt" | "le" | "ge" | "eq" | "ne" union.
type ComparisonOp string

const (
	CompareLt ComparisonOp = "lt"
	CompareGt ComparisonOp = "gt"
	CompareLe ComparisonOp = "le"
	CompareGe ComparisonOp = "ge"
	CompareEq ComparisonOp = "eq"
	CompareNe ComparisonOp = "ne"
)

// CompareKnown is compareKnown in the TS source.
func CompareKnown(ctx *FlowContext, op ComparisonOp, strict bool, a, b abstractdomain.AbstractValue) abstractdomain.AbstractValue {
	operandTrustLevel := abstractdomain.MinTrustLevel(abstractdomain.TrustLevelOf(a), abstractdomain.TrustLevelOf(b))
	// kernel-decided rows carry the operands' grade; the transcribed
	// corner tables (NaN, absence, the extended-real order) dip to spec
	kernelRow := func(v bool) abstractdomain.AbstractValue {
		return boolValue(v, operandTrustLevel)
	}
	boolAt := func(v bool) abstractdomain.AbstractValue {
		return boolValue(v, abstractdomain.MinTrustLevel(operandTrustLevel, abstractdomain.TrustSpec))
	}
	if a.Kind == abstractdomain.KindNaN || b.Kind == abstractdomain.KindNaN {
		return boolAt(op == CompareNe)
	}
	// null and undefined loose-equal each other and nothing else; the
	// absent marker CONFLATES the two, so strict absent-vs-absent stays
	// unknown (null === undefined is false, undefined === undefined
	// true) while absent-vs-value decides
	if a.Kind == abstractdomain.KindUndef || b.Kind == abstractdomain.KindUndef {
		if op != CompareEq && op != CompareNe {
			return silence.Residue()
		}
		if a.Kind == abstractdomain.KindUndef && b.Kind == abstractdomain.KindUndef {
			if strict {
				return silence.Residue()
			}
			return boolAt(op == CompareEq)
		}
		other := a
		if a.Kind == abstractdomain.KindUndef {
			other = b
		}
		if other.Kind == abstractdomain.KindValues || other.Kind == abstractdomain.KindObject ||
			other.Kind == abstractdomain.KindList || other.Kind == abstractdomain.KindArrayHoles {
			return boolAt(op == CompareNe)
		}
		return silence.Residue()
	}
	if a.Kind != abstractdomain.KindValues || b.Kind != abstractdomain.KindValues {
		return silence.Residue()
	}
	// strings: equality is tuple equality
	if a.KindTag == abstractdomain.PrimitiveString && b.KindTag == abstractdomain.PrimitiveString {
		if op != CompareEq && op != CompareNe {
			return silence.Residue() // lexicographic order: later
		}
		if len(a.Values) == 0 || len(b.Values) == 0 {
			equal := len(a.Values) == 0 && len(b.Values) == 0
			if op == CompareEq {
				return boolAt(equal)
			}
			return boolAt(!equal)
		}
		target, ok := abstractdomain.SetOfKnown(b)
		if !ok {
			return silence.Residue()
		}
		equal, err := callMember(ctx.Kernel, target, a.Values)
		if err != nil {
			return silence.Residue()
		}
		if op == CompareEq {
			return kernelRow(equal)
		}
		return kernelRow(!equal)
	}
	// strict equality across sorts is decided by type alone: when the
	// sides wear different sorts, IsStrictlyEqual answers false before
	// any value is read (sec-isstrictlyequal, step 1: if SameType is
	// false, return false)
	if strict && a.KindTag != b.KindTag {
		if op == CompareEq {
			return boolAt(false)
		}
		if op == CompareNe {
			return boolAt(true)
		}
		return silence.Residue()
	}
	// arrays compare by REFERENCE — identity is not value knowledge
	if a.KindTag == abstractdomain.PrimitiveArray || b.KindTag == abstractdomain.PrimitiveArray {
		return silence.Residue()
	}
	if len(a.Values) != 1 || len(b.Values) != 1 {
		return silence.Residue()
	}
	x := a.Values[0]
	y := b.Values[0]
	if !isFinite(x) || !isFinite(y) {
		switch op {
		case CompareEq:
			return boolAt(x == y)
		case CompareNe:
			return boolAt(x != y)
		case CompareLt:
			return boolAt(x < y)
		case CompareGt:
			return boolAt(x > y)
		case CompareLe:
			return boolAt(x <= y)
		case CompareGe:
			return boolAt(x >= y)
		}
	}
	switch op {
	case CompareLt:
		v, err := callMember(ctx.Kernel, refinementsets.MakeRefinedSet(refinementsets.Below(y)), []float64{x})
		if err != nil {
			return silence.Residue()
		}
		return kernelRow(v)
	case CompareGt:
		v, err := callMember(ctx.Kernel, refinementsets.MakeRefinedSet(refinementsets.Above(y)), []float64{x})
		if err != nil {
			return silence.Residue()
		}
		return kernelRow(v)
	case CompareLe:
		v, err := callMember(ctx.Kernel, refinementsets.MakeRefinedSet(refinementsets.AtMost(y)), []float64{x})
		if err != nil {
			return silence.Residue()
		}
		return kernelRow(v)
	case CompareGe:
		v, err := callMember(ctx.Kernel, refinementsets.MakeRefinedSet(refinementsets.AtLeast(y)), []float64{x})
		if err != nil {
			return silence.Residue()
		}
		return kernelRow(v)
	case CompareEq:
		v, err := callMember(ctx.Kernel, refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{y})), []float64{x})
		if err != nil {
			return silence.Residue()
		}
		return kernelRow(v)
	case CompareNe:
		v, err := callMember(ctx.Kernel, refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{y})), []float64{x})
		if err != nil {
			return silence.Residue()
		}
		return kernelRow(!v)
	}
	return silence.Residue()
}

func boolValue(v bool, grade abstractdomain.TrustLevel) abstractdomain.AbstractValue {
	if v {
		return abstractdomain.KnownValues([]float64{1}, abstractdomain.PrimitiveBoolean, grade)
	}
	return abstractdomain.KnownValues([]float64{0}, abstractdomain.PrimitiveBoolean, grade)
}

// callMember mirrors the TS source's try/catch around
// ctx.kernel.member(...): Member has no ready-made recovering wrapper
// (kernelbridge's question methods panic on a refused question, per
// PORT.md), so this recovers the same way the TS catch does.
func callMember(kernel *kernelbridge.RefinedTSKernel, set refinementsets.RefinedSet, tuple []float64) (member bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errKernelRefused
		}
	}()
	return kernel.Member(set, tuple), nil
}
