// from evaluation/math_transfer.ts
//
// The Math built-ins the checker reads. (AbstractValue{}, false) =
// not transferred.

package walk

import (
	"math"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// transferSign is transferSign in the TS source: which of {−1, 0,
// +1} the operand's set can reach — Math.sign's spec steps, decided
// as kernel DISJOINTNESS questions against the three rays. Exact when
// one piece remains.
func transferSign(raw abstractdomain.AbstractValue, hasRaw bool) abstractdomain.AbstractValue {
	kernel := currentTransferKernel()
	if !hasRaw || kernel == nil {
		return silence.Residue()
	}
	only := NumericOperand(raw)
	if only.Kind == abstractdomain.KindNaN {
		return abstractdomain.NaNValue
	}
	A, ok := SetOfKnownForTransfer(only)
	if !ok {
		return silence.Residue()
	}
	var pieces []float64
	disjointBelow, disOk1 := callScalarDisjoint(kernel, A, refinementsets.MakeRefinedSet(refinementsets.Below(0)))
	disjointZero, disOk2 := callScalarDisjoint(kernel, A, refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{0})))
	disjointAbove, disOk3 := callScalarDisjoint(kernel, A, refinementsets.MakeRefinedSet(refinementsets.Above(0)))
	if !disOk1 || !disOk2 || !disOk3 {
		return silence.Residue()
	}
	if !disjointBelow {
		pieces = append(pieces, -1)
	}
	if !disjointZero {
		pieces = append(pieces, 0)
	}
	if !disjointAbove {
		pieces = append(pieces, 1)
	}
	if len(pieces) == 0 {
		return silence.Residue()
	}
	if len(pieces) == 1 {
		return abstractdomain.KnownValues(pieces, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
	}
	return abstractdomain.KnownSet(refinementsets.MakeRefinedSet(refinementsets.OneOf(pieces)), nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone)
}

func callScalarDisjoint(kernel *kernelbridge.RefinedTSKernel, a, b refinementsets.RefinedSet) (disjoint bool, ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	return kernel.ScalarDisjoint(a, b), true
}

func int32Window() refinementsets.RefinedSet {
	return refinementsets.MakeRefinedSet(
		refinementsets.AtLeast(-(math.Pow(2, 31))),
		refinementsets.AtMost(math.Pow(2, 31)-1),
		refinementsets.Integer,
	)
}

// transferImul is transferImul in the TS source: Math.imul =
// ToInt32(ToUint32(a)·ToUint32(b) mod 2³²), and the residues mod 2³²
// of ToInt32 and ToUint32 agree — so imul is the COMPOSITION
// toInt32(toInt32(a) · toInt32(b)) of questions the kernel already
// answers (the int32 product ≤ 2⁶² is an exact dyadic). Total:
// whatever survives, the result is an int32.
func transferImul(rawA, rawB abstractdomain.AbstractValue) abstractdomain.AbstractValue {
	kernel := currentTransferKernel()
	if kernel == nil {
		return silence.Residue()
	}
	zeroIfNaN := func(k abstractdomain.AbstractValue) abstractdomain.AbstractValue {
		if k.Kind == abstractdomain.KindNaN {
			return abstractdomain.KnownValues([]float64{0}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
		}
		return k
	}
	a := zeroIfNaN(NumericOperand(rawA))
	b := zeroIfNaN(NumericOperand(rawB))
	A, aOk := SetOfKnownForTransfer(a)
	B, bOk := SetOfKnownForTransfer(b)
	if !aOk || !bOk {
		return silence.Residue()
	}
	answer, ok := callTransferForImul(kernel, A, B)
	if !ok {
		return abstractdomain.KnownSet(int32Window(), nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone)
	}
	return answer
}

func callTransferForImul(kernel *kernelbridge.RefinedTSKernel, A, B refinementsets.RefinedSet) (result abstractdomain.AbstractValue, ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	ia, iaOk := setOfAnswer(kernel.Transfer(kernelbridge.TransferQuestion{Op: kernelbridge.TransferOpToInt32, A: A}))
	ib, ibOk := setOfAnswer(kernel.Transfer(kernelbridge.TransferQuestion{Op: kernelbridge.TransferOpToInt32, A: B}))
	if !iaOk || !ibOk {
		return abstractdomain.AbstractValue{}, false
	}
	product, productOk := setOfAnswer(kernel.Transfer(kernelbridge.TransferQuestion{Op: kernelbridge.TransferOpMul, A: ia, B: ib}))
	if !productOk {
		return abstractdomain.AbstractValue{}, false
	}
	return KnownOfAnswer(kernel.Transfer(kernelbridge.TransferQuestion{Op: kernelbridge.TransferOpToInt32, A: product})), true
}

// setOfAnswer is the TS source's setOfAnswer: a transfer answer read
// back as a set (a single value is its singleton); (RefinedSet{},
// false) where the answer carries no set.
func setOfAnswer(answer kernelbridge.TransferAnswer) (refinementsets.RefinedSet, bool) {
	switch answer.Kind {
	case kernelbridge.TransferAnswerValues:
		if len(answer.Values) >= 1 {
			return refinementsets.MakeRefinedSet(refinementsets.OneOf(answer.Values)), true
		}
		return refinementsets.RefinedSet{}, false
	case kernelbridge.TransferAnswerSet:
		return answer.Set, true
	default:
		return refinementsets.RefinedSet{}, false
	}
}

// transferClz32 is transferClz32 in the TS source: Math.clz32 counts
// the leading zeros of ToUint32(x) — an exactly specified integer
// function: the host computes the exact row (the string-read oracle
// rule), and the window [0, 32] is total.
func transferClz32(raw abstractdomain.AbstractValue) abstractdomain.AbstractValue {
	only := NumericOperand(raw)
	if only.Kind == abstractdomain.KindNaN {
		return abstractdomain.KnownValues([]float64{32}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved) // ToUint32(NaN) = 0
	}
	if only.Kind == abstractdomain.KindValues && len(only.Values) == 1 &&
		only.KindTag != abstractdomain.PrimitiveString && only.KindTag != abstractdomain.PrimitiveArray {
		return abstractdomain.KnownValues([]float64{float64(clz32(only.Values[0]))}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
	}
	return abstractdomain.KnownSet(refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.AtMost(32), refinementsets.Integer), nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone)
}

// clz32 mirrors Math.clz32: leading zero count of ToUint32(x).
func clz32(x float64) int {
	u := toUint32(x)
	if u == 0 {
		return 32
	}
	n := 0
	for (u & 0x80000000) == 0 {
		u <<= 1
		n++
	}
	return n
}

// toUint32 mirrors the ECMA-262 ToUint32 abstract operation.
func toUint32(x float64) uint32 {
	if math.IsNaN(x) || math.IsInf(x, 0) || x == 0 {
		return 0
	}
	trunc := math.Trunc(x)
	mod := math.Mod(trunc, 4294967296)
	if mod < 0 {
		mod += 4294967296
	}
	return uint32(mod)
}

// TransferMathCall is transferMathCall in the TS source: the Math
// built-ins the checker reads. (AbstractValue{}, false) = not
// transferred (the caller reads the call as plain TypeScript); a
// REFUSED one is unknown (true, silence.Residue()).
func TransferMathCall(name string, rawArgs []abstractdomain.AbstractValue) (abstractdomain.AbstractValue, bool) {
	// a name this file does not transfer stays (AbstractValue{}, false)
	// (the caller reads the call as plain TypeScript); a REFUSED one is
	// unknown
	outside := false
	transferred := OrUnknown(func() abstractdomain.AbstractValue {
		answer, matched := mathImage(name, rawArgs)
		if !matched {
			outside = true
			return abstractdomain.AbstractValue{}
		}
		return answer
	})
	if outside {
		return abstractdomain.AbstractValue{}, false
	}
	return transferred, true
}

func mathImage(name string, rawArgs []abstractdomain.AbstractValue) (abstractdomain.AbstractValue, bool) {
	// the LEDGER floor for this call: the weakest operand, further
	// lowered to "spec" by any host-evaluated or TS-table row below
	operandTrustLevel := abstractdomain.DerivedTrustLevel(abstractdomain.TrustProved, rawArgs...)
	argAt := func(i int) abstractdomain.AbstractValue {
		if i < len(rawArgs) {
			return rawArgs[i]
		}
		return silence.Residue()
	}
	// the int32 functions read NaN as ToUint32's 0 — they dispatch
	// BEFORE the NaN-in gate below
	if name == "imul" {
		return abstractdomain.AtTrustLevel(transferImul(argAt(0), argAt(1)), operandTrustLevel), true
	}
	// Math.pow IS Number::exponentiate (sec-math.pow), so the `**`
	// lattice answers — its own NaN corners included (pow(NaN, 0) is
	// 1), so it too dispatches before the NaN gate
	if name == "pow" && len(rawArgs) == 2 {
		return abstractdomain.AtTrustLevel(TransferPow(rawArgs[0], rawArgs[1]), operandTrustLevel), true
	}
	if name == "clz32" {
		return abstractdomain.AtTrustLevel(
			transferClz32(argAt(0)),
			abstractdomain.MinTrustLevel(operandTrustLevel, abstractdomain.TrustSpec),
		), true
	}
	// Math.random is PINNED to the set [0, 1) — an unknowable value
	// with a certain home (a TS-transcribed spec row)
	if name == "random" {
		if len(rawArgs) == 0 {
			return abstractdomain.KnownSet(refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.Below(1)), nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone), true
		}
		return silence.Residue(), true
	}
	args := make([]abstractdomain.AbstractValue, len(rawArgs))
	for i, a := range rawArgs {
		args[i] = NumericOperand(a)
	}
	// hypot precedes the NaN gate: an infinite coordinate makes +∞
	// even beside a NaN (sec-math.hypot checks infinities first)
	if name == "hypot" {
		for _, a := range args {
			if a.Kind == abstractdomain.KindValues && a.KindTag == abstractdomain.PrimitiveNumber &&
				len(a.Values) == 1 && !isFinite(a.Values[0]) {
				return abstractdomain.KnownValues([]float64{math.Inf(1)}, abstractdomain.PrimitiveNumber, abstractdomain.MinTrustLevel(operandTrustLevel, abstractdomain.TrustSpec)), true
			}
		}
		for _, a := range args {
			if a.Kind == abstractdomain.KindNaN {
				return abstractdomain.AtTrustLevel(abstractdomain.NaNValue, operandTrustLevel), true
			}
		}
		// variadic: √(Σxᵢ²) folds pairwise — mathematically exact, the
		// windows compounding their widening soundly
		kernel := currentTransferKernel()
		if len(args) >= 2 && kernel != nil {
			held, heldOk := SetOfKnownForTransfer(args[0])
			for i := 1; heldOk && i < len(args); i++ {
				B, bOk := SetOfKnownForTransfer(args[i])
				if !bOk {
					heldOk = false
					break
				}
				image := kernel.Transfer(kernelbridge.TransferQuestion{Op: kernelbridge.TransferOpHypot, A: held, B: B})
				if image.Kind == kernelbridge.TransferAnswerSet {
					held = image.Set
				} else {
					heldOk = false
				}
			}
			if heldOk {
				// the image never goes negative — hypot is the square root
				// of a sum of squares (vendored spec, sec-math.hypot), so
				// the window's floor is 0 whatever slack the fold carried
				forms := append(append([]refinementsets.Refinement{}, held.Forms...), refinementsets.AtLeast(0))
				return abstractdomain.AtTrustLevel(
					abstractdomain.KnownSet(refinementsets.MakeRefinedSet(forms...), nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone),
					abstractdomain.TrustSpec,
				), true
			}
		}
		return silence.Residue(), true
	}
	// atan2(y, x) in the right half-plane through the kernel window
	if name == "atan2" && len(args) == 2 {
		for _, a := range args {
			if a.Kind == abstractdomain.KindNaN {
				return abstractdomain.AtTrustLevel(abstractdomain.NaNValue, operandTrustLevel), true
			}
		}
		kernel := currentTransferKernel()
		if kernel != nil {
			A, aOk := SetOfKnownForTransfer(args[0])
			B, bOk := SetOfKnownForTransfer(args[1])
			if aOk && bOk {
				return abstractdomain.AtTrustLevel(
					KnownOfAnswer(kernel.Transfer(kernelbridge.TransferQuestion{Op: kernelbridge.TransferOpAtan2, A: A, B: B})),
					abstractdomain.TrustSpec,
				), true
			}
		}
		return silence.Residue(), true
	}
	// every transferred built-in below is specified NaN-in, NaN-out
	for _, a := range args {
		if a.Kind == abstractdomain.KindNaN {
			return abstractdomain.AtTrustLevel(abstractdomain.NaNValue, operandTrustLevel), true
		}
	}
	if unary, matched := UnaryMathImage(name, args, operandTrustLevel); matched {
		return unary, true
	}
	switch name {
	case "min", "max":
		kernel := currentTransferKernel()
		if len(args) == 0 || kernel == nil {
			return silence.Residue(), true
		}
		// all-exact operands: the spec's own min/max on the values
		// (NaN handled by the gate above; -0 by the host's spec rows)
		allExact := true
		for _, a := range args {
			if !(a.Kind == abstractdomain.KindValues && len(a.Values) == 1 &&
				a.KindTag != abstractdomain.PrimitiveString && a.KindTag != abstractdomain.PrimitiveArray) {
				allExact = false
				break
			}
		}
		if allExact {
			values := make([]float64, len(args))
			for i, a := range args {
				values[i] = a.Values[0]
			}
			var result float64
			if name == "min" {
				result = values[0]
				for _, v := range values[1:] {
					result = math.Min(result, v)
				}
			} else {
				result = values[0]
				for _, v := range values[1:] {
					result = math.Max(result, v)
				}
			}
			return abstractdomain.KnownValues(
				[]float64{result},
				abstractdomain.PrimitiveNumber,
				abstractdomain.MinTrustLevel(operandTrustLevel, abstractdomain.TrustSpec), // the host row
			), true
		}
		op := kernelbridge.TransferOpMax
		if name == "min" {
			op = kernelbridge.TransferOpMin
		}
		held, heldOk := SetOfKnownForTransfer(args[0])
		if !heldOk {
			return silence.Residue(), true
		}
		// a single argument still collapses to its range, as the old
		// fold did — posed as a pick against itself
		if len(args) == 1 {
			return abstractdomain.AtTrustLevel(
				KnownOfAnswer(kernel.Transfer(kernelbridge.TransferQuestion{Op: op, A: held, B: held})),
				operandTrustLevel,
			), true
		}
		result := silence.Residue()
		for i := 1; i < len(args); i++ {
			next, nextOk := SetOfKnownForTransfer(args[i])
			if !nextOk {
				return silence.Residue(), true
			}
			answer := kernel.Transfer(kernelbridge.TransferQuestion{Op: op, A: held, B: next})
			if answer.Kind != kernelbridge.TransferAnswerSet {
				return abstractdomain.AtTrustLevel(KnownOfAnswer(answer), operandTrustLevel), true
			}
			held = answer.Set
			result = abstractdomain.KnownSet(answer.Set, nil, operandTrustLevel, abstractdomain.SetKindTagNone)
		}
		return result, true
	case "sign":
		var arg0 abstractdomain.AbstractValue
		hasArg0 := len(args) > 0
		if hasArg0 {
			arg0 = args[0]
		}
		return abstractdomain.AtTrustLevel(transferSign(arg0, hasArg0), operandTrustLevel), true
	case "fround":
		// exactly specified: roundTiesToEven into binary32 and back —
		// deterministic, so the host computes the exact row (the
		// string-read oracle rule)
		if len(args) > 0 {
			only := args[0]
			if only.Kind == abstractdomain.KindValues && len(only.Values) == 1 {
				return abstractdomain.KnownValues(
					[]float64{float64(float32(only.Values[0]))},
					abstractdomain.PrimitiveNumber,
					abstractdomain.MinTrustLevel(operandTrustLevel, abstractdomain.TrustSpec),
				), true
			}
			// a WINDOW maps through the same conversion: correct rounding
			// is monotone, so the image of [lo, hi] is exactly
			// [fround(lo), fround(hi)] — both endpoints the host's own
			// rows (sec-math.fround)
			window := RangeOfKnown(only)
			if window != nil && isFinite(window.Lo) && isFinite(window.Hi) {
				return abstractdomain.KnownSet(
					refinementsets.MakeRefinedSet(
						refinementsets.AtLeast(float64(float32(window.Lo))),
						refinementsets.AtMost(float64(float32(window.Hi))),
					),
					nil,
					abstractdomain.MinTrustLevel(operandTrustLevel, abstractdomain.TrustSpec),
					abstractdomain.SetKindTagNone,
				), true
			}
		}
		return silence.Residue(), true
	default:
		// corner tables are TS-transcribed spec rows
		var arg0 abstractdomain.AbstractValue
		hasArg0 := len(args) > 0
		if hasArg0 {
			arg0 = args[0]
		}
		approximated, matched := TransferApproximated(name, arg0, hasArg0)
		if !matched {
			return abstractdomain.AbstractValue{}, false
		}
		return abstractdomain.AtTrustLevel(approximated, abstractdomain.MinTrustLevel(operandTrustLevel, abstractdomain.TrustSpec)), true
	}
}
