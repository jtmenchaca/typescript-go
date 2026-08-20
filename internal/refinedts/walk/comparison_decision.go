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
	// KindUndef now means exactly-undefined and KindNull exactly-null (the
	// domain's AbsentMark split); a KindPossiblyUndefined wrapper is the
	// only value still admitting both flavors at once (PossiblyUndefined's
	// own comment: a wrapper with Inner.Kind == KindNull states "null or
	// undefined" and does not collapse). Flavored rows below therefore
	// gate on the EXACT kinds only — a wrapper or KindUnknown on either
	// side must fall through undecided rather than be read as one flavor.
	aExactAbsent := a.Kind == abstractdomain.KindUndef || a.Kind == abstractdomain.KindNull
	bExactAbsent := b.Kind == abstractdomain.KindUndef || b.Kind == abstractdomain.KindNull
	if aExactAbsent || bExactAbsent {
		if op != CompareEq && op != CompareNe {
			return silence.Residue()
		}
		if aExactAbsent && bExactAbsent {
			// sec-isstrictlyequal step 1: SameType false -> false;
			// sec-islooselyequal steps 2-3: null/undefined cross-flavor
			// -> true; same-flavor case (SameType true) defers to
			// IsStrictlyEqual (islooselyequal step 1), which for a
			// non-Number SameType pair is SameValueNonNumber
			// (sec-samevaluenonnumber step 2: "If x is either undefined
			// or null, return true").
			sameFlavor := a.Kind == b.Kind
			if strict {
				return boolAt(sameFlavor == (op == CompareEq))
			}
			// loose: same-flavor -> true (via strict), cross-flavor ->
			// true (islooselyequal steps 2-3) — every combination of
			// exact null/undefined loose-equals every other
			return boolAt(op == CompareEq)
		}
		// exactly one side is an exact absent kind (Undef or Null); the
		// other side is some non-absent value
		other := a
		if aExactAbsent {
			other = b
		}
		if other.Kind == abstractdomain.KindValues || other.Kind == abstractdomain.KindObject ||
			other.Kind == abstractdomain.KindList || other.Kind == abstractdomain.KindArrayHoles {
			// sec-isstrictlyequal step 1: SameType(absent, non-absent)
			// is false -> IsStrictlyEqual false, so === is false and
			// !== is true regardless of strict/loose — a non-absent
			// exact value is never null/undefined at runtime, and
			// IsLooselyEqual's own null/undefined steps (2-3) only
			// fire when the OTHER side is itself null or undefined, so
			// the loose reading agrees with the strict one here too
			return boolAt(op == CompareNe)
		}
		return silence.Residue()
	}
	if a.Kind != abstractdomain.KindValues || b.Kind != abstractdomain.KindValues {
		return silence.Residue()
	}
	// strings: equality is tuple equality; ordering is code-unit
	// lexicographic (cmp.7)
	if a.KindTag == abstractdomain.PrimitiveString && b.KindTag == abstractdomain.PrimitiveString {
		if len(a.Values) == 0 || len(b.Values) == 0 {
			return compareKnownEmptyString(op, boolAt, len(a.Values), len(b.Values))
		}
		switch op {
		case CompareEq, CompareNe:
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
		case CompareLt, CompareGt, CompareLe, CompareGe:
			return compareKnownStringOrder(ctx, op, kernelRow, a, b)
		}
		return silence.Residue()
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

// callSeqLexLt recovers a refused SeqLexLt ask the same way callMember
// recovers Member — the kernel panics on a refusal, never returns an
// error value, so every ask needs its own recovering wrapper.
func callSeqLexLt(kernel *kernelbridge.RefinedTSKernel, a, b refinementsets.RefinedSet) (lt bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errKernelRefused
		}
	}()
	return kernel.SeqLexLt(a, b), nil
}

// compareKnownEmptyString decides a comparison where at least one side
// is the empty string, without asking the kernel: `""` is the least
// word under lexLtB (lexLtB [] l = (l ≠ [])), so ordering against an
// empty operand reads off length alone, and equality is length
// equality at this corner (both sides length 0).
func compareKnownEmptyString(op ComparisonOp, boolAt func(bool) abstractdomain.AbstractValue, aLen, bLen int) abstractdomain.AbstractValue {
	switch op {
	case CompareEq:
		return boolAt(aLen == 0 && bLen == 0)
	case CompareNe:
		return boolAt(!(aLen == 0 && bLen == 0))
	case CompareLt:
		return boolAt(aLen == 0 && bLen > 0)
	case CompareLe:
		return boolAt(aLen == 0)
	case CompareGt:
		return boolAt(bLen == 0 && aLen > 0)
	case CompareGe:
		return boolAt(bLen == 0)
	}
	return silence.Residue()
}

// compareKnownStringOrder decides `<`/`>`/`<=`/`>=` on two known
// nonempty strings — cmp.7, code-unit lexicographic order. Every row
// is phrased as ONE SeqLexLt ask: `<` and `>` ask it directly (in
// swapped operand order for `>`), and `<=`/`>=` are lt's negation the
// other way (a <= b iff not (b < a), the total order's trichotomy —
// lexLtB_asymm plus totality mean exactly one of a<b, a=b, b<a holds,
// so "not b<a" is exactly "a<=b").
func compareKnownStringOrder(ctx *FlowContext, op ComparisonOp, kernelRow func(bool) abstractdomain.AbstractValue, a, b abstractdomain.AbstractValue) abstractdomain.AbstractValue {
	setA, okA := abstractdomain.SetOfKnown(a)
	setB, okB := abstractdomain.SetOfKnown(b)
	if !okA || !okB {
		return silence.Residue()
	}
	switch op {
	case CompareLt:
		v, err := callSeqLexLt(ctx.Kernel, setA, setB)
		if err != nil {
			return silence.Residue()
		}
		return kernelRow(v)
	case CompareGt:
		v, err := callSeqLexLt(ctx.Kernel, setB, setA)
		if err != nil {
			return silence.Residue()
		}
		return kernelRow(v)
	case CompareLe:
		v, err := callSeqLexLt(ctx.Kernel, setB, setA)
		if err != nil {
			return silence.Residue()
		}
		return kernelRow(!v)
	case CompareGe:
		v, err := callSeqLexLt(ctx.Kernel, setA, setB)
		if err != nil {
			return silence.Residue()
		}
		return kernelRow(!v)
	}
	return silence.Residue()
}
